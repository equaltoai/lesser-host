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
	if s == nil || s.ai == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var req claimVerifyRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	text := strings.TrimSpace(req.Text)
	claims := make([]string, 0, claimVerifyMaxClaims)
	for _, c := range req.Claims {
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

	if len(claims) == 0 && text == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "text or claims is required"}
	}

	// Evidence policy v1: caller must supply bounded evidence texts for citations.
	if len(req.Evidence) == 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "evidence is required"}
	}
	if len(req.Evidence) > claimVerifyMaxEvidenceItems {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "too many evidence items"}
	}

	totalEvidenceBytes := int64(0)
	evidence := make([]ai.ClaimVerifyEvidenceV1, 0, len(req.Evidence))
	seenIDs := map[string]struct{}{}
	for _, e := range req.Evidence {
		id := strings.TrimSpace(e.SourceID)
		if id == "" {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "evidence.source_id is required"}
		}
		if _, ok := seenIDs[id]; ok {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "duplicate evidence.source_id"}
		}
		seenIDs[id] = struct{}{}

		evText := strings.TrimSpace(e.Text)
		if evText == "" {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "evidence.text is required"}
		}
		b := int64(len([]byte(evText)))
		if b > claimVerifyMaxEvidenceBytes {
			raw := []byte(evText)
			evText = strings.TrimSpace(string(raw[:claimVerifyMaxEvidenceBytes]))
			b = int64(len([]byte(evText)))
		}
		totalEvidenceBytes += b
		if totalEvidenceBytes > claimVerifyMaxTotalEvidence {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "evidence too large"}
		}

		evidence = append(evidence, ai.ClaimVerifyEvidenceV1{
			SourceID: id,
			URL:      strings.TrimSpace(e.URL),
			Title:    strings.TrimSpace(e.Title),
			Text:     evText,
		})
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

	baseCredits := claimVerifyBaseCreditsMin + (claimCount * claimVerifyBaseCreditsPerClaim)
	// Evidence scaling (coarse): +1 credit per 16KiB of evidence.
	baseCredits += totalEvidenceBytes / (16 * 1024)

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == "allow"

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

	if resp.Status == ai.JobStatusQueued {
		if s.queues == nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "safety queue not configured"}
		}
		if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue job"}
		}
	}

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
	if resp.Result != nil {
		var parsed any
		if strings.TrimSpace(resp.Result.ResultJSON) != "" {
			_ = json.Unmarshal([]byte(resp.Result.ResultJSON), &parsed)
		}
		out.Contract.CreatedAt = resp.Result.CreatedAt
		out.Contract.ExpiresAt = resp.Result.ExpiresAt
		out.Result = parsed
		out.Usage = resp.Result.Usage
		out.Errors = resp.Result.Errors
	}

	return apptheory.JSON(http.StatusOK, out)
}
