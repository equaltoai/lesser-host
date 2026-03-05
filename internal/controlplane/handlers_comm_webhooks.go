package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/httpx"
)

type legacyInboundEmailWebhookRequest struct {
	To          string  `json:"to"`
	From        string  `json:"from"`
	FromName    string  `json:"fromName,omitempty"`
	Subject     string  `json:"subject"`
	Body        string  `json:"body"`
	BodyMimeType string `json:"bodyMimeType,omitempty"`
	ReceivedAt  string  `json:"receivedAt,omitempty"`
	MessageID   string  `json:"messageId"`
	InReplyTo   *string `json:"inReplyTo,omitempty"`
	Attachments []commworker.InboundAttachment `json:"attachments,omitempty"`
}

type telnyxInboundWebhook struct {
	Data struct {
		EventType  string `json:"event_type"`
		OccurredAt string `json:"occurred_at"`
		Payload    struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			From struct {
				PhoneNumber string `json:"phone_number"`
			} `json:"from"`
			To []struct {
				PhoneNumber string `json:"phone_number"`
			} `json:"to"`
		} `json:"payload"`
	} `json:"data"`
}

func (s *Server) handleCommEmailInboundWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.enqueueCommMessage == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}

	// Prefer the stable communication:inbound payload shape.
	var notif commworker.InboundNotification
	if err := httpx.ParseJSON(ctx, &notif); err == nil && strings.TrimSpace(notif.Type) != "" {
		notif.Channel = "email"
		msg := commworker.QueueMessage{
			Kind:         commworker.QueueMessageKindInbound,
			Provider:     "migadu",
			Notification: notif,
		}
		if err := msg.Validate(); err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
		}
		if err := s.enqueueCommMessage(ctx.Context(), msg); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue"}
		}
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	// Legacy / provider-adapter shape (flat to/from strings).
	var legacy legacyInboundEmailWebhookRequest
	if err := httpx.ParseJSON(ctx, &legacy); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
	}
	legacy.To = strings.TrimSpace(legacy.To)
	legacy.From = strings.TrimSpace(legacy.From)
	legacy.Subject = strings.TrimSpace(legacy.Subject)
	legacy.Body = strings.TrimSpace(legacy.Body)
	legacy.MessageID = strings.TrimSpace(legacy.MessageID)
	legacy.ReceivedAt = strings.TrimSpace(legacy.ReceivedAt)
	if legacy.ReceivedAt == "" {
		legacy.ReceivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	msg := commworker.QueueMessage{
		Kind:     commworker.QueueMessageKindInbound,
		Provider: "migadu",
		Notification: commworker.InboundNotification{
			Type:         "communication:inbound",
			Channel:      "email",
			From:         commworker.InboundParty{Address: legacy.From, DisplayName: strings.TrimSpace(legacy.FromName)},
			To:           &commworker.InboundParty{Address: legacy.To},
			Subject:      legacy.Subject,
			Body:         legacy.Body,
			BodyMimeType: strings.TrimSpace(legacy.BodyMimeType),
			ReceivedAt:   legacy.ReceivedAt,
			MessageID:    legacy.MessageID,
			InReplyTo:    legacy.InReplyTo,
			Attachments:  legacy.Attachments,
		},
	}
	if err := msg.Validate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
	}
	if err := s.enqueueCommMessage(ctx.Context(), msg); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue"}
	}
	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCommSMSInboundWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.enqueueCommMessage == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}

	// Accept already-normalized communication:inbound payloads.
	var notif commworker.InboundNotification
	if err := httpx.ParseJSON(ctx, &notif); err == nil && strings.TrimSpace(notif.Type) != "" {
		notif.Channel = "sms"
		msg := commworker.QueueMessage{
			Kind:         commworker.QueueMessageKindInbound,
			Provider:     "telnyx",
			Notification: notif,
		}
		if err := msg.Validate(); err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
		}
		if err := s.enqueueCommMessage(ctx.Context(), msg); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue"}
		}
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	// Telnyx webhook payload.
	var tel telnyxInboundWebhook
	if err := json.Unmarshal(ctx.Request.Body, &tel); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
	}

	from := strings.TrimSpace(tel.Data.Payload.From.PhoneNumber)
	to := ""
	if len(tel.Data.Payload.To) > 0 {
		to = strings.TrimSpace(tel.Data.Payload.To[0].PhoneNumber)
	}
	body := strings.TrimSpace(tel.Data.Payload.Text)
	messageID := strings.TrimSpace(tel.Data.Payload.ID)
	receivedAt := strings.TrimSpace(tel.Data.OccurredAt)
	if receivedAt == "" {
		receivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	msg := commworker.QueueMessage{
		Kind:     commworker.QueueMessageKindInbound,
		Provider: "telnyx",
		Notification: commworker.InboundNotification{
			Type:       "communication:inbound",
			Channel:    "sms",
			From:       commworker.InboundParty{Number: from},
			To:         &commworker.InboundParty{Number: to},
			Body:       body,
			ReceivedAt: receivedAt,
			MessageID:  messageID,
		},
	}
	if err := msg.Validate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
	}
	if err := s.enqueueCommMessage(ctx.Context(), msg); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue"}
	}
	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCommVoiceInboundWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}
	return apptheory.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleCommVoiceStatusWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}
	return apptheory.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

