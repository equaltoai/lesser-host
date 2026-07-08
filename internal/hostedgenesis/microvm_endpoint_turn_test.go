package hostedgenesis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/testkit/microvm"
)

// fakeEndpointSDK is the test double for the raw lambda-microvms SDK client
// RunTurnViaEndpoint uses to bridge the framework gap (get-microvm for the
// Endpoint + create-microvm-auth-token for the token value). It is a strict
// subset of the framework's lambdaMicroVMAPI.
type fakeEndpointSDK struct {
	endpoint       string
	state          lambdatypes.MicrovmState
	stateSequence  []lambdatypes.MicrovmState
	getErr         error
	getCalls       atomic.Int32
	token          string
	tokenErr       error
	allowPortsSeen []lambdatypes.PortSpecification
}

func (f *fakeEndpointSDK) GetMicrovm(_ context.Context, _ *lambdamicrovms.GetMicrovmInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.GetMicrovmOutput, error) {
	call := int(f.getCalls.Add(1)) - 1
	if f.getErr != nil {
		return nil, f.getErr
	}
	state := f.state
	if call < len(f.stateSequence) {
		state = f.stateSequence[call]
	}
	return &lambdamicrovms.GetMicrovmOutput{
		Endpoint:  strPtr(f.endpoint),
		State:     state,
		MicrovmId: strPtr("microvm-000001"),
	}, nil
}

func (f *fakeEndpointSDK) CreateMicrovmAuthToken(_ context.Context, in *lambdamicrovms.CreateMicrovmAuthTokenInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.CreateMicrovmAuthTokenOutput, error) {
	if f.tokenErr != nil {
		return nil, f.tokenErr
	}
	f.allowPortsSeen = append([]lambdatypes.PortSpecification(nil), in.AllowedPorts...)
	token := f.token
	if token == "" {
		token = "proxy-token-value"
	}
	return &lambdamicrovms.CreateMicrovmAuthTokenOutput{
		AuthToken: map[string]string{"X-aws-proxy-auth": token},
	}, nil
}

func strPtr(s string) *string { return &s }

