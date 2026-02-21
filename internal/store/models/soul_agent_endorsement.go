package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentPeerEndorsement stores a signed endorsement from one agent to another.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: ENDORSEMENT#{endorserAgentId}
type SoulAgentPeerEndorsement struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID         string `theorydb:"attr:agentId" json:"agent_id"` // endorsed agent (hex-encoded uint256)
	EndorserAgentID string `theorydb:"attr:endorserAgentId" json:"endorser_agent_id"`

	Message   string `theorydb:"attr:message" json:"message,omitempty"`
	Signature string `theorydb:"attr:signature" json:"signature"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
}

// TableName returns the database table name for SoulAgentPeerEndorsement.
func (SoulAgentPeerEndorsement) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentPeerEndorsement.
func (e *SoulAgentPeerEndorsement) BeforeCreate() error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	return e.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulAgentPeerEndorsement.
func (e *SoulAgentPeerEndorsement) UpdateKeys() error {
	e.AgentID = strings.ToLower(strings.TrimSpace(e.AgentID))
	e.EndorserAgentID = strings.ToLower(strings.TrimSpace(e.EndorserAgentID))
	e.Message = strings.TrimSpace(e.Message)
	e.Signature = strings.ToLower(strings.TrimSpace(e.Signature))

	e.PK = fmt.Sprintf("SOUL#AGENT#%s", e.AgentID)
	e.SK = fmt.Sprintf("ENDORSEMENT#%s", e.EndorserAgentID)
	return nil
}

// GetPK returns the partition key for SoulAgentPeerEndorsement.
func (e *SoulAgentPeerEndorsement) GetPK() string { return e.PK }

// GetSK returns the sort key for SoulAgentPeerEndorsement.
func (e *SoulAgentPeerEndorsement) GetSK() string { return e.SK }
