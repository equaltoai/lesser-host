package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
)

// hookPort is the port the AWS Lambda MicrovmImage invokes the workload's
// lifecycle hooks on. It matches the AppTheoryMicrovmImage hooks.port CDK
// configuration in cdk/lib/hosted-genesis-microvm.ts.
const hookPort = "8080"

// Framework-feedback workaround (AppTheory v1.15.0): NewLifecycleAdapter
// validates only with the M15 ValidateLifecycleContract, so it cannot ingest
// DefaultRealLifecycleContract() (the documented M16 canonical vocabulary).
// Until a real-contract-aware adapter constructor lands upstream, this workload
// consumes the framework's EXPORTED M16 vocabulary directly: it validates the
// contract with ValidateRealLifecycleContract at startup and derives each hook's
// LifecycleResult from the framework's own DefaultRealLifecycleContract()
// transition table. This is not a local substitute engine — no fork, no patched
// adapter, no reimplemented sanitization. See memory mem-8365ca36ad0fd5f6.
const (
	hookErrorCode       = "m16.microvm.lifecycle_hook_failed"
	hookErrorIncomplete = "m16.microvm.lifecycle_event_incomplete"
)

// hookServer serves the AppTheory M16 MicroVM image lifecycle hooks on the
// configured port. Each hook path receives a sanitized microvm.LifecycleEvent
// JSON body, validates it against the framework's M16 real lifecycle contract,
// and returns the framework-shaped LifecycleResult. The run hook additionally
// executes the assistant turn + declaration extraction and durably records
// completion to HostedGenesisSession truth.
//
// The server is fail-closed: unknown hooks, malformed events, unsupported
// transitions, and execution failures surface as a failed LifecycleResult with a
// typed SafeError envelope. There is no degraded/non-MicroVM fallback path.
type hookServer struct {
	contract  runtimemicrovm.LifecycleContract
	handlers  map[runtimemicrovm.LifecycleHook]runtimemicrovm.LifecycleHandler
	runner    *turnRunner
	namespace string
}

// hookBinding extracts the HostedGenesisSession completion-turn identifiers from
// a sanitized MicroVM lifecycle event's metadata. Host's controller records
// source_of_truth, registration_id, agent_id, conversation_id, and turn_id in
// the session spec metadata; the image surfaces them back on each hook event.
type hookBinding struct {
	instanceSlug   string
	conversationID string
	turnID         string
	requestID      string
}

// fromEvent resolves the completion-turn binding from a lifecycle event. The
// instance slug is the tenant boundary (slug:<slug>); metadata carries the rest.
func (hookBinding) fromEvent(event runtimemicrovm.LifecycleEvent) (hookBinding, error) {
	b := hookBinding{
		requestID: strings.TrimSpace(event.RequestID),
	}
	tenantID := strings.TrimSpace(event.TenantID)
	if !strings.HasPrefix(tenantID, "slug:") {
		return hookBinding{}, fmt.Errorf("lifecycle event tenant_id %q is not a slug boundary", tenantID)
	}
	b.instanceSlug = strings.TrimSpace(strings.TrimPrefix(tenantID, "slug:"))
	b.conversationID = strings.TrimSpace(event.Metadata["conversation_id"])
	b.turnID = strings.TrimSpace(event.Metadata["turn_id"])
	if b.instanceSlug == "" || b.conversationID == "" || b.turnID == "" {
		return hookBinding{}, errors.New("lifecycle event metadata is missing hosted-genesis ids")
	}
	return b, nil
}

func (b hookBinding) completionTurn() completion.CompletionTurn {
	return completion.CompletionTurn{
		InstanceSlug:   b.instanceSlug,
		ConversationID: b.conversationID,
		TurnID:         b.turnID,
		RequestID:      b.requestID,
	}
}

