package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulLifecycleChallenge stores a short-lived, single-use lifecycle nonce issued
// by archive/successor begin endpoints.
type SoulLifecycleChallenge struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	AgentID          string    `theorydb:"attr:agentId" json:"agent_id"`
	Nonce            string    `theorydb:"attr:nonce" json:"nonce"`
	Purpose          string    `theorydb:"attr:purpose" json:"purpose"`
	SuccessorAgentID string    `theorydb:"attr:successorAgentId" json:"successor_agent_id,omitempty"`
	IssuedAt         time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	ExpiresAt        time.Time `theorydb:"attr:expiresAt" json:"expires_at"`
}

// TableName returns the database table name for SoulLifecycleChallenge.
func (SoulLifecycleChallenge) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulLifecycleChallenge.
func (c *SoulLifecycleChallenge) BeforeCreate() error {
	if c.IssuedAt.IsZero() {
		c.IssuedAt = time.Now().UTC()
	}
	if c.ExpiresAt.IsZero() {
		c.ExpiresAt = c.IssuedAt.Add(5 * time.Minute)
	}
	if err := c.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", c.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("nonce", c.Nonce); err != nil {
		return err
	}
	if err := requireNonEmpty("purpose", c.Purpose); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys and TTL for SoulLifecycleChallenge.
func (c *SoulLifecycleChallenge) UpdateKeys() error {
	c.AgentID = strings.ToLower(strings.TrimSpace(c.AgentID))
	c.Nonce = strings.TrimSpace(c.Nonce)
	c.Purpose = strings.ToLower(strings.TrimSpace(c.Purpose))
	c.SuccessorAgentID = strings.ToLower(strings.TrimSpace(c.SuccessorAgentID))
	c.IssuedAt = c.IssuedAt.UTC()
	c.ExpiresAt = c.ExpiresAt.UTC()
	c.PK = fmt.Sprintf("SOUL#AGENT#%s", c.AgentID)
	c.SK = fmt.Sprintf("LIFECYCLE_CHALLENGE#%s", c.Nonce)
	if !c.ExpiresAt.IsZero() {
		c.TTL = c.ExpiresAt.Unix()
	}
	return nil
}

// GetPK returns the partition key for SoulLifecycleChallenge.
func (c *SoulLifecycleChallenge) GetPK() string { return c.PK }

// GetSK returns the sort key for SoulLifecycleChallenge.
func (c *SoulLifecycleChallenge) GetSK() string { return c.SK }
