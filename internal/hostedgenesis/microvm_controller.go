package hostedgenesis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
)

const (
	microVMLifecycleRefSource = MicroVMSourceOfTruth

	// MicroVMRegistryReconstructionTTL bounds reconstructed AppTheory registry
	// cache state. Host truth remains HostedGenesisSession; reconstructed records
	// only rehydrate execution/cache bindings long enough for M16 operations.
	MicroVMRegistryReconstructionTTL = time.Hour
)

var (
	ErrMicroVMControllerIncomplete = errors.New("hosted genesis microvm controller is incomplete")
	ErrInvalidMicroVMLifecycleRef  = errors.New("hosted genesis microvm lifecycle ref is invalid")
	ErrStaleMicroVMRegistryState   = errors.New("hosted genesis microvm registry state is stale")
)

// MicroVMControllerRuntime is Host's thin AppTheory runtime boundary for hosted
// genesis MicroVM execution/cache sessions. It owns no workflow truth: callers
// must commit/load HostedGenesisSession first, then pass only the safe binding
// ids through this controller.
type MicroVMControllerRuntime struct {
	controller                  *runtimemicrovm.Controller
	imageRef                    string
	networkConnectorRef         string
	ingressNetworkConnectorRefs []string
	egressNetworkConnectorRefs  []string
	// maximumDurationSeconds caps each dispatched MicroVM run session duration
	// (decision 7: sized for the longest LLM turn plus in-VM phase tool and
	// validation work). Zero/positive-only; a non-positive value lets AppTheory's
	// provider default apply. It is set on the run request envelope only.
	maximumDurationSeconds int32
}

// MicroVMControllerRuntimeConfig configures the AppTheory M16 controller
// boundary. Provider must be an AppTheory runtime/microvm.Provider; Host
// business logic never receives a raw AWS SDK client, provider token, or
// lifecycle hook payload.
type MicroVMControllerRuntimeConfig struct {
	Provider                    runtimemicrovm.Provider
	Registry                    runtimemicrovm.SessionRegistry
	ReconstructionHook          runtimemicrovm.SessionReconstructionHook
	ImageRef                    string
	NetworkConnectorRef         string
	IngressNetworkConnectorRefs []string
	EgressNetworkConnectorRefs  []string
	ControllerID                string
	Clock                       runtimemicrovm.Clock
	IDGenerator                 runtimemicrovm.IDGenerator
	SessionTTL                  time.Duration
	ReconstructionStaleAfter    time.Duration
	// MaximumDurationSeconds caps each dispatched MicroVM run session duration,
	// sized for the longest provider-backed typed-section phase. Non-positive
	// leaves the AppTheory provider default in place.
	MaximumDurationSeconds int32
}

// NewMicroVMControllerRuntime creates the AppTheory M16 real controller Host
// uses for hosted-genesis MicroVM dispatch and deterministic tests.
func NewMicroVMControllerRuntime(cfg MicroVMControllerRuntimeConfig) (*MicroVMControllerRuntime, error) {
	imageRef := strings.TrimSpace(cfg.ImageRef)
	networkConnectorRef := strings.TrimSpace(cfg.NetworkConnectorRef)
	if cfg.Provider == nil || cfg.Registry == nil || imageRef == "" || networkConnectorRef == "" {
		return nil, ErrMicroVMControllerIncomplete
	}
	registry := cfg.Registry
	if cfg.ReconstructionHook != nil {
		opts := []runtimemicrovm.SessionReconstructionOption{}
		if cfg.ReconstructionStaleAfter > 0 {
			opts = append(opts, runtimemicrovm.WithSessionReconstructionStaleAfter(cfg.ReconstructionStaleAfter))
		}
		if cfg.Clock != nil {
			opts = append(opts, runtimemicrovm.WithSessionReconstructionClock(cfg.Clock))
		}
		wrapped, err := runtimemicrovm.NewReconstructingSessionRegistry(registry, cfg.ReconstructionHook, opts...)
		if err != nil {
			return nil, err
		}
		registry = wrapped
	}
	opts := []runtimemicrovm.ControllerOption{runtimemicrovm.WithControllerID(firstNonEmptyString(cfg.ControllerID, MicroVMControllerID))}
	if cfg.Clock != nil {
		opts = append(opts, runtimemicrovm.WithControllerClock(cfg.Clock))
	}
	if cfg.IDGenerator != nil {
		opts = append(opts, runtimemicrovm.WithControllerIDGenerator(cfg.IDGenerator))
	}
	if cfg.SessionTTL > 0 {
		opts = append(opts, runtimemicrovm.WithControllerSessionTTL(cfg.SessionTTL))
	}
	opts = append(opts, runtimemicrovm.WithControllerDeploymentDefaults(runtimemicrovm.ControllerDeploymentDefaults{
		ImageRef:                    imageRef,
		NetworkConnectorRef:         networkConnectorRef,
		IngressNetworkConnectorRefs: normalizeStringSlice(cfg.IngressNetworkConnectorRefs),
		EgressNetworkConnectorRefs:  normalizeStringSlice(cfg.EgressNetworkConnectorRefs),
	}))
	controller, err := runtimemicrovm.NewRealController(cfg.Provider, registry, opts...)
	if err != nil {
		return nil, err
	}
	return &MicroVMControllerRuntime{
		controller:                  controller,
		imageRef:                    imageRef,
		networkConnectorRef:         networkConnectorRef,
		ingressNetworkConnectorRefs: normalizeStringSlice(cfg.IngressNetworkConnectorRefs),
		egressNetworkConnectorRefs:  normalizeStringSlice(cfg.EgressNetworkConnectorRefs),
		maximumDurationSeconds:      cfg.MaximumDurationSeconds,
	}, nil
}

