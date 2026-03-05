package controlplane

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
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
	MessageID         string `json:"messageId"`
	Status            string `json:"status"`
	Channel           string `json:"channel"`
	AgentID           string `json:"agentId"`
	To                string `json:"to"`
	Provider          string `json:"provider,omitempty"`
	ProviderMessageID string `json:"providerMessageId,omitempty"`
	ErrorCode         string `json:"errorCode,omitempty"`
	ErrorMessage      string `json:"errorMessage,omitempty"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

func (s *Server) handleSoulCommSend(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	stage := strings.TrimSpace(s.cfg.Stage)
	if stage == "" {
		stage = "lab"
	}
	metricsInstance := strings.TrimSpace(key.InstanceSlug)
	if metricsInstance == "" {
		metricsInstance = "unknown"
	}
	metricsChannel := "unknown"
	metricsProvider := "unknown"
	metricsStatus := "unknown"
	defer func() {
		hostmetrics.Emit("lesser-host", map[string]string{
			"Stage":    stage,
			"Service":  ServiceName,
			"Instance": metricsInstance,
			"Channel":  strings.TrimSpace(metricsChannel),
			"Provider": strings.TrimSpace(metricsProvider),
			"Status":   strings.TrimSpace(metricsStatus),
		}, []hostmetrics.Metric{
			{Name: "CommOutboundRequests", Unit: hostmetrics.UnitCount, Value: 1},
		}, nil)
	}()

	var req soulCommSendRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		metricsStatus = "invalid_request"
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "invalid request").WithStatusCode(http.StatusBadRequest)
	}
	req.Channel = strings.ToLower(strings.TrimSpace(req.Channel))
	if req.Channel != "" {
		metricsChannel = req.Channel
	}

	if req.Channel != "email" && req.Channel != "sms" && req.Channel != "voice" {
		metricsStatus = "invalid_request"
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "channel is invalid").WithStatusCode(http.StatusBadRequest)
	}
	agentIDHex, idErr := normalizeCommAgentID(req.AgentID)
	if idErr != nil {
		metricsStatus = "invalid_request"
		return nil, idErr
	}

	to := strings.TrimSpace(req.To)
	if to == "" {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "to is required").WithStatusCode(http.StatusBadRequest)
	}
	if req.Channel == "email" {
		if _, err := mail.ParseAddress(to); err != nil {
			return nil, apptheory.NewAppTheoryError("comm.invalid_request", "to must be an email address").WithStatusCode(http.StatusBadRequest)
		}
		subject := strings.TrimSpace(req.Subject)
		if subject == "" {
			return nil, apptheory.NewAppTheoryError("comm.invalid_request", "subject is required for email").WithStatusCode(http.StatusBadRequest)
		}
	} else {
		to = normalizeCommPhoneE164(to)
		if !soulE164Regex.MatchString(to) {
			return nil, apptheory.NewAppTheoryError("comm.invalid_request", "to must be an E.164 phone number").WithStatusCode(http.StatusBadRequest)
		}
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "body is required").WithStatusCode(http.StatusBadRequest)
	}

	// Minimal boundary enforcement MVP: require inReplyTo for outbound communication.
	inReplyTo := ""
	if req.InReplyTo != nil {
		inReplyTo = strings.TrimSpace(*req.InReplyTo)
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		metricsStatus = "unauthorized"
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	effectiveStatus := strings.TrimSpace(identity.LifecycleStatus)
	if effectiveStatus == "" {
		effectiveStatus = strings.TrimSpace(identity.Status)
	}
	if effectiveStatus != models.SoulAgentStatusActive {
		metricsStatus = "agent_not_active"
		return nil, apptheory.NewAppTheoryError("comm.agent_not_active", "agent is not active").WithStatusCode(http.StatusConflict)
	}

	if appErr := s.requireCommAgentInstanceAccess(ctx.Context(), key, identity); appErr != nil {
		metricsStatus = "unauthorized"
		return nil, appErr
	}

	channelSK := "CHANNEL#email"
	if req.Channel == "sms" || req.Channel == "voice" {
		channelSK = "CHANNEL#phone"
	}

	ch, chErr := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, channelSK)
	if chErr != nil {
		if theoryErrors.IsNotFound(chErr) {
			metricsStatus = "channel_not_provisioned"
			return nil, apptheory.NewAppTheoryError("comm.channel_not_provisioned", "channel is not provisioned").WithStatusCode(http.StatusConflict)
		}
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if ch == nil || strings.TrimSpace(ch.Identifier) == "" || ch.ProvisionedAt.IsZero() || !ch.DeprovisionedAt.IsZero() || strings.TrimSpace(ch.Status) != models.SoulChannelStatusActive {
		metricsStatus = "channel_not_provisioned"
		return nil, apptheory.NewAppTheoryError("comm.channel_not_provisioned", "channel is not provisioned").WithStatusCode(http.StatusConflict)
	}
	if !ch.Verified {
		metricsStatus = "channel_unverified"
		return nil, apptheory.NewAppTheoryError("comm.channel_unverified", "channel is not verified").WithStatusCode(http.StatusConflict)
	}

	// Basic outbound rate limiting MVP.
	now := time.Now().UTC()
	hourCount, countErr := s.countSoulOutboundCommSince(ctx.Context(), agentIDHex, req.Channel, now.Add(-1*time.Hour), 250)
	if countErr != nil {
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	dayCount, countErr := s.countSoulOutboundCommSince(ctx.Context(), agentIDHex, req.Channel, now.Add(-24*time.Hour), 500)
	if countErr != nil {
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	maxHour, maxDay := 50, 500
	if req.Channel == "sms" {
		maxHour, maxDay = 20, 200
	}
	if hourCount >= maxHour || dayCount >= maxDay {
		metricsStatus = "rate_limited"
		return nil, apptheory.NewAppTheoryError("comm.rate_limited", "rate limited").WithStatusCode(http.StatusTooManyRequests)
	}

	if inReplyTo == "" {
		metricsStatus = "boundary_violation"
		violationID := strings.TrimSpace(ctx.RequestID)
		if violationID == "" {
			if token, err := generateRandomSecret(8); err == nil {
				violationID = token
			}
		}
		activity := &models.SoulAgentCommActivity{
			AgentID:       agentIDHex,
			ActivityID:    "comm-violation-" + violationID,
			ChannelType:   req.Channel,
			Direction:     models.SoulCommDirectionOutbound,
			Counterparty:  to,
			Action:        "send",
			MessageID:     "",
			InReplyTo:     "",
			BoundaryCheck: models.SoulCommBoundaryCheckViolated,
			Timestamp:     now,
		}
		_ = activity.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(activity).Create()

		return nil, apptheory.NewAppTheoryError("comm.boundary_violation", "unsolicited outbound communication is not allowed; inReplyTo is required").WithStatusCode(http.StatusForbidden)
	}

	messageIDToken, err := generateRandomSecret(12)
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	messageID := "comm-msg-" + messageIDToken

	provider := ""
	providerMessageID := ""

	switch req.Channel {
	case "email":
		if s.ssmGetParameter == nil || s.migaduSendSMTP == nil {
			metricsStatus = "provider_unavailable"
			return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "provider not configured").WithStatusCode(http.StatusServiceUnavailable)
		}

		password, err := s.ssmGetParameter(ctx.Context(), s.soulAgentEmailPasswordSSMParam(agentIDHex))
		if err != nil || strings.TrimSpace(password) == "" {
			metricsStatus = "provider_unavailable"
			return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "channel credentials not available").WithStatusCode(http.StatusServiceUnavailable)
		}

		// RFC 5322 Message-ID uses angle brackets.
		providerMessageID = fmt.Sprintf("<%s@lessersoul.ai>", messageID)

		emailRaw, recipients, buildErr := buildOutboundEmailRFC5322(outboundEmailRFC5322Input{
			From:               strings.TrimSpace(ch.Identifier),
			To:                 to,
			CC:                 req.CC,
			BCC:                req.BCC,
			ReplyTo:            strings.TrimSpace(req.ReplyTo),
			Subject:            strings.TrimSpace(req.Subject),
			Body:               body,
			MessageID:          providerMessageID,
			InReplyToMessageID: inReplyTo,
			SentAt:             now,
		})
		if buildErr != nil {
			return nil, buildErr
		}

		if err := s.migaduSendSMTP(ctx.Context(), strings.TrimSpace(ch.Identifier), strings.TrimSpace(password), strings.TrimSpace(ch.Identifier), recipients, emailRaw); err != nil {
			if isCommProviderUnavailable(err) {
				metricsProvider = "migadu"
				metricsStatus = "provider_unavailable"
				return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "provider unavailable").WithStatusCode(http.StatusServiceUnavailable)
			}
			metricsProvider = "migadu"
			metricsStatus = "provider_rejected"
			return nil, apptheory.NewAppTheoryError("comm.provider_rejected", "provider rejected message").WithStatusCode(http.StatusBadGateway)
		}
		provider = "migadu"

	case "sms":
		if s.telnyxSendSMS == nil {
			metricsStatus = "provider_unavailable"
			return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "provider not configured").WithStatusCode(http.StatusServiceUnavailable)
		}

		instanceSlug := strings.TrimSpace(key.InstanceSlug)
		if instanceSlug == "" {
			metricsStatus = "internal_error"
			return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
		}

		var inst models.Instance
		err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.Instance{}).
			Where("PK", "=", fmt.Sprintf("INSTANCE#%s", instanceSlug)).
			Where("SK", "=", models.SKMetadata).
			First(&inst)
		if err != nil && !theoryErrors.IsNotFound(err) {
			metricsStatus = "internal_error"
			return nil, apptheory.NewAppTheoryError("comm.internal", "failed to load instance").WithStatusCode(http.StatusInternalServerError)
		}
		allowOverage := strings.EqualFold(strings.TrimSpace(inst.OveragePolicy), "allow")

		month := now.UTC().Format("2006-01")
		pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
		sk := fmt.Sprintf("BUDGET#%s", month)
		var budget models.InstanceBudgetMonth
		if err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.InstanceBudgetMonth{}).
			Where("PK", "=", pk).
			Where("SK", "=", sk).
			ConsistentRead().
			First(&budget); err != nil {
			if theoryErrors.IsNotFound(err) {
				metricsProvider = "telnyx"
				metricsStatus = "insufficient_credits"
				return nil, apptheory.NewAppTheoryError("comm.insufficient_credits", "credits are not configured; purchase credits first").WithStatusCode(http.StatusConflict)
			}
			metricsStatus = "internal_error"
			return nil, apptheory.NewAppTheoryError("comm.internal", "failed to load credits budget").WithStatusCode(http.StatusInternalServerError)
		}

		remaining := budget.IncludedCredits - budget.UsedCredits
		if remaining < commSMSSendCreditCost && !allowOverage {
			metricsProvider = "telnyx"
			metricsStatus = "insufficient_credits"
			return nil, apptheory.NewAppTheoryError("comm.insufficient_credits", "insufficient credits").WithStatusCode(http.StatusConflict)
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
		err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
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
			metricsProvider = "telnyx"
			metricsStatus = "insufficient_credits"
			return nil, apptheory.NewAppTheoryError("comm.insufficient_credits", "insufficient credits").WithStatusCode(http.StatusConflict)
		}
		if err != nil {
			metricsStatus = "internal_error"
			return nil, apptheory.NewAppTheoryError("comm.internal", "failed to debit credits").WithStatusCode(http.StatusInternalServerError)
		}

		providerMessageID, err = s.telnyxSendSMS(ctx.Context(), strings.TrimSpace(ch.Identifier), to, body)
		if err != nil {
			metricsProvider = "telnyx"
			metricsStatus = "provider_rejected"
			return nil, apptheory.NewAppTheoryError("comm.provider_rejected", "provider rejected message").WithStatusCode(http.StatusBadGateway)
		}
		provider = "telnyx"

	case "voice":
		metricsStatus = "provider_unavailable"
		return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "channel not supported").WithStatusCode(http.StatusServiceUnavailable)
	}

	status := &models.SoulCommMessageStatus{
		MessageID:         messageID,
		AgentID:           agentIDHex,
		ChannelType:       req.Channel,
		To:                to,
		Provider:          provider,
		ProviderMessageID: providerMessageID,
		Status:            models.SoulCommMessageStatusSent,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	_ = status.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(status).Create(); err != nil {
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "failed to record status").WithStatusCode(http.StatusInternalServerError)
	}

	activity := &models.SoulAgentCommActivity{
		AgentID:       agentIDHex,
		ActivityID:    messageID,
		ChannelType:   req.Channel,
		Direction:     models.SoulCommDirectionOutbound,
		Counterparty:  to,
		Action:        "send",
		MessageID:     messageID,
		InReplyTo:     inReplyTo,
		BoundaryCheck: models.SoulCommBoundaryCheckPassed,
		Timestamp:     now,
	}
	_ = activity.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(activity).Create(); err != nil {
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "failed to record activity").WithStatusCode(http.StatusInternalServerError)
	}

	metricsProvider = provider
	metricsStatus = "sent"

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     fmt.Sprintf("instance:%s", strings.TrimSpace(key.InstanceSlug)),
		Action:    fmt.Sprintf("soul.comm.send.%s", strings.TrimSpace(req.Channel)),
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		CreatedAt: now,
	})

	resp, err := apptheory.JSON(http.StatusOK, soulCommSendResponse{
		MessageID:         messageID,
		Status:            models.SoulCommMessageStatusSent,
		Channel:           req.Channel,
		AgentID:           agentIDHex,
		To:                to,
		Provider:          provider,
		ProviderMessageID: providerMessageID,
		CreatedAt:         now.Format(time.RFC3339Nano),
	})
	if err != nil {
		metricsStatus = "internal_error"
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return resp, nil
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
		CreatedAt:         item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !item.UpdatedAt.IsZero() {
		out.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}

	resp, err := apptheory.JSON(http.StatusOK, out)
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

	var d models.Domain
	err := s.store.DB.WithContext(ctx).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", normalizedDomain)).
		Where("SK", "=", models.SKMetadata).
		First(&d)
	if theoryErrors.IsNotFound(err) {
		return apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !strings.EqualFold(strings.TrimSpace(d.InstanceSlug), strings.TrimSpace(key.InstanceSlug)) {
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

	if _, err := mail.ParseAddress(from); err != nil {
		return nil, nil, apptheory.NewAppTheoryError("comm.invalid_request", "from must be an email address").WithStatusCode(http.StatusBadRequest)
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return nil, nil, apptheory.NewAppTheoryError("comm.invalid_request", "to must be an email address").WithStatusCode(http.StatusBadRequest)
	}
	if _, err := mail.ParseAddress(replyTo); err != nil {
		return nil, nil, apptheory.NewAppTheoryError("comm.invalid_request", "replyTo must be an email address").WithStatusCode(http.StatusBadRequest)
	}

	normalizeList := func(in []string) []string {
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

	cc := normalizeList(input.CC)
	bcc := normalizeList(input.BCC)

	recipients := []string{to}
	recipients = append(recipients, cc...)
	recipients = append(recipients, bcc...)
	recipients = normalizeList(recipients)
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
