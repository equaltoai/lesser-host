package hostedgenesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambdamicrovms"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambdamicrovms/types"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

// Endpoint-POST turn execution (P52 H1, endpoint-based architecture).
//
// The P52 H1 architecture runs a hosted-genesis turn by POSTing an M16
// LifecycleEvent to the MicroVM's runtime endpoint, which the workload's
// existing runHook handler (cmd/hosted-genesis-microvm-workload) serves at
// /aws/lambda-microvms/runtime/v1/run on :8080. The controller Lambda owns this
// flow: it starts the MicroVM through the framework's safe envelope, then makes
// two raw lambda-microvms API calls the framework deliberately does not surface,
// then POSTs the turn.
//
// Framework-gap exception (principal-approved): AppTheory v1.15.2's
// AWSLambdaMicroVMProvider does NOT surface the MicroVM Endpoint on
// ProviderSession (sessionFromRunOutput/sessionFromGetOutput in
// aws_provider.go read State/ImageArn/ImageVersion/StartedAt/TerminatedAt but
// drop out.Endpoint), and CreateAuthToken DELIBERATELY discards the token value
// (providerTokenMetadata returns metadata only; ProviderToken has no
// token-value field by design). So host must call the lambda-microvms API
// directly to obtain (a) the Endpoint via get-microvm and (b) the auth token
// value via create-microvm-auth-token. The framework's Run IS reused (it starts
// the MicroVM and records the session). The M16 LifecycleEvent + the workload's
// runHook are reused. The gap (surface Endpoint on ProviderSession + add a
// scoped way to receive the auth token value) is routed upstream to AppTheory
// with evidence; this file is the host-side framework-gap bridge, not a fork.

var (
	// ErrMicroVMEndpointTurnUnavailable is the fail-closed error for the
	// endpoint-POST turn path. It is never a license to fall back to a
	// synchronous control-plane LLM call; callers must surface it loudly.
	ErrMicroVMEndpointTurnUnavailable = errors.New("hosted genesis microvm endpoint turn is unavailable")
	// ErrMicroVMEndpointNotRunning is the loud failure returned when the
	// MicroVM does not reach the RUNNING state within the configured readiness
	// timeout (or reaches a terminal state instead).
	ErrMicroVMEndpointNotRunning = errors.New("hosted genesis microvm did not reach running state")
	// ErrMicroVMEndpointMissing is the loud failure returned when get-microvm
	// succeeds but the response carries no HTTPS endpoint URL.
	ErrMicroVMEndpointMissing = errors.New("hosted genesis microvm endpoint is missing")
	// ErrMicroVMAuthTokenMissing is the loud failure returned when
	// create-microvm-auth-token succeeds but the response carries no
	// X-aws-proxy-auth token value.
	ErrMicroVMAuthTokenMissing = errors.New("hosted genesis microvm auth token is missing")
)

const (
	// microvmRunHookPort is the workload port the run hook serves
	// (/aws/lambda-microvms/runtime/v1/run). The X-aws-proxy-port header routes
	// the controller's POST through the MicroVM's ingress connector to :8080.
	microvmRunHookPort = 8080

	// microvmRunHookPath is the runtime hook path the workload registers for
	// the run hook under the /aws/lambda-microvms/runtime/v1 prefix.
	microvmRunHookPath = "/aws/lambda-microvms/runtime/v1/run"

	// microvmAuthTokenHeader is the response map key (and request header name)
	// for the proxy auth token. create-microvm-auth-token returns it in a map
	// under this key; the controller presents it as a request header of the
	// same name when POSTing to the endpoint.
	microvmAuthTokenHeader = "X-aws-proxy-auth"

	// microvmProxyPortHeader is the request header that selects the workload
	// port the endpoint routes the POST to.
	microvmProxyPortHeader = "X-aws-proxy-port"

	// microvmEndpointTurnTimeout caps the full endpoint POST (the LLM turn +
	// in-VM declaration extraction + persistence). It is sized for the longest
	// LLM turn plus extraction (P52 H1.5 decision 7 sizing carried forward).
	microvmEndpointTurnTimeout = 120 * time.Second

	// microvmEndpointReadyTimeout bounds how long the controller waits for the
	// MicroVM to reach RUNNING before declaring a loud failure. The MicroVM
	// moves PENDING -> RUNNING quickly once the runtime environment attaches
	// ingress routing; 60s is a generous ceiling.
	microvmEndpointReadyTimeout = 60 * time.Second

	// microvmEndpointReadyPollInterval is the get-microvm poll cadence while
	// waiting for RUNNING.
	microvmEndpointReadyPollInterval = 2 * time.Second

	// microvmAuthTokenTTLMinutes is the auth-token lifetime requested (in
	// minutes; the AWS API accepts minutes, max 60). The token is scoped to the
	// MicroVM and used immediately for one turn, so a short lifetime is
	// sufficient.
	microvmAuthTokenTTLMinutes = int32(15)

	// microvmEndpointResponseLimit bounds the endpoint POST response body the
	// controller reads. The LifecycleResult envelope is small (kilobytes); 1
	// MiB is a generous ceiling that defends against a misbehaving endpoint.
	microvmEndpointResponseLimit = 1 << 20
)

