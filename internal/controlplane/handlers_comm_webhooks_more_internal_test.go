package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type commWebhookTestDB struct {
	db        *ttmocks.MockExtendedDB
	qPhone    *ttmocks.MockQuery
	qIdentity *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
	qBudget   *ttmocks.MockQuery
}

func newCommWebhookTestDB() commWebhookTestDB {
	db, queries := newTestDBWithModelQueries(
		"*models.SoulPhoneAgentIndex",
		"*models.SoulAgentIdentity",
		"*models.Domain",
		"*models.Instance",
		"*models.InstanceBudgetMonth",
	)
	return commWebhookTestDB{
		db:        db,
		qPhone:    queries[0],
		qIdentity: queries[1],
		qDomain:   queries[2],
		qInstance: queries[3],
		qBudget:   queries[4],
	}
}

const (
	commWebhookReceivedAt       = "2026-03-05T12:00:00Z"
	commWebhookFailedToEnqueue  = "failed to enqueue"
	commWebhookCallHangup       = "call.hangup"
	commWebhookJSONContentType  = "application/json"
	commWebhookSecretSSMParam   = "/test/comm/webhook"
	commWebhookTestInstanceSlug = "inst1"
	commWebhookTestSecret       = "test-webhook-secret"
)

type commWebhookHandler func(*Server, *apptheory.Context) (*apptheory.Response, error)

func TestTelnyxSMSWebhookQueueMessage_NormalizesPayload(t *testing.T) {
	t.Parallel()

	var tel telnyxInboundWebhook
	tel.Data.OccurredAt = "2026-03-05T12:00:00Z"
	tel.Data.Payload.ID = " msg-1 "
	tel.Data.Payload.Text = " sms webhook body "
	tel.Data.Payload.From.PhoneNumber = " +15550142 "
	tel.Data.Payload.To = append(tel.Data.Payload.To, struct {
		PhoneNumber string `json:"phone_number"`
	}{PhoneNumber: " +15550143 "})

	msg := telnyxSMSWebhookQueueMessage(tel)
	if msg.Provider != commDeliveryProviderTelnyx || msg.Kind != commworker.QueueMessageKindInbound {
		t.Fatalf("unexpected queue metadata: %#v", msg)
	}
	if msg.Notification.Channel != commChannelSMS || msg.Notification.MessageID != "msg-1" || msg.Notification.Body != "sms webhook body" {
		t.Fatalf("unexpected notification payload: %#v", msg.Notification)
	}
	if msg.Notification.From.Number != "+15550142" || msg.Notification.To == nil || msg.Notification.To.Number != "+15550143" {
		t.Fatalf("unexpected telnyx parties: %#v", msg.Notification)
	}
}

func TestExtractTelnyxVoiceFields(t *testing.T) {
	t.Parallel()

	if from, to, callID, occurredAt, duration := extractTelnyxVoiceFields(nil); from != "" || to != "" || callID != "" || occurredAt != "" || duration != 0 {
		t.Fatalf("expected nil payload to produce empty fields")
	}

	tel := &telnyxVoiceWebhook{}
	tel.Data.EventType = commWebhookCallHangup
	tel.Data.OccurredAt = commWebhookReceivedAt
	tel.Data.Payload = map[string]any{
		"from":             map[string]any{"phoneNumber": " +15550142 "},
		"to":               map[string]any{"number": " +15550143 "},
		"call_session_id":  " call-1 ",
		"duration_seconds": json.Number("61"),
	}
	from, to, callID, occurredAt, duration := extractTelnyxVoiceFields(tel)
	if from != "+15550142" || to != "+15550143" || callID != "call-1" || occurredAt != commWebhookReceivedAt || duration != 61 {
		t.Fatalf("unexpected extracted voice fields: from=%q to=%q callID=%q occurredAt=%q duration=%d", from, to, callID, occurredAt, duration)
	}
}

