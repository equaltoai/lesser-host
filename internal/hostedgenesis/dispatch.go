package hostedgenesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"
)

// ErrMicroVMDispatchUnavailable is the fail-closed error returned when the
// hosted-genesis accept path cannot dispatch a MicroVM controller run command.
// It is never a license to fall back to a synchronous control-plane LLM call;
// callers must surface it loudly (typed AppTheoryError) and persist a failed turn.
var (
	ErrMicroVMDispatchUnavailable = errors.New("hosted genesis microvm dispatch is unavailable")
	// ErrMicroVMRelaunchRequired means the AppTheory controller observed a
	// tenant/session-bound MicroVM ref that is no longer invokable (terminal or
	// expired). Callers may relaunch from Host truth/checkpoint; they must not
	// relaunch on auth, tenant-binding, transport, or validation errors.
	ErrMicroVMRelaunchRequired = errors.New("hosted genesis microvm relaunch is required")
)

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
// CannotCompletePendingTurn reports that the observed MicroVM can no longer be
// trusted to finish an already-accepted provider call. That includes terminal
// and expired sessions plus suspended sessions: suspension preserves guest
// memory, but it stops compute and does not prove that an in-flight provider
// connection can resume safely. The control plane must converge such a turn to
// durable typed failure rather than resume uncertain work or leave it pending.
type MicroVMReconcileResult struct {
	LifecycleRef              MicroVMLifecycleRef
	SessionID                 string
	CannotCompletePendingTurn bool
}

// MicroVMDispatcher is the governed HTTP dispatch boundary for the hosted
// genesis MicroVM execution path. The AI-worker accept-turn handoff, control-
// plane recovery path, and typed candidate phase continuation call through this seam
// after HostedGenesisSession truth is durably committed. DispatchMicroVMRun
// invokes the AppTheory controller run command through the governed
// AppTheoryMicrovmController HTTP API (POST /microvms) and returns the
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

// FreshMicroVMDispatcher is the official AppTheory lifecycle extension used
// only for governed same-step provider-timeout recovery. Preparation observes
// and terminates the old session when needed, performs exactly one new Run with
// the deployment-pinned image/version and waits for readiness without invoking
// provider work. Host can therefore atomically persist the new lifecycle ref
// with its retry/debit transaction before InvokeMicroVMTurn begins governed
// dispatch.
type FreshMicroVMDispatcher interface {
	MicroVMDispatcher
	PrepareFreshMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding, previous MicroVMLifecycleRef) (MicroVMDispatchResult, error)
	InvokeMicroVMTurn(ctx context.Context, requestID string, binding MicroVMSessionBinding) error
}

// microvmRunRequestPayload is the POST /microvms request body shape the
// AppTheoryMicrovmController HTTP route handler decodes
// (runtime/microvm/controller_routes.go controllerRoutePayload). The route
// handler fixes the command to "run", derives tenant_id from x-tenant-id /
// query, and namespace from x-namespace-id; only the session + image/network
// fields + AppTheory maximum runtime are caller-supplied in the body. Hosted
// Genesis deliberately omits ProviderIdlePolicy: its accepted provider work is
// asynchronous endpoint work, and AWS measures MicroVM idle from inbound
// endpoint traffic rather than guest CPU/network activity. AuthContext is
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
	endpoint         string // /microvms base URL (the construct's controller.endpoint)
	authToken        string // authorizer bearer token (the identity source)
	imageRef         string
	imageVersion     string
	executionRoleARN string
	runtimeLogGroup  string
	networkConnRef   string
	ingressConnRefs  []string
	egressConnRefs   []string
	maxDuration      int32
	httpClient       *http.Client
}

