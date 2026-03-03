package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulDisputeStatus* constants define dispute lifecycle states.
const (
	SoulDisputeStatusOpen      = "open"
	SoulDisputeStatusResolved  = "resolved"
	SoulDisputeStatusDismissed = "dismissed"
)

// SoulAgentDispute stores a dispute record for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: DISPUTE#{disputeId}
type SoulAgentDispute struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID   string `theorydb:"attr:agentId" json:"agent_id"`
	DisputeID string `theorydb:"attr:disputeId" json:"dispute_id"`

	SignalRef  string `theorydb:"attr:signalRef" json:"signal_ref,omitempty"`
	Evidence   string `theorydb:"attr:evidence" json:"evidence,omitempty"`
	Statement  string `theorydb:"attr:statement" json:"statement,omitempty"`
	Resolution string `theorydb:"attr:resolution" json:"resolution,omitempty"`
	Status     string `theorydb:"attr:status" json:"status"`

	CreatedAt  time.Time `theorydb:"attr:createdAt" json:"created_at"`
	ResolvedAt time.Time `theorydb:"attr:resolvedAt" json:"resolved_at,omitempty"`
}

// TableName returns the database table name for SoulAgentDispute.
func (SoulAgentDispute) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentDispute.
func (d *SoulAgentDispute) BeforeCreate() error {
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(d.Status) == "" {
		d.Status = SoulDisputeStatusOpen
	}
	return d.UpdateKeys()
}

// BeforeUpdate updates keys before updating SoulAgentDispute.
func (d *SoulAgentDispute) BeforeUpdate() error {
	return d.UpdateKeys()
}

// UpdateKeys updates the database keys for SoulAgentDispute.
func (d *SoulAgentDispute) UpdateKeys() error {
	d.AgentID = strings.ToLower(strings.TrimSpace(d.AgentID))
	d.DisputeID = strings.TrimSpace(d.DisputeID)
	d.SignalRef = strings.TrimSpace(d.SignalRef)
	d.Evidence = strings.TrimSpace(d.Evidence)
	d.Statement = strings.TrimSpace(d.Statement)
	d.Resolution = strings.TrimSpace(d.Resolution)
	d.Status = strings.ToLower(strings.TrimSpace(d.Status))

	d.PK = fmt.Sprintf("SOUL#AGENT#%s", d.AgentID)
	d.SK = fmt.Sprintf("DISPUTE#%s", d.DisputeID)
	return nil
}

// GetPK returns the partition key for SoulAgentDispute.
func (d *SoulAgentDispute) GetPK() string { return d.PK }

// GetSK returns the sort key for SoulAgentDispute.
func (d *SoulAgentDispute) GetSK() string { return d.SK }
