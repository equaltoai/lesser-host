package hostedgenesis

import (
	"context"
	"errors"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

// ErrMicroVMDispatchUnavailable is the fail-closed error returned when the
// hosted-genesis accept path cannot dispatch a MicroVM controller run command.
// It is never a license to fall back to a synchronous control-plane LLM call;
// callers must surface it loudly (typed AppError) and persist a failed turn.
var ErrMicroVMDispatchUnavailable = errors.New("hosted genesis microvm dispatch is unavailable")

// MicroVMDispatchResult is the safe, non-secret outcome of a controller run
// dispatch: the validated execution/cache lifecycle ref Host records on the
// HostedGenesisSession, plus the AppTheory session id the controller allocated.
// It deliberately excludes endpoint auth tokens, bearer tokens, raw lifecycle
// payloads, transcripts, and provider credentials.
type MicroVMDispatchResult struct {
	LifecycleRef MicroVMLifecycleRef
	SessionID    string
}

// MicroVMReconcileResult is the safe, non-secret outcome of a controller get
// dispatch: the AppTheory session id queried and the reconciled execution/cache
// lifecycle ref reflecting real VM state observed by the controller. Like
// MicroVMDispatchResult it excludes endpoint auth tokens, bearer tokens, raw
// lifecycle payloads, transcripts, and provider credentials.
//
// Terminal reports whether the observed lifecycle state is a terminal MicroVM
// state (terminated/failed) so the control plane can map a dead/expired VM to a
// loud failure instead of silently no-oping reconstruction.
type MicroVMReconcileResult struct {
	LifecycleRef MicroVMLifecycleRef
	SessionID    string
	Terminal     bool
}

// MicroVMDispatcher is the control-plane dispatch boundary for the hosted
// genesis MicroVM execution path. The accept path calls DispatchMicroVMRun
// after the HostedGenesisSession accepted turn is durably committed; the
// dispatcher invokes the AppTheory M16 controller run command through the
// constrained provider adapter (no raw AWS SDK) and returns the validated
// lifecycle ref Host records as non-authoritative execution/cache state.
//
// ReconcileMicroVM issues the M16 controller get command for an existing
// execution/cache ref so the control plane can observe real VM state and
// reconcile the HostedGenesisSession. It is the production reconstruction
// reachability site: the controller get drives the AppTheory reconstructing
// session registry (Host's reconstruction hook fires on cache miss) and the
// provider Get (real VM state). A dead/expired VM is reported as Terminal with
// the reconciled ref; callers must map terminal state to a loud failure, never
// a silent no-op.
//
// A nil/unwired dispatcher is fail-closed: the accept path must not fall back
// to a synchronous in-request LLM call, and the reconcile path must not treat
// an unwired dispatcher as a successful no-op reconstruction. Transport
// selection (in-process controller runtime versus the lab-only controller
// Lambda HTTP route) is encapsulated behind this seam; the control plane never
// handles raw provider SDK clients, bearer tokens, or lifecycle hook payloads.
type MicroVMDispatcher interface {
	DispatchMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error)
	ReconcileMicroVM(ctx context.Context, requestID string, binding MicroVMSessionBinding, ref MicroVMLifecycleRef) (MicroVMReconcileResult, error)
}

// ControllerRuntimeDispatcher adapts an AppTheory M16 MicroVMControllerRuntime
// into the MicroVMDispatcher contract. It issues a run command for the already
// committed Host session binding and converts the safe controller envelope into
// Host's compact, validated lifecycle ref.
type ControllerRuntimeDispatcher struct {
	runtime *MicroVMControllerRuntime
}

// NewControllerRuntimeDispatcher wraps a MicroVMControllerRuntime so the control
// plane can dispatch controller run commands through the MicroVMDispatcher seam.
// A nil runtime yields a fail-closed dispatcher.
func NewControllerRuntimeDispatcher(runtime *MicroVMControllerRuntime) *ControllerRuntimeDispatcher {
	return &ControllerRuntimeDispatcher{runtime: runtime}
}

