package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentENSResolution stores ENS gateway material for a single name.
//
// Keys:
//
//	PK: ENS#NAME#{ensName}
//	SK: RESOLUTION
type SoulAgentENSResolution struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ENSName string `theorydb:"attr:ensName" json:"ens_name"`
	AgentID string `theorydb:"attr:agentId" json:"agent_id"`
	Wallet  string `theorydb:"attr:wallet" json:"wallet,omitempty"`
	LocalID string `theorydb:"attr:localId" json:"local_id,omitempty"`
	Domain  string `theorydb:"attr:domain" json:"domain,omitempty"`

	SoulRegistrationURI string `theorydb:"attr:soulRegistrationUri" json:"soul_registration_uri,omitempty"`
	MCPEndpoint         string `theorydb:"attr:mcpEndpoint" json:"mcp_endpoint,omitempty"`
	ActivityPubURI      string `theorydb:"attr:activitypubUri" json:"activitypub_uri,omitempty"`

	Email string `theorydb:"attr:email" json:"email,omitempty"`
	Phone string `theorydb:"attr:phone" json:"phone,omitempty"`

	Description string `theorydb:"attr:description" json:"description,omitempty"`
	Status      string `theorydb:"attr:status" json:"status,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for SoulAgentENSResolution.
func (SoulAgentENSResolution) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentENSResolution.
func (r *SoulAgentENSResolution) BeforeCreate() error {
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = now
	}
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("ensName", r.ENSName); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", r.AgentID); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulAgentENSResolution.
func (r *SoulAgentENSResolution) BeforeUpdate() error {
	r.UpdatedAt = time.Now().UTC()
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("ensName", r.ENSName); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", r.AgentID); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentENSResolution.
func (r *SoulAgentENSResolution) UpdateKeys() error {
	r.ENSName = normalizeSoulENSName(r.ENSName)
	r.AgentID = strings.ToLower(strings.TrimSpace(r.AgentID))
	r.Wallet = strings.ToLower(strings.TrimSpace(r.Wallet))
	r.LocalID = normalizeSoulLocalID(r.LocalID)
	r.Domain = strings.ToLower(strings.TrimSpace(r.Domain))
	r.SoulRegistrationURI = strings.TrimSpace(r.SoulRegistrationURI)
	r.MCPEndpoint = strings.TrimSpace(r.MCPEndpoint)
	r.ActivityPubURI = strings.TrimSpace(r.ActivityPubURI)
	r.Email = normalizeSoulEmail(r.Email)
	r.Phone = normalizeSoulPhoneE164(r.Phone)
	r.Description = strings.TrimSpace(r.Description)
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))

	r.PK = fmt.Sprintf("ENS#NAME#%s", r.ENSName)
	r.SK = "RESOLUTION"
	return nil
}

// GetPK returns the partition key for SoulAgentENSResolution.
func (r *SoulAgentENSResolution) GetPK() string { return r.PK }

// GetSK returns the sort key for SoulAgentENSResolution.
func (r *SoulAgentENSResolution) GetSK() string { return r.SK }
