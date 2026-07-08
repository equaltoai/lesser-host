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

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

// ErrMicroVMDispatchUnavailable is the fail-closed error returned when the
// hosted-genesis accept path cannot dispatch a MicroVM controller run command.
// It is never a license to fall back to a synchronous control-plane LLM call;
// callers must surface it loudly (typed AppTheoryError) and persist a failed turn.
var ErrMicroVMDispatchUnavailable = errors.New("hosted genesis microvm dispatch is unavailable")

// MicroVMDispatchResult is the safe, non-secret outcome of a controller run
// dispatch: the validated execution/cache lifecycle ref Host records on the
// HostedGenesisSession, plus the AppTheory session id the controller allocated.
// It deliberately excludes endpoint auth tokens, bearer tokens, raw lifecycle
// payloads, transcripts, and provider credentials.
type MicroVMDispatchResult struct {
	LifecycleRef MicroVMLifecycleRef
	SessionID    string
}

// MicroVMReconcileResult is the safe, non-secret outcome of a controller get
// dispatch: the AppTheory session id queried and the reconciled execution/cache
// lifecycle ref reflecting real VM state observed by the controller. Like
// MicroVMDispatchResult it excludes endpoint auth tokens, bearer tokens, raw
// lifecycle payloads, transcripts, and provider credentials.
//
// Terminal reports whether the observed lifecycle state is a terminal MicroVM
// state (terminated/failed) so the control plane can map a dead/expired VM to a
// loud failure instead of silently no-oping reconstruction.
type MicroVMReconcileResult struct {
	LifecycleRef MicroVMLifecycleRef
	SessionID    string
	Terminal     bool
}

// MicroVMDispatcher is the control-plane dispatch boundary for the hosted
// genesis MicroVM execution path. The accept path calls DispatchMicroVMRun
// after the HostedGenesisSession accepted turn is durably committed; the
// dispatcher invokes the AppTheory M16 controller run command through the
// governed AppTheoryMicrovmController HTTP API (POST /microvms) and returns the
// validated lifecycle ref Host records as non-authoritative execution/cache
// state.
//
// ReconcileMicroVM issues the M16 controller get command (GET
// /microvms/{session_id}) for an existing execution/cache ref so the control
// plane can observe real VM state and reconcile the HostedGenesisSession. It is
// the production reconstruction reachability site: the controller get drives the
// AppTheory reconstructing session registry (Host's reconstruction hook fires on
// cache miss) and the provider Get (real VM state). A dead/expired VM is
// reported as Terminal with the reconciled ref; callers must map terminal state
// to a loud failure, never a silent no-op.
//
// A nil/unwired dispatcher is fail-closed: the accept path must not fall back
// to a synchronous in-request LLM call, and the reconcile path must not treat
// an unwired dispatcher as a successful no-op reconstruction. Transport is
// HTTP through the governed controller API (the single AppTheory golden path);
// the control plane never handles raw provider SDK clients, bearer-token
// material beyond presenting the authorizer header, or lifecycle hook payloads.
type MicroVMDispatcher interface {
	DispatchMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error)
	ReconcileMicroVM(ctx context.Context, requestID string, binding MicroVMSessionBinding, ref MicroVMLifecycleRef) (MicroVMReconcileResult, error)
}

// microvmRunRequestPayload is the POST /microvms request body shape the
// AppTheoryMicrovmController HTTP route handler decodes
// (runtime/microvm/controller_routes.go controllerRoutePayload). The route
// handler fixes the command to "run", derives tenant_id from x-tenant-id /
// query, and namespace from x-namespace-id; only the session + image/network
// fields + the duration cap are caller-supplied in the body. AuthContext is
// populated by the controller authorizer, not the body.
type microvmRunRequestPayload struct {
	SessionID                   string                     `json:"session_id,omitempty"`
	ImageRef                    string                     `json:"image_ref"`
	ImageVersion                string                     `json:"image_version,omitempty"`
	NetworkConnectorRef         string                     `json:"network_connector_ref"`
	IngressNetworkConnectorRefs []string                   `json:"ingress_network_connector_refs,omitempty"`
	EgressNetworkConnectorRefs  []string                   `json:"egress_network_connector_refs,omitempty"`
	SessionSpec                 runtimemicrovm.SessionSpec `json:"session_spec,omitempty"`
	MaximumDurationSeconds      int32                      `json:"maximum_duration_seconds,omitempty"`
}

