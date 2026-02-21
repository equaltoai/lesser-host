package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulSuspendAgentRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (s *Server) handleSuspendSoulAgent(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req soulSuspendAgentRequest
	_ = httpx.ParseJSON(ctx, &req)

	now := time.Now().UTC()
	if strings.TrimSpace(identity.Status) != models.SoulAgentStatusSuspended {
		identity.Status = models.SoulAgentStatusSuspended
		identity.UpdatedAt = now
		_ = identity.UpdateKeys()
		if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Status", "UpdatedAt"); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to suspend agent"}
		}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.suspend",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, identity)
}

func (s *Server) handleReinstateSoulAgent(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	now := time.Now().UTC()
	if strings.TrimSpace(identity.Status) != models.SoulAgentStatusSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is not suspended"}
	}

	identity.Status = models.SoulAgentStatusActive
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Status", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to reinstate agent"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.reinstate",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, identity)
}
