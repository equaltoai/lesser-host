package controlplane

import (
	"net/http"
	"net/mail"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulCommMailboxReplyRequest struct {
	Body           string   `json:"body"`
	Subject        string   `json:"subject,omitempty"`
	CC             []string `json:"cc,omitempty"`
	BCC            []string `json:"bcc,omitempty"`
	ReplyTo        string   `json:"replyTo,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
}

func (s *Server) handleSoulCommMailboxReply(ctx *apptheory.Context) (*apptheory.Response, error) {
	reqCtx, appErr := s.requireMailboxRequestContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	source, appErr := s.loadMailboxMessageByRef(ctx.Context(), reqCtx.key.InstanceSlug, reqCtx.agentID, mailboxMessageRefParam(ctx))
	if appErr != nil {
		return nil, appErr
	}
	if source.Deleted {
		return nil, apptheory.NewAppTheoryError("comm.not_found", "message not found").WithStatusCode(http.StatusNotFound)
	}

	metrics := newSoulCommSendMetrics(s.cfg.Stage, reqCtx.key.InstanceSlug)
	defer metrics.emit()

	req, appErr := parseSoulCommMailboxReplyRequest(ctx, source, reqCtx.agentID, metrics)
	if appErr != nil {
		return nil, appErr
	}
	route, appErr := s.loadSoulCommSendRoute(ctx.Context(), reqCtx.key, req, metrics)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	guardDecision, guardErr := s.enforceSoulCommSendGuards(ctx, route.identity, req, now, metrics)
	if guardErr != nil {
		return nil, guardErr
	}

	messageID, appErr := newSoulCommSendMessageID()
	if appErr != nil {
		metrics.status = commMetricInternalError
		return nil, appErr
	}

	idem, existingResp, appErr := s.prepareSoulCommSendIdempotency(ctx, reqCtx.key, req, messageID, now, metrics)
	if appErr != nil {
		return nil, appErr
	}
	if existingResp != nil {
		return existingResp, nil
	}

	delivery, appErr := s.dispatchSoulCommSend(ctx, reqCtx.key, req, route.channel, now, messageID, metrics)
	s.finalizeSoulCommSendIdempotency(ctx.Context(), idem, delivery, appErr)
	if appErr != nil {
		return nil, appErr
	}
	recordErr := s.recordSoulCommSend(ctx, reqCtx.key, req, messageID, delivery, guardDecision, now, metrics)
	if recordErr != nil {
		return nil, recordErr
	}

	metrics.provider = delivery.provider
	metrics.status = commMetricSent
	resp, appErr := soulCommSendJSON(messageID, strings.TrimSpace(reqCtx.key.InstanceSlug), req, delivery, now)
	if appErr != nil {
		metrics.status = commMetricInternalError
		return nil, appErr
	}
	return resp, nil
}

func parseSoulCommMailboxReplyRequest(ctx *apptheory.Context, source *models.SoulCommMailboxMessage, agentID string, metrics *soulCommSendMetrics) (validatedSoulCommSendRequest, *apptheory.AppTheoryError) {
	var req soulCommMailboxReplyRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "invalid request").WithStatusCode(http.StatusBadRequest)
	}

	body := strings.TrimSpace(req.Body)
	if body == "" {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "body is required").WithStatusCode(http.StatusBadRequest)
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if len(idempotencyKey) > 256 {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "idempotencyKey is invalid").WithStatusCode(http.StatusBadRequest)
	}

	channel := strings.ToLower(strings.TrimSpace(source.ChannelType))
	metrics.channel = channel
	if channel != commChannelEmail && channel != commChannelSMS && channel != commChannelVoice {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError("comm.reply_unavailable", "message channel cannot be replied to").WithStatusCode(http.StatusConflict)
	}

	to, appErr := mailboxReplyRecipient(source, channel)
	if appErr != nil {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, appErr
	}

	subject := strings.TrimSpace(req.Subject)
	if channel == commChannelEmail {
		if subject == "" {
			subject = mailboxReplySubject(source.Subject)
		}
		if _, err := mail.ParseAddress(to); err != nil {
			metrics.status = commMetricInvalidRequest
			return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError("comm.reply_unavailable", "reply recipient is unavailable").WithStatusCode(http.StatusConflict)
		}
	} else {
		to = normalizeCommPhoneE164(to)
		if !soulE164Regex.MatchString(to) {
			metrics.status = commMetricInvalidRequest
			return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError("comm.reply_unavailable", "reply recipient is unavailable").WithStatusCode(http.StatusConflict)
		}
	}

	providerReplyTo := strings.TrimSpace(source.ProviderMessageID)
	if providerReplyTo == "" {
		providerReplyTo = strings.TrimSpace(source.MessageID)
	}

	return validatedSoulCommSendRequest{
		channel:         channel,
		agentIDHex:      strings.ToLower(strings.TrimSpace(agentID)),
		to:              to,
		cc:              req.CC,
		bcc:             req.BCC,
		replyTo:         strings.TrimSpace(req.ReplyTo),
		subject:         subject,
		body:            body,
		inReplyTo:       strings.TrimSpace(source.MessageID),
		providerReplyTo: providerReplyTo,
		threadID:        strings.TrimSpace(source.ThreadID),
		idempotencyKey:  idempotencyKey,
	}, nil
}

func mailboxReplyRecipient(source *models.SoulCommMailboxMessage, channel string) (string, *apptheory.AppTheoryError) {
	if source == nil {
		return "", apptheory.NewAppTheoryError("comm.reply_unavailable", "reply recipient is unavailable").WithStatusCode(http.StatusConflict)
	}
	inbound := strings.EqualFold(strings.TrimSpace(source.Direction), models.SoulCommDirectionInbound)
	switch channel {
	case commChannelEmail:
		if inbound {
			return strings.TrimSpace(source.FromAddress), nil
		}
		return strings.TrimSpace(source.ToAddress), nil
	case commChannelSMS, commChannelVoice:
		if inbound {
			return strings.TrimSpace(source.FromNumber), nil
		}
		return strings.TrimSpace(source.ToNumber), nil
	default:
		return "", apptheory.NewAppTheoryError("comm.reply_unavailable", "message channel cannot be replied to").WithStatusCode(http.StatusConflict)
	}
}

func mailboxReplySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Re: message"
	}
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}
