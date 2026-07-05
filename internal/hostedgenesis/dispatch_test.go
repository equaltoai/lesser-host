package hostedgenesis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

// stubControllerServer is an httptest.Server-backed stub of the governed
// AppTheoryMicrovmController HTTP API. It serves POST /microvms (run) and GET
// /microvms/{session_id} (get), serializing the same ControllerResponse JSON
// shape the real controller route handler emits, so the HTTPControllerDispatcher
// under test exercises the real HTTP transport + response decoding path. It
// validates the Authorization bearer header + x-tenant-id + x-namespace-id the
// dispatcher presents, and lets a test terminate a session so a subsequent get
// observes a terminal state (the kill-VM recovery arc).
type stubControllerServer struct {
	t        *testing.T
	token    string
	mu       sync.Mutex
	sessions map[string]runtimemicrovm.ControllerResponse
}

func newStubControllerServer(t *testing.T, token string) *stubControllerServer {
	t.Helper()
	return &stubControllerServer{
		t:        t,
		token:    token,
		sessions: map[string]runtimemicrovm.ControllerResponse{},
	}
}

func (s *stubControllerServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/microvms", s.handleRun)
	mux.HandleFunc("/microvms/", s.handleGet)
	return mux
}

func (s *stubControllerServer) authorize(r *http.Request) bool {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if token == "" || token == authorization {
		return false
	}
	if token != s.token {
		return false
	}
	if strings.TrimSpace(r.Header.Get("x-tenant-id")) == "" || strings.TrimSpace(r.Header.Get("x-namespace-id")) == "" {
		return false
	}
	return true
}

func (s *stubControllerServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		writeControllerJSON(w, http.StatusUnauthorized, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.unauthenticated_controller", Message: "unauthorized"},
		})
		return
	}
	var payload microvmRunRequestPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeControllerJSON(w, http.StatusBadRequest, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.invalid_controller_request", Message: "malformed"},
		})
		return
	}
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = "stub-session"
	}
	now := time.Now().UTC()
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         strings.TrimSpace(r.Header.Get("x-request-id")),
		TenantID:          strings.TrimSpace(r.Header.Get("x-tenant-id")),
		Namespace:         strings.TrimSpace(r.Header.Get("x-namespace-id")),
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
	s.mu.Lock()
	s.sessions[sessionID] = resp
	s.mu.Unlock()
	writeControllerJSON(w, http.StatusOK, resp)
}

func (s *stubControllerServer) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		writeControllerJSON(w, http.StatusUnauthorized, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.unauthenticated_controller", Message: "unauthorized"},
		})
		return
	}
	sessionID := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/microvms/"), "/")
	s.mu.Lock()
	resp, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		writeControllerJSON(w, http.StatusNotFound, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.session_registry_incomplete", Message: "session not found"},
		})
		return
	}
	resp.Command = runtimemicrovm.CommandGet
	resp.LastAction = runtimemicrovm.CommandGet
	resp.LastTransition = time.Now().UTC()
	writeControllerJSON(w, http.StatusOK, resp)
}

// terminate marks a session terminated so a subsequent get observes a terminal
// state (the kill-VM recovery arc).
func (s *stubControllerServer) terminate(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	resp.State = runtimemicrovm.StateTerminated
	resp.LifecycleState = runtimemicrovm.StateTerminated
	resp.DesiredState = runtimemicrovm.StateTerminated
	resp.ProviderState = "terminated"
	resp.LastAction = runtimemicrovm.CommandTerminate
	resp.ExpiresAt = time.Now().UTC().Add(-time.Minute) // past expiry → terminal
	s.sessions[sessionID] = resp
}

func writeControllerJSON(w http.ResponseWriter, status int, resp runtimemicrovm.ControllerResponse) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func testHTTPDispatcher(t *testing.T, endpoint, token string) *HTTPControllerDispatcher {
	t.Helper()
	d, err := NewHTTPControllerDispatcher(HTTPControllerDispatcherConfig{
		Endpoint:            endpoint,
		AuthToken:           token,
		ImageRef:            "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef: "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress",
		MaxDurationSeconds:  300,
		HTTPClient:          &http.Client{Timeout: 5 * time.Second},
	})
	require.NoError(t, err)
	return d
}

