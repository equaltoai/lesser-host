package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulEmailLegacyAliasStatus* constants describe host-internal legacy email
// aliases used only for migrated bare Lesser Soul addresses.
const (
	SoulEmailLegacyAliasStatusActive   = "active"
	SoulEmailLegacyAliasStatusDisabled = "disabled"
)

// SoulEmailAgentIndex is a materialized index for looking up an agent by email address.
//
// Keys:
//
//	PK: SOUL#EMAIL#{normalizedEmail}
//	SK: AGENT
type SoulEmailAgentIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Email   string `theorydb:"attr:email" json:"email"`
	AgentID string `theorydb:"attr:agentId" json:"agent_id"`
}

// TableName returns the database table name for SoulEmailAgentIndex.
func (SoulEmailAgentIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulEmailAgentIndex.
func (i *SoulEmailAgentIndex) BeforeCreate() error {
	if err := i.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("email", i.Email); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", i.AgentID); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulEmailAgentIndex.
func (i *SoulEmailAgentIndex) UpdateKeys() error {
	i.Email = normalizeSoulEmail(i.Email)
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))

	i.PK = fmt.Sprintf("SOUL#EMAIL#%s", i.Email)
	i.SK = "AGENT"
	return nil
}

// GetPK returns the partition key for SoulEmailAgentIndex.
func (i *SoulEmailAgentIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulEmailAgentIndex.
func (i *SoulEmailAgentIndex) GetSK() string { return i.SK }

// SoulEmailLegacyAliasIndex is a host-internal deprecated alias lookup used
// only for migrated managed Lesser Soul email channels. It is deliberately
// separate from SoulEmailAgentIndex so public/current channel lookup remains
// the canonical instance-scoped address only.
//
// Keys:
//
//	PK: SOUL#EMAIL_ALIAS#{normalizedLegacyAliasEmail}
//	SK: CANONICAL
type SoulEmailLegacyAliasIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AliasEmail     string    `theorydb:"attr:aliasEmail" json:"alias_email"`
	CanonicalEmail string    `theorydb:"attr:canonicalEmail" json:"canonical_email"`
	AgentID        string    `theorydb:"attr:agentId" json:"agent_id"`
	Status         string    `theorydb:"attr:status" json:"status"`
	Source         string    `theorydb:"attr:source" json:"source,omitempty"`
	CreatedAt      time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt      time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

// TableName returns the database table name for SoulEmailLegacyAliasIndex.
func (SoulEmailLegacyAliasIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulEmailLegacyAliasIndex.
func (i *SoulEmailLegacyAliasIndex) BeforeCreate() error {
	now := time.Now().UTC()
	if i.CreatedAt.IsZero() {
		i.CreatedAt = now
	}
	i.UpdatedAt = now
	if strings.TrimSpace(i.Status) == "" {
		i.Status = SoulEmailLegacyAliasStatusActive
	}
	if err := i.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("aliasEmail", i.AliasEmail); err != nil {
		return err
	}
	if err := requireNonEmpty("canonicalEmail", i.CanonicalEmail); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", i.AgentID); err != nil {
		return err
	}
	if err := requireOneOf("status", i.Status, SoulEmailLegacyAliasStatusActive, SoulEmailLegacyAliasStatusDisabled); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps before updating SoulEmailLegacyAliasIndex.
func (i *SoulEmailLegacyAliasIndex) BeforeUpdate() error {
	i.UpdatedAt = time.Now().UTC()
	if strings.TrimSpace(i.Status) == "" {
		i.Status = SoulEmailLegacyAliasStatusActive
	}
	if err := i.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("aliasEmail", i.AliasEmail); err != nil {
		return err
	}
	if err := requireNonEmpty("canonicalEmail", i.CanonicalEmail); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", i.AgentID); err != nil {
		return err
	}
	if err := requireOneOf("status", i.Status, SoulEmailLegacyAliasStatusActive, SoulEmailLegacyAliasStatusDisabled); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulEmailLegacyAliasIndex.
func (i *SoulEmailLegacyAliasIndex) UpdateKeys() error {
	i.AliasEmail = normalizeSoulEmail(i.AliasEmail)
	i.CanonicalEmail = normalizeSoulEmail(i.CanonicalEmail)
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))
	i.Status = strings.ToLower(strings.TrimSpace(i.Status))
	i.Source = strings.TrimSpace(i.Source)

	i.PK = fmt.Sprintf("SOUL#EMAIL_ALIAS#%s", i.AliasEmail)
	i.SK = "CANONICAL"
	return nil
}

