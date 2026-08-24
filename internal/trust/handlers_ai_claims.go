package trust

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/attestations"
	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	claimVerifyMaxClaims           = 10
	claimVerifyMaxTextBytes        = int64(16 * 1024)
	claimVerifyMaxEvidenceItems    = 5
	claimVerifyMaxEvidenceBytes    = int64(8 * 1024)
	claimVerifyMaxTotalEvidence    = int64(64 * 1024)
	claimVerifyMaxSourceIDBytes    = int64(128)
	claimVerifyMaxURLBytes         = int64(2048)
	claimVerifyMaxTitleBytes       = int64(240)
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

func validateClaimVerifyRequest(text string, claims []string, evidence []claimVerifyEvidenceRequest, retrievalMode string) *apptheory.AppTheoryError {
	if len(claims) == 0 && strings.TrimSpace(text) == "" {
		return newAppTheoryError("app.bad_request", "text or claims is required")
	}
	if len(evidence) == 0 && retrievalMode != ai.ClaimVerifyRetrievalModeOpenAIWebSearch {
		return newAppTheoryError("app.bad_request", "evidence is required")
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

func claimVerifyModelSet(instCfg instanceTrustConfig, retrievalMode string) (string, *apptheory.AppTheoryError) {
	modelSet := "deterministic"
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}
	if retrievalMode == ai.ClaimVerifyRetrievalModeOpenAIWebSearch && (!instCfg.AIEnabled || !strings.HasPrefix(strings.ToLower(modelSet), "openai:")) {
		return "", newAppTheoryError("app.bad_request", "retrieval.mode=openai_web_search requires ai_enabled and an openai:* model_set")
	}
	return modelSet, nil
}

func (s *Server) handleAIClaimVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	instanceSlug, err := s.requireAIHandler(ctx)
	if err != nil {
		return nil, err
	}

	var req claimVerifyRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	actorURI := strings.TrimSpace(req.ActorURI)
	objectURI := strings.TrimSpace(req.ObjectURI)
	contentHash := strings.TrimSpace(req.ContentHash)
	subject, appErr := s.normalizeInstanceAttestationSubject(ctx.Context(), instanceSlug, actorURI, objectURI, contentHash)
	if appErr != nil {
		return nil, appErr
	}
	actorURI = subject.ActorURI
	objectURI = subject.ObjectURI
	contentHash = subject.ContentHash

	text, appErr := normalizeClaimVerifyText(req.Text)
	if appErr != nil {
		return nil, appErr
	}
	claims := sanitizeClaimVerifyClaims(req.Claims)

	retrieval := normalizeClaimVerifyRetrieval(req.Retrieval)
	retrievalMode := claimVerifyRetrievalMode(retrieval)
	if validateErr := validateClaimVerifyRequest(text, claims, req.Evidence, retrievalMode); validateErr != nil {
		return nil, validateErr
	}

	evidence, totalEvidenceBytes, err := buildClaimVerifyEvidence(req.Evidence)
	if err != nil {
		return nil, err
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow
	baseCredits := estimateClaimVerifyCredits(text, claims, retrieval, retrievalMode, totalEvidenceBytes)

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
	if !instCfg.AIEnabled {
		out := aiClaimVerifyResponse{
			Status: statusDisabled,
			Cached: false,
			Budget: ai.BudgetDecision{
				Allowed:          false,
				OverBudget:       false,
				Reason:           aiDisabledForInstanceReason,
				RequestedCredits: billing.PricedCredits(baseCredits, instCfg.AIPricingMultiplierBps),
				DebitedCredits:   0,
			},
			Contract: ai.ModuleContract{
				Module:        ai.ClaimVerifyLLMModule,
				PolicyVersion: ai.ClaimVerifyLLMPolicyVersion,
				ModelSet:      modelSetDeterministic,
				InputsHash:    strings.TrimSpace(inputsHash),
			},
		}
		return apptheory.JSON(http.StatusOK, out)
	}

	modelSet, appErr := claimVerifyModelSet(instCfg, retrievalMode)
	if appErr != nil {
		return nil, appErr
	}

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
		fmt.Printf("ai.GetOrQueue error request_id=%s instance=%s module=%s err=%v\n", strings.TrimSpace(ctx.RequestID), instanceSlug, ai.ClaimVerifyLLMModule, err)
		s.emitAIRequestMetrics(instanceSlug, ai.ClaimVerifyLLMModule, ai.Response{Status: ai.JobStatusError}, err)
		return nil, newAppTheoryError("app.internal", "failed to queue job")
	}

	if err := s.enqueueAIJobIfQueued(ctx, resp); err != nil {
		s.emitAIRequestMetrics(instanceSlug, ai.ClaimVerifyLLMModule, ai.Response{Status: ai.JobStatusError, Budget: resp.Budget}, err)
		return nil, err
	}
	s.emitAIRequestMetrics(instanceSlug, ai.ClaimVerifyLLMModule, resp, nil)

	attID := ""
	attURL := ""
	if actorURI != "" && objectURI != "" && contentHash != "" {
		attID = attestations.InstanceAttestationID(instanceSlug, actorURI, objectURI, contentHash, ai.ClaimVerifyLLMModule, ai.ClaimVerifyLLMPolicyVersion)
		attURL = attestationURL(ctx, attID, s.cfg.PublicBaseURL)
	}

	out := buildAIClaimVerifyResponse(resp, modelSet, inputsHash, attID, attURL)

	return apptheory.JSON(http.StatusOK, out)
}

