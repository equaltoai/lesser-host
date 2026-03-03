package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	SoulValidationChallengeStatusIssued    = "issued"
	SoulValidationChallengeStatusResponded = "responded"
	SoulValidationChallengeStatusEvaluated = "evaluated"
)

// SoulAgentValidationChallenge stores the mutable state for a validation challenge (issued → responded → evaluated).
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: VALIDATIONCHAL#{challengeId}
type SoulAgentValidationChallenge struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"` // hex-encoded uint256

	ChallengeID   string `theorydb:"attr:challengeId" json:"challenge_id"`
	ChallengeType string `theorydb:"attr:challengeType" json:"challenge_type"`
	ValidatorID   string `theorydb:"attr:validatorId" json:"validator_id"`

	Request  string `theorydb:"attr:request" json:"request,omitempty"`
	Response string `theorydb:"attr:response" json:"response,omitempty"`

	Status      string  `theorydb:"attr:status" json:"status"`
	OptInStatus string  `theorydb:"attr:optInStatus" json:"opt_in_status,omitempty"`
	Result      string  `theorydb:"attr:result" json:"result,omitempty"`
	Score       float64 `theorydb:"attr:score" json:"score,omitempty"`

	IssuedAt    time.Time `theorydb:"attr:issuedAt" json:"issued_at"`
	RespondedAt time.Time `theorydb:"attr:respondedAt" json:"responded_at,omitempty"`
	EvaluatedAt time.Time `theorydb:"attr:evaluatedAt" json:"evaluated_at,omitempty"`
	UpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulAgentValidationChallenge.
func (SoulAgentValidationChallenge) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentValidationChallenge.
func (c *SoulAgentValidationChallenge) BeforeCreate() error {
	now := time.Now().UTC()
	if c.IssuedAt.IsZero() {
		c.IssuedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if c.TTL == 0 {
		c.TTL = c.IssuedAt.Add(30 * 24 * time.Hour).Unix()
	}
	if strings.TrimSpace(c.Status) == "" {
		c.Status = SoulValidationChallengeStatusIssued
	}
	return c.UpdateKeys()
}

// BeforeUpdate updates timestamps and keys before updating SoulAgentValidationChallenge.
func (c *SoulAgentValidationChallenge) BeforeUpdate() error {
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now().UTC()
	}
	return c.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulAgentValidationChallenge.
func (c *SoulAgentValidationChallenge) UpdateKeys() error {
	c.AgentID = strings.ToLower(strings.TrimSpace(c.AgentID))
	c.ChallengeID = strings.TrimSpace(c.ChallengeID)
	c.ChallengeType = strings.ToLower(strings.TrimSpace(c.ChallengeType))
	c.ValidatorID = strings.ToLower(strings.TrimSpace(c.ValidatorID))
	c.Request = strings.TrimSpace(c.Request)
	c.Response = strings.TrimSpace(c.Response)
	c.Status = strings.ToLower(strings.TrimSpace(c.Status))
	c.Result = strings.ToLower(strings.TrimSpace(c.Result))

	c.PK = fmt.Sprintf("SOUL#AGENT#%s", c.AgentID)
	c.SK = fmt.Sprintf("VALIDATIONCHAL#%s", c.ChallengeID)
	return nil
}

// GetPK returns the partition key for SoulAgentValidationChallenge.
func (c *SoulAgentValidationChallenge) GetPK() string { return c.PK }

// GetSK returns the sort key for SoulAgentValidationChallenge.
func (c *SoulAgentValidationChallenge) GetSK() string { return c.SK }
