package hostedgenesis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
// AppTheoryMicrovmController HTTP API. It serves POST /microvms (run), GET
// /microvms/{session_id} (get), POST /microvms/{session_id}/resume, and
// POST /microvms/{session_id}/invoke/hosted-genesis/turn (canonical invoke),
// serializing the same ControllerResponse / LifecycleResult JSON shapes the
// real controller route handlers emit, so the HTTPControllerDispatcher under
// test exercises the real HTTP transport + response decoding path. It validates
// the Authorization bearer header + x-tenant-id + x-namespace-id the dispatcher
// presents, and lets a test terminate a session so a subsequent get observes a
// terminal state (the kill-VM recovery arc).
type stubControllerServer struct {
	t             *testing.T
	token         string
	mu            sync.Mutex
	sessions      map[string]runtimemicrovm.ControllerResponse
	invokes       int
	gets          int
	resumes       int
	terminates    int
	runs          int
	runState      runtimemicrovm.LifecycleState
	getState      runtimemicrovm.LifecycleState
	invokeFailure *runtimemicrovm.SafeError
	runPayloads   []microvmRunRequestPayload
	runBodies     []string
	operations    []string
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
	mux.HandleFunc("/microvms/", s.handleMicroVMRoute)
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

func (s *stubControllerServer) handleMicroVMRoute(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/invoke") {
		s.handleInvoke(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/resume") {
		s.handleResume(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		s.handleTerminate(w, r)
		return
	}
	s.handleGet(w, r)
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
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		writeControllerJSON(w, http.StatusBadRequest, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.invalid_controller_request", Message: "malformed"},
		})
		return
	}
	var payload microvmRunRequestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		writeControllerJSON(w, http.StatusBadRequest, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.invalid_controller_request", Message: "malformed"},
		})
		return
	}
	s.recordRunPayload(payload, string(body))
	sessionID := strings.TrimSpace(payload.SessionID)
	if sessionID == "" {
		sessionID = "stub-session"
	}
	now := time.Now().UTC()
	runState := s.runState
	if runState == "" {
		runState = runtimemicrovm.StateRunning
	}
	s.mu.Lock()
	s.runs++
	s.operations = append(s.operations, "run")
	runGeneration := s.runs
	s.mu.Unlock()
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         strings.TrimSpace(r.Header.Get("x-request-id")),
		TenantID:          strings.TrimSpace(r.Header.Get("x-tenant-id")),
		Namespace:         strings.TrimSpace(r.Header.Get("x-namespace-id")),
		SessionID:         sessionID,
		State:             runState,
		LifecycleState:    runState,
		DesiredState:      runtimemicrovm.StateRunning,
		ProviderMicroVMID: fmt.Sprintf("stub-microvm-%s-%d", sessionID, runGeneration),
		ProviderState:     string(runState),
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

func (s *stubControllerServer) handleTerminate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(r) {
		writeControllerJSON(w, http.StatusUnauthorized, runtimemicrovm.ControllerResponse{Error: &runtimemicrovm.SafeError{Code: "m15.microvm.unauthenticated_controller", Message: "unauthorized"}})
		return
	}
	sessionID := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/microvms/"), "/")
	now := time.Now().UTC()
	s.mu.Lock()
	resp, ok := s.sessions[sessionID]
	if ok {
		s.terminates++
		s.operations = append(s.operations, "terminate")
		resp.Command = runtimemicrovm.CommandTerminate
		resp.LastAction = runtimemicrovm.CommandTerminate
		resp.State = runtimemicrovm.StateTerminated
		resp.LifecycleState = runtimemicrovm.StateTerminated
		resp.DesiredState = runtimemicrovm.StateTerminated
		resp.ProviderState = string(runtimemicrovm.StateTerminated)
		resp.LastTransition = now
		resp.RegistryVersion++
		resp.ExpiresAt = now.Add(-time.Second)
		s.sessions[sessionID] = resp
	}
	s.mu.Unlock()
	if !ok {
		writeControllerJSON(w, http.StatusNotFound, runtimemicrovm.ControllerResponse{Error: &runtimemicrovm.SafeError{Code: "m15.microvm.session_registry_incomplete", Message: "session not found"}})
		return
	}
	writeControllerJSON(w, http.StatusOK, resp)
}