// newEndpointTurnTestServer stands up the workload's run hook in-process. It
// records the received headers + body and returns a running LifecycleResult by
// default; the header/body assertions can be customized via the returned hooks.
func newEndpointTurnTestServer(t *testing.T, status int, result *runtimemicrovm.LifecycleResult) (*httptest.Server, *endpointTurnServerState) {
	t.Helper()
	state := &endpointTurnServerState{}
	mux := http.NewServeMux()
	mux.HandleFunc(MicroVMTurnEndpointPath, func(w http.ResponseWriter, r *http.Request) {
		state.authHeader = r.Header.Get("X-aws-proxy-auth")
		state.portHeader = r.Header.Get("X-aws-proxy-port")
		state.contentType = r.Header.Get("content-type")
		_ = json.NewDecoder(r.Body).Decode(&state.event)
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		if result != nil {
			_ = json.NewEncoder(w).Encode(*result)
		} else if status >= 200 && status < 300 {
			_ = json.NewEncoder(w).Encode(runtimemicrovm.LifecycleResult{
				RequestID: state.event.RequestID,
				TenantID:  state.event.TenantID,
				Namespace: state.event.Namespace,
				SessionID: state.event.SessionID,
				Hook:      runtimemicrovm.HookRun,
				State:     runtimemicrovm.StateRunning,
			})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type endpointTurnServerState struct {
	authHeader  string
	portHeader  string
	contentType string
	event       runtimemicrovm.LifecycleEvent
}

// newEndpointTurnRuntime builds a MicroVMControllerRuntime wired with the fake
// provider + an EndpointTurnClient pointing at the fake SDK + HTTP client.
func newEndpointTurnRuntime(t *testing.T, sdk microvmEndpointAPI, httpClient *http.Client, opts ...func(*MicroVMControllerRuntimeConfig)) *MicroVMControllerRuntime {
	t.Helper()
	cfg := MicroVMControllerRuntimeConfig{
		Provider:                    microvmtestkit.NewFakeProviderWithTime(time.Date(2026, 6, 25, 20, 0, 0, 0, time.UTC)),
		Registry:                    runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:                    "arn:aws:lambda:us-east-1:123456789012:microvm-image/hosted-genesis:1",
		NetworkConnectorRef:         "arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress",
		IngressNetworkConnectorRefs: []string{"HTTP_INGRESS"},
		EgressNetworkConnectorRefs:  []string{"arn:aws:lambda:us-east-1:123456789012:network-connector/hosted-genesis-egress"},
		EndpointTurnClient: EndpointTurnClient{
			SDKClient:    sdk,
			HTTPClient:   httpClient,
			ReadyTimeout: 5 * time.Second,
			PollInterval: 10 * time.Millisecond,
			TurnTimeout:  5 * time.Second,
			now:          func() time.Time { return time.Date(2026, 6, 25, 20, 0, 1, 0, time.UTC) },
		},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	runtime, err := NewMicroVMControllerRuntime(cfg)
	if err != nil {
		t.Fatalf("NewMicroVMControllerRuntime: %v", err)
	}
	return runtime
}

// endpointTurnTestSessionID / endpointTurnTestProxyToken are the repeated test
// fixtures hoisted to constants to satisfy goconst (>=3 occurrences).
const (
	endpointTurnTestSessionID  = "conv_123"
	endpointTurnTestProxyToken = "proxy-token-value"
)

func TestNormalizeMicroVMEndpointURLPrefixesBareAWSHost(t *testing.T) {
	t.Parallel()
	got, err := normalizeMicroVMEndpointURL(" 0ccf9f2b-f186-fbdf-0768-f47712b635d4.lambda-microvm.us-east-1.on.aws/ ")
	requireNoError(t, err)
	if got != "https://0ccf9f2b-f186-fbdf-0768-f47712b635d4.lambda-microvm.us-east-1.on.aws" {
		t.Fatalf("expected AWS MicroVM endpoint host to gain https scheme, got %q", got)
	}
}

func TestNormalizeMicroVMEndpointURLPreservesExplicitHTTPTestEndpoint(t *testing.T) {
	t.Parallel()
	got, err := normalizeMicroVMEndpointURL("http://127.0.0.1:8080/")
	requireNoError(t, err)
	if got != "http://127.0.0.1:8080" {
		t.Fatalf("expected explicit httptest endpoint to be preserved, got %q", got)
	}
}

func TestNormalizeMicroVMEndpointURLRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	_, err := normalizeMicroVMEndpointURL("ftp://lambda-microvm.example")
	if !errors.Is(err, ErrMicroVMEndpointMissing) {
		t.Fatalf("expected ErrMicroVMEndpointMissing, got %v", err)
	}
}

func TestRunTurnViaEndpointHappyPath(t *testing.T) {
	t.Parallel()
	result, _, _ := runTurnViaEndpointHappy(t)

	// Run command envelope: the framework started the MicroVM (running, session
	// bound); the lifecycle ref maps back to the binding.
	if result.RunResponse.Command != runtimemicrovm.CommandRun {
		t.Fatalf("expected run command, got %q", result.RunResponse.Command)
	}
	if result.RunResponse.SessionID != endpointTurnTestSessionID {
		t.Fatalf("expected session %s, got %q", endpointTurnTestSessionID, result.RunResponse.SessionID)
	}
	if result.RunResponse.State != runtimemicrovm.StateRunning {
		t.Fatalf("expected running state, got %q", result.RunResponse.State)
	}
	if err := result.LifecycleRef.Validate(testMicroVMBinding()); err != nil {
		t.Fatalf("lifecycle ref invalid: %v", err)
	}
	// Turn result: the workload's run hook returned running.
	if result.TurnResult.Hook != runtimemicrovm.HookRun {
		t.Fatalf("expected turn hook run, got %q", result.TurnResult.Hook)
	}
	if result.TurnResult.State != runtimemicrovm.StateRunning {
		t.Fatalf("expected turn state running, got %q", result.TurnResult.State)
	}
}

func TestRunTurnViaEndpointHappyPathPOSTAndPortScope(t *testing.T) {
	t.Parallel()
	_, state, sdk := runTurnViaEndpointHappy(t)

	// The POST carried the scoped proxy-auth token + the run-hook port.
	if state.authHeader != endpointTurnTestProxyToken {
		t.Fatalf("expected X-aws-proxy-auth header, got %q", state.authHeader)
	}
	if state.portHeader != "8080" {
		t.Fatalf("expected X-aws-proxy-port 8080, got %q", state.portHeader)
	}
	if state.contentType != "application/json" {
		t.Fatalf("expected content-type application/json, got %q", state.contentType)
	}
	// The POST body is an M16 LifecycleEvent with the slug-boundary tenant_id
	// + the hosted-genesis namespace + the session + run hook + running state.
	if state.event.TenantID != "slug:demo" {
		t.Fatalf("expected tenant slug:demo, got %q", state.event.TenantID)
	}
	if state.event.Namespace != MicroVMNamespace {
		t.Fatalf("expected namespace %q, got %q", MicroVMNamespace, state.event.Namespace)
	}
	if state.event.SessionID != endpointTurnTestSessionID {
		t.Fatalf("expected session %s, got %q", endpointTurnTestSessionID, state.event.SessionID)
	}
	if state.event.Hook != runtimemicrovm.HookRun {
		t.Fatalf("expected hook run, got %q", state.event.Hook)
	}
	if state.event.State != runtimemicrovm.StateRunning {
		t.Fatalf("expected state running, got %q", state.event.State)
	}
	// Metadata carries the HostedGenesisSession ids (no raw credentials).
	if state.event.Metadata["conversation_id"] != endpointTurnTestSessionID || state.event.Metadata["turn_id"] != "turn_123" || state.event.Metadata["agent_id"] != "agent_123" {
		t.Fatalf("expected hosted-genesis metadata, got %#v", state.event.Metadata)
	}
	// The auth token was scoped to all ports on the MicroVM (allPorts).
	if len(sdk.allowPortsSeen) != 1 {
		t.Fatalf("expected one allowed-port spec, got %d", len(sdk.allowPortsSeen))
	}
	if _, ok := sdk.allowPortsSeen[0].(*lambdatypes.PortSpecificationMemberAllPorts); !ok {
		t.Fatalf("expected allPorts spec, got %T", sdk.allowPortsSeen[0])
	}
	// RUNNING on the first get-microvm poll => no wasted polling.
	if sdk.getCalls.Load() != 1 {
		t.Fatalf("expected one get-microvm call, got %d", sdk.getCalls.Load())
	}
}

// runTurnViaEndpointHappy is the shared happy-path fixture: a fake SDK returning
// RUNNING at the test server endpoint + a workload run-hook test server. It
// returns the turn result, the recorded server state, and the fake SDK so the
// two happy-path tests can assert disjoint facets without re-running the flow.
func runTurnViaEndpointHappy(t *testing.T) (EndpointTurnResult, *endpointTurnServerState, *fakeEndpointSDK) {
	t.Helper()
	srv, state := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())
	result, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-1", testMicroVMBinding(), EndpointTurnClient{})
	requireNoError(t, err)
	return result, state, sdk
}

func TestRunTurnViaEndpointPollsUntilRunning(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{
		endpoint:      srv.URL,
		stateSequence: []lambdatypes.MicrovmState{lambdatypes.MicrovmStatePending, lambdatypes.MicrovmStatePending, lambdatypes.MicrovmStateRunning},
	}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	result, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-2", testMicroVMBinding(), EndpointTurnClient{})
	requireNoError(t, err)
	if result.TurnResult.State != runtimemicrovm.StateRunning {
		t.Fatalf("expected turn state running, got %q", result.TurnResult.State)
	}
	// Polled PENDING twice then RUNNING.
	if sdk.getCalls.Load() != 3 {
		t.Fatalf("expected three get-microvm calls (PENDING,PENDING,RUNNING), got %d", sdk.getCalls.Load())
	}
}

func TestRunTurnViaEndpointFailClosedOnNilClient(t *testing.T) {
	t.Parallel()
	runtime := newEndpointTurnRuntime(t, nil, &http.Client{}, func(cfg *MicroVMControllerRuntimeConfig) {
		cfg.EndpointTurnClient.SDKClient = nil // simulate a missing SDK client (production fail-closed)
	})
	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-3", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMEndpointTurnUnavailable) {
		t.Fatalf("expected ErrMicroVMEndpointTurnUnavailable, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnNilHTTPClient(t *testing.T) {
	t.Parallel()
	sdk := &fakeEndpointSDK{endpoint: "https://example.internal", state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, nil, func(cfg *MicroVMControllerRuntimeConfig) {
		cfg.EndpointTurnClient.HTTPClient = nil
	})
	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-4", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMEndpointTurnUnavailable) {
		t.Fatalf("expected ErrMicroVMEndpointTurnUnavailable, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnInvalidBinding(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())
	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-5", MicroVMSessionBinding{}, EndpointTurnClient{})
	if err == nil {
		t.Fatalf("expected error for invalid binding, got nil")
	}
}

func TestRunTurnViaEndpointFailClosedOnEmptyRequestID(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())
	_, err := runtime.RunTurnViaEndpoint(context.Background(), "  ", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMEndpointTurnUnavailable) {
		t.Fatalf("expected ErrMicroVMEndpointTurnUnavailable, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnNeverRunning(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStatePending} // never RUNNING
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client(), func(cfg *MicroVMControllerRuntimeConfig) {
		cfg.EndpointTurnClient.ReadyTimeout = 50 * time.Millisecond
		cfg.EndpointTurnClient.PollInterval = 10 * time.Millisecond
	})
	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-6", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMEndpointNotRunning) {
		t.Fatalf("expected ErrMicroVMEndpointNotRunning, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnTerminalState(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateTerminated}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-7", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMEndpointNotRunning) {
		t.Fatalf("expected ErrMicroVMEndpointNotRunning on terminal state, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnMissingEndpoint(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: "", state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-8", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMEndpointMissing) {
		t.Fatalf("expected ErrMicroVMEndpointMissing, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnGetMicrovmError(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning, getErr: errors.New("aws: throttled")}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-9", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "get microvm") {
		t.Fatalf("expected get-microvm error, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnMissingToken(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning, tokenErr: errors.New("aws: denied")}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-10", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "create auth token") {
		t.Fatalf("expected create-auth-token error, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnEmptyTokenValue(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning, token: "   "}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-11", testMicroVMBinding(), EndpointTurnClient{})
	if !errors.Is(err, ErrMicroVMAuthTokenMissing) {
		t.Fatalf("expected ErrMicroVMAuthTokenMissing, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnNon2xxResponse(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, http.StatusInternalServerError, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-12", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnUnreachableEndpoint(t *testing.T) {
	t.Parallel()
	// An HTTP client pointed at a closed server => post error.
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, &http.Client{Timeout: 200 * time.Millisecond})
	srv.Close() // close immediately so the POST fails

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-13", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "post turn") {
		t.Fatalf("expected post-turn error, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnWorkloadSafeError(t *testing.T) {
	t.Parallel()
	result := &runtimemicrovm.LifecycleResult{
		RequestID: "req-ep-14",
		TenantID:  "slug:demo",
		Namespace: MicroVMNamespace,
		SessionID: endpointTurnTestSessionID,
		Hook:      runtimemicrovm.HookRun,
		State:     runtimemicrovm.StateFailed,
		Error:     &runtimemicrovm.SafeError{Code: "m15.microvm.lifecycle_hook_failed", Message: "workload turn failed"},
	}
	srv, _ := newEndpointTurnTestServer(t, http.StatusOK, result)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-14", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "workload failed") {
		t.Fatalf("expected workload SafeError to fail closed, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnEmptySuccessResult(t *testing.T) {
	t.Parallel()
	result := &runtimemicrovm.LifecycleResult{}
	srv, _ := newEndpointTurnTestServer(t, http.StatusOK, result)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-empty", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "request binding mismatch") {
		t.Fatalf("expected empty result to fail closed, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnWrongSessionResult(t *testing.T) {
	t.Parallel()
	result := &runtimemicrovm.LifecycleResult{
		RequestID: "req-ep-wrong",
		TenantID:  "slug:demo",
		Namespace: MicroVMNamespace,
		SessionID: "other-session",
		Hook:      runtimemicrovm.HookRun,
		State:     runtimemicrovm.StateRunning,
	}
	srv, _ := newEndpointTurnTestServer(t, http.StatusOK, result)
	sdk := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client())

	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-wrong", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil || !strings.Contains(err.Error(), "session binding mismatch") {
		t.Fatalf("expected wrong session result to fail closed, got %v", err)
	}
}

func TestRunTurnViaEndpointCallerClientOverridesRuntimeClient(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	// Runtime wired with a failing SDK; caller passes a working SDK client.
	runtime := newEndpointTurnRuntime(t, &fakeEndpointSDK{getErr: errors.New("runtime-sdk-broken")}, srv.Client())
	override := &fakeEndpointSDK{endpoint: srv.URL, state: lambdatypes.MicrovmStateRunning}
	_, err := runtime.RunTurnViaEndpoint(context.Background(), "req-ep-15", testMicroVMBinding(), EndpointTurnClient{
		SDKClient:  override,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("caller override should win, got %v", err)
	}
}

func TestRunTurnViaEndpointFailClosedOnContextCancelled(t *testing.T) {
	t.Parallel()
	srv, _ := newEndpointTurnTestServer(t, 0, nil)
	sdk := &fakeEndpointSDK{
		endpoint:      srv.URL,
		stateSequence: []lambdatypes.MicrovmState{lambdatypes.MicrovmStatePending}, // never RUNNING so the poll loop spins
	}
	runtime := newEndpointTurnRuntime(t, sdk, srv.Client(), func(cfg *MicroVMControllerRuntimeConfig) {
		cfg.EndpointTurnClient.ReadyTimeout = 10 * time.Second
		cfg.EndpointTurnClient.PollInterval = 5 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	_, err := runtime.RunTurnViaEndpoint(ctx, "req-ep-16", testMicroVMBinding(), EndpointTurnClient{})
	if err == nil {
		t.Fatalf("expected context-canceled error, got nil")
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