// DispatchMicroVMRun issues the AppTheory M16 run command for the binding and
// records the validated lifecycle ref. It fails closed when the runtime is nil
// or the controller rejects the request; it never falls back to a local
// execution path.
func (d *ControllerRuntimeDispatcher) DispatchMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error) {
	if d == nil || d.runtime == nil {
		return MicroVMDispatchResult{}, ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return MicroVMDispatchResult{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return MicroVMDispatchResult{}, ErrMicroVMDispatchUnavailable
	}
	resp, err := d.runtime.Run(ctx, requestID, binding)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMDispatchResult{}, resp.Error
	}
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, resp, observedAt)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	return MicroVMDispatchResult{LifecycleRef: ref, SessionID: strings.TrimSpace(resp.SessionID)}, nil
}

// ReconcileMicroVM issues the AppTheory M16 get command for the binding's
// existing execution/cache session and returns the reconciled lifecycle ref
// reflecting real VM state. It fails closed when the runtime is nil, the
// controller rejects the request, or the observed state no longer maps to the
// Host session binding (stale/tenant mismatch). It never falls back to a local
// execution path and never swallows a dead/expired VM as a silent no-op:
// terminal observed state is reported via Terminal=true so the caller maps it
// to a loud failure.
//
// H1.4: a session is Terminal when EITHER the observed lifecycle state is a
// terminal MicroVM state (terminated/failed) OR the controller-reported
// session expiry has passed (ExpiresAt is set and in the past). An expired
// session is dead even when its lifecycle state is non-terminal (e.g. stopped):
// the VM can no longer service the pending turn, so the recover path must map
// it to a loud retryable failure rather than preserve a pending status that can
// never advance. The reconciled lifecycle ref still reflects the observed
// non-terminal state; only the Terminal flag forces the loud-failed mapping.
func (d *ControllerRuntimeDispatcher) ReconcileMicroVM(ctx context.Context, requestID string, binding MicroVMSessionBinding, ref MicroVMLifecycleRef) (MicroVMReconcileResult, error) {
	if d == nil || d.runtime == nil {
		return MicroVMReconcileResult{}, ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return MicroVMReconcileResult{}, err
	}
	if err := ref.Validate(binding); err != nil {
		return MicroVMReconcileResult{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return MicroVMReconcileResult{}, ErrMicroVMDispatchUnavailable
	}
	resp, err := d.runtime.Command(ctx, runtimemicrovm.CommandGet, requestID, binding)
	if err != nil {
		return MicroVMReconcileResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMReconcileResult{}, resp.Error
	}
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	observedState := firstNonEmptyLifecycleState(resp.LifecycleState, resp.State)
	status := runtimemicrovm.SessionStatus{
		TenantID:        strings.TrimSpace(resp.TenantID),
		Namespace:       strings.TrimSpace(resp.Namespace),
		SessionID:       strings.TrimSpace(resp.SessionID),
		State:           observedState,
		DesiredState:    firstNonEmptyLifecycleState(resp.DesiredState, observedState),
		LifecycleState:  observedState,
		MicroVMID:       firstNonEmptyString(resp.ProviderMicroVMID, resp.MicroVMID),
		LastAction:      resp.LastAction,
		LastTransition:  firstNonZeroTimeValue(resp.LastTransition, observedAt),
		RegistryVersion: resp.RegistryVersion,
	}
	reconciled, err := ReconcileMicroVMRegistryStatus(binding, ref, status)
	if err != nil {
		return MicroVMReconcileResult{}, err
	}
	return MicroVMReconcileResult{
		LifecycleRef: reconciled,
		SessionID:    strings.TrimSpace(resp.SessionID),
		Terminal:     microVMReconcileIsTerminal(reconciled.LifecycleState, resp.ExpiresAt, observedAt),
	}, nil
}

// microVMReconcileIsTerminal reports whether a reconciled MicroVM session is
// dead/expired and therefore must map to a loud retryable recovery failure. A
// session is terminal when its observed lifecycle state is a terminal MicroVM
// state (terminated/failed) OR its controller-reported expiry has passed. A zero
// ExpiresAt (the controller did not report one) is not treated as expired; only
// a set, past expiry forces the terminal mapping. observedAt is the controller
// get observation time used to judge expiry.
func microVMReconcileIsTerminal(state runtimemicrovm.LifecycleState, expiresAt time.Time, observedAt time.Time) bool {
	if runtimemicrovm.IsTerminalState(state) {
		return true
	}
	if expiresAt.IsZero() {
		return false
	}
	return expiresAt.Before(observedAt) || expiresAt.Equal(observedAt)
}
