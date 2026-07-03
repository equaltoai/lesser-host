package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
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

func TestControllerAppRegistersAppTheoryM16Routes(t *testing.T) {
	t.Parallel()
	app := testControllerApp(t)

	unauthorized := invoke(t, app, "POST", "/microvms", runBody("conv_123"), false)
	if unauthorized.StatusCode != 401 {
		t.Fatalf("expected missing auth to fail closed with 401, got %d body=%s", unauthorized.StatusCode, unauthorized.Body)
	}

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

func testControllerApp(t *testing.T) interface {
	ServeAPIGatewayV2(context.Context, events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPResponse
} {
	t.Helper()
	runtime, err := hostedgenesis.NewMicroVMControllerRuntime(hostedgenesis.MicroVMControllerRuntimeConfig{
		Provider:            microvmtestkit.NewFakeProviderWithTime(time.Date(2026, 6, 25, 20, 0, 0, 0, time.UTC)),
		Registry:            runtimemicrovm.NewMemorySessionRegistry(),
		ImageRef:            "image-ref",
		NetworkConnectorRef: "egress-ref",
		IngressNetworkConnectorRefs: []string{
			"ingress-ref",
		},
		EgressNetworkConnectorRefs: []string{"egress-ref"},
	})
	if err != nil {
		t.Fatalf("NewMicroVMControllerRuntime: %v", err)
	}
	app, err := newControllerApp(runtime.Controller(), func(key string) string {
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
	headers := map[string]string{
		"content-type":   "application/json",
		"x-request-id":   "req-route",
		"x-tenant-id":    "slug:demo",
		"x-namespace-id": hostedgenesis.MicroVMNamespace,
	}
	if authorized {
		headers["authorization"] = "Bearer lab-token"
	}
	return app.ServeAPIGatewayV2(context.Background(), events.APIGatewayV2HTTPRequest{
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
	})
}

func runBody(sessionID string) string {
	return `{"session_id":"` + sessionID + `","image_ref":"image-ref","network_connector_ref":"egress-ref","ingress_network_connector_refs":["ingress-ref"],"egress_network_connector_refs":["egress-ref"],"session_spec":{"metadata":{"source_of_truth":"host-dynamodb-hosted-genesis-session","registration_id":"reg_123","agent_id":"agent_123","conversation_id":"` + sessionID + `"}}}`
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