// Controller returns the AppTheory controller so cmd entrypoints can register
// the canonical M16 HTTP routes with microvm.RegisterControllerRoutes.
func (r *MicroVMControllerRuntime) Controller() *runtimemicrovm.Controller {
	if r == nil {
		return nil
	}
	return r.controller
}

// Handle passes a prebuilt ControllerRequest through AppTheory's validator and
// safe envelope. It exists for Lambda handlers; business code should prefer the
// typed helpers below so run commands always use the CDK-provided refs.
func (r *MicroVMControllerRuntime) Handle(ctx context.Context, req runtimemicrovm.ControllerRequest) (runtimemicrovm.ControllerResponse, error) {
	if r == nil || r.controller == nil {
		return runtimemicrovm.ControllerResponse{}, ErrMicroVMControllerIncomplete
	}
	if req.Command == runtimemicrovm.CommandRun {
		if strings.TrimSpace(req.ImageRef) == "" {
			req.ImageRef = r.imageRef
		}
		if strings.TrimSpace(req.NetworkConnectorRef) == "" {
			req.NetworkConnectorRef = r.networkConnectorRef
		}
		if len(req.IngressNetworkConnectorRefs) == 0 {
			req.IngressNetworkConnectorRefs = append([]string(nil), r.ingressNetworkConnectorRefs...)
		}
		if len(req.EgressNetworkConnectorRefs) == 0 {
			req.EgressNetworkConnectorRefs = append([]string(nil), r.egressNetworkConnectorRefs...)
		}
		// P52 H1.5 decision 7: cap the dispatched run session duration for the
		// longest LLM turn plus in-VM phase-tool validation. A caller-provided positive value
		// wins; otherwise the runtime-configured cap applies. A non-positive cap
		// leaves the AppTheory provider default in place.
		if req.MaximumDurationSeconds <= 0 && r.maximumDurationSeconds > 0 {
			req.MaximumDurationSeconds = r.maximumDurationSeconds
		}
	}
	if req.Command == runtimemicrovm.CommandAuthToken && len(req.AllowedPortScope) == 0 {
		req.AllowedPortScope = []runtimemicrovm.ProviderPortScope{{Port: 443}}
	}
	return r.controller.Handle(ctx, req)
}

// Run issues an AppTheory M16 run command for the already-committed Host
// session binding.
func (r *MicroVMControllerRuntime) Run(ctx context.Context, requestID string, binding MicroVMSessionBinding) (runtimemicrovm.ControllerResponse, error) {
	req, err := NewMicroVMRunRequest(requestID, binding, r.imageRef, r.networkConnectorRef, r.ingressNetworkConnectorRefs, r.egressNetworkConnectorRefs)
	if err != nil {
		return runtimemicrovm.ControllerResponse{}, err
	}
	return r.Handle(ctx, req)
}

// Command issues canonical M16 operations for an existing execution/cache ref.
func (r *MicroVMControllerRuntime) Command(ctx context.Context, command runtimemicrovm.Command, requestID string, binding MicroVMSessionBinding) (runtimemicrovm.ControllerResponse, error) {
	req, err := NewMicroVMOperationRequest(command, requestID, binding)
	if err != nil {
		return runtimemicrovm.ControllerResponse{}, err
	}
	return r.Handle(ctx, req)
}