// microvmEndpointAPI is the minimal lambda-microvms SDK surface
// RunTurnViaEndpoint needs. It is a strict subset of the framework's
// lambdaMicroVMAPI (aws_provider.go): only the two operations the framework
// does not safely surface (GetMicrovm for the Endpoint + state, and
// CreateMicrovmAuthToken for the token value). The full SDK client satisfies it;
// tests provide a fake.
type microvmEndpointAPI interface {
	GetMicrovm(ctx context.Context, in *lambdamicrovms.GetMicrovmInput, opts ...func(*lambdamicrovms.Options)) (*lambdamicrovms.GetMicrovmOutput, error)
	CreateMicrovmAuthToken(ctx context.Context, in *lambdamicrovms.CreateMicrovmAuthTokenInput, opts ...func(*lambdamicrovms.Options)) (*lambdamicrovms.CreateMicrovmAuthTokenOutput, error)
}

// EndpointTurnClient is the injectable dependency bundle RunTurnViaEndpoint uses
// to make the raw lambda-microvms calls and the endpoint HTTP POST. The SDK
// client bypasses the framework's safe envelope (which suppresses the Endpoint
// and discards the auth token value) — this is the principal-approved
// framework-gap bridge. All fields are required; a nil client or HTTP client is
// fail-closed.
type EndpointTurnClient struct {
	SDKClient    microvmEndpointAPI // raw lambda-microvms client (get-microvm + create-auth-token)
	HTTPClient   *http.Client       // posts the LifecycleEvent to the MicroVM endpoint
	ReadyTimeout time.Duration      // max wait for RUNNING; <=0 uses microvmEndpointReadyTimeout
	PollInterval time.Duration      // get-microvm poll cadence; <=0 uses microvmEndpointReadyPollInterval
	TurnTimeout  time.Duration      // POST timeout; <=0 uses microvmEndpointTurnTimeout
	now          func() time.Time   // injectable clock (tests); nil uses time.Now
}

// EndpointTurnResult is the outcome of RunTurnViaEndpoint: the run-command
// lifecycle ref Host records as non-authoritative execution/cache state, plus
// the M16 LifecycleResult the workload's runHook returned (the turn outcome).
// It deliberately excludes the endpoint URL, the auth token value, and the raw
// LifecycleEvent body — those are ephemeral and secret.
type EndpointTurnResult struct {
	LifecycleRef MicroVMLifecycleRef
	RunResponse  runtimemicrovm.ControllerResponse
	TurnResult   runtimemicrovm.LifecycleResult
}

