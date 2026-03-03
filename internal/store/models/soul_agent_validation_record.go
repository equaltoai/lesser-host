package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulValidationResult* constants describe evaluation outcomes.
const (
	SoulValidationResultPass     = "pass"
	SoulValidationResultFail     = "fail"
	SoulValidationResultTimeout  = "timeout"
	SoulValidationResultDeclined = "declined"
)

// SoulValidationOptInStatus* constants describe opt-in states for validation challenges.
const (
	SoulValidationOptInStatusAccepted = "accepted"
	SoulValidationOptInStatusDeclined = "declined"
	SoulValidationOptInStatusPending  = "pending"
)

// SoulAgentValidationRecord stores a single validation challenge evaluation record.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: VALIDATION#{timestamp}#{challengeId}
type SoulAgentValidationRecord struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"` // hex-encoded uint256

	ChallengeID   string `theorydb:"attr:challengeId" json:"challenge_id"`
	ChallengeType string `theorydb:"attr:challengeType" json:"challenge_type"`
	ValidatorID   string `theorydb:"attr:validatorId" json:"validator_id"`

	Request  string `theorydb:"attr:request" json:"request,omitempty"`
	Response string `theorydb:"attr:response" json:"response,omitempty"`

	Result string  `theorydb:"attr:result" json:"result"`
	Score  float64 `theorydb:"attr:score" json:"score"`

	OptInStatus string `theorydb:"attr:optInStatus" json:"opt_in_status,omitempty"`

	EvaluatedAt time.Time `theorydb:"attr:evaluatedAt" json:"evaluated_at"`
}

// TableName returns the database table name for SoulAgentValidationRecord.
func (SoulAgentValidationRecord) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentValidationRecord.
func (v *SoulAgentValidationRecord) BeforeCreate() error {
	if v.EvaluatedAt.IsZero() {
		v.EvaluatedAt = time.Now().UTC()
	}
	if err := v.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", v.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("challengeId", v.ChallengeID); err != nil {
		return err
	}
	if err := requireNonEmpty("challengeType", v.ChallengeType); err != nil {
		return err
	}
	if err := requireNonEmpty("validatorId", v.ValidatorID); err != nil {
		return err
	}
	if err := requireOneOf("result", v.Result, SoulValidationResultPass, SoulValidationResultFail, SoulValidationResultTimeout, SoulValidationResultDeclined); err != nil {
		return err
	}
	if strings.TrimSpace(v.OptInStatus) != "" {
		if err := requireOneOf("optInStatus", v.OptInStatus, SoulValidationOptInStatusAccepted, SoulValidationOptInStatusDeclined, SoulValidationOptInStatusPending); err != nil {
			return err
		}
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentValidationRecord.
func (v *SoulAgentValidationRecord) UpdateKeys() error {
	v.AgentID = strings.ToLower(strings.TrimSpace(v.AgentID))
	v.ChallengeID = strings.TrimSpace(v.ChallengeID)
	v.ChallengeType = strings.ToLower(strings.TrimSpace(v.ChallengeType))
	v.ValidatorID = strings.ToLower(strings.TrimSpace(v.ValidatorID))
	v.Request = strings.TrimSpace(v.Request)
	v.Response = strings.TrimSpace(v.Response)
	v.Result = strings.ToLower(strings.TrimSpace(v.Result))
	v.OptInStatus = strings.ToLower(strings.TrimSpace(v.OptInStatus))

	v.PK = fmt.Sprintf("SOUL#AGENT#%s", v.AgentID)
	ts := v.EvaluatedAt.UTC().Format(time.RFC3339Nano)
	v.SK = fmt.Sprintf("VALIDATION#%s#%s", ts, v.ChallengeID)
	return nil
}

// GetPK returns the partition key for SoulAgentValidationRecord.
func (v *SoulAgentValidationRecord) GetPK() string { return v.PK }

// GetSK returns the sort key for SoulAgentValidationRecord.
func (v *SoulAgentValidationRecord) GetSK() string { return v.SK }
