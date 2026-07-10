package models

import (
	"fmt"
	"strings"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

// HostedGenesisMicroVMExecution stores Host-owned operational cache for a
// hosted-genesis AppTheory MicroVM execution session. HostedGenesisSession
// remains the durable business/source truth; this item is reconstructible cache
// used only to satisfy the controller registry surface.
//
// Keys are derived internally from the tenant-bound slug, namespace, and
// session id. Controller/domain callers must not pass or depend on PK/SK.
//
//	PK: HOSTED_GENESIS_MICROVM#INSTANCE#{instanceSlug}#NAMESPACE#{namespace}
//	SK: SESSION#{sessionId}
//
// The model intentionally persists only AppTheory's safe SessionRecord fields:
// no raw bearer tokens, provider credentials, raw Instance API keys, wallet
// signatures, SSM values, AWS credentials, or raw transcripts.
type HostedGenesisMicroVMExecution struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Version int64 `theorydb:"version,attr:version" json:"version"`
	TTL     int64 `theorydb:"ttl,attr:ttl" json:"-"`

	InstanceSlug string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	TenantID     string `theorydb:"attr:tenantId" json:"tenant_id"`
	Namespace    string `theorydb:"attr:namespace" json:"namespace"`
	SessionID    string `theorydb:"attr:sessionId" json:"session_id"`

	State        runtimemicrovm.LifecycleState `theorydb:"attr:state" json:"state"`
	DesiredState runtimemicrovm.LifecycleState `theorydb:"attr:desiredState" json:"desired_state"`

	Endpoint                    string   `theorydb:"attr:endpoint,omitempty" json:"endpoint,omitempty"`
	MicroVMID                   string   `theorydb:"attr:microVmId,omitempty" json:"microvm_id,omitempty"`
	ProviderID                  string   `theorydb:"attr:providerId" json:"provider_id"`
	ProviderMicroVMID           string   `theorydb:"attr:providerMicroVmId,omitempty" json:"provider_microvm_id,omitempty"`
	ProviderState               string   `theorydb:"attr:providerState" json:"provider_state"`
	AWSLifecycleState           string   `theorydb:"attr:awsLifecycleState" json:"aws_lifecycle_state"`
	ImageRef                    string   `theorydb:"attr:imageRef" json:"image_ref"`
	ImageVersion                string   `theorydb:"attr:imageVersion,omitempty" json:"image_version,omitempty"`
	NetworkConnectorRef         string   `theorydb:"attr:networkConnectorRef" json:"network_connector_ref"`
	IngressNetworkConnectorRefs []string `theorydb:"attr:ingressNetworkConnectorRefs,omitempty" json:"ingress_network_connector_refs,omitempty"`
	EgressNetworkConnectorRefs  []string `theorydb:"attr:egressNetworkConnectorRefs,omitempty" json:"egress_network_connector_refs,omitempty"`
	ControllerID                string   `theorydb:"attr:controllerId" json:"controller_id"`

	CreatedAt            time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt            time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	LastObservedAt       time.Time `theorydb:"attr:lastObservedAt" json:"last_observed_at"`
	ProviderStartedAt    time.Time `theorydb:"attr:providerStartedAt,omitempty" json:"provider_started_at,omitempty"`
	ProviderTerminatedAt time.Time `theorydb:"attr:providerTerminatedAt,omitempty" json:"provider_terminated_at,omitempty"`
	ExpiresAt            time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	Generation           int64     `theorydb:"attr:generation" json:"generation"`

	LastAction    runtimemicrovm.Command `theorydb:"attr:lastAction" json:"last_action"`
	LastCommandID string                 `theorydb:"attr:lastCommandId" json:"last_command_id"`
	AuthSubject   string                 `theorydb:"attr:authSubject" json:"auth_subject"`

	ReasonMetadata map[string]string `theorydb:"attr:reasonMetadata,omitempty" json:"reason_metadata,omitempty"`
	StatusMetadata map[string]string `theorydb:"attr:statusMetadata,omitempty" json:"status_metadata,omitempty"`
	// TokenMetadata is AppTheory's sanitized token metadata only: token id/type,
	// expiry, and scope. It must never contain token plaintext or bearer values.
	TokenMetadata []runtimemicrovm.SessionTokenMetadata `theorydb:"attr:tokenMetadata,omitempty" json:"token_metadata,omitempty"`
	Metadata      map[string]string                     `theorydb:"attr:metadata,omitempty" json:"metadata,omitempty"`
}