func (s *stubControllerServer) recordRunPayload(payload microvmRunRequestPayload, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runPayloads = append(s.runPayloads, payload)
	s.runBodies = append(s.runBodies, body)
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
	if strings.Contains(sessionID, "/") {
		writeControllerJSON(w, http.StatusNotFound, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.session_registry_incomplete", Message: "session not found"},
		})
		return
	}
	s.mu.Lock()
	resp, ok := s.sessions[sessionID]
	s.gets++
	s.operations = append(s.operations, "get")
	getState := s.getState
	s.mu.Unlock()
	if !ok {
		writeControllerJSON(w, http.StatusNotFound, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.session_registry_incomplete", Message: "session not found"},
		})
		return
	}
	if getState != "" {
		resp.State = getState
		resp.LifecycleState = getState
		resp.ProviderState = string(getState)
		resp.RegistryVersion++
	}
	resp.Command = runtimemicrovm.CommandGet
	resp.LastAction = runtimemicrovm.CommandGet
	resp.LastTransition = time.Now().UTC()
	writeControllerJSON(w, http.StatusOK, resp)
}

func (s *stubControllerServer) handleResume(w http.ResponseWriter, r *http.Request) {
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
	sessionID := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/microvms/"), "/"), "/resume")
	if strings.TrimSpace(sessionID) == "" || strings.Contains(sessionID, "/") {
		writeControllerJSON(w, http.StatusNotFound, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.session_registry_incomplete", Message: "session not found"},
		})
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	resp, ok := s.sessions[sessionID]
	if ok {
		s.resumes++
		resp.Command = runtimemicrovm.CommandResume
		resp.LastAction = runtimemicrovm.CommandResume
		resp.State = runtimemicrovm.StateReady
		resp.LifecycleState = runtimemicrovm.StateReady
		resp.DesiredState = runtimemicrovm.StateReady
		resp.ProviderState = string(runtimemicrovm.StateReady)
		resp.LastTransition = now
		resp.RegistryVersion++
		resp.ExpiresAt = now.Add(time.Hour)
		s.sessions[sessionID] = resp
	}
	s.mu.Unlock()
	if !ok {
		writeControllerJSON(w, http.StatusNotFound, runtimemicrovm.ControllerResponse{
			Error: &runtimemicrovm.SafeError{Code: "m15.microvm.session_registry_incomplete", Message: "session not found"},
		})
		return
	}
	writeControllerJSON(w, http.StatusOK, resp)
}

func (s *stubControllerServer) handleInvoke(w http.ResponseWriter, r *http.Request) {
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
	sessionID, ok := stubInvokeSessionID(r.URL.Path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !stubInvokeHeadersValid(r) {
		http.Error(w, "invalid invoke headers", http.StatusBadRequest)
		return
	}
	if !s.recordInvoke(sessionID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var event runtimemicrovm.LifecycleEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "malformed event", http.StatusBadRequest)
		return
	}
	if !stubInvokeEventMatches(r, event, sessionID) {
		http.Error(w, "binding mismatch", http.StatusForbidden)
		return
	}
	if s.invokeFailure != nil {
		writeLifecycleJSON(w, http.StatusOK, runtimemicrovm.LifecycleResult{
			RequestID: event.RequestID,
			TenantID:  event.TenantID,
			Namespace: event.Namespace,
			SessionID: event.SessionID,
			Hook:      runtimemicrovm.HookRun,
			State:     runtimemicrovm.StateFailed,
			Error:     s.invokeFailure,
		})
		return
	}
	writeLifecycleJSON(w, http.StatusOK, runtimemicrovm.LifecycleResult{
		RequestID:     event.RequestID,
		TenantID:      event.TenantID,
		Namespace:     event.Namespace,
		SessionID:     event.SessionID,
		Hook:          runtimemicrovm.HookRun,
		PreviousState: runtimemicrovm.StateRunning,
		State:         runtimemicrovm.StateRunning,
		Metadata:      event.Metadata,
	})
}

func stubInvokeSessionID(path string) (string, bool) {
	tail := strings.TrimPrefix(strings.TrimPrefix(path, "/microvms/"), "/")
	sessionID, invokePath, ok := strings.Cut(tail, "/invoke")
	if !ok || strings.TrimSpace(sessionID) == "" || invokePath != MicroVMTurnEndpointPath {
		return "", false
	}
	return strings.TrimSpace(sessionID), true
}

