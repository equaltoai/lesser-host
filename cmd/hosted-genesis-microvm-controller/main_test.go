package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	microvmtestkit "github.com/theory-cloud/apptheory/testkit/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

func TestControllerEventFailsClosedWhenDisabled(t *testing.T) {
	t.Parallel()
	event := events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{RequestID: "req-1"}}
	resp, err := handleControllerEvent(context.Background(), event, func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if strings.Contains(strings.ToLower(resp.Body), "token") || strings.Contains(strings.ToLower(resp.Body), "authorization") {
		t.Fatalf("fail-closed response leaked auth vocabulary: %s", resp.Body)
	}
}

// TestControllerEventFailsClosedWhenAuthLoosened proves the H1.5 de-lab-gating
// kept fail-closed auth: with STAGE set to a non-lab value but the auth env vars
// loosened (AUTH_REQUIRED not "true" or AUTH_DEFAULT not "deny"), the controller
// still refuses to serve (403). The runtime lab-gate is gone, but the
// authorizer-required + deny-by-default posture is intact across all stages.
func TestControllerEventFailsClosedWhenAuthLoosened(t *testing.T) {
	t.Parallel()
	event := events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{RequestID: "req-2"}}
	// Non-lab stage with fail-closed auth intact should NOT be rejected by a
	// lab gate (the lab gate is gone); but auth loosened must still 403.
	loosenedAuth := func(key string) string {
		switch key {
		case "STAGE":
			return "live"
		case "APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED":
			return "false"
		case "APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT":
			return runtimemicrovm.ControllerAuthDefaultDeny
		}
		return ""
	}
	resp, err := handleControllerEvent(context.Background(), event, loosenedAuth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 when auth is loosened (fail-closed preserved), got %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.Body), "fail-closed") {
		t.Fatalf("expected fail-closed message, got %s", resp.Body)
	}
}

// TestControllerEventGateNoLongerLabOnly is the grep-proof structural guard
// that the H1.5 de-lab-gating removed the runtime lab gate from the controller
// (the STAGE != "lab" disjunct is gone) while keeping the fail-closed auth
// checks (AUTH_REQUIRED == true, AUTH_DEFAULT == deny).
func TestControllerEventGateNoLongerLabOnly(t *testing.T) {
	t.Parallel()
	src := mustReadControllerSource(t, "main.go")
	if strings.Contains(src, `getenv("STAGE")) != "lab"`) {
		t.Fatalf("H1.5 regression: controller still lab-gates on STAGE != \"lab\"; the runtime lab gate should be removed (fail-closed auth preserved)")
	}
	if !strings.Contains(src, `getenv("APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED")) != "true"`) {
		t.Fatalf("H1.5 regression: fail-closed AUTH_REQUIRED check missing from controller gate")
	}
	if !strings.Contains(src, `getenv("APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT")) != runtimemicrovm.ControllerAuthDefaultDeny`) {
		t.Fatalf("H1.5 regression: fail-closed AUTH_DEFAULT deny check missing from controller gate")
	}
}

func TestRuntimeControllerUsesHostOwnedMicroVMRegistry(t *testing.T) {
	t.Parallel()
	src := mustReadControllerSource(t, "main.go")
	if strings.Contains(src, "tabletheory.LambdaInit(&runtimemicrovm.SessionRegistryRecord{})") ||
		strings.Contains(src, "NewTableTheorySessionRegistry") ||
		strings.Contains(src, "runtimemicrovm.NewMemorySessionRegistry()") {
		t.Fatalf("hosted-genesis controller must use Host's cache adapter, not the generic AppTheory TableTheory or in-memory registry")
	}
	if strings.Contains(src, "github.com/theory-cloud/tabletheory/v2") {
		t.Fatalf("hosted-genesis controller must not import TableTheory directly; store owns the TableTheory boundary")
	}
	if !strings.Contains(src, "store.NewHostedGenesisMicroVMRegistry") {
		t.Fatalf("expected controller to use the Host-owned MicroVM registry adapter")
	}
	if !strings.Contains(src, "HostedGenesisMicroVMReconstructionHook") {
		t.Fatalf("expected missing/stale MicroVM registry cache to reconstruct from Host HostedGenesisSession truth")
	}
}

