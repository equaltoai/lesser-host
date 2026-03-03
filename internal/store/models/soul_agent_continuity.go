package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulContinuityEntryType* constants enumerate continuity entry types.
const (
	SoulContinuityEntryTypeCapabilityAcquired  = "capability_acquired"
	SoulContinuityEntryTypeCapabilityDeprecated = "capability_deprecated"
	SoulContinuityEntryTypeSignificantFailure  = "significant_failure"
	SoulContinuityEntryTypeRecovery            = "recovery"
	SoulContinuityEntryTypeBoundaryAdded       = "boundary_added"
	SoulContinuityEntryTypeMigration           = "migration"
	SoulContinuityEntryTypeModelChange         = "model_change"
	SoulContinuityEntryTypeRelationshipFormed  = "relationship_formed"
	SoulContinuityEntryTypeRelationshipEnded   = "relationship_ended"
	SoulContinuityEntryTypeSelfSuspension      = "self_suspension"
	SoulContinuityEntryTypeArchived            = "archived"
	SoulContinuityEntryTypeSuccessionDeclared  = "succession_declared"
	SoulContinuityEntryTypeSuccessionReceived  = "succession_received"
)

// SoulAgentContinuity stores a single continuity journal entry for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: CONTINUITY#{timestamp}#{entryType}
type SoulAgentContinuity struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"`

	Type      string `theorydb:"attr:type" json:"type"`
	Summary   string `theorydb:"attr:summary" json:"summary"`
	Recovery  string `theorydb:"attr:recovery" json:"recovery,omitempty"`
	References string `theorydb:"attr:references" json:"references,omitempty"` // JSON array of reference IDs
	Signature string `theorydb:"attr:signature" json:"signature,omitempty"`

	Timestamp time.Time `theorydb:"attr:timestamp" json:"timestamp"`
}

// TableName returns the database table name for SoulAgentContinuity.
func (SoulAgentContinuity) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentContinuity.
func (c *SoulAgentContinuity) BeforeCreate() error {
	if c.Timestamp.IsZero() {
		c.Timestamp = time.Now().UTC()
	}
	return c.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulAgentContinuity.
func (c *SoulAgentContinuity) UpdateKeys() error {
	c.AgentID = strings.ToLower(strings.TrimSpace(c.AgentID))
	c.Type = strings.ToLower(strings.TrimSpace(c.Type))
	c.Summary = strings.TrimSpace(c.Summary)
	c.Recovery = strings.TrimSpace(c.Recovery)
	c.References = strings.TrimSpace(c.References)
	c.Signature = strings.ToLower(strings.TrimSpace(c.Signature))

	ts := c.Timestamp.UTC().Format(time.RFC3339Nano)
	c.PK = fmt.Sprintf("SOUL#AGENT#%s", c.AgentID)
	c.SK = fmt.Sprintf("CONTINUITY#%s#%s", ts, c.Type)
	return nil
}

// GetPK returns the partition key for SoulAgentContinuity.
func (c *SoulAgentContinuity) GetPK() string { return c.PK }

// GetSK returns the sort key for SoulAgentContinuity.
func (c *SoulAgentContinuity) GetSK() string { return c.SK }