func stubInvokeHeadersValid(r *http.Request) bool {
	return r.Header.Get("x-apptheory-microvm-port") == "8080" &&
		r.Header.Get("x-aws-proxy-auth") == "" &&
		r.Header.Get("x-aws-proxy-port") == ""
}

func (s *stubControllerServer) recordInvoke(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[sessionID]; !exists {
		return false
	}
	s.invokes++
	s.operations = append(s.operations, "invoke")
	return true
}

func stubInvokeEventMatches(r *http.Request, event runtimemicrovm.LifecycleEvent, sessionID string) bool {
	return event.RequestID == strings.TrimSpace(r.Header.Get("x-request-id")) &&
		event.TenantID == strings.TrimSpace(r.Header.Get("x-tenant-id")) &&
		event.Namespace == strings.TrimSpace(r.Header.Get("x-namespace-id")) &&
		event.SessionID == sessionID &&
		event.Hook == runtimemicrovm.HookRun
}

// terminate marks a session terminated so a subsequent get observes a terminal
// state (the kill-VM recovery arc).
func (s *stubControllerServer) suspend(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, ok := s.sessions[sessionID]
	if !ok {
		return
	}
	resp.State = runtimemicrovm.StateSuspended
	resp.LifecycleState = runtimemicrovm.StateSuspended
	resp.DesiredState = runtimemicrovm.StateSuspended
	resp.ProviderState = "suspended"
	resp.LastAction = runtimemicrovm.CommandSuspend
	resp.LastTransition = time.Now().UTC()
	resp.ExpiresAt = time.Now().UTC().Add(time.Hour)
	s.sessions[sessionID] = resp
}

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

func (s *stubControllerServer) invokeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invokes
}

func (s *stubControllerServer) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func (s *stubControllerServer) resumeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resumes
}

func (s *stubControllerServer) terminateCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminates
}

func (s *stubControllerServer) runCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}

func (s *stubControllerServer) operationSequence() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.operations...)
}

func (s *stubControllerServer) lastRunPayload() (microvmRunRequestPayload, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runPayloads) == 0 {
		return microvmRunRequestPayload{}, "", false
	}
	return s.runPayloads[len(s.runPayloads)-1], s.runBodies[len(s.runBodies)-1], true
}

func writeControllerJSON(w http.ResponseWriter, status int, resp runtimemicrovm.ControllerResponse) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeLifecycleJSON(w http.ResponseWriter, status int, resp runtimemicrovm.LifecycleResult) {
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
		ImageVersion:        "29",
		ExecutionRoleARN:    "arn:aws:iam::123456789012:role/hosted-genesis-current",
		RuntimeLogGroup:     "/aws/lambda/microvms/hosted-genesis-current",
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
	require.Equal(t, 1, stub.invokeCount(), "dispatch must invoke the hosted-genesis turn through AppTheory's canonical controller invoke route")
}

func TestHTTPControllerDispatcherPreparesFreshCurrentRuntimeBeforeInvoke(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	previous, err := dispatcher.StartMicroVMRun(context.Background(), "req-old", binding)
	require.NoError(t, err)
	require.Equal(t, 1, stub.runCount())
	oldMicroVMID := previous.LifecycleRef.MicroVMID

	prepared, err := dispatcher.PrepareFreshMicroVMRun(context.Background(), "req-fresh", binding, previous.LifecycleRef)
	require.NoError(t, err)
	require.Equal(t, binding.ConversationID, prepared.SessionID, "Host/AppTheory session identity must remain the conversation id")
	require.Equal(t, 1, stub.terminateCount(), "old non-terminal runtime must be retired through AppTheory Terminate")
	require.Equal(t, 2, stub.runCount(), "fresh preparation must issue exactly one additional AppTheory Run")
	require.Equal(t, 0, stub.invokeCount(), "preparation must not invoke provider work before Host persists retry/debit truth")
	require.NotEqual(t, oldMicroVMID, prepared.LifecycleRef.MicroVMID)
	require.Equal(t, "29", prepared.LifecycleRef.ImageVersion)
	require.Equal(t, "arn:aws:iam::123456789012:role/hosted-genesis-current", prepared.LifecycleRef.ExecutionRoleARN)
	require.Equal(t, int32(300), prepared.LifecycleRef.MaximumDurationSeconds)
	require.Equal(t, "/aws/lambda/microvms/hosted-genesis-current", prepared.LifecycleRef.RuntimeLogGroup)
	payload, _, ok := stub.lastRunPayload()
	require.True(t, ok)
	require.Equal(t, "29", payload.ImageVersion, "fresh Run must pin the currently deployed image version")

	require.NoError(t, dispatcher.InvokeMicroVMTurn(context.Background(), "req-invoke", binding))
	require.Equal(t, 1, stub.invokeCount(), "governed dispatch invokes exactly once after preparation")
	require.Equal(t, []string{"run", "get", "terminate", "run", "get", "invoke"}, stub.operationSequence(), "fresh recovery must use AppTheory Get/Terminate/Run/readiness Get before Invoke")
}