func TestControllerAppRegistersAppTheoryM16Routes(t *testing.T) {
	t.Parallel()
	app := testControllerApp(t)

	unauthorized := invoke(t, app, "POST", "/microvms", runBody("conv_123"), false)
	if unauthorized.StatusCode != 401 {
		t.Fatalf("expected missing auth to fail closed with 401, got %d body=%s", unauthorized.StatusCode, unauthorized.Body)
	}

	// P52 H1: POST /microvms now executes the turn via the MicroVM endpoint
	// (RunTurnViaEndpoint) instead of only starting the MicroVM. The fake SDK
	// client + test HTTP server in testControllerApp simulate the workload's
	// run hook, so the run response still carries the run command, the session
	// id, and the running state.
	run := invokeOK(t, app, "POST", "/microvms", runBody("conv_123"))
	if run.Command != runtimemicrovm.CommandRun || run.SessionID != "conv_123" || run.State != runtimemicrovm.StateRunning {
		t.Fatalf("unexpected run response: %#v", run)
	}

	for _, route := range microVMRouteCases() {
		route := route
		t.Run(route.name, func(t *testing.T) {
			assertControllerRoute(t, app, route)
		})
	}
}

func TestControllerStagePathNormalizationSupportsDeployedHTTPAPIStages(t *testing.T) {
	t.Parallel()
	app := testControllerApp(t)

	event := controllerRequestEvent("POST", "/lab/microvms", runBody("conv_stage"), true)
	event = normalizeControllerStagePath(event, "lab")
	if event.RawPath != "/microvms" || event.RequestContext.HTTP.Path != "/microvms" || event.RouteKey != "POST /microvms" {
		t.Fatalf("stage normalization mismatch: route=%q raw=%q path=%q", event.RouteKey, event.RawPath, event.RequestContext.HTTP.Path)
	}
	response := app.ServeAPIGatewayV2(context.Background(), event)
	if response.StatusCode != 200 {
		t.Fatalf("expected normalized deployed-stage path to route, got %d body=%s", response.StatusCode, response.Body)
	}
	var payload runtimemicrovm.ControllerResponse
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v body=%s", err, response.Body)
	}
	if payload.Command != runtimemicrovm.CommandRun || payload.SessionID != "conv_stage" {
		t.Fatalf("unexpected normalized run response: %#v", payload)
	}

	unchanged := normalizeControllerStagePath(controllerRequestEvent("GET", "/labyrinth/microvms", "", true), "lab")
	if unchanged.RawPath != "/labyrinth/microvms" || unchanged.RequestContext.HTTP.Path != "/labyrinth/microvms" {
		t.Fatalf("stage normalization stripped a non-stage segment: raw=%q path=%q", unchanged.RawPath, unchanged.RequestContext.HTTP.Path)
	}
	root := normalizeControllerStagePath(controllerRequestEvent("GET", "/lab", "", true), "lab")
	if root.RawPath != "/" || root.RequestContext.HTTP.Path != "/" {
		t.Fatalf("stage root normalization mismatch: raw=%q path=%q", root.RawPath, root.RequestContext.HTTP.Path)
	}
}

func TestControllerRunTurnRouteFailsClosedForMalformedOrIncompleteBinding(t *testing.T) {
	t.Parallel()
	app := testControllerApp(t)

	malformed := invoke(t, app, "POST", "/microvms", "{", true)
	if malformed.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected malformed run-turn request to fail with 400, got %d body=%s", malformed.StatusCode, malformed.Body)
	}
	assertControllerSafeError(t, malformed, runtimemicrovm.ErrorCodeInvalidControllerRequest)

	incomplete := invoke(t, app, "POST", "/microvms", `{}`, true)
	if incomplete.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected incomplete run-turn binding to fail with 400, got %d body=%s", incomplete.StatusCode, incomplete.Body)
	}
	assertControllerSafeError(t, incomplete, runtimemicrovm.ErrorCodeInvalidControllerRequest)
}

