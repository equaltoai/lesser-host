package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/observability"
	"github.com/equaltoai/lesser-host/internal/store"
)

const serviceName = "hosted-genesis-microvm-controller"

var controllerApp = apptheory.New(apptheory.WithObservability(observability.New(serviceName)))

func main() {
	if controllerApp == nil {
		panic("hosted genesis microvm controller observability not initialized")
	}
	lambda.Start(handle)
}

func handle(ctx context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	resp, err := handleControllerEvent(ctx, event, os.Getenv)
	return resp, err
}

type getenvFunc func(string) string

func handleControllerEvent(ctx context.Context, event events.APIGatewayV2HTTPRequest, getenv getenvFunc) (events.APIGatewayV2HTTPResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	event = normalizeControllerStagePath(event, getenv("STAGE"))
	// P52 H1.5: the runtime lab-gate is removed so deployed stages (lab AND
	// live) get the MicroVM controller. Fail-closed auth is preserved: the
	// AppTheoryMicrovmController CDK construct always sets
	// APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED=true and
	// APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT=deny; if either is missing or
	// loosened the controller refuses to serve (403). This keeps the
	// authorizer-required, deny-by-default posture intact across all stages.
	if strings.TrimSpace(getenv("APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED")) != "true" || strings.TrimSpace(getenv("APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT")) != runtimemicrovm.ControllerAuthDefaultDeny {
		return jsonResponse(http.StatusForbidden, safeFailure(runtimemicrovm.Command(""), requestIDFromEvent(event), "hosted_genesis_microvm_controller_disabled", "hosted genesis microvm controller is fail-closed (auth required, deny by default)"))
	}

	runtime, err := newRuntimeController(ctx, getenv)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, safeFailure(runtimemicrovm.Command(""), requestIDFromEvent(event), "microvm_controller_unavailable", "AppTheory MicroVM controller is unavailable"))
	}
	app, err := newControllerApp(runtime, getenv)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, safeFailure(runtimemicrovm.Command(""), requestIDFromEvent(event), "microvm_controller_unavailable", "AppTheory MicroVM controller routes are unavailable"))
	}
	return withControllerHeaders(app.ServeAPIGatewayV2(ctx, event)), nil
}

// normalizeControllerStagePath removes API Gateway's deployed-stage path prefix
// before handing the event to AppTheory's router. The AppTheoryMicrovmController
// endpoint includes the HTTP API stage (`/.../lab/microvms`), and API Gateway
// v2 passes RawPath / RequestContext.HTTP.Path through as `/lab/microvms` even
// though the route key is `POST /microvms`. Host registers the governed
// controller routes at `/microvms`; without this normalization deployed lab/live
// requests miss every route and fail as app.not_found. Only an exact stage path
// segment is stripped, so `/labyrinth/...` is not rewritten.
func normalizeControllerStagePath(event events.APIGatewayV2HTTPRequest, stage string) events.APIGatewayV2HTTPRequest {
	stage = strings.Trim(strings.TrimSpace(stage), "/")
	if stage == "" || stage == "$default" {
		return event
	}
	event.RawPath = stripControllerStagePrefix(event.RawPath, stage)
	event.RequestContext.HTTP.Path = stripControllerStagePrefix(event.RequestContext.HTTP.Path, stage)
	if method, routePath, ok := strings.Cut(event.RouteKey, " "); ok {
		event.RouteKey = strings.TrimSpace(method) + " " + stripControllerStagePrefix(routePath, stage)
	}
	return event
}

func stripControllerStagePrefix(path string, stage string) string {
	path = strings.TrimSpace(path)
	if path == "" || stage == "" {
		return path
	}
	prefix := "/" + stage
	if path == prefix {
		return "/"
	}
	if strings.HasPrefix(path, prefix+"/") {
		return strings.TrimPrefix(path, prefix)
	}
	return path
}

