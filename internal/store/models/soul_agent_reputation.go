package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulAgentReputation stores the off-chain reputation summary record for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: REPUTATION
type SoulAgentReputation struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"` // hex-encoded uint256

	BlockRef int64 `theorydb:"attr:blockRef" json:"block_ref,omitempty"`

	Composite     float64 `theorydb:"attr:composite" json:"composite"`
	Economic      float64 `theorydb:"attr:economic" json:"economic"`
	Social        float64 `theorydb:"attr:social" json:"social"`
	Validation    float64 `theorydb:"attr:validation" json:"validation"`
	Trust         float64 `theorydb:"attr:trust" json:"trust"`
	Integrity     float64 `theorydb:"attr:integrity" json:"integrity"`
	Communication float64 `theorydb:"attr:communication" json:"communication"`

	TipsReceived         int64 `theorydb:"attr:tipsReceived" json:"tips_received"`
	Interactions         int64 `theorydb:"attr:interactions" json:"interactions"`
	ValidationsPassed    int64 `theorydb:"attr:validationsPassed" json:"validations_passed"`
	Endorsements         int64 `theorydb:"attr:endorsements" json:"endorsements"`
	Flags                int64 `theorydb:"attr:flags" json:"flags"`
	DelegationsCompleted int64 `theorydb:"attr:delegationsCompleted" json:"delegations_completed"`
	BoundaryViolations   int64 `theorydb:"attr:boundaryViolations" json:"boundary_violations"`
	FailureRecoveries    int64 `theorydb:"attr:failureRecoveries" json:"failure_recoveries"`

	EmailsSent                      int64   `theorydb:"attr:emailsSent" json:"emails_sent"`
	EmailsReceived                  int64   `theorydb:"attr:emailsReceived" json:"emails_received"`
	SMSSent                         int64   `theorydb:"attr:smsSent" json:"sms_sent"`
	SMSReceived                     int64   `theorydb:"attr:smsReceived" json:"sms_received"`
	CallsMade                       int64   `theorydb:"attr:callsMade" json:"calls_made"`
	CallsReceived                   int64   `theorydb:"attr:callsReceived" json:"calls_received"`
	CommunicationBoundaryViolations int64   `theorydb:"attr:communicationBoundaryViolations" json:"communication_boundary_violations"`
	SpamReports                     int64   `theorydb:"attr:spamReports" json:"spam_reports"`
	ResponseRate                    float64 `theorydb:"attr:responseRate" json:"response_rate"`
	AvgResponseTimeMinutes          float64 `theorydb:"attr:avgResponseTimeMinutes" json:"avg_response_time_minutes"`

	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulAgentReputation.
func (SoulAgentReputation) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentReputation.
func (r *SoulAgentReputation) BeforeCreate() error {
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	if err := requireNonEmpty("agentId", r.AgentID); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulAgentReputation.
func (r *SoulAgentReputation) BeforeUpdate() error {
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now().UTC()
	}
	if err := r.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", r.AgentID); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentReputation.
func (r *SoulAgentReputation) UpdateKeys() error {
	r.AgentID = strings.ToLower(strings.TrimSpace(r.AgentID))

	r.PK = fmt.Sprintf("SOUL#AGENT#%s", r.AgentID)
	r.SK = "REPUTATION"
	return nil
}

// GetPK returns the partition key for SoulAgentReputation.
func (r *SoulAgentReputation) GetPK() string { return r.PK }

// GetSK returns the sort key for SoulAgentReputation.
func (r *SoulAgentReputation) GetSK() string { return r.SK }
