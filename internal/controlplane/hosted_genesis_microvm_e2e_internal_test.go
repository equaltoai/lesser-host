package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

// TestH1_5_E2E_HappyPathAndKillVMRecovery is the stub/local-proved E2E harness
// for the P52 H1.5 lab gate. It drives the full MicroVM dispatch lifecycle
// through the REAL HTTPControllerDispatcher wired via the H1.5 NewServer seam
// (not a stubMicroVMDispatcher) against an httptest.Server-backed stub
// controller serving the governed AppTheoryMicrovmController HTTP routes,
// proving the state-machine arc the lab E2E gate exercises without calling AWS:
//
//  1. Happy path: dispatch run (POST /microvms) -> in_progress lifecycle ref
//     (well under the <2s accept budget); reconcile get (GET
//     /microvms/{session_id}) -> live VM preserves the pending ref
//     (assistant_turn_ready is reached by the in-VM workload writing session
//     truth out-of-band, modeled here by the stub controller keeping the
//     session running); a follow-on extraction run dispatches on the same
//     session (declaration_extraction_pending -> declaration_ready is the
//     workload completing in-VM).
//  2. Kill-VM recovery: dispatch run -> in_progress; the stub controller
//     reports the session terminated (the VM was killed mid-turn); reconcile
//     get surfaces Terminal=true -> the recover path maps it to a loud
//     retryable failed session; a retry dispatch run allocates a fresh
//     in_progress ref on the same session (retry works).
//
// It complements the link-by-link H1.2/H1.3/H1.4 handler tests by proving the
// wired HTTP dispatcher chain end-to-end. The runnable lab script
// (scripts/hosted-genesis-microvm-e2e-gate.sh) drives the same arc against the
// deployed lab endpoints; this test is the local-proof gate that runs in CI.
func TestH1_5_E2E_HappyPathAndKillVMRecovery(t *testing.T) {
	cfg := microVMWiringTestConfig()
	stub := newStubControllerServer("stub-bearer")
	controllerSrv := httptest.NewServer(stub.handler())
	t.Cleanup(controllerSrv.Close)
	cfg.HostedGenesisMicroVM.ControllerEndpoint = controllerSrv.URL + "/microvms"

	dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, stubSSMGetter{
		param: cfg.HostedGenesisMicroVM.AuthTokenSSMParam,
		token: stub.token,
	}.GetParameter, hostedGenesisMicroVMDispatcherOptions{})
	if dispatcher == nil {
		t.Fatalf("E2E harness expected a wired dispatcher, got nil")
	}
	crd, ok := dispatcher.(*hostedgenesis.HTTPControllerDispatcher)
	if !ok || crd == nil {
		t.Fatalf("E2E harness expected *HTTPControllerDispatcher, got %T", dispatcher)
	}

	binding := hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   "acme",
		RegistrationID: "reg_e2e",
		AgentID:        "agent_e2e",
		ConversationID: "conv_e2e_001",
		TurnID:         "turn_1",
	}
	const acceptBudget = 2 * time.Second

	// --- Happy path: accept dispatch (must complete well under the <2s budget) ---
	acceptStart := time.Now()
	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_e2e_accept", binding)
	acceptElapsed := time.Since(acceptStart)
	if err != nil {
		t.Fatalf("happy path: accept dispatch failed: %v", err)
	}
	if acceptElapsed >= acceptBudget {
		t.Fatalf("happy path: accept dispatch took %s, expected < %s (202 budget)", acceptElapsed, acceptBudget)
	}
	if dispatch.LifecycleRef.LifecycleState != runtimemicrovm.StateRunning {
		t.Fatalf("happy path: expected running lifecycle state after accept, got %q", dispatch.LifecycleRef.LifecycleState)
	}
	if dispatch.LifecycleRef.SessionID != binding.ConversationID {
		t.Fatalf("happy path: dispatch ref bound to wrong session: %#v", dispatch.LifecycleRef)
	}

	// Poll: reconcile get observes a live VM -> non-terminal, pending preserved.
	reconcile, err := dispatcher.ReconcileMicroVM(context.Background(), "req_e2e_poll", binding, dispatch.LifecycleRef)
	if err != nil {
		t.Fatalf("happy path: poll reconcile failed: %v", err)
	}
	if reconcile.Terminal {
		t.Fatalf("happy path: a live VM must not be Terminal; expected pending preserved, got terminal reconcile %#v", reconcile)
	}
	if reconcile.LifecycleRef.LifecycleState != runtimemicrovm.StateRunning {
		t.Fatalf("happy path: expected running observed state on live VM, got %q", reconcile.LifecycleRef.LifecycleState)
	}

	// In-VM extraction: a follow-on run dispatch on the same session (the
	// declaration_extraction_pending -> declaration_ready transition is serviced
	// by the VM). The dispatcher allocates the same session id (binding-bound).
	extraction, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_e2e_extract", binding)
	if err != nil {
		t.Fatalf("happy path: extraction dispatch failed: %v", err)
	}
	if extraction.LifecycleRef.SessionID != binding.ConversationID {
		t.Fatalf("happy path: extraction dispatch ref bound to wrong session: %#v", extraction.LifecycleRef)
	}
	if extraction.LifecycleRef.LifecycleState != runtimemicrovm.StateRunning {
		t.Fatalf("happy path: extraction expected running state, got %q", extraction.LifecycleRef.LifecycleState)
	}

	// --- Kill-VM recovery arc (extracted to keep the happy-path test under the
	// gocognit budget) ---
	h1_5KillVMRecoveryArc(t, dispatcher, stub)
}