// MicroVMLifecycleRef is the only MicroVM execution/cache state Host records on
// HostedGenesisSession. It intentionally excludes endpoint auth tokens, bearer
// tokens, raw lifecycle payloads, raw transcripts, provider credentials, and AWS
// credentials. It is reconstructible execution state, not business truth.
type MicroVMLifecycleRef struct {
	SourceOfTruth          string                        `json:"source_of_truth"`
	TenantID               string                        `json:"tenant_id"`
	Namespace              string                        `json:"namespace"`
	SessionID              string                        `json:"session_id"`
	LifecycleState         runtimemicrovm.LifecycleState `json:"lifecycle_state"`
	DesiredState           runtimemicrovm.LifecycleState `json:"desired_state,omitempty"`
	MicroVMID              string                        `json:"microvm_id,omitempty"`
	ImageRef               string                        `json:"image_ref,omitempty"`
	ImageVersion           string                        `json:"image_version,omitempty"`
	ExecutionRoleARN       string                        `json:"execution_role_arn,omitempty"`
	MaximumDurationSeconds int32                         `json:"maximum_duration_seconds,omitempty"`
	RuntimeLogGroup        string                        `json:"runtime_log_group,omitempty"`
	NetworkConnectorRef    string                        `json:"network_connector_ref,omitempty"`
	LastAction             runtimemicrovm.Command        `json:"last_action"`
	LastTransition         time.Time                     `json:"last_transition,omitempty"`
	RegistryVersion        int64                         `json:"registry_version,omitempty"`
	UpdatedAt              time.Time                     `json:"updated_at"`
}

// Validate proves the ref still maps to the Host session binding and AppTheory
// namespace before Host records or trusts it as execution/cache state.
func (r MicroVMLifecycleRef) Validate(binding MicroVMSessionBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	r = normalizeMicroVMLifecycleRef(r)
	if r.SourceOfTruth != microVMLifecycleRefSource || r.TenantID != binding.TenantID() || r.Namespace != MicroVMNamespace || r.SessionID != strings.TrimSpace(binding.ConversationID) {
		return ErrInvalidMicroVMLifecycleRef
	}
	if r.LifecycleState == "" || r.LastAction == "" || r.UpdatedAt.IsZero() {
		return ErrInvalidMicroVMLifecycleRef
	}
	status := runtimemicrovm.SessionStatus{
		TenantID:        r.TenantID,
		Namespace:       r.Namespace,
		SessionID:       r.SessionID,
		State:           r.LifecycleState,
		DesiredState:    firstNonEmptyLifecycleState(r.DesiredState, r.LifecycleState),
		LifecycleState:  r.LifecycleState,
		MicroVMID:       r.MicroVMID,
		LastAction:      r.LastAction,
		LastTransition:  firstNonZeroTimeValue(r.LastTransition, r.UpdatedAt),
		RegistryVersion: firstPositiveInt64(r.RegistryVersion, 1),
	}
	if err := runtimemicrovm.ValidateSessionStatus(status); err != nil {
		return ErrInvalidMicroVMLifecycleRef
	}
	return nil
}

// MicroVMLifecycleRefFromResponse converts AppTheory's safe controller envelope
// into Host's compact execution/cache ref.
func MicroVMLifecycleRefFromResponse(binding MicroVMSessionBinding, resp runtimemicrovm.ControllerResponse, observedAt time.Time) (MicroVMLifecycleRef, error) {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	state := firstNonEmptyLifecycleState(resp.LifecycleState, resp.State)
	ref := MicroVMLifecycleRef{
		SourceOfTruth:   microVMLifecycleRefSource,
		TenantID:        strings.TrimSpace(resp.TenantID),
		Namespace:       strings.TrimSpace(resp.Namespace),
		SessionID:       strings.TrimSpace(resp.SessionID),
		LifecycleState:  state,
		DesiredState:    resp.DesiredState,
		MicroVMID:       firstNonEmptyString(resp.ProviderMicroVMID, resp.MicroVMID),
		LastAction:      resp.Command,
		LastTransition:  firstNonZeroTimeValue(resp.LastTransition, observedAt),
		RegistryVersion: resp.RegistryVersion,
		UpdatedAt:       observedAt.UTC(),
	}
	if ref.DesiredState == "" {
		ref.DesiredState = state
	}
	if ref.TenantID == "" {
		ref.TenantID = binding.TenantID()
	}
	if ref.Namespace == "" {
		ref.Namespace = MicroVMNamespace
	}
	if ref.SessionID == "" {
		ref.SessionID = strings.TrimSpace(binding.ConversationID)
	}
	if ref.LastAction == "" {
		ref.LastAction = resp.Command
	}
	if ref.LastTransition.IsZero() {
		ref.LastTransition = ref.UpdatedAt
	}
	ref = normalizeMicroVMLifecycleRef(ref)
	if err := ref.Validate(binding); err != nil {
		return MicroVMLifecycleRef{}, err
	}
	return ref, nil
}

