package main

import (
	"context"
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
	tabletheory "github.com/theory-cloud/tabletheory"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/observability"
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
	if strings.TrimSpace(getenv("STAGE")) != "lab" || strings.TrimSpace(getenv("APPTHEORY_MICROVM_CONTROLLER_AUTH_REQUIRED")) != "true" || strings.TrimSpace(getenv("APPTHEORY_MICROVM_CONTROLLER_AUTH_DEFAULT")) != runtimemicrovm.ControllerAuthDefaultDeny {
		return jsonResponse(http.StatusForbidden, safeFailure(runtimemicrovm.Command(""), requestIDFromEvent(event), "hosted_genesis_microvm_controller_disabled", "hosted genesis microvm controller is lab-only and fail-closed"))
	}

	req, err := controllerRequestFromEvent(event)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, safeFailure(runtimemicrovm.Command(""), requestIDFromEvent(event), "invalid_controller_request", "invalid AppTheory MicroVM controller request"))
	}
	if req.RequestID == "" {
		req.RequestID = requestIDFromEvent(event)
	}

	registryDB, err := tabletheory.LambdaInit(&runtimemicrovm.SessionRegistryRecord{})
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, safeFailure(req.Command, req.RequestID, "session_registry_unavailable", "AppTheory MicroVM session registry is unavailable"))
	}
	registry, err := runtimemicrovm.NewTableTheorySessionRegistry(registryDB)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, safeFailure(req.Command, req.RequestID, "session_registry_unavailable", "AppTheory MicroVM session registry is unavailable"))
	}
	client, err := hostedgenesis.NewProvisionalDogfoodMicroVMClient(registry, time.Hour)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, safeFailure(req.Command, req.RequestID, "microvm_client_unavailable", "AppTheory MicroVM client is unavailable"))
	}
	runtime, err := hostedgenesis.NewMicroVMControllerRuntime(hostedgenesis.MicroVMControllerRuntimeConfig{
		Client:              client,
		ImageRef:            getenv("APPTHEORY_MICROVM_IMAGE_REF"),
		NetworkConnectorRef: firstCSV(getenv("APPTHEORY_MICROVM_NETWORK_CONNECTOR_REFS")),
	})
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, safeFailure(req.Command, req.RequestID, "microvm_controller_unavailable", "AppTheory MicroVM controller is unavailable"))
	}

	controllerResp, handleErr := runtime.Handle(ctx, req)
	status := http.StatusOK
	if handleErr != nil {
		status = http.StatusBadRequest
	}
	return jsonResponse(status, controllerResp)
}

func controllerRequestFromEvent(event events.APIGatewayV2HTTPRequest) (runtimemicrovm.ControllerRequest, error) {
	var req runtimemicrovm.ControllerRequest
	if strings.TrimSpace(event.Body) != "" {
		body := event.Body
		if event.IsBase64Encoded {
			return req, errors.New("base64 controller bodies are not accepted")
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return req, err
		}
	}
	command, sessionID := commandAndSessionFromRoute(event)
	if req.Command == "" {
		req.Command = command
	}
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = sessionID
	}
	if strings.TrimSpace(req.RequestID) == "" {
		req.RequestID = requestIDFromEvent(event)
	}
	return req, nil
}

func commandAndSessionFromRoute(event events.APIGatewayV2HTTPRequest) (runtimemicrovm.Command, string) {
	routeKey := strings.TrimSpace(event.RouteKey)
	if routeKey == "" {
		routeKey = strings.TrimSpace(event.RequestContext.RouteKey)
	}
	path := strings.TrimSpace(event.RawPath)
	if path == "" {
		path = strings.TrimSpace(event.RequestContext.HTTP.Path)
	}
	sessionID := strings.TrimSpace(event.PathParameters["session_id"])
	if sessionID == "" {
		sessionID = strings.TrimSpace(event.PathParameters["sessionId"])
	}
	switch {
	case routeKey == "POST /microvms" || path == "/microvms":
		return runtimemicrovm.CommandCreate, sessionID
	case strings.HasSuffix(path, "/start") || strings.Contains(routeKey, "/start"):
		return runtimemicrovm.CommandStart, sessionID
	case strings.HasSuffix(path, "/stop") || strings.Contains(routeKey, "/stop"):
		return runtimemicrovm.CommandStop, sessionID
	case strings.HasSuffix(path, "/status") || strings.Contains(routeKey, "/status"):
		return runtimemicrovm.CommandStatus, sessionID
	default:
		return runtimemicrovm.CommandSession, sessionID
	}
}

func requestIDFromEvent(event events.APIGatewayV2HTTPRequest) string {
	if id := strings.TrimSpace(event.RequestContext.RequestID); id != "" {
		return id
	}
	return strings.TrimSpace(event.Headers["x-request-id"])
}

func firstCSV(value string) string {
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers: map[string]string{
			"content-type":           "application/json",
			"cache-control":          "no-store",
			"x-host-service":         serviceName,
			"x-content-type-options": "nosniff",
		},
		Body: string(b),
	}, nil
}
