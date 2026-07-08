package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store"
)

// microVMWiringTestConfig returns a complete HTTP-transport MicroVM config for
// NewServer wiring tests. The ControllerEndpoint + AuthTokenSSMParam +
// ImageRef/NetworkConnectorRef satisfy Complete(); tests inject an httptest
// stub controller URL + a stub SSM getter so no AWS or SSM is called.
func microVMWiringTestConfig() config.Config {
	return config.Config{
		Stage: "lab",
		HostedGenesisMicroVM: config.HostedGenesisMicroVMConfig{
			Enabled:                true,
			ControllerEndpoint:     "https://placeholder.example/microvms",
			AuthTokenSSMParam:      "/lesser-host/hosted-genesis/microvm/auth-token",
			ImageRef:               "arn:aws:lambda::microvm-image/hosted-genesis:test",
			NetworkConnectorRef:    "arn:aws:lambda::network-connector/egress:test",
			IngressConnectorRefs:   []string{"arn:aws:lambda::network-connector/all-ingress:test"},
			EgressConnectorRefs:    []string{"arn:aws:lambda::network-connector/egress:test"},
			MaximumDurationSeconds: 300,
		},
	}
}

// stubControllerServer re-uses the hostedgenesis package's stub controller via
// an httptest.Server. It is duplicated here as a thin local helper so the
// controlplane wiring tests can stand up a stub controller without importing
// the hostedgenesis test helpers (which are not exported). It serializes the
// same ControllerResponse JSON shape the real controller route handler emits.
type stubControllerServer struct {
	token    string
	sessions map[string]runtimemicrovm.ControllerResponse
}

func newStubControllerServer(token string) *stubControllerServer {
	return &stubControllerServer{token: token, sessions: map[string]runtimemicrovm.ControllerResponse{}}
}

func (s *stubControllerServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/microvms", s.handleRun)
	mux.HandleFunc("/microvms/", s.handleGet)
	return mux
}

func (s *stubControllerServer) authorize(r *http.Request) bool {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return token != "" && token != r.Header.Get("Authorization") && token == s.token &&
		r.Header.Get("x-tenant-id") != "" && r.Header.Get("x-namespace-id") != ""
}

func (s *stubControllerServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !s.authorize(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var payload struct {
		SessionID              string `json:"session_id"`
		MaximumDurationSeconds int32  `json:"maximum_duration_seconds"`
	}
	_ = readJSONBody(r, &payload)
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = "stub-session"
	}
	now := time.Now().UTC()
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         r.Header.Get("x-request-id"),
		TenantID:          r.Header.Get("x-tenant-id"),
		Namespace:         r.Header.Get("x-namespace-id"),
		SessionID:         sessionID,
		State:             runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		DesiredState:      runtimemicrovm.StateRunning,
		ProviderMicroVMID: "stub-microvm-" + sessionID,
		ProviderState:     "running",
		LastAction:        runtimemicrovm.CommandRun,
		LastTransition:    now,
		RegistryVersion:   1,
		ExpiresAt:         now.Add(time.Hour),
	}
	s.sessions[sessionID] = resp
	writeJSON(w, http.StatusOK, resp)
}

