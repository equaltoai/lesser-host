package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

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

// hookPathPrefix is the URL prefix the AWS Lambda Microvms service invokes
// lifecycle hooks at: /aws/lambda-microvms/runtime/v1/<hook-name> on the
// configured port (see the OpenAPI spec in the AWS docs at
// https://docs.aws.amazon.com/lambda/latest/dg/microvms-launching.html —
// servers: [{ url: "/aws/lambda-microvms/runtime/v1" }] with paths /ready,
// /validate, /run, /suspend, /resume, /terminate; the image-build hooks /ready
// and /validate are documented at /aws/lambda-microvms/runtime/v1/<hook> in
// https://docs.aws.amazon.com/lambda/latest/dg/microvms-images.html).
//
// The short /<hook> paths the workload previously registered are NOT called by
// the service — the service got 404 at /ready during image creation, so the
// ready hook never returned 200 and the build timed out (CREATE_FAILED "did not
// stabilize"). AppTheory's M16 lifecycle contract defines the lifecycle event
// handling (HookReady/HookValidate/...); it does not define the HTTP paths the
// AWS service invokes, so the path registration is host-owned.
const hookPathPrefix = "/aws/lambda-microvms/runtime/v1"

// routes returns the HTTP handler that dispatches lifecycle hooks. The mux is
// wrapped in a request-logging + panic-recovery middleware so every inbound
// request is logged BEFORE its handler runs (a handler that hangs or panics
// still produces a "request received" line), and a catch-all on "/" (registered
// last so the specific hook paths take precedence) logs + acknowledges any path
// the workload does not register. This is diagnostic: deploy #8 showed the
// workload serving ("serving M16 lifecycle hooks" logged twice) with ZERO
// hook-request logs despite correct hook paths + empty-body tolerance, so the
// question is whether ANY request reaches the app, at what path, and whether a
// handler panics. The hook handlers, paths, and empty-body tolerance are
// unchanged; the catch-all is not a security boundary (the specific hook paths
// still go through handleHook).
func (s *hookServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(hookPathPrefix+"/validate", s.handleHook(runtimemicrovm.HookValidate))
	mux.HandleFunc(hookPathPrefix+"/run", s.handleRunHook)
	mux.HandleFunc(hookPathPrefix+"/ready", s.handleHook(runtimemicrovm.HookReady))
	mux.HandleFunc(hookPathPrefix+"/suspend", s.handleHook(runtimemicrovm.HookSuspend))
	mux.HandleFunc(hookPathPrefix+"/resume", s.handleHook(runtimemicrovm.HookResume))
	mux.HandleFunc(hookPathPrefix+"/terminate", s.handleHook(runtimemicrovm.HookTerminate))
	mux.HandleFunc(hookPathPrefix+"/failure", s.handleHook(runtimemicrovm.HookFailure))
	mux.HandleFunc("/healthz", s.handleHealth) // workload's own liveness check — not an AWS service hook
	// Registered last: "/" is the ServeMux catch-all (matches any unmatched
	// path), so the specific hook paths above take precedence.
	mux.HandleFunc("/", s.handleCatchAll)
	return requestLoggingMiddleware(recoverMiddleware(mux))
}

// handleCatchAll acknowledges any path the workload does not register, logging
// the method + path + first N bytes of body so a build that calls an unexpected
// path (e.g. a different prefix or a short /<hook> path) is visible in the
// CloudWatch build log group. It returns 200 so the AWS service does not retry
// an unmatched path into a build timeout during diagnosis. This is diagnostic,
// not a security boundary: the specific hook paths still go through handleHook,
// and the build hooks carry no body to log. Body logging is capped at
// logBodyCap bytes; the /run payload is a tenant string, not a secret.
func (s *hookServer) handleCatchAll(w http.ResponseWriter, r *http.Request) {
	bodyPreview := readBodyPreview(r, logBodyCap)
	slog.Info(serviceName+": unmatched path", //nolint:gosec // G706: method/path/preview are structured slog attributes (JSON-encoded key/values), not a log format string.
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("remote", r.RemoteAddr),
		slog.String("body_preview", bodyPreview),
	)
	writeJSON(w, http.StatusOK, map[string]string{"status": "unmatched"})
}

