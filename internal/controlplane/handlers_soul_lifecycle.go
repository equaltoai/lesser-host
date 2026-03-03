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

type soulArchiveRequest struct {
	Reason string `json:"reason,omitempty"`
}

type soulDesignateSuccessorRequest struct {
	SuccessorAgentID string `json:"successor_agent_id"`
	Reason           string `json:"reason,omitempty"`
}

// --- Handlers ---

// handleSoulArchiveAgent archives an agent, making it read-only with a final continuity entry.
// Only active or self_suspended agents can be archived. This is a one-way transition.
func (s *Server) handleSoulArchiveAgent(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	// Only active or self_suspended agents can be archived.
	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusActive && currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "only active or self-suspended agents can be archived"}
	}

	var req soulArchiveRequest
	_ = httpx.ParseJSON(ctx, &req)
	reason := strings.TrimSpace(req.Reason)

	now := time.Now().UTC()
	identity.Status = models.SoulAgentStatusArchived
	identity.LifecycleStatus = models.SoulAgentStatusArchived
	identity.LifecycleReason = reason
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to archive agent"}
	}

	// Final continuity entry.
	summary := "Agent archived"
	if reason != "" {
		summary = fmt.Sprintf("Agent archived: %s", reason)
	}
	s.appendContinuityEntry(ctx, agentIDHex, models.SoulContinuityEntryTypeArchived, summary)

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.archive",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, identity)
}

// handleSoulDesignateSuccessor designates a successor agent and transitions the
// current agent to "succeeded" status. Creates continuity entries on both agents.
func (s *Server) handleSoulDesignateSuccessor(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	// Only active or self_suspended agents can designate a successor.
	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusActive && currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "only active or self-suspended agents can designate a successor"}
	}

	var req soulDesignateSuccessorRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	successorIDHex := strings.ToLower(strings.TrimSpace(req.SuccessorAgentID))
	if successorIDHex == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "successor_agent_id is required"}
	}
	if successorIDHex == agentIDHex {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "agent cannot succeed itself"}
	}

	// Verify the successor agent exists.
	successorIdentity, err := s.getSoulAgentIdentity(ctx.Context(), successorIDHex)
	if err != nil || successorIdentity == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "successor agent not found"}
	}

	reason := strings.TrimSpace(req.Reason)

	now := time.Now().UTC()
	identity.Status = models.SoulAgentStatusSucceeded
	identity.LifecycleStatus = models.SoulAgentStatusSucceeded
	identity.LifecycleReason = reason
	identity.SuccessorAgentId = successorIDHex
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Status", "LifecycleStatus", "LifecycleReason", "SuccessorAgentId", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to designate successor"}
	}

	// Bidirectional continuity entries.
	declaredSummary := fmt.Sprintf("Succession declared to %s", successorIDHex)
	if reason != "" {
		declaredSummary = fmt.Sprintf("Succession declared to %s: %s", successorIDHex, reason)
	}
	s.appendContinuityEntry(ctx, agentIDHex, models.SoulContinuityEntryTypeSuccessionDeclared, declaredSummary)
	s.appendContinuityEntry(ctx, successorIDHex, models.SoulContinuityEntryTypeSuccessionReceived,
		fmt.Sprintf("Succession received from %s", agentIDHex))

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.designate_successor",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, identity)
}