// HTTPControllerDispatcherConfig configures the HTTP MicroVM dispatcher. The
// endpoint is the AppTheoryMicrovmController /microvms base URL; the authToken
// is the authorizer bearer token presented in the Authorization header (a
// credential — never committed or logged); the image/network refs are the
// non-secret refs sent in the POST /microvms run body. MaxDurationSeconds caps
// each active run session; zero lets the controller/provider default apply.
type HTTPControllerDispatcherConfig struct {
	Endpoint             string
	AuthToken            string //nolint:gosec // G117: name describes the authorizer bearer token; in-process config struct, never JSON-serialized over an untrusted wire.
	ImageRef             string
	ImageVersion         string
	ExecutionRoleARN     string
	RuntimeLogGroup      string
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
	imageVersion := strings.TrimSpace(cfg.ImageVersion)
	executionRoleARN := strings.TrimSpace(cfg.ExecutionRoleARN)
	runtimeLogGroup := strings.TrimSpace(cfg.RuntimeLogGroup)
	networkConnRef := strings.TrimSpace(cfg.NetworkConnectorRef)
	if endpoint == "" || authToken == "" || imageRef == "" || networkConnRef == "" {
		return nil, ErrMicroVMDispatchUnavailable
	}
	if cfg.HTTPClient == nil {
		return nil, ErrMicroVMDispatchUnavailable
	}
	if cfg.MaxDurationSeconds < 0 || cfg.MaxDurationSeconds > int32(AWSLambdaMicroVMMaximumDuration/time.Second) {
		return nil, ErrMicroVMDispatchUnavailable
	}
	return &HTTPControllerDispatcher{
		endpoint:         strings.TrimRight(endpoint, "/"),
		authToken:        authToken,
		imageRef:         imageRef,
		imageVersion:     imageVersion,
		executionRoleARN: executionRoleARN,
		runtimeLogGroup:  runtimeLogGroup,
		networkConnRef:   networkConnRef,
		ingressConnRefs:  normalizeStringSlice(cfg.IngressConnectorRefs),
		egressConnRefs:   normalizeStringSlice(cfg.EgressConnectorRefs),
		maxDuration:      cfg.MaxDurationSeconds,
		httpClient:       cfg.HTTPClient,
	}, nil
}

// StartMicroVMRun POSTs /microvms to the governed controller API for the
// binding and returns the validated execution/cache ref from that run command
// only. It does not wait for the VM to become running and does not invoke the
// workload. Worker handoff code uses this split phase so Host can persist the
// lifecycle ref to HostedGenesisSession truth before asking the long-running
// MicroVM workload to do provider work.
func (d *HTTPControllerDispatcher) StartMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error) {
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
		ImageVersion:                d.imageVersion,
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
	return MicroVMDispatchResult{LifecycleRef: d.decorateLifecycleRef(ref), SessionID: strings.TrimSpace(resp.SessionID)}, nil
}

// PrepareFreshMicroVMRun uses only AppTheory v1.17.0 public controller
// operations: Get the bound old session, Terminate it when non-terminal, wait
// for terminal observation, Run the same stable Host conversation/session on
// the deployment-pinned current image/version and execution role, then Get
// until readiness. It deliberately does not invoke the workload.
func (d *HTTPControllerDispatcher) PrepareFreshMicroVMRun(ctx context.Context, requestID string, binding MicroVMSessionBinding, previous MicroVMLifecycleRef) (MicroVMDispatchResult, error) {
	requestID, err := d.validateFreshMicroVMPreparation(requestID, binding, previous)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if retireErr := d.observeAndRetirePreviousMicroVM(ctx, requestID, binding, previous); retireErr != nil {
		return MicroVMDispatchResult{}, retireErr
	}

	started, err := d.StartMicroVMRun(ctx, requestID, binding)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	ready, err := d.waitForPreparedMicroVMInvokable(ctx, requestID, binding)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if strings.TrimSpace(ready.LifecycleRef.MicroVMID) == "" || strings.TrimSpace(ready.LifecycleRef.MicroVMID) == strings.TrimSpace(previous.MicroVMID) {
		return MicroVMDispatchResult{}, ErrStaleMicroVMRegistryState
	}
	if started.SessionID != ready.SessionID {
		return MicroVMDispatchResult{}, ErrStaleMicroVMRegistryState
	}
	return ready, nil
}

