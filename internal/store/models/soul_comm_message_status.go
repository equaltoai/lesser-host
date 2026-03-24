package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulCommMessageStatus* constants define supported comm message status values.
const (
	SoulCommMessageStatusAccepted = "accepted"
	SoulCommMessageStatusSent     = "sent"
	SoulCommMessageStatusFailed   = "failed"
)

// SoulCommMessageStatus stores delivery status for a single comm message.
//
// Keys:
//
//	PK: COMM#MSG#{messageId}
//	SK: STATUS
type SoulCommMessageStatus struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK,omitempty" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK,omitempty" json:"-"`

	MessageID      string `theorydb:"attr:messageId" json:"message_id"`
	InstanceSlug   string `theorydb:"attr:instanceSlug" json:"instance_slug,omitempty"`
	AgentID        string `theorydb:"attr:agentId" json:"agent_id"`
	IdempotencyKey string `theorydb:"attr:idempotencyKey" json:"idempotency_key,omitempty"`

	ChannelType string `theorydb:"attr:channelType" json:"channel_type"` // email|sms|voice
	To          string `theorydb:"attr:to" json:"to"`

	Provider          string `theorydb:"attr:provider" json:"provider,omitempty"`
	ProviderMessageID string `theorydb:"attr:providerMessageId" json:"provider_message_id,omitempty"`

	Status          string    `theorydb:"attr:status" json:"status"` // accepted|sent|failed
	ErrorCode       string    `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage    string    `theorydb:"attr:errorMessage" json:"error_message,omitempty"`
	ReplyMessageID  string    `theorydb:"attr:replyMessageId" json:"reply_message_id,omitempty"`
	ReplyBody       string    `theorydb:"attr:replyBody" json:"reply_body,omitempty"`
	ReplyConfidence *float64  `theorydb:"attr:replyConfidence" json:"reply_confidence,omitempty"`
	ReplyReceivedAt time.Time `theorydb:"attr:replyReceivedAt" json:"reply_received_at,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulCommMessageStatus.
func (SoulCommMessageStatus) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulCommMessageStatus.
func (m *SoulCommMessageStatus) BeforeCreate() error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = SoulCommMessageStatusAccepted
	}
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	if strings.TrimSpace(m.IdempotencyKey) != "" {
		if err := requireNonEmpty("instanceSlug", m.InstanceSlug); err != nil {
			return err
		}
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", m.ChannelType); err != nil {
		return err
	}
	if err := requireNonEmpty("to", m.To); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulCommMessageStatusAccepted, SoulCommMessageStatusSent, SoulCommMessageStatusFailed); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulCommMessageStatus.
func (m *SoulCommMessageStatus) BeforeUpdate() error {
	m.UpdatedAt = time.Now().UTC()
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulCommMessageStatusAccepted, SoulCommMessageStatusSent, SoulCommMessageStatusFailed); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulCommMessageStatus.
func (m *SoulCommMessageStatus) UpdateKeys() error {
	m.MessageID = strings.TrimSpace(m.MessageID)
	m.InstanceSlug = strings.ToLower(strings.TrimSpace(m.InstanceSlug))
	m.AgentID = strings.ToLower(strings.TrimSpace(m.AgentID))
	m.IdempotencyKey = strings.TrimSpace(m.IdempotencyKey)
	m.ChannelType = strings.ToLower(strings.TrimSpace(m.ChannelType))
	m.To = strings.TrimSpace(m.To)
	m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
	m.ProviderMessageID = strings.TrimSpace(m.ProviderMessageID)
	m.Status = strings.ToLower(strings.TrimSpace(m.Status))
	m.ErrorCode = strings.TrimSpace(m.ErrorCode)
	m.ErrorMessage = strings.TrimSpace(m.ErrorMessage)
	m.ReplyMessageID = strings.TrimSpace(m.ReplyMessageID)
	m.ReplyBody = strings.TrimSpace(m.ReplyBody)

	m.PK = fmt.Sprintf("COMM#MSG#%s", m.MessageID)
	m.SK = "STATUS"
	if m.InstanceSlug != "" && m.IdempotencyKey != "" && m.AgentID != "" {
		m.GSI1PK = SoulCommMessageStatusIdempotencyIndexPK(m.InstanceSlug, m.AgentID, m.IdempotencyKey)
		m.GSI1SK = fmt.Sprintf("%s#%s", m.CreatedAt.UTC().Format(time.RFC3339Nano), m.MessageID)
	} else {
		m.GSI1PK = ""
		m.GSI1SK = ""
	}
	m.TTL = m.CreatedAt.UTC().Add(90 * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulCommMessageStatus.
func (m *SoulCommMessageStatus) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulCommMessageStatus.
func (m *SoulCommMessageStatus) GetSK() string { return m.SK }
