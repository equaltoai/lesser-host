package commworker

import (
	"fmt"
	"net/mail"
	"strings"
)

// QueueMessageKind* constants define comm-worker SQS message kinds.
const (
	QueueMessageKindInbound = "comm.inbound"
	inboundNotificationType = "communication:inbound"
	inboundChannelEmail     = "email"
	inboundChannelSMS       = "sms"
	inboundChannelVoice     = "voice"
)

// QueueMessage is the SQS payload processed by comm-worker.
type QueueMessage struct {
	Kind         string              `json:"kind"`
	Provider     string              `json:"provider,omitempty"` // migadu|telnyx|test
	Notification InboundNotification `json:"notification"`
}

// InboundNotification matches the communication:inbound notification payload contract.
type InboundNotification struct {
	Type         string             `json:"type"`
	Channel      string             `json:"channel"` // email|sms|voice
	From         InboundParty       `json:"from"`
	To           *InboundParty      `json:"to,omitempty"` // required for routing; schema allows omitting
	Subject      string             `json:"subject,omitempty"`
	Body         string             `json:"body"`
	BodyMimeType string             `json:"bodyMimeType,omitempty"`
	ReceivedAt   string             `json:"receivedAt"`
	MessageID    string             `json:"messageId"`
	InReplyTo    *string            `json:"inReplyTo,omitempty"`
	Attachments  []InboundAttachment `json:"attachments,omitempty"`
}

type InboundParty struct {
	Address     string  `json:"address,omitempty"`
	Number      string  `json:"number,omitempty"`
	SoulAgentID *string `json:"soulAgentId,omitempty"`
	DisplayName string  `json:"displayName,omitempty"`
}

type InboundAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256,omitempty"`
}

func (m *QueueMessage) Validate() error {
	if m == nil {
		return fmt.Errorf("message is nil")
	}
	if strings.TrimSpace(m.Kind) != QueueMessageKindInbound {
		return fmt.Errorf("unsupported kind")
	}
	return m.Notification.Validate()
}

func (n *InboundNotification) Validate() error {
	if n == nil {
		return fmt.Errorf("notification is nil")
	}
	if strings.TrimSpace(n.Type) != inboundNotificationType {
		return fmt.Errorf("invalid notification type")
	}

	channel := strings.ToLower(strings.TrimSpace(n.Channel))
	switch channel {
	case inboundChannelEmail, inboundChannelSMS, inboundChannelVoice:
	default:
		return fmt.Errorf("invalid channel")
	}

	if err := n.From.Validate(); err != nil {
		return fmt.Errorf("invalid from: %w", err)
	}
	if n.To == nil {
		return fmt.Errorf("to is required")
	}
	if err := n.To.Validate(); err != nil {
		return fmt.Errorf("invalid to: %w", err)
	}

	if strings.TrimSpace(n.Body) == "" {
		return fmt.Errorf("body is required")
	}
	if strings.TrimSpace(n.ReceivedAt) == "" {
		return fmt.Errorf("receivedAt is required")
	}
	if strings.TrimSpace(n.MessageID) == "" {
		return fmt.Errorf("messageId is required")
	}

	if channel == inboundChannelEmail && strings.TrimSpace(n.Subject) == "" {
		return fmt.Errorf("subject is required for email")
	}

	return nil
}

func (p *InboundParty) Validate() error {
	if p == nil {
		return fmt.Errorf("party is nil")
	}
	if strings.TrimSpace(p.Address) == "" && strings.TrimSpace(p.Number) == "" {
		return fmt.Errorf("address or number is required")
	}
	if strings.TrimSpace(p.Address) != "" {
		if _, err := mail.ParseAddress(strings.TrimSpace(p.Address)); err != nil {
			return fmt.Errorf("invalid address")
		}
	}
	// Number shape validation is handled deeper in the pipeline (normalization + index lookups).
	return nil
}