func TestHTTPControllerDispatcherPassesAppTheoryRuntimeEnvelopeWithoutSecretsOrIdlePolicy(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	_, err := dispatcher.StartMicroVMRun(context.Background(), "req-lifetime", binding)
	require.NoError(t, err)

	payload, rawBody, ok := stub.lastRunPayload()
	require.True(t, ok, "expected stub controller to capture the run payload")
	require.Equal(t, binding.ConversationID, payload.SessionID)
	require.Equal(t, int32(300), payload.MaximumDurationSeconds)
	require.NoError(t, runtimemicrovm.ValidateProviderRunInput(runtimemicrovm.ProviderRunInput{
		RequestID:                   "req-lifetime",
		TenantID:                    binding.TenantID(),
		Namespace:                   MicroVMNamespace,
		SessionID:                   binding.ConversationID,
		AuthContext:                 authContext(binding),
		ImageRef:                    payload.ImageRef,
		NetworkConnectorRef:         payload.NetworkConnectorRef,
		IngressNetworkConnectorRefs: payload.IngressNetworkConnectorRefs,
		EgressNetworkConnectorRefs:  payload.EgressNetworkConnectorRefs,
		SessionSpec:                 payload.SessionSpec,
		MaximumDurationSeconds:      payload.MaximumDurationSeconds,
	}))
	rawLower := strings.ToLower(rawBody)
	require.NotContains(t, rawLower, "idle_policy", "asynchronous hosted-genesis work must omit AWS endpoint-idle suspension")
	for _, forbidden := range []string{
		"stub-bearer",
		"authorization",
		"bearer_token",
		"provider_key",
		"provider_secret",
		"aws_secret_access_key",
		"aws_access_key_id",
		"instance_api_key",
		"microvm_endpoint_token",
		"transcript",
		"messages",
		"prompt",
	} {
		require.NotContains(t, rawLower, forbidden, "controller run body must carry only AppTheory run metadata/runtime envelope")
	}
}

func TestHTTPControllerDispatcherRecordsReadyGETResponseAfterValidatingRun(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	stub.runState = runtimemicrovm.StateValidating
	stub.getState = runtimemicrovm.StateRunning
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	result, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-dispatch", binding)
	require.NoError(t, err)
	require.Equal(t, binding.ConversationID, result.SessionID)
	require.NoError(t, result.LifecycleRef.Validate(binding))
	require.GreaterOrEqual(t, stub.getCount(), 1, "dispatch must poll controller get when run returns a non-running state")
	require.Equal(t, 1, stub.invokeCount(), "dispatch must invoke after the ready get response")
	require.Equal(t, runtimemicrovm.StateRunning, result.LifecycleRef.LifecycleState, "dispatch must record the ready/running get response, not the original validating run response")
	require.Equal(t, int64(2), result.LifecycleRef.RegistryVersion, "ready get response should advance the recorded registry version")
}

