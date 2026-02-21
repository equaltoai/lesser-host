package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentRegistrationStatus* constants describe a soul registration lifecycle.
const (
	SoulAgentRegistrationStatusPending   = "pending"
	SoulAgentRegistrationStatusCompleted = "completed"
)

// SoulAgentRegistration represents a soul agent registration request with proof challenges.
type SoulAgentRegistration struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	ID       string `theorydb:"attr:id" json:"id"`
	Username string `theorydb:"attr:username" json:"username,omitempty"`

	DomainRaw        string `theorydb:"attr:domainRaw" json:"domain_raw,omitempty"`
	DomainNormalized string `theorydb:"attr:domainNormalized" json:"domain_normalized"`

	LocalIDRaw string `theorydb:"attr:localIdRaw" json:"local_id_raw,omitempty"`
	LocalID    string `theorydb:"attr:localId" json:"local_id"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"` // hex-encoded uint256
	Wallet  string `theorydb:"attr:wallet" json:"wallet_address"`

	Capabilities []string `theorydb:"attr:capabilities" json:"capabilities,omitempty"`

	WalletNonce   string `theorydb:"attr:walletNonce" json:"wallet_nonce,omitempty"`
	WalletMessage string `theorydb:"attr:walletMessage" json:"wallet_message,omitempty"`

	ProofToken string `theorydb:"attr:proofToken" json:"proof_token,omitempty"`

	DNSVerified    bool      `theorydb:"attr:dnsVerified" json:"dns_verified,omitempty"`
	HTTPSVerified  bool      `theorydb:"attr:httpsVerified" json:"https_verified,omitempty"`
	WalletVerified bool      `theorydb:"attr:walletVerified" json:"wallet_verified,omitempty"`
	VerifiedAt     time.Time `theorydb:"attr:verifiedAt" json:"verified_at,omitempty"`

	Status string `theorydb:"attr:status" json:"status"`

	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
	ExpiresAt   time.Time `theorydb:"attr:expiresAt" json:"expires_at,omitempty"`
	CompletedAt time.Time `theorydb:"attr:completedAt" json:"completed_at,omitempty"`
}

// TableName returns the database table name for SoulAgentRegistration.
func (SoulAgentRegistration) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentRegistration.
func (r *SoulAgentRegistration) BeforeCreate() error {
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	if r.ExpiresAt.IsZero() {
		r.ExpiresAt = now.Add(30 * time.Minute)
	}
	r.TTL = r.ExpiresAt.Unix()
	if strings.TrimSpace(r.Status) == "" {
		r.Status = SoulAgentRegistrationStatusPending
	}
	return nil
}

// BeforeUpdate updates timestamps and TTL before updating SoulAgentRegistration.
func (r *SoulAgentRegistration) BeforeUpdate() error {
	r.UpdatedAt = time.Now().UTC()
	if !r.ExpiresAt.IsZero() {
		r.TTL = r.ExpiresAt.Unix()
	}
	return r.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulAgentRegistration.
func (r *SoulAgentRegistration) UpdateKeys() error {
	r.ID = strings.TrimSpace(r.ID)
	r.Username = strings.TrimSpace(r.Username)

	r.DomainRaw = strings.TrimSpace(r.DomainRaw)
	r.DomainNormalized = strings.ToLower(strings.TrimSpace(r.DomainNormalized))

	r.LocalIDRaw = strings.TrimSpace(r.LocalIDRaw)
	r.LocalID = normalizeSoulLocalID(r.LocalID)

	r.AgentID = strings.ToLower(strings.TrimSpace(r.AgentID))
	r.Wallet = strings.ToLower(strings.TrimSpace(r.Wallet))

	r.WalletNonce = strings.TrimSpace(r.WalletNonce)
	r.WalletMessage = strings.TrimSpace(r.WalletMessage)

	r.ProofToken = strings.TrimSpace(r.ProofToken)

	r.Status = strings.ToLower(strings.TrimSpace(r.Status))

	r.PK = fmt.Sprintf("SOUL_REG#%s", r.ID)
	r.SK = "REG"
	if !r.ExpiresAt.IsZero() {
		r.TTL = r.ExpiresAt.Unix()
	}

	return nil
}

// GetPK returns the partition key for SoulAgentRegistration.
func (r *SoulAgentRegistration) GetPK() string { return r.PK }

// GetSK returns the sort key for SoulAgentRegistration.
func (r *SoulAgentRegistration) GetSK() string { return r.SK }
