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
	Version    string                    `json:"version"`
	Failures   []models.SoulAgentFailure `json:"failures"`
	Count      int                       `json:"count"`
	HasMore    bool                      `json:"has_more"`
	NextCursor string                    `json:"next_cursor,omitempty"`
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
	if len(failureID) > 128 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_id is too long"}
	}
	failureType := strings.ToLower(strings.TrimSpace(req.FailureType))
	if failureType == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_type is required"}
	}
	if len(failureType) > 64 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_type is too long"}
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "description is required"}
	}
	if len(description) > 8192 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "description is too long"}
	}
	impact := strings.TrimSpace(req.Impact)
	if len(impact) > 8192 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "impact is too long"}
	}

	now := time.Now().UTC()
	failure := &models.SoulAgentFailure{
		AgentID:     agentIDHex,
		FailureID:   failureID,
		FailureType: failureType,
		Description: description,
		Impact:      impact,
		Status:      "open",
		Timestamp:   now,
	}
	_ = failure.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(failure).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "failure with this ID already exists"}
	}

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.failure.record",
		Target:    fmt.Sprintf("soul_agent_failure:%s:%s", agentIDHex, failureID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

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
	if len(failureID) > 128 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "failure_id is too long"}
	}
	recoveryRef := strings.TrimSpace(req.RecoveryRef)
	if len(recoveryRef) > 1024 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "recovery_ref is too long"}
	}

	target := s.findSoulFailureByID(ctx, agentIDHex, failureID)
	if target == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "failure not found"}
	}
	if target.Status == "recovered" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "failure is already recovered"}
	}

	target.Status = "recovered"
	target.RecoveryRef = recoveryRef
	_ = target.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(target).IfExists().Update("Status", "RecoveryRef"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record recovery"}
	}

	// Audit log.
	now := time.Now().UTC()
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.failure.recover",
		Target:    fmt.Sprintf("soul_agent_failure:%s:%s", agentIDHex, failureID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusOK, target)
}

func (s *Server) findSoulFailureByID(ctx *apptheory.Context, agentIDHex string, failureID string) *models.SoulAgentFailure {
	var failures []*models.SoulAgentFailure
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentFailure{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "FAILURE#").
		All(&failures)

	for _, failure := range failures {
		if failure != nil && strings.TrimSpace(failure.FailureID) == failureID {
			return failure
		}
	}
	return nil
}

// handleSoulPublicGetFailures returns paginated failure history for an agent.
func (s *Server) handleSoulPublicGetFailures(ctx *apptheory.Context) (*apptheory.Response, error) {
	out, hasMore, nextCursor, appErr := listSoulPublicAgentItems[models.SoulAgentFailure](
		s,
		ctx,
		&models.SoulAgentFailure{},
		"FAILURE#",
		"failed to list failures",
	)
	if appErr != nil {
		return nil, appErr
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
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}
