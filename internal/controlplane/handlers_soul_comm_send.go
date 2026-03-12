package controlplane

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/hostmetrics"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	commSMSSendCreditCost int64 = 4 // 1000 credits = $1.00 (default). Telnyx SMS is ~$0.004.

	commChannelEmail = "email"
	commChannelSMS   = "sms"
	commChannelVoice = "voice"

	commDeliveryProviderMigadu = "migadu"
	commDeliveryProviderTelnyx = "telnyx"

	commMetricUnknown             = "unknown"
	commMetricInvalidRequest      = "invalid_request"
	commMetricUnauthorized        = "unauthorized"
	commMetricInternalError       = "internal_error"
	commMetricProviderUnavailable = "provider_unavailable"
	commMetricProviderRejected    = "provider_rejected"
	commMetricInsufficientCredits = "insufficient_credits"
	commMetricPreferenceViolation = "preference_violation"
	commMetricSent                = "sent"

	commCodeInvalidRequest      = "comm.invalid_request"
	commCodeUnauthorized        = "comm.unauthorized"
	commCodeInternal            = "comm.internal"
	commCodeProviderUnavailable = "comm.provider_unavailable"
	commCodeInsufficientCredits = "comm.insufficient_credits"
	commCodePreferenceViolation = "comm.preference_violation"
)