// newHookServer builds the workload's hook dispatcher over the framework's
// exported M16 real lifecycle contract. It fails closed at startup if the
// contract is invalid. Handlers are the workload's per-hook behavior; the run
// handler executes the assistant turn + declaration extraction.
func newHookServer(runner *turnRunner, namespace string) (*hookServer, error) {
	contract := runtimemicrovm.DefaultRealLifecycleContract()
	if err := runtimemicrovm.ValidateRealLifecycleContract(contract); err != nil {
		return nil, fmt.Errorf("validate M16 real lifecycle contract: %w", err)
	}
	handlers := map[runtimemicrovm.LifecycleHook]runtimemicrovm.LifecycleHandler{
		runtimemicrovm.HookValidate:  validateHook,
		runtimemicrovm.HookRun:       runHook(runner),
		runtimemicrovm.HookReady:     readyHook,
		runtimemicrovm.HookSuspend:   passthroughHook,
		runtimemicrovm.HookResume:    passthroughHook,
		runtimemicrovm.HookTerminate: passthroughHook,
		runtimemicrovm.HookFailure:   failureHook,
	}
	return &hookServer{contract: contract, handlers: handlers, runner: runner, namespace: namespace}, nil
}

// routes returns the HTTP handler that dispatches lifecycle hooks.
func (s *hookServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/validate", s.handleHook(runtimemicrovm.HookValidate))
	mux.HandleFunc("/run", s.handleRunHook)
	mux.HandleFunc("/ready", s.handleHook(runtimemicrovm.HookReady))
	mux.HandleFunc("/suspend", s.handleHook(runtimemicrovm.HookSuspend))
	mux.HandleFunc("/resume", s.handleHook(runtimemicrovm.HookResume))
	mux.HandleFunc("/terminate", s.handleHook(runtimemicrovm.HookTerminate))
	mux.HandleFunc("/failure", s.handleHook(runtimemicrovm.HookFailure))
	mux.HandleFunc("/healthz", s.handleHealth)
	return mux
}

func (s *hookServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleHook returns a handler that drives one non-run lifecycle hook through
// the framework's M16 contract vocabulary. It normalizes the event, validates
// the transition, invokes the workload's hook handler, and returns the
// framework-shaped LifecycleResult (success state, or failed with a SafeError).
func (s *hookServer) handleHook(hook runtimemicrovm.LifecycleHook) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		event, err := decodeLifecycleEvent(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, incompleteEventError("", err))
			return
		}
		event.Hook = hook
		writeJSON(w, http.StatusOK, s.drive(r.Context(), event))
	}
}

// handleRunHook drives the run lifecycle hook. The contract records the running
// state; the workload's run handler then executes the assistant turn +
// declaration extraction and durably records completion. A workload execution
// failure is recorded as a typed completion failure and surfaced via a failed
// lifecycle result so the controller observes a failed session.
func (s *hookServer) handleRunHook(w http.ResponseWriter, r *http.Request) {
	event, err := decodeLifecycleEvent(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, incompleteEventError("", err))
		return
	}
	event.Hook = runtimemicrovm.HookRun
	writeJSON(w, http.StatusOK, s.drive(r.Context(), event))
}

