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

const (
	hookErrorCode       = "m16.microvm.lifecycle_hook_failed"
	hookErrorIncomplete = "m16.microvm.lifecycle_event_incomplete"
)

// hookServer serves the AppTheory M16 MicroVM image lifecycle hooks on the
// configured port. Each hook path receives a sanitized microvm.LifecycleEvent
// JSON body and drives it through AppTheory's real lifecycle adapter
// (DefaultRealLifecycleContract + NewLifecycleAdapter). The run hook
// additionally executes the assistant turn + declaration extraction and durably
// records completion to HostedGenesisSession truth.
//
// The server is fail-closed: unknown hooks, malformed events, unsupported
// transitions, and execution failures surface as a failed LifecycleResult with a
// typed SafeError envelope. There is no degraded/non-MicroVM fallback path.
type hookServer struct {
	adapter   *runtimemicrovm.LifecycleAdapter
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

// newHookServer builds the workload's hook dispatcher over the framework's M16
// real lifecycle adapter. It fails closed at startup if the contract or handler
// set is invalid. Handlers are the workload's per-hook behavior; the run handler
// executes the assistant turn + declaration extraction.
func newHookServer(runner *turnRunner, namespace string) (*hookServer, error) {
	contract := runtimemicrovm.DefaultRealLifecycleContract()
	adapter, err := runtimemicrovm.NewLifecycleAdapter(
		runtimemicrovm.WithLifecycleContract(contract),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookValidate, namespaceBoundHook(namespace, validateHook)),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookRun, namespaceBoundHook(namespace, runHook(runner))),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookReady, namespaceBoundHook(namespace, readyHook)),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookSuspend, namespaceBoundHook(namespace, passthroughHook)),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookResume, namespaceBoundHook(namespace, passthroughHook)),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookTerminate, namespaceBoundHook(namespace, passthroughHook)),
		runtimemicrovm.WithLifecycleHandler(runtimemicrovm.HookFailure, namespaceBoundHook(namespace, failureHook)),
	)
	if err != nil {
		return nil, fmt.Errorf("configure M16 real lifecycle adapter: %w", err)
	}
	return &hookServer{adapter: adapter, runner: runner, namespace: namespace}, nil
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
// AppTheory's M16 real lifecycle adapter. The adapter normalizes the event,
// validates the transition, invokes the workload's hook handler, and returns the
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

// drive applies one lifecycle event through AppTheory's M16 real lifecycle
// adapter. There is no local transition engine: validation, transition lookup,
// handler invocation, and safe-error translation stay inside the framework
// adapter.
func (s *hookServer) drive(ctx context.Context, event runtimemicrovm.LifecycleEvent) runtimemicrovm.LifecycleResult {
	if s == nil || s.adapter == nil {
		return lifecycleFailedResult(event, event.Hook, errors.New("lifecycle adapter is not configured"))
	}
	result, err := s.adapter.Handle(ctx, event)
	if err != nil {
		slog.Error("hosted-genesis-microvm-workload: lifecycle hook failed", //nolint:gosec // G706: values are structured slog attributes (JSON-encoded key/values), not a log format string; no format-string injection surface.
			slog.String("hook", string(event.Hook)),
			slog.String("tenant_id", strings.TrimSpace(event.TenantID)),
			slog.String("session_id", strings.TrimSpace(event.SessionID)),
			slog.String("request_id", strings.TrimSpace(event.RequestID)),
			slog.String("error", err.Error()),
		)
	}
	return result
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

func decodeLifecycleEvent(r *http.Request) (runtimemicrovm.LifecycleEvent, error) {
	var event runtimemicrovm.LifecycleEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		return runtimemicrovm.LifecycleEvent{}, fmt.Errorf("decode lifecycle event: %w", err)
	}
	return event, nil
}

func namespaceBoundHook(namespace string, handler runtimemicrovm.LifecycleHandler) runtimemicrovm.LifecycleHandler {
	return func(ctx context.Context, event runtimemicrovm.LifecycleEvent) error {
		if namespace != "" && strings.TrimSpace(event.Namespace) != namespace {
			return fmt.Errorf("lifecycle event namespace %q is not %s", event.Namespace, namespace)
		}
		if handler == nil {
			return nil
		}
		return handler(ctx, event)
	}
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
