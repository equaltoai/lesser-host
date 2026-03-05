package models

import (
	"fmt"
	"strings"
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
func (i *SoulEmailAgentIndex) BeforeCreate() error { return i.UpdateKeys() }

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
func (i *SoulPhoneAgentIndex) BeforeCreate() error { return i.UpdateKeys() }

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