// h1_5KillVMRecoveryArc drives the kill-VM recovery half of the E2E gate:
// dispatch a fresh session, terminate it mid-turn in the stub controller (the
// VM was killed), reconcile get surfaces Terminal=true (recover maps to loud
// failed), then a retry dispatch allocates a fresh in_progress ref. Extracted
// from TestH1_5_E2E_HappyPathAndKillVMRecovery to keep that func's cognitive
// complexity under the gocognit >20 budget.
func h1_5KillVMRecoveryArc(t *testing.T, dispatcher hostedgenesis.MicroVMDispatcher, stub *stubControllerServer) {
	t.Helper()
	killedBinding := hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   "acme",
		RegistrationID: "reg_e2e",
		AgentID:        "agent_e2e",
		ConversationID: "conv_e2e_002",
		TurnID:         "turn_1",
	}
	killedDispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_e2e_kill_accept", killedBinding)
	if err != nil {
		t.Fatalf("kill-vm: accept dispatch failed: %v", err)
	}
	// Simulate the VM being killed mid-turn: terminate the stub session so a
	// subsequent reconcile get observes a terminal state.
	stub.terminate(killedBinding.ConversationID)

	// Recover: reconcile get surfaces Terminal=true -> recover maps to loud failed.
	killedReconcile, err := dispatcher.ReconcileMicroVM(context.Background(), "req_e2e_recover", killedBinding, killedDispatch.LifecycleRef)
	if err != nil {
		t.Fatalf("kill-vm: recover reconcile failed: %v", err)
	}
	if !killedReconcile.Terminal {
		t.Fatalf("kill-vm: a terminated VM must be Terminal so recover maps to loud failed, got non-terminal %#v", killedReconcile)
	}
	if killedReconcile.LifecycleRef.LifecycleState != runtimemicrovm.StateTerminated {
		t.Fatalf("kill-vm: expected terminated observed state, got %q", killedReconcile.LifecycleRef.LifecycleState)
	}

	// Retry works: a fresh dispatch run on the same killed session allocates a
	// new in_progress lifecycle ref (the controller re-runs the VM). The stub
	// controller records a new running session for the retry.
	retryDispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_e2e_retry", killedBinding)
	if err != nil {
		t.Fatalf("kill-vm: retry dispatch failed: %v", err)
	}
	if retryDispatch.LifecycleRef.LifecycleState != runtimemicrovm.StateRunning {
		t.Fatalf("kill-vm: retry expected a fresh running lifecycle ref, got %q", retryDispatch.LifecycleRef.LifecycleState)
	}
	if retryDispatch.LifecycleRef.SessionID != killedBinding.ConversationID {
		t.Fatalf("kill-vm: retry ref bound to wrong session: %#v", retryDispatch.LifecycleRef)
	}
}