func TestHTTPControllerDispatcherDispatchesRunViaPOST(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	result, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", binding)
	require.NoError(t, err)
	require.Equal(t, binding.ConversationID, result.SessionID)
	require.NoError(t, result.LifecycleRef.Validate(binding))
	require.Equal(t, MicroVMSourceOfTruth, result.LifecycleRef.SourceOfTruth)
	require.Equal(t, runtimemicrovm.CommandRun, result.LifecycleRef.LastAction)
	require.NotEmpty(t, result.LifecycleRef.MicroVMID)
	require.Equal(t, runtimemicrovm.StateRunning, result.LifecycleRef.LifecycleState)
}

func TestHTTPControllerDispatcherReconcilesViaGET(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)

	// Reconcile via GET /microvms/{session_id}: the live VM is observed running
	// (non-terminal) and the reconciled ref maps back to the binding.
	result, err := dispatcher.ReconcileMicroVM(context.Background(), "req-get", binding, dispatch.LifecycleRef)
	require.NoError(t, err)
	require.Equal(t, binding.ConversationID, result.SessionID)
	require.False(t, result.Terminal, "live VM must not be terminal")
	require.NoError(t, result.LifecycleRef.Validate(binding))
	require.Equal(t, runtimemicrovm.CommandGet, result.LifecycleRef.LastAction)
}

func TestHTTPControllerDispatcherReconcileTerminalVMReportsTerminal(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)

	// Simulate the VM being killed mid-turn: terminate the stub session so a
	// subsequent reconcile get observes a terminal state.
	stub.terminate(binding.ConversationID)

	result, err := dispatcher.ReconcileMicroVM(context.Background(), "req-get", binding, dispatch.LifecycleRef)
	require.NoError(t, err)
	require.True(t, result.Terminal, "terminated VM must reconcile as terminal")
	require.Equal(t, runtimemicrovm.StateTerminated, result.LifecycleRef.LifecycleState)
	require.NoError(t, result.LifecycleRef.Validate(binding))
}

func TestHTTPControllerDispatcherFailsClosedWhenNil(t *testing.T) {
	t.Parallel()
	var dispatcher *HTTPControllerDispatcher
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", testMicroVMBinding())
	require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable)
	_, err = dispatcher.ReconcileMicroVM(context.Background(), "req-get", testMicroVMBinding(), MicroVMLifecycleRef{})
	require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable)
}

func TestHTTPControllerDispatcherConstructionFailsClosedOnMissingConfig(t *testing.T) {
	t.Parallel()
	client := &http.Client{Timeout: time.Second}
	cases := []struct {
		name string
		cfg  HTTPControllerDispatcherConfig
	}{
		{"missing endpoint", HTTPControllerDispatcherConfig{AuthToken: "tok", ImageRef: "img", NetworkConnectorRef: "net", HTTPClient: client}},
		{"missing auth token", HTTPControllerDispatcherConfig{Endpoint: "https://example/microvms", ImageRef: "img", NetworkConnectorRef: "net", HTTPClient: client}},
		{"missing image ref", HTTPControllerDispatcherConfig{Endpoint: "https://example/microvms", AuthToken: "tok", NetworkConnectorRef: "net", HTTPClient: client}},
		{"missing network ref", HTTPControllerDispatcherConfig{Endpoint: "https://example/microvms", AuthToken: "tok", ImageRef: "img", HTTPClient: client}},
		{"nil http client", HTTPControllerDispatcherConfig{Endpoint: "https://example/microvms", AuthToken: "tok", ImageRef: "img", NetworkConnectorRef: "net"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewHTTPControllerDispatcher(tc.cfg)
			require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable, "incomplete config must fail closed, not panic")
		})
	}
}

func TestHTTPControllerDispatcherFailsClosedOnInvalidBinding(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", MicroVMSessionBinding{})
	require.Error(t, err)
}

func TestHTTPControllerDispatcherFailsClosedOnEmptyRequestID(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "  ", testMicroVMBinding())
	require.ErrorIs(t, err, ErrMicroVMDispatchUnavailable)
}

func TestHTTPControllerDispatcherFailsClosedOnUnauthorized(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "expected-token")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	// Present the wrong bearer token: the stub authorizer denies, the
	// controller returns 401 with a SafeError, and the dispatcher must surface
	// the error rather than fall back.
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", "wrong-token")
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", testMicroVMBinding())
	require.Error(t, err)
}