func TestBuildTelnyxVoiceNotificationFallback(t *testing.T) {
	t.Parallel()

	notif, to, callID, duration := buildTelnyxVoiceNotification([]byte(" "), &telnyxVoiceWebhook{Data: struct {
		EventType  string         `json:"event_type"`
		OccurredAt string         `json:"occurred_at"`
		Payload    map[string]any `json:"payload"`
	}{EventType: commWebhookCallHangup, Payload: map[string]any{"duration": float64(2)}}})
	if notif.Body != commWebhookCallHangup || notif.BodyMimeType != commWebhookJSONContentType || notif.Channel != commChannelVoice {
		t.Fatalf("unexpected fallback voice notification: %#v", notif)
	}
	if to != "" || callID != commWebhookCallHangup || duration != 2 {
		t.Fatalf("unexpected fallback voice fields: to=%q callID=%q duration=%d", to, callID, duration)
	}
}

func TestTelnyxPrimitivePayloadReaders(t *testing.T) {
	t.Parallel()

	if got := readTelnyxPhoneValue(" +15550142 "); got != "+15550142" {
		t.Fatalf("unexpected string phone value %q", got)
	}
	if got := readTelnyxPhoneValue(map[string]any{"phone_number": " +15550143 "}); got != "+15550143" {
		t.Fatalf("unexpected phone_number value %q", got)
	}
	if got := readTelnyxPhoneValue(map[string]any{"number": " +15550144 "}); got != "+15550144" {
		t.Fatalf("unexpected number value %q", got)
	}
	if got := readTelnyxPhonePayload(map[string]any{"to": map[string]any{"phoneNumber": " +15550145 "}}, "to"); got != "+15550145" {
		t.Fatalf("unexpected payload phone value %q", got)
	}

	payload := map[string]any{"string": "12", "bad": "nope", "int": 3, "int64": int64(4), "float": float64(5), "json": json.Number("6")}
	for key, want := range map[string]int64{"string": 12, "int": 3, "int64": 4, "float": 5, "json": 6} {
		if got := readTelnyxInt64Payload(payload, key); got != want {
			t.Fatalf("readTelnyxInt64Payload(%q)=%d want %d", key, got, want)
		}
	}
	if got := readTelnyxInt64Payload(payload, "bad", "missing"); got != 0 {
		t.Fatalf("expected bad/missing integer fields to return zero, got %d", got)
	}
	if value, ok := coerceTelnyxInt64([]string{"bad"}); ok || value != 0 {
		t.Fatalf("expected unsupported integer type to fail, got value=%d ok=%v", value, ok)
	}
}

func TestHandleCommEmailInboundWebhook_Disabled(t *testing.T) {
	t.Parallel()

	assertCommWebhookDisabled(t, func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
		return s.handleCommEmailInboundWebhook(ctx)
	})
}

func TestHandleCommEmailInboundWebhook_MissingQueueReturnsInternal(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulEnabled: true}}
	_, err := s.handleCommEmailInboundWebhook(&apptheory.Context{})
	requireWebhookAppError(t, err, appErrCodeInternal, "internal error")
}

func TestHandleCommEmailInboundWebhook_RejectsUnsignedPayload(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for unsigned payload: %#v", msg)
		return nil
	})
	body := marshalCommWebhookBody(t, commworker.InboundNotification{
		Type:      "communication:inbound",
		Channel:   "email",
		From:      commworker.InboundParty{Address: "alice@example.com"},
		To:        &commworker.InboundParty{Address: "agent@lessersoul.ai"},
		MessageID: "msg-unsigned",
	})
	_, err := s.handleCommEmailInboundWebhook(&apptheory.Context{Request: apptheory.Request{Body: body}})
	requireWebhookAppError(t, err, "comm.unauthorized", "unauthorized")
}

func TestHandleCommEmailInboundWebhook_InvalidNormalizedPayloadRejected(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for invalid payload")
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"type":       "communication:inbound",
		"from":       map[string]any{"address": "alice@example.com"},
		"body":       "legacy webhook body",
		"receivedAt": commWebhookReceivedAt,
		"messageId":  "msg-1",
	})
	_, err := s.handleCommEmailInboundWebhook(signedCommWebhookCtx(t, body))
	requireWebhookAppError(t, err, appErrCodeBadRequest, "invalid webhook payload")
}