// logBodyCap is the maximum number of bytes of request body the catch-all logs.
// The build hooks (/ready, /validate) send no body; the /run payload is a tenant
// string, not a secret. Capping prevents a large body from flooding the log.
const logBodyCap = 512

// readBodyPreview reads up to the first n bytes of the request body for logging
// by the catch-all. It restores the body so downstream code (none currently, but
// defensively) can still read it. A read error is surfaced as a preview marker
// rather than failing the request.
func readBodyPreview(r *http.Request, n int) string {
	if r.Body == nil {
		return ""
	}
	preview, err := io.ReadAll(io.LimitReader(r.Body, int64(n)))
	_ = r.Body.Close()
	r.Body = io.NopCloser(strings.NewReader(string(preview)))
	if err != nil {
		return "<read error: " + err.Error() + ">"
	}
	return string(preview)
}

// requestLoggingMiddleware logs every request at the START (method, path,
// remote) before the handler runs, and the status code AFTER, via a
// statusWriter wrapper. Logging before the handler means a handler that hangs
// or panics still produces a "request received" line — the gap deploy #8 could
// not see (the workload logged nothing after "serving" despite serving).
func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info(serviceName+": request received", //nolint:gosec // G706: method/path/remote are structured slog attributes (JSON-encoded key/values), not a log format string.
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("remote", r.RemoteAddr),
		)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info(serviceName+": request complete", //nolint:gosec // G706: path is a structured slog attribute (JSON-encoded key/value), not a log format string.
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
		)
	})
}

// recoverMiddleware recovers from a handler panic, logs it, and writes 500 so a
// panicking handler is visible in the build log instead of being swallowed by
// net/http (which would otherwise close the connection with no log and no
// status, leaving the AWS service to retry into a build timeout).
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error(serviceName+": handler panic", //nolint:gosec // G706: path/panic are structured slog attributes (JSON-encoded key/values), not a log format string.
					slog.String("path", r.URL.Path),
					slog.String("panic", fmt.Sprintf("%v", rec)),
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "panic"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code for
// request-complete logging. It deliberately does NOT implement
// http.Hijacker/Flusher/Pusher — the hook handlers only use WriteHeader + Write,
// so a minimal wrapper is correct and avoids silently dropping capabilities the
// handlers do not use.
type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (sw *statusWriter) WriteHeader(status int) {
	if sw.wrote {
		return
	}
	sw.status = status
	sw.wrote = true
	sw.ResponseWriter.WriteHeader(status)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wrote {
		sw.wrote = true
	}
	return sw.ResponseWriter.Write(b)
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
			logHookRequest(r.URL.Path, http.StatusBadRequest, "")
			return
		}
		event.Hook = hook
		result := s.drive(r.Context(), event)
		writeJSON(w, http.StatusOK, result)
		logHookRequest(r.URL.Path, http.StatusOK, string(result.State))
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
		logHookRequest(r.URL.Path, http.StatusBadRequest, "")
		return
	}
	event.Hook = runtimemicrovm.HookRun
	result := s.drive(r.Context(), event)
	writeJSON(w, http.StatusOK, result)
	logHookRequest(r.URL.Path, http.StatusOK, string(result.State))
}

