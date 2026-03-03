package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulRelationshipFromIndex is a materialized index for querying relationships
// *from* a given agent. This is the inverse of the main SoulAgentRelationship
// record which is stored under the target (toAgentId).
//
// Keys:
//
//	PK: SOUL#RELATIONSHIPS_FROM#{fromAgentId}
//	SK: TO#{toAgentId}#{timestamp}
type SoulRelationshipFromIndex struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	FromAgentID string `theorydb:"attr:fromAgentId" json:"from_agent_id"`
	ToAgentID   string `theorydb:"attr:toAgentId" json:"to_agent_id"`

	Type string `theorydb:"attr:type" json:"type"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for SoulRelationshipFromIndex.
func (SoulRelationshipFromIndex) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulRelationshipFromIndex.
func (i *SoulRelationshipFromIndex) BeforeCreate() error {
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Now().UTC()
	}
	return i.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulRelationshipFromIndex.
func (i *SoulRelationshipFromIndex) UpdateKeys() error {
	i.FromAgentID = strings.ToLower(strings.TrimSpace(i.FromAgentID))
	i.ToAgentID = strings.ToLower(strings.TrimSpace(i.ToAgentID))
	i.Type = strings.ToLower(strings.TrimSpace(i.Type))

	ts := i.CreatedAt.UTC().Format(time.RFC3339Nano)
	i.PK = fmt.Sprintf("SOUL#RELATIONSHIPS_FROM#%s", i.FromAgentID)
	i.SK = fmt.Sprintf("TO#%s#%s", i.ToAgentID, ts)
	return nil
}

// GetPK returns the partition key for SoulRelationshipFromIndex.
func (i *SoulRelationshipFromIndex) GetPK() string { return i.PK }

// GetSK returns the sort key for SoulRelationshipFromIndex.
func (i *SoulRelationshipFromIndex) GetSK() string { return i.SK }
