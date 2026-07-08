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
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
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
		EndpointTurnClient:       newEndpointTurnClient(ctx),
	}
	return hostedgenesis.NewMicroVMControllerRuntime(cfg)
}

func newMicroVMProvider(ctx context.Context) (runtimemicrovm.Provider, error) {
	delegate, err := runtimemicrovm.NewAWSLambdaMicroVMProvider(ctx)
	if err != nil {
		return nil, err
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return hostedgenesis.NewNoRunHookAWSLambdaMicroVMProvider(lambdamicrovms.NewFromConfig(awsCfg), delegate)
}

// newEndpointTurnClient builds the raw lambda-microvms SDK client + HTTP client
// RunTurnViaEndpoint uses to bridge the framework gap (the framework does not
// surface the MicroVM Endpoint and discards the auth token value). The SDK
// client loads the controller Lambda's execution-role AWS config (the role has
// the lambda-microvms permissions). The HTTP client POSTs the LifecycleEvent to
// the AWS-returned MicroVM endpoint. Construction failure is fail-closed: a nil
// SDK client disables the endpoint-POST path (the controller refuses
// run-via-endpoint instead of falling back to a synchronous LLM path).
func newEndpointTurnClient(ctx context.Context) hostedgenesis.EndpointTurnClient {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return hostedgenesis.EndpointTurnClient{}
	}
	return hostedgenesis.EndpointTurnClient{
		SDKClient:  lambdamicrovms.NewFromConfig(awsCfg),
		HTTPClient: &http.Client{Timeout: 0}, // per-request timeout is set in RunTurnViaEndpoint via context
	}
}

func newControllerApp(runtime *hostedgenesis.MicroVMControllerRuntime, getenv getenvFunc) (*apptheory.App, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if runtime == nil {
		return nil, errors.New("hosted genesis microvm controller runtime is nil")
	}
	app := apptheory.New(
		apptheory.WithObservability(observability.New(serviceName)),
		apptheory.WithAuthHook(controllerAuthHook(getenv)),
	)
	// P52 H1: register the endpoint-POST run route BEFORE the framework's
	// RegisterControllerRoutes so it wins for POST /microvms. AppTheory's router
	// prefers earlier registration order for equally-specific routes
	// (router.go routeMoreSpecific), and RegisterControllerRoutes registers
	// POST /microvms -> controller.Handle(CommandRun) which only STARTS the
	// MicroVM. The endpoint-based architecture additionally executes the turn
	// by POSTing an M16 LifecycleEvent to the MicroVM's runtime endpoint; the
	// custom run route does both (RunTurnViaEndpoint) and returns the same
	// ControllerResponse envelope so the control plane's HTTPControllerDispatcher
	// decodes it unchanged. Registering first is the framework-gap-safe way to
	// override the run route without patching the framework.
	app.Handle("POST", "/microvms", runTurnViaEndpointHandler(runtime), apptheory.RequireAuth())
	if err := registerControllerRoutesExceptRun(app, runtime); err != nil {
		return nil, err
	}
	return app, nil
}

func registerControllerRoutesExceptRun(app *apptheory.App, runtime *hostedgenesis.MicroVMControllerRuntime) error {
	if app == nil || runtime == nil {
		return errors.New("hosted genesis microvm controller route registration is incomplete")
	}
	routes := []struct {
		method  string
		path    string
		command runtimemicrovm.Command
	}{
		{"GET", "/microvms", runtimemicrovm.CommandList},
		{"GET", "/microvms/{session_id}", runtimemicrovm.CommandGet},
		{"POST", "/microvms/{session_id}/suspend", runtimemicrovm.CommandSuspend},
		{"POST", "/microvms/{session_id}/resume", runtimemicrovm.CommandResume},
		{"DELETE", "/microvms/{session_id}", runtimemicrovm.CommandTerminate},
		{"POST", "/microvms/{session_id}/auth-token", runtimemicrovm.CommandAuthToken},
		{"POST", "/microvms/{session_id}/shell-auth-token", runtimemicrovm.CommandShellAuthToken},
		// Compatibility route retained by AppTheory v1.16.1 for callers created
		// during the M16 correction window.
		{"POST", "/microvms/{session_id}/shell-token", runtimemicrovm.CommandShellAuthToken},
	}
	for _, route := range routes {
		app.Handle(route.method, route.path, controllerRouteHandler(runtime, route.command), apptheory.RequireAuth())
	}
	return nil
}