// TestH1_5_E2E_MaximumDurationSecondsWired proves the H1.5 timeout-budget
// wiring: the dispatcher-sized MaximumDurationSeconds (config, decision 7) is
// set on the dispatched POST /microvms run body so the MicroVM session is
// bounded for the longest LLM turn plus in-VM extraction. The stub controller
// records the value it received on the run body.
func TestH1_5_E2E_MaximumDurationSecondsWired(t *testing.T) {
	cfg := microVMWiringTestConfig()
	const wantMaxDuration = int32(300)
	cfg.HostedGenesisMicroVM.MaximumDurationSeconds = wantMaxDuration

	stub := newMaxDurationCapturingControllerServer(t, "stub-bearer")
	controllerSrv := httptest.NewServer(stub.handler())
	t.Cleanup(controllerSrv.Close)
	cfg.HostedGenesisMicroVM.ControllerEndpoint = controllerSrv.URL + "/microvms"

	dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, stubSSMGetter{
		param: cfg.HostedGenesisMicroVM.AuthTokenSSMParam,
		token: stub.inner.token,
	}.GetParameter, hostedGenesisMicroVMDispatcherOptions{})
	if dispatcher == nil {
		t.Fatalf("expected wired dispatcher for max-duration test, got nil")
	}
	binding := hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   "acme",
		RegistrationID: "reg_md",
		AgentID:        "agent_md",
		ConversationID: "conv_md_001",
		TurnID:         "turn_1",
	}
	if _, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_md", binding); err != nil {
		t.Fatalf("max-duration dispatch failed: %v", err)
	}
	if got := stub.capturedMaxDuration(); got != wantMaxDuration {
		t.Fatalf("expected MaximumDurationSeconds=%d on the run request, got %d", wantMaxDuration, got)
	}
}

// maxDurationCapturingControllerServer wraps the stub controller and captures
// the maximum_duration_seconds passed in the POST /microvms run body so the E2E
// harness can assert the timeout-budget wiring without calling AWS.
type maxDurationCapturingControllerServer struct {
	inner    *stubControllerServer
	captured *int32
}

func newMaxDurationCapturingControllerServer(t *testing.T, token string) *maxDurationCapturingControllerServer {
	t.Helper()
	inner := newStubControllerServer(token)
	return &maxDurationCapturingControllerServer{inner: inner, captured: new(int32)}
}

func (s *maxDurationCapturingControllerServer) handler() http.Handler {
	innerHandler := s.inner.handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/microvms") {
			// Buffer the body so the inner handler can still decode it after we
			// capture the duration field.
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err == nil {
				var payload struct {
					MaximumDurationSeconds int32 `json:"maximum_duration_seconds"`
				}
				_ = json.Unmarshal(body, &payload)
				*s.captured = payload.MaximumDurationSeconds
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		innerHandler.ServeHTTP(w, r)
	})
}

func (s *maxDurationCapturingControllerServer) capturedMaxDuration() int32 { return *s.captured }

// TestH1_5_E2E_HarnessConfigCompleteGuard is a lightweight guard that the
// harness's wiring-test config satisfies config.HostedGenesisMicroVMConfig.Complete
// (the gate NewServer uses to decide between wiring the real dispatcher and
// failing closed). If this breaks, the E2E harness config drifted from the
// production config shape.
func TestH1_5_E2E_HarnessConfigCompleteGuard(t *testing.T) {
	cfg := microVMWiringTestConfig()
	if !cfg.HostedGenesisMicroVM.Complete() {
		t.Fatalf("E2E harness config must be Complete (enabled + endpoint + auth token + image + egress), got %#v", cfg.HostedGenesisMicroVM)
	}
	if !strings.HasPrefix(cfg.HostedGenesisMicroVM.ImageRef, "arn:") {
		t.Fatalf("E2E harness config image ref drifted: %q", cfg.HostedGenesisMicroVM.ImageRef)
	}
	if !strings.HasPrefix(cfg.HostedGenesisMicroVM.ControllerEndpoint, "https://placeholder.example/microvms") {
		t.Fatalf("E2E harness config controller endpoint drifted: %q", cfg.HostedGenesisMicroVM.ControllerEndpoint)
	}
}