// HTTPControllerDispatcher adapts the governed AppTheoryMicrovmController HTTP
// API into the MicroVMDispatcher contract. It POSTs /microvms to run a session
// and GETs /microvms/{session_id} to reconcile one, presenting the authorizer
// bearer token in the Authorization header (the construct's
// authorizerHeaderName identity source). Responses are decoded into the same
// runtimemicrovm.ControllerResponse shape the in-process path produced — the
// HTTP route handler serializes that exact struct — so
// MicroVMLifecycleRefFromResponse / ReconcileMicroVMRegistryStatus / the H1.4
// expired-VM terminal classification all apply unchanged.
//
// The control plane never makes raw AWS RunMicrovm/GetMicrovm SDK calls or
// touches the session registry through this dispatcher: the controller Lambda
// is the single governed surface (auth, lifecycle validation, registry shape,
// fail-closed env). There is no dual path.
type HTTPControllerDispatcher struct {
	endpoint        string // /microvms base URL (the construct's controller.endpoint)
	authToken       string // authorizer bearer token (the identity source)
	imageRef        string
	networkConnRef  string
	ingressConnRefs []string
	egressConnRefs  []string
	maxDuration     int32
	httpClient      *http.Client
}

// HTTPControllerDispatcherConfig configures the HTTP MicroVM dispatcher. The
// endpoint is the AppTheoryMicrovmController /microvms base URL; the authToken
// is the authorizer bearer token presented in the Authorization header (a
// credential — never committed or logged); the image/network refs are the
// non-secret refs sent in the POST /microvms run body. MaxDurationSeconds caps
// each run session; zero lets the controller/provider default apply.
type HTTPControllerDispatcherConfig struct {
	Endpoint             string
	AuthToken            string //nolint:gosec // G117: name describes the authorizer bearer token; in-process config struct, never JSON-serialized over an untrusted wire.
	ImageRef             string
	NetworkConnectorRef  string
	IngressConnectorRefs []string
	EgressConnectorRefs  []string
	MaxDurationSeconds   int32
	HTTPClient           *http.Client
}

// NewHTTPControllerDispatcher constructs the production HTTP MicroVM
// dispatcher. A nil http.Client yields a fail-closed dispatcher (the caller
// must supply a client; construction failures are loud, never a sync fallback).
// The endpoint + auth token + image/network refs must be non-empty; the
// constructor is fail-closed when any are missing.
func NewHTTPControllerDispatcher(cfg HTTPControllerDispatcherConfig) (*HTTPControllerDispatcher, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	authToken := strings.TrimSpace(cfg.AuthToken)
	imageRef := strings.TrimSpace(cfg.ImageRef)
	networkConnRef := strings.TrimSpace(cfg.NetworkConnectorRef)
	if endpoint == "" || authToken == "" || imageRef == "" || networkConnRef == "" {
		return nil, ErrMicroVMDispatchUnavailable
	}
	if cfg.HTTPClient == nil {
		return nil, ErrMicroVMDispatchUnavailable
	}
	return &HTTPControllerDispatcher{
		endpoint:        strings.TrimRight(endpoint, "/"),
		authToken:       authToken,
		imageRef:        imageRef,
		networkConnRef:  networkConnRef,
		ingressConnRefs: normalizeStringSlice(cfg.IngressConnectorRefs),
		egressConnRefs:  normalizeStringSlice(cfg.EgressConnectorRefs),
		maxDuration:     cfg.MaxDurationSeconds,
		httpClient:      cfg.HTTPClient,
	}, nil
}