type controllerRoutePayload struct {
	TenantID                    string                             `json:"tenant_id"`
	Namespace                   string                             `json:"namespace"`
	SessionID                   string                             `json:"session_id"`
	ImageRef                    string                             `json:"image_ref"`
	ImageVersion                string                             `json:"image_version"`
	NetworkConnectorRef         string                             `json:"network_connector_ref"`
	IngressNetworkConnectorRefs []string                           `json:"ingress_network_connector_refs"`
	EgressNetworkConnectorRefs  []string                           `json:"egress_network_connector_refs"`
	SessionSpec                 runtimemicrovm.SessionSpec         `json:"session_spec"`
	IdlePolicy                  *runtimemicrovm.ProviderIdlePolicy `json:"idle_policy"`
	MaximumDurationSeconds      int32                              `json:"maximum_duration_seconds"`
	TTLSeconds                  int32                              `json:"ttl_seconds"`
	AllowedPortScope            []runtimemicrovm.ProviderPortScope `json:"allowed_port_scope"`
	MaxResults                  int32                              `json:"max_results"`
}

func controllerRouteHandler(runtime *hostedgenesis.MicroVMControllerRuntime, command runtimemicrovm.Command) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		req, safe := controllerRequestFromContext(ctx, command)
		if safe.Code != "" {
			return controllerJSON(controllerHTTPStatus(&safe), controllerErrorResponse(req, &safe))
		}
		resp, err := runtime.Handle(ctx.Context(), req)
		if err != nil && resp.Error == nil {
			safeErr := runTurnSafeError(err, req.RequestID)
			resp = controllerErrorResponse(req, safeErr)
		}
		return controllerJSON(controllerHTTPStatus(resp.Error), resp)
	}
}

func controllerRequestFromContext(ctx *apptheory.Context, command runtimemicrovm.Command) (runtimemicrovm.ControllerRequest, runtimemicrovm.SafeError) {
	request := runtimemicrovm.ControllerRequest{Command: command}
	if ctx == nil {
		return request, safeErrorMicrovm(runtimemicrovm.ErrorCodeInvalidControllerRequest, "apptheory: microvm controller route context is missing")
	}
	payload := controllerRoutePayload{}
	if len(ctx.Request.Body) > 0 {
		if err := json.Unmarshal(ctx.Request.Body, &payload); err != nil {
			request.RequestID = controllerRequestID(ctx)
			return request, safeErrorMicrovm(runtimemicrovm.ErrorCodeInvalidControllerRequest, "apptheory: microvm controller route request is malformed")
		}
	}
	pathSessionID := strings.TrimSpace(ctx.Param("session_id"))
	bodySessionID := strings.TrimSpace(payload.SessionID)
	if pathSessionID != "" && bodySessionID != "" && pathSessionID != bodySessionID {
		request.RequestID = controllerRequestID(ctx)
		return request, safeErrorMicrovm(runtimemicrovm.ErrorCodeTenantBindingViolation, "apptheory: microvm controller route session binding mismatch")
	}
	if ctx.TenantID != "" && strings.TrimSpace(payload.TenantID) != "" && strings.TrimSpace(payload.TenantID) != strings.TrimSpace(ctx.TenantID) {
		request.RequestID = controllerRequestID(ctx)
		return request, safeErrorMicrovm(runtimemicrovm.ErrorCodeTenantBindingViolation, "apptheory: microvm controller route tenant binding mismatch")
	}
	if ctx.TenantID != "" && strings.TrimSpace(ctx.Query("tenant_id")) != "" && strings.TrimSpace(ctx.Query("tenant_id")) != strings.TrimSpace(ctx.TenantID) {
		request.RequestID = controllerRequestID(ctx)
		return request, safeErrorMicrovm(runtimemicrovm.ErrorCodeTenantBindingViolation, "apptheory: microvm controller route tenant binding mismatch")
	}
	namespace := firstNonEmptyString(payload.Namespace, ctx.Header("x-namespace-id"), ctx.Query("namespace"))
	request = runtimemicrovm.ControllerRequest{
		Command:                     command,
		RequestID:                   controllerRequestID(ctx),
		TenantID:                    firstNonEmptyString(ctx.TenantID, payload.TenantID, ctx.Query("tenant_id")),
		Namespace:                   namespace,
		AuthContext:                 runtimemicrovm.AuthContext{Subject: strings.TrimSpace(ctx.AuthIdentity), TenantID: strings.TrimSpace(ctx.TenantID), Namespace: namespace},
		SessionID:                   firstNonEmptyString(pathSessionID, bodySessionID),
		ImageRef:                    payload.ImageRef,
		ImageVersion:                payload.ImageVersion,
		NetworkConnectorRef:         payload.NetworkConnectorRef,
		IngressNetworkConnectorRefs: append([]string(nil), payload.IngressNetworkConnectorRefs...),
		EgressNetworkConnectorRefs:  append([]string(nil), payload.EgressNetworkConnectorRefs...),
		SessionSpec:                 payload.SessionSpec,
		IdlePolicy:                  payload.IdlePolicy,
		MaximumDurationSeconds:      payload.MaximumDurationSeconds,
		TTLSeconds:                  payload.TTLSeconds,
		AllowedPortScope:            append([]runtimemicrovm.ProviderPortScope(nil), payload.AllowedPortScope...),
		MaxResults:                  firstPositiveInt32(payload.MaxResults, parseInt32(ctx.Query("max_results"))),
	}
	return request, runtimemicrovm.SafeError{}
}