// RunTurnViaEndpoint starts the MicroVM through the framework's safe run
// envelope, then executes the hosted-genesis turn by POSTing an M16
// LifecycleEvent to the MicroVM's runtime endpoint. It is the P52 H1
// endpoint-based turn execution path, owned by the controller Lambda.
//
// Flow:
//  1. Reuse r.Handle(CommandRun) to start the MicroVM + record the session
//     (framework's safe envelope; returns the microvm_id).
//  2. Raw get-microvm poll until state=RUNNING (or terminal), extracting the
//     HTTPS endpoint URL the framework does not surface.
//  3. Raw create-microvm-auth-token (allPorts) to obtain the X-aws-proxy-auth
//     token value the framework deliberately discards.
//  4. HTTP POST the M16 LifecycleEvent to <endpoint>/aws/lambda-microvms/
//     runtime/v1/run with the proxy-auth + proxy-port headers; the workload's
//     runHook executes the LLM turn + declaration extraction + persistence.
//  5. Return the run-command lifecycle ref + the LifecycleResult.
//
// Fail-closed: every failure (run failure, missing endpoint, never-RUNNING,
// token failure, HTTP failure, non-2xx response) is a loud typed error. There
// is no silent fallback to a synchronous control-plane LLM path. The auth token
// is scoped to the MicroVM and lives only in-memory for the duration of the
// POST; it is never logged, persisted, or returned. The POST body is an M16
// LifecycleEvent carrying only HostedGenesisSession ids (no raw credentials).
func (r *MicroVMControllerRuntime) RunTurnViaEndpoint(ctx context.Context, requestID string, binding MicroVMSessionBinding, client EndpointTurnClient) (EndpointTurnResult, error) {
	if r == nil || r.controller == nil {
		return EndpointTurnResult{}, ErrMicroVMControllerIncomplete
	}
	if r.endpointClient == nil && client.SDKClient == nil {
		return EndpointTurnResult{}, ErrMicroVMEndpointTurnUnavailable
	}
	if err := binding.Validate(); err != nil {
		return EndpointTurnResult{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return EndpointTurnResult{}, ErrMicroVMEndpointTurnUnavailable
	}
	deps, err := r.resolveEndpointTurnDeps(client)
	if err != nil {
		return EndpointTurnResult{}, err
	}

	// Step 1: start the MicroVM through the framework's safe run envelope.
	runResp, err := r.Run(ctx, requestID, binding)
	if err != nil {
		return EndpointTurnResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: start microvm: %w", err)
	}
	if runResp.Error != nil && runResp.Error.Code != "" {
		return EndpointTurnResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: start microvm: %w", runResp.Error)
	}
	microvmID := firstNonEmptyString(runResp.ProviderMicroVMID, runResp.MicroVMID)
	if microvmID == "" {
		return EndpointTurnResult{}, ErrMicroVMEndpointTurnUnavailable
	}

	// Steps 2-4: wait for RUNNING + get the endpoint, create the auth token,
	// POST the turn. Extracted to keep RunTurnViaEndpoint under the QUA-3
	// complexity budget.
	turnResult, err := executeMicrovmTurn(ctx, deps, microvmID, requestID, binding)
	if err != nil {
		return EndpointTurnResult{}, err
	}

	// Step 5: build the run-command lifecycle ref Host records as
	// non-authoritative execution/cache state. The run response already carries
	// the validated session binding; the turn result's state is the observed
	// post-turn lifecycle state.
	observedAt := deps.now().UTC()
	if !runResp.LastTransition.IsZero() {
		observedAt = runResp.LastTransition.UTC()
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, runResp, observedAt)
	if err != nil {
		return EndpointTurnResult{}, err
	}
	return EndpointTurnResult{LifecycleRef: ref, RunResponse: runResp, TurnResult: turnResult}, nil
}

// executeMicrovmTurn runs the framework-gap bridge steps after the MicroVM is
// started: poll get-microvm until RUNNING + extract the endpoint, create the
// scoped auth token, POST the M16 LifecycleEvent to the workload's run hook.
// It returns the LifecycleResult the workload's runHook produced. Every failure
// is a loud fail-closed error.
func executeMicrovmTurn(ctx context.Context, deps resolvedEndpointTurnDeps, microvmID, requestID string, binding MicroVMSessionBinding) (runtimemicrovm.LifecycleResult, error) {
	endpoint, err := waitForMicroVMRunning(ctx, deps.sdk, microvmID, requestID, deps.readyTimeout, deps.pollInterval)
	if err != nil {
		return runtimemicrovm.LifecycleResult{}, err
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return runtimemicrovm.LifecycleResult{}, ErrMicroVMEndpointMissing
	}
	token, err := createMicrovmAuthToken(ctx, deps.sdk, microvmID, requestID)
	if err != nil {
		return runtimemicrovm.LifecycleResult{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return runtimemicrovm.LifecycleResult{}, ErrMicroVMAuthTokenMissing
	}
	return postTurnToEndpoint(ctx, deps.httpClient, endpoint, token, requestID, binding, deps.turnTimeout)
}

// resolvedEndpointTurnDeps is the resolved dependency bundle
// resolveEndpointTurnDeps produces: the SDK + HTTP clients and the resolved
// timeouts/clock with all the <=0 fallbacks applied.
type resolvedEndpointTurnDeps struct {
	sdk          microvmEndpointAPI
	httpClient   *http.Client
	readyTimeout time.Duration
	pollInterval time.Duration
	turnTimeout  time.Duration
	now          func() time.Time
}

// resolveEndpointTurnDeps merges a caller-supplied EndpointTurnClient (tests)
// with the runtime-configured client (production), with the caller winning for
// any non-nil/non-positive field. It applies package defaults for any remaining
// <=0 timeout and fails closed when no HTTP client is resolvable. Extracting
// this keeps RunTurnViaEndpoint's cognitive complexity under the QUA-3 budget.
func (r *MicroVMControllerRuntime) resolveEndpointTurnDeps(client EndpointTurnClient) (resolvedEndpointTurnDeps, error) {
	sdk := client.SDKClient
	if sdk == nil {
		sdk = r.endpointClient
	}
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = r.endpointHTTPClient
	}
	if httpClient == nil {
		return resolvedEndpointTurnDeps{}, ErrMicroVMEndpointTurnUnavailable
	}
	readyTimeout := firstPositiveDuration(client.ReadyTimeout, r.endpointReadyTimeout, microvmEndpointReadyTimeout)
	pollInterval := firstPositiveDuration(client.PollInterval, r.endpointPollInterval, microvmEndpointReadyPollInterval)
	turnTimeout := firstPositiveDuration(client.TurnTimeout, r.endpointTurnTimeout, microvmEndpointTurnTimeout)
	now := client.now
	if now == nil {
		now = r.endpointNow
	}
	if now == nil {
		now = time.Now
	}
	return resolvedEndpointTurnDeps{
		sdk:          sdk,
		httpClient:   httpClient,
		readyTimeout: readyTimeout,
		pollInterval: pollInterval,
		turnTimeout:  turnTimeout,
		now:          now,
	}, nil
}

// firstPositiveDuration returns the first positive duration among values, or
// the last value if none are positive (so a zero default still resolves).
func firstPositiveDuration(values ...time.Duration) time.Duration {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	if len(values) > 0 {
		return values[len(values)-1]
	}
	return 0
}

// waitForMicroVMRunning polls get-microvm until the MicroVM reaches RUNNING or a
// terminal state, returning the HTTPS endpoint URL. A terminal state before
// RUNNING is a loud failure (ErrMicroVMEndpointNotRunning). The ready timeout
// bounds the wait; on expiry a loud failure is returned. The framework's
// sessionFromGetOutput drops out.Endpoint, so host reads it directly here.
func waitForMicroVMRunning(ctx context.Context, sdk microvmEndpointAPI, microvmID, requestID string, readyTimeout, pollInterval time.Duration) (string, error) {
	deadline := time.Now().Add(readyTimeout)
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("hosted genesis microvm endpoint turn: wait for running: %w", ctxErr)
		}
		out, err := sdk.GetMicrovm(ctx, &lambdamicrovms.GetMicrovmInput{
			MicrovmIdentifier: aws.String(microvmID),
		})
		if err != nil {
			return "", fmt.Errorf("hosted genesis microvm endpoint turn: get microvm: %w", err)
		}
		if out == nil {
			return "", ErrMicroVMEndpointNotRunning
		}
		state := out.State
		if state == lambdatypes.MicrovmStateRunning {
			return aws.ToString(out.Endpoint), nil
		}
		if isTerminalMicrovmState(state) {
			return "", fmt.Errorf("hosted genesis microvm endpoint turn: %w (state=%s)", ErrMicroVMEndpointNotRunning, state)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("hosted genesis microvm endpoint turn: %w (last state=%s)", ErrMicroVMEndpointNotRunning, state)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("hosted genesis microvm endpoint turn: wait for running: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// isTerminalMicrovmState reports whether a MicroVM state is terminal (no further
// progress to RUNNING is possible). TERMINATED is terminal; TERMINATING is
// treated as terminal for readiness purposes since the VM is being torn down.
// The AWS MicrovmState enum (lambdamicrovms/types) has no FAILED constant;
// failure is surfaced via StateReason on a terminal state.
func isTerminalMicrovmState(state lambdatypes.MicrovmState) bool {
	switch state {
	case lambdatypes.MicrovmStateTerminated, lambdatypes.MicrovmStateTerminating:
		return true
	default:
		return false
	}
}

// createMicrovmAuthToken calls create-microvm-auth-token (allPorts) and extracts
// the X-aws-proxy-auth token value the framework's CreateAuthToken deliberately
// discards. The token is scoped to all ports on this MicroVM; the POST targets
// this MicroVM's endpoint only. The token value lives in-memory for the POST
// and is never logged or persisted.
func createMicrovmAuthToken(ctx context.Context, sdk microvmEndpointAPI, microvmID, requestID string) (string, error) {
	out, err := sdk.CreateMicrovmAuthToken(ctx, &lambdamicrovms.CreateMicrovmAuthTokenInput{
		MicrovmIdentifier:   aws.String(microvmID),
		ExpirationInMinutes: aws.Int32(microvmAuthTokenTTLMinutes),
		AllowedPorts: []lambdatypes.PortSpecification{
			&lambdatypes.PortSpecificationMemberAllPorts{Value: lambdatypes.Unit{}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("hosted genesis microvm endpoint turn: create auth token: %w", err)
	}
	if out == nil || len(out.AuthToken) == 0 {
		return "", ErrMicroVMAuthTokenMissing
	}
	return out.AuthToken[microvmAuthTokenHeader], nil
}

// postTurnToEndpoint POSTs the M16 LifecycleEvent to the workload's run hook at
// <endpoint>/aws/lambda-microvms/runtime/v1/run. The X-aws-proxy-auth header
// presents the scoped token; X-aws-proxy-port routes the POST to the workload's
// :8080 listener. The body carries only HostedGenesisSession ids (slug boundary
// tenant_id + conversation_id/turn_id/agent_id metadata) — no raw credentials.
// A non-2xx response or decode failure is a loud fail-closed error.
func postTurnToEndpoint(ctx context.Context, httpClient *http.Client, endpoint, token, requestID string, binding MicroVMSessionBinding, turnTimeout time.Duration) (runtimemicrovm.LifecycleResult, error) {
	event := runtimemicrovm.LifecycleEvent{
		RequestID: requestID,
		TenantID:  binding.TenantID(),
		Namespace: MicroVMNamespace,
		SessionID: strings.TrimSpace(binding.ConversationID),
		Hook:      runtimemicrovm.HookRun,
		State:     runtimemicrovm.StateRunning,
		Metadata:  binding.Metadata(),
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return runtimemicrovm.LifecycleResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: encode turn event: %w", err)
	}
	postCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	url := strings.TrimRight(endpoint, "/") + microvmRunHookPath
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return runtimemicrovm.LifecycleResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: build turn request: %w", err)
	}
	req.Header.Set(microvmAuthTokenHeader, token)
	req.Header.Set(microvmProxyPortHeader, fmt.Sprintf("%d", microvmRunHookPort))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	httpResp, err := httpClient.Do(req) //nolint:gosec // G704: the endpoint is the AWS-returned MicroVM URL from get-microvm (not user input); the controller never POSTs to a caller-supplied URL.
	if err != nil {
		return runtimemicrovm.LifecycleResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: post turn: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, microvmEndpointResponseLimit))
	if err != nil {
		return runtimemicrovm.LifecycleResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: read turn response: %w", err)
	}
	var result runtimemicrovm.LifecycleResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return runtimemicrovm.LifecycleResult{}, fmt.Errorf("hosted genesis microvm endpoint turn: decode turn response (status %d): %w", httpResp.StatusCode, err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		// A LifecycleResult carrying a SafeError is the preferred error surface;
		// otherwise synthesize a loud fail-closed error from the HTTP status.
		if result.Error != nil && result.Error.Code != "" {
			return result, fmt.Errorf("hosted genesis microvm endpoint turn: workload returned status %d: %w", httpResp.StatusCode, result.Error)
		}
		return result, fmt.Errorf("hosted genesis microvm endpoint turn: workload returned status %d", httpResp.StatusCode)
	}
	return result, nil
}
