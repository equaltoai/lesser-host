package controlplane

import (
	"encoding/json"
	"fmt"
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
	To           string                         `json:"to"`
	From         string                         `json:"from"`
	FromName     string                         `json:"fromName,omitempty"`
	Subject      string                         `json:"subject"`
	Body         string                         `json:"body"`
	BodyMimeType string                         `json:"bodyMimeType,omitempty"`
	ReceivedAt   string                         `json:"receivedAt,omitempty"`
	MessageID    string                         `json:"messageId"`
	InReplyTo    *string                        `json:"inReplyTo,omitempty"`
	Attachments  []commworker.InboundAttachment `json:"attachments,omitempty"`
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
		notif.Channel = commChannelEmail
		msg := commworker.QueueMessage{
			Kind:         commworker.QueueMessageKindInbound,
			Provider:     commDeliveryProviderMigadu,
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
		Provider: commDeliveryProviderMigadu,
		Notification: commworker.InboundNotification{
			Type:         "communication:inbound",
			Channel:      commChannelEmail,
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
		notif.Channel = commChannelSMS
		msg := commworker.QueueMessage{
			Kind:         commworker.QueueMessageKindInbound,
			Provider:     commDeliveryProviderTelnyx,
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
	eventType := strings.TrimSpace(tel.Data.EventType)
	if eventType != "message.received" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": eventType})
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
		Provider: commDeliveryProviderTelnyx,
		Notification: commworker.InboundNotification{
			Type:       "communication:inbound",
			Channel:    commChannelSMS,
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
		notif.Channel = commChannelVoice
		msg := commworker.QueueMessage{
			Kind:         commworker.QueueMessageKindInbound,
			Provider:     commDeliveryProviderTelnyx,
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

	notif, to, callID, durationSeconds := buildTelnyxVoiceNotification(ctx.Request.Body, &tel)

	// Best-effort billing: meter call usage on status-like events with duration.
	if durationSeconds > 0 {
		_ = s.meterTelnyxVoiceCall(ctx, to, callID, durationSeconds)
	}

	msg := commworker.QueueMessage{
		Kind:         commworker.QueueMessageKindInbound,
		Provider:     commDeliveryProviderTelnyx,
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

func (s *Server) handleCommVoiceStatusWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil || s.enqueueCommMessage == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}
	if resp, handled, err := s.maybeHandleOutboundVoiceStatusWebhook(ctx); handled || err != nil {
		return resp, err
	}
	return s.handleCommVoiceInboundWebhook(ctx)
}

func extractTelnyxVoiceFields(tel *telnyxVoiceWebhook) (from string, to string, callID string, occurredAt string, durationSeconds int64) {
	if tel == nil {
		return "", "", "", "", 0
	}

	occurredAt = strings.TrimSpace(tel.Data.OccurredAt)
	payload := tel.Data.Payload
	from = readTelnyxPhonePayload(payload, "from")
	to = readTelnyxPhonePayload(payload, "to")
	callID = readTelnyxStringPayload(payload, "call_leg_id", "call_leg", "call_control_id", "call_control", "call_session_id", "call_session", "id")
	if callID == "" {
		callID = strings.TrimSpace(tel.Data.EventType)
	}
	durationSeconds = readTelnyxInt64Payload(payload, "duration", "duration_seconds", "durationSeconds", "call_duration", "call_duration_seconds", "callDurationSeconds")

	return from, to, callID, occurredAt, durationSeconds
}

func buildTelnyxVoiceNotification(body []byte, tel *telnyxVoiceWebhook) (commworker.InboundNotification, string, string, int64) {
	from, to, callID, occurredAt, durationSeconds := extractTelnyxVoiceFields(tel)
	if occurredAt == "" {
		occurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if callID == "" {
		callID = fmt.Sprintf("telnyx-voice-%d", time.Now().UTC().UnixNano())
	}

	text := strings.TrimSpace(string(body))
	if text == "" && tel != nil {
		text = strings.TrimSpace(tel.Data.EventType)
	}

	return commworker.InboundNotification{
		Type:         "communication:inbound",
		Channel:      commChannelVoice,
		From:         commworker.InboundParty{Number: from},
		To:           &commworker.InboundParty{Number: to},
		Body:         text,
		BodyMimeType: "application/json",
		ReceivedAt:   occurredAt,
		MessageID:    callID,
	}, to, callID, durationSeconds
}

func readTelnyxStringPayload(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func readTelnyxPhonePayload(payload map[string]any, key string) string {
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	return readTelnyxPhoneValue(raw)
}

func readTelnyxPhoneValue(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"phone_number", "phoneNumber", "number"} {
			value, ok := v[key].(string)
			if ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func readTelnyxInt64Payload(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		if value, ok := coerceTelnyxInt64(raw); ok {
			return value
		}
	}
	return 0
}

func coerceTelnyxInt64(raw any) (int64, bool) {
	switch n := raw.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		value, err := n.Int64()
		return value, err == nil
	case string:
		value, err := json.Number(strings.TrimSpace(n)).Int64()
		return value, err == nil
	default:
		return 0, false
	}
}

func (s *Server) meterTelnyxVoiceCall(ctx *apptheory.Context, toNumber string, callID string, durationSeconds int64) error {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return fmt.Errorf("store not initialized")
	}
	toNumber = normalizeCommPhoneE164(toNumber)
	if toNumber == "" || callID == "" || durationSeconds <= 0 {
		return nil
	}

	agentID, instanceSlug, budget, now, err := s.resolveTelnyxVoiceBudget(ctx, toNumber)
	if err != nil {
		return nil
	}
	month := now.Format("2006-01")

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

func (s *Server) resolveTelnyxVoiceBudget(ctx *apptheory.Context, toNumber string) (string, string, models.InstanceBudgetMonth, time.Time, error) {
	var budget models.InstanceBudgetMonth
	now := time.Now().UTC()

	idx := &models.SoulPhoneAgentIndex{Phone: toNumber}
	_ = idx.UpdateKeys()
	var phoneIdx models.SoulPhoneAgentIndex
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulPhoneAgentIndex{}).
		Where("PK", "=", idx.PK).
		Where("SK", "=", idx.SK).
		First(&phoneIdx); err != nil {
		return "", "", budget, now, err
	}

	agentID := strings.ToLower(strings.TrimSpace(phoneIdx.AgentID))
	if agentID == "" {
		return "", "", budget, now, fmt.Errorf("phone index missing agent")
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentID)
	if err != nil || identity == nil {
		return "", "", budget, now, fmt.Errorf("agent identity not found")
	}

	instanceSlug, err := s.loadTelnyxVoiceInstanceSlug(ctx, identity)
	if err != nil {
		return "", "", budget, now, err
	}

	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", instanceSlug)).
		Where("SK", "=", fmt.Sprintf("BUDGET#%s", now.Format("2006-01"))).
		ConsistentRead().
		First(&budget); err != nil {
		return "", "", budget, now, err
	}

	return agentID, instanceSlug, budget, now, nil
}

func (s *Server) loadTelnyxVoiceInstanceSlug(ctx *apptheory.Context, identity *models.SoulAgentIdentity) (string, error) {
	d, err := s.loadManagedStageAwareDomain(ctx.Context(), strings.ToLower(strings.TrimSpace(identity.Domain)))
	if err != nil {
		return "", err
	}
	if d == nil {
		return "", fmt.Errorf("domain missing instance slug")
	}
	instanceSlug := strings.TrimSpace(d.InstanceSlug)
	if instanceSlug == "" {
		return "", fmt.Errorf("domain missing instance slug")
	}
	return instanceSlug, nil
}