func TestHTTPControllerDispatcherRejectsControllerVisibleWorkloadFailure(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	stub.invokeFailure = &runtimemicrovm.SafeError{Code: "m16.microvm.lifecycle_hook_failed", Message: "turn store unavailable"}
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	_, err := dispatcher.StartMicroVMRun(context.Background(), "req-dispatch", binding)
	require.NoError(t, err)
	_, err = dispatcher.WaitAndInvokeMicroVMTurn(context.Background(), "req-dispatch", binding)
	require.Error(t, err)
	require.Contains(t, err.Error(), "workload failed")
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
	require.False(t, result.CannotCompletePendingTurn, "live VM must not be terminal")
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
	require.True(t, result.CannotCompletePendingTurn, "terminated VM must reconcile as terminal")
	require.Equal(t, runtimemicrovm.StateTerminated, result.LifecycleRef.LifecycleState)
	require.NoError(t, result.LifecycleRef.Validate(binding))
}

func TestHTTPControllerDispatcherReconcileSuspendedAcceptedTurnRequiresDurableConvergence(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	dispatch, err := dispatcher.DispatchMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)
	stub.suspend(binding.ConversationID)

	result, err := dispatcher.ReconcileMicroVM(context.Background(), "req-observe-suspended", binding, dispatch.LifecycleRef)
	require.NoError(t, err)
	require.True(t, result.CannotCompletePendingTurn, "a suspended VM cannot prove an accepted provider stream remains runnable")
	require.Equal(t, runtimemicrovm.StateSuspended, result.LifecycleRef.LifecycleState)
	require.Equal(t, 0, stub.resumeCount(), "wait-only observation must never resume uncertain provider work")
}

func TestHTTPControllerDispatcherEnsuresSuspendedSessionViaResumeThenInvoke(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	started, err := dispatcher.StartMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)
	stub.suspend(binding.ConversationID)

	ensured, err := dispatcher.EnsureMicroVMTurnSession(context.Background(), "req-resume", binding, started.LifecycleRef)
	require.NoError(t, err)
	require.Equal(t, runtimemicrovm.StateReady, ensured.LifecycleRef.LifecycleState)
	require.Equal(t, runtimemicrovm.CommandResume, ensured.LifecycleRef.LastAction)
	require.Equal(t, 1, stub.resumeCount(), "suspended AppTheory sessions must resume through POST /microvms/{session_id}/resume")

	require.NoError(t, dispatcher.InvokeMicroVMTurn(context.Background(), "req-invoke", binding))
	require.Equal(t, 1, stub.invokeCount(), "ensured session should be invoked through AppTheory invoke route")
}

func TestHTTPControllerDispatcherEnsureTerminalSessionRequiresRelaunch(t *testing.T) {
	t.Parallel()
	stub := newStubControllerServer(t, "stub-bearer")
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)

	binding := testMicroVMBinding()
	dispatcher := testHTTPDispatcher(t, srv.URL+"/microvms", stub.token)
	started, err := dispatcher.StartMicroVMRun(context.Background(), "req-run", binding)
	require.NoError(t, err)
	stub.terminate(binding.ConversationID)

	_, err = dispatcher.EnsureMicroVMTurnSession(context.Background(), "req-ensure", binding, started.LifecycleRef)
	require.ErrorIs(t, err, ErrMicroVMRelaunchRequired)
	require.Equal(t, 0, stub.resumeCount(), "terminal sessions must not be resumed")
	require.Equal(t, 0, stub.invokeCount(), "terminal sessions must not be invoked")
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
		{"negative maximum duration", HTTPControllerDispatcherConfig{Endpoint: "https://example/microvms", AuthToken: "tok", ImageRef: "img", NetworkConnectorRef: "net", MaxDurationSeconds: -1, HTTPClient: client}},
		{"above AWS maximum duration", HTTPControllerDispatcherConfig{Endpoint: "https://example/microvms", AuthToken: "tok", ImageRef: "img", NetworkConnectorRef: "net", MaxDurationSeconds: 28801, HTTPClient: client}},
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

func TestMicroVMReconcileCannotCompletePendingTurnIncludesSuspended(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 7, 22, 13, 40, 0, 0, time.UTC)
	require.True(t, microVMReconcileCannotCompletePendingTurn(runtimemicrovm.StateSuspended, observedAt.Add(time.Hour), observedAt))
	require.True(t, microVMReconcileCannotCompletePendingTurn(runtimemicrovm.StateTerminated, time.Time{}, observedAt))
	require.False(t, microVMReconcileCannotCompletePendingTurn(runtimemicrovm.StateRunning, observedAt.Add(time.Hour), observedAt))
}
