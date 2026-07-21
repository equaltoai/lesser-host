package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulMintInstanceReadListDefaultLimit        = 20
	soulMintInstanceReadListMaxLimit            = 50
	soulMintInstanceReadConversationIDMaxLength = 128
	soulMintInstanceReadListMaxBytes            = 1024 * 1024
	soulMintInstanceReadSingleMaxBytes          = 2 * 1024 * 1024

	soulMintInstanceReadRouteList    = "mint_conversation_list"
	soulMintInstanceReadRouteSingle  = "mint_conversation_single"
	soulMintInstanceReadRouteRecover = "mint_conversation_recover"

	soulMintInstanceReadCodeInvalidRequest    = "soul_mint.invalid_request"
	soulMintInstanceReadCodeUnauthorized      = "soul_mint.unauthorized"
	soulMintInstanceReadCodeBoundaryViolation = "soul_mint.boundary_violation"
	soulMintInstanceReadCodeNotFound          = "soul_mint.not_found"
	soulMintInstanceReadCodeConflict          = "soul_mint.conflict"
	soulMintInstanceReadCodeRateLimited       = "soul_mint.rate_limited"
	soulMintInstanceReadCodeResponseTooLarge  = "soul_mint.response_too_large"
	soulMintInstanceReadCodeInternal          = "soul_mint.internal"

	soulMintInstanceReadJSONContentType    = "application/json; charset=utf-8"
	soulMintInstanceReadHeaderContentType  = "content-type"
	soulMintInstanceReadMessageRateLimited = "rate limited"
	soulMintInstanceReadEnvelopeError      = "error"
	soulMintInstanceReadEnvelopeMessage    = "message"

	soulMintInstanceReadDetailField    = "field"
	soulMintInstanceReadDetailReason   = "reason"
	soulMintInstanceReadDetailBoundary = "boundary"

	soulMintInstanceReadFieldAgentID        = "agentId"
	soulMintInstanceReadFieldConversationID = "conversationId"
	soulMintInstanceReadFieldLimit          = "limit"
	soulMintInstanceReadFieldRequestBody    = "body"

	soulMintInstanceReadBoundaryInstanceDomain = "instance_domain"

	soulMintInstanceReadReasonInvalidAgentID        = "invalid_agent_id"
	soulMintInstanceReadReasonInvalidConversationID = "invalid_conversation_id"
	soulMintInstanceReadReasonInvalidLimit          = "invalid_limit"
	soulMintInstanceReadReasonRequestBodyPresent    = "request_body_present"
	soulMintInstanceReadReasonTenantMismatch        = "tenant_domain_mismatch"
	soulMintInstanceReadReasonDomainNotVerified     = "domain_not_verified"
	soulMintInstanceReadReasonResponseTooLarge      = "response_too_large"

	soulMintAppErrCodeNotFound = "app.not_found"
	soulMintAppErrCodeConflict = "app.conflict"
	soulMintAppErrCodeInternal = "app.internal"
)

type soulInstanceMintConversationsResponse struct {
	Version       string                                `json:"version"`
	Conversations []soulInstanceMintConversationSummary `json:"conversations"`
	Count         int                                   `json:"count"`
	Limit         int                                   `json:"limit"`
}

