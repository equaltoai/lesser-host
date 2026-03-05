package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulCommQueueStatus* constants define delivery queue status values.
const (
	SoulCommQueueStatusQueued    = "queued"
	SoulCommQueueStatusDelivered = "delivered"
	SoulCommQueueStatusExpired   = "expired"
)

// SoulAgentCommQueue stores a queued inbound communication for later delivery.
//
// Keys:
//
//	PK: COMM#QUEUE#{agentId}
//	SK: MSG#{scheduledDeliveryTime}#{messageId}
type SoulAgentCommQueue struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	AgentID     string `theorydb:"attr:agentId" json:"agent_id"`
	MessageID   string `theorydb:"attr:messageId" json:"message_id"`
	ChannelType string `theorydb:"attr:channelType" json:"channel_type"` // email|sms|voice

	FromAddress     string `theorydb:"attr:fromAddress" json:"from_address,omitempty"`
	FromNumber      string `theorydb:"attr:fromNumber" json:"from_number,omitempty"`
	FromSoulAgentID string `theorydb:"attr:fromSoulAgentId" json:"from_soul_agent_id,omitempty"`
	FromDisplayName string `theorydb:"attr:fromDisplayName" json:"from_display_name,omitempty"`

	Subject string `theorydb:"attr:subject" json:"subject,omitempty"`
	Body    string `theorydb:"attr:body" json:"body"`

	InReplyTo string `theorydb:"attr:inReplyTo" json:"in_reply_to,omitempty"`

	ReceivedAt            time.Time `theorydb:"attr:receivedAt" json:"received_at"`
	ScheduledDeliveryTime time.Time `theorydb:"attr:scheduledDeliveryTime" json:"scheduled_delivery_time"`
	Status                string    `theorydb:"attr:status" json:"status"`
}

// TableName returns the database table name for SoulAgentCommQueue.
func (SoulAgentCommQueue) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentCommQueue.
func (q *SoulAgentCommQueue) BeforeCreate() error {
	now := time.Now().UTC()
	if q.ReceivedAt.IsZero() {
		q.ReceivedAt = now
	}
	if q.ScheduledDeliveryTime.IsZero() {
		q.ScheduledDeliveryTime = q.ReceivedAt
	}
	if strings.TrimSpace(q.Status) == "" {
		q.Status = SoulCommQueueStatusQueued
	}
	if err := q.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", q.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", q.MessageID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", q.ChannelType); err != nil {
		return err
	}
	if err := requireNonEmpty("body", q.Body); err != nil {
		return err
	}
	if err := requireOneOf("status", q.Status, SoulCommQueueStatusQueued, SoulCommQueueStatusDelivered, SoulCommQueueStatusExpired); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentCommQueue.
func (q *SoulAgentCommQueue) UpdateKeys() error {
	q.AgentID = strings.ToLower(strings.TrimSpace(q.AgentID))
	q.MessageID = strings.TrimSpace(q.MessageID)
	q.ChannelType = strings.ToLower(strings.TrimSpace(q.ChannelType))

	q.FromAddress = normalizeSoulEmail(q.FromAddress)
	q.FromNumber = normalizeSoulPhoneE164(q.FromNumber)
	q.FromSoulAgentID = strings.ToLower(strings.TrimSpace(q.FromSoulAgentID))
	q.FromDisplayName = strings.TrimSpace(q.FromDisplayName)

	q.Subject = strings.TrimSpace(q.Subject)
	q.Body = strings.TrimSpace(q.Body)
	q.InReplyTo = strings.TrimSpace(q.InReplyTo)

	q.Status = strings.ToLower(strings.TrimSpace(q.Status))

	scheduled := q.ScheduledDeliveryTime.UTC().Format("2006-01-02T15:04:05.000000000Z")
	q.PK = fmt.Sprintf("COMM#QUEUE#%s", q.AgentID)
	q.SK = fmt.Sprintf("MSG#%s#%s", scheduled, q.MessageID)
	q.TTL = q.ReceivedAt.UTC().Add(72 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulAgentCommQueue.
func (q *SoulAgentCommQueue) GetPK() string { return q.PK }

// GetSK returns the sort key for SoulAgentCommQueue.
func (q *SoulAgentCommQueue) GetSK() string { return q.SK }
