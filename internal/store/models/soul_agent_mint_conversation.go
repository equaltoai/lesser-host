package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulMintConversationStatus* constants define minting conversation states.
const (
	SoulMintConversationStatusInProgress = "in_progress"
	SoulMintConversationStatusCompleted  = "completed"
	SoulMintConversationStatusFailed     = "failed"
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

	Usage          AIUsage `theorydb:"attr:usage" json:"usage,omitempty"`
	ChargedCredits int64   `theorydb:"attr:chargedCredits" json:"charged_credits,omitempty"`

	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
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
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("conversationId", m.ConversationID); err != nil {
		return err
	}
	if err := requireOneOf("status", m.Status, SoulMintConversationStatusInProgress, SoulMintConversationStatusCompleted, SoulMintConversationStatusFailed); err != nil {
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
	if err := requireOneOf("status", m.Status, SoulMintConversationStatusInProgress, SoulMintConversationStatusCompleted, SoulMintConversationStatusFailed); err != nil {
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

	m.PK = fmt.Sprintf("SOUL#AGENT#%s", m.AgentID)
	m.SK = fmt.Sprintf("MINT_CONVERSATION#%s", m.ConversationID)
	return nil
}

// GetPK returns the partition key for SoulAgentMintConversation.
func (m *SoulAgentMintConversation) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulAgentMintConversation.
func (m *SoulAgentMintConversation) GetSK() string { return m.SK }
