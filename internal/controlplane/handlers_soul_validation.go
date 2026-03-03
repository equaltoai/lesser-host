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
	"github.com/equaltoai/lesser-host/internal/soulvalidation"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const soulValidatorSystem = "system"

type soulIssueValidationChallengeRequest struct {
	ChallengeType string `json:"challenge_type"`
	ValidatorID   string `json:"validator_id,omitempty"` // agentId of validator, or "system"
	Request       string `json:"request,omitempty"`
	TTLSeconds    int64  `json:"ttl_seconds,omitempty"`
}

type soulIssueValidationChallengeResponse struct {
	Challenge models.SoulAgentValidationChallenge `json:"challenge"`
}

func (s *Server) handleSoulIssueValidationChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
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
	identityErr := s.requireSoulAgentIdentityExists(ctx.Context(), agentIDHex)
	if identityErr != nil {
		return nil, identityErr
	}

	var req soulIssueValidationChallengeRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	challengeType, appErr := normalizeSoulValidationChallengeType(req.ChallengeType)
	if appErr != nil {
		return nil, appErr
	}
	validatorID, appErr := normalizeSoulValidationValidatorID(req.ValidatorID)
	if appErr != nil {
		return nil, appErr
	}

	id, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create challenge id"}
	}

	now := time.Now().UTC()
	ttlSeconds := normalizeSoulValidationChallengeTTL(req.TTLSeconds)

	chal := &models.SoulAgentValidationChallenge{
		AgentID:       agentIDHex,
		ChallengeID:   id,
		ChallengeType: challengeType,
		ValidatorID:   validatorID,
		Request:       strings.TrimSpace(req.Request),
		Status:        models.SoulValidationChallengeStatusIssued,
		OptInStatus:   models.SoulValidationOptInStatusPending,
		IssuedAt:      now,
		UpdatedAt:     now,
		TTL:           0,
		RespondedAt:   time.Time{},
		EvaluatedAt:   time.Time{},
		Result:        "",
		Score:         0,
		Response:      "",
	}
	if ttlSeconds > 0 {
		chal.TTL = now.Add(time.Duration(ttlSeconds) * time.Second).Unix()
	}
	_ = chal.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(chal).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create challenge"}
	}

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.validation.challenge.issue",
		Target:    fmt.Sprintf("soul_agent_validation_challenge:%s:%s", agentIDHex, id),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, soulIssueValidationChallengeResponse{Challenge: *chal})
}

func (s *Server) requireSoulAgentIdentityExists(ctx context.Context, agentIDHex string) *apptheory.AppError {
	if s == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	_, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return nil
}

func normalizeSoulValidationChallengeType(raw string) (string, *apptheory.AppError) {
	challengeType := strings.ToLower(strings.TrimSpace(raw))
	if !soulvalidation.IsValidChallengeType(challengeType) {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid challenge_type"}
	}
	return challengeType, nil
}

func normalizeSoulValidationValidatorID(raw string) (string, *apptheory.AppError) {
	validatorID := strings.ToLower(strings.TrimSpace(raw))
	if validatorID == "" {
		return soulValidatorSystem, nil
	}
	if validatorID == soulValidatorSystem {
		return validatorID, nil
	}
	if _, _, vErr := parseSoulAgentIDHex(validatorID); vErr != nil {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid validator_id"}
	}
	return validatorID, nil
}

func normalizeSoulValidationChallengeTTL(ttlSeconds int64) int64 {
	if ttlSeconds < 0 {
		return 0
	}
	max := int64((30 * 24 * time.Hour).Seconds())
	if ttlSeconds > max {
		return max
	}
	return ttlSeconds
}

type soulRecordValidationResponseRequest struct {
	Response string `json:"response"`
}

type soulRecordValidationResponseResponse struct {
	Challenge models.SoulAgentValidationChallenge `json:"challenge"`
}

func (s *Server) handleSoulRecordValidationResponse(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
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
	challengeID := strings.TrimSpace(ctx.Param("challengeId"))
	if challengeID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "challenge_id is required"}
	}

	chal, appErr := s.getUnevaluatedSoulValidationChallenge(ctx.Context(), agentIDHex, challengeID)
	if appErr != nil {
		return nil, appErr
	}

	var req soulRecordValidationResponseRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	chal.Response = strings.TrimSpace(req.Response)
	chal.Status = models.SoulValidationChallengeStatusResponded
	chal.RespondedAt = now
	chal.UpdatedAt = now
	_ = chal.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(chal).IfExists().Update("Response", "Status", "RespondedAt", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update challenge"}
	}

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.validation.challenge.response",
		Target:    fmt.Sprintf("soul_agent_validation_challenge:%s:%s", agentIDHex, challengeID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, soulRecordValidationResponseResponse{Challenge: *chal})
}

