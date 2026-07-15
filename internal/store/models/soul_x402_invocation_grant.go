package models

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	SoulX402InvocationGrantStatusIssued  = "issued"
	SoulX402InvocationGrantStatusRevoked = "revoked"

	SoulX402InvocationGrantPaymentSchemeX402              = "x402"
	SoulX402InvocationGrantFacilitatorTrustCallerProvided = "caller_provided_unverified"
	SoulX402InvocationGrantAuthorityScopedInvocation      = "scoped_invocation"
	SoulX402InvocationGrantCapabilityVocabularyScopedV1   = "scoped-invocation/v1"
	SoulX402InvocationGrantCapabilityVocabularyInstanceV1 = "instance-capability/v1"
	SoulX402InvocationGrantCapabilityInstanceAgentCreate  = "instance:agent_create"
	SoulX402InvocationGrantCapabilityInstanceInstallPlan  = "instance:install_plan"
	SoulX402InvocationGrantScopeRead                      = "read"
	SoulX402InvocationGrantScopeWrite                     = "write"
	SoulX402InvocationGrantScopeAdmin                     = "admin"

	soulX402InvocationGrantTTLRetention = 90 * 24 * time.Hour
)

// SoulX402InvocationGrant stores a minimized, off-chain x402 invocation grant.
//
// The grant is not principal/operator authority. It only authorizes a bounded
// public paid invocation when an authenticated managed instance later presents
// the one-time raw grant token whose hash is stored here.
type SoulX402InvocationGrant struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GrantID string `theorydb:"attr:grantId" json:"grant_id"`
	AgentID string `theorydb:"attr:agentId" json:"agent_id"`

	CapabilityVersion string `theorydb:"attr:capabilityVersion" json:"capability_version"`
	Capability        string `theorydb:"attr:capability" json:"capability"`
	Tool              string `theorydb:"attr:tool" json:"tool"`
	Resource          string `theorydb:"attr:resource" json:"resource"`
	Scope             string `theorydb:"attr:scope" json:"scope"`
	RequestHash       string `theorydb:"attr:requestHash" json:"request_hash"`

	CallerSubjectHash string `theorydb:"attr:callerSubjectHash" json:"caller_subject_hash"`

	PaymentScheme                   string `theorydb:"attr:paymentScheme" json:"payment_scheme"`
	PaymentNetwork                  string `theorydb:"attr:paymentNetwork" json:"payment_network"`
	PaymentAsset                    string `theorydb:"attr:paymentAsset" json:"payment_asset,omitempty"`
	PaymentAmount                   string `theorydb:"attr:paymentAmount" json:"payment_amount"`
	PaymentCurrency                 string `theorydb:"attr:paymentCurrency" json:"payment_currency,omitempty"`
	PaymentFacilitator              string `theorydb:"attr:paymentFacilitator" json:"payment_facilitator,omitempty"`
	PaymentFacilitatorTrustBoundary string `theorydb:"attr:paymentFacilitatorTrustBoundary" json:"payment_facilitator_trust_boundary"`
	PaymentEvidenceHash             string `theorydb:"attr:paymentEvidenceHash" json:"payment_evidence_hash"`
	PaymentIDHash                   string `theorydb:"attr:paymentIdHash" json:"payment_id_hash,omitempty"`

	Nonce                string `theorydb:"attr:nonce" json:"nonce"`
	IdempotencyKeyHash   string `theorydb:"attr:idempotencyKeyHash" json:"idempotency_key_hash"`
	IssueRequestHash     string `theorydb:"attr:issueRequestHash" json:"issue_request_hash"`
	GrantTokenHash       string `theorydb:"attr:grantTokenHash" json:"grant_token_hash"`
	PolicyVersion        string `theorydb:"attr:policyVersion" json:"policy_version"`
	Authority            string `theorydb:"attr:authority" json:"authority"`
	Status               string `theorydb:"attr:status" json:"status"`
	MaxUsage             int    `theorydb:"attr:maxUsage" json:"max_usage"`
	ConsumedUsageCount   int    `theorydb:"attr:consumedUsageCount" json:"consumed_usage_count,omitempty"`
	LatestConsumeSlot    int    `theorydb:"attr:latestConsumeSlot" json:"latest_consume_slot,omitempty"`
	LatestConsumeRequest string `theorydb:"attr:latestConsumeRequestHash" json:"latest_consume_request_hash,omitempty"`

	IssuedAt       time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	ExpiresAt      time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
	UpdatedAt      time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
	LastConsumedAt time.Time `theorydb:"attr:lastConsumedAt" json:"last_consumed_at,omitempty"`
}

