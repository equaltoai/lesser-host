package models

import (
	"fmt"
	"strings"
	"time"
)

// SoulCommVoiceInstruction stores the TeXML payload source for an outbound voice message.
//
// Keys:
//
//	PK: COMM#MSG#{messageId}
//	SK: VOICE#INSTRUCTION
type SoulCommVoiceInstruction struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	MessageID string `theorydb:"attr:messageId" json:"message_id"`
	AgentID   string `theorydb:"attr:agentId" json:"agent_id"`
	From      string `theorydb:"attr:from" json:"from"`
	To        string `theorydb:"attr:to" json:"to"`
	Body      string `theorydb:"attr:body" json:"body"`
	Voice     string `theorydb:"attr:voice,omitempty" json:"voice,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
}

// TableName returns the database table name for SoulCommVoiceInstruction.
func (SoulCommVoiceInstruction) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating SoulCommVoiceInstruction.
func (m *SoulCommVoiceInstruction) BeforeCreate() error {
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", m.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("from", m.From); err != nil {
		return err
	}
	if err := requireNonEmpty("to", m.To); err != nil {
		return err
	}
	if err := requireNonEmpty("body", m.Body); err != nil {
		return err
	}
	return nil
}

// BeforeUpdate updates timestamps and keys before updating SoulCommVoiceInstruction.
func (m *SoulCommVoiceInstruction) BeforeUpdate() error {
	m.UpdatedAt = time.Now().UTC()
	if err := m.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("messageId", m.MessageID); err != nil {
		return err
	}
	return nil
}

// UpdateKeys updates the database keys for SoulCommVoiceInstruction.
func (m *SoulCommVoiceInstruction) UpdateKeys() error {
	m.MessageID = strings.TrimSpace(m.MessageID)
	m.AgentID = strings.ToLower(strings.TrimSpace(m.AgentID))
	m.From = strings.TrimSpace(m.From)
	m.To = strings.TrimSpace(m.To)
	m.Body = strings.TrimSpace(m.Body)
	m.Voice = strings.TrimSpace(m.Voice)

	m.PK = fmt.Sprintf("COMM#MSG#%s", m.MessageID)
	m.SK = "VOICE#INSTRUCTION"
	m.TTL = m.CreatedAt.UTC().Add(24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for SoulCommVoiceInstruction.
func (m *SoulCommVoiceInstruction) GetPK() string { return m.PK }

// GetSK returns the sort key for SoulCommVoiceInstruction.
func (m *SoulCommVoiceInstruction) GetSK() string { return m.SK }
