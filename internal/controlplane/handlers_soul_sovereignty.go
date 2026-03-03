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

type soulSelfSuspendRequest struct {
	Reason string `json:"reason"`
}

type soulDisputeRequest struct {
	DisputeID string `json:"dispute_id"`
	SignalRef string `json:"signal_ref"`
	Evidence  string `json:"evidence"`
	Statement string `json:"statement"`
}

type soulValidationOptInRequest struct {
	Status string `json:"status"` // "accepted" or "declined"
}

// --- Handlers ---

// handleSoulSelfSuspend allows an agent to voluntarily suspend itself.
func (s *Server) handleSoulSelfSuspend(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req soulSelfSuspendRequest
	_ = httpx.ParseJSON(ctx, &req)
	reason := strings.TrimSpace(req.Reason)

	now := time.Now().UTC()
	identity.Status = models.SoulAgentStatusSelfSuspended
	identity.LifecycleStatus = models.SoulAgentStatusSelfSuspended
	identity.LifecycleReason = reason
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to self-suspend agent"}
	}

	// Create continuity entry.
	summary := "Agent voluntarily self-suspended"
	if reason != "" {
		summary = fmt.Sprintf("Agent voluntarily self-suspended: %s", reason)
	}
	s.appendContinuityEntry(ctx, agentIDHex, models.SoulContinuityEntryTypeSelfSuspension, summary)

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.self_suspend",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, identity)
}

// handleSoulSelfReinstate allows an agent to reinstate itself from self-suspension.
func (s *Server) handleSoulSelfReinstate(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	// Cannot use requireActiveSoulAgentWithDomainAccess here since agent is self_suspended.
	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain)); accessErr != nil {
		return nil, accessErr
	}

	// Only self-suspended agents can self-reinstate.
	currentStatus := strings.TrimSpace(identity.Status)
	if currentStatus != models.SoulAgentStatusSelfSuspended {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent is not self-suspended"}
	}

	now := time.Now().UTC()
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive
	identity.LifecycleReason = ""
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(identity).IfExists().Update("Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to reinstate agent"}
	}

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.self_reinstate",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, identity)
}

// handleSoulValidationOptIn records an agent's opt-in decision for a validation challenge.
func (s *Server) handleSoulValidationOptIn(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	challengeID := strings.TrimSpace(ctx.Param("challengeId"))
	if challengeID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "challengeId is required"}
	}

	var req soulValidationOptInRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	optInStatus := strings.ToLower(strings.TrimSpace(req.Status))
	if optInStatus != models.SoulValidationOptInStatusAccepted && optInStatus != models.SoulValidationOptInStatusDeclined {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "status must be 'accepted' or 'declined'"}
	}

	// Load the validation challenge.
	challenge, err := getSoulAgentItemBySK[models.SoulAgentValidationChallenge](s, ctx.Context(), agentIDHex, fmt.Sprintf("VALIDATIONCHAL#%s", challengeID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "challenge not found"}
	}

	// Only challenges in "issued" status can be opted in/out.
	if strings.TrimSpace(challenge.Status) != models.SoulValidationChallengeStatusIssued {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "challenge is not in issued status"}
	}

	challenge.OptInStatus = optInStatus
	challenge.UpdatedAt = time.Now().UTC()
	_ = challenge.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(challenge).IfExists().Update("OptInStatus", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update opt-in status"}
	}

	return apptheory.JSON(http.StatusOK, challenge)
}

// handleSoulCreateDispute creates a dispute record for an agent.
func (s *Server) handleSoulCreateDispute(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	var req soulDisputeRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	disputeID := strings.TrimSpace(req.DisputeID)
	if disputeID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "dispute_id is required"}
	}
	signalRef := strings.TrimSpace(req.SignalRef)
	if signalRef == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signal_ref is required"}
	}
	statement := strings.TrimSpace(req.Statement)
	if statement == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "statement is required"}
	}

	now := time.Now().UTC()
	dispute := &models.SoulAgentDispute{
		AgentID:   agentIDHex,
		DisputeID: disputeID,
		SignalRef: signalRef,
		Evidence:  strings.TrimSpace(req.Evidence),
		Statement: statement,
		Status:    models.SoulDisputeStatusOpen,
		CreatedAt: now,
	}
	_ = dispute.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(dispute).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "dispute with this ID already exists"}
	}

	// Create continuity entry.
	s.appendContinuityEntry(ctx, agentIDHex, "dispute", fmt.Sprintf("Dispute filed: %s", signalRef))

	// Audit log.
	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.dispute.create",
		Target:    fmt.Sprintf("soul_agent_dispute:%s:%s", agentIDHex, disputeID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusCreated, dispute)
}
