package models

import (
	"fmt"
	"strings"
)

// SoulWalletAgentIndex is a materialized index for looking up agents by wallet address.
//
// Keys:
//
//	PK: SOUL#WALLET#{wallet}
//	SK: AGENT#{agentId}
type SoulWalletAgentIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Wallet  string `theorydb:"attr:wallet" json:"wallet"`
	AgentID string `theorydb:"attr:agentId" json:"agent_id"`
}

// TableName returns the database table name for SoulWalletAgentIndex.
func (SoulWalletAgentIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulWalletAgentIndex.
func (i *SoulWalletAgentIndex) BeforeCreate() error { return i.UpdateKeys() }

// UpdateKeys updates the database keys for SoulWalletAgentIndex.
func (i *SoulWalletAgentIndex) UpdateKeys() error {
	i.Wallet = strings.ToLower(strings.TrimSpace(i.Wallet))
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))

	i.PK = fmt.Sprintf("SOUL#WALLET#%s", i.Wallet)
	i.SK = fmt.Sprintf("AGENT#%s", i.AgentID)
	return nil
}

// GetPK returns the partition key for SoulWalletAgentIndex.
func (i *SoulWalletAgentIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulWalletAgentIndex.
func (i *SoulWalletAgentIndex) GetSK() string { return i.SK }

// SoulDomainAgentIndex is a materialized index for looking up agents by domain + local ID.
//
// Keys:
//
//	PK: SOUL#DOMAIN#{normalizedDomain}
//	SK: LOCAL#{normalizedLocalAgentId}#AGENT#{agentId}
type SoulDomainAgentIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Domain  string `theorydb:"attr:domain" json:"domain"`
	LocalID string `theorydb:"attr:localId" json:"local_id"`
	AgentID string `theorydb:"attr:agentId" json:"agent_id"`
}

// TableName returns the database table name for SoulDomainAgentIndex.
func (SoulDomainAgentIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulDomainAgentIndex.
func (i *SoulDomainAgentIndex) BeforeCreate() error { return i.UpdateKeys() }

// UpdateKeys updates the database keys for SoulDomainAgentIndex.
func (i *SoulDomainAgentIndex) UpdateKeys() error {
	i.Domain = strings.ToLower(strings.TrimSpace(i.Domain))
	i.LocalID = normalizeSoulLocalID(i.LocalID)
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))

	i.PK = fmt.Sprintf("SOUL#DOMAIN#%s", i.Domain)
	i.SK = fmt.Sprintf("LOCAL#%s#AGENT#%s", i.LocalID, i.AgentID)
	return nil
}

// GetPK returns the partition key for SoulDomainAgentIndex.
func (i *SoulDomainAgentIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulDomainAgentIndex.
func (i *SoulDomainAgentIndex) GetSK() string { return i.SK }

// SoulCapabilityAgentIndex is a materialized index for looking up agents by capability.
//
// Keys:
//
//	PK: SOUL#CAP#{capability}
//	SK: DOMAIN#{normalizedDomain}#LOCAL#{normalizedLocalAgentId}#AGENT#{agentId}
type SoulCapabilityAgentIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	Capability string `theorydb:"attr:capability" json:"capability"`
	Domain     string `theorydb:"attr:domain" json:"domain"`
	LocalID    string `theorydb:"attr:localId" json:"local_id"`
	AgentID    string `theorydb:"attr:agentId" json:"agent_id"`
}

// TableName returns the database table name for SoulCapabilityAgentIndex.
func (SoulCapabilityAgentIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulCapabilityAgentIndex.
func (i *SoulCapabilityAgentIndex) BeforeCreate() error { return i.UpdateKeys() }

// UpdateKeys updates the database keys for SoulCapabilityAgentIndex.
func (i *SoulCapabilityAgentIndex) UpdateKeys() error {
	i.Capability = strings.ToLower(strings.TrimSpace(i.Capability))
	i.Domain = strings.ToLower(strings.TrimSpace(i.Domain))
	i.LocalID = normalizeSoulLocalID(i.LocalID)
	i.AgentID = strings.ToLower(strings.TrimSpace(i.AgentID))

	i.PK = fmt.Sprintf("SOUL#CAP#%s", i.Capability)
	i.SK = fmt.Sprintf("DOMAIN#%s#LOCAL#%s#AGENT#%s", i.Domain, i.LocalID, i.AgentID)
	return nil
}

// GetPK returns the partition key for SoulCapabilityAgentIndex.
func (i *SoulCapabilityAgentIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulCapabilityAgentIndex.
func (i *SoulCapabilityAgentIndex) GetSK() string { return i.SK }