// TableName returns the database table name for SoulX402InvocationGrant.
func (SoulX402InvocationGrant) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulX402InvocationGrant.
func (g *SoulX402InvocationGrant) BeforeCreate() error {
	now := time.Now().UTC()
	if g.IssuedAt.IsZero() {
		g.IssuedAt = now
	}
	if g.UpdatedAt.IsZero() {
		g.UpdatedAt = g.IssuedAt
	}
	if strings.TrimSpace(g.PaymentScheme) == "" {
		g.PaymentScheme = SoulX402InvocationGrantPaymentSchemeX402
	}
	if strings.TrimSpace(g.PaymentFacilitatorTrustBoundary) == "" {
		g.PaymentFacilitatorTrustBoundary = SoulX402InvocationGrantFacilitatorTrustCallerProvided
	}
	if strings.TrimSpace(g.PolicyVersion) == "" {
		g.PolicyVersion = SoulCallerAccessPaymentPolicyVersionV1
	}
	if strings.TrimSpace(g.Authority) == "" {
		g.Authority = SoulX402InvocationGrantAuthorityScopedInvocation
	}
	if strings.TrimSpace(g.Status) == "" {
		g.Status = SoulX402InvocationGrantStatusIssued
	}
	if g.MaxUsage <= 0 {
		g.MaxUsage = 1
	}
	if err := g.UpdateKeys(); err != nil {
		return err
	}
	return g.validate()
}

// BeforeUpdate updates timestamps and keys before updating SoulX402InvocationGrant.
func (g *SoulX402InvocationGrant) BeforeUpdate() error {
	g.UpdatedAt = time.Now().UTC()
	if err := g.UpdateKeys(); err != nil {
		return err
	}
	return g.validate()
}

func (g *SoulX402InvocationGrant) validate() error {
	if err := g.validateRequiredFields(); err != nil {
		return err
	}
	if err := requireOneOf("authority", g.Authority, SoulX402InvocationGrantAuthorityScopedInvocation); err != nil {
		return err
	}
	if err := requireOneOf("capabilityVersion", g.CapabilityVersion, SoulX402InvocationGrantCapabilityVocabularyScopedV1, SoulX402InvocationGrantCapabilityVocabularyInstanceV1); err != nil {
		return err
	}
	if err := requireOneOf("scope", g.Scope, SoulX402InvocationGrantScopeRead, SoulX402InvocationGrantScopeWrite, SoulX402InvocationGrantScopeAdmin); err != nil {
		return err
	}
	if err := requireOneOf("status", g.Status, SoulX402InvocationGrantStatusIssued, SoulX402InvocationGrantStatusRevoked); err != nil {
		return err
	}
	if g.MaxUsage < 1 || g.MaxUsage > 100 {
		return fmt.Errorf("maxUsage must be between 1 and 100")
	}
	if g.IssuedAt.IsZero() {
		return fmt.Errorf("issuedAt is required")
	}
	if g.ExpiresAt.IsZero() {
		return fmt.Errorf("expiresAt is required")
	}
	if !g.ExpiresAt.After(g.IssuedAt) {
		return fmt.Errorf("expiresAt must be after issuedAt")
	}
	return nil
}

