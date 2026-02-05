package trust

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	claimVerifyMaxClaims           = 10
	claimVerifyMaxEvidenceItems    = 5
	claimVerifyMaxEvidenceBytes    = int64(8 * 1024)
	claimVerifyMaxTotalEvidence    = int64(64 * 1024)
	claimVerifyBaseCreditsMin      = int64(10)
	claimVerifyBaseCreditsPerClaim = int64(2)
)

type claimVerifyEvidenceRequest struct {
	SourceID string `json:"source_id"`
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Text     string `json:"text"`
}

type claimVerifyRequest struct {
	Text     string                       `json:"text,omitempty"`
	Claims   []string                     `json:"claims,omitempty"`
	Evidence []claimVerifyEvidenceRequest `json:"evidence"`
}

type aiClaimVerifyResponse struct {
	Status string `json:"status"` // ok|queued|not_checked_budget|error
	Cached bool   `json:"cached"`
	JobID  string `json:"job_id,omitempty"`

	Budget ai.BudgetDecision `json:"budget"`

	Contract ai.ModuleContract `json:"contract"`

	Result any              `json:"result,omitempty"`
	Usage  models.AIUsage   `json:"usage,omitempty"`
	Errors []models.AIError `json:"errors,omitempty"`
}

func (s *Server) handleAIClaimVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	instanceSlug, err := s.requireAIHandler(ctx)
	if err != nil {
		return nil, err
	}

	var req claimVerifyRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	text := strings.TrimSpace(req.Text)
	claims := sanitizeClaimVerifyClaims(req.Claims)
	if len(claims) == 0 && text == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "text or claims is required"}
	}

	evidence, totalEvidenceBytes, err := buildClaimVerifyEvidence(req.Evidence)
	if err != nil {
		return nil, err
	}

	// Estimate claims count when only text is provided.
	estimatedClaims := claims
	if len(estimatedClaims) == 0 {
		estimatedClaims = ai.ExtractClaimsDeterministicV1(text, claimVerifyMaxClaims)
	}
	claimCount := int64(len(estimatedClaims))
	if claimCount <= 0 {
		claimCount = 1
	}

	baseCredits := estimateClaimVerifyBaseCredits(claimCount, totalEvidenceBytes)

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow

	modelSet := "deterministic"
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}

	inputs := ai.ClaimVerifyInputsV1{
		Text:     text,
		Claims:   claims,
		Evidence: evidence,
	}
	inputsHash, _ := ai.InputsHash(inputs)

	resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
		InstanceSlug:         instanceSlug,
		RequestID:            strings.TrimSpace(ctx.RequestID),
		Module:               ai.ClaimVerifyLLMModule,
		PolicyVersion:        ai.ClaimVerifyLLMPolicyVersion,
		ModelSet:             modelSet,
		CacheScope:           ai.CacheScopeInstance,
		Inputs:               inputs,
		BaseCredits:          baseCredits,
		PricingMultiplierBps: instCfg.AIPricingMultiplierBps,
		AllowOverage:         allowOverage,
		JobTTL:               30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	if err := s.enqueueAIJobIfQueued(ctx, resp); err != nil {
		return nil, err
	}

	out := buildAIClaimVerifyResponse(resp, modelSet, inputsHash)

	return apptheory.JSON(http.StatusOK, out)
}

func (s *Server) requireAIHandler(ctx *apptheory.Context) (string, error) {
	if s == nil || s.ai == nil || s.store == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	return instanceSlug, nil
}

func sanitizeClaimVerifyClaims(in []string) []string {
	claims := make([]string, 0, claimVerifyMaxClaims)
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if len(c) > 240 {
			c = strings.TrimSpace(c[:240])
		}
		claims = append(claims, c)
		if len(claims) >= claimVerifyMaxClaims {
			break
		}
	}
	return claims
}

func buildClaimVerifyEvidence(req []claimVerifyEvidenceRequest) ([]ai.ClaimVerifyEvidenceV1, int64, error) {
	// Evidence policy v1: caller must supply bounded evidence texts for citations.
	if len(req) == 0 {
		return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "evidence is required"}
	}
	if len(req) > claimVerifyMaxEvidenceItems {
		return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "too many evidence items"}
	}

	totalEvidenceBytes := int64(0)
	evidence := make([]ai.ClaimVerifyEvidenceV1, 0, len(req))
	seenIDs := map[string]struct{}{}

	for _, e := range req {
		id := strings.TrimSpace(e.SourceID)
		if id == "" {
			return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "evidence.source_id is required"}
		}
		if _, ok := seenIDs[id]; ok {
			return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "duplicate evidence.source_id"}
		}
		seenIDs[id] = struct{}{}

		evText, b, err := clampEvidenceText(e.Text, claimVerifyMaxEvidenceBytes)
		if err != nil {
			return nil, 0, err
		}
		totalEvidenceBytes += b
		if totalEvidenceBytes > claimVerifyMaxTotalEvidence {
			return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "evidence too large"}
		}

		evidence = append(evidence, ai.ClaimVerifyEvidenceV1{
			SourceID: id,
			URL:      strings.TrimSpace(e.URL),
			Title:    strings.TrimSpace(e.Title),
			Text:     evText,
		})
	}

	return evidence, totalEvidenceBytes, nil
}

func clampEvidenceText(raw string, maxBytes int64) (string, int64, error) {
	evText := strings.TrimSpace(raw)
	if evText == "" {
		return "", 0, &apptheory.AppError{Code: "app.bad_request", Message: "evidence.text is required"}
	}

	b := int64(len([]byte(evText)))
	if b <= maxBytes {
		return evText, b, nil
	}

	trimmed := strings.TrimSpace(string([]byte(evText)[:maxBytes]))
	return trimmed, int64(len([]byte(trimmed))), nil
}

func estimateClaimVerifyBaseCredits(claimCount int64, totalEvidenceBytes int64) int64 {
	baseCredits := claimVerifyBaseCreditsMin + (claimCount * claimVerifyBaseCreditsPerClaim)
	// Evidence scaling (coarse): +1 credit per 16KiB of evidence.
	baseCredits += totalEvidenceBytes / (16 * 1024)
	return baseCredits
}

func (s *Server) enqueueAIJobIfQueued(ctx *apptheory.Context, resp ai.Response) error {
	if resp.Status != ai.JobStatusQueued {
		return nil
	}
	if s == nil || s.queues == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "safety queue not configured"}
	}

	if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue job"}
	}
	return nil
}

func buildAIClaimVerifyResponse(resp ai.Response, modelSet string, inputsHash string) aiClaimVerifyResponse {
	out := aiClaimVerifyResponse{
		Status: string(resp.Status),
		Cached: resp.Cached,
		JobID:  strings.TrimSpace(resp.JobID),
		Budget: resp.Budget,
		Contract: ai.ModuleContract{
			Module:        ai.ClaimVerifyLLMModule,
			PolicyVersion: ai.ClaimVerifyLLMPolicyVersion,
			ModelSet:      modelSet,
			InputsHash:    strings.TrimSpace(inputsHash),
		},
	}
	if resp.Result == nil {
		return out
	}

	var parsed any
	if strings.TrimSpace(resp.Result.ResultJSON) != "" {
		_ = json.Unmarshal([]byte(resp.Result.ResultJSON), &parsed)
	}
	out.Contract.CreatedAt = resp.Result.CreatedAt
	out.Contract.ExpiresAt = resp.Result.ExpiresAt
	out.Result = parsed
	out.Usage = resp.Result.Usage
	out.Errors = resp.Result.Errors
	return out
}