type soulInstanceMintConversationSummary struct {
	AgentID        string         `json:"agent_id"`
	ConversationID string         `json:"conversation_id"`
	Model          string         `json:"model,omitempty"`
	Status         string         `json:"status"`
	Usage          models.AIUsage `json:"usage,omitempty"`
	ChargedCredits int64          `json:"charged_credits,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
}

type mintConversationInstanceReadContext struct {
	key        *models.InstanceKey
	identity   *models.SoulAgentIdentity
	agentIDHex string
}

func (s *Server) requireMintConversationInstanceReadContext(ctx *apptheory.Context) (mintConversationInstanceReadContext, *apptheory.AppTheoryError) {
	if appErr := requireStoreDB(s); appErr != nil {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadErrorFromAppError(appErr)
	}
	if ctx == nil {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadError(soulMintInstanceReadCodeInvalidRequest, "invalid request", http.StatusBadRequest, nil)
	}
	if len(bytes.TrimSpace(ctx.Request.Body)) > 0 {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadError(
			soulMintInstanceReadCodeInvalidRequest,
			"request body is not allowed for this read",
			http.StatusBadRequest,
			soulMintInstanceReadDetails(soulMintInstanceReadFieldRequestBody, soulMintInstanceReadReasonRequestBodyPresent, ""),
		)
	}

	key, appErr := s.requireSoulMintInstanceReadKey(ctx)
	if appErr != nil {
		return mintConversationInstanceReadContext{}, appErr
	}
	if !s.cfg.SoulEnabled {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadErrorFromAppError(newAppTheoryError(appErrCodeConflict, "soul registry is not configured"))
	}

	agentIDHex, _, parseErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if parseErr != nil {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadError(
			soulMintInstanceReadCodeInvalidRequest,
			"agentId is invalid",
			http.StatusBadRequest,
			soulMintInstanceReadDetails(soulMintInstanceReadFieldAgentID, soulMintInstanceReadReasonInvalidAgentID, ""),
		)
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadError(soulMintInstanceReadCodeNotFound, "agent not found", http.StatusNotFound, nil)
	}
	if err != nil {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}

	if accessErr := s.requireSoulAgentInstanceAccess(ctx.Context(), key.InstanceSlug, identity); accessErr != nil {
		return mintConversationInstanceReadContext{}, soulMintInstanceReadAccessError(accessErr)
	}

	return mintConversationInstanceReadContext{
		key:        key,
		identity:   identity,
		agentIDHex: agentIDHex,
	}, nil
}

func (s *Server) requireSoulMintInstanceReadKey(ctx *apptheory.Context) (*models.InstanceKey, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	token := httpx.BearerToken(ctx.Request.Headers)
	if token == "" {
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
	}
	tokenHash := sha256HexTrimmed(token)
	if key := soulMintConversationInstanceReadKeyFromContext(ctx); soulMintConversationInstanceReadKeyActiveForHash(key, tokenHash) {
		s.updateSoulMintInstanceReadKeyLastUsed(ctx, key)
		return key, nil
	}

	key, err := s.store.GetInstanceKey(ctx.Context(), tokenHash)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return nil, soulMintInstanceReadError(soulMintInstanceReadCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
		}
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	if key == nil || !key.RevokedAt.IsZero() {
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
	}

	s.updateSoulMintInstanceReadKeyLastUsed(ctx, key)
	return key, nil
}

func (s *Server) updateSoulMintInstanceReadKeyLastUsed(ctx *apptheory.Context, key *models.InstanceKey) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || key == nil {
		return
	}
	key.LastUsedAt = time.Now().UTC()
	_ = key.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(key).IfExists().Update("LastUsedAt")
}

func soulMintInstanceReadErrorFromAppError(appErr *apptheory.AppTheoryError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	switch appErr.Code {
	case appErrCodeBadRequest:
		return soulMintInstanceReadError(soulMintInstanceReadCodeInvalidRequest, appErr.Message, http.StatusBadRequest, nil)
	case soulMintAppErrCodeNotFound:
		return soulMintInstanceReadError(soulMintInstanceReadCodeNotFound, appErr.Message, http.StatusNotFound, nil)
	case appErrCodeUnauthorized:
		return soulMintInstanceReadError(soulMintInstanceReadCodeUnauthorized, "unauthorized", http.StatusUnauthorized, nil)
	case soulMintAppErrCodeConflict:
		return soulMintInstanceReadError(soulMintInstanceReadCodeConflict, appErr.Message, http.StatusConflict, nil)
	default:
		return soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
}

func soulMintInstanceReadAccessError(appErr *apptheory.AppTheoryError) *apptheory.AppTheoryError {
	if appErr == nil {
		return nil
	}
	if appErr.Code == soulMintAppErrCodeInternal {
		return soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}
	reason := soulMintInstanceReadReasonTenantMismatch
	if appErr.Code == soulMintAppErrCodeConflict {
		reason = soulMintInstanceReadReasonDomainNotVerified
	}
	return soulMintInstanceReadError(
		soulMintInstanceReadCodeBoundaryViolation,
		"agent is outside the authenticated instance boundary",
		http.StatusForbidden,
		soulMintInstanceReadDetails(soulMintInstanceReadFieldAgentID, reason, soulMintInstanceReadBoundaryInstanceDomain),
	)
}

func soulMintInstanceReadError(code string, message string, status int, details map[string]any) *apptheory.AppTheoryError {
	err := apptheory.NewAppTheoryError(code, message).WithStatusCode(status)
	if len(details) > 0 {
		err = err.WithDetails(details)
	}
	return err
}

func soulMintInstanceReadDetails(field string, reason string, boundary string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(field) != "" {
		out[soulMintInstanceReadDetailField] = strings.TrimSpace(field)
	}
	if strings.TrimSpace(reason) != "" {
		out[soulMintInstanceReadDetailReason] = strings.TrimSpace(reason)
	}
	if strings.TrimSpace(boundary) != "" {
		out[soulMintInstanceReadDetailBoundary] = strings.TrimSpace(boundary)
	}
	return out
}

func requireSoulMintInstanceReadConversationID(ctx *apptheory.Context) (string, *apptheory.AppTheoryError) {
	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if !soulMintInstanceReadConversationIDSafe(conversationID) {
		return "", soulMintInstanceReadError(
			soulMintInstanceReadCodeInvalidRequest,
			"conversationId is invalid",
			http.StatusBadRequest,
			soulMintInstanceReadDetails(soulMintInstanceReadFieldConversationID, soulMintInstanceReadReasonInvalidConversationID, ""),
		)
	}
	return conversationID, nil
}

func soulMintInstanceReadConversationIDSafe(conversationID string) bool {
	if conversationID == "" || len(conversationID) > soulMintInstanceReadConversationIDMaxLength {
		return false
	}
	for _, r := range conversationID {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return false
		}
	}
	return true
}

func parseSoulMintInstanceReadLimit(ctx *apptheory.Context) (int, *apptheory.AppTheoryError) {
	raw := strings.TrimSpace(queryFirst(ctx, "limit"))
	if raw == "" {
		return soulMintInstanceReadListDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > soulMintInstanceReadListMaxLimit {
		return 0, soulMintInstanceReadError(
			soulMintInstanceReadCodeInvalidRequest,
			"limit is invalid",
			http.StatusBadRequest,
			soulMintInstanceReadDetails(soulMintInstanceReadFieldLimit, soulMintInstanceReadReasonInvalidLimit, ""),
		)
	}
	return n, nil
}

func (s *Server) listSoulAgentMintConversationSummaries(ctx context.Context, agentIDHex string, limit int) ([]soulInstanceMintConversationSummary, *apptheory.AppTheoryError) {
	items, appErr := s.listSoulAgentMintConversationsWithoutPrivateDecode(ctx, agentIDHex, limit)
	if appErr != nil {
		return nil, appErr
	}
	summaries := make([]soulInstanceMintConversationSummary, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		summaries = append(summaries, soulMintConversationSummaryFromModel(item))
	}
	return summaries, nil
}

func (s *Server) listSoulAgentMintConversationsWithoutPrivateDecode(ctx context.Context, agentIDHex string, limit int) ([]*models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
	}

	var items []*models.SoulAgentMintConversation
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentMintConversation{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "MINT_CONVERSATION#").
		All(&items); err != nil && !theoryErrors.IsNotFound(err) {
		return nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "failed to list mint conversations", http.StatusInternalServerError, nil)
	}

	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left == nil || right == nil {
			return right == nil
		}
		if left.CreatedAt.Equal(right.CreatedAt) {
			return strings.TrimSpace(left.ConversationID) > strings.TrimSpace(right.ConversationID)
		}
		return left.CreatedAt.After(right.CreatedAt)
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func soulMintConversationSummaryFromModel(item *models.SoulAgentMintConversation) soulInstanceMintConversationSummary {
	if item == nil {
		return soulInstanceMintConversationSummary{}
	}
	var completedAt *time.Time
	if !item.CompletedAt.IsZero() {
		t := item.CompletedAt.UTC()
		completedAt = &t
	}
	return soulInstanceMintConversationSummary{
		AgentID:        strings.TrimSpace(item.AgentID),
		ConversationID: strings.TrimSpace(item.ConversationID),
		Model:          strings.TrimSpace(item.Model),
		Status:         strings.TrimSpace(item.Status),
		Usage:          item.Usage,
		ChargedCredits: item.ChargedCredits,
		CreatedAt:      item.CreatedAt.UTC(),
		CompletedAt:    completedAt,
	}
}

func (s *Server) handleSoulInstanceListMintConversations(ctx *apptheory.Context) (*apptheory.Response, error) {
	start := time.Now()
	reqCtx, appErr := s.requireMintConversationInstanceReadContext(ctx)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, nil, ctxParam(ctx, "agentId"), "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	limit, appErr := parseSoulMintInstanceReadLimit(ctx)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	items, appErr := s.listSoulAgentMintConversationSummaries(ctx.Context(), reqCtx.agentIDHex, limit)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	resp, err := soulMintInstanceReadJSON(http.StatusOK, soulInstanceMintConversationsResponse{
		Version:       "1",
		Conversations: items,
		Count:         len(items),
		Limit:         limit,
	}, soulMintInstanceReadListMaxBytes)
	if err != nil {
		appErr := soulMintInstanceReadResponseError(err)
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}
	s.recordSoulMintInstanceReadAudit(ctx, reqCtx.key, reqCtx.agentIDHex, "", soulMintInstanceReadRouteList, "success", resp.Status, len(resp.Body), start)
	return resp, nil
}

func (s *Server) handleSoulInstanceGetMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	start := time.Now()
	reqCtx, appErr := s.requireMintConversationInstanceReadContext(ctx)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, nil, ctxParam(ctx, "agentId"), ctxParam(ctx, "conversationId"), soulMintInstanceReadRouteSingle, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}
	conversationID, appErr := requireSoulMintInstanceReadConversationID(ctx)
	if appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, ctxParam(ctx, "conversationId"), soulMintInstanceReadRouteSingle, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	session, err := s.store.GetHostedGenesisSession(ctx.Context(), reqCtx.key.InstanceSlug, conversationID)
	if err != nil || session == nil || !strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(reqCtx.agentIDHex)) {
		appErr := soulMintInstanceReadError(soulMintInstanceReadCodeNotFound, "conversation not found", http.StatusNotFound, nil)
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, conversationID, soulMintInstanceReadRouteSingle, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}
	conv, convErr := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), reqCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if convErr == nil && conv != nil {
		decodeMintConversationFields(conv)
	}
	promotion, promotionErr := s.loadSoulAgentPromotionForPublishedConvergence(ctx.Context(), reqCtx.agentIDHex)
	if promotionErr != nil {
		return nil, soulMintInstanceReadErrorFromAppError(promotionErr)
	}
	if _, convergeErr := s.convergeHostedGenesisPublished(ctx.Context(), session, conv, reqCtx.identity, promotion); convergeErr != nil {
		return nil, soulMintInstanceReadErrorFromAppError(convergeErr)
	}

	if appErr := rejectOversizeSoulMintInstanceConversation(conv); appErr != nil {
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, conversationID, soulMintInstanceReadRouteSingle, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}

	resp, jsonErr := hostedGenesisConversationJSONFromSession(http.StatusOK, session, conv, hostedGenesisProjectionOptions{
		RegistrationID:  session.RegistrationID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if jsonErr != nil {
		appErr := soulMintInstanceReadResponseError(jsonErr)
		s.logSoulMintInstanceReadAccess(ctx, reqCtx.key, reqCtx.agentIDHex, conversationID, soulMintInstanceReadRouteSingle, "error", appErr.StatusCode, 0, start)
		return nil, appErr
	}
	s.recordSoulMintInstanceReadAudit(ctx, reqCtx.key, reqCtx.agentIDHex, conversationID, soulMintInstanceReadRouteSingle, "success", resp.Status, len(resp.Body), start)
	return resp, nil
}

func rejectOversizeSoulMintInstanceConversation(conv *models.SoulAgentMintConversation) *apptheory.AppTheoryError {
	if conv == nil {
		return nil
	}
	if len(conv.Messages)+len(conv.ProducedDeclarations) > soulMintInstanceReadSingleMaxBytes {
		return soulMintInstanceReadError(
			soulMintInstanceReadCodeResponseTooLarge,
			"mint conversation response is too large",
			http.StatusRequestEntityTooLarge,
			soulMintInstanceReadDetails("", soulMintInstanceReadReasonResponseTooLarge, ""),
		)
	}
	return nil
}

func soulMintInstanceReadJSON(status int, value any, maxBytes int) (*apptheory.Response, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && len(body) > maxBytes {
		return nil, soulMintInstanceReadError(
			soulMintInstanceReadCodeResponseTooLarge,
			"mint conversation response is too large",
			http.StatusRequestEntityTooLarge,
			soulMintInstanceReadDetails("", soulMintInstanceReadReasonResponseTooLarge, ""),
		)
	}
	return &apptheory.Response{
		Status: status,
		Headers: map[string][]string{
			soulMintInstanceReadHeaderContentType: {soulMintInstanceReadJSONContentType},
		},
		Body:     body,
		IsBase64: false,
	}, nil
}

func soulMintInstanceReadResponseError(err error) *apptheory.AppTheoryError {
	if appErr, ok := err.(*apptheory.AppTheoryError); ok && appErr != nil {
		return appErr
	}
	return soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", http.StatusInternalServerError, nil)
}

func (s *Server) recordSoulMintInstanceReadAudit(ctx *apptheory.Context, key *models.InstanceKey, agentID string, conversationID string, routeClass string, outcome string, status int, responseBytes int, started time.Time) {
	s.logSoulMintInstanceReadAccess(ctx, key, agentID, conversationID, routeClass, outcome, status, responseBytes, started)
	if s == nil || key == nil || ctx == nil {
		return
	}
	target := fmt.Sprintf(
		"soul_mint_instance_read:%s:instance=%s:agent=%s",
		strings.TrimSpace(routeClass),
		soulMintInstanceReadAuditHash(key.InstanceSlug),
		soulMintInstanceReadAuditHash(agentID),
	)
	if strings.TrimSpace(conversationID) != "" {
		target += ":conversation=" + soulMintInstanceReadAuditHash(conversationID)
	}
	target += fmt.Sprintf(":status=%d:bytes=%d", status, responseBytes)
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:  "instance_key:" + soulMintInstanceReadAuditHash(key.ID),
		Action: "soul.mint_conversation.instance_read." + strings.TrimSpace(routeClass) + "." + strings.TrimSpace(outcome),
		Target: target,
	})
}

func (s *Server) logSoulMintInstanceReadAccess(ctx *apptheory.Context, key *models.InstanceKey, agentID string, conversationID string, routeClass string, outcome string, status int, responseBytes int, started time.Time) {
	durationMS := int64(0)
	if !started.IsZero() {
		durationMS = time.Since(started).Milliseconds()
	}
	instanceSlug := ""
	instanceKeyID := ""
	if key != nil {
		instanceSlug = key.InstanceSlug
		instanceKeyID = key.ID
	}
	requestID := ""
	if ctx != nil {
		requestID = strings.TrimSpace(ctx.RequestID)
		if instanceKeyID == "" {
			instanceKeyID = sha256HexTrimmed(httpx.BearerToken(ctx.Request.Headers))
		}
	}
	log.Printf(
		"controlplane: soul_mint_instance_read auth_scheme=instance_key instance_slug_hash=%s instance_key_hash=%s agent_id_hash=%s conversation_id_hash=%s route_class=%s outcome=%s status=%d request_id=%s duration_ms=%d response_bytes=%d",
		soulMintInstanceReadAuditHash(instanceSlug),
		soulMintInstanceReadAuditHash(instanceKeyID),
		soulMintInstanceReadAuditHash(agentID),
		soulMintInstanceReadAuditHash(conversationID),
		strings.TrimSpace(routeClass),
		strings.TrimSpace(outcome),
		status,
		requestID,
		durationMS,
		responseBytes,
	)
}

func soulMintInstanceReadAuditHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

func ctxParam(ctx *apptheory.Context, key string) string {
	if ctx == nil || key == "" {
		return ""
	}
	return strings.TrimSpace(ctx.Param(key))
}