// ReconcileMicroVMRegistryStatus compares non-authoritative registry/cache
// status to the Host session binding. Registry loss or tenant/session mismatch
// is stale cache; Host must recover from HostedGenesisSession rather than trust
// the registry as business state.
func ReconcileMicroVMRegistryStatus(binding MicroVMSessionBinding, ref MicroVMLifecycleRef, status runtimemicrovm.SessionStatus) (MicroVMLifecycleRef, error) {
	if err := ref.Validate(binding); err != nil {
		return MicroVMLifecycleRef{}, err
	}
	if err := runtimemicrovm.ValidateSessionStatus(status); err != nil {
		return MicroVMLifecycleRef{}, ErrStaleMicroVMRegistryState
	}
	if strings.TrimSpace(status.TenantID) != binding.TenantID() || strings.TrimSpace(status.Namespace) != MicroVMNamespace || strings.TrimSpace(status.SessionID) != strings.TrimSpace(binding.ConversationID) {
		return MicroVMLifecycleRef{}, ErrStaleMicroVMRegistryState
	}
	ref.LifecycleState = status.LifecycleState
	if ref.LifecycleState == "" {
		ref.LifecycleState = status.State
	}
	ref.DesiredState = status.DesiredState
	ref.MicroVMID = strings.TrimSpace(status.MicroVMID)
	ref.LastAction = status.LastAction
	ref.LastTransition = status.LastTransition
	ref.RegistryVersion = status.RegistryVersion
	ref.UpdatedAt = time.Now().UTC()
	ref = normalizeMicroVMLifecycleRef(ref)
	if err := ref.Validate(binding); err != nil {
		return MicroVMLifecycleRef{}, err
	}
	return ref, nil
}

func normalizeMicroVMLifecycleRef(ref MicroVMLifecycleRef) MicroVMLifecycleRef {
	ref.SourceOfTruth = strings.TrimSpace(ref.SourceOfTruth)
	ref.TenantID = strings.TrimSpace(ref.TenantID)
	ref.Namespace = strings.TrimSpace(ref.Namespace)
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	ref.LifecycleState = runtimemicrovm.LifecycleState(strings.TrimSpace(string(ref.LifecycleState)))
	ref.DesiredState = runtimemicrovm.LifecycleState(strings.TrimSpace(string(ref.DesiredState)))
	ref.MicroVMID = strings.TrimSpace(ref.MicroVMID)
	ref.ImageRef = strings.TrimSpace(ref.ImageRef)
	ref.ImageVersion = strings.TrimSpace(ref.ImageVersion)
	ref.ExecutionRoleARN = strings.TrimSpace(ref.ExecutionRoleARN)
	ref.RuntimeLogGroup = strings.TrimSpace(ref.RuntimeLogGroup)
	ref.NetworkConnectorRef = strings.TrimSpace(ref.NetworkConnectorRef)
	ref.LastAction = runtimemicrovm.Command(strings.TrimSpace(string(ref.LastAction)))
	if !ref.LastTransition.IsZero() {
		ref.LastTransition = ref.LastTransition.UTC()
	}
	if !ref.UpdatedAt.IsZero() {
		ref.UpdatedAt = ref.UpdatedAt.UTC()
	}
	return ref
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyLifecycleState(values ...runtimemicrovm.LifecycleState) runtimemicrovm.LifecycleState {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroTimeValue(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// FormatMicroVMExecutionStateRef returns a compact non-secret execution ref string
// suitable for HostedGenesisSession.ExecutionStateRef.
func FormatMicroVMExecutionStateRef(ref MicroVMLifecycleRef) string {
	ref = normalizeMicroVMLifecycleRef(ref)
	return fmt.Sprintf("microvm://%s/%s/%s#%s@%d", ref.SourceOfTruth, ref.TenantID, ref.SessionID, ref.LifecycleState, firstPositiveInt64(ref.RegistryVersion, 1))
}
