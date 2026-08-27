package models

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// SoulMintConversationStatus* constants define minting conversation states.
const (
	SoulMintConversationStatusCreated            = "created"
	SoulMintConversationStatusInProgress         = "in_progress"
	SoulMintConversationStatusAssistantTurnReady = "assistant_turn_ready"
	SoulMintConversationStatusDeclarationReady   = "declaration_ready"
	SoulMintConversationStatusPublished          = "published"
	SoulMintConversationStatusFailed             = "failed"

	// SoulMintConversationStatusCompleted is the legacy terminal status stored by
	// pre-Project-49 records. New durable hosted-genesis responses project it as
	// declaration_ready only when valid produced declarations are present.
	SoulMintConversationStatusCompleted = "completed"
)

// SoulAgentMintConversation stores a minting conversation record for a soul agent.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: MINT_CONVERSATION#{conversationId}
type SoulAgentMintConversation struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	AgentID        string `theorydb:"attr:agentId" json:"agent_id"`
	ConversationID string `theorydb:"attr:conversationId" json:"conversation_id"`

	Model                string `theorydb:"attr:model" json:"model"`
	Messages             string `theorydb:"attr:messages" json:"messages,omitempty"`                          // JSON array of conversation messages
	ProducedDeclarations string `theorydb:"attr:producedDeclarations" json:"produced_declarations,omitempty"` // JSON object of structured output
	Status               string `theorydb:"attr:status" json:"status"`
	StatusReason         string `theorydb:"attr:statusReason" json:"status_reason,omitempty"`
	LatestTurnID         string `theorydb:"attr:latestTurnId" json:"latest_turn_id,omitempty"`
	RequestID            string `theorydb:"attr:requestId" json:"request_id,omitempty"`
	CorrelationID        string `theorydb:"attr:correlationId" json:"correlation_id,omitempty"`
	IdempotencyKey       string `theorydb:"attr:idempotencyKey" json:"idempotency_key,omitempty"`

	Usage          AIUsage `theorydb:"attr:usage" json:"usage,omitempty"`
	ChargedCredits int64   `theorydb:"attr:chargedCredits" json:"charged_credits,omitempty"`

	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
	CompletedAt time.Time `theorydb:"attr:completedAt" json:"completed_at,omitempty"`
}

// TableName returns the database table name for SoulAgentMintConversation.
func (SoulAgentMintConversation) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulAgentMintConversation.
func (m *SoulAgentMintConversation) BeforeCreate() error {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = SoulMintConversationStatusInProgress
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("conversationId", m.ConversationID); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulMintConversationAllowedStatuses()...); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates keys before updating SoulAgentMintConversation.
func (m *SoulAgentMintConversation) BeforeUpdate() error {
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("conversationId", m.ConversationID); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulMintConversationAllowedStatuses()...); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulAgentMintConversation.
func (m *SoulAgentMintConversation) UpdateKeys() error {
	m.AgentID = strings.ToLower(strings.TrimSpace(m.AgentID))
	m.ConversationID = strings.TrimSpace(m.ConversationID)
	m.Model = strings.TrimSpace(m.Model)
	m.Messages = strings.TrimSpace(m.Messages)
	m.ProducedDeclarations = strings.TrimSpace(m.ProducedDeclarations)
	m.Status = strings.ToLower(strings.TrimSpace(m.Status))
	m.StatusReason = strings.TrimSpace(m.StatusReason)
	m.LatestTurnID = strings.TrimSpace(m.LatestTurnID)
	m.RequestID = strings.TrimSpace(m.RequestID)
	m.CorrelationID = strings.TrimSpace(m.CorrelationID)
	m.IdempotencyKey = strings.TrimSpace(m.IdempotencyKey)

	m.PK = fmt.Sprintf("SOUL#AGENT#%s", m.AgentID)
	m.SK = fmt.Sprintf("MINT_CONVERSATION#%s", m.ConversationID)
	return nil
}

// GetPK returns the partition key for SoulAgentMintConversation.
func (m *SoulAgentMintConversation) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulAgentMintConversation.
func (m *SoulAgentMintConversation) GetSK() string { return m.SK }

// SoulMintConversationAllowedStatuses returns statuses accepted by the store
// model. It intentionally includes legacy "completed" for old records.
func SoulMintConversationAllowedStatuses() []string {
	return []string{
		SoulMintConversationStatusCreated,
		SoulMintConversationStatusInProgress,
		SoulMintConversationStatusAssistantTurnReady,
		SoulMintConversationStatusDeclarationReady,
		SoulMintConversationStatusPublished,
		SoulMintConversationStatusFailed,
		SoulMintConversationStatusCompleted,
	}
}

const soulMintConversationBlobPrefix = "b64:"

// EncodeSoulMintConversationBlob stores bounded private transcript/declaration
// payloads as opaque base64 strings so raw JSON is not accidentally projected.
func EncodeSoulMintConversationBlob(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return soulMintConversationBlobPrefix + base64.StdEncoding.EncodeToString([]byte(trimmed))
}

