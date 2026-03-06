package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
	Status   string `json:"status,omitempty"`   // "accepted" or "declined" (legacy)
	Accepted *bool  `json:"accepted,omitempty"` // true=accepted, false=declined (SPEC)
	Reason   string `json:"reason,omitempty"`
}

// --- Response types ---

type soulListDisputesResponse struct {
	Version    string                    `json:"version"`
	Disputes   []models.SoulAgentDispute `json:"disputes"`
	Count      int                       `json:"count"`
	HasMore    bool                      `json:"has_more"`
	NextCursor string                    `json:"next_cursor,omitempty"`
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

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.self_suspend",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

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
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.agent.self_reinstate",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

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

	optInStatus, appErr := parseSoulValidationOptInStatus(ctx)
	if appErr != nil {
		return nil, appErr
	}
	challenge, appErr := s.loadSoulValidationChallengeForOptIn(ctx.Context(), agentIDHex, challengeID)
	if appErr != nil {
		return nil, appErr
	}
	now := time.Now().UTC()
	challenge.OptInStatus = optInStatus

	if optInStatus == models.SoulValidationOptInStatusDeclined {
		if appErr := s.recordDeclinedSoulValidation(ctx.Context(), agentIDHex, challenge, optInStatus, now); appErr != nil {
			return nil, appErr
		}
		return apptheory.JSON(http.StatusOK, challenge)
	}

	challenge.UpdatedAt = now
	_ = challenge.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(challenge).IfExists().Update("OptInStatus", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update opt-in status"}
	}

	return apptheory.JSON(http.StatusOK, challenge)
}

func parseSoulValidationOptInStatus(ctx *apptheory.Context) (string, *apptheory.AppError) {
	var req soulValidationOptInRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		appErr, ok := parseErr.(*apptheory.AppError)
		if !ok {
			return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		return "", appErr
	}
	if req.Accepted != nil {
		if *req.Accepted {
			return models.SoulValidationOptInStatusAccepted, nil
		}
		return models.SoulValidationOptInStatusDeclined, nil
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status != models.SoulValidationOptInStatusAccepted && status != models.SoulValidationOptInStatusDeclined {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "status must be 'accepted' or 'declined'"}
	}
	return status, nil
}

func (s *Server) loadSoulValidationChallengeForOptIn(ctx context.Context, agentIDHex string, challengeID string) (*models.SoulAgentValidationChallenge, *apptheory.AppError) {
	challenge, err := getSoulAgentItemBySK[models.SoulAgentValidationChallenge](s, ctx, agentIDHex, fmt.Sprintf("VALIDATIONCHAL#%s", challengeID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "challenge not found"}
	}
	if strings.TrimSpace(challenge.Status) != models.SoulValidationChallengeStatusIssued {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "challenge is not in issued status"}
	}
	return challenge, nil
}

func (s *Server) recordDeclinedSoulValidation(ctx context.Context, agentIDHex string, challenge *models.SoulAgentValidationChallenge, optInStatus string, now time.Time) *apptheory.AppError {
	rec := &models.SoulAgentValidationRecord{
		AgentID:       agentIDHex,
		ChallengeID:   strings.TrimSpace(challenge.ChallengeID),
		ChallengeType: strings.TrimSpace(challenge.ChallengeType),
		ValidatorID:   strings.TrimSpace(challenge.ValidatorID),
		Request:       strings.TrimSpace(challenge.Request),
		Response:      "",
		Result:        models.SoulValidationResultDeclined,
		Score:         0,
		OptInStatus:   optInStatus,
		EvaluatedAt:   now,
	}
	_ = rec.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(rec).Create(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to record declined validation"}
	}
	challenge.Status = models.SoulValidationChallengeStatusEvaluated
	challenge.Result = models.SoulValidationResultDeclined
	challenge.Score = 0
	challenge.EvaluatedAt = now
	challenge.UpdatedAt = now
	_ = challenge.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(challenge).IfExists().Update("OptInStatus", "Status", "Result", "Score", "EvaluatedAt", "UpdatedAt"); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update opt-in status"}
	}
	return nil
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
	if len(disputeID) > 128 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "dispute_id is too long"}
	}
	signalRef := strings.TrimSpace(req.SignalRef)
	if signalRef == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signal_ref is required"}
	}
	if len(signalRef) > 1024 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signal_ref is too long"}
	}
	evidence := strings.TrimSpace(req.Evidence)
	if len(evidence) > 8192 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "evidence is too long"}
	}
	statement := strings.TrimSpace(req.Statement)
	if statement == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "statement is required"}
	}
	if len(statement) > 4096 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "statement is too long"}
	}

	now := time.Now().UTC()
	dispute := &models.SoulAgentDispute{
		AgentID:   agentIDHex,
		DisputeID: disputeID,
		SignalRef: signalRef,
		Evidence:  evidence,
		Statement: statement,
		Status:    models.SoulDisputeStatusOpen,
		CreatedAt: now,
	}
	_ = dispute.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(dispute).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "dispute with this ID already exists"}
	}

	// Audit log.
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.dispute.create",
		Target:    fmt.Sprintf("soul_agent_dispute:%s:%s", agentIDHex, disputeID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusCreated, dispute)
}

// handleSoulPublicGetDisputes returns paginated disputes for an agent.
func (s *Server) handleSoulPublicGetDisputes(ctx *apptheory.Context) (*apptheory.Response, error) {
	out, hasMore, nextCursor, appErr := listSoulPublicAgentItems[models.SoulAgentDispute](
		s,
		ctx,
		&models.SoulAgentDispute{},
		"DISPUTE#",
		"failed to list disputes",
	)
	if appErr != nil {
		return nil, appErr
	}

	resp, err := apptheory.JSON(http.StatusOK, soulListDisputesResponse{
		Version:    "1",
		Disputes:   out,
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

func (s *Server) handleSoulPublicGetDispute(ctx *apptheory.Context) (*apptheory.Response, error) {
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
	disputeID := strings.TrimSpace(ctx.Param("disputeId"))
	if disputeID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "disputeId is required"}
	}

	dispute, err := getSoulAgentItemBySK[models.SoulAgentDispute](s, ctx.Context(), agentIDHex, fmt.Sprintf("DISPUTE#%s", disputeID))
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return apptheory.JSON(http.StatusOK, dispute)
}
