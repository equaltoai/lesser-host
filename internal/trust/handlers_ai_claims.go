package trust

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/attestations"
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
	RenderID string `json:"render_id,omitempty"`
	Text     string `json:"text,omitempty"`
}

type claimVerifyRetrievalRequest struct {
	Mode string `json:"mode,omitempty"` // provided_only|openai_web_search

	MaxSources        int    `json:"max_sources,omitempty"`
	SearchContextSize string `json:"search_context_size,omitempty"` // low|medium|high
}

type claimVerifyRequest struct {
	ActorURI    string `json:"actor_uri,omitempty"`
	ObjectURI   string `json:"object_uri,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`

	Text   string   `json:"text,omitempty"`
	Claims []string `json:"claims,omitempty"`

	Evidence  []claimVerifyEvidenceRequest `json:"evidence,omitempty"`
	Retrieval *claimVerifyRetrievalRequest `json:"retrieval,omitempty"`
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

	AttestationID  string `json:"attestation_id,omitempty"`
	AttestationURL string `json:"attestation_url,omitempty"`
}

func claimVerifyRetrievalMode(retrieval *ai.ClaimVerifyRetrievalV1) string {
	mode := ai.ClaimVerifyRetrievalModeProvidedOnly
	if retrieval != nil && strings.TrimSpace(retrieval.Mode) != "" {
		mode = strings.TrimSpace(retrieval.Mode)
	}
	return mode
}

func validateClaimVerifyRequest(text string, claims []string, evidence []claimVerifyEvidenceRequest, retrievalMode string) *apptheory.AppError {
	if len(claims) == 0 && strings.TrimSpace(text) == "" {
		return &apptheory.AppError{Code: "app.bad_request", Message: "text or claims is required"}
	}
	if len(evidence) == 0 && retrievalMode != ai.ClaimVerifyRetrievalModeOpenAIWebSearch {
		return &apptheory.AppError{Code: "app.bad_request", Message: "evidence is required"}
	}
	return nil
}

func estimateClaimVerifyCredits(text string, claims []string, retrieval *ai.ClaimVerifyRetrievalV1, retrievalMode string, totalEvidenceBytes int64) int64 {
	estimatedClaims := claims
	if len(estimatedClaims) == 0 {
		estimatedClaims = ai.ExtractClaimsDeterministicV1(strings.TrimSpace(text), claimVerifyMaxClaims)
	}
	claimCount := int64(len(estimatedClaims))
	if claimCount <= 0 {
		claimCount = 1
	}

	estimatedEvidenceBytes := totalEvidenceBytes
	if retrievalMode == ai.ClaimVerifyRetrievalModeOpenAIWebSearch {
		estSources := 3
		if retrieval != nil && retrieval.MaxSources > 0 {
			estSources = retrieval.MaxSources
		}
		if estSources > claimVerifyMaxEvidenceItems {
			estSources = claimVerifyMaxEvidenceItems
		}
		estimatedEvidenceBytes += int64(estSources) * claimVerifyMaxEvidenceBytes
	}

	baseCredits := estimateClaimVerifyBaseCredits(claimCount, estimatedEvidenceBytes)
	if retrievalMode == ai.ClaimVerifyRetrievalModeOpenAIWebSearch {
		baseCredits += 10 // retrieval overhead (coarse)
	}
	return baseCredits
}

func claimVerifyModelSet(instCfg instanceTrustConfig, retrievalMode string) (string, *apptheory.AppError) {
	modelSet := "deterministic"
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}
	if retrievalMode == ai.ClaimVerifyRetrievalModeOpenAIWebSearch && (!instCfg.AIEnabled || !strings.HasPrefix(strings.ToLower(modelSet), "openai:")) {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "retrieval.mode=openai_web_search requires ai_enabled and an openai:* model_set"}
	}
	return modelSet, nil
}