func (d *HTTPControllerDispatcher) validateFreshMicroVMPreparation(requestID string, binding MicroVMSessionBinding, previous MicroVMLifecycleRef) (string, error) {
	if d == nil || strings.TrimSpace(d.imageVersion) == "" || strings.TrimSpace(d.executionRoleARN) == "" || strings.TrimSpace(d.runtimeLogGroup) == "" || d.maxDuration <= 0 {
		return "", ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return "", err
	}
	if err := previous.Validate(binding); err != nil {
		return "", err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || strings.TrimSpace(previous.MicroVMID) == "" {
		return "", ErrMicroVMDispatchUnavailable
	}
	return requestID, nil
}

func (d *HTTPControllerDispatcher) observeAndRetirePreviousMicroVM(ctx context.Context, requestID string, binding MicroVMSessionBinding, previous MicroVMLifecycleRef) error {
	getURL := d.endpoint + "/" + url.PathEscape(strings.TrimSpace(binding.ConversationID))
	observed, err := d.doControllerRequest(ctx, http.MethodGet, getURL, requestID, binding, nil)
	if err != nil {
		return err
	}
	if observed.Error != nil && observed.Error.Code != "" {
		return observed.Error
	}
	observedID := firstNonEmptyString(observed.ProviderMicroVMID, observed.MicroVMID)
	if observedID == "" || observedID != strings.TrimSpace(previous.MicroVMID) {
		return ErrStaleMicroVMRegistryState
	}
	if microVMResponseIsTerminal(observed) {
		return nil
	}
	terminated, err := d.doControllerRequest(ctx, http.MethodDelete, getURL, requestID, binding, nil)
	if err != nil {
		return err
	}
	if terminated.Error != nil && terminated.Error.Code != "" {
		return terminated.Error
	}
	if microVMResponseIsTerminal(terminated) {
		return nil
	}
	_, waitErr := d.waitForControllerMicroVMTerminal(ctx, requestID, binding, terminated)
	return waitErr
}

func (d *HTTPControllerDispatcher) waitForPreparedMicroVMInvokable(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error) {
	resp, err := d.doControllerRequest(ctx, http.MethodGet, d.endpoint+"/"+url.PathEscape(strings.TrimSpace(binding.ConversationID)), requestID, binding, nil)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMDispatchResult{}, resp.Error
	}
	ready, err := d.waitForControllerMicroVMInvokable(ctx, requestID, binding, resp)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	observedAt := time.Now().UTC()
	if !ready.LastTransition.IsZero() {
		observedAt = ready.LastTransition.UTC()
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, ready, observedAt)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	return MicroVMDispatchResult{LifecycleRef: d.decorateLifecycleRef(ref), SessionID: strings.TrimSpace(ready.SessionID)}, nil
}

func (d *HTTPControllerDispatcher) waitForControllerMicroVMTerminal(ctx context.Context, requestID string, binding MicroVMSessionBinding, resp runtimemicrovm.ControllerResponse) (runtimemicrovm.ControllerResponse, error) {
	deadline := time.Now().Add(microvmControllerReadyTimeout)
	for !microVMResponseIsTerminal(resp) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: wait for termination: %w", ctxErr)
		}
		if time.Now().After(deadline) {
			return runtimemicrovm.ControllerResponse{}, errors.New("hosted genesis microvm dispatch: microvm did not terminate")
		}
		select {
		case <-ctx.Done():
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: wait for termination: %w", ctx.Err())
		case <-time.After(microvmControllerReadyPollInterval):
		}
		var err error
		resp, err = d.doControllerRequest(ctx, http.MethodGet, d.endpoint+"/"+url.PathEscape(strings.TrimSpace(binding.ConversationID)), requestID, binding, nil)
		if err != nil {
			return runtimemicrovm.ControllerResponse{}, err
		}
		if resp.Error != nil && resp.Error.Code != "" {
			return runtimemicrovm.ControllerResponse{}, resp.Error
		}
	}
	return resp, nil
}

func (d *HTTPControllerDispatcher) decorateLifecycleRef(ref MicroVMLifecycleRef) MicroVMLifecycleRef {
	ref.ImageRef = d.imageRef
	ref.ImageVersion = d.imageVersion
	ref.ExecutionRoleARN = d.executionRoleARN
	ref.MaximumDurationSeconds = d.maxDuration
	ref.RuntimeLogGroup = d.runtimeLogGroup
	ref.NetworkConnectorRef = d.networkConnRef
	return normalizeMicroVMLifecycleRef(ref)
}

// EnsureMicroVMTurnSession revalidates a Host-recorded AppTheory MicroVM
// lifecycle ref through the controller get route, resumes suspended sessions
// through the canonical AppTheory resume route, and returns an invokable
// lifecycle ref. Terminal/expired sessions return ErrMicroVMRelaunchRequired so
// the caller can relaunch from Host truth/checkpoint; auth, tenant-binding,
// transport, and validation errors are never treated as relaunchable.
func (d *HTTPControllerDispatcher) EnsureMicroVMTurnSession(ctx context.Context, requestID string, binding MicroVMSessionBinding, ref MicroVMLifecycleRef) (MicroVMDispatchResult, error) {
	if d == nil {
		return MicroVMDispatchResult{}, ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return MicroVMDispatchResult{}, err
	}
	if err := ref.Validate(binding); err != nil {
		return MicroVMDispatchResult{}, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return MicroVMDispatchResult{}, ErrMicroVMDispatchUnavailable
	}
	sessionID := strings.TrimSpace(binding.ConversationID)
	getURL := d.endpoint + "/" + url.PathEscape(sessionID)
	resp, err := d.doControllerRequest(ctx, http.MethodGet, getURL, requestID, binding, nil)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMDispatchResult{}, resp.Error
	}
	resp, err = d.ensureControllerMicroVMInvokable(ctx, requestID, binding, resp)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	lifecycleRef, err := MicroVMLifecycleRefFromResponse(binding, resp, observedAt)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	return MicroVMDispatchResult{LifecycleRef: d.decorateLifecycleRef(lifecycleRef), SessionID: strings.TrimSpace(resp.SessionID)}, nil
}

