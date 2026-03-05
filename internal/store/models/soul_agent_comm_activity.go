package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulCommDirection* constants define message direction.
const (
	SoulCommDirectionInbound  = "inbound"
	SoulCommDirectionOutbound = "outbound"
)

// SoulCommBoundaryCheck* constants define boundary-check outcomes.
const (
	SoulCommBoundaryCheckPassed   = "passed"
	SoulCommBoundaryCheckViolated = "violated"
	SoulCommBoundaryCheckSkipped  = "skipped"
)

// SoulAgentCommActivity stores a single communication activity event for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: COMM#{timestamp}#{activityId}
type SoulAgentCommActivity struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	AgentID     string `theorydb:"attr:agentId" json:"agent_id"`
	ActivityID  string `theorydb:"attr:activityId" json:"activity_id"`
	ChannelType string `theorydb:"attr:channelType" json:"channel_type"` // email|sms|voice
	Direction   string `theorydb:"attr:direction" json:"direction"`      // inbound|outbound

	Counterparty string `theorydb:"attr:counterparty" json:"counterparty,omitempty"` // email/phone/agentId
	Action       string `theorydb:"attr:action" json:"action,omitempty"`             // send|receive|call|sms

	MessageID string `theorydb:"attr:messageId" json:"message_id,omitempty"`
	InReplyTo string `theorydb:"attr:inReplyTo" json:"in_reply_to,omitempty"`

	BoundaryCheck       string `theorydb:"attr:boundaryCheck" json:"boundary_check,omitempty"` // passed|violated|skipped
	PreferenceRespected *bool  `theorydb:"attr:preferenceRespected" json:"preference_respected,omitempty"`

	Timestamp time.Time `theorydb:"attr:timestamp" json:"timestamp"`
}

// TableName returns the database table name for SoulAgentCommActivity.
func (SoulAgentCommActivity) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentCommActivity.
func (a *SoulAgentCommActivity) BeforeCreate() error {
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(a.Direction) == "" {
		a.Direction = SoulCommDirectionInbound
	}
	if strings.TrimSpace(a.BoundaryCheck) == "" {
		a.BoundaryCheck = SoulCommBoundaryCheckSkipped
	}
	if err := a.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", a.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("activityId", a.ActivityID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", a.ChannelType); err != nil {
		return err
	}
	if err := requireOneOf("direction", a.Direction, SoulCommDirectionInbound, SoulCommDirectionOutbound); err != nil {
		return err
	}
	if err := requireOneOf("boundaryCheck", a.BoundaryCheck, SoulCommBoundaryCheckPassed, SoulCommBoundaryCheckViolated, SoulCommBoundaryCheckSkipped); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentCommActivity.
func (a *SoulAgentCommActivity) UpdateKeys() error {
	a.AgentID = strings.ToLower(strings.TrimSpace(a.AgentID))
	a.ActivityID = strings.TrimSpace(a.ActivityID)
	a.ChannelType = strings.ToLower(strings.TrimSpace(a.ChannelType))
	a.Direction = strings.ToLower(strings.TrimSpace(a.Direction))
	a.Counterparty = strings.TrimSpace(a.Counterparty)
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	a.MessageID = strings.TrimSpace(a.MessageID)
	a.InReplyTo = strings.TrimSpace(a.InReplyTo)
	a.BoundaryCheck = strings.ToLower(strings.TrimSpace(a.BoundaryCheck))

	ts := a.Timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z")
	a.PK = fmt.Sprintf("SOUL#AGENT#%s", a.AgentID)
	a.SK = fmt.Sprintf("COMM#%s#%s", ts, a.ActivityID)
	a.TTL = a.Timestamp.UTC().Add(90 * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulAgentCommActivity.
func (a *SoulAgentCommActivity) GetPK() string { return a.PK }

// GetSK returns the sort key for SoulAgentCommActivity.
func (a *SoulAgentCommActivity) GetSK() string { return a.SK }
