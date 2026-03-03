package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentFailure stores a failure record for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: FAILURE#{timestamp}#{failureId}
type SoulAgentFailure struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID   string `theorydb:"attr:agentId" json:"agent_id"`
	FailureID string `theorydb:"attr:failureId" json:"failure_id"`

	FailureType string `theorydb:"attr:failureType" json:"failure_type"`
	Description string `theorydb:"attr:description" json:"description,omitempty"`
	Impact      string `theorydb:"attr:impact" json:"impact,omitempty"`
	RecoveryRef string `theorydb:"attr:recoveryRef" json:"recovery_ref,omitempty"`
	Status      string `theorydb:"attr:status" json:"status,omitempty"` // "open", "recovered"

	Timestamp time.Time `theorydb:"attr:timestamp" json:"timestamp"`
}

// TableName returns the database table name for SoulAgentFailure.
func (SoulAgentFailure) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentFailure.
func (f *SoulAgentFailure) BeforeCreate() error {
	if f.Timestamp.IsZero() {
		f.Timestamp = time.Now().UTC()
	}
	if err := f.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", f.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("failureId", f.FailureID); err != nil {
		return err
	}
	if err := requireNonEmpty("failureType", f.FailureType); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentFailure.
func (f *SoulAgentFailure) UpdateKeys() error {
	f.AgentID = strings.ToLower(strings.TrimSpace(f.AgentID))
	f.FailureID = strings.TrimSpace(f.FailureID)
	f.FailureType = strings.ToLower(strings.TrimSpace(f.FailureType))
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))
	f.Description = strings.TrimSpace(f.Description)
	f.Impact = strings.TrimSpace(f.Impact)
	f.RecoveryRef = strings.TrimSpace(f.RecoveryRef)

	ts := f.Timestamp.UTC().Format(time.RFC3339Nano)
	f.PK = fmt.Sprintf("SOUL#AGENT#%s", f.AgentID)
	f.SK = fmt.Sprintf("FAILURE#%s#%s", ts, f.FailureID)
	return nil
}

// GetPK returns the partition key for SoulAgentFailure.
func (f *SoulAgentFailure) GetPK() string { return f.PK }

// GetSK returns the sort key for SoulAgentFailure.
func (f *SoulAgentFailure) GetSK() string { return f.SK }