// InvokeMicroVMTurn invokes Host's hosted-genesis turn endpoint through
// AppTheory's canonical controller invoke route for an already-validated,
// invokable MicroVM session. It is split from EnsureMicroVMTurnSession so the
// worker can persist the latest lifecycle ref before doing provider work.
func (d *HTTPControllerDispatcher) InvokeMicroVMTurn(ctx context.Context, requestID string, binding MicroVMSessionBinding) error {
	if d == nil {
		return ErrMicroVMDispatchUnavailable
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ErrMicroVMDispatchUnavailable
	}
	return d.invokeMicroVMTurn(ctx, requestID, binding)
}

// WaitAndInvokeMicroVMTurn waits for an existing AppTheory MicroVM session to
// reach an invokable running/ready state via the controller GET route, then
// invokes Host's hosted-genesis turn endpoint through AppTheory's canonical
// controller invoke route. It returns the lifecycle ref observed immediately
// before invoke.
func (d *HTTPControllerDispatcher) WaitAndInvokeMicroVMTurn(ctx context.Context, requestID string, binding MicroVMSessionBinding) (MicroVMDispatchResult, error) {
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
	resp, err := d.doControllerRequest(ctx, http.MethodGet, d.endpoint+"/"+url.PathEscape(strings.TrimSpace(binding.ConversationID)), requestID, binding, nil)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	if resp.Error != nil && resp.Error.Code != "" {
		return MicroVMDispatchResult{}, resp.Error
	}
	readyResp, waitErr := d.waitForControllerMicroVMInvokable(ctx, requestID, binding, resp)
	if waitErr != nil {
		return MicroVMDispatchResult{}, waitErr
	}
	if invokeErr := d.invokeMicroVMTurn(ctx, requestID, binding); invokeErr != nil {
		return MicroVMDispatchResult{}, invokeErr
	}
	observedAt := time.Now().UTC()
	if !readyResp.LastTransition.IsZero() {
		observedAt = readyResp.LastTransition.UTC()
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, readyResp, observedAt)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	return MicroVMDispatchResult{LifecycleRef: d.decorateLifecycleRef(ref), SessionID: strings.TrimSpace(readyResp.SessionID)}, nil
}

// DispatchMicroVMRun POSTs /microvms, waits until running, invokes the workload,
// and returns the validated running lifecycle ref. It fails closed when the
// dispatcher is misconfigured, the HTTP call fails, the controller returns a
// non-2xx status, or the response envelope carries a controller error; it never
// falls back to a local execution path.
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
		ImageVersion:                d.imageVersion,
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
	readyResp, waitErr := d.waitForControllerMicroVMInvokable(ctx, requestID, binding, resp)
	if waitErr != nil {
		return MicroVMDispatchResult{}, waitErr
	}
	if invokeErr := d.invokeMicroVMTurn(ctx, requestID, binding); invokeErr != nil {
		return MicroVMDispatchResult{}, invokeErr
	}
	observedAt := time.Now().UTC()
	if !readyResp.LastTransition.IsZero() {
		observedAt = readyResp.LastTransition.UTC()
	}
	ref, err := MicroVMLifecycleRefFromResponse(binding, readyResp, observedAt)
	if err != nil {
		return MicroVMDispatchResult{}, err
	}
	return MicroVMDispatchResult{LifecycleRef: d.decorateLifecycleRef(ref), SessionID: strings.TrimSpace(readyResp.SessionID)}, nil
}