type microVMRouteCase struct {
	name    string
	method  string
	path    string
	body    string
	command runtimemicrovm.Command
}

func microVMRouteCases() []microVMRouteCase {
	return []microVMRouteCase{
		{"get", "GET", "/microvms/conv_123", "{}", runtimemicrovm.CommandGet},
		{"list", "GET", "/microvms", "", runtimemicrovm.CommandList},
		{"suspend", "POST", "/microvms/conv_123/suspend", "{}", runtimemicrovm.CommandSuspend},
		{"resume", "POST", "/microvms/conv_123/resume", "{}", runtimemicrovm.CommandResume},
		{"auth-token", "POST", "/microvms/conv_123/auth-token", `{"allowed_port_scope":[{"port":443}]}`, runtimemicrovm.CommandAuthToken},
		{"shell-auth-token", "POST", "/microvms/conv_123/shell-auth-token", "{}", runtimemicrovm.CommandShellAuthToken},
		{"terminate", "DELETE", "/microvms/conv_123", "{}", runtimemicrovm.CommandTerminate},
	}
}

func assertControllerRoute(t *testing.T, app interface {
	ServeAPIGatewayV2(context.Context, events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse
}, route microVMRouteCase) {
	t.Helper()
	got := invokeOK(t, app, route.method, route.path, route.body)
	if got.Command != route.command {
		t.Fatalf("expected command %q, got %#v", route.command, got)
	}
	if route.command == runtimemicrovm.CommandAuthToken || route.command == runtimemicrovm.CommandShellAuthToken {
		assertTokenResponseSanitized(t, got)
	}
}

func assertTokenResponseSanitized(t *testing.T, got runtimemicrovm.ControllerResponse) {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"token_value", "bearer_token", "provider_token", "x-aws-proxy-auth", "lab-token"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("token response leaked %q: %s", forbidden, encoded)
		}
	}
}

func assertControllerSafeError(t *testing.T, response events.APIGatewayV2HTTPResponse, code string) {
	t.Helper()
	var payload runtimemicrovm.ControllerResponse
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v body=%s", err, response.Body)
	}
	if payload.Error == nil || payload.Error.Code != code {
		t.Fatalf("expected safe error %q, got %#v", code, payload.Error)
	}
}

func TestControllerAuthHookRequiresHashedBearerAndTenantHeaders(t *testing.T) {
	t.Parallel()
	getenv := func(key string) string {
		if key == "HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256" {
			return "sha256:" + tokenHash("lab-token")
		}
		return ""
	}
	if !bearerMatchesHash("Bearer lab-token", getenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256")) {
		t.Fatal("expected hashed bearer to match")
	}
	if bearerMatchesHash("Bearer wrong", getenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256")) {
		t.Fatal("expected wrong bearer to fail")
	}
	if bearerMatchesHash("lab-token", getenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256")) {
		t.Fatal("expected missing bearer prefix to fail")
	}
}

func TestControllerCSVAndFirstStringNormalize(t *testing.T) {
	t.Parallel()

	if got := firstString([]string{"", "  ", " egress-ref ", "other"}); got != "egress-ref" {
		t.Fatalf("firstString trimmed first non-empty value incorrectly: %q", got)
	}
	if got := firstString(nil); got != "" {
		t.Fatalf("firstString nil = %q, want empty", got)
	}
	if got := csv(" ingress-ref, ,egress-ref "); len(got) != 2 || got[0] != "ingress-ref" || got[1] != "egress-ref" {
		t.Fatalf("csv normalization mismatch: %#v", got)
	}
	if got := csv(""); len(got) != 0 {
		t.Fatalf("csv empty = %#v, want none", got)
	}
}