// drive applies one lifecycle event through the framework's exported M16
// contract vocabulary. It mirrors the LifecycleAdapter.Handle envelope (event
// normalization, transition lookup, handler invocation, safe-error translation
// to failed state) but uses DefaultRealLifecycleContract()'s transition table
// directly because NewLifecycleAdapter cannot ingest the real contract
// (framework gap; see memory mem-8365ca36ad0fd5f6).
func (s *hookServer) drive(ctx context.Context, event runtimemicrovm.LifecycleEvent) runtimemicrovm.LifecycleResult {
	normalized, err := normalizeLifecycleEvent(event, s.namespace)
	if err != nil {
		return lifecycleFailedResult(event, event.Hook, err)
	}
	spec, ok := hookSpec(s.contract, normalized.Hook)
	if !ok {
		return lifecycleFailedResult(normalized, normalized.Hook, errors.New("lifecycle hook is unsupported"))
	}
	activeState, ok := nextTransitionState(s.contract, normalized.State, normalized.Hook)
	if !ok {
		return lifecycleFailedResult(normalized, normalized.Hook, errors.New("lifecycle transition is unsupported"))
	}
	if normalized.Hook != runtimemicrovm.HookFailure && activeState != spec.State {
		return lifecycleFailedResult(normalized, normalized.Hook, errors.New("lifecycle transition is not the hook active state"))
	}
	handler := s.handlers[normalized.Hook]
	if handler == nil {
		return lifecycleFailedResult(normalized, normalized.Hook, errors.New("lifecycle hook handler is missing"))
	}
	handlerEvent := normalized
	handlerEvent.State = activeState
	if err := handler(ctx, handlerEvent); err != nil {
		slog.Error("hosted-genesis-microvm-workload: lifecycle hook failed", //nolint:gosec // G706: values are structured slog attributes (JSON-encoded key/values), not a log format string; no format-string injection surface.
			slog.String("hook", string(normalized.Hook)),
			slog.String("tenant_id", normalized.TenantID),
			slog.String("session_id", normalized.SessionID),
			slog.String("request_id", normalized.RequestID),
			slog.String("error", err.Error()),
		)
		return lifecycleFailedResult(normalized, normalized.Hook, fmt.Errorf("lifecycle hook failed: %w", err))
	}
	state := spec.SuccessState
	if normalized.Hook == runtimemicrovm.HookFailure {
		state = runtimemicrovm.StateFailed
	} else if !hasTransition(s.contract, activeState, normalized.Hook, state) {
		return lifecycleFailedResult(normalized, normalized.Hook, errors.New("lifecycle success transition is unsupported"))
	}
	return runtimemicrovm.LifecycleResult{
		RequestID:     normalized.RequestID,
		TenantID:      normalized.TenantID,
		Namespace:     normalized.Namespace,
		SessionID:     normalized.SessionID,
		Hook:          normalized.Hook,
		PreviousState: normalized.State,
		State:         state,
		Metadata:      cloneStringMap(normalized.Metadata),
	}
}

// validateHook is the image-build validation hook. It fails closed if the
// lifecycle event is not bound to the hosted-genesis namespace.
func validateHook(_ context.Context, event runtimemicrovm.LifecycleEvent) error {
	if strings.TrimSpace(event.Namespace) != hostedgenesis.MicroVMNamespace {
		return fmt.Errorf("lifecycle event namespace %q is not %s", event.Namespace, hostedgenesis.MicroVMNamespace)
	}
	return nil
}

// runHook returns the run lifecycle handler that executes the assistant turn +
// declaration extraction and durably records completion.
func runHook(runner *turnRunner) runtimemicrovm.LifecycleHandler {
	return func(ctx context.Context, event runtimemicrovm.LifecycleEvent) error {
		if runner == nil {
			return errors.New("workload runner is not configured")
		}
		binding, err := hookBinding{}.fromEvent(event)
		if err != nil {
			return err
		}
		return runner.runTurnAndPersist(ctx, binding.completionTurn())
	}
}

// readyHook records a readiness observation without widening the state model.
func readyHook(_ context.Context, event runtimemicrovm.LifecycleEvent) error {
	if strings.TrimSpace(event.Namespace) != hostedgenesis.MicroVMNamespace {
		return fmt.Errorf("lifecycle event namespace %q is not %s", event.Namespace, hostedgenesis.MicroVMNamespace)
	}
	return nil
}

// passthroughHook acknowledges suspend/resume/terminate observations. Host truth
// is owned by HostedGenesisSession; these hooks do not mutate business state.
func passthroughHook(_ context.Context, _ runtimemicrovm.LifecycleEvent) error {
	return nil
}

// failureHook records a terminal failure observation.
func failureHook(_ context.Context, _ runtimemicrovm.LifecycleEvent) error {
	return nil
}

