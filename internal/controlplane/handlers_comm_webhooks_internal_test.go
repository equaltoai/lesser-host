package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/config"
)

func TestHandleCommEmailInboundWebhook_EnqueuesNormalizedPayload(t *testing.T) {
	t.Parallel()

	var got *commworker.QueueMessage
	s := &Server{
		cfg: config.Config{SoulEnabled: true},
		enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
			cp := msg
			got = &cp
			return nil
		},
	}

	body, _ := json.Marshal(commworker.InboundNotification{
		Type:    "communication:inbound",
		Channel: "email",
		From: commworker.InboundParty{
			Address:     "alice@example.com",
			DisplayName: "Alice",
		},
		To:         &commworker.InboundParty{Address: "agent-bob@lessersoul.ai"},
		Subject:    "Hello",
		Body:       "Test",
		ReceivedAt: "2026-03-04T12:00:00Z",
		MessageID:  "comm-msg-001",
	})

	ctx := &apptheory.Context{
		RequestID: "r-webhook-email-1",
		Request:   apptheory.Request{Body: body},
	}

	resp, err := s.handleCommEmailInboundWebhook(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if got == nil || got.Provider != "migadu" || got.Kind != commworker.QueueMessageKindInbound {
		t.Fatalf("expected migadu comm.inbound message, got %#v", got)
	}
	if got.Notification.Channel != "email" || got.Notification.Type != "communication:inbound" {
		t.Fatalf("unexpected notification shape: %#v", got.Notification)
	}
}

func TestHandleCommEmailInboundWebhook_EnqueuesLegacyFlatPayload(t *testing.T) {
	t.Parallel()

	var got *commworker.QueueMessage
	s := &Server{
		cfg: config.Config{SoulEnabled: true},
		enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
			cp := msg
			got = &cp
			return nil
		},
	}

	body, _ := json.Marshal(map[string]any{
		"to":        "agent-bob@lessersoul.ai",
		"from":      "alice@example.com",
		"fromName":  "Alice",
		"subject":   "Hello",
		"body":      "Test",
		"receivedAt": "2026-03-04T12:00:00Z",
		"messageId": "comm-msg-001",
	})

	ctx := &apptheory.Context{
		RequestID: "r-webhook-email-2",
		Request:   apptheory.Request{Body: body},
	}

	resp, err := s.handleCommEmailInboundWebhook(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if got == nil || got.Provider != "migadu" || got.Kind != commworker.QueueMessageKindInbound {
		t.Fatalf("expected migadu comm.inbound message, got %#v", got)
	}
	if got.Notification.To == nil || got.Notification.To.Address != "agent-bob@lessersoul.ai" {
		t.Fatalf("expected to address, got %#v", got.Notification.To)
	}
	if got.Notification.From.Address != "alice@example.com" {
		t.Fatalf("expected from address, got %#v", got.Notification.From)
	}
}

func TestHandleCommSMSInboundWebhook_EnqueuesTelnyxPayload(t *testing.T) {
	t.Parallel()

	var got *commworker.QueueMessage
	s := &Server{
		cfg: config.Config{SoulEnabled: true},
		enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
			cp := msg
			got = &cp
			return nil
		},
	}

	body, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"event_type":  "message.received",
			"occurred_at": "2026-03-04T12:00:00Z",
			"payload": map[string]any{
				"id":   "telnyx-msg-1",
				"text": "Hello",
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   []map[string]any{{"phone_number": "+15550143"}},
			},
		},
	})

	ctx := &apptheory.Context{
		RequestID: "r-webhook-sms-1",
		Request:   apptheory.Request{Body: body},
	}

	resp, err := s.handleCommSMSInboundWebhook(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if got == nil || got.Provider != "telnyx" || got.Kind != commworker.QueueMessageKindInbound {
		t.Fatalf("expected telnyx comm.inbound message, got %#v", got)
	}
	if got.Notification.Channel != "sms" || got.Notification.MessageID != "telnyx-msg-1" {
		t.Fatalf("unexpected notification: %#v", got.Notification)
	}
	if got.Notification.To == nil || got.Notification.To.Number != "+15550143" {
		t.Fatalf("unexpected to: %#v", got.Notification.To)
	}
}

