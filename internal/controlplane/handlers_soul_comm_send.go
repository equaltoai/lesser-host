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
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
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

	var req soulCommSendRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "invalid request").WithStatusCode(http.StatusBadRequest)
	}
	req.Channel = strings.ToLower(strings.TrimSpace(req.Channel))

	if req.Channel != "email" && req.Channel != "sms" && req.Channel != "voice" {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "channel is invalid").WithStatusCode(http.StatusBadRequest)
	}
	agentIDHex, idErr := normalizeCommAgentID(req.AgentID)
	if idErr != nil {
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
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return nil, apptheory.NewAppTheoryError("comm.invalid_request", "body is required").WithStatusCode(http.StatusBadRequest)
	}

	// M5 MVP: email only.
	if req.Channel != "email" {
		return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "channel not supported").WithStatusCode(http.StatusServiceUnavailable)
	}

	// Minimal boundary enforcement MVP: require inReplyTo for outbound email.
	inReplyTo := ""
	if req.InReplyTo != nil {
		inReplyTo = strings.TrimSpace(*req.InReplyTo)
	}
	if inReplyTo == "" {
		return nil, apptheory.NewAppTheoryError("comm.boundary_violation", "unsolicited outbound email is not allowed; inReplyTo is required").WithStatusCode(http.StatusForbidden)
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	effectiveStatus := strings.TrimSpace(identity.LifecycleStatus)
	if effectiveStatus == "" {
		effectiveStatus = strings.TrimSpace(identity.Status)
	}
	if effectiveStatus != models.SoulAgentStatusActive {
		return nil, apptheory.NewAppTheoryError("comm.agent_not_active", "agent is not active").WithStatusCode(http.StatusConflict)
	}

	if appErr := s.requireCommAgentInstanceAccess(ctx.Context(), key, identity); appErr != nil {
		return nil, appErr
	}

	ch, chErr := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx.Context(), agentIDHex, "CHANNEL#email")
	if chErr != nil {
		if theoryErrors.IsNotFound(chErr) {
			return nil, apptheory.NewAppTheoryError("comm.channel_not_provisioned", "channel is not provisioned").WithStatusCode(http.StatusConflict)
		}
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if ch == nil || strings.TrimSpace(ch.Identifier) == "" || ch.ProvisionedAt.IsZero() || !ch.DeprovisionedAt.IsZero() || strings.TrimSpace(ch.Status) != models.SoulChannelStatusActive {
		return nil, apptheory.NewAppTheoryError("comm.channel_not_provisioned", "channel is not provisioned").WithStatusCode(http.StatusConflict)
	}
	if !ch.Verified {
		return nil, apptheory.NewAppTheoryError("comm.channel_unverified", "channel is not verified").WithStatusCode(http.StatusConflict)
	}

	// Basic outbound rate limiting MVP.
	now := time.Now().UTC()
	hourCount, countErr := s.countSoulOutboundCommSince(ctx.Context(), agentIDHex, "email", now.Add(-1*time.Hour), 250)
	if countErr != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	dayCount, countErr := s.countSoulOutboundCommSince(ctx.Context(), agentIDHex, "email", now.Add(-24*time.Hour), 500)
	if countErr != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if hourCount >= 50 || dayCount >= 500 {
		return nil, apptheory.NewAppTheoryError("comm.rate_limited", "rate limited").WithStatusCode(http.StatusTooManyRequests)
	}
	if s.ssmGetParameter == nil || s.migaduSendSMTP == nil {
		return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "provider not configured").WithStatusCode(http.StatusServiceUnavailable)
	}

	password, err := s.ssmGetParameter(ctx.Context(), s.soulAgentEmailPasswordSSMParam(agentIDHex))
	if err != nil || strings.TrimSpace(password) == "" {
		return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "channel credentials not available").WithStatusCode(http.StatusServiceUnavailable)
	}

	messageIDToken, err := generateRandomSecret(12)
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	messageID := "comm-msg-" + messageIDToken
	// RFC 5322 Message-ID uses angle brackets.
	providerMessageID := fmt.Sprintf("<%s@lessersoul.ai>", messageID)

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
			return nil, apptheory.NewAppTheoryError("comm.provider_unavailable", "provider unavailable").WithStatusCode(http.StatusServiceUnavailable)
		}
		return nil, apptheory.NewAppTheoryError("comm.provider_rejected", "provider rejected message").WithStatusCode(http.StatusBadGateway)
	}

	status := &models.SoulCommMessageStatus{
		MessageID:         messageID,
		AgentID:           agentIDHex,
		ChannelType:       "email",
		To:                to,
		Provider:          "migadu",
		ProviderMessageID: providerMessageID,
		Status:            models.SoulCommMessageStatusSent,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	_ = status.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(status).Create(); err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "failed to record status").WithStatusCode(http.StatusInternalServerError)
	}

	activity := &models.SoulAgentCommActivity{
		AgentID:       agentIDHex,
		ActivityID:    messageID,
		ChannelType:   "email",
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
		return nil, apptheory.NewAppTheoryError("comm.internal", "failed to record activity").WithStatusCode(http.StatusInternalServerError)
	}

	resp, err := apptheory.JSON(http.StatusOK, soulCommSendResponse{
		MessageID:         messageID,
		Status:            models.SoulCommMessageStatusSent,
		Channel:           "email",
		AgentID:           agentIDHex,
		To:                to,
		Provider:          "migadu",
		ProviderMessageID: providerMessageID,
		CreatedAt:         now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	return resp, nil
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
	key, err := s.store.GetInstanceKey(ctx.Context(), token)
	if theoryErrors.IsNotFound(err) || key == nil {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !key.RevokedAt.IsZero() {
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
