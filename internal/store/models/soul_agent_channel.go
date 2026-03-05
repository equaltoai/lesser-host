package models

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SoulChannelType* constants define supported channel types.
const (
	SoulChannelTypeENS   = "ens"
	SoulChannelTypeEmail = "email"
	SoulChannelTypePhone = "phone"
)

// SoulChannelStatus* constants define supported channel lifecycle states.
const (
	SoulChannelStatusActive         = "active"
	SoulChannelStatusPaused         = "paused"
	SoulChannelStatusDecommissioned = "decommissioned"
)

// SoulAgentChannel stores a single communication channel for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: CHANNEL#{channelType}
type SoulAgentChannel struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID      string   `theorydb:"attr:agentId" json:"agent_id"`
	ChannelType  string   `theorydb:"attr:channelType" json:"channel_type"` // ens|email|phone
	Identifier   string   `theorydb:"attr:identifier" json:"identifier"`    // ens name, email address, or phone number
	Capabilities []string `theorydb:"attr:capabilities" json:"capabilities,omitempty"`
	Protocols    []string `theorydb:"attr:protocols" json:"protocols,omitempty"` // email: smtp|imap

	ENSResolverAddress string `theorydb:"attr:ensResolverAddress" json:"ens_resolver_address,omitempty"`
	ENSChain           string `theorydb:"attr:ensChain" json:"ens_chain,omitempty"`

	Provider string `theorydb:"attr:provider" json:"provider,omitempty"`

	Verified   bool      `theorydb:"attr:verified" json:"verified"`
	VerifiedAt time.Time `theorydb:"attr:verifiedAt" json:"verified_at,omitempty"`

	ProvisionedAt   time.Time `theorydb:"attr:provisionedAt" json:"provisioned_at,omitempty"`
	DeprovisionedAt time.Time `theorydb:"attr:deprovisionedAt" json:"deprovisioned_at,omitempty"`

	Status    string    `theorydb:"attr:status" json:"status"`
	SecretRef string    `theorydb:"attr:secretRef" json:"-"` // SSM parameter name/ARN (per ADR 0004)
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulAgentChannel.
func (SoulAgentChannel) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentChannel.
func (c *SoulAgentChannel) BeforeCreate() error {
	now := time.Now().UTC()
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if strings.TrimSpace(c.Status) == "" {
		c.Status = SoulChannelStatusActive
	}
	if err := c.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", c.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", c.ChannelType); err != nil {
		return err
	}
	if err := requireNonEmpty("identifier", c.Identifier); err != nil {
		return err
	}
	if err := requireOneOf("channelType", c.ChannelType, SoulChannelTypeENS, SoulChannelTypeEmail, SoulChannelTypePhone); err != nil {
		return err
	}
	if err := requireOneOf("status", c.Status, SoulChannelStatusActive, SoulChannelStatusPaused, SoulChannelStatusDecommissioned); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulAgentChannel.
func (c *SoulAgentChannel) BeforeUpdate() error {
	c.UpdatedAt = time.Now().UTC()
	if err := c.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", c.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", c.ChannelType); err != nil {
		return err
	}
	if err := requireNonEmpty("identifier", c.Identifier); err != nil {
		return err
	}
	if err := requireOneOf("channelType", c.ChannelType, SoulChannelTypeENS, SoulChannelTypeEmail, SoulChannelTypePhone); err != nil {
		return err
	}
	if err := requireOneOf("status", c.Status, SoulChannelStatusActive, SoulChannelStatusPaused, SoulChannelStatusDecommissioned); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentChannel.
func (c *SoulAgentChannel) UpdateKeys() error {
	c.AgentID = strings.ToLower(strings.TrimSpace(c.AgentID))
	c.ChannelType = strings.ToLower(strings.TrimSpace(c.ChannelType))
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	c.Status = strings.ToLower(strings.TrimSpace(c.Status))
	c.SecretRef = strings.TrimSpace(c.SecretRef)
	c.ENSResolverAddress = strings.ToLower(strings.TrimSpace(c.ENSResolverAddress))
	c.ENSChain = strings.ToLower(strings.TrimSpace(c.ENSChain))

	switch c.ChannelType {
	case SoulChannelTypeEmail:
		c.Identifier = normalizeSoulEmail(c.Identifier)
	case SoulChannelTypePhone:
		c.Identifier = normalizeSoulPhoneE164(c.Identifier)
	case SoulChannelTypeENS:
		c.Identifier = normalizeSoulENSName(c.Identifier)
	default:
		c.Identifier = strings.TrimSpace(c.Identifier)
	}

	if len(c.Capabilities) > 0 {
		seen := map[string]struct{}{}
		out := make([]string, 0, len(c.Capabilities))
		for _, it := range c.Capabilities {
			it = strings.ToLower(strings.TrimSpace(it))
			if it == "" {
				continue
			}
			if _, ok := seen[it]; ok {
				continue
			}
			seen[it] = struct{}{}
			out = append(out, it)
		}
		sort.Strings(out)
		c.Capabilities = out
	}

	if len(c.Protocols) > 0 {
		seen := map[string]struct{}{}
		out := make([]string, 0, len(c.Protocols))
		for _, it := range c.Protocols {
			it = strings.ToLower(strings.TrimSpace(it))
			if it == "" {
				continue
			}
			if _, ok := seen[it]; ok {
				continue
			}
			seen[it] = struct{}{}
			out = append(out, it)
		}
		sort.Strings(out)
		c.Protocols = out
	}

	c.PK = fmt.Sprintf("SOUL#AGENT#%s", c.AgentID)
	c.SK = fmt.Sprintf("CHANNEL#%s", c.ChannelType)
	return nil
}

// GetPK returns the partition key for SoulAgentChannel.
func (c *SoulAgentChannel) GetPK() string { return c.PK }

// GetSK returns the sort key for SoulAgentChannel.
func (c *SoulAgentChannel) GetSK() string { return c.SK }