func controllerErrorResponse(req runtimemicrovm.ControllerRequest, safe *runtimemicrovm.SafeError) runtimemicrovm.ControllerResponse {
	return runtimemicrovm.ControllerResponse{
		Command:   req.Command,
		RequestID: strings.TrimSpace(req.RequestID),
		TenantID:  strings.TrimSpace(req.TenantID),
		Namespace: strings.TrimSpace(req.Namespace),
		SessionID: strings.TrimSpace(req.SessionID),
		Error:     safe,
	}
}

// runTurnViaEndpointHandler is the POST /microvms handler that executes a
// hosted-genesis turn via the MicroVM's runtime endpoint (P52 H1
// endpoint-based architecture). It decodes the same controllerRoutePayload the
// framework's run route accepts, resolves the HostedGenesisSession binding from
// the session-spec metadata, calls RunTurnViaEndpoint (start MicroVM -> get
// endpoint -> auth token -> POST turn -> LifecycleResult), and returns the run
// ControllerResponse envelope. A turn-execution failure is surfaced as a
// ControllerResponse carrying a SafeError so the control plane's
// HTTPControllerDispatcher observes resp.Error and fails closed (no silent
// fallback to a synchronous LLM path).
func runTurnViaEndpointHandler(runtime *hostedgenesis.MicroVMControllerRuntime) apptheory.Handler {
	return func(ctx *apptheory.Context) (*apptheory.Response, error) {
		requestID := controllerRequestID(ctx)
		binding, safeErr := controllerRunBindingFromContext(ctx)
		if safeErr.Code != "" {
			return controllerJSON(http.StatusBadRequest, runTurnErrorResponse(requestID, &safeErr))
		}
		result, err := runtime.RunTurnViaEndpoint(ctx.Context(), requestID, binding, hostedgenesis.EndpointTurnClient{})
		if err != nil {
			safe := runTurnSafeError(err, requestID)
			return controllerJSON(controllerHTTPStatus(safe), runTurnErrorResponse(requestID, safe))
		}
		resp := result.RunResponse
		if resp.Error != nil && resp.Error.Code != "" {
			return controllerJSON(controllerHTTPStatus(resp.Error), resp)
		}
		return controllerJSON(http.StatusOK, resp)
	}
}

