package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentStatus* constants define lifecycle states for a soul agent identity.
const (
	SoulAgentStatusPending       = "pending"
	SoulAgentStatusActive        = "active"
	SoulAgentStatusSuspended     = "suspended"
	SoulAgentStatusSelfSuspended = "self_suspended"
	SoulAgentStatusArchived      = "archived"
	SoulAgentStatusSucceeded     = "succeeded"
	SoulAgentStatusBurned        = "burned"
)

// SoulAgentIdentity stores the off-chain identity record for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: IDENTITY
type SoulAgentIdentity struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"` // hex-encoded uint256

	Domain  string `theorydb:"attr:domain" json:"domain"`
	LocalID string `theorydb:"attr:localId" json:"local_id"`

	Wallet  string `theorydb:"attr:wallet" json:"wallet"`
	TokenID string `theorydb:"attr:tokenId" json:"token_id,omitempty"`
	MetaURI string `theorydb:"attr:metaURI" json:"meta_uri,omitempty"`

	Capabilities []string `theorydb:"attr:capabilities" json:"capabilities,omitempty"`

	// v2: principal declaration
	PrincipalAddress       string `theorydb:"attr:principalAddress" json:"principal_address,omitempty"`
	PrincipalSignature     string `theorydb:"attr:principalSignature" json:"principal_signature,omitempty"`
	PrincipalDeclaration   string `theorydb:"attr:principalDeclaration" json:"principal_declaration,omitempty"`
	PrincipalDeclaredAt    string `theorydb:"attr:principalDeclaredAt" json:"principal_declared_at,omitempty"`
	SelfDescriptionVersion int    `theorydb:"attr:selfDescriptionVersion" json:"self_description_version,omitempty"`

	// v2: lifecycle (replaces simple Status for richer state machine)
	LifecycleStatus    string `theorydb:"attr:lifecycleStatus" json:"lifecycle_status,omitempty"`
	LifecycleReason    string `theorydb:"attr:lifecycleReason" json:"lifecycle_reason,omitempty"`
	SuccessorAgentID   string `theorydb:"attr:successorAgentId" json:"successor_agent_id,omitempty"`
	PredecessorAgentID string `theorydb:"attr:predecessorAgentId" json:"predecessor_agent_id,omitempty"`

	Status     string    `theorydb:"attr:status" json:"status"`
	MintTxHash string    `theorydb:"attr:mintTxHash" json:"mint_tx_hash,omitempty"`
	MintedAt   time.Time `theorydb:"attr:mintedAt" json:"minted_at,omitempty"`
	UpdatedAt  time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulAgentIdentity.
func (SoulAgentIdentity) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentIdentity.
func (a *SoulAgentIdentity) BeforeCreate() error {
	now := time.Now().UTC()
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}
	if strings.TrimSpace(a.Status) == "" {
		a.Status = SoulAgentStatusPending
	}
	if strings.TrimSpace(a.LifecycleStatus) == "" {
		a.LifecycleStatus = a.Status
	}
	if err := a.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", a.AgentID); err != nil {
		return err
	}
	if err := requireOneOf(
		"status",
		a.Status,
		SoulAgentStatusPending,
		SoulAgentStatusActive,
		SoulAgentStatusSuspended,
		SoulAgentStatusSelfSuspended,
		SoulAgentStatusArchived,
		SoulAgentStatusSucceeded,
		SoulAgentStatusBurned,
	); err != nil {
		return err
	}
	if err := requireOneOf(
		"lifecycleStatus",
		a.LifecycleStatus,
		SoulAgentStatusPending,
		SoulAgentStatusActive,
		SoulAgentStatusSuspended,
		SoulAgentStatusSelfSuspended,
		SoulAgentStatusArchived,
		SoulAgentStatusSucceeded,
		SoulAgentStatusBurned,
	); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulAgentIdentity.
func (a *SoulAgentIdentity) BeforeUpdate() error {
	a.UpdatedAt = time.Now().UTC()
	if err := a.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", a.AgentID); err != nil {
		return err
	}
	if err := requireOneOf(
		"status",
		a.Status,
		SoulAgentStatusPending,
		SoulAgentStatusActive,
		SoulAgentStatusSuspended,
		SoulAgentStatusSelfSuspended,
		SoulAgentStatusArchived,
		SoulAgentStatusSucceeded,
		SoulAgentStatusBurned,
	); err != nil {
		return err
	}
	if strings.TrimSpace(a.LifecycleStatus) == "" {
		// Backward-compatible: if lifecycleStatus is absent, treat it as equal to status.
		a.LifecycleStatus = a.Status
		_ = a.UpdateKeys()
	}
	if err := requireOneOf(
		"lifecycleStatus",
		a.LifecycleStatus,
		SoulAgentStatusPending,
		SoulAgentStatusActive,
		SoulAgentStatusSuspended,
		SoulAgentStatusSelfSuspended,
		SoulAgentStatusArchived,
		SoulAgentStatusSucceeded,
		SoulAgentStatusBurned,
	); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentIdentity.
func (a *SoulAgentIdentity) UpdateKeys() error {
	a.AgentID = strings.ToLower(strings.TrimSpace(a.AgentID))
	a.Domain = strings.ToLower(strings.TrimSpace(a.Domain))
	a.LocalID = normalizeSoulLocalID(a.LocalID)
	a.Wallet = strings.ToLower(strings.TrimSpace(a.Wallet))
	a.TokenID = strings.ToLower(strings.TrimSpace(a.TokenID))
	a.MetaURI = strings.TrimSpace(a.MetaURI)
	a.PrincipalAddress = strings.ToLower(strings.TrimSpace(a.PrincipalAddress))
	a.PrincipalSignature = strings.ToLower(strings.TrimSpace(a.PrincipalSignature))
	a.PrincipalDeclaration = strings.TrimSpace(a.PrincipalDeclaration)
	a.PrincipalDeclaredAt = strings.TrimSpace(a.PrincipalDeclaredAt)
	a.LifecycleStatus = strings.ToLower(strings.TrimSpace(a.LifecycleStatus))
	a.LifecycleReason = strings.TrimSpace(a.LifecycleReason)
	a.SuccessorAgentID = strings.ToLower(strings.TrimSpace(a.SuccessorAgentID))
	a.PredecessorAgentID = strings.ToLower(strings.TrimSpace(a.PredecessorAgentID))
	a.Status = strings.ToLower(strings.TrimSpace(a.Status))
	a.MintTxHash = strings.ToLower(strings.TrimSpace(a.MintTxHash))

	a.PK = fmt.Sprintf("SOUL#AGENT#%s", a.AgentID)
	a.SK = "IDENTITY"
	return nil
}

// GetPK returns the partition key for SoulAgentIdentity.
func (a *SoulAgentIdentity) GetPK() string { return a.PK }

// GetSK returns the sort key for SoulAgentIdentity.
func (a *SoulAgentIdentity) GetSK() string { return a.SK }
