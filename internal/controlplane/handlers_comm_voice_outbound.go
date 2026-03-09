package controlplane

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const telnyxDefaultOutboundVoice = "Polly.Amy-Neural"
const telnyxDefaultOutboundReplyPrompt = "If you would like to reply, speak now. Otherwise, you may hang up."

type telnyxVoiceGatherCallback struct {
	CallSID      string
	From         string
	To           string
	Digits       string
	SpeechResult string
	Confidence   string
}

type voiceGatherCapture struct {
	replyMessageID  string
	replyBody       string
	replyConfidence *float64
	replyReceivedAt time.Time
	fromNumber      string
	toNumber        string
}

func normalizeSoulCommPublicBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return ""
	}
	if u.User != nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "https" && scheme != "http" {
		return ""
	}
	host := strings.TrimSpace(u.Host)
	if !safeSoulCommHost(host) {
		return ""
	}
	return scheme + "://" + host
}

func safeSoulCommHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.ContainsAny(host, " \t\r\n/\\") {
		return false
	}
	if strings.Contains(host, "@") {
		return false
	}
	return true
}

func soulCommRequestBaseURL(ctx *apptheory.Context, publicBaseURL string) string {
	if base := normalizeSoulCommPublicBaseURL(publicBaseURL); base != "" {
		return base
	}
	if ctx == nil {
		return ""
	}
	host := strings.TrimSpace(httpx.FirstHeaderValue(ctx.Request.Headers, "host"))
	if !safeSoulCommHost(host) {
		return ""
	}
	return "https://" + host
}

func (s *Server) handleCommVoiceTeXML(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}

	messageID := strings.TrimSpace(ctx.Param("messageId"))
	if messageID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "messageId is required"}
	}

	item, ok, err := s.loadSoulCommVoiceInstruction(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apptheory.NewAppTheoryError("app.not_found", "not found").WithStatusCode(http.StatusNotFound)
	}

	baseURL := soulCommRequestBaseURL(ctx, s.cfg.PublicBaseURL)
	if baseURL == "" {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	actionURL := baseURL + "/webhooks/comm/voice/gather/" + url.PathEscape(messageID)

	payload, buildErr := buildSoulCommVoiceTeXML(strings.TrimSpace(item.Body), strings.TrimSpace(item.Voice), actionURL)
	if buildErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return &apptheory.Response{
		Status: http.StatusOK,
		Headers: map[string][]string{
			"content-type": {"application/xml; charset=utf-8"},
		},
		Body: payload,
	}, nil
}

func (s *Server) loadSoulCommVoiceInstruction(ctx *apptheory.Context, messageID string) (models.SoulCommVoiceInstruction, bool, error) {
	instruction := &models.SoulCommVoiceInstruction{MessageID: messageID}
	_ = instruction.UpdateKeys()
	var item models.SoulCommVoiceInstruction
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommVoiceInstruction{}).
		Where("PK", "=", instruction.PK).
		Where("SK", "=", instruction.SK).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return models.SoulCommVoiceInstruction{}, false, nil
	}
	if err != nil {
		return models.SoulCommVoiceInstruction{}, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return item, true, nil
}

func buildSoulCommVoiceTeXML(body string, voice string, actionURL string) ([]byte, error) {
	body = strings.TrimSpace(body)
	voice = strings.TrimSpace(voice)
	actionURL = strings.TrimSpace(actionURL)
	if body == "" {
		body = "No message provided."
	}
	if voice == "" {
		voice = telnyxDefaultOutboundVoice
	}
	if actionURL == "" {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "action url is required"}
	}

	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(body)); err != nil {
		return nil, err
	}
	var escapedVoice bytes.Buffer
	if err := xml.EscapeText(&escapedVoice, []byte(voice)); err != nil {
		return nil, err
	}
	var escapedAction bytes.Buffer
	if err := xml.EscapeText(&escapedAction, []byte(actionURL)); err != nil {
		return nil, err
	}
	var escapedReplyPrompt bytes.Buffer
	if err := xml.EscapeText(&escapedReplyPrompt, []byte(telnyxDefaultOutboundReplyPrompt)); err != nil {
		return nil, err
	}

	payload := `<Response><Gather input="speech" action="` + escapedAction.String() + `" method="POST" timeout="5" speechTimeout="auto"><Say voice="` + escapedVoice.String() + `">` + escaped.String() + `</Say><Say voice="` + escapedVoice.String() + `">` + escapedReplyPrompt.String() + `</Say></Gather><Say voice="` + escapedVoice.String() + `">No reply was captured. Goodbye.</Say><Hangup/></Response>`
	return []byte(payload), nil
}

func (s *Server) handleCommVoiceGatherWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || s.enqueueCommMessage == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return apptheory.JSON(http.StatusNotFound, map[string]any{"ok": false})
	}

	messageID := strings.TrimSpace(ctx.Param("messageId"))
	if messageID == "" {
		messageID = strings.TrimSpace(queryFirst(ctx, "messageId"))
	}
	if messageID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "messageId is required"}
	}

	instruction, ok, err := s.loadSoulCommVoiceInstruction(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "captured": false})
	}

	callback, appErr := parseTelnyxVoiceGatherCallback(ctx)
	if appErr != nil {
		return nil, appErr
	}
	capture, ok := buildVoiceGatherCapture(messageID, instruction, callback)
	if !ok {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "captured": false})
	}
	if appErr := s.enqueueVoiceGatherCapture(ctx, messageID, capture); appErr != nil {
		return nil, appErr
	}
	_ = s.recordOutboundVoiceReply(ctx, messageID, capture.replyMessageID, capture.replyBody, capture.replyConfidence, capture.replyReceivedAt)

	return apptheory.JSON(http.StatusOK, map[string]any{
		"ok":         true,
		"captured":   true,
		"messageId":  capture.replyMessageID,
		"inReplyTo":  messageID,
		"replyBody":  capture.replyBody,
		"receivedAt": capture.replyReceivedAt.Format(time.RFC3339Nano),
		"confidence": capture.replyConfidence,
	})
}

func buildVoiceGatherCapture(messageID string, instruction models.SoulCommVoiceInstruction, callback telnyxVoiceGatherCallback) (voiceGatherCapture, bool) {
	replyBody := normalizeTelnyxVoiceGatherReply(callback.SpeechResult, callback.Digits)
	if replyBody == "" {
		return voiceGatherCapture{}, false
	}

	fromNumber := strings.TrimSpace(instruction.To)
	toNumber := strings.TrimSpace(instruction.From)
	if fromNumber == "" {
		fromNumber = strings.TrimSpace(callback.To)
	}
	if toNumber == "" {
		toNumber = strings.TrimSpace(callback.From)
	}

	return voiceGatherCapture{
		replyMessageID:  voiceReplyMessageID(messageID, callback.CallSID),
		replyBody:       replyBody,
		replyConfidence: parseTelnyxVoiceGatherConfidence(callback.Confidence),
		replyReceivedAt: time.Now().UTC(),
		fromNumber:      fromNumber,
		toNumber:        toNumber,
	}, true
}

func (s *Server) enqueueVoiceGatherCapture(ctx *apptheory.Context, messageID string, capture voiceGatherCapture) *apptheory.AppError {
	inReplyTo := messageID
	notif := commworker.InboundNotification{
		Type:       "communication:inbound",
		Channel:    commChannelVoice,
		From:       commworker.InboundParty{Number: capture.fromNumber},
		To:         &commworker.InboundParty{Number: capture.toNumber},
		Body:       capture.replyBody,
		ReceivedAt: capture.replyReceivedAt.Format(time.RFC3339Nano),
		MessageID:  capture.replyMessageID,
		InReplyTo:  &inReplyTo,
	}
	msg := commworker.QueueMessage{
		Kind:         commworker.QueueMessageKindInbound,
		Provider:     commDeliveryProviderTelnyx,
		Notification: notif,
	}
	if err := msg.Validate(); err != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "invalid gather payload"}
	}
	if err := s.enqueueCommMessage(ctx.Context(), msg); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue"}
	}
	return nil
}