func newRuntimeController(ctx context.Context, getenv getenvFunc) (*hostedgenesis.MicroVMControllerRuntime, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	// Host's durable business truth for hosted genesis is
	// models.HostedGenesisSession in the Host state table. The AppTheory
	// controller registry is only operational cache for the MicroVM lifecycle
	// envelope. Persist that cache through Host's repo-owned, camelCase
	// HostedGenesisMicroVMExecution model and adapter; missing/stale cache is
	// reconstructed from HostedGenesisSession truth below.
	stateDB, err := store.LambdaInit()
	if err != nil {
		return nil, err
	}
	stateStore := store.New(stateDB)
	registry, err := store.NewHostedGenesisMicroVMRegistry(stateStore)
	if err != nil {
		return nil, err
	}
	provider, err := newMicroVMProvider(ctx)
	if err != nil {
		return nil, err
	}
	imageRef := strings.TrimSpace(getenv("APPTHEORY_MICROVM_IMAGE_REF"))
	egressRefs := csv(getenv("APPTHEORY_MICROVM_EGRESS_NETWORK_CONNECTOR_REFS"))
	if len(egressRefs) == 0 {
		egressRefs = csv(getenv("APPTHEORY_MICROVM_NETWORK_CONNECTOR_REFS"))
	}
	networkConnectorRef := firstString(egressRefs)
	cfg := hostedgenesis.MicroVMControllerRuntimeConfig{
		Provider:            provider,
		Registry:            registry,
		ImageRef:            imageRef,
		NetworkConnectorRef: networkConnectorRef,
		IngressNetworkConnectorRefs: csv(
			getenv("APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS"),
		),
		EgressNetworkConnectorRefs: egressRefs,
		ReconstructionHook: stateStore.HostedGenesisMicroVMReconstructionHook(store.HostedGenesisMicroVMReconstructionConfig{
			ImageRef:                    imageRef,
			NetworkConnectorRef:         networkConnectorRef,
			IngressNetworkConnectorRefs: csv(getenv("APPTHEORY_MICROVM_INGRESS_NETWORK_CONNECTOR_REFS")),
			EgressNetworkConnectorRefs:  egressRefs,
			ControllerID:                hostedgenesis.MicroVMControllerID,
			TTL:                         hostedgenesis.MicroVMRegistryReconstructionTTL,
		}),
		ControllerID:             hostedgenesis.MicroVMControllerID,
		SessionTTL:               hostedgenesis.MicroVMRegistryReconstructionTTL,
		ReconstructionStaleAfter: 5 * time.Minute,
	}
	return hostedgenesis.NewMicroVMControllerRuntime(cfg)
}

func newMicroVMProvider(ctx context.Context) (runtimemicrovm.Provider, error) {
	return runtimemicrovm.NewAWSLambdaMicroVMProvider(ctx)
}

func newControllerApp(runtime *hostedgenesis.MicroVMControllerRuntime, getenv getenvFunc) (*apptheory.App, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if runtime == nil || runtime.Controller() == nil {
		return nil, errors.New("hosted genesis microvm controller runtime is nil")
	}
	app := apptheory.New(
		apptheory.WithObservability(observability.New(serviceName)),
		apptheory.WithAuthHook(controllerAuthHook(getenv)),
	)
	return runtimemicrovm.RegisterControllerRoutes(app, runtime.Controller())
}

func controllerAuthHook(getenv getenvFunc) apptheory.AuthHook {
	return func(ctx *apptheory.Context) (string, error) {
		if ctx == nil {
			return "", nil
		}
		if !bearerMatchesHash(ctx.Header("authorization"), getenv("HOSTED_GENESIS_MICROVM_AUTH_TOKEN_SHA256")) {
			return "", nil
		}
		if strings.TrimSpace(ctx.TenantID) == "" || strings.TrimSpace(ctx.Header("x-namespace-id")) == "" {
			return "", nil
		}
		return hostedgenesis.MicroVMAuthSubject, nil
	}
}

func bearerMatchesHash(authorization string, expectedHash string) bool {
	expected := normalizeSHA256(expectedHash)
	if expected == "" {
		return false
	}
	authorization = strings.TrimSpace(authorization)
	if authorization == "" {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if token == "" || token == authorization {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	got := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func normalizeSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return ""
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return value
}

func requestIDFromEvent(event events.APIGatewayV2HTTPRequest) string {
	if id := strings.TrimSpace(event.RequestContext.RequestID); id != "" {
		return id
	}
	return strings.TrimSpace(event.Headers["x-request-id"])
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func csv(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func safeFailure(command runtimemicrovm.Command, requestID string, code string, message string) runtimemicrovm.ControllerResponse {
	return runtimemicrovm.ControllerResponse{
		Command:   command,
		RequestID: strings.TrimSpace(requestID),
		Error: &runtimemicrovm.SafeError{
			Code:      strings.TrimSpace(code),
			Message:   strings.TrimSpace(message),
			RequestID: strings.TrimSpace(requestID),
		},
	}
}

func jsonResponse(status int, payload any) (events.APIGatewayV2HTTPResponse, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{}, err
	}
	return withControllerHeaders(events.APIGatewayV2HTTPResponse{StatusCode: status, Body: string(b)}), nil
}

func withControllerHeaders(resp events.APIGatewayV2HTTPResponse) events.APIGatewayV2HTTPResponse {
	if resp.Headers == nil {
		resp.Headers = map[string]string{}
	}
	if _, ok := resp.Headers["content-type"]; !ok {
		resp.Headers["content-type"] = "application/json"
	}
	resp.Headers["cache-control"] = "no-store"
	resp.Headers["x-host-service"] = serviceName
	resp.Headers["x-content-type-options"] = "nosniff"
	return resp
}