// GetPK returns the partition key for SoulEmailLegacyAliasIndex.
func (i *SoulEmailLegacyAliasIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulEmailLegacyAliasIndex.
func (i *SoulEmailLegacyAliasIndex) GetSK() string { return i.SK }

// SoulPhoneAgentIndex is a materialized index for looking up an agent by phone number (E.164).
//
// Keys:
//
//	PK: SOUL#PHONE#{e164}
//	SK: AGENT
type SoulPhoneAgentIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Phone   string `theorydb:"attr:phone" json:"phone"`
	AgentID string `theorydb:"attr:agentId" json:"agent_id"`
}

// TableName returns the database table name for SoulPhoneAgentIndex.
func (SoulPhoneAgentIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulPhoneAgentIndex.
func (i *SoulPhoneAgentIndex) BeforeCreate() error {
	if err := i.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("phone", i.Phone); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", i.AgentID); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulPhoneAgentIndex.
func (i *SoulPhoneAgentIndex) UpdateKeys() error {
	i.Phone = normalizeSoulPhoneE164(i.Phone)
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))

	i.PK = fmt.Sprintf("SOUL#PHONE#%s", i.Phone)
	i.SK = "AGENT"
	return nil
}

// GetPK returns the partition key for SoulPhoneAgentIndex.
func (i *SoulPhoneAgentIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulPhoneAgentIndex.
func (i *SoulPhoneAgentIndex) GetSK() string { return i.SK }

// SoulChannelAgentIndex is a materialized index for searching agents by channel type.
//
// Keys:
//
//	PK: SOUL#CHANNEL#{channelType}
//	SK: DOMAIN#{normalizedDomain}#LOCAL#{normalizedLocalAgentId}#AGENT#{agentId}
type SoulChannelAgentIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	ChannelType string `theorydb:"attr:channelType" json:"channel_type"` // email|phone
	Domain      string `theorydb:"attr:domain" json:"domain"`
	LocalID     string `theorydb:"attr:localId" json:"local_id"`
	AgentID     string `theorydb:"attr:agentId" json:"agent_id"`
}

// TableName returns the database table name for SoulChannelAgentIndex.
func (SoulChannelAgentIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulChannelAgentIndex.
func (i *SoulChannelAgentIndex) BeforeCreate() error { return i.UpdateKeys() }

// UpdateKeys updates the database keys for SoulChannelAgentIndex.
func (i *SoulChannelAgentIndex) UpdateKeys() error {
	i.ChannelType = strings.ToLower(strings.TrimSpace(i.ChannelType))
	i.Domain = strings.ToLower(strings.TrimSpace(i.Domain))
	i.LocalID = normalizeSoulLocalID(i.LocalID)
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))

	i.PK = fmt.Sprintf("SOUL#CHANNEL#%s", i.ChannelType)
	i.SK = fmt.Sprintf("DOMAIN#%s#LOCAL#%s#AGENT#%s", i.Domain, i.LocalID, i.AgentID)
	return nil
}

// GetPK returns the partition key for SoulChannelAgentIndex.
func (i *SoulChannelAgentIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulChannelAgentIndex.
func (i *SoulChannelAgentIndex) GetSK() string { return i.SK }