func TestControllerHTTPStatusMapping(t *testing.T) {
	t.Parallel()

	requireStatus := func(code string, want int) {
		t.Helper()
		if got := controllerHTTPStatus(&runtimemicrovm.SafeError{Code: code}); got != want {
			t.Fatalf("controllerHTTPStatus(%q) = %d, want %d", code, got, want)
		}
	}
	if got := controllerHTTPStatus(nil); got != http.StatusOK {
		t.Fatalf("nil safe error status = %d, want 200", got)
	}
	requireStatus(runtimemicrovm.ErrorCodeUnauthenticatedController, http.StatusUnauthorized)
	requireStatus(runtimemicrovm.ErrorCodeTenantBindingViolation, http.StatusForbidden)
	requireStatus(runtimemicrovm.ErrorCodeSessionRegistryIncomplete, http.StatusNotFound)
	requireStatus(runtimemicrovm.ErrorCodeControllerIncomplete, http.StatusInternalServerError)
	requireStatus(runtimemicrovm.ErrorCodeControllerCommandFailed, http.StatusBadGateway)
	requireStatus(runtimemicrovm.ErrorCodeProviderOperationFailed, http.StatusBadGateway)
	requireStatus("other", http.StatusBadRequest)
}

func TestControllerRunTurnSafeErrorHelpers(t *testing.T) {
	t.Parallel()

	safe := runTurnSafeError(errors.New(" provider failed "), " req-123 ")
	if safe.Code != runtimemicrovm.ErrorCodeControllerCommandFailed || safe.RequestID != "req-123" || safe.Message != "provider failed" {
		t.Fatalf("unexpected run-turn safe error: %#v", safe)
	}
	resp := runTurnErrorResponse(" req-123 ", safe)
	if resp.Command != runtimemicrovm.CommandRun || resp.RequestID != "req-123" || resp.Error != safe {
		t.Fatalf("unexpected run-turn error response: %#v", resp)
	}
	microvmErr := safeErrorMicrovm(" code ", " message ")
	if microvmErr.Code != " code " || microvmErr.Message != " message " {
		t.Fatalf("safeErrorMicrovm should preserve exact framework code/message: %#v", microvmErr)
	}
}

func testControllerApp(t *testing.T) interface {
	ServeAPIGatewayV2(context.Context, events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse
} {
	t.Helper()
	endpoint := newEndpointTurnTestServer(t)
	runtime, err := hostedgenesis.NewMicroVMControllerRuntime(hostedgenesis.MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProviderWithTime(time.Date(2026, 6, 25, 20, 0, 0, 0, time.UTC)),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "egress-ref",
		IngressNetworkConnectorRefs: []string{
			"ingress-ref",
		},
		EgressNetworkConnectorRefs: []string{"egress-ref"},
		EndpointTurnClient: hostedgenesis.EndpointTurnClient{
			SDKClient:  &fakeEndpointSDK{endpoint: endpoint},
			HTTPClient: &http.Client{},
		},
	})
	if err != nil {
		t.Fatalf("NewMicroVMControllerRuntime: %v", err)
	}
	app, err := newControllerApp(runtime, func(key string) string {
		if key == "HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256" {
			return "sha256:" + tokenHash("lab-token")
		}
		return ""
	})
	if err != nil {
		t.Fatalf("newControllerApp: %v", err)
	}
	return app
}

// fakeEndpointSDK is the test lambda-microvms SDK client. It returns a RUNNING
// MicroVM at the test HTTP server's endpoint (so RunTurnViaEndpoint's
// get-microvm poll resolves immediately) and a fixed X-aws-proxy-auth token.
// It is the framework-gap bridge test double: the real SDK client loads the
// controller Lambda's execution-role AWS config.
type fakeEndpointSDK struct {
	endpoint string
	getCalls atomic.Int32
}

