package controlplane

import (
	"fmt"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
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

type telnyxVoiceWebhook struct {
	Data struct {
		EventType  string         `json:"event_type"`
		OccurredAt string         `json:"occurred_at"`
		Payload    map[string]any `json:"payload"`
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
	if s == nil || ctx == nil || s.enqueueCommMessage == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}

	// Accept already-normalized communication:inbound payloads.
	var notif commworker.InboundNotification
	if err := httpx.ParseJSON(ctx, &notif); err == nil && strings.TrimSpace(notif.Type) != "" {
		notif.Channel = "voice"
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

	// Telnyx voice webhook payload.
	var tel telnyxVoiceWebhook
	if err := json.Unmarshal(ctx.Request.Body, &tel); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
	}

	from, to, callID, occurredAt, durationSeconds := extractTelnyxVoiceFields(&tel)
	if occurredAt == "" {
		occurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if callID == "" {
		callID = fmt.Sprintf("telnyx-voice-%d", time.Now().UTC().UnixNano())
	}

	// Best-effort billing: meter call usage on status-like events with duration.
	if durationSeconds > 0 {
		_ = s.meterTelnyxVoiceCall(ctx, to, callID, durationSeconds)
	}

	body := strings.TrimSpace(string(ctx.Request.Body))
	if body == "" {
		body = strings.TrimSpace(tel.Data.EventType)
	}

	msg := commworker.QueueMessage{
		Kind:     commworker.QueueMessageKindInbound,
		Provider: "telnyx",
		Notification: commworker.InboundNotification{
			Type:         "communication:inbound",
			Channel:      "voice",
			From:         commworker.InboundParty{Number: from},
			To:           &commworker.InboundParty{Number: to},
			Body:         body,
			BodyMimeType: "application/json",
			ReceivedAt:   occurredAt,
			MessageID:    callID,
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

func (s *Server) handleCommVoiceStatusWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.enqueueCommMessage == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}
	return s.handleCommVoiceInboundWebhook(ctx)
}

func extractTelnyxVoiceFields(tel *telnyxVoiceWebhook) (from string, to string, callID string, occurredAt string, durationSeconds int64) {
	if tel == nil {
		return "", "", "", "", 0
	}

	occurredAt = strings.TrimSpace(tel.Data.OccurredAt)
	payload := tel.Data.Payload

	readString := func(keys ...string) string {
		for _, key := range keys {
			raw, ok := payload[key]
			if !ok {
				continue
			}
			s, ok := raw.(string)
			if !ok {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			return s
		}
		return ""
	}

	readNumber := func(raw any) string {
		switch v := raw.(type) {
		case string:
			return strings.TrimSpace(v)
		case map[string]any:
			for _, key := range []string{"phone_number", "phoneNumber", "number"} {
				if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
			return ""
		default:
			return ""
		}
	}

	if raw, ok := payload["from"]; ok {
		from = readNumber(raw)
	}
	if raw, ok := payload["to"]; ok {
		to = readNumber(raw)
	}

	callID = readString("call_leg_id", "call_leg", "call_control_id", "call_control", "call_session_id", "call_session", "id")
	if callID == "" {
		callID = strings.TrimSpace(tel.Data.EventType)
	}

	readInt64 := func(keys ...string) int64 {
		for _, key := range keys {
			raw, ok := payload[key]
			if !ok {
				continue
			}
			switch n := raw.(type) {
			case int:
				return int64(n)
			case int64:
				return n
			case float64:
				return int64(n)
			case json.Number:
				if v, err := n.Int64(); err == nil {
					return v
				}
			case string:
				if v, err := json.Number(strings.TrimSpace(n)).Int64(); err == nil {
					return v
				}
			}
		}
		return 0
	}

	durationSeconds = readInt64("duration", "duration_seconds", "durationSeconds", "call_duration", "call_duration_seconds", "callDurationSeconds")

	return from, to, callID, occurredAt, durationSeconds
}

func (s *Server) meterTelnyxVoiceCall(ctx *apptheory.Context, toNumber string, callID string, durationSeconds int64) error {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return fmt.Errorf("store not initialized")
	}
	toNumber = normalizeCommPhoneE164(toNumber)
	if toNumber == "" || callID == "" || durationSeconds <= 0 {
		return nil
	}

	// Resolve agent -> domain -> instance.
	idx := &models.SoulPhoneAgentIndex{Phone: toNumber}
	_ = idx.UpdateKeys()
	var phoneIdx models.SoulPhoneAgentIndex
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulPhoneAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&phoneIdx)
	if err != nil {
		return nil
	}
	agentID := strings.ToLower(strings.TrimSpace(phoneIdx.AgentID))
	if agentID == "" {
		return nil
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentID)
	if err != nil || identity == nil {
		return nil
	}

	var d models.Domain
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", strings.ToLower(strings.TrimSpace(identity.Domain)))).
		Where("SK", "=", models.SKMetadata).
		First(&d); err != nil {
		return nil
	}
	instanceSlug := strings.TrimSpace(d.InstanceSlug)
	if instanceSlug == "" {
		return nil
	}

	now := time.Now().UTC()
	month := now.Format("2006-01")
	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)
	var budget models.InstanceBudgetMonth
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget); err != nil {
		return nil
	}

	minutes := (durationSeconds + 59) / 60
	if minutes <= 0 {
		return nil
	}

	// 8 credits / minute ~ $0.008, approximating Telnyx voice rates.
	credits := minutes * 8

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, credits)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, callID, "comm.voice.call", callID, credits),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 "comm.voice.call",
		Target:                 callID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              callID,
		RequestedCredits:       credits,
		ListCredits:            credits,
		PricingMultiplierBps:   10000,
		DebitedCredits:         credits,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		ActorURI:               fmt.Sprintf("soul_agent:%s", agentID),
		CreatedAt:              now,
	}
	_ = ledger.UpdateKeys()

	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = updateBudget.UpdateKeys()

	return s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Put(ledger)
		tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", credits)
			ub.Set("UpdatedAt", now)
			return nil
		}, tabletheory.IfExists())
		return nil
	})
}