func TestHTTPControllerDispatcherFailsClosedOnHTTPError(t *testing.T) {
	t.Parallel()
	// A server returning 500 with no SafeError body must surface a loud error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	t.Cleanup(srv.Close)
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", "any")
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", testMicroVMBinding())
	require.Error(t, err)
}

func TestHTTPControllerDispatcherFailsClosedOnUnreachableEndpoint(t *testing.T) {
	t.Parallel()
	// A closed server simulates an unreachable controller endpoint.
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	srv.Close()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	_, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", testMicroVMBinding())
	require.Error(t, err)
}

// TestHTTPControllerDispatcherLifecycleRefFromResponseFields proves
// MicroVMLifecycleRefFromResponse populates the three MicroVM execution/cache
// fields (MicroVMID, ExecutionStateRef via LifecycleState, MicroVMLifecycleRef)
// from the HTTP response body — the grep-proof that the HTTP response shape maps
// to the same ref the in-process path produced.
func TestHTTPControllerDispatcherLifecycleRefFromResponseFields(t *testing.T) {
	t.Parallel()
	binding := testMicroVMBinding()
	now := time.Now().UTC()
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         "req-fields",
		TenantID:          binding.TenantID(),
		Namespace:         MicroVMNamespace,
		SessionID:         binding.ConversationID,
		State:             runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		ProviderMicroVMID: "microvm-id-from-http",
		ProviderState:     "running",
		LastAction:        runtimemicrovm.CommandRun,
		LastTransition:    now,
		RegistryVersion:   7,
		ExpiresAt:         now.Add(time.Hour),
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, resp, now)
	require.NoError(t, err)
	require.Equal(t, "microvm-id-from-http", ref.MicroVMID, "MicroVMID must populate from HTTP ProviderMicroVMID")
	require.Equal(t, runtimemicrovm.StateRunning, ref.LifecycleState, "LifecycleState must populate from HTTP response")
	require.Equal(t, int64(7), ref.RegistryVersion, "RegistryVersion must populate from HTTP response")
	// ExecutionStateRef is the compact string Host records; it must reflect the
	// HTTP-derived lifecycle state + registry version (the three MicroVM
	// execution/cache fields Host records on HostedGenesisSession).
	require.Contains(t, FormatMicroVMExecutionStateRef(ref), "#running@7",
		"FormatMicroVMExecutionStateRef must reflect the HTTP-derived LifecycleState + RegistryVersion")
}

// TestH1_4_MicroVMReconcileIsTerminalClassification proves the H1.4 terminal
// classification used by the reconcile seam maps dead/expired sessions to
// terminal=true and live sessions to terminal=false across every branch: a
// terminal lifecycle state is terminal regardless of expiry; a non-terminal
// state with a past expiry is terminal (expired/dead); a non-terminal state
// with a future expiry is NOT terminal (live); a non-terminal state with no
// reported expiry is NOT terminal. This is the lifecycle-state coverage H1.4
// adds on top of H1.3's terminated/failed mapping: expiry-in-the-past is a
// dead VM even when the lifecycle state is non-terminal (e.g. stopped).
func TestH1_4_MicroVMReconcileIsTerminalClassification(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		state     runtimemicrovm.LifecycleState
		expiresAt time.Time
		want      bool
	}{
		{"terminated is terminal regardless of expiry", runtimemicrovm.StateTerminated, observedAt.Add(-time.Hour), true},
		{"failed is terminal regardless of expiry", runtimemicrovm.StateFailed, observedAt.Add(time.Hour), true},
		{"non-terminal with past expiry is terminal (expired)", runtimemicrovm.StateStopped, observedAt.Add(-time.Minute), true},
		{"non-terminal with expiry exactly at observation is terminal", runtimemicrovm.StateStopped, observedAt, true},
		{"non-terminal with future expiry is not terminal (live)", runtimemicrovm.StateRunning, observedAt.Add(time.Hour), false},
		{"non-terminal with no reported expiry is not terminal", runtimemicrovm.StateRunning, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := microVMReconcileIsTerminal(tc.state, tc.expiresAt, observedAt)
			require.Equal(t, tc.want, got)
		})
	}
}