// DispatchMicroVMRun POSTs /microvms to the governed controller API for the
// binding and records the validated lifecycle ref. It fails closed when the
// dispatcher is misconfigured, the HTTP call fails, the controller returns a
// non-2xx status, or the response envelope carries a controller error; it
// never falls back to a local execution path.
func (d *HTTPControllerDispatcher) DispatchMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error) {
	if d == nil {
		return MicroVMDispatchResult{}, ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return MicroVMDispatchResult{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return MicroVMDispatchResult{}, ErrMicroVMDispatchUnavailable
	}
	payload := microvmRunRequestPayload{
		SessionID:                   strings.TrimSpace(binding.ConversationID),
		ImageRef:                    d.imageRef,
		NetworkConnectorRef:         d.networkConnRef,
		IngressNetworkConnectorRefs: append([]string(nil), d.ingressConnRefs...),
		EgressNetworkConnectorRefs:  append([]string(nil), d.egressConnRefs...),
		SessionSpec: runtimemicrovm.SessionSpec{
			Metadata: binding.Metadata(),
		},
		MaximumDurationSeconds: d.maxDuration,
	}
	resp, err := d.doControllerRequest(ctx, http.MethodPost, d.endpoint, requestID, binding, payload)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMDispatchResult{}, resp.Error
	}
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, resp, observedAt)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	return MicroVMDispatchResult{LifecycleRef: ref, SessionID: strings.TrimSpace(resp.SessionID)}, nil
}