func (f *fakeEndpointSDK) GetMicrovm(_ context.Context, _ *lambdamicrovms.GetMicrovmInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.GetMicrovmOutput, error) {
	f.getCalls.Add(1)
	return &lambdamicrovms.GetMicrovmOutput{
		Endpoint:  ptrString(f.endpoint),
		State:     lambdatypes.MicrovmStateRunning,
		MicrovmId: ptrString("microvm-000001"),
	}, nil
}

func (f *fakeEndpointSDK) CreateMicrovmAuthToken(_ context.Context, _ *lambdamicrovms.CreateMicrovmAuthTokenInput, _ ...func(*lambdamicrovms.Options)) (*lambdamicrovms.CreateMicrovmAuthTokenOutput, error) {
	return &lambdamicrovms.CreateMicrovmAuthTokenOutput{
		AuthToken: map[string]string{"X-aws-proxy-auth": "test-proxy-token"},
	}, nil
}

// newEndpointTurnTestServer stands up the workload's run hook in-process for
// the endpoint-POST turn tests. It asserts the proxy-auth + proxy-port headers
// arrive and returns a running LifecycleResult.
func newEndpointTurnTestServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != hostedgenesis.MicroVMTurnEndpointPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-aws-proxy-auth") != "test-proxy-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-aws-proxy-port") != "8080" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var event runtimemicrovm.LifecycleEvent
		_ = json.NewDecoder(r.Body).Decode(&event)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(runtimemicrovm.LifecycleResult{
			RequestID: event.RequestID,
			TenantID:  event.TenantID,
			Namespace: event.Namespace,
			SessionID: event.SessionID,
			Hook:      runtimemicrovm.HookRun,
			State:     runtimemicrovm.StateRunning,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func ptrString(s string) *string { return &s }

func invokeOK(t *testing.T, app interface {
	ServeAPIGatewayV2(context.Context, events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse
}, method string, path string, body string) runtimemicrovm.ControllerResponse {
	t.Helper()
	response := invoke(t, app, method, path, body, true)
	if response.StatusCode != 200 {
		t.Fatalf("expected 200 from %s %s, got %d body=%s", method, path, response.StatusCode, response.Body)
	}
	var payload runtimemicrovm.ControllerResponse
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v body=%s", err, response.Body)
	}
	if payload.Error != nil {
		t.Fatalf("expected success payload, got %#v", payload.Error)
	}
	return payload
}

func invoke(t *testing.T, app interface {
	ServeAPIGatewayV2(context.Context, events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse
}, method string, path string, body string, authorized bool) events.APIGatewayV2HTTPResponse {
	t.Helper()
	return app.ServeAPIGatewayV2(context.Background(), controllerRequestEvent(method, path, body, authorized))
}

func controllerRequestEvent(method string, path string, body string, authorized bool) events.APIGatewayV2HTTPRequest {
	headers := map[string]string{
		"content-type":   "application/json",
		"x-request-id":   "req-route",
		"x-tenant-id":    "slug:demo",
		"x-namespace-id": hostedgenesis.MicroVMNamespace,
	}
	if authorized {
		headers["authorization"] = "Bearer lab-token"
	}
	return events.APIGatewayV2HTTPRequest{
		RouteKey: method + " " + path,
		RawPath:  path,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: "req-route",
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: method,
				Path:   path,
			},
		},
		Headers: headers,
		Body:    body,
	}
}

func runBody(sessionID string) string {
	return `{"session_id":"` + sessionID + `","image_ref":"image-ref","network_connector_ref":"egress-ref","ingress_network_connector_refs":["ingress-ref"],"egress_network_connector_refs":["egress-ref"],"session_spec":{"metadata":{"source_of_truth":"host-dynamodb-hosted-genesis-session","registration_id":"reg_123","agent_id":"agent_123","conversation_id":"` + sessionID + `","turn_id":"turn_123"}}}`
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// mustReadControllerSource reads a file from this cmd package for grep-proof
// structural guards. It fails the test if the file cannot be read.
func mustReadControllerSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}
