package models

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	SoulCommSendIdempotencyStatusProcessing = "processing"
	SoulCommSendIdempotencyStatusSucceeded  = "succeeded"
	SoulCommSendIdempotencyStatusFailed     = "failed"
)

// SoulCommSendIdempotency stores the outcome for an outbound comm send request keyed by
// caller-provided idempotency material so retries can return the original result without
// re-dispatching the provider call.
type SoulCommSendIdempotency struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	InstanceSlug   string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	AgentID        string `theorydb:"attr:agentId" json:"agent_id"`
	IdempotencyKey string `theorydb:"attr:idempotencyKey" json:"idempotency_key"`
	RequestHash    string `theorydb:"attr:requestHash" json:"request_hash"`

	MessageID      string `theorydb:"attr:messageId" json:"message_id"`
	ChannelType    string `theorydb:"attr:channelType" json:"channel_type"`
	To             string `theorydb:"attr:to" json:"to"`
	Status         string `theorydb:"attr:status" json:"status"`                  // processing|succeeded|failed
	ResponseStatus string `theorydb:"attr:responseStatus" json:"response_status"` // accepted|sent|failed

	Provider          string `theorydb:"attr:provider" json:"provider,omitempty"`
	ProviderMessageID string `theorydb:"attr:providerMessageId" json:"provider_message_id,omitempty"`

	ErrorCode       string `theorydb:"attr:errorCode" json:"error_code,omitempty"`
	ErrorMessage    string `theorydb:"attr:errorMessage" json:"error_message,omitempty"`
	ErrorStatusCode int    `theorydb:"attr:errorStatusCode" json:"error_status_code,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulCommSendIdempotency.
func (SoulCommSendIdempotency) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulCommSendIdempotency.
func (m *SoulCommSendIdempotency) BeforeCreate() error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = SoulCommSendIdempotencyStatusProcessing
	}
	if strings.TrimSpace(m.ResponseStatus) == "" {
		m.ResponseStatus = SoulCommMessageStatusAccepted
	}
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", m.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("idempotencyKey", m.IdempotencyKey); err != nil {
		return err
	}
	if err := requireNonEmpty("requestHash", m.RequestHash); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	if err := requireNonEmpty("channelType", m.ChannelType); err != nil {
		return err
	}
	if err := requireNonEmpty("to", m.To); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulCommSendIdempotencyStatusProcessing, SoulCommSendIdempotencyStatusSucceeded, SoulCommSendIdempotencyStatusFailed); err != nil {
		return err
	}
	return requireOneOf("responseStatus", m.ResponseStatus, SoulCommMessageStatusAccepted, SoulCommMessageStatusSent, SoulCommMessageStatusFailed)
}

// BeforeUpdate updates timestamps and keys before updating SoulCommSendIdempotency.
func (m *SoulCommSendIdempotency) BeforeUpdate() error {
	m.UpdatedAt = time.Now().UTC()
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", m.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("idempotencyKey", m.IdempotencyKey); err != nil {
		return err
	}
	if err := requireNonEmpty("requestHash", m.RequestHash); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulCommSendIdempotencyStatusProcessing, SoulCommSendIdempotencyStatusSucceeded, SoulCommSendIdempotencyStatusFailed); err != nil {
		return err
	}
	return requireOneOf("responseStatus", m.ResponseStatus, SoulCommMessageStatusAccepted, SoulCommMessageStatusSent, SoulCommMessageStatusFailed)
}

// UpdateKeys updates the database keys for SoulCommSendIdempotency.
func (m *SoulCommSendIdempotency) UpdateKeys() error {
	m.InstanceSlug = strings.ToLower(strings.TrimSpace(m.InstanceSlug))
	m.AgentID = strings.ToLower(strings.TrimSpace(m.AgentID))
	m.IdempotencyKey = strings.TrimSpace(m.IdempotencyKey)
	m.RequestHash = strings.TrimSpace(m.RequestHash)
	m.MessageID = strings.TrimSpace(m.MessageID)
	m.ChannelType = strings.ToLower(strings.TrimSpace(m.ChannelType))
	m.To = strings.TrimSpace(m.To)
	m.Status = strings.ToLower(strings.TrimSpace(m.Status))
	m.ResponseStatus = strings.ToLower(strings.TrimSpace(m.ResponseStatus))
	m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
	m.ProviderMessageID = strings.TrimSpace(m.ProviderMessageID)
	m.ErrorCode = strings.TrimSpace(m.ErrorCode)
	m.ErrorMessage = strings.TrimSpace(m.ErrorMessage)

	m.PK = SoulCommSendIdempotencyPK(m.InstanceSlug, m.AgentID, m.IdempotencyKey)
	m.SK = "STATE"
	m.TTL = m.CreatedAt.UTC().Add(90 * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulCommSendIdempotency.
func (m *SoulCommSendIdempotency) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulCommSendIdempotency.
func (m *SoulCommSendIdempotency) GetSK() string { return m.SK }

// SoulCommSendIdempotencyScope returns the canonical scope for outbound comm idempotency.
func SoulCommSendIdempotencyScope(instanceSlug string, agentID string, idempotencyKey string) string {
	return fmt.Sprintf("%s#%s#%s",
		strings.ToLower(strings.TrimSpace(instanceSlug)),
		strings.ToLower(strings.TrimSpace(agentID)),
		strings.TrimSpace(idempotencyKey),
	)
}

// SoulCommSendIdempotencyPK returns the primary key used to reserve an outbound comm idempotency key.
func SoulCommSendIdempotencyPK(instanceSlug string, agentID string, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(SoulCommSendIdempotencyScope(instanceSlug, agentID, idempotencyKey)))
	return "COMM#IDEMPOTENCY#" + hex.EncodeToString(sum[:])
}

// SoulCommMessageStatusIdempotencyIndexPK returns the GSI partition key for message status rows
// that belong to an outbound comm idempotency scope.
func SoulCommMessageStatusIdempotencyIndexPK(instanceSlug string, agentID string, idempotencyKey string) string {
	return "COMM#IDEMPOTENCY#" + SoulCommSendIdempotencyScope(instanceSlug, agentID, idempotencyKey)
}
