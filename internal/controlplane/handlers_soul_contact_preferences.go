package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulUpdateAgentContactPreferencesRequest struct {
	ContactPreferences *soul.ContactPreferencesV3 `json:"contactPreferences"`
}

func (s *Server) handleSoulUpdateAgentChannelPreferences(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
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
	identity, appErr := s.requireActiveSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	prefs, appErr := parseSoulUpdateAgentContactPreferencesRequest(ctx)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	model := buildSoulV3ContactPreferencesModel(agentIDHex, prefs, now)
	if err := model.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: fmt.Sprintf("invalid contactPreferences: %v", err)}
	}
	if appErr := s.syncSoulV3ContactPreferences(ctx.Context(), agentIDHex, prefs, now); appErr != nil {
		return nil, appErr
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.contact_preferences.update",
		Target:    fmt.Sprintf("soul_agent_contact_preferences:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	resp, err := apptheory.JSON(http.StatusOK, soulAgentContactPreferencesResponse(agentIDHex, identity.UpdatedAt, model))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return resp, nil
}

func parseSoulUpdateAgentContactPreferencesRequest(ctx *apptheory.Context) (*soul.ContactPreferencesV3, *apptheory.AppError) {
	var req soulUpdateAgentContactPreferencesRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return nil, appErr
		}
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid JSON"}
	}
	if req.ContactPreferences == nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "contactPreferences is required"}
	}
	if err := req.ContactPreferences.Validate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: fmt.Sprintf("invalid contactPreferences: %v", err)}
	}
	return req.ContactPreferences, nil
}