// controllerRunBindingFromContext decodes the POST /microvms body the control
// plane's HTTPControllerDispatcher sends (microvmRunRequestPayload shape) and
// resolves the HostedGenesisSession binding from the session-spec metadata +
// tenant header. It mirrors the framework's controllerRequestFromHTTP tenant +
// namespace resolution so the run envelope the control plane sends is honored
// unchanged. A binding that cannot be tied back to a hosted-genesis
// conversation (missing slug/registration/agent/conversation/turn) is a loud
// fail-closed SafeError.
func controllerRunBindingFromContext(ctx *apptheory.Context) (hostedgenesis.MicroVMSessionBinding, runtimemicrovm.SafeError) {
	var payload controllerRunPayload
	if ctx == nil {
		return hostedgenesis.MicroVMSessionBinding{}, safeErrorMicrovm(runtimemicrovm.ErrorCodeInvalidControllerRequest, "apptheory: microvm run-turn route context is missing")
	}
	if len(ctx.Request.Body) > 0 {
		if err := json.Unmarshal(ctx.Request.Body, &payload); err != nil {
			return hostedgenesis.MicroVMSessionBinding{}, safeErrorMicrovm(runtimemicrovm.ErrorCodeInvalidControllerRequest, "apptheory: microvm run-turn route request is malformed")
		}
	}
	tenantID := strings.TrimSpace(ctx.TenantID)
	if tenantID == "" {
		tenantID = strings.TrimSpace(payload.TenantID)
	}
	if tenantID == "" {
		tenantID = strings.TrimSpace(ctx.Query("tenant_id"))
	}
	instanceSlug := strings.TrimSpace(strings.TrimPrefix(tenantID, "slug:"))
	metadata := payload.SessionSpec.Metadata
	binding := hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   instanceSlug,
		RegistrationID: strings.TrimSpace(metadata["registration_id"]),
		AgentID:        strings.TrimSpace(metadata["agent_id"]),
		ConversationID: strings.TrimSpace(payload.SessionID),
		TurnID:         strings.TrimSpace(metadata["turn_id"]),
	}
	if binding.ConversationID == "" {
		binding.ConversationID = strings.TrimSpace(metadata["conversation_id"])
	}
	if err := binding.Validate(); err != nil {
		return binding, safeErrorMicrovm(runtimemicrovm.ErrorCodeInvalidControllerRequest, "apptheory: microvm run-turn route binding is incomplete")
	}
	return binding, runtimemicrovm.SafeError{}
}

// controllerRunPayload is the POST /microvms body shape (the same
// microvmRunRequestPayload the control plane's HTTPControllerDispatcher sends
// and the framework's controllerRoutePayload decodes). Only the fields the
// run-turn route needs are modeled: SessionID + TenantID + SessionSpec.Metadata
// (the HostedGenesisSession ids). Image/network refs come from the runtime's
// CDK-provided config, not the body.
type controllerRunPayload struct {
	SessionID   string                     `json:"session_id,omitempty"`
	TenantID    string                     `json:"tenant_id,omitempty"`
	SessionSpec runtimemicrovm.SessionSpec `json:"session_spec,omitempty"`
}

func controllerRequestID(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	if id := strings.TrimSpace(ctx.RequestID); id != "" {
		return id
	}
	return strings.TrimSpace(ctx.Header("x-request-id"))
}

func runTurnSafeError(err error, requestID string) *runtimemicrovm.SafeError {
	return &runtimemicrovm.SafeError{
		Code:      runtimemicrovm.ErrorCodeControllerCommandFailed,
		Message:   strings.TrimSpace(err.Error()),
		RequestID: strings.TrimSpace(requestID),
	}
}

func runTurnErrorResponse(requestID string, safe *runtimemicrovm.SafeError) runtimemicrovm.ControllerResponse {
	return runtimemicrovm.ControllerResponse{
		Command:   runtimemicrovm.CommandRun,
		RequestID: strings.TrimSpace(requestID),
		Error:     safe,
	}
}

func safeErrorMicrovm(code, message string) runtimemicrovm.SafeError {
	return runtimemicrovm.SafeError{Code: code, Message: message}
}

// controllerHTTPStatus mirrors the framework's controllerHTTPStatus
// (controller_routes.go) for the SafeError codes the run-turn route can
// return, so the HTTP status semantics stay consistent across the custom run
// route and the framework's other M16 routes.
func controllerHTTPStatus(err *runtimemicrovm.SafeError) int {
	if err == nil || err.Code == "" {
		return http.StatusOK
	}
	switch err.Code {
	case runtimemicrovm.ErrorCodeUnauthenticatedController:
		return http.StatusUnauthorized
	case runtimemicrovm.ErrorCodeTenantBindingViolation:
		return http.StatusForbidden
	case runtimemicrovm.ErrorCodeSessionRegistryIncomplete:
		return http.StatusNotFound
	case runtimemicrovm.ErrorCodeControllerIncomplete:
		return http.StatusInternalServerError
	case runtimemicrovm.ErrorCodeControllerCommandFailed, runtimemicrovm.ErrorCodeProviderOperationFailed:
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func controllerJSON(status int, value any) (*apptheory.Response, error) {
	return apptheory.JSON(status, value)
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseInt32(value string) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
	if err != nil || n <= 0 {
		return 0
	}
	return int32(n)
}

func firstPositiveInt32(value int32, fallback int32) int32 {
	if value > 0 {
		return value
	}
	return fallback
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