func TestHandleCommEmailInboundWebhook_LegacyPayloadDefaultsReceivedAt(t *testing.T) {
	t.Parallel()

	var got *commworker.QueueMessage
	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		cp := msg
		got = &cp
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"to":        "agent-bob@lessersoul.ai",
		"from":      "alice@example.com",
		"subject":   "Hello",
		"body":      "Test",
		"messageId": "comm-msg-legacy",
	})
	resp, err := s.handleCommEmailInboundWebhook(signedCommWebhookCtx(t, body))
	requireCommWebhookOK(t, resp, err)
	if got == nil {
		t.Fatalf("expected queued message")
	}
	assertCommWebhookReceivedAt(t, got.Notification.ReceivedAt)
}

func TestHandleCommEmailInboundWebhook_EnqueueFailureReturnsInternal(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		return context.DeadlineExceeded
	})
	body := marshalCommWebhookBody(t, commworker.InboundNotification{
		Type:       "communication:inbound",
		Channel:    "email",
		From:       commworker.InboundParty{Address: "alice@example.com"},
		To:         &commworker.InboundParty{Address: "agent@lessersoul.ai"},
		Subject:    "Hello",
		Body:       "Test",
		ReceivedAt: commWebhookReceivedAt,
		MessageID:  "msg-2",
	})
	_, err := s.handleCommEmailInboundWebhook(signedCommWebhookCtx(t, body))
	requireWebhookAppError(t, err, appErrCodeInternal, commWebhookFailedToEnqueue)
}

func TestHandleCommSMSInboundWebhook_Disabled(t *testing.T) {
	t.Parallel()

	assertCommWebhookDisabled(t, func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
		return s.handleCommSMSInboundWebhook(ctx)
	})
}

func TestHandleCommSMSInboundWebhook_NormalizesPayload(t *testing.T) {
	t.Parallel()

	assertNormalizedCommWebhook(t, func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
		return s.handleCommSMSInboundWebhook(ctx)
	}, commworker.InboundNotification{
		Type:       "communication:inbound",
		Channel:    "voice",
		From:       commworker.InboundParty{Number: "+15550142"},
		To:         &commworker.InboundParty{Number: "+15550143"},
		Body:       "Hello",
		ReceivedAt: commWebhookReceivedAt,
		MessageID:  "sms-msg-1",
	}, "sms")
}

func TestHandleCommSMSInboundWebhook_AcceptsValidTelnyxSignature(t *testing.T) {
	t.Parallel()

	var got *commworker.QueueMessage
	s, privateKey := newTelnyxWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		cp := msg
		got = &cp
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": "message.received",
			"payload": map[string]any{
				"id":   "telnyx-msg-signed",
				"text": "Hello",
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   []map[string]any{{"phone_number": "+15550143"}},
			},
		},
	})
	resp, err := s.handleCommSMSInboundWebhook(&apptheory.Context{Request: signedTelnyxWebhookRequest(t, privateKey, body, time.Now())})
	requireCommWebhookOK(t, resp, err)
	if got == nil || got.Notification.MessageID != "telnyx-msg-signed" {
		t.Fatalf("expected signed Telnyx notification, got %#v", got)
	}
}

func TestHandleCommSMSInboundWebhook_RejectsInvalidTelnyxSignature(t *testing.T) {
	t.Parallel()

	s, privateKey := newTelnyxWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for invalid signature: %#v", msg)
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": "message.received",
			"payload": map[string]any{
				"id":   "telnyx-msg-invalid",
				"text": "Hello",
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   []map[string]any{{"phone_number": "+15550143"}},
			},
		},
	})
	req := signedTelnyxWebhookRequest(t, privateKey, body, time.Now())
	req.Body = append([]byte(`{"tampered":true}`), body...)
	_, err := s.handleCommSMSInboundWebhook(&apptheory.Context{Request: req})
	requireWebhookAppError(t, err, "comm.unauthorized", "unauthorized")
}

func TestHandleCommSMSInboundWebhook_RejectsStaleSignature(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for stale signature: %#v", msg)
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": "message.received",
			"payload": map[string]any{
				"id":   "telnyx-msg-stale",
				"text": "Hello",
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   []map[string]any{{"phone_number": "+15550143"}},
			},
		},
	})
	_, err := s.handleCommSMSInboundWebhook(&apptheory.Context{Request: signedCommWebhookRequestAt(t, body, time.Now().Add(-10*time.Minute))})
	requireWebhookAppError(t, err, "comm.unauthorized", "unauthorized")
}