// TableName returns the database table name for HostedGenesisMicroVMExecution.
func (HostedGenesisMicroVMExecution) TableName() string { return MainTableName() }

// BeforeCreate validates and derives keys before creating cache state.
func (e *HostedGenesisMicroVMExecution) BeforeCreate() error { return e.validateAndUpdateKeys() }

// BeforeUpdate validates and derives keys before updating cache state.
func (e *HostedGenesisMicroVMExecution) BeforeUpdate() error { return e.validateAndUpdateKeys() }

// UpdateKeys normalizes semantic fields and derives TableTheory keys.
func (e *HostedGenesisMicroVMExecution) UpdateKeys() error {
	if e == nil {
		return fmt.Errorf("hosted genesis microvm execution is required")
	}
	e.InstanceSlug = normalizeSlug(e.InstanceSlug)
	e.TenantID = strings.TrimSpace(e.TenantID)
	if e.InstanceSlug == "" {
		e.InstanceSlug = HostedGenesisMicroVMExecutionSlugFromTenantID(e.TenantID)
	}
	if e.TenantID == "" && e.InstanceSlug != "" {
		e.TenantID = "slug:" + e.InstanceSlug
	}
	e.Namespace = strings.TrimSpace(e.Namespace)
	e.SessionID = strings.TrimSpace(e.SessionID)
	e.State = runtimemicrovm.LifecycleState(strings.TrimSpace(string(e.State)))
	e.DesiredState = runtimemicrovm.LifecycleState(strings.TrimSpace(string(e.DesiredState)))
	e.Endpoint = strings.TrimSpace(e.Endpoint)
	e.MicroVMID = strings.TrimSpace(e.MicroVMID)
	e.ProviderID = strings.TrimSpace(e.ProviderID)
	e.ProviderMicroVMID = strings.TrimSpace(e.ProviderMicroVMID)
	e.ProviderState = strings.TrimSpace(e.ProviderState)
	e.AWSLifecycleState = strings.TrimSpace(e.AWSLifecycleState)
	e.ImageRef = strings.TrimSpace(e.ImageRef)
	e.ImageVersion = strings.TrimSpace(e.ImageVersion)
	e.NetworkConnectorRef = strings.TrimSpace(e.NetworkConnectorRef)
	e.IngressNetworkConnectorRefs = normalizeModelStringSlice(e.IngressNetworkConnectorRefs)
	e.EgressNetworkConnectorRefs = normalizeModelStringSlice(e.EgressNetworkConnectorRefs)
	e.ControllerID = strings.TrimSpace(e.ControllerID)
	e.LastAction = runtimemicrovm.Command(strings.TrimSpace(string(e.LastAction)))
	e.LastCommandID = strings.TrimSpace(e.LastCommandID)
	e.AuthSubject = strings.TrimSpace(e.AuthSubject)
	e.ReasonMetadata = normalizeModelStringMap(e.ReasonMetadata)
	e.StatusMetadata = normalizeModelStringMap(e.StatusMetadata)
	e.TokenMetadata = normalizeModelSessionTokenMetadata(e.TokenMetadata)
	e.Metadata = normalizeModelStringMap(e.Metadata)
	if !e.CreatedAt.IsZero() {
		e.CreatedAt = e.CreatedAt.UTC()
	}
	if !e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.UpdatedAt.UTC()
	}
	if !e.LastObservedAt.IsZero() {
		e.LastObservedAt = e.LastObservedAt.UTC()
	}
	if !e.ProviderStartedAt.IsZero() {
		e.ProviderStartedAt = e.ProviderStartedAt.UTC()
	}
	if !e.ProviderTerminatedAt.IsZero() {
		e.ProviderTerminatedAt = e.ProviderTerminatedAt.UTC()
	}
	if !e.ExpiresAt.IsZero() {
		e.ExpiresAt = e.ExpiresAt.UTC()
		e.TTL = e.ExpiresAt.Unix()
	}

	e.PK = HostedGenesisMicroVMExecutionPK(e.InstanceSlug, e.Namespace)
	e.SK = HostedGenesisMicroVMExecutionSK(e.SessionID)
	return nil
}