func parseTelnyxVoiceGatherCallback(ctx *apptheory.Context) (telnyxVoiceGatherCallback, *apptheory.AppError) {
	if ctx == nil {
		return telnyxVoiceGatherCallback{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	body := bytes.TrimSpace(ctx.Request.Body)
	if len(body) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil && len(payload) > 0 {
			cb := telnyxVoiceGatherCallback{
				CallSID:      mapStringFirst(payload, "CallSid", "callSid", "call_sid"),
				From:         mapStringFirst(payload, "From", "from"),
				To:           mapStringFirst(payload, "To", "to"),
				Digits:       mapStringFirst(payload, "Digits", "digits"),
				SpeechResult: mapStringFirst(payload, "SpeechResult", "speechResult", "speech_result"),
				Confidence:   mapStringFirst(payload, "Confidence", "confidence"),
			}
			if hasTelnyxVoiceGatherContent(cb) {
				return cb, nil
			}
		}

		if values, err := url.ParseQuery(string(body)); err == nil && len(values) > 0 {
			cb := telnyxVoiceGatherCallback{
				CallSID:      firstNonEmpty(values.Get("CallSid"), values.Get("callSid"), values.Get("call_sid")),
				From:         firstNonEmpty(values.Get("From"), values.Get("from")),
				To:           firstNonEmpty(values.Get("To"), values.Get("to")),
				Digits:       firstNonEmpty(values.Get("Digits"), values.Get("digits")),
				SpeechResult: firstNonEmpty(values.Get("SpeechResult"), values.Get("speechResult"), values.Get("speech_result")),
				Confidence:   firstNonEmpty(values.Get("Confidence"), values.Get("confidence")),
			}
			if hasTelnyxVoiceGatherContent(cb) {
				return cb, nil
			}
		}
	}

	cb := telnyxVoiceGatherCallback{
		CallSID:      strings.TrimSpace(queryFirst(ctx, "CallSid")),
		From:         strings.TrimSpace(firstNonEmpty(queryFirst(ctx, "From"), queryFirst(ctx, "from"))),
		To:           strings.TrimSpace(firstNonEmpty(queryFirst(ctx, "To"), queryFirst(ctx, "to"))),
		Digits:       strings.TrimSpace(firstNonEmpty(queryFirst(ctx, "Digits"), queryFirst(ctx, "digits"))),
		SpeechResult: strings.TrimSpace(firstNonEmpty(queryFirst(ctx, "SpeechResult"), queryFirst(ctx, "speechResult"), queryFirst(ctx, "speech_result"))),
		Confidence:   strings.TrimSpace(firstNonEmpty(queryFirst(ctx, "Confidence"), queryFirst(ctx, "confidence"))),
	}
	if !hasTelnyxVoiceGatherContent(cb) {
		return telnyxVoiceGatherCallback{}, &apptheory.AppError{Code: "app.bad_request", Message: "invalid gather payload"}
	}
	return cb, nil
}

func mapStringFirst(m map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := m[key]
		if !ok {
			continue
		}
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case json.Number:
			if s := strings.TrimSpace(v.String()); s != "" {
				return s
			}
		case float64:
			return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hasTelnyxVoiceGatherContent(cb telnyxVoiceGatherCallback) bool {
	return strings.TrimSpace(cb.CallSID) != "" ||
		strings.TrimSpace(cb.From) != "" ||
		strings.TrimSpace(cb.To) != "" ||
		strings.TrimSpace(cb.Digits) != "" ||
		strings.TrimSpace(cb.SpeechResult) != "" ||
		strings.TrimSpace(cb.Confidence) != ""
}

func normalizeTelnyxVoiceGatherReply(speechResult string, digits string) string {
	speechResult = strings.TrimSpace(speechResult)
	digits = strings.TrimSpace(digits)
	if speechResult != "" {
		return speechResult
	}
	if digits != "" {
		return "Digits: " + digits
	}
	return ""
}

func voiceReplyMessageID(messageID string, callSID string) string {
	messageID = strings.TrimSpace(messageID)
	callSID = strings.TrimSpace(callSID)
	if messageID == "" {
		return ""
	}
	if callSID == "" {
		return messageID + "-reply"
	}
	return messageID + "-reply-" + callSID
}

func parseTelnyxVoiceGatherConfidence(raw string) *float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}

func (s *Server) recordOutboundVoiceReply(ctx *apptheory.Context, messageID string, replyMessageID string, replyBody string, replyConfidence *float64, replyReceivedAt time.Time) error {
	statusKey := &models.SoulCommMessageStatus{MessageID: messageID}
	_ = statusKey.UpdateKeys()

	var statusItem models.SoulCommMessageStatus
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommMessageStatus{}).
		Where("PK", "=", statusKey.PK).
		Where("SK", "=", statusKey.SK).
		First(&statusItem)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	statusItem.ReplyMessageID = strings.TrimSpace(replyMessageID)
	statusItem.ReplyBody = strings.TrimSpace(replyBody)
	statusItem.ReplyConfidence = replyConfidence
	statusItem.ReplyReceivedAt = replyReceivedAt.UTC()
	if strings.TrimSpace(statusItem.Status) == models.SoulCommMessageStatusAccepted {
		statusItem.Status = models.SoulCommMessageStatusSent
	}
	return s.store.DB.WithContext(ctx.Context()).Model(&statusItem).IfExists().Update("Status", "ReplyMessageID", "ReplyBody", "ReplyConfidence", "ReplyReceivedAt", "UpdatedAt")
}