func TestHandleCommSMSInboundWebhook_InvalidPayloadRejected(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error { return nil })
	_, err := s.handleCommSMSInboundWebhook(signedCommWebhookCtx(t, []byte("{")))
	requireWebhookAppError(t, err, appErrCodeBadRequest, "invalid webhook payload")
}

func TestHandleCommSMSInboundWebhook_EnqueueFailureReturnsInternal(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		return context.DeadlineExceeded
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": "message.received",
			"payload": map[string]any{
				"id":   "telnyx-msg-2",
				"text": "Hello",
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   []map[string]any{{"phone_number": "+15550143"}},
			},
		},
	})
	_, err := s.handleCommSMSInboundWebhook(signedCommWebhookCtx(t, body))
	requireWebhookAppError(t, err, appErrCodeInternal, commWebhookFailedToEnqueue)
}

func TestHandleCommSMSInboundWebhook_SkipsNonInboundTelnyxEvents(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for non-inbound Telnyx events: %#v", msg)
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": "message.sent",
			"payload": map[string]any{
				"id":   "telnyx-msg-2",
				"text": "Hello",
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   []map[string]any{{"phone_number": "+15550143"}},
			},
		},
	})

	resp, err := s.handleCommSMSInboundWebhook(signedCommWebhookCtx(t, body))
	requireCommWebhookOK(t, resp, err)

	var got map[string]any
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["skipped"] != "message.sent" {
		t.Fatalf("expected skipped event type, got %#v", got)
	}
}

func TestHandleCommVoiceInboundWebhook_NormalizesPayload(t *testing.T) {
	t.Parallel()

	assertNormalizedCommWebhook(t, func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
		return s.handleCommVoiceInboundWebhook(ctx)
	}, commworker.InboundNotification{
		Type:       "communication:inbound",
		Channel:    "sms",
		From:       commworker.InboundParty{Number: "+15550142"},
		To:         &commworker.InboundParty{Number: "+15550143"},
		Body:       "Connected",
		ReceivedAt: commWebhookReceivedAt,
		MessageID:  "voice-msg-1",
	}, "voice")
}

func TestHandleCommVoiceInboundWebhook_RejectsUnsignedBeforeEnqueue(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for unsigned voice inbound payload: %#v", msg)
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": commWebhookCallHangup,
			"payload": map[string]any{
				"from":             map[string]any{"phone_number": "+15550142"},
				"to":               map[string]any{"phone_number": "+15550143"},
				"call_session_id":  "call-unsigned",
				"duration_seconds": 61,
			},
		},
	})
	_, err := s.handleCommVoiceInboundWebhook(&apptheory.Context{Request: apptheory.Request{Body: body}})
	requireWebhookAppError(t, err, "comm.unauthorized", "unauthorized")
}

func TestHandleCommVoiceInboundWebhook_TelnyxFallbackCallIDAndReceivedAt(t *testing.T) {
	t.Parallel()

	var got *commworker.QueueMessage
	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		cp := msg
		got = &cp
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": commWebhookCallHangup,
			"payload": map[string]any{
				"from": map[string]any{"phone_number": "+15550142"},
				"to":   map[string]any{"phone_number": "+15550143"},
			},
		},
	})
	resp, err := s.handleCommVoiceInboundWebhook(signedCommWebhookCtx(t, body))
	requireCommWebhookOK(t, resp, err)
	if got == nil {
		t.Fatalf("expected queued message")
	}
	if got.Notification.MessageID != commWebhookCallHangup {
		t.Fatalf("expected fallback message id from event type, got %#v", got.Notification)
	}
	assertCommWebhookReceivedAt(t, got.Notification.ReceivedAt)
}

func TestHandleCommVoiceInboundWebhook_InvalidTelnyxPayloadRejected(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error { return nil })
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": commWebhookCallHangup,
			"payload":    map[string]any{},
		},
	})
	_, err := s.handleCommVoiceInboundWebhook(signedCommWebhookCtx(t, body))
	requireWebhookAppError(t, err, appErrCodeBadRequest, "invalid webhook payload")
}