func (e *HostedGenesisMicroVMExecution) validateAndUpdateKeys() error {
	if err := e.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", e.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("tenantId", e.TenantID); err != nil {
		return err
	}
	if err := requireNonEmpty("namespace", e.Namespace); err != nil {
		return err
	}
	if err := requireNonEmpty("sessionId", e.SessionID); err != nil {
		return err
	}
	if e.Namespace != hostedgenesis.MicroVMNamespace {
		return fmt.Errorf("hosted genesis microvm execution namespace is invalid")
	}
	if e.TenantID != "slug:"+e.InstanceSlug {
		return fmt.Errorf("hosted genesis microvm execution tenant does not match instance slug")
	}
	if _, err := e.ToSessionRecord(); err != nil {
		return err
	}
	return nil
}

// ToSessionRecord converts Host operational cache to AppTheory's safe runtime
// registry shape without exposing TableTheory keys.
func (e *HostedGenesisMicroVMExecution) ToSessionRecord() (runtimemicrovm.SessionRecord, error) {
	if e == nil {
		return runtimemicrovm.SessionRecord{}, fmt.Errorf("hosted genesis microvm execution is required")
	}
	record := runtimemicrovm.SessionRecord{
		TenantID:                    strings.TrimSpace(e.TenantID),
		Namespace:                   strings.TrimSpace(e.Namespace),
		SessionID:                   strings.TrimSpace(e.SessionID),
		State:                       runtimemicrovm.LifecycleState(strings.TrimSpace(string(e.State))),
		DesiredState:                runtimemicrovm.LifecycleState(strings.TrimSpace(string(e.DesiredState))),
		Endpoint:                    strings.TrimSpace(e.Endpoint),
		MicroVMID:                   strings.TrimSpace(e.MicroVMID),
		ProviderID:                  strings.TrimSpace(e.ProviderID),
		ProviderMicroVMID:           strings.TrimSpace(e.ProviderMicroVMID),
		ProviderState:               strings.TrimSpace(e.ProviderState),
		AWSLifecycleState:           strings.TrimSpace(e.AWSLifecycleState),
		ImageRef:                    strings.TrimSpace(e.ImageRef),
		ImageVersion:                strings.TrimSpace(e.ImageVersion),
		NetworkConnectorRef:         strings.TrimSpace(e.NetworkConnectorRef),
		IngressNetworkConnectorRefs: normalizeModelStringSlice(e.IngressNetworkConnectorRefs),
		EgressNetworkConnectorRefs:  normalizeModelStringSlice(e.EgressNetworkConnectorRefs),
		ControllerID:                strings.TrimSpace(e.ControllerID),
		CreatedAt:                   e.CreatedAt.UTC(),
		UpdatedAt:                   e.UpdatedAt.UTC(),
		LastObservedAt:              e.LastObservedAt.UTC(),
		ProviderStartedAt:           e.ProviderStartedAt.UTC(),
		ProviderTerminatedAt:        e.ProviderTerminatedAt.UTC(),
		ExpiresAt:                   e.ExpiresAt.UTC(),
		Generation:                  e.Generation,
		LastAction:                  runtimemicrovm.Command(strings.TrimSpace(string(e.LastAction))),
		LastCommandID:               strings.TrimSpace(e.LastCommandID),
		AuthSubject:                 strings.TrimSpace(e.AuthSubject),
		ReasonMetadata:              normalizeModelStringMap(e.ReasonMetadata),
		StatusMetadata:              normalizeModelStringMap(e.StatusMetadata),
		TokenMetadata:               normalizeModelSessionTokenMetadata(e.TokenMetadata),
		Metadata:                    normalizeModelStringMap(e.Metadata),
	}
	if err := runtimemicrovm.ValidateSessionRecord(record); err != nil {
		return runtimemicrovm.SessionRecord{}, err
	}
	return record, nil
}

