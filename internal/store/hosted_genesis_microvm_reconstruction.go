package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/v2/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// HostedGenesisMicroVMReconstructionConfig contains safe deployment refs needed
// to rebuild AppTheory execution/cache state from Host truth. It must never
// contain browser Host credentials, Instance API keys, provider tokens, wallet
// signatures, SSM values, AWS credentials, or raw transcripts.
type HostedGenesisMicroVMReconstructionConfig struct {
	ImageRef                    string
	NetworkConnectorRef         string
	IngressNetworkConnectorRefs []string
	EgressNetworkConnectorRefs  []string
	ControllerID                string
	TTL                         time.Duration
}

// HostedGenesisMicroVMReconstructionHook returns the Host-owned AppTheory M16
// reconstruction hook. The lookup is tenant-scoped by slug parsed from
// request.TenantID; Host never looks up hosted-genesis sessions by conversation
// id alone.
func (s *Store) HostedGenesisMicroVMReconstructionHook(cfg HostedGenesisMicroVMReconstructionConfig) runtimemicrovm.SessionReconstructionHook {
	return func(ctx context.Context, request runtimemicrovm.SessionReconstructionRequest) (runtimemicrovm.SessionRecord, error) {
		slug := slugFromMicroVMTenantID(request.TenantID)
		if slug == "" || strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.Namespace) != hostedgenesis.MicroVMNamespace {
			return runtimemicrovm.SessionRecord{}, fmt.Errorf("hosted genesis microvm reconstruction request is not tenant-bound")
		}
		if s == nil {
			return runtimemicrovm.SessionRecord{}, fmt.Errorf("hosted genesis microvm reconstruction store is unavailable")
		}
		session, err := s.GetHostedGenesisSession(ctx, slug, request.SessionID)
		if err != nil {
			return runtimemicrovm.SessionRecord{}, err
		}
		return ReconstructMicroVMRegistryRecordFromHostedGenesisSession(session, request, cfg)
	}
}

// ReconstructMicroVMRegistryRecordFromHostedGenesisSession maps authorized Host
// HostedGenesisSession truth to the AppTheory SessionRecord cache shape.
func ReconstructMicroVMRegistryRecordFromHostedGenesisSession(
	session *models.HostedGenesisSession,
	request runtimemicrovm.SessionReconstructionRequest,
	cfg HostedGenesisMicroVMReconstructionConfig,
) (runtimemicrovm.SessionRecord, error) {
	input, err := prepareMicroVMReconstructionInput(session, request, cfg)
	if err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	record := buildMicroVMReconstructionRecord(session, request, cfg, input)
	if err := runtimemicrovm.ValidateSessionRecord(record); err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	return record, nil
}

type microVMReconstructionInput struct {
	Binding             hostedgenesis.MicroVMSessionBinding
	Ref                 hostedgenesis.MicroVMLifecycleRef
	ProviderMicroVMID   string
	ImageRef            string
	NetworkConnectorRef string
}

func prepareMicroVMReconstructionInput(
	session *models.HostedGenesisSession,
	request runtimemicrovm.SessionReconstructionRequest,
	cfg HostedGenesisMicroVMReconstructionConfig,
) (microVMReconstructionInput, error) {
	if session == nil || session.MicroVMLifecycleRef == nil {
		return microVMReconstructionInput{}, fmt.Errorf("hosted genesis microvm lifecycle ref is unavailable")
	}
	binding := session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		return microVMReconstructionInput{}, err
	}
	if !microVMReconstructionRequestMatches(binding, request) {
		return microVMReconstructionInput{}, fmt.Errorf("hosted genesis microvm reconstruction tenant/session mismatch")
	}
	ref := *session.MicroVMLifecycleRef
	if err := ref.Validate(binding); err != nil {
		return microVMReconstructionInput{}, err
	}
	input := microVMReconstructionInput{
		Binding:             binding,
		Ref:                 ref,
		ProviderMicroVMID:   strings.TrimSpace(ref.MicroVMID),
		ImageRef:            firstNonEmptyStoreString(ref.ImageRef, cfg.ImageRef),
		NetworkConnectorRef: firstNonEmptyStoreString(ref.NetworkConnectorRef, cfg.NetworkConnectorRef),
	}
	if input.ProviderMicroVMID == "" || input.ImageRef == "" || input.NetworkConnectorRef == "" {
		return microVMReconstructionInput{}, fmt.Errorf("hosted genesis microvm reconstruction binding is incomplete")
	}
	return input, nil
}

