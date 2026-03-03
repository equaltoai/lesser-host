package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulRelationshipType* constants enumerate relationship types.
const (
	SoulRelationshipTypeEndorsement     = "endorsement"
	SoulRelationshipTypeDelegation      = "delegation"
	SoulRelationshipTypeCollaboration   = "collaboration"
	SoulRelationshipTypeTrustGrant      = "trust_grant"
	SoulRelationshipTypeTrustRevocation = "trust_revocation"
)

// SoulAgentRelationship stores a relationship record between two soul agents.
// The record is stored under the *target* agent (toAgentId) for easy lookup of
// "who has expressed something about this agent."
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}       (toAgentId — the agent the record is about)
//	SK: RELATIONSHIP#{fromAgentId}#{timestamp}
type SoulAgentRelationship struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	FromAgentID string `theorydb:"attr:fromAgentId" json:"from_agent_id"`
	ToAgentID   string `theorydb:"attr:toAgentId" json:"to_agent_id"`

	Type        string         `theorydb:"attr:type" json:"type"`
	ContextJSON string         `theorydb:"attr:context" json:"-"`                   // legacy: JSON object encoded as string
	ContextV2   map[string]any `theorydb:"attr:contextV2" json:"context,omitempty"` // typed context map
	TaskType    string         `theorydb:"attr:taskType" json:"-"`                  // extracted for filtering

	Message   string `theorydb:"attr:message" json:"message,omitempty"`
	Signature string `theorydb:"attr:signature" json:"signature,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for SoulAgentRelationship.
func (SoulAgentRelationship) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentRelationship.
func (r *SoulAgentRelationship) BeforeCreate() error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("fromAgentId", r.FromAgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("toAgentId", r.ToAgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("type", r.Type); err != nil {
		return err
	}
	if strings.EqualFold(r.FromAgentID, r.ToAgentID) {
		return fmt.Errorf("fromAgentId and toAgentId must differ")
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentRelationship.
func (r *SoulAgentRelationship) UpdateKeys() error {
	r.FromAgentID = strings.ToLower(strings.TrimSpace(r.FromAgentID))
	r.ToAgentID = strings.ToLower(strings.TrimSpace(r.ToAgentID))
	r.Type = strings.ToLower(strings.TrimSpace(r.Type))
	r.ContextJSON = strings.TrimSpace(r.ContextJSON)
	r.TaskType = strings.ToLower(strings.TrimSpace(r.TaskType))
	r.Message = strings.TrimSpace(r.Message)
	r.Signature = strings.ToLower(strings.TrimSpace(r.Signature))

	ts := r.CreatedAt.UTC().Format(time.RFC3339Nano)
	r.PK = fmt.Sprintf("SOUL#AGENT#%s", r.ToAgentID)
	r.SK = fmt.Sprintf("RELATIONSHIP#%s#%s", r.FromAgentID, ts)
	return nil
}

// GetPK returns the partition key for SoulAgentRelationship.
func (r *SoulAgentRelationship) GetPK() string { return r.PK }

// GetSK returns the sort key for SoulAgentRelationship.
func (r *SoulAgentRelationship) GetSK() string { return r.SK }