func TestHandleCommVoiceInboundWebhook_EnqueueFailureReturnsInternal(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		return context.DeadlineExceeded
	})
	body := marshalCommWebhookBody(t, commworker.InboundNotification{
		Type:       "communication:inbound",
		Channel:    "voice",
		From:       commworker.InboundParty{Number: "+15550142"},
		To:         &commworker.InboundParty{Number: "+15550143"},
		Body:       "Connected",
		ReceivedAt: commWebhookReceivedAt,
		MessageID:  "voice-msg-2",
	})
	_, err := s.handleCommVoiceInboundWebhook(signedCommWebhookCtx(t, body))
	requireWebhookAppError(t, err, appErrCodeInternal, commWebhookFailedToEnqueue)
}

func TestHandleCommVoiceStatusWebhook_DelegatesAndDisabled(t *testing.T) {
	t.Parallel()

	t.Run("disabled", func(t *testing.T) {
		s := &Server{
			cfg: config.Config{SoulEnabled: false},
			enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
				return nil
			},
		}
		resp, err := s.handleCommVoiceStatusWebhook(&apptheory.Context{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.Status)
		}
	})

	t.Run("delegates to inbound handler", func(t *testing.T) {
		var got *commworker.QueueMessage
		s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
			cp := msg
			got = &cp
			return nil
		})
		body, _ := json.Marshal(commworker.InboundNotification{
			Type:       "communication:inbound",
			Channel:    "voice",
			From:       commworker.InboundParty{Number: "+15550142"},
			To:         &commworker.InboundParty{Number: "+15550143"},
			Body:       "Connected",
			ReceivedAt: commWebhookReceivedAt,
			MessageID:  "voice-status-1",
		})
		resp, err := s.handleCommVoiceStatusWebhook(signedCommWebhookCtx(t, body))
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.Status)
		}
		if got == nil || got.Notification.Channel != "voice" {
			t.Fatalf("expected delegated voice notification, got %#v", got)
		}
	})
}

func TestHandleCommVoiceStatusWebhook_RejectsUnsignedBeforeBilling(t *testing.T) {
	t.Parallel()

	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		t.Fatalf("enqueue should not be called for unsigned voice status payload: %#v", msg)
		return nil
	})
	body := marshalCommWebhookBody(t, map[string]any{
		"data": map[string]any{
			"event_type": commWebhookCallHangup,
			"payload": map[string]any{
				"from":             map[string]any{"phone_number": "+15550142"},
				"to":               map[string]any{"phone_number": "+15550143"},
				"call_session_id":  "call-unsigned-status",
				"duration_seconds": 61,
			},
		},
	})
	_, err := s.handleCommVoiceStatusWebhook(&apptheory.Context{
		Params:  map[string]string{"messageId": "comm-msg-unsigned"},
		Request: apptheory.Request{Body: body},
	})
	requireWebhookAppError(t, err, "comm.unauthorized", "unauthorized")
}

func TestExtractTelnyxVoiceFields_CoversAlternateShapes(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		from, to, callID, occurredAt, duration := extractTelnyxVoiceFields(nil)
		if from != "" || to != "" || callID != "" || occurredAt != "" || duration != 0 {
			t.Fatalf("expected zero values, got from=%q to=%q callID=%q occurredAt=%q duration=%d", from, to, callID, occurredAt, duration)
		}
	})

	t.Run("alternate keys and numeric parsing", func(t *testing.T) {
		tel := &telnyxVoiceWebhook{}
		tel.Data.EventType = "call.answered"
		tel.Data.OccurredAt = commWebhookReceivedAt
		tel.Data.Payload = map[string]any{
			"from":            "+15550142",
			"to":              map[string]any{"phoneNumber": "+15550143"},
			"call_session_id": "call-session-1",
			"durationSeconds": json.Number("61"),
		}
		from, to, callID, occurredAt, duration := extractTelnyxVoiceFields(tel)
		if from != "+15550142" || to != "+15550143" || callID != "call-session-1" || occurredAt != commWebhookReceivedAt || duration != 61 {
			t.Fatalf("unexpected extracted fields: from=%q to=%q callID=%q occurredAt=%q duration=%d", from, to, callID, occurredAt, duration)
		}
	})

	t.Run("fallback event type and string duration", func(t *testing.T) {
		tel := &telnyxVoiceWebhook{}
		tel.Data.EventType = commWebhookCallHangup
		tel.Data.Payload = map[string]any{
			"from":     map[string]any{"number": "+15550144"},
			"to":       map[string]any{"phone_number": "+15550145"},
			"duration": "45",
		}
		_, _, callID, _, duration := extractTelnyxVoiceFields(tel)
		if callID != "call.hangup" || duration != 45 {
			t.Fatalf("expected event type call id and parsed string duration, got callID=%q duration=%d", callID, duration)
		}
	})
}