func (s *Server) maybeHandleOutboundVoiceStatusWebhook(ctx *apptheory.Context) (*apptheory.Response, bool, error) {
	messageID := strings.TrimSpace(ctx.Param("messageId"))
	if messageID == "" {
		messageID = strings.TrimSpace(queryFirst(ctx, "messageId"))
	}
	if messageID == "" {
		return nil, false, nil
	}
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, true, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var tel telnyxVoiceWebhook
	if err := httpx.ParseJSON(ctx, &tel); err != nil {
		return nil, true, &apptheory.AppError{Code: "app.bad_request", Message: "invalid webhook payload"}
	}

	if err := s.updateOutboundVoiceStatusFromWebhook(ctx, messageID, &tel); err != nil {
		return nil, true, err
	}
	resp, err := apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	return resp, true, err
}

func (s *Server) updateOutboundVoiceStatusFromWebhook(ctx *apptheory.Context, messageID string, tel *telnyxVoiceWebhook) error {
	statusKey := &models.SoulCommMessageStatus{MessageID: messageID}
	_ = statusKey.UpdateKeys()

	var statusItem models.SoulCommMessageStatus
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommMessageStatus{}).
		Where("PK", "=", statusKey.PK).
		Where("SK", "=", statusKey.SK).
		First(&statusItem)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	_, to, callID, _, durationSeconds := extractTelnyxVoiceFields(tel)
	if durationSeconds > 0 {
		_ = s.meterTelnyxVoiceCall(ctx, to, callID, durationSeconds)
	}

	nextStatus, errorCode, errorMessage, shouldUpdate := mapTelnyxVoiceEventToSoulStatus(strings.TrimSpace(tel.Data.EventType), durationSeconds, strings.TrimSpace(statusItem.Status))
	if !shouldUpdate && strings.TrimSpace(callID) == "" {
		return nil
	}

	if strings.TrimSpace(callID) != "" {
		statusItem.ProviderMessageID = strings.TrimSpace(callID)
	}
	if shouldUpdate {
		statusItem.Status = nextStatus
		statusItem.ErrorCode = errorCode
		statusItem.ErrorMessage = errorMessage
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(&statusItem).IfExists().Update("ProviderMessageID", "Status", "ErrorCode", "ErrorMessage", "UpdatedAt"); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return nil
}

func mapTelnyxVoiceEventToSoulStatus(eventType string, durationSeconds int64, currentStatus string) (status string, errorCode string, errorMessage string, shouldUpdate bool) {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	currentStatus = strings.ToLower(strings.TrimSpace(currentStatus))
	switch {
	case strings.Contains(eventType, "answered"), strings.Contains(eventType, "speak.ended"), strings.Contains(eventType, "call.playback.ended"):
		return models.SoulCommMessageStatusSent, "", "", true
	case strings.Contains(eventType, "hangup"), strings.Contains(eventType, "ended"), strings.Contains(eventType, "completed"):
		if durationSeconds > 0 || currentStatus == models.SoulCommMessageStatusSent {
			return models.SoulCommMessageStatusSent, "", "", currentStatus != models.SoulCommMessageStatusSent
		}
		return models.SoulCommMessageStatusFailed, "call.hangup", "call ended before delivery", true
	case strings.Contains(eventType, "busy"), strings.Contains(eventType, "failed"), strings.Contains(eventType, "error"), strings.Contains(eventType, "no_answer"), strings.Contains(eventType, "no-answer"), strings.Contains(eventType, "rejected"), strings.Contains(eventType, "canceled"):
		if currentStatus == models.SoulCommMessageStatusSent {
			return "", "", "", false
		}
		return models.SoulCommMessageStatusFailed, eventType, "provider reported call failure", true
	default:
		return "", "", "", false
	}
}