type soulCommSendRequest struct {
	Channel   string   `json:"channel"`
	AgentID   string   `json:"agentId"`
	To        string   `json:"to"`
	CC        []string `json:"cc,omitempty"`
	BCC       []string `json:"bcc,omitempty"`
	ReplyTo   string   `json:"replyTo,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Body      string   `json:"body"`
	InReplyTo *string  `json:"inReplyTo,omitempty"`
}

type soulCommSendResponse struct {
	MessageID         string `json:"messageId"`
	Status            string `json:"status"`
	Channel           string `json:"channel"`
	AgentID           string `json:"agentId"`
	To                string `json:"to"`
	Provider          string `json:"provider,omitempty"`
	ProviderMessageID string `json:"providerMessageId,omitempty"`
	CreatedAt         string `json:"createdAt"`
}

type soulCommStatusResponse struct {
	MessageID         string   `json:"messageId"`
	Status            string   `json:"status"`
	Channel           string   `json:"channel"`
	AgentID           string   `json:"agentId"`
	To                string   `json:"to"`
	Provider          string   `json:"provider,omitempty"`
	ProviderMessageID string   `json:"providerMessageId,omitempty"`
	ErrorCode         string   `json:"errorCode,omitempty"`
	ErrorMessage      string   `json:"errorMessage,omitempty"`
	ReplyMessageID    string   `json:"replyMessageId,omitempty"`
	ReplyBody         string   `json:"replyBody,omitempty"`
	ReplyConfidence   *float64 `json:"replyConfidence,omitempty"`
	ReplyReceivedAt   string   `json:"replyReceivedAt,omitempty"`
	CreatedAt         string   `json:"createdAt"`
	UpdatedAt         string   `json:"updatedAt,omitempty"`
}

type soulCommSendMetrics struct {
	stage    string
	instance string
	channel  string
	provider string
	status   string
}

type validatedSoulCommSendRequest struct {
	channel    string
	agentIDHex string
	to         string
	cc         []string
	bcc        []string
	replyTo    string
	subject    string
	body       string
	inReplyTo  string
}

type soulCommSendRoute struct {
	identity *models.SoulAgentIdentity
	channel  *models.SoulAgentChannel
}

type soulCommSendDelivery struct {
	provider          string
	providerMessageID string
	initialStatus     string
}

type soulCommSendGuardDecision struct {
	preferenceRespected *bool
}

func newSoulCommSendMetrics(stage string, instance string) *soulCommSendMetrics {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = "lab"
	}
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = commMetricUnknown
	}
	return &soulCommSendMetrics{
		stage:    stage,
		instance: instance,
		channel:  commMetricUnknown,
		provider: commMetricUnknown,
		status:   commMetricUnknown,
	}
}

func (m *soulCommSendMetrics) emit() {
	stage := strings.TrimSpace(m.stage)
	service := strings.TrimSpace(ServiceName)
	instance := strings.TrimSpace(m.instance)
	channel := strings.TrimSpace(m.channel)
	provider := strings.TrimSpace(m.provider)
	status := strings.TrimSpace(m.status)

	hostmetrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  service,
		"Instance": instance,
		"Channel":  channel,
		"Provider": provider,
		"Status":   status,
	}, []hostmetrics.Metric{
		{Name: "CommOutboundRequests", Unit: hostmetrics.UnitCount, Value: 1},
	}, nil)

	// Emit an alarm-friendly rollup because CloudWatch metric alarms cannot target SEARCH expressions.
	if status == commMetricProviderRejected {
		hostmetrics.Emit("lesser-host", map[string]string{
			"Stage":   stage,
			"Service": service,
			"Status":  status,
		}, []hostmetrics.Metric{
			{Name: "CommOutboundRequests", Unit: hostmetrics.UnitCount, Value: 1},
		}, nil)
	}
}

func (s *Server) handleSoulCommSend(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !s.cfg.SoulEnabled {
		return nil, apptheory.NewAppTheoryError(commCodeUnauthorized, "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}

	key, authErr := s.requireCommInstanceKey(ctx)
	if authErr != nil {
		return nil, authErr
	}

	metrics := newSoulCommSendMetrics(s.cfg.Stage, key.InstanceSlug)
	defer metrics.emit()

	req, appErr := parseSoulCommSendRequest(ctx, metrics)
	if appErr != nil {
		return nil, appErr
	}
	route, appErr := s.loadSoulCommSendRoute(ctx.Context(), key, req, metrics)
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

	delivery, appErr := s.dispatchSoulCommSend(ctx, key, req, route.channel, now, messageID, metrics)
	if appErr != nil {
		return nil, appErr
	}
	recordErr := s.recordSoulCommSend(ctx, key, req, messageID, delivery, guardDecision, now, metrics)
	if recordErr != nil {
		return nil, recordErr
	}

	metrics.provider = delivery.provider
	metrics.status = commMetricSent
	resp, appErr := soulCommSendJSON(messageID, req, delivery, now)
	if appErr != nil {
		metrics.status = commMetricInternalError
		return nil, appErr
	}
	return resp, nil
}

func parseSoulCommSendRequest(ctx *apptheory.Context, metrics *soulCommSendMetrics) (validatedSoulCommSendRequest, *apptheory.AppTheoryError) {
	var req soulCommSendRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "invalid request").WithStatusCode(http.StatusBadRequest)
	}

	channel := strings.ToLower(strings.TrimSpace(req.Channel))
	if channel == "" || (channel != commChannelEmail && channel != commChannelSMS && channel != commChannelVoice) {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "channel is invalid").WithStatusCode(http.StatusBadRequest)
	}
	metrics.channel = channel

	agentIDHex, appErr := normalizeCommAgentID(req.AgentID)
	if appErr != nil {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, appErr
	}

	to := strings.TrimSpace(req.To)
	if to == "" {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "to is required").WithStatusCode(http.StatusBadRequest)
	}
	subject := strings.TrimSpace(req.Subject)
	switch channel {
	case commChannelEmail:
		if _, err := mail.ParseAddress(to); err != nil {
			metrics.status = commMetricInvalidRequest
			return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "to must be an email address").WithStatusCode(http.StatusBadRequest)
		}
		if subject == "" {
			metrics.status = commMetricInvalidRequest
			return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "subject is required for email").WithStatusCode(http.StatusBadRequest)
		}
	default:
		to = normalizeCommPhoneE164(to)
		if !soulE164Regex.MatchString(to) {
			metrics.status = commMetricInvalidRequest
			return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "to must be an E.164 phone number").WithStatusCode(http.StatusBadRequest)
		}
	}

	body := strings.TrimSpace(req.Body)
	if body == "" {
		metrics.status = commMetricInvalidRequest
		return validatedSoulCommSendRequest{}, apptheory.NewAppTheoryError(commCodeInvalidRequest, "body is required").WithStatusCode(http.StatusBadRequest)
	}

	inReplyTo := ""
	if req.InReplyTo != nil {
		inReplyTo = strings.TrimSpace(*req.InReplyTo)
	}

	return validatedSoulCommSendRequest{
		channel:    channel,
		agentIDHex: agentIDHex,
		to:         to,
		cc:         req.CC,
		bcc:        req.BCC,
		replyTo:    strings.TrimSpace(req.ReplyTo),
		subject:    subject,
		body:       body,
		inReplyTo:  inReplyTo,
	}, nil
}

func (s *Server) loadSoulCommSendRoute(ctx context.Context, key *models.InstanceKey, req validatedSoulCommSendRequest, metrics *soulCommSendMetrics) (soulCommSendRoute, *apptheory.AppTheoryError) {
	identity, appErr := s.loadSoulCommSendIdentity(ctx, key, req.agentIDHex, metrics)
	if appErr != nil {
		return soulCommSendRoute{}, appErr
	}
	channel, appErr := s.loadSoulCommSendChannel(ctx, req, metrics)
	if appErr != nil {
		return soulCommSendRoute{}, appErr
	}
	return soulCommSendRoute{identity: identity, channel: channel}, nil
}

func (s *Server) loadSoulCommSendIdentity(ctx context.Context, key *models.InstanceKey, agentIDHex string, metrics *soulCommSendMetrics) (*models.SoulAgentIdentity, *apptheory.AppTheoryError) {
	identity, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if theoryErrors.IsNotFound(err) {
		metrics.status = commMetricUnauthorized
		return nil, apptheory.NewAppTheoryError(commCodeUnauthorized, "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		metrics.status = commMetricInternalError
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	effectiveStatus := strings.TrimSpace(identity.LifecycleStatus)
	if effectiveStatus == "" {
		effectiveStatus = strings.TrimSpace(identity.Status)
	}
	if effectiveStatus != models.SoulAgentStatusActive {
		metrics.status = "agent_not_active"
		return nil, apptheory.NewAppTheoryError("comm.agent_not_active", "agent is not active").WithStatusCode(http.StatusConflict)
	}
	accessErr := s.requireCommAgentInstanceAccess(ctx, key, identity)
	if accessErr != nil {
		metrics.status = commMetricUnauthorized
		return nil, accessErr
	}
	return identity, nil
}

func (s *Server) loadSoulCommSendChannel(ctx context.Context, req validatedSoulCommSendRequest, metrics *soulCommSendMetrics) (*models.SoulAgentChannel, *apptheory.AppTheoryError) {
	channelSK := "CHANNEL#email"
	if req.channel == commChannelSMS || req.channel == commChannelVoice {
		channelSK = "CHANNEL#phone"
	}
	channel, chErr := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx, req.agentIDHex, channelSK)
	if chErr != nil {
		if theoryErrors.IsNotFound(chErr) {
			metrics.status = "channel_not_provisioned"
			return nil, apptheory.NewAppTheoryError("comm.channel_not_provisioned", "channel is not provisioned").WithStatusCode(http.StatusConflict)
		}
		metrics.status = commMetricInternalError
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if channel == nil || strings.TrimSpace(channel.Identifier) == "" || channel.ProvisionedAt.IsZero() || !channel.DeprovisionedAt.IsZero() || strings.TrimSpace(channel.Status) != models.SoulChannelStatusActive {
		metrics.status = "channel_not_provisioned"
		return nil, apptheory.NewAppTheoryError("comm.channel_not_provisioned", "channel is not provisioned").WithStatusCode(http.StatusConflict)
	}
	if !channel.Verified {
		metrics.status = "channel_unverified"
		return nil, apptheory.NewAppTheoryError("comm.channel_unverified", "channel is not verified").WithStatusCode(http.StatusConflict)
	}
	return channel, nil
}

func (s *Server) enforceSoulCommSendGuards(ctx *apptheory.Context, identity *models.SoulAgentIdentity, req validatedSoulCommSendRequest, now time.Time, metrics *soulCommSendMetrics) (soulCommSendGuardDecision, *apptheory.AppTheoryError) {
	hourCount, err := s.countSoulOutboundCommSince(ctx.Context(), req.agentIDHex, req.channel, now.Add(-1*time.Hour), 250)
	if err != nil {
		metrics.status = commMetricInternalError
		return soulCommSendGuardDecision{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	dayCount, err := s.countSoulOutboundCommSince(ctx.Context(), req.agentIDHex, req.channel, now.Add(-24*time.Hour), 500)
	if err != nil {
		metrics.status = commMetricInternalError
		return soulCommSendGuardDecision{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	maxHour, maxDay := 50, 500
	if req.channel == commChannelSMS {
		maxHour, maxDay = 20, 200
	}
	if hourCount >= maxHour || dayCount >= maxDay {
		metrics.status = "rate_limited"
		return soulCommSendGuardDecision{}, apptheory.NewAppTheoryError("comm.rate_limited", "rate limited").WithStatusCode(http.StatusTooManyRequests)
	}
	if req.inReplyTo != "" {
		return soulCommSendGuardDecision{}, nil
	}
	return s.enforceSoulCommFirstContactPolicy(ctx, identity, req, now, metrics)
}

func (s *Server) enforceSoulCommFirstContactPolicy(ctx *apptheory.Context, identity *models.SoulAgentIdentity, req validatedSoulCommSendRequest, now time.Time, metrics *soulCommSendMetrics) (soulCommSendGuardDecision, *apptheory.AppTheoryError) {
	recipientAgentID, prefs, appErr := s.loadSoulCommFirstContactRecipient(ctx.Context(), req)
	if appErr != nil {
		return soulCommSendGuardDecision{}, appErr
	}
	if recipientAgentID == "" || prefs == nil {
		return soulCommSendGuardDecision{}, nil
	}

	enforced := false
	if prefs.FirstContactRequireSoul {
		enforced = true
		if identity == nil || strings.TrimSpace(identity.AgentID) == "" {
			return soulCommSendGuardDecision{preferenceRespected: boolPtr(false)}, s.denySoulCommFirstContact(ctx, req, now, metrics, "recipient first-contact policy requires a soul sender")
		}
	}

	if prefs.FirstContactRequireReputation != nil {
		enforced = true
		rep, err := s.getSoulAgentReputation(ctx.Context(), req.agentIDHex)
		if err != nil && !theoryErrors.IsNotFound(err) {
			metrics.status = commMetricInternalError
			return soulCommSendGuardDecision{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
		}
		score := 0.0
		if rep != nil {
			score = rep.Composite
		}
		if rep == nil || score < *prefs.FirstContactRequireReputation {
			return soulCommSendGuardDecision{preferenceRespected: boolPtr(false)}, s.denySoulCommFirstContact(
				ctx,
				req,
				now,
				metrics,
				fmt.Sprintf("recipient first-contact policy requires soul reputation >= %.2f", *prefs.FirstContactRequireReputation),
			)
		}
	}

	// `introductionExpected` is currently advisory because the send contract does not yet
	// include a structured introduction field to validate against.
	if prefs.FirstContactIntroductionExpected {
		enforced = true
	}
	if enforced {
		return soulCommSendGuardDecision{preferenceRespected: boolPtr(true)}, nil
	}
	return soulCommSendGuardDecision{}, nil
}

func (s *Server) loadSoulCommFirstContactRecipient(ctx context.Context, req validatedSoulCommSendRequest) (string, *models.SoulAgentContactPreferences, *apptheory.AppTheoryError) {
	agentID, found, err := s.lookupSoulCommRecipientAgentID(ctx, req.channel, req.to)
	if err != nil {
		return "", nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !found || agentID == "" {
		return "", nil, nil
	}

	prefs, err := getSoulAgentItemBySK[models.SoulAgentContactPreferences](s, ctx, agentID, "CONTACT_PREFERENCES")
	if theoryErrors.IsNotFound(err) {
		return agentID, nil, nil
	}
	if err != nil {
		return "", nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return agentID, prefs, nil
}

func (s *Server) lookupSoulCommRecipientAgentID(ctx context.Context, channel string, to string) (string, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return "", false, errors.New("store not configured")
	}

	switch strings.ToLower(strings.TrimSpace(channel)) {
	case commChannelEmail:
		idx := &models.SoulEmailAgentIndex{Email: strings.TrimSpace(to)}
		_ = idx.UpdateKeys()
		var item models.SoulEmailAgentIndex
		err := s.store.DB.WithContext(ctx).
			Model(&models.SoulEmailAgentIndex{}).
			Where("PK", "=", idx.PK).
			Where("SK", "=", "AGENT").
			First(&item)
		if theoryErrors.IsNotFound(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		agentID := strings.ToLower(strings.TrimSpace(item.AgentID))
		return agentID, agentID != "", nil
	case commChannelSMS, commChannelVoice:
		idx := &models.SoulPhoneAgentIndex{Phone: strings.TrimSpace(to)}
		_ = idx.UpdateKeys()
		var item models.SoulPhoneAgentIndex
		err := s.store.DB.WithContext(ctx).
			Model(&models.SoulPhoneAgentIndex{}).
			Where("PK", "=", idx.PK).
			Where("SK", "=", "AGENT").
			First(&item)
		if theoryErrors.IsNotFound(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		agentID := strings.ToLower(strings.TrimSpace(item.AgentID))
		return agentID, agentID != "", nil
	default:
		return "", false, nil
	}
}

func (s *Server) denySoulCommFirstContact(ctx *apptheory.Context, req validatedSoulCommSendRequest, now time.Time, metrics *soulCommSendMetrics, message string) *apptheory.AppTheoryError {
	metrics.status = commMetricPreferenceViolation
	violationID := strings.TrimSpace(ctx.RequestID)
	if violationID == "" {
		if token, tokenErr := generateRandomSecret(8); tokenErr == nil {
			violationID = token
		}
	}
	preferenceRespected := false
	activity := &models.SoulAgentCommActivity{
		AgentID:             req.agentIDHex,
		ActivityID:          "comm-pref-deny-" + violationID,
		ChannelType:         req.channel,
		Direction:           models.SoulCommDirectionOutbound,
		Counterparty:        req.to,
		Action:              "send",
		MessageID:           "",
		InReplyTo:           "",
		BoundaryCheck:       models.SoulCommBoundaryCheckSkipped,
		PreferenceRespected: &preferenceRespected,
		Timestamp:           now,
	}
	_ = activity.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(activity).Create()
	return apptheory.NewAppTheoryError(commCodePreferenceViolation, message).WithStatusCode(http.StatusForbidden)
}

func newSoulCommSendMessageID() (string, *apptheory.AppTheoryError) {
	messageIDToken, err := generateRandomSecret(12)
	if err != nil {
		return "", apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return "comm-msg-" + messageIDToken, nil
}

func (s *Server) dispatchSoulCommSend(ctx *apptheory.Context, key *models.InstanceKey, req validatedSoulCommSendRequest, channel *models.SoulAgentChannel, now time.Time, messageID string, metrics *soulCommSendMetrics) (soulCommSendDelivery, *apptheory.AppTheoryError) {
	switch req.channel {
	case commChannelEmail:
		return s.sendSoulCommEmail(ctx, req, channel, now, messageID, metrics)
	case commChannelSMS:
		return s.sendSoulCommSMS(ctx, key, req, channel, now, messageID, metrics)
	case commChannelVoice:
		return s.sendSoulCommVoice(ctx, key, req, channel, now, messageID, metrics)
	default:
		metrics.status = commMetricProviderUnavailable
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeProviderUnavailable, "channel not supported").WithStatusCode(http.StatusServiceUnavailable)
	}
}

func (s *Server) sendSoulCommEmail(ctx *apptheory.Context, req validatedSoulCommSendRequest, channel *models.SoulAgentChannel, now time.Time, messageID string, metrics *soulCommSendMetrics) (soulCommSendDelivery, *apptheory.AppTheoryError) {
	if s.ssmGetParameter == nil || s.migaduSendSMTP == nil {
		metrics.status = commMetricProviderUnavailable
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeProviderUnavailable, "provider not configured").WithStatusCode(http.StatusServiceUnavailable)
	}

	password, err := s.ssmGetParameter(ctx.Context(), s.soulAgentEmailPasswordSSMParam(req.agentIDHex))
	if err != nil || strings.TrimSpace(password) == "" {
		metrics.status = commMetricProviderUnavailable
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeProviderUnavailable, "channel credentials not available").WithStatusCode(http.StatusServiceUnavailable)
	}

	providerMessageID := fmt.Sprintf("<%s@lessersoul.ai>", messageID)
	from := strings.TrimSpace(channel.Identifier)
	emailRaw, recipients, buildErr := buildOutboundEmailRFC5322(outboundEmailRFC5322Input{
		From:               from,
		To:                 req.to,
		CC:                 req.cc,
		BCC:                req.bcc,
		ReplyTo:            req.replyTo,
		Subject:            req.subject,
		Body:               req.body,
		MessageID:          providerMessageID,
		InReplyToMessageID: req.inReplyTo,
		SentAt:             now,
	})
	if buildErr != nil {
		return soulCommSendDelivery{}, buildErr
	}

	sendErr := s.migaduSendSMTP(ctx.Context(), from, strings.TrimSpace(password), from, recipients, emailRaw)
	if sendErr != nil {
		metrics.provider = commDeliveryProviderMigadu
		if isCommProviderUnavailable(sendErr) {
			metrics.status = commMetricProviderUnavailable
			return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeProviderUnavailable, "provider unavailable").WithStatusCode(http.StatusServiceUnavailable)
		}
		metrics.status = commMetricProviderRejected
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError("comm.provider_rejected", "provider rejected message").WithStatusCode(http.StatusBadGateway)
	}

	return soulCommSendDelivery{
		provider:          commDeliveryProviderMigadu,
		providerMessageID: providerMessageID,
		initialStatus:     models.SoulCommMessageStatusSent,
	}, nil
}

func (s *Server) sendSoulCommSMS(ctx *apptheory.Context, key *models.InstanceKey, req validatedSoulCommSendRequest, channel *models.SoulAgentChannel, now time.Time, messageID string, metrics *soulCommSendMetrics) (soulCommSendDelivery, *apptheory.AppTheoryError) {
	if s.telnyxSendSMS == nil {
		metrics.status = commMetricProviderUnavailable
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeProviderUnavailable, "provider not configured").WithStatusCode(http.StatusServiceUnavailable)
	}

	instanceSlug := strings.TrimSpace(key.InstanceSlug)
	if instanceSlug == "" {
		metrics.status = commMetricInternalError
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	if appErr := s.debitSoulSMSCredits(ctx.Context(), instanceSlug, req.agentIDHex, messageID, now, metrics); appErr != nil {
		return soulCommSendDelivery{}, appErr
	}

	providerMessageID, err := s.telnyxSendSMS(ctx.Context(), strings.TrimSpace(channel.Identifier), req.to, req.body)
	if err != nil {
		metrics.provider = commDeliveryProviderTelnyx
		metrics.status = commMetricProviderRejected
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError("comm.provider_rejected", "provider rejected message").WithStatusCode(http.StatusBadGateway)
	}

	return soulCommSendDelivery{
		provider:          commDeliveryProviderTelnyx,
		providerMessageID: providerMessageID,
		initialStatus:     models.SoulCommMessageStatusSent,
	}, nil
}

func (s *Server) sendSoulCommVoice(ctx *apptheory.Context, key *models.InstanceKey, req validatedSoulCommSendRequest, channel *models.SoulAgentChannel, now time.Time, messageID string, metrics *soulCommSendMetrics) (soulCommSendDelivery, *apptheory.AppTheoryError) {
	if s.telnyxCallVoice == nil {
		metrics.status = commMetricProviderUnavailable
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeProviderUnavailable, "provider not configured").WithStatusCode(http.StatusServiceUnavailable)
	}

	voiceInstruction := &models.SoulCommVoiceInstruction{
		MessageID: messageID,
		AgentID:   req.agentIDHex,
		From:      strings.TrimSpace(channel.Identifier),
		To:        req.to,
		Body:      req.body,
		Voice:     telnyxDefaultOutboundVoice,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := voiceInstruction.UpdateKeys(); err != nil {
		metrics.status = commMetricInternalError
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(voiceInstruction).Create(); err != nil {
		metrics.status = commMetricInternalError
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeInternal, "failed to store voice instruction").WithStatusCode(http.StatusInternalServerError)
	}

	baseURL := soulCommRequestBaseURL(ctx, s.cfg.PublicBaseURL)
	if baseURL == "" {
		metrics.status = commMetricInternalError
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError(commCodeInternal, "public base url is unavailable").WithStatusCode(http.StatusInternalServerError)
	}
	texmlURL := baseURL + "/webhooks/comm/voice/texml/" + url.PathEscape(messageID)
	statusCallbackURL := baseURL + "/webhooks/comm/voice/status/" + url.PathEscape(messageID)

	providerMessageID, err := s.telnyxCallVoice(ctx.Context(), strings.TrimSpace(channel.Identifier), req.to, texmlURL, statusCallbackURL)
	if err != nil {
		metrics.provider = commDeliveryProviderTelnyx
		metrics.status = commMetricProviderRejected
		return soulCommSendDelivery{}, apptheory.NewAppTheoryError("comm.provider_rejected", "provider rejected call").WithStatusCode(http.StatusBadGateway)
	}

	return soulCommSendDelivery{
		provider:          commDeliveryProviderTelnyx,
		providerMessageID: providerMessageID,
		initialStatus:     models.SoulCommMessageStatusAccepted,
	}, nil
}

func (s *Server) debitSoulSMSCredits(ctx context.Context, instanceSlug string, agentIDHex string, messageID string, now time.Time, metrics *soulCommSendMetrics) *apptheory.AppTheoryError {
	var inst models.Instance
	instanceErr := s.store.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", instanceSlug)).
		Where("SK", "=", models.SKMetadata).
		First(&inst)
	if instanceErr != nil && !theoryErrors.IsNotFound(instanceErr) {
		metrics.status = commMetricInternalError
		return apptheory.NewAppTheoryError(commCodeInternal, "failed to load instance").WithStatusCode(http.StatusInternalServerError)
	}
	allowOverage := strings.EqualFold(strings.TrimSpace(inst.OveragePolicy), "allow")

	month := now.UTC().Format("2006-01")
	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)
	var budget models.InstanceBudgetMonth
	budgetErr := s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if budgetErr != nil {
		if theoryErrors.IsNotFound(budgetErr) {
			metrics.provider = commDeliveryProviderTelnyx
			metrics.status = commMetricInsufficientCredits
			return apptheory.NewAppTheoryError(commCodeInsufficientCredits, "credits are not configured; purchase credits first").WithStatusCode(http.StatusConflict)
		}
		metrics.status = commMetricInternalError
		return apptheory.NewAppTheoryError(commCodeInternal, "failed to load credits budget").WithStatusCode(http.StatusInternalServerError)
	}
	if budget.IncludedCredits-budget.UsedCredits < commSMSSendCreditCost && !allowOverage {
		metrics.provider = commDeliveryProviderTelnyx
		metrics.status = commMetricInsufficientCredits
		return apptheory.NewAppTheoryError(commCodeInsufficientCredits, "insufficient credits").WithStatusCode(http.StatusConflict)
	}

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, commSMSSendCreditCost)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)
	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, messageID, "comm.sms.send", messageID, commSMSSendCreditCost),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 "comm.sms.send",
		Target:                 messageID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              messageID,
		RequestedCredits:       commSMSSendCreditCost,
		ListCredits:            commSMSSendCreditCost,
		PricingMultiplierBps:   10000,
		DebitedCredits:         commSMSSendCreditCost,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		ActorURI:               fmt.Sprintf("soul_agent:%s", agentIDHex),
		CreatedAt:              now.UTC(),
	}
	_ = ledger.UpdateKeys()

	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now.UTC(),
	}
	_ = updateBudget.UpdateKeys()

	maxUsed := budget.IncludedCredits - commSMSSendCreditCost
	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(ledger)
		if allowOverage {
			tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", commSMSSendCreditCost)
				ub.Set("UpdatedAt", now.UTC())
				return nil
			}, tabletheory.IfExists())
			return nil
		}

		tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", commSMSSendCreditCost)
			ub.Set("UpdatedAt", now.UTC())
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.ConditionExpression(
				"attribute_not_exists(usedCredits) OR usedCredits <= :max",
				map[string]any{
					":max": maxUsed,
				},
			),
		)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		metrics.provider = commDeliveryProviderTelnyx
		metrics.status = commMetricInsufficientCredits
		return apptheory.NewAppTheoryError(commCodeInsufficientCredits, "insufficient credits").WithStatusCode(http.StatusConflict)
	}
	if err != nil {
		metrics.status = commMetricInternalError
		return apptheory.NewAppTheoryError(commCodeInternal, "failed to debit credits").WithStatusCode(http.StatusInternalServerError)
	}
	return nil
}

func (s *Server) recordSoulCommSend(ctx *apptheory.Context, key *models.InstanceKey, req validatedSoulCommSendRequest, messageID string, delivery soulCommSendDelivery, decision soulCommSendGuardDecision, now time.Time, metrics *soulCommSendMetrics) *apptheory.AppTheoryError {
	statusValue := strings.TrimSpace(delivery.initialStatus)
	if statusValue == "" {
		statusValue = models.SoulCommMessageStatusSent
	}
	status := &models.SoulCommMessageStatus{
		MessageID:         messageID,
		AgentID:           req.agentIDHex,
		ChannelType:       req.channel,
		To:                req.to,
		Provider:          delivery.provider,
		ProviderMessageID: delivery.providerMessageID,
		Status:            statusValue,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	_ = status.UpdateKeys()
	createStatusErr := s.store.DB.WithContext(ctx.Context()).Model(status).Create()
	if createStatusErr != nil {
		metrics.status = commMetricInternalError
		return apptheory.NewAppTheoryError(commCodeInternal, "failed to record status").WithStatusCode(http.StatusInternalServerError)
	}

	activity := &models.SoulAgentCommActivity{
		AgentID:             req.agentIDHex,
		ActivityID:          messageID,
		ChannelType:         req.channel,
		Direction:           models.SoulCommDirectionOutbound,
		Counterparty:        req.to,
		Action:              "send",
		MessageID:           messageID,
		InReplyTo:           req.inReplyTo,
		BoundaryCheck:       models.SoulCommBoundaryCheckPassed,
		PreferenceRespected: decision.preferenceRespected,
		Timestamp:           now,
	}
	_ = activity.UpdateKeys()
	createActivityErr := s.store.DB.WithContext(ctx.Context()).Model(activity).Create()
	if createActivityErr != nil {
		metrics.status = commMetricInternalError
		return apptheory.NewAppTheoryError(commCodeInternal, "failed to record activity").WithStatusCode(http.StatusInternalServerError)
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     fmt.Sprintf("instance:%s", strings.TrimSpace(key.InstanceSlug)),
		Action:    fmt.Sprintf("soul.comm.send.%s", req.channel),
		Target:    fmt.Sprintf("soul_agent_identity:%s", req.agentIDHex),
		CreatedAt: now,
	})
	return nil
}

func soulCommSendJSON(messageID string, req validatedSoulCommSendRequest, delivery soulCommSendDelivery, now time.Time) (*apptheory.Response, *apptheory.AppTheoryError) {
	statusValue := strings.TrimSpace(delivery.initialStatus)
	if statusValue == "" {
		statusValue = models.SoulCommMessageStatusSent
	}
	resp, err := apptheory.JSON(http.StatusOK, soulCommSendResponse{
		MessageID:         messageID,
		Status:            statusValue,
		Channel:           req.channel,
		AgentID:           req.agentIDHex,
		To:                req.to,
		Provider:          delivery.provider,
		ProviderMessageID: delivery.providerMessageID,
		CreatedAt:         now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return resp, nil
}

func soulCommStatusJSON(item models.SoulCommMessageStatus) soulCommStatusResponse {
	out := soulCommStatusResponse{
		MessageID:         strings.TrimSpace(item.MessageID),
		Status:            strings.TrimSpace(item.Status),
		Channel:           strings.TrimSpace(item.ChannelType),
		AgentID:           strings.ToLower(strings.TrimSpace(item.AgentID)),
		To:                strings.TrimSpace(item.To),
		Provider:          strings.TrimSpace(item.Provider),
		ProviderMessageID: strings.TrimSpace(item.ProviderMessageID),
		ErrorCode:         strings.TrimSpace(item.ErrorCode),
		ErrorMessage:      strings.TrimSpace(item.ErrorMessage),
		ReplyMessageID:    strings.TrimSpace(item.ReplyMessageID),
		ReplyBody:         strings.TrimSpace(item.ReplyBody),
		ReplyConfidence:   item.ReplyConfidence,
		CreatedAt:         item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !item.ReplyReceivedAt.IsZero() {
		out.ReplyReceivedAt = item.ReplyReceivedAt.UTC().Format(time.RFC3339Nano)
	}
	if !item.UpdatedAt.IsZero() {
		out.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func normalizeCommPhoneE164(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, " ", "")
	raw = strings.ReplaceAll(raw, "-", "")
	raw = strings.ReplaceAll(raw, "(", "")
	raw = strings.ReplaceAll(raw, ")", "")
	raw = strings.ReplaceAll(raw, ".", "")
	return raw
}

func (s *Server) handleSoulCommStatus(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !s.cfg.SoulEnabled {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}

	key, authErr := s.requireCommInstanceKey(ctx)
	if authErr != nil {
		return nil, authErr
	}

	messageID := strings.TrimSpace(ctx.Param("messageId"))
	if messageID == "" || len(messageID) > 128 {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "messageId is invalid").WithStatusCode(http.StatusBadRequest)
	}

	rec := &models.SoulCommMessageStatus{MessageID: messageID}
	_ = rec.UpdateKeys()
	var item models.SoulCommMessageStatus
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommMessageStatus{}).
		Where("PK", "=", rec.PK).
		Where("SK", "=", rec.SK).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "message not found").WithStatusCode(http.StatusBadRequest)
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), strings.ToLower(strings.TrimSpace(item.AgentID)))
	if theoryErrors.IsNotFound(err) {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if appErr := s.requireCommAgentInstanceAccess(ctx.Context(), key, identity); appErr != nil {
		return nil, appErr
	}

	resp, err := apptheory.JSON(http.StatusOK, soulCommStatusJSON(item))
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return resp, nil
}

func (s *Server) requireCommInstanceKey(ctx *apptheory.Context) (*models.InstanceKey, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	token := httpx.BearerToken(ctx.Request.Headers)
	if token == "" {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}

	var key *models.InstanceKey
	candidates := []string{sha256HexTrimmed(token), strings.TrimSpace(token)}
	for _, id := range candidates {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}

		item, err := s.store.GetInstanceKey(ctx.Context(), id)
		if theoryErrors.IsNotFound(err) || item == nil {
			continue
		}
		if err != nil {
			return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
		}
		if !item.RevokedAt.IsZero() {
			continue
		}
		key = item
		break
	}
	if key == nil {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}

	// Best-effort: update last used timestamp.
	key.LastUsedAt = time.Now().UTC()
	_ = key.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(key).IfExists().Update("LastUsedAt")

	return key, nil
}

func (s *Server) requireCommAgentInstanceAccess(ctx context.Context, key *models.InstanceKey, identity *models.SoulAgentIdentity) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil || key == nil {
		return apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if strings.TrimSpace(key.InstanceSlug) == "" {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	normalizedDomain := strings.ToLower(strings.TrimSpace(identity.Domain))
	if normalizedDomain == "" {
		return apptheory.NewAppTheoryError("comm.invalid_request", "agent domain is invalid").WithStatusCode(http.StatusBadRequest)
	}

	d, err := s.loadManagedStageAwareDomain(ctx, normalizedDomain)
	if theoryErrors.IsNotFound(err) {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if d == nil || !strings.EqualFold(strings.TrimSpace(d.InstanceSlug), strings.TrimSpace(key.InstanceSlug)) {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	return nil
}

func (s *Server) countSoulOutboundCommSince(ctx context.Context, agentIDHex string, channelType string, since time.Time, scanLimit int) (int, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return 0, fmt.Errorf("store not configured")
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if agentIDHex == "" || channelType == "" {
		return 0, fmt.Errorf("agent and channelType are required")
	}
	if scanLimit <= 0 {
		scanLimit = 250
	}
	if scanLimit > 1000 {
		scanLimit = 1000
	}

	var items []*models.SoulAgentCommActivity
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentCommActivity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "COMM#").
		OrderBy("SK", "DESC").
		Limit(scanLimit).
		All(&items)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, item := range items {
		if item == nil {
			continue
		}
		if item.Timestamp.Before(since) {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.Direction)) != models.SoulCommDirectionOutbound {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.ChannelType)) != channelType {
			continue
		}
		count++
	}
	return count, nil
}

type outboundEmailRFC5322Input struct {
	From               string
	To                 string
	CC                 []string
	BCC                []string
	ReplyTo            string
	Subject            string
	Body               string
	MessageID          string
	InReplyToMessageID string
	SentAt             time.Time
}

func buildOutboundEmailRFC5322(input outboundEmailRFC5322Input) ([]byte, []string, *apptheory.AppTheoryError) {
	from := strings.TrimSpace(input.From)
	to := strings.TrimSpace(input.To)
	replyTo := strings.TrimSpace(input.ReplyTo)
	subject := strings.TrimSpace(input.Subject)
	body := strings.TrimRight(input.Body, "\r\n")
	messageID := strings.TrimSpace(input.MessageID)
	inReplyTo := strings.TrimSpace(input.InReplyToMessageID)

	if from == "" || to == "" || subject == "" || body == "" || messageID == "" {
		return nil, nil, apptheory.NewAppTheoryError("comm.invalid_request", "invalid email payload").WithStatusCode(http.StatusBadRequest)
	}
	if replyTo == "" {
		replyTo = from
	}

	if appErr := validateCommEmailAddress(from, "from"); appErr != nil {
		return nil, nil, appErr
	}
	if appErr := validateCommEmailAddress(to, "to"); appErr != nil {
		return nil, nil, appErr
	}
	if appErr := validateCommEmailAddress(replyTo, "replyTo"); appErr != nil {
		return nil, nil, appErr
	}

	cc := normalizeCommEmailList(input.CC)
	bcc := normalizeCommEmailList(input.BCC)

	recipients := []string{to}
	recipients = append(recipients, cc...)
	recipients = append(recipients, bcc...)
	recipients = normalizeCommEmailList(recipients)
	if len(recipients) == 0 {
		return nil, nil, apptheory.NewAppTheoryError("comm.invalid_request", "no recipients").WithStatusCode(http.StatusBadRequest)
	}

	date := input.SentAt.UTC()
	if date.IsZero() {
		date = time.Now().UTC()
	}

	headers := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Reply-To: %s", replyTo),
		fmt.Sprintf("Subject: %s", subject),
		fmt.Sprintf("Date: %s", date.Format(time.RFC1123Z)),
		fmt.Sprintf("Message-ID: %s", messageID),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
	}
	if len(cc) > 0 {
		headers = append(headers, fmt.Sprintf("Cc: %s", strings.Join(cc, ", ")))
	}
	if inReplyTo != "" {
		// Best-effort: if caller supplied a known message id token, embed it as a Message-ID reference.
		headers = append(headers, fmt.Sprintf("In-Reply-To: <%s@lessersoul.ai>", strings.Trim(inReplyTo, "<>")))
	}

	raw := strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n"
	return []byte(raw), recipients, nil
}

func validateCommEmailAddress(value string, field string) *apptheory.AppTheoryError {
	if _, err := mail.ParseAddress(value); err != nil {
		return apptheory.NewAppTheoryError(commCodeInvalidRequest, field+" must be an email address").WithStatusCode(http.StatusBadRequest)
	}
	return nil
}

func normalizeCommEmailList(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		addr, err := mail.ParseAddress(raw)
		if err != nil || addr == nil || strings.TrimSpace(addr.Address) == "" {
			continue
		}
		email := strings.TrimSpace(addr.Address)
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, email)
	}
	sort.Strings(out)
	return out
}

func normalizeCommAgentID(raw string) (string, *apptheory.AppTheoryError) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "", apptheory.NewAppTheoryError("comm.invalid_request", "agentId is required").WithStatusCode(http.StatusBadRequest)
	}
	if !strings.HasPrefix(raw, "0x") || len(raw) != 66 {
		return "", apptheory.NewAppTheoryError("comm.invalid_request", "agentId is invalid").WithStatusCode(http.StatusBadRequest)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(raw, "0x")); err != nil {
		return "", apptheory.NewAppTheoryError("comm.invalid_request", "agentId is invalid").WithStatusCode(http.StatusBadRequest)
	}
	return raw, nil
}

func isCommProviderUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func boolPtr(v bool) *bool { return &v }