func (s *Server) handleAIClaimVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	instanceSlug, err := s.requireAIHandler(ctx)
	if err != nil {
		return nil, err
	}

	var req claimVerifyRequest
	if parseErr := parseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	actorURI := strings.TrimSpace(req.ActorURI)
	objectURI := strings.TrimSpace(req.ObjectURI)
	contentHash := strings.TrimSpace(req.ContentHash)

	text := strings.TrimSpace(req.Text)
	claims := sanitizeClaimVerifyClaims(req.Claims)

	retrieval := normalizeClaimVerifyRetrieval(req.Retrieval)
	retrievalMode := claimVerifyRetrievalMode(retrieval)
	if appErr := validateClaimVerifyRequest(text, claims, req.Evidence, retrievalMode); appErr != nil {
		return nil, appErr
	}

	evidence, totalEvidenceBytes, err := buildClaimVerifyEvidence(req.Evidence)
	if err != nil {
		return nil, err
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow
	baseCredits := estimateClaimVerifyCredits(text, claims, retrieval, retrievalMode, totalEvidenceBytes)

	modelSet, appErr := claimVerifyModelSet(instCfg, retrievalMode)
	if appErr != nil {
		return nil, appErr
	}

	inputs := ai.ClaimVerifyInputsV1{
		ActorURI:    actorURI,
		ObjectURI:   objectURI,
		ContentHash: contentHash,
		Text:        text,
		Claims:      claims,
		Evidence:    evidence,
		Retrieval:   retrieval,
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
		MaxInflightJobs:      instCfg.AIMaxInflightJobs,
	})
	if err != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ClaimVerifyLLMModule, ai.Response{Status: ai.JobStatusError}, err)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue job"}
	}

	if err := s.enqueueAIJobIfQueued(ctx, resp); err != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ClaimVerifyLLMModule, ai.Response{Status: ai.JobStatusError, Budget: resp.Budget}, err)
		return nil, err
	}
	s.emitAIRequestMetrics(instanceSlug, ai.ClaimVerifyLLMModule, resp, nil)

	attID := ""
	attURL := ""
	if actorURI != "" && objectURI != "" && contentHash != "" {
		attID = attestations.AttestationID(actorURI, objectURI, contentHash, ai.ClaimVerifyLLMModule, ai.ClaimVerifyLLMPolicyVersion)
		attURL = attestationURL(ctx, attID)
	}

	out := buildAIClaimVerifyResponse(resp, modelSet, inputsHash, attID, attURL)

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

		renderID := strings.TrimSpace(e.RenderID)
		if renderID != "" && !aiJobIDRE.MatchString(renderID) {
			return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "invalid evidence.render_id"}
		}

		evText := ""
		b := int64(0)
		if strings.TrimSpace(e.Text) != "" {
			var err error
			evText, b, err = clampEvidenceText(e.Text, claimVerifyMaxEvidenceBytes)
			if err != nil {
				return nil, 0, err
			}
		} else if renderID == "" {
			return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "evidence.text or evidence.render_id is required"}
		}
		totalEvidenceBytes += b
		if renderID != "" && b == 0 {
			// Approximate bounded evidence size when using render snapshots to avoid under-estimating costs.
			totalEvidenceBytes += claimVerifyMaxEvidenceBytes
		}
		if totalEvidenceBytes > claimVerifyMaxTotalEvidence {
			return nil, 0, &apptheory.AppError{Code: "app.bad_request", Message: "evidence too large"}
		}

		evidence = append(evidence, ai.ClaimVerifyEvidenceV1{
			SourceID: id,
			URL:      strings.TrimSpace(e.URL),
			Title:    strings.TrimSpace(e.Title),
			RenderID: renderID,
			Text:     evText,
		})
	}

	return evidence, totalEvidenceBytes, nil
}

func normalizeClaimVerifyRetrieval(req *claimVerifyRetrievalRequest) *ai.ClaimVerifyRetrievalV1 {
	if req == nil {
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "":
		mode = ai.ClaimVerifyRetrievalModeProvidedOnly
	case ai.ClaimVerifyRetrievalModeProvidedOnly, ai.ClaimVerifyRetrievalModeOpenAIWebSearch:
		// ok
	default:
		mode = ai.ClaimVerifyRetrievalModeProvidedOnly
	}

	maxSources := req.MaxSources
	if maxSources < 0 {
		maxSources = 0
	}
	if maxSources > claimVerifyMaxEvidenceItems {
		maxSources = claimVerifyMaxEvidenceItems
	}

	ctxSize := strings.ToLower(strings.TrimSpace(req.SearchContextSize))
	switch ctxSize {
	case "", ai.ClaimVerifySearchContextLow, ai.ClaimVerifySearchContextMedium, ai.ClaimVerifySearchContextHigh:
		// ok
	default:
		ctxSize = ""
	}

	return &ai.ClaimVerifyRetrievalV1{
		Mode:              mode,
		MaxSources:        maxSources,
		SearchContextSize: ctxSize,
	}
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

func buildAIClaimVerifyResponse(resp ai.Response, modelSet string, inputsHash string, attestationID string, attestationURL string) aiClaimVerifyResponse {
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
		AttestationID:  strings.TrimSpace(attestationID),
		AttestationURL: strings.TrimSpace(attestationURL),
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