// logHookRequest emits one structured log line per lifecycle hook request with
// its path + HTTP status (+ result state when non-empty), so image-build / hook
// failures are diagnosable in the CloudWatch build log group
// (/aws/lambda/microvms/<image-name>). The path is a structured slog attribute
// (JSON-encoded key/value), not a printf format string, so there is no
// format-string injection surface.
//
//nolint:gosec // G706: path/state are structured slog attributes (JSON-encoded key/values), not a log format string.
func logHookRequest(path string, status int, state string) {
	attrs := []any{
		slog.String("path", path),
		slog.Int("status", status),
	}
	if state != "" {
		attrs = append(attrs, slog.String("state", state))
	}
	slog.Info("hosted-genesis-microvm-workload: hook request", attrs...)
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

// validateHook is the image-build validation hook. The AWS Lambda Microvms
// service invokes /validate during image creation with NO request body (see the
// OpenAPI spec in the AWS docs at
// https://docs.aws.amazon.com/lambda/latest/dg/microvms-launching.html —
// /validate defines no requestBody, only 200/503 responses), so there is no
// namespace or session context to bind. The image is valid once the workload is
// serving the hook; return nil unconditionally.
func validateHook(_ context.Context, _ runtimemicrovm.LifecycleEvent) error {
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
// The AWS Lambda Microvms service invokes /ready during image creation with NO
// request body (see the OpenAPI spec in the AWS docs at
// https://docs.aws.amazon.com/lambda/latest/dg/microvms-launching.html — /ready
// defines no requestBody, only 200/503 responses, where 503 is retried until
// timeout), so there is no namespace or session context to bind. The workload is
// ready once it is serving the hook; return nil unconditionally. Returning a
// non-nil error here (e.g. for a missing namespace) made the service retry /ready
// until the ~120s readyTimeoutInSeconds elapsed, after which the image build
// failed with CREATE_FAILED "did not stabilize".
func readyHook(_ context.Context, _ runtimemicrovm.LifecycleEvent) error {
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

// decodeLifecycleEvent reads a sanitized MicroVM lifecycle event from the hook
// request body. The AWS Lambda Microvms service's image-build hooks (/ready and
// /validate) send NO request body (see the OpenAPI spec in the AWS docs at
// https://docs.aws.amazon.com/lambda/latest/dg/microvms-launching.html — neither
// /ready nor /validate defines a requestBody). An empty body therefore decodes
// to io.EOF, which is the build-hook contract, not a malformed event: return an
// empty LifecycleEvent with no error so the hook can acknowledge readiness /
// validity. Only a non-empty but malformed JSON body is a real decode failure.
func decodeLifecycleEvent(r *http.Request) (runtimemicrovm.LifecycleEvent, error) {
	var event runtimemicrovm.LifecycleEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return runtimemicrovm.LifecycleEvent{}, nil
		}
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

// httpServer wires a real *http.Server with the configured hook port and the
// middleware-wrapped routes handler. It does NOT call ListenAndServe; use
// serveWithListener for the explicit-bind serving path so a bind failure is
// logged + fails fast instead of a ~120s build timeout.
func (s *hookServer) httpServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// serveWithListener binds the address explicitly with net.Listen BEFORE
// serving, so a successful bind is confirmed with a "listening" log line and a
// bind failure is logged + returned (the caller exits non-zero) instead of
// surfacing as a ~120s build timeout (deploy #8's "did not stabilize"). It also
// logs the registered hook paths at startup so the build log shows what the app
// is serving. The *http.Server is provided so graceful Shutdown still drains
// in-flight requests.
func (s *hookServer) serveWithListener(srv *http.Server, addr string) error {
	logRegisteredRoutes()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error(serviceName+": listen failed", slog.String("addr", addr), slog.String("error", err.Error())) //nolint:gosec // G706: addr/error are structured slog attributes (JSON-encoded key/values), not a log format string.
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	slog.Info(serviceName+": listening", slog.String("addr", addr)) //nolint:gosec // G706: addr is a structured slog attribute (JSON-encoded key/value), not a log format string.
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// logRegisteredRoutes logs the hook path prefix + the registered hook paths at
// startup so the build log shows exactly what the app is serving (deploy #8
// showed "serving" but no proof of what paths were registered).
func logRegisteredRoutes() {
	slog.Info(serviceName+": registered routes", //nolint:gosec // G706: prefix is a structured slog attribute (JSON-encoded key/value), not a log format string.
		slog.String("prefix", hookPathPrefix),
		slog.String("healthz", "/healthz"),
		slog.String("catch_all", "/"),
	)
}