// normalizeLifecycleEvent trims and validates a lifecycle event envelope against
// the hosted-genesis namespace binding. It mirrors the framework's unexported
// normalizeLifecycleEvent shape using only exported contract vocabulary.
func normalizeLifecycleEvent(event runtimemicrovm.LifecycleEvent, namespace string) (runtimemicrovm.LifecycleEvent, error) {
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.TenantID = strings.TrimSpace(event.TenantID)
	event.Namespace = strings.TrimSpace(event.Namespace)
	event.SessionID = strings.TrimSpace(event.SessionID)
	event.Hook = runtimemicrovm.LifecycleHook(strings.TrimSpace(string(event.Hook)))
	event.State = runtimemicrovm.LifecycleState(strings.TrimSpace(string(event.State)))
	event.Metadata = cloneStringMap(event.Metadata)
	if event.RequestID == "" || event.TenantID == "" || event.Namespace == "" || event.SessionID == "" {
		return runtimemicrovm.LifecycleEvent{}, errors.New("lifecycle envelope is incomplete")
	}
	if event.Hook == "" || event.State == "" {
		return runtimemicrovm.LifecycleEvent{}, errors.New("lifecycle hook and state are required")
	}
	if namespace != "" && event.Namespace != namespace {
		return runtimemicrovm.LifecycleEvent{}, fmt.Errorf("lifecycle event namespace %q is not %s", event.Namespace, namespace)
	}
	return event, nil
}

func hookSpec(contract runtimemicrovm.LifecycleContract, hook runtimemicrovm.LifecycleHook) (runtimemicrovm.LifecycleHookSpec, bool) {
	for _, h := range contract.Hooks {
		if runtimemicrovm.LifecycleHook(strings.TrimSpace(string(h.Name))) == hook {
			return h, true
		}
	}
	return runtimemicrovm.LifecycleHookSpec{}, false
}

func nextTransitionState(contract runtimemicrovm.LifecycleContract, from runtimemicrovm.LifecycleState, hook runtimemicrovm.LifecycleHook) (runtimemicrovm.LifecycleState, bool) {
	for _, t := range contract.Transitions {
		if t.From == from && t.Hook == hook {
			return t.To, true
		}
	}
	return "", false
}

func hasTransition(contract runtimemicrovm.LifecycleContract, from runtimemicrovm.LifecycleState, hook runtimemicrovm.LifecycleHook, to runtimemicrovm.LifecycleState) bool {
	for _, t := range contract.Transitions {
		if t.From == from && t.Hook == hook && t.To == to {
			return true
		}
	}
	return false
}

func decodeLifecycleEvent(r *http.Request) (runtimemicrovm.LifecycleEvent, error) {
	var event runtimemicrovm.LifecycleEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		return runtimemicrovm.LifecycleEvent{}, fmt.Errorf("decode lifecycle event: %w", err)
	}
	return event, nil
}

func lifecycleFailedResult(event runtimemicrovm.LifecycleEvent, hook runtimemicrovm.LifecycleHook, err error) runtimemicrovm.LifecycleResult {
	safe := runtimemicrovm.SafeError{
		Code:      hookErrorCode,
		Message:   err.Error(),
		RequestID: strings.TrimSpace(event.RequestID),
	}
	return runtimemicrovm.LifecycleResult{
		RequestID:     strings.TrimSpace(event.RequestID),
		TenantID:      strings.TrimSpace(event.TenantID),
		Namespace:     strings.TrimSpace(event.Namespace),
		SessionID:     strings.TrimSpace(event.SessionID),
		Hook:          hook,
		PreviousState: event.State,
		State:         runtimemicrovm.StateFailed,
		Error:         &safe,
	}
}

func incompleteEventError(requestID string, err error) runtimemicrovm.LifecycleResult {
	return lifecycleFailedResult(runtimemicrovm.LifecycleEvent{RequestID: requestID}, "", err)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// httpServer wires a real *http.Server with the configured hook port.
func (s *hookServer) httpServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
}