// ReconcileMicroVM GETs /microvms/{session_id} from the governed controller API
// for the binding's existing execution/cache session and returns the reconciled
// lifecycle ref reflecting real VM state. It fails closed when the dispatcher is
// misconfigured, the HTTP call fails, the controller returns a non-2xx status,
// or the observed state no longer maps to the Host session binding
// (stale/tenant mismatch). It never falls back to a local execution path and
// never swallows a dead/expired VM as a silent no-op: terminal observed state is
// reported via Terminal=true so the caller maps it to a loud failure.
//
// H1.4: a session is Terminal when EITHER the observed lifecycle state is a
// terminal MicroVM state (terminated/failed) OR the controller-reported
// session expiry has passed (ExpiresAt is set and in the past). An expired
// session is dead even when its lifecycle state is non-terminal (e.g. stopped):
// the VM can no longer service the pending turn, so the recover path must map
// it to a loud retryable failure rather than preserve a pending status that can
// never advance. The reconciled lifecycle ref still reflects the observed
// non-terminal state; only the Terminal flag forces the loud-failed mapping.
func (d *HTTPControllerDispatcher) ReconcileMicroVM(ctx context.Context, requestID string, binding MicroVMSessionBinding, ref MicroVMLifecycleRef) (MicroVMReconcileResult, error) {
	if d == nil {
		return MicroVMReconcileResult{}, ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return MicroVMReconcileResult{}, err
	}
	if err := ref.Validate(binding); err != nil {
		return MicroVMReconcileResult{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return MicroVMReconcileResult{}, ErrMicroVMDispatchUnavailable
	}
	sessionID := strings.TrimSpace(binding.ConversationID)
	getURL := d.endpoint + "/" + sessionID
	resp, err := d.doControllerRequest(ctx, http.MethodGet, getURL, requestID, binding, nil)
	if err != nil {
		return MicroVMReconcileResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMReconcileResult{}, resp.Error
	}
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	observedState := firstNonEmptyLifecycleState(resp.LifecycleState, resp.State)
	status := runtimemicrovm.SessionStatus{
		TenantID:        strings.TrimSpace(resp.TenantID),
		Namespace:       strings.TrimSpace(resp.Namespace),
		SessionID:       strings.TrimSpace(resp.SessionID),
		State:           observedState,
		DesiredState:    firstNonEmptyLifecycleState(resp.DesiredState, observedState),
		LifecycleState:  observedState,
		MicroVMID:       firstNonEmptyString(resp.ProviderMicroVMID, resp.MicroVMID),
		LastAction:      resp.LastAction,
		LastTransition:  firstNonZeroTimeValue(resp.LastTransition, observedAt),
		RegistryVersion: resp.RegistryVersion,
	}
	reconciled, err := ReconcileMicroVMRegistryStatus(binding, ref, status)
	if err != nil {
		return MicroVMReconcileResult{}, err
	}
	return MicroVMReconcileResult{
		LifecycleRef: reconciled,
		SessionID:    strings.TrimSpace(resp.SessionID),
		Terminal:     microVMReconcileIsTerminal(reconciled.LifecycleState, resp.ExpiresAt, observedAt),
	}, nil
}

// doControllerRequest issues one HTTP call to the governed controller API and
// decodes the response body into the AppTheory ControllerResponse shape. It
// sets the authorizer bearer token (Authorization header), the tenant + Host
// MicroVM namespace headers (the controller route handler derives tenant_id
// from x-tenant-id and namespace from x-namespace-id), and x-request-id. A nil
// body is sent for GET. A non-2xx HTTP status is a loud fail-closed error;
// the body may still carry a controller SafeError on a 2xx response, which the
// caller checks separately.
func (d *HTTPControllerDispatcher) doControllerRequest(ctx context.Context, method, url, requestID string, binding MicroVMSessionBinding, body any) (runtimemicrovm.ControllerResponse, error) {
	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: encode request: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+d.authToken)
	req.Header.Set("x-tenant-id", binding.TenantID())
	req.Header.Set("x-namespace-id", MicroVMNamespace)
	req.Header.Set("x-request-id", requestID)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	req.Header.Set("accept", "application/json")

	httpResp, err := d.httpClient.Do(req) //nolint:gosec // G704: the endpoint is the CDK-provided AppTheoryMicrovmController URL from config (not user input); the control plane never dispatches to a caller-supplied URL.
	if err != nil {
		return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: controller call failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, microvmHTTPResponseLimit))
	if err != nil {
		return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: read response: %w", err)
	}
	var resp runtimemicrovm.ControllerResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: decode response (status %d): %w", httpResp.StatusCode, err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		// A controller SafeError in the body is the preferred error surface;
		// otherwise synthesize a loud fail-closed error from the HTTP status.
		if resp.Error != nil && resp.Error.Code != "" {
			return resp, resp.Error
		}
		return resp, fmt.Errorf("hosted genesis microvm dispatch: controller returned status %d", httpResp.StatusCode)
	}
	return resp, nil
}

// microvmHTTPResponseLimit bounds the controller HTTP response body the control
// plane reads, defending against a misbehaving or hostile endpoint returning an
// unbounded stream. The ControllerResponse envelope is small (kilobytes); 1 MiB
// is a generous ceiling.
const microvmHTTPResponseLimit = 1 << 20

// microVMReconcileIsTerminal reports whether a reconciled MicroVM session is
// dead/expired and therefore must map to a loud retryable recovery failure. A
// session is terminal when its observed lifecycle state is a terminal MicroVM
// state (terminated/failed) OR its controller-reported expiry has passed. A zero
// ExpiresAt (the controller did not report one) is not treated as expired; only
// a set, past expiry forces the terminal mapping. observedAt is the controller
// get observation time used to judge expiry.
func microVMReconcileIsTerminal(state runtimemicrovm.LifecycleState, expiresAt time.Time, observedAt time.Time) bool {
	if runtimemicrovm.IsTerminalState(state) {
		return true
	}
	if expiresAt.IsZero() {
		return false
	}
	return expiresAt.Before(observedAt) || expiresAt.Equal(observedAt)
}