func (s *stubControllerServer) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !s.authorize(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	sessionID := strings.TrimPrefix(r.URL.Path, "/microvms/")
	resp, ok := s.sessions[sessionID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	resp.Command = runtimemicrovm.CommandGet
	resp.LastAction = runtimemicrovm.CommandGet
	writeJSON(w, http.StatusOK, resp)
}

func (s *stubControllerServer) terminate(sessionID string) {
	resp, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	resp.State = runtimemicrovm.StateTerminated
	resp.LifecycleState = runtimemicrovm.StateTerminated
	resp.DesiredState = runtimemicrovm.StateTerminated
	resp.ProviderState = "terminated"
	resp.LastAction = runtimemicrovm.CommandTerminate
	resp.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	s.sessions[sessionID] = resp
}

// stubSSMGetter returns a fixed auth token for the configured SSM param name.
type stubSSMGetter struct {
	param string
	token string
	err   error
}

func (g stubSSMGetter) GetParameter(_ context.Context, name string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	if name != g.param {
		return "", nil
	}
	return g.token, nil
}

// TestH1_5_NewServerWiresHTTPControllerDispatcherForDeployedStages proves
// NewServer constructs a real HTTPControllerDispatcher against the governed
// AppTheoryMicrovmController HTTP API when the MicroVM config is enabled and
// complete, and sets it on the Server so the accept path is dispatch-only (no
// sync LLM). The httptest stub controller + stub SSM getter prove the wiring
// without calling AWS or SSM.
func TestH1_5_NewServerWiresHTTPControllerDispatcherForDeployedStages(t *testing.T) {
	cfg := microVMWiringTestConfig()
	stub := newStubControllerServer("stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	cfg.HostedGenesisMicroVM.ControllerEndpoint = srv.URL + "/microvms"

	dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, stubSSMGetter{
		param: cfg.HostedGenesisMicroVM.AuthTokenSSMParam,
		token: stub.token,
	}.GetParameter, hostedGenesisMicroVMDispatcherOptions{})
	if dispatcher == nil {
		t.Fatalf("expected a real HTTPControllerDispatcher wired for a complete enabled config, got nil")
	}

	crd, ok := dispatcher.(*hostedgenesis.HTTPControllerDispatcher)
	if !ok {
		t.Fatalf("expected *hostedgenesis.HTTPControllerDispatcher, got %T", dispatcher)
	}
	if crd == nil {
		t.Fatalf("http controller dispatcher is nil")
	}

	// Dispatching a run through the wired dispatcher must reach the stub
	// controller over HTTP (proving the real HTTP transport is wired, not a
	// stub seam) and return a validated in_progress lifecycle ref.
	binding := hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   "acme",
		RegistrationID: "reg_123",
		AgentID:        "agent_abc",
		ConversationID: "conv_001",
		TurnID:         "turn_1",
	}
	result, err := dispatcher.DispatchMicroVMRun(context.Background(), "req_h1_5_wiring", binding)
	if err != nil {
		t.Fatalf("wired dispatcher run dispatch failed: %v", err)
	}
	if result.SessionID == "" || result.LifecycleRef.SessionID != binding.ConversationID {
		t.Fatalf("wired dispatcher returned an invalid dispatch result: %#v", result)
	}
	if result.LifecycleRef.LifecycleState != runtimemicrovm.StateRunning {
		t.Fatalf("expected running lifecycle state from stub controller, got %q", result.LifecycleRef.LifecycleState)
	}
}

// TestH1_5_NewServerLeavesDispatcherNilWhenConfigIncomplete proves the
// fail-closed posture: when the MicroVM config is disabled or incomplete,
// NewServer leaves the dispatcher unwired (nil) so the accept path fails closed
// and loudly with a typed 503 microvm_unavailable, never a silent sync LLM
// fallback.
func TestH1_5_NewServerLeavesDispatcherNilWhenConfigIncomplete(t *testing.T) {
	ssm := stubSSMGetter{param: "/x", token: "tok"}.GetParameter

	disabled := microVMWiringTestConfig()
	disabled.HostedGenesisMicroVM.Enabled = false
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), disabled, ssm, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("disabled config must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}

	incomplete := microVMWiringTestConfig()
	incomplete.HostedGenesisMicroVM.AuthTokenSSMParam = ""
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), incomplete, ssm, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("incomplete config must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}

	missingEndpoint := microVMWiringTestConfig()
	missingEndpoint.HostedGenesisMicroVM.ControllerEndpoint = ""
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), missingEndpoint, ssm, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("missing endpoint must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}
}

// TestH1_5_DispatcherConstructionFailureFailsLoudlyNoSyncFallback proves that
// when the SSM auth-token fetch fails or returns an empty token, the dispatcher
// is left unwired (nil) — the accept path then fails closed with a typed 503,
// never a silent fallback to the synchronous control-plane LLM.
func TestH1_5_DispatcherConstructionFailureFailsLoudlyNoSyncFallback(t *testing.T) {
	cfg := microVMWiringTestConfig()
	stub := newStubControllerServer("stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	cfg.HostedGenesisMicroVM.ControllerEndpoint = srv.URL + "/microvms"

	ssmErr := errMicroVM("ssm unavailable")
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, stubSSMGetter{
		param: cfg.HostedGenesisMicroVM.AuthTokenSSMParam,
		err:   ssmErr,
	}.GetParameter, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("ssm auth-token fetch failure must yield a nil dispatcher (fail-closed, no sync fallback), got %T", dispatcher)
	}

	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, stubSSMGetter{
		param: cfg.HostedGenesisMicroVM.AuthTokenSSMParam,
		token: "  ", // empty after trim
	}.GetParameter, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("empty auth token must yield a nil dispatcher (fail-closed, no sync fallback), got %T", dispatcher)
	}

	// No SSM getter at all (production misconfiguration): fail closed.
	if dispatcher := newHostedGenesisMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisMicroVMDispatcherOptions{}); dispatcher != nil {
		t.Fatalf("nil ssm getter must yield a nil dispatcher (fail-closed), got %T", dispatcher)
	}
}