// NewHostedGenesisMicroVMExecutionFromSessionRecord builds the Host-owned
// operational cache item from AppTheory's safe SessionRecord shape.
func NewHostedGenesisMicroVMExecutionFromSessionRecord(record runtimemicrovm.SessionRecord) (*HostedGenesisMicroVMExecution, error) {
	if err := runtimemicrovm.ValidateSessionRecord(record); err != nil {
		return nil, err
	}
	instanceSlug := HostedGenesisMicroVMExecutionSlugFromTenantID(record.TenantID)
	if instanceSlug == "" || strings.TrimSpace(record.Namespace) != hostedgenesis.MicroVMNamespace {
		return nil, fmt.Errorf("hosted genesis microvm execution record is not tenant-bound")
	}
	item := &HostedGenesisMicroVMExecution{
		InstanceSlug:                instanceSlug,
		TenantID:                    strings.TrimSpace(record.TenantID),
		Namespace:                   strings.TrimSpace(record.Namespace),
		SessionID:                   strings.TrimSpace(record.SessionID),
		State:                       record.State,
		DesiredState:                record.DesiredState,
		Endpoint:                    strings.TrimSpace(record.Endpoint),
		MicroVMID:                   strings.TrimSpace(record.MicroVMID),
		ProviderID:                  strings.TrimSpace(record.ProviderID),
		ProviderMicroVMID:           strings.TrimSpace(record.ProviderMicroVMID),
		ProviderState:               strings.TrimSpace(record.ProviderState),
		AWSLifecycleState:           strings.TrimSpace(record.AWSLifecycleState),
		ImageRef:                    strings.TrimSpace(record.ImageRef),
		ImageVersion:                strings.TrimSpace(record.ImageVersion),
		NetworkConnectorRef:         strings.TrimSpace(record.NetworkConnectorRef),
		IngressNetworkConnectorRefs: normalizeModelStringSlice(record.IngressNetworkConnectorRefs),
		EgressNetworkConnectorRefs:  normalizeModelStringSlice(record.EgressNetworkConnectorRefs),
		ControllerID:                strings.TrimSpace(record.ControllerID),
		CreatedAt:                   record.CreatedAt.UTC(),
		UpdatedAt:                   record.UpdatedAt.UTC(),
		LastObservedAt:              record.LastObservedAt.UTC(),
		ProviderStartedAt:           record.ProviderStartedAt.UTC(),
		ProviderTerminatedAt:        record.ProviderTerminatedAt.UTC(),
		ExpiresAt:                   record.ExpiresAt.UTC(),
		Generation:                  record.Generation,
		LastAction:                  record.LastAction,
		LastCommandID:               strings.TrimSpace(record.LastCommandID),
		AuthSubject:                 strings.TrimSpace(record.AuthSubject),
		ReasonMetadata:              normalizeModelStringMap(record.ReasonMetadata),
		StatusMetadata:              normalizeModelStringMap(record.StatusMetadata),
		TokenMetadata:               normalizeModelSessionTokenMetadata(record.TokenMetadata),
		Metadata:                    normalizeModelStringMap(record.Metadata),
	}
	if err := item.validateAndUpdateKeys(); err != nil {
		return nil, err
	}
	return item, nil
}

// HostedGenesisMicroVMExecutionPK returns the tenant+namespace partition key.
func HostedGenesisMicroVMExecutionPK(instanceSlug string, namespace string) string {
	return fmt.Sprintf("HOSTED_GENESIS_MICROVM#INSTANCE#%s#NAMESPACE#%s", normalizeSlug(instanceSlug), strings.TrimSpace(namespace))
}

// HostedGenesisMicroVMExecutionSK returns the session sort key.
func HostedGenesisMicroVMExecutionSK(sessionID string) string {
	return fmt.Sprintf("SESSION#%s", strings.TrimSpace(sessionID))
}

// HostedGenesisMicroVMExecutionSlugFromTenantID extracts Host's managed
// instance slug from the AppTheory tenant id. Only slug-scoped tenants are
// accepted for hosted-genesis MicroVM execution cache.
func HostedGenesisMicroVMExecutionSlugFromTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if !strings.HasPrefix(tenantID, "slug:") {
		return ""
	}
	return normalizeSlug(strings.TrimPrefix(tenantID, "slug:"))
}

func normalizeModelStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeModelStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		out[trimmedKey] = trimmedValue
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeModelSessionTokenMetadata(values []runtimemicrovm.SessionTokenMetadata) []runtimemicrovm.SessionTokenMetadata {
	out := make([]runtimemicrovm.SessionTokenMetadata, 0, len(values))
	for _, value := range values {
		metadata := runtimemicrovm.SessionTokenMetadata{
			TokenID:   strings.TrimSpace(value.TokenID),
			TokenType: strings.TrimSpace(value.TokenType),
			ExpiresAt: value.ExpiresAt.UTC(),
			Scope:     normalizeModelStringSlice(value.Scope),
		}
		if metadata.TokenID == "" && metadata.TokenType == "" && metadata.ExpiresAt.IsZero() && len(metadata.Scope) == 0 {
			continue
		}
		out = append(out, metadata)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