type soulEvaluateValidationChallengeRequest struct {
	Result string `json:"result"` // pass|fail|timeout
}

type soulEvaluateValidationChallengeResponse struct {
	Challenge models.SoulAgentValidationChallenge `json:"challenge"`
	Record    models.SoulAgentValidationRecord    `json:"record"`
}

func (s *Server) handleSoulEvaluateValidationChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
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
	challengeID := strings.TrimSpace(ctx.Param("challengeId"))
	if challengeID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "challenge_id is required"}
	}

	chal, appErr := s.getUnevaluatedSoulValidationChallenge(ctx.Context(), agentIDHex, challengeID)
	if appErr != nil {
		return nil, appErr
	}

	var req soulEvaluateValidationChallengeRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	result, appErr := normalizeSoulValidationResult(req.Result)
	if appErr != nil {
		return nil, appErr
	}

	score := soulvalidation.ScoreDelta(strings.TrimSpace(chal.ChallengeType), result)
	if strings.TrimSpace(chal.OptInStatus) == models.SoulValidationOptInStatusDeclined {
		// Declined challenges carry no score penalty and are recorded distinctly.
		result = models.SoulValidationResultDeclined
		score = 0
	}

	now := time.Now().UTC()
	optInStatus := strings.TrimSpace(chal.OptInStatus)
	if optInStatus == "" {
		optInStatus = models.SoulValidationOptInStatusPending
	}
	rec := &models.SoulAgentValidationRecord{
		AgentID:       agentIDHex,
		ChallengeID:   strings.TrimSpace(chal.ChallengeID),
		ChallengeType: strings.TrimSpace(chal.ChallengeType),
		ValidatorID:   strings.TrimSpace(chal.ValidatorID),
		Request:       strings.TrimSpace(chal.Request),
		Response:      strings.TrimSpace(chal.Response),
		Result:        result,
		Score:         score,
		OptInStatus:   optInStatus,
		EvaluatedAt:   now,
	}
	_ = rec.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(rec).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record validation"}
	}

	chal.Status = models.SoulValidationChallengeStatusEvaluated
	chal.Result = result
	chal.Score = score
	chal.EvaluatedAt = now
	chal.UpdatedAt = now
	_ = chal.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(chal).IfExists().Update("Status", "Result", "Score", "EvaluatedAt", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update challenge"}
	}

	_ = s.store.DB.WithContext(ctx.Context()).Model(&models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.validation.challenge.evaluate",
		Target:    fmt.Sprintf("soul_agent_validation_challenge:%s:%s", agentIDHex, challengeID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}).Create()

	return apptheory.JSON(http.StatusOK, soulEvaluateValidationChallengeResponse{Challenge: *chal, Record: *rec})
}

func (s *Server) getUnevaluatedSoulValidationChallenge(ctx context.Context, agentIDHex string, challengeID string) (*models.SoulAgentValidationChallenge, *apptheory.AppError) {
	chal, err := s.getSoulValidationChallenge(ctx, agentIDHex, challengeID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "challenge not found"}
	}
	if err != nil || chal == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(chal.Status) == models.SoulValidationChallengeStatusEvaluated {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "challenge is already evaluated"}
	}
	return chal, nil
}

func normalizeSoulValidationResult(raw string) (string, *apptheory.AppError) {
	result := strings.ToLower(strings.TrimSpace(raw))
	switch result {
	case models.SoulValidationResultPass, models.SoulValidationResultFail, models.SoulValidationResultTimeout:
		return result, nil
	default:
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid result"}
	}
}

func (s *Server) getSoulValidationChallenge(ctx context.Context, agentID string, challengeID string) (*models.SoulAgentValidationChallenge, error) {
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return nil, fmt.Errorf("challenge_id is required")
	}
	return getSoulAgentItemBySK[models.SoulAgentValidationChallenge](s, ctx, agentID, fmt.Sprintf("VALIDATIONCHAL#%s", challengeID))
}
