package hostedgenesis

import (
	"context"
	"errors"
	"strings"
	"time"
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

// MicroVMDispatcher is the control-plane dispatch boundary for the hosted
// genesis MicroVM execution path. The accept path calls DispatchMicroVMRun
// after the HostedGenesisSession accepted turn is durably committed; the
// dispatcher invokes the AppTheory M16 controller run command through the
// constrained provider adapter (no raw AWS SDK) and returns the validated
// lifecycle ref Host records as non-authoritative execution/cache state.
//
// A nil/unwired dispatcher is fail-closed: the accept path must not fall back
// to a synchronous in-request LLM call. Transport selection (in-process
// controller runtime versus the lab-only controller Lambda HTTP route) is
// encapsulated behind this seam; the control plane never handles raw provider
// SDK clients, bearer tokens, or lifecycle hook payloads.
type MicroVMDispatcher interface {
	DispatchMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error)
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