// ensureControllerMicroVMInvokable maps the controller get/resume contract into
// the Host handoff contract. Suspended sessions are resumed through
// POST /microvms/{session_id}/resume. Terminal or expired sessions are a safe
// checkpoint/relaunch signal; all other errors remain fail-closed.
func (d *HTTPControllerDispatcher) ensureControllerMicroVMInvokable(ctx context.Context, requestID string, binding MicroVMSessionBinding, resp runtimemicrovm.ControllerResponse) (runtimemicrovm.ControllerResponse, error) {
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	state := firstNonEmptyLifecycleState(resp.LifecycleState, resp.State)
	if microVMReconcileIsTerminal(state, resp.ExpiresAt, observedAt) {
		return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: %w (state=%s)", ErrMicroVMRelaunchRequired, state)
	}
	if microVMInvokableState(state) {
		return resp, nil
	}
	if state == runtimemicrovm.StateSuspended {
		resumeURL := d.endpoint + "/" + url.PathEscape(strings.TrimSpace(binding.ConversationID)) + "/resume"
		resumed, err := d.doControllerRequest(ctx, http.MethodPost, resumeURL, requestID, binding, nil)
		if err != nil {
			return runtimemicrovm.ControllerResponse{}, err
		}
		if resumed.Error != nil && resumed.Error.Code != "" {
			return runtimemicrovm.ControllerResponse{}, resumed.Error
		}
		resp = resumed
	}
	return d.waitForControllerMicroVMInvokable(ctx, requestID, binding, resp)
}

// waitForControllerMicroVMInvokable uses AppTheory's canonical controller `get`
// route to observe an invokable provider state before Host invokes the workload.
// Running and ready are both invokable under the v1.17.0 controller/provider
// contract; terminal/expired sessions return ErrMicroVMRelaunchRequired so the
// worker can relaunch from Host truth/checkpoint instead of trusting process
// memory as correctness state.
func (d *HTTPControllerDispatcher) waitForControllerMicroVMInvokable(ctx context.Context, requestID string, binding MicroVMSessionBinding, runResp runtimemicrovm.ControllerResponse) (runtimemicrovm.ControllerResponse, error) {
	state := firstNonEmptyLifecycleState(runResp.LifecycleState, runResp.State)
	if microVMInvokableState(state) {
		return runResp, nil
	}
	if microVMResponseIsTerminal(runResp) {
		return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: %w (state=%s)", ErrMicroVMRelaunchRequired, state)
	}
	deadline := time.Now().Add(microvmControllerReadyTimeout)
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: wait for invokable: %w", ctxErr)
		}
		if time.Now().After(deadline) {
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: microvm did not reach invokable state (last state=%s)", state)
		}
		select {
		case <-ctx.Done():
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: wait for invokable: %w", ctx.Err())
		case <-time.After(microvmControllerReadyPollInterval):
		}
		resp, err := d.doControllerRequest(ctx, http.MethodGet, d.endpoint+"/"+url.PathEscape(strings.TrimSpace(binding.ConversationID)), requestID, binding, nil)
		if err != nil {
			return runtimemicrovm.ControllerResponse{}, err
		}
		if resp.Error != nil && resp.Error.Code != "" {
			return runtimemicrovm.ControllerResponse{}, resp.Error
		}
		state = firstNonEmptyLifecycleState(resp.LifecycleState, resp.State)
		if microVMInvokableState(state) {
			return resp, nil
		}
		if microVMResponseIsTerminal(resp) {
			return runtimemicrovm.ControllerResponse{}, fmt.Errorf("hosted genesis microvm dispatch: %w (state=%s)", ErrMicroVMRelaunchRequired, state)
		}
	}
}

func microVMInvokableState(state runtimemicrovm.LifecycleState) bool {
	return state == runtimemicrovm.StateRunning || state == runtimemicrovm.StateReady
}

func microVMResponseIsTerminal(resp runtimemicrovm.ControllerResponse) bool {
	observedAt := time.Now().UTC()
	if !resp.LastTransition.IsZero() {
		observedAt = resp.LastTransition.UTC()
	}
	return microVMReconcileIsTerminal(firstNonEmptyLifecycleState(resp.LifecycleState, resp.State), resp.ExpiresAt, observedAt)
}