func TestMeterTelnyxVoiceCall_StoreNotInitializedReturnsError(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err == nil || err.Error() != "store not initialized" {
		t.Fatalf("expected store not initialized error, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_InvalidInputsAreIgnored(t *testing.T) {
	t.Parallel()

	s := &Server{store: store.New(ttmocks.NewMockExtendedDB())}
	ctx := &apptheory.Context{}
	if err := s.meterTelnyxVoiceCall(ctx, "", "call-1", 60); err != nil {
		t.Fatalf("expected nil for blank phone, got %v", err)
	}
	if err := s.meterTelnyxVoiceCall(ctx, "+15550142", "", 60); err != nil {
		t.Fatalf("expected nil for blank call id, got %v", err)
	}
	if err := s.meterTelnyxVoiceCall(ctx, "+15550142", "call-1", 0); err != nil {
		t.Fatalf("expected nil for non-positive duration, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_PhoneIndexNotFound(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qPhone.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(assertNotFound()).Once()
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+1 (555) 0142", "call-1", 60); err != nil {
		t.Fatalf("expected nil when phone index missing, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_BlankAgentIDIgnored(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db)}
	expectCommWebhookPhoneAgent(t, tdb.qPhone, " ")
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err != nil {
		t.Fatalf("expected nil when agent id blank, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_IdentityMissing(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db)}
	expectCommWebhookPhoneAgent(t, tdb.qPhone, "0xabc")
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(assertNotFound()).Once()
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err != nil {
		t.Fatalf("expected nil when identity missing, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_DomainMissing(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db)}
	expectCommWebhookPhoneAgent(t, tdb.qPhone, "0xabc")
	expectCommWebhookIdentity(t, tdb.qIdentity, "0xabc", "example.com")
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(assertNotFound()).Once()
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err != nil {
		t.Fatalf("expected nil when domain missing, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_BlankInstanceSlugIgnored(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db)}
	expectCommWebhookPhoneAgent(t, tdb.qPhone, "0xabc")
	expectCommWebhookIdentity(t, tdb.qIdentity, "0xabc", "example.com")
	expectCommWebhookDomain(t, tdb.qDomain, " ")
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err != nil {
		t.Fatalf("expected nil when instance slug blank, got %v", err)
	}
}

func TestMeterTelnyxVoiceCall_BudgetMissing(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db)}
	expectCommWebhookPhoneAgent(t, tdb.qPhone, "0xabc")
	expectCommWebhookIdentity(t, tdb.qIdentity, "0xabc", "example.com")
	expectCommWebhookDomain(t, tdb.qDomain, commWebhookTestInstanceSlug)
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(assertNotFound()).Once()
	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err != nil {
		t.Fatalf("expected nil when budget missing, got %v", err)
	}
}

func TestLoadTelnyxVoiceInstanceSlug_AllowsManagedStageAlias(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{Stage: "lab"}}
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(assertNotFound()).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "example.com",
			InstanceSlug:       commWebhookTestInstanceSlug,
			Type:               models.DomainTypePrimary,
			Status:             models.DomainStatusVerified,
			VerificationMethod: "managed",
		}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: commWebhookTestInstanceSlug, HostedBaseDomain: "example.com"}
	}).Once()

	instanceSlug, err := s.loadTelnyxVoiceInstanceSlug(&apptheory.Context{}, &models.SoulAgentIdentity{Domain: "dev.example.com"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if instanceSlug != commWebhookTestInstanceSlug {
		t.Fatalf("unexpected instance slug: %q", instanceSlug)
	}
}

func TestMeterTelnyxVoiceCall_CreatesLedgerAndBudgetUpdate(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	tdb.qPhone.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulPhoneAgentIndex](t, args, 0)
		*dest = models.SoulPhoneAgentIndex{Phone: "+15550142", AgentID: "0xabc"}
	}).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: "0xabc", Domain: "example.com"}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: commWebhookTestInstanceSlug}
	}).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: commWebhookTestInstanceSlug, IncludedCredits: 12, UsedCredits: 5}
	}).Once()

	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.UsageLedgerEntry](t, args, 0)
		if entry.InstanceSlug != commWebhookTestInstanceSlug {
			t.Fatalf("expected instance slug %s, got %q", commWebhookTestInstanceSlug, entry.InstanceSlug)
		}
		if entry.Module != "comm.voice.call" || entry.Target != "call-1" {
			t.Fatalf("unexpected ledger routing fields: %+v", entry)
		}
		if entry.RequestedCredits != 16 || entry.DebitedCredits != 16 {
			t.Fatalf("expected 16 credits for 2 billable minutes, got requested=%d debited=%d", entry.RequestedCredits, entry.DebitedCredits)
		}
		if entry.IncludedDebitedCredits != 7 || entry.OverageDebitedCredits != 9 {
			t.Fatalf("expected mixed included/overage split 7/9, got %d/%d", entry.IncludedDebitedCredits, entry.OverageDebitedCredits)
		}
		if entry.BillingType != models.BillingTypeMixed {
			t.Fatalf("expected mixed billing type, got %q", entry.BillingType)
		}
		if !strings.HasPrefix(entry.ActorURI, "soul_agent:0xabc") {
			t.Fatalf("expected actor uri for agent, got %q", entry.ActorURI)
		}
	})
	tb.On("UpdateWithBuilder", mock.Anything, mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		budget := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		if budget.InstanceSlug != commWebhookTestInstanceSlug {
			t.Fatalf("expected budget update for %s, got %q", commWebhookTestInstanceSlug, budget.InstanceSlug)
		}
		if strings.TrimSpace(budget.Month) == "" {
			t.Fatalf("expected budget month to be populated")
		}
	})

	if err := s.meterTelnyxVoiceCall(ctx, "+1 (555) 0142", "call-1", 61); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestMeterTelnyxVoiceCall_DuplicateLedgerIsIdempotent(t *testing.T) {
	t.Parallel()

	tdb := newCommWebhookTestDB()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

	s := &Server{store: store.New(tdb.db)}
	expectCommWebhookPhoneAgent(t, tdb.qPhone, "0xabc")
	expectCommWebhookIdentity(t, tdb.qIdentity, "0xabc", "example.com")
	expectCommWebhookDomain(t, tdb.qDomain, commWebhookTestInstanceSlug)
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: commWebhookTestInstanceSlug, IncludedCredits: 12, UsedCredits: 5}
	}).Once()
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("UpdateWithBuilder", mock.Anything, mock.Anything, mock.Anything).Return(tb).Once()

	if err := s.meterTelnyxVoiceCall(&apptheory.Context{}, "+15550142", "call-1", 60); err != nil {
		t.Fatalf("expected duplicate ledger to be idempotent, got %v", err)
	}
}