func microVMReconstructionRequestMatches(
	binding hostedgenesis.MicroVMSessionBinding,
	request runtimemicrovm.SessionReconstructionRequest,
) bool {
	return binding.TenantID() == strings.TrimSpace(request.TenantID) &&
		strings.TrimSpace(request.Namespace) == hostedgenesis.MicroVMNamespace &&
		strings.TrimSpace(binding.ConversationID) == strings.TrimSpace(request.SessionID)
}

func buildMicroVMReconstructionRecord(
	session *models.HostedGenesisSession,
	request runtimemicrovm.SessionReconstructionRequest,
	cfg HostedGenesisMicroVMReconstructionConfig,
	input microVMReconstructionInput,
) runtimemicrovm.SessionRecord {
	now := firstNonZeroStoreTime(request.Now, time.Now().UTC())
	createdAt := firstNonZeroStoreTime(session.CreatedAt, now)
	updatedAt := firstNonZeroStoreTime(input.Ref.UpdatedAt, session.UpdatedAt, now)
	state := input.Ref.LifecycleState
	record := runtimemicrovm.SessionRecord{
		TenantID:                    strings.TrimSpace(request.TenantID),
		Namespace:                   hostedgenesis.MicroVMNamespace,
		SessionID:                   strings.TrimSpace(request.SessionID),
		State:                       state,
		DesiredState:                firstNonEmptyLifecycleState(input.Ref.DesiredState, state),
		MicroVMID:                   input.ProviderMicroVMID,
		ProviderID:                  runtimemicrovm.AWSLambdaMicroVMProviderID,
		ProviderMicroVMID:           input.ProviderMicroVMID,
		ProviderState:               string(state),
		AWSLifecycleState:           string(state),
		ImageRef:                    input.ImageRef,
		NetworkConnectorRef:         input.NetworkConnectorRef,
		IngressNetworkConnectorRefs: normalizeStoreStringSlice(cfg.IngressNetworkConnectorRefs),
		EgressNetworkConnectorRefs:  normalizeStoreStringSlice(cfg.EgressNetworkConnectorRefs),
		ControllerID:                firstNonEmptyStoreString(cfg.ControllerID, hostedgenesis.MicroVMControllerID),
		CreatedAt:                   createdAt,
		UpdatedAt:                   updatedAt,
		LastObservedAt:              updatedAt,
		ProviderStartedAt:           updatedAt,
		ExpiresAt:                   now.Add(firstPositiveStoreDuration(cfg.TTL, hostedgenesis.MicroVMRegistryReconstructionTTL)),
		Generation:                  firstPositiveStoreInt64(input.Ref.RegistryVersion, session.Version, 1),
		LastAction:                  input.Ref.LastAction,
		LastCommandID:               firstNonEmptyStoreString(request.RequestID, session.RequestID, "reconstruct-"+strings.TrimSpace(request.SessionID)),
		AuthSubject:                 firstNonEmptyStoreString(request.AuthSubject, hostedgenesis.MicroVMAuthSubject),
		Metadata:                    input.Binding.Metadata(),
	}
	if runtimemicrovm.IsTerminalState(state) {
		record.ProviderTerminatedAt = updatedAt
	}
	return record
}

func firstNonEmptyLifecycleState(values ...runtimemicrovm.LifecycleState) runtimemicrovm.LifecycleState {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositiveStoreDuration(values ...time.Duration) time.Duration {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func slugFromMicroVMTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if !strings.HasPrefix(tenantID, "slug:") {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(tenantID, "slug:")))
}

func firstNonEmptyStoreString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonZeroStoreTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func firstPositiveStoreInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func normalizeStoreStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
