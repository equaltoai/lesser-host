package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request types ---

type soulRecordFailureRequest struct {
	FailureID   string `json:"failure_id"`
	FailureType string `json:"failure_type"` // e.g. "boundary_violation", "operational", "safety"
	Description string `json:"description"`
	Impact      string `json:"impact,omitempty"`
}

type soulRecordRecoveryRequest struct {
	FailureID   string `json:"failure_id"`
	RecoveryRef string `json:"recovery_ref,omitempty"`
}

// --- Response types ---

type soulListFailuresResponse struct {
	Version    string                   `json:"version"`
	Failures   []models.SoulAgentFailure `json:"failures"`
	Count      int                      `json:"count"`
	HasMore    bool                     `json:"has_more"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

// --- Handlers ---

// handleSoulRecordFailure records a failure for an agent.
func (s *Server) handleSoulRecordFailure(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	_, appErr = s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	var req soulRecordFailureRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	failureID := strings.TrimSpace(req.FailureID)
	if failureID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_id is required"}
	}
	failureType := strings.ToLower(strings.TrimSpace(req.FailureType))
	if failureType == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_type is required"}
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "description is required"}
	}

	now := time.Now().UTC()
	failure := &models.SoulAgentFailure{
		AgentID:     agentIDHex,
		FailureID:   failureID,
		FailureType: failureType,
		Description: description,
		Impact:      strings.TrimSpace(req.Impact),
		Status:      "open",
		Timestamp:   now,
	}
	_ = failure.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(failure).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "failure with this ID already exists"}
	}

	// Create continuity entry.
	s.appendContinuityEntry(ctx, agentIDHex, models.SoulContinuityEntryTypeSignificantFailure,
		fmt.Sprintf("Failure recorded: %s - %s", failureType, description))

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.failure.record",
		Target:    fmt.Sprintf("soul_agent_failure:%s:%s", agentIDHex, failureID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusCreated, failure)
}

// handleSoulRecordRecovery marks a failure as recovered.
func (s *Server) handleSoulRecordRecovery(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	_, appErr = s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	var req soulRecordRecoveryRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	failureID := strings.TrimSpace(req.FailureID)
	if failureID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_id is required"}
	}

	// Find the failure record — need to search since SK includes timestamp.
	var failures []*models.SoulAgentFailure
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentFailure{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "FAILURE#").
		All(&failures)

	var target *models.SoulAgentFailure
	for _, f := range failures {
		if f != nil && strings.TrimSpace(f.FailureID) == failureID {
			target = f
			break
		}
	}
	if target == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "failure not found"}
	}
	if target.Status == "recovered" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "failure is already recovered"}
	}

	target.Status = "recovered"
	target.RecoveryRef = strings.TrimSpace(req.RecoveryRef)
	_ = target.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(target).IfExists().Update("Status", "RecoveryRef")

	// Create continuity entry.
	s.appendContinuityEntry(ctx, agentIDHex, models.SoulContinuityEntryTypeRecovery,
		fmt.Sprintf("Recovery from failure: %s", failureID))

	return apptheory.JSON(http.StatusOK, target)
}

// handleSoulPublicGetFailures returns paginated failure history for an agent.
func (s *Server) handleSoulPublicGetFailures(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*models.SoulAgentFailure
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentFailure{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "FAILURE#").
		OrderBy("SK", "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list failures"}
	}

	out := make([]models.SoulAgentFailure, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}

	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}

	resp, err := apptheory.JSON(http.StatusOK, soulListFailuresResponse{
		Version:    "1",
		Failures:   out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	setSoulPublicHeaders(resp, "public, max-age=60")
	return resp, nil
}