// invokeMicroVMTurn sends the hosted-genesis lifecycle event through
// AppTheory's canonical controller invoke route. The controller reads the
// tenant-bound endpoint from its registry, mints the Lambda MicroVM provider
// token internally, strips control/proxy headers, and returns only the workload
// HTTP response. Host never handles endpoint URLs or provider token values.
func (d *HTTPControllerDispatcher) invokeMicroVMTurn(ctx context.Context, requestID string, binding MicroVMSessionBinding) error {
	event, err := NewMicroVMTurnLifecycleEvent(requestID, binding)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("hosted genesis microvm dispatch: encode invoke event: %w", err)
	}
	invokeURL := d.endpoint + "/" + url.PathEscape(strings.TrimSpace(binding.ConversationID)) + "/invoke" + MicroVMTurnEndpointPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, invokeURL, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("hosted genesis microvm dispatch: build invoke request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+d.authToken)
	req.Header.Set("x-tenant-id", binding.TenantID())
	req.Header.Set("x-namespace-id", MicroVMNamespace)
	req.Header.Set("x-request-id", requestID)
	req.Header.Set("x-apptheory-microvm-port", fmt.Sprintf("%d", MicroVMTurnPort))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")

	httpResp, err := d.httpClient.Do(req) //nolint:gosec // G704: the endpoint is the CDK-provided AppTheoryMicrovmController URL from config; the control plane never dispatches to caller-supplied URLs.
	if err != nil {
		return fmt.Errorf("hosted genesis microvm dispatch: controller invoke failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, microvmHTTPResponseLimit))
	if err != nil {
		return fmt.Errorf("hosted genesis microvm dispatch: read invoke response: %w", err)
	}
	var result runtimemicrovm.LifecycleResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("hosted genesis microvm dispatch: decode invoke response (status %d): %w", httpResp.StatusCode, err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		if result.Error != nil && result.Error.Code != "" {
			return fmt.Errorf("hosted genesis microvm dispatch: workload returned status %d: %w", httpResp.StatusCode, result.Error)
		}
		return fmt.Errorf("hosted genesis microvm dispatch: workload returned status %d", httpResp.StatusCode)
	}
	return ValidateMicroVMTurnResult(result, requestID, binding)
}

// ReconcileMicroVM GETs /microvms/{session_id} from the governed controller API
// for the binding's existing execution/cache session and returns the reconciled
// lifecycle ref reflecting real VM state. It fails closed when the dispatcher is
// misconfigured, the HTTP call fails, the controller returns a non-2xx status,
// or the observed state no longer maps to the Host session binding
// (stale/tenant mismatch). It never falls back to a local execution path and
// never swallows a dead/expired VM as a silent no-op: terminal observed state is
// reported via CannotCompletePendingTurn=true so the caller maps it to a loud
// failure.
//
// H1.4: an accepted turn cannot complete when the observed lifecycle is
// terminated/failed/suspended, or the controller-reported session expiry has
// passed. Suspended guest memory is not proof that the provider TCP/TLS stream
// survived, so wait-only observation never resumes it. Recovery can launch one
// fresh AppTheory runtime from Host truth when the durable budget allows.
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
		LifecycleRef:              reconciled,
		SessionID:                 strings.TrimSpace(resp.SessionID),
		CannotCompletePendingTurn: microVMReconcileCannotCompletePendingTurn(reconciled.LifecycleState, resp.ExpiresAt, observedAt),
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

const (
	// microvmControllerReadyTimeout bounds how long the control plane waits for
	// the AppTheory controller get route to observe a running MicroVM before the
	// invoke call. This replaces raw get-microvm polling without weakening the
	// fail-closed dispatch posture.
	microvmControllerReadyTimeout      = 60 * time.Second
	microvmControllerReadyPollInterval = 250 * time.Millisecond
)

// microVMReconcileCannotCompletePendingTurn reports whether a reconciled
// MicroVM cannot safely finish accepted provider work and must map to a loud
// typed recovery failure. A zero ExpiresAt is not treated as expired.
func microVMReconcileCannotCompletePendingTurn(state runtimemicrovm.LifecycleState, expiresAt time.Time, observedAt time.Time) bool {
	return state == runtimemicrovm.StateSuspended || microVMReconcileIsTerminal(state, expiresAt, observedAt)
}

// microVMReconcileIsTerminal is the narrower lifecycle predicate used before
// dispatch, where an idle suspended VM can still be explicitly resumed before
// any provider work is accepted. Post-accept observation uses the stronger
// predicate above and never resumes uncertain work.
func microVMReconcileIsTerminal(state runtimemicrovm.LifecycleState, expiresAt time.Time, observedAt time.Time) bool {
	if runtimemicrovm.IsTerminalState(state) {
		return true
	}
	if expiresAt.IsZero() {
		return false
	}
	return expiresAt.Before(observedAt) || expiresAt.Equal(observedAt)
}