// DecodeSoulMintConversationBlob decodes transcript/declaration payloads. It
// retains compatibility with the pre-M2 base64 prefix format.
func DecodeSoulMintConversationBlob(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, soulMintConversationBlobPrefix) {
		return trimmed
	}
	encoded := strings.TrimPrefix(trimmed, soulMintConversationBlobPrefix)
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return strings.TrimSpace(string(decoded))
	}
	return trimmed
}

const (
	SoulMintConversationIdempotencyStatusProcessing = "processing"
	SoulMintConversationIdempotencyStatusSucceeded  = "succeeded"
	SoulMintConversationIdempotencyStatusFailed     = "failed"
)

// SoulMintConversationIdempotency reserves a caller-provided hosted-genesis
// idempotency key without storing raw browser tokens, raw Instance API keys, or
// raw transcript text.
type SoulMintConversationIdempotency struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	InstanceSlug   string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	RegistrationID string `theorydb:"attr:registrationId" json:"registration_id"`
	AgentID        string `theorydb:"attr:agentId" json:"agent_id"`
	ConversationID string `theorydb:"attr:conversationId" json:"conversation_id"`
	TurnID         string `theorydb:"attr:turnId" json:"turn_id"`
	IdempotencyKey string `theorydb:"attr:idempotencyKey" json:"idempotency_key"`
	RequestHash    string `theorydb:"attr:requestHash" json:"request_hash"`
	RequestID      string `theorydb:"attr:requestId" json:"request_id,omitempty"`
	CorrelationID  string `theorydb:"attr:correlationId" json:"correlation_id,omitempty"`
	Status         string `theorydb:"attr:status" json:"status"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulMintConversationIdempotency.
func (SoulMintConversationIdempotency) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulMintConversationIdempotency.
func (m *SoulMintConversationIdempotency) BeforeCreate() error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = m.CreatedAt
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = SoulMintConversationIdempotencyStatusProcessing
	}
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", m.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("registrationId", m.RegistrationID); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("conversationId", m.ConversationID); err != nil {
		return err
	}
	if err := requireNonEmpty("turnId", m.TurnID); err != nil {
		return err
	}
	if err := requireNonEmpty("idempotencyKey", m.IdempotencyKey); err != nil {
		return err
	}
	if err := requireNonEmpty("requestHash", m.RequestHash); err != nil {
		return err
	}
	return requireOneOf("status", m.Status, SoulMintConversationIdempotencyStatusProcessing, SoulMintConversationIdempotencyStatusSucceeded, SoulMintConversationIdempotencyStatusFailed)
}

// BeforeUpdate updates timestamps and keys before updating SoulMintConversationIdempotency.
func (m *SoulMintConversationIdempotency) BeforeUpdate() error {
	m.UpdatedAt = time.Now().UTC()
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", m.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("registrationId", m.RegistrationID); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("conversationId", m.ConversationID); err != nil {
		return err
	}
	if err := requireNonEmpty("turnId", m.TurnID); err != nil {
		return err
	}
	if err := requireNonEmpty("idempotencyKey", m.IdempotencyKey); err != nil {
		return err
	}
	if err := requireNonEmpty("requestHash", m.RequestHash); err != nil {
		return err
	}
	return requireOneOf("status", m.Status, SoulMintConversationIdempotencyStatusProcessing, SoulMintConversationIdempotencyStatusSucceeded, SoulMintConversationIdempotencyStatusFailed)
}

// UpdateKeys updates the database keys for SoulMintConversationIdempotency.
func (m *SoulMintConversationIdempotency) UpdateKeys() error {
	m.InstanceSlug = strings.ToLower(strings.TrimSpace(m.InstanceSlug))
	m.RegistrationID = strings.TrimSpace(m.RegistrationID)
	m.AgentID = strings.ToLower(strings.TrimSpace(m.AgentID))
	m.ConversationID = strings.TrimSpace(m.ConversationID)
	m.TurnID = strings.TrimSpace(m.TurnID)
	m.IdempotencyKey = strings.TrimSpace(m.IdempotencyKey)
	m.RequestHash = strings.ToLower(strings.TrimSpace(m.RequestHash))
	m.RequestID = strings.TrimSpace(m.RequestID)
	m.CorrelationID = strings.TrimSpace(m.CorrelationID)
	m.Status = strings.ToLower(strings.TrimSpace(m.Status))

	m.PK = SoulMintConversationIdempotencyPK(m.InstanceSlug, m.RegistrationID, m.IdempotencyKey)
	m.SK = stateSortKey
	m.TTL = m.CreatedAt.UTC().Add(7 * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulMintConversationIdempotency.
func (m *SoulMintConversationIdempotency) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulMintConversationIdempotency.
func (m *SoulMintConversationIdempotency) GetSK() string { return m.SK }

// SoulMintConversationIdempotencyPK returns the primary key used to reserve a
// hosted-genesis idempotency key.
func SoulMintConversationIdempotencyPK(instanceSlug string, registrationID string, idempotencyKey string) string {
	scope := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(instanceSlug)),
		strings.TrimSpace(registrationID),
		strings.TrimSpace(idempotencyKey),
	}, "#")
	sum := sha256.Sum256([]byte(scope))
	return "SOUL#MINT_CONVERSATION#IDEMPOTENCY#" + hex.EncodeToString(sum[:])
}