func (s *Server) requireAIHandler(ctx *apptheory.Context) (string, error) {
	if s == nil || s.ai == nil || s.store == nil {
		return "", newAppTheoryError("app.internal", "internal error")
	}
	if ctx == nil {
		return "", newAppTheoryError("app.internal", "internal error")
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
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

func normalizeClaimVerifyText(raw string) (string, *apptheory.AppTheoryError) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", nil
	}
	if int64(len([]byte(text))) > claimVerifyMaxTextBytes {
		return "", newAppTheoryError("app.bad_request", "text is too large")
	}
	return text, nil
}

func normalizeClaimVerifyMetadata(field string, value string, maxBytes int64) (string, *apptheory.AppTheoryError) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if int64(len([]byte(value))) > maxBytes {
		return "", newAppTheoryError("app.bad_request", field+" is too large")
	}
	return value, nil
}

func buildClaimVerifyEvidence(req []claimVerifyEvidenceRequest) ([]ai.ClaimVerifyEvidenceV1, int64, error) {
	// Evidence policy v1: caller must supply bounded evidence texts for citations.
	if len(req) > claimVerifyMaxEvidenceItems {
		return nil, 0, newAppTheoryError("app.bad_request", "too many evidence items")
	}

	totalEvidenceBytes := int64(0)
	evidence := make([]ai.ClaimVerifyEvidenceV1, 0, len(req))
	seenIDs := map[string]struct{}{}

	for _, e := range req {
		item, itemBytes, err := buildClaimVerifyEvidenceItem(e, seenIDs)
		if err != nil {
			return nil, 0, err
		}
		totalEvidenceBytes += itemBytes
		if item.RenderID != "" && itemBytes == 0 {
			// Approximate bounded evidence size when using render snapshots to avoid under-estimating costs.
			totalEvidenceBytes += claimVerifyMaxEvidenceBytes
		}
		if totalEvidenceBytes > claimVerifyMaxTotalEvidence {
			return nil, 0, newAppTheoryError("app.bad_request", "evidence too large")
		}

		evidence = append(evidence, item)
	}

	return evidence, totalEvidenceBytes, nil
}

func buildClaimVerifyEvidenceItem(e claimVerifyEvidenceRequest, seenIDs map[string]struct{}) (ai.ClaimVerifyEvidenceV1, int64, error) {
	id, appErr := normalizeClaimVerifyMetadata("evidence.source_id", e.SourceID, claimVerifyMaxSourceIDBytes)
	if appErr != nil {
		return ai.ClaimVerifyEvidenceV1{}, 0, appErr
	}
	if id == "" {
		return ai.ClaimVerifyEvidenceV1{}, 0, newAppTheoryError("app.bad_request", "evidence.source_id is required")
	}
	if _, ok := seenIDs[id]; ok {
		return ai.ClaimVerifyEvidenceV1{}, 0, newAppTheoryError("app.bad_request", "duplicate evidence.source_id")
	}
	seenIDs[id] = struct{}{}

	renderID := strings.TrimSpace(e.RenderID)
	if renderID != "" && !aiJobIDRE.MatchString(renderID) {
		return ai.ClaimVerifyEvidenceV1{}, 0, newAppTheoryError("app.bad_request", "invalid evidence.render_id")
	}

	url, appErr := normalizeClaimVerifyMetadata("evidence.url", e.URL, claimVerifyMaxURLBytes)
	if appErr != nil {
		return ai.ClaimVerifyEvidenceV1{}, 0, appErr
	}
	title, appErr := normalizeClaimVerifyMetadata("evidence.title", e.Title, claimVerifyMaxTitleBytes)
	if appErr != nil {
		return ai.ClaimVerifyEvidenceV1{}, 0, appErr
	}

	evText, textBytes, err := claimVerifyEvidenceText(e.Text, renderID)
	if err != nil {
		return ai.ClaimVerifyEvidenceV1{}, 0, err
	}

	return ai.ClaimVerifyEvidenceV1{
		SourceID: id,
		URL:      url,
		Title:    title,
		RenderID: renderID,
		Text:     evText,
	}, textBytes, nil
}

func claimVerifyEvidenceText(text string, renderID string) (string, int64, error) {
	if strings.TrimSpace(text) != "" {
		return clampEvidenceText(text, claimVerifyMaxEvidenceBytes)
	}
	if strings.TrimSpace(renderID) == "" {
		return "", 0, newAppTheoryError("app.bad_request", "evidence.text or evidence.render_id is required")
	}
	return "", 0, nil
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
		return "", 0, newAppTheoryError("app.bad_request", "evidence.text is required")
	}

	b := int64(len([]byte(evText)))
	if b <= maxBytes {
		return evText, b, nil
	}

	return "", 0, newAppTheoryError("app.bad_request", "evidence.text is too large")
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
		return newAppTheoryError("app.internal", "safety queue not configured")
	}

	if err := s.queues.enqueueAIJob(ctx.Context(), ai.JobMessage{Kind: "ai_job", JobID: resp.JobID}); err != nil {
		return newAppTheoryError("app.internal", "failed to enqueue job")
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