func requireAppError(t *testing.T, err error) *apptheory.AppError {
	t.Helper()
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T (%v)", err, err)
	}
	return appErr
}

func assertNotFound() error {
	return theoryErrors.ErrItemNotFound
}

func newCommWebhookServer(enqueue func(context.Context, commworker.QueueMessage) error) *Server {
	return &Server{
		cfg:                config.Config{SoulEnabled: true, CommWebhookSharedSecretSSMParam: commWebhookSecretSSMParam},
		enqueueCommMessage: enqueue,
		ssmGetParameter: func(_ context.Context, name string) (string, error) {
			if name != commWebhookSecretSSMParam {
				return "", context.Canceled
			}
			return commWebhookTestSecret, nil
		},
	}
}

func newTelnyxWebhookServer(enqueue func(context.Context, commworker.QueueMessage) error) (*Server, ed25519.PrivateKey) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	s := newCommWebhookServer(enqueue)
	s.cfg.TelnyxWebhookPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	return s, privateKey
}

func signedCommWebhookCtx(t *testing.T, body []byte) *apptheory.Context {
	t.Helper()
	return &apptheory.Context{Request: signedCommWebhookRequest(t, body)}
}

func signedCommWebhookRequest(t *testing.T, body []byte) apptheory.Request {
	t.Helper()
	return signedCommWebhookRequestAt(t, body, time.Now())
}