func (g *SoulX402InvocationGrant) validateRequiredFields() error {
	required := []struct {
		name  string
		value string
	}{
		{"grantId", g.GrantID},
		{"agentId", g.AgentID},
		{"capabilityVersion", g.CapabilityVersion},
		{"capability", g.Capability},
		{"tool", g.Tool},
		{"resource", g.Resource},
		{"scope", g.Scope},
		{"requestHash", g.RequestHash},
		{"callerSubjectHash", g.CallerSubjectHash},
		{"paymentScheme", g.PaymentScheme},
		{"paymentNetwork", g.PaymentNetwork},
		{"paymentAmount", g.PaymentAmount},
		{"paymentFacilitatorTrustBoundary", g.PaymentFacilitatorTrustBoundary},
		{"paymentEvidenceHash", g.PaymentEvidenceHash},
		{"nonce", g.Nonce},
		{"idempotencyKeyHash", g.IdempotencyKeyHash},
		{"issueRequestHash", g.IssueRequestHash},
		{"grantTokenHash", g.GrantTokenHash},
		{"policyVersion", g.PolicyVersion},
	}
	for _, field := range required {
		if err := requireNonEmpty(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

// UpdateKeys updates the database keys for SoulX402InvocationGrant.
func (g *SoulX402InvocationGrant) UpdateKeys() error {
	g.GrantID = strings.TrimSpace(g.GrantID)
	g.AgentID = strings.ToLower(strings.TrimSpace(g.AgentID))
	g.CapabilityVersion = strings.ToLower(strings.TrimSpace(g.CapabilityVersion))
	g.Capability = strings.ToLower(strings.TrimSpace(g.Capability))
	g.Tool = strings.ToLower(strings.TrimSpace(g.Tool))
	g.Resource = strings.TrimSpace(g.Resource)
	g.Scope = strings.ToLower(strings.TrimSpace(g.Scope))
	g.RequestHash = strings.ToLower(strings.TrimSpace(g.RequestHash))
	g.CallerSubjectHash = strings.ToLower(strings.TrimSpace(g.CallerSubjectHash))
	g.PaymentScheme = strings.ToLower(strings.TrimSpace(g.PaymentScheme))
	g.PaymentNetwork = strings.ToLower(strings.TrimSpace(g.PaymentNetwork))
	g.PaymentAsset = strings.ToLower(strings.TrimSpace(g.PaymentAsset))
	g.PaymentAmount = strings.TrimSpace(g.PaymentAmount)
	g.PaymentCurrency = strings.ToUpper(strings.TrimSpace(g.PaymentCurrency))
	g.PaymentFacilitator = strings.TrimSpace(g.PaymentFacilitator)
	g.PaymentFacilitatorTrustBoundary = strings.ToLower(strings.TrimSpace(g.PaymentFacilitatorTrustBoundary))
	g.PaymentEvidenceHash = strings.ToLower(strings.TrimSpace(g.PaymentEvidenceHash))
	g.PaymentIDHash = strings.ToLower(strings.TrimSpace(g.PaymentIDHash))
	g.Nonce = strings.TrimSpace(g.Nonce)
	g.IdempotencyKeyHash = strings.ToLower(strings.TrimSpace(g.IdempotencyKeyHash))
	g.IssueRequestHash = strings.ToLower(strings.TrimSpace(g.IssueRequestHash))
	g.GrantTokenHash = strings.ToLower(strings.TrimSpace(g.GrantTokenHash))
	g.PolicyVersion = strings.ToLower(strings.TrimSpace(g.PolicyVersion))
	g.Authority = strings.ToLower(strings.TrimSpace(g.Authority))
	g.Status = strings.ToLower(strings.TrimSpace(g.Status))

	g.PK = SoulX402InvocationGrantPK(g.GrantID)
	g.SK = stateSortKey
	if !g.ExpiresAt.IsZero() {
		g.TTL = g.ExpiresAt.UTC().Add(soulX402InvocationGrantTTLRetention).Unix()
	}
	return nil
}

// GetPK returns the partition key for SoulX402InvocationGrant.
func (g *SoulX402InvocationGrant) GetPK() string { return g.PK }

// GetSK returns the sort key for SoulX402InvocationGrant.
func (g *SoulX402InvocationGrant) GetSK() string { return g.SK }

// SoulX402InvocationGrantPK returns the grant partition key.
func SoulX402InvocationGrantPK(grantID string) string {
	return "SOUL#X402#GRANT#" + strings.TrimSpace(grantID)
}

// SoulX402InvocationGrantID returns the deterministic grant id for an issue idempotency scope.
func SoulX402InvocationGrantID(agentID string, idempotencyKey string) string {
	scope := fmt.Sprintf("%s#%s", strings.ToLower(strings.TrimSpace(agentID)), strings.TrimSpace(idempotencyKey))
	sum := sha256.Sum256([]byte(scope))
	return "x402-grant-" + hex.EncodeToString(sum[:])
}

// SoulX402InvocationGrantUsage records one consumed usage slot for an invocation grant.
//
// Usage slots are write-once. Creating at most MaxUsage slot rows with IfNotExists
// bounds replay and double-spend without storing raw caller payment evidence.
type SoulX402InvocationGrantUsage struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GrantID                   string    `theorydb:"attr:grantId" json:"grant_id"`
	UsageSlot                 int       `theorydb:"attr:usageSlot" json:"usage_slot"`
	AgentID                   string    `theorydb:"attr:agentId" json:"agent_id"`
	InstanceSlug              string    `theorydb:"attr:instanceSlug" json:"instance_slug"`
	ConsumeIdempotencyKeyHash string    `theorydb:"attr:consumeIdempotencyKeyHash" json:"consume_idempotency_key_hash"`
	ConsumeRequestHash        string    `theorydb:"attr:consumeRequestHash" json:"consume_request_hash"`
	ConsumedAt                time.Time `theorydb:"attr:consumedAt" json:"consumed_at"`
}

// TableName returns the database table name for SoulX402InvocationGrantUsage.
func (SoulX402InvocationGrantUsage) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulX402InvocationGrantUsage.
func (u *SoulX402InvocationGrantUsage) BeforeCreate() error {
	if u.ConsumedAt.IsZero() {
		u.ConsumedAt = time.Now().UTC()
	}
	if err := u.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("grantId", u.GrantID); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", u.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", u.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("consumeIdempotencyKeyHash", u.ConsumeIdempotencyKeyHash); err != nil {
		return err
	}
	if err := requireNonEmpty("consumeRequestHash", u.ConsumeRequestHash); err != nil {
		return err
	}
	if u.UsageSlot < 0 || u.UsageSlot > 99 {
		return fmt.Errorf("usageSlot must be between 0 and 99")
	}
	return nil
}

// UpdateKeys updates the database keys for SoulX402InvocationGrantUsage.
func (u *SoulX402InvocationGrantUsage) UpdateKeys() error {
	u.GrantID = strings.TrimSpace(u.GrantID)
	u.AgentID = strings.ToLower(strings.TrimSpace(u.AgentID))
	u.InstanceSlug = strings.ToLower(strings.TrimSpace(u.InstanceSlug))
	u.ConsumeIdempotencyKeyHash = strings.ToLower(strings.TrimSpace(u.ConsumeIdempotencyKeyHash))
	u.ConsumeRequestHash = strings.ToLower(strings.TrimSpace(u.ConsumeRequestHash))

	u.PK = SoulX402InvocationGrantPK(u.GrantID)
	u.SK = SoulX402InvocationGrantUsageSK(u.UsageSlot)
	if !u.ConsumedAt.IsZero() {
		u.TTL = u.ConsumedAt.UTC().Add(soulX402InvocationGrantTTLRetention).Unix()
	}
	return nil
}

// GetPK returns the partition key for SoulX402InvocationGrantUsage.
func (u *SoulX402InvocationGrantUsage) GetPK() string { return u.PK }

// GetSK returns the sort key for SoulX402InvocationGrantUsage.
func (u *SoulX402InvocationGrantUsage) GetSK() string { return u.SK }

// SoulX402InvocationGrantUsageSK returns the usage slot sort key.
func SoulX402InvocationGrantUsageSK(slot int) string {
	return fmt.Sprintf("USAGE#%06d", slot)
}