// TestH1_5_NewServerSetsNonNilDispatcherOnServer proves NewServer wires a real
// HTTPControllerDispatcher onto the Server for a deployed-stage config. It
// overrides the package-level builder seam to inject a stub SSM getter + stub
// controller endpoint so the test never calls AWS or SSM, and asserts the
// Server's hostedGenesisMicroVMDispatcher is a *HTTPControllerDispatcher (not
// nil). The seam is restored on cleanup.
func TestH1_5_NewServerSetsNonNilDispatcherOnServer(t *testing.T) {
	originalBuilder := hostedGenesisMicroVMDispatcherBuilder
	t.Cleanup(func() { hostedGenesisMicroVMDispatcherBuilder = originalBuilder })

	stub := newStubControllerServer("stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	cfg := microVMWiringTestConfig()
	cfg.HostedGenesisMicroVM.ControllerEndpoint = srv.URL + "/microvms"
	ssm := stubSSMGetter{param: cfg.HostedGenesisMicroVM.AuthTokenSSMParam, token: stub.token}.GetParameter
	hostedGenesisMicroVMDispatcherBuilder = func(ctx context.Context, c config.Config, _ func(context.Context, string) (string, error), _ hostedGenesisMicroVMDispatcherOptions) hostedgenesis.MicroVMDispatcher {
		return newHostedGenesisMicroVMDispatcher(ctx, c, ssm, hostedGenesisMicroVMDispatcherOptions{})
	}

	srv2 := NewServer(cfg, store.New(nil))
	if srv2 == nil {
		t.Fatalf("NewServer returned nil")
	}
	if srv2.hostedGenesisMicroVMDispatcher == nil {
		t.Fatalf("expected NewServer to wire a non-nil MicroVM dispatcher for a complete enabled config")
	}
	if _, ok := srv2.hostedGenesisMicroVMDispatcher.(*hostedgenesis.HTTPControllerDispatcher); !ok {
		t.Fatalf("expected Server.hostedGenesisMicroVMDispatcher to be *HTTPControllerDispatcher, got %T", srv2.hostedGenesisMicroVMDispatcher)
	}
}

// TestH1_5_NewServerLeavesDispatcherNilForEmptyConfig proves NewServer leaves
// the dispatcher unwired for the default/empty config (the existing
// fail-closed posture preserved across all current tests): no AWS calls, no
// sync LLM fallback, accept path 503s.
func TestH1_5_NewServerLeavesDispatcherNilForEmptyConfig(t *testing.T) {
	srv := NewServer(config.Config{}, store.New(nil))
	if srv == nil {
		t.Fatalf("NewServer returned nil")
	}
	if srv.hostedGenesisMicroVMDispatcher != nil {
		t.Fatalf("empty config must leave the dispatcher nil (fail-closed), got %T", srv.hostedGenesisMicroVMDispatcher)
	}
}

// TestH1_5_MicroVMUnavailableAcceptPathReturnsExplicit503 is the grep-proof
// structural guard that the three MicroVM-unavailable accept-path returns in
// handlers_soul_mint_conversation_async.go (dispatcher-unavailable, invalid
// binding, dispatch-failed) carry forward G10a's explicit-status posture from
// H1.4: they return http.StatusServiceUnavailable (503), matching the error
// mapper's 503 for appErrCodeMicroVMUnavailable, not the prior silent
// http.StatusOK. The mapper-emitted 503 assertion at
// handlers_soul_mint_conversation_async_internal_test.go (TestH1_2_MicroVMUnavailableIsLoudFailureNotSyncLLFallthrough)
// stays green; this guard proves the returned response-status int is also 503.
func TestH1_5_MicroVMUnavailableAcceptPathReturnsExplicit503(t *testing.T) {
	asyncSrc := mustReadControlplaneSource(t, "handlers_soul_mint_conversation_async.go")

	// The three MicroVM-unavailable returns must reference 503, not 200.
	if strings.Contains(asyncSrc, "return failedSession, failedConv, http.StatusOK, newAppTheoryError(appErrCodeMicroVMUnavailable") {
		t.Fatalf("H1.5 regression: a MicroVM-unavailable accept-path return still uses http.StatusOK (silent 200-on-failure), expected http.StatusServiceUnavailable")
	}
	explicit503Count := strings.Count(asyncSrc, "return failedSession, failedConv, http.StatusServiceUnavailable, newAppTheoryError(appErrCodeMicroVMUnavailable")
	if explicit503Count != 3 {
		t.Fatalf("expected exactly three explicit 503 MicroVM-unavailable accept-path returns, got %d", explicit503Count)
	}
}

// readJSONBody decodes a JSON request body into dst. It is a thin local helper
// for the stub controller server so the wiring tests do not import the
// hostedgenesis test helpers (which are not exported).
func readJSONBody(r *http.Request, dst any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}

// writeJSON encodes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