func signedCommWebhookRequestAt(t *testing.T, body []byte, at time.Time) apptheory.Request {
	t.Helper()
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(commWebhookTestSecret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return apptheory.Request{
		Body: body,
		Headers: map[string][]string{
			"x-lesser-host-timestamp": {ts},
			"x-lesser-host-signature": {"sha256=" + hex.EncodeToString(mac.Sum(nil))},
		},
	}
}

func signedTelnyxWebhookRequest(t *testing.T, privateKey ed25519.PrivateKey, body []byte, at time.Time) apptheory.Request {
	t.Helper()
	ts := strconv.FormatInt(at.Unix(), 10)
	payload := append([]byte(ts+"|"), body...)
	return apptheory.Request{
		Body: body,
		Headers: map[string][]string{
			"telnyx-timestamp":         {ts},
			"telnyx-signature-ed25519": {base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))},
			"x-lesser-host-timestamp":  {"ignored"},
			"x-lesser-host-signature":  {"ignored"},
		},
	}
}

func assertCommWebhookDisabled(t *testing.T, handler commWebhookHandler) {
	t.Helper()

	s := &Server{
		cfg: config.Config{SoulEnabled: false},
		enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
			return nil
		},
	}
	resp, err := handler(s, &apptheory.Context{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response")
	}
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Status)
	}
}

func assertNormalizedCommWebhook(t *testing.T, handler commWebhookHandler, notification commworker.InboundNotification, wantChannel string) {
	t.Helper()

	var got *commworker.QueueMessage
	s := newCommWebhookServer(func(_ context.Context, msg commworker.QueueMessage) error {
		cp := msg
		got = &cp
		return nil
	})
	resp, err := handler(s, signedCommWebhookCtx(t, marshalCommWebhookBody(t, notification)))
	requireCommWebhookOK(t, resp, err)
	if got == nil || got.Notification.Channel != wantChannel || got.Provider != "telnyx" {
		t.Fatalf("expected normalized %s notification, got %#v", wantChannel, got)
	}
}

func requireCommWebhookOK(t *testing.T, resp *apptheory.Response, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response")
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func requireWebhookAppError(t *testing.T, err error, code string, message string) {
	t.Helper()
	if appErr, ok := apptheory.AsAppTheoryError(err); ok {
		if appErr.Code != code || appErr.Message != message {
			t.Fatalf("expected %s %q, got %v", code, message, appErr)
		}
		return
	}
	appErr := requireAppError(t, err)
	if appErr.Code != code || appErr.Message != message {
		t.Fatalf("expected %s %q, got %v", code, message, appErr)
	}
}

func marshalCommWebhookBody(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal webhook body: %v", err)
	}
	return body
}

func assertCommWebhookReceivedAt(t *testing.T, receivedAt string) {
	t.Helper()
	if receivedAt == "" {
		t.Fatalf("expected receivedAt fallback to be populated")
	}
	if _, err := time.Parse(time.RFC3339Nano, receivedAt); err != nil {
		t.Fatalf("expected RFC3339Nano fallback timestamp, got %q err=%v", receivedAt, err)
	}
}

func expectCommWebhookPhoneAgent(t *testing.T, q *ttmocks.MockQuery, agentID string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulPhoneAgentIndex](t, args, 0)
		*dest = models.SoulPhoneAgentIndex{Phone: "+15550142", AgentID: agentID}
	}).Once()
}

func expectCommWebhookIdentity(t *testing.T, q *ttmocks.MockQuery, agentID string, domain string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: domain}
	}).Once()
}

func expectCommWebhookDomain(t *testing.T, q *ttmocks.MockQuery, instanceSlug string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: instanceSlug}
	}).Once()
}
