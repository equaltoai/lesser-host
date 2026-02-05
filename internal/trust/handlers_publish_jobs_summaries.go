package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	linkRenderSummaryPolicyVersion = "v1"
	linkRenderSummaryCreditCost    = int64(2) // credits per cache-miss summary
)

type linkRenderSummaryLinkResult struct {
	URL           string `json:"url"`
	NormalizedURL string `json:"normalized_url,omitempty"`
	Risk          string `json:"risk,omitempty"`

	RenderID string `json:"render_id,omitempty"`

	Status string `json:"status"` // ok|queued|skipped|blocked|invalid|not_checked_budget|error
	Cached bool   `json:"cached"`

	Summary      string                 `json:"summary,omitempty"`
	Bullets      []string               `json:"bullets,omitempty"`
	Risks        []ai.RenderSummaryRisk `json:"risks,omitempty"`
	SummaryJobID string                 `json:"summary_job_id,omitempty"`
}

type linkRenderSummarySummary struct {
	TotalLinks int `json:"total_links"`

	Candidates int `json:"candidates"`
	Cached     int `json:"cached"`
	Generated  int `json:"generated"`

	Queued           int `json:"queued"`
	Skipped          int `json:"skipped"`
	Blocked          int `json:"blocked"`
	Invalid          int `json:"invalid"`
	NotCheckedBudget int `json:"not_checked_budget"`
	Errors           int `json:"errors"`
}

type linkRenderSummaryResult struct {
	RenderPolicy  string                        `json:"render_policy"`
	PolicyVersion string                        `json:"policy_version"`
	Summary       linkRenderSummarySummary      `json:"summary"`
	Links         []linkRenderSummaryLinkResult `json:"links"`
}

type queuedRenderSummary struct {
	Index          int
	JobID          string
	Inputs         ai.RenderSummaryInputsV1
	EstimatedBytes int64
	Artifact       *models.RenderArtifact
}

func (s *Server) runLinkRenderSummaryJob(
	ctx *apptheory.Context,
	instanceSlug string,
	renderPolicy string,
	instCfg instanceTrustConfig,
	pricingMultiplierBps int64,
	canonicalLinks []string,
) publishJobModuleResponse {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return linkRenderSummaryErrorResponse("internal error")
	}
	if s.artifacts == nil {
		return linkRenderSummaryErrorResponse("artifact store not configured")
	}

	now := time.Now().UTC()
	policy := normalizeRenderPolicy(renderPolicy)
	instanceSlug = strings.TrimSpace(instanceSlug)

	cfg := linkRenderSummaryJobConfigFromInstance(instCfg, pricingMultiplierBps)

	out := newLinkRenderSummaryResult(policy, len(canonicalLinks))
	queued := make([]queuedRenderSummary, 0)
	budget := &linkRenderSummaryBudgetTotals{}

	for _, raw := range canonicalLinks {
		index := len(out.Links)
		linkOut, q := s.processLinkRenderSummaryLink(ctx, instanceSlug, now, policy, cfg, raw, index, budget)
		out.Links = append(out.Links, linkOut)
		applyLinkRenderSummaryCounts(&out.Summary, linkOut)
		if q != nil {
			queued = append(queued, *q)
		}
	}

	if len(queued) > 0 {
		s.processQueuedRenderSummaries(ctx, instanceSlug, now, cfg, queued, &out)
	}

	status := linkRenderSummaryStatus(out.Summary)
	cached := linkRenderSummaryCached(out.Summary)
	reason := linkRenderSummaryReason(*budget, cached, out.Summary)
	month := strings.TrimSpace(budget.Month)
	if month == "" {
		month = now.Format("2006-01")
	}

	return publishJobModuleResponse{
		Name:          "link_render_summary",
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Status:        status,
		Cached:        cached,
		Budget: budgetDecision{
			Allowed:          status != statusNotCheckedBudget,
			OverBudget:       budget.OverBudget,
			Reason:           reason,
			Month:            month,
			IncludedCredits:  budget.IncludedCredits,
			UsedCredits:      budget.UsedCredits,
			RemainingCredits: budget.RemainingCredits,
			RequestedCredits: budget.RequestedCredits,
			DebitedCredits:   budget.DebitedCredits,
		},
		Result: out,
	}
}

type linkRenderSummaryJobConfig struct {
	AllowOverage       bool
	ModelSet           string
	CombinedPricingBps int64
	BatchingMode       string
	BatchMaxItems      int64
	BatchMaxTotalBytes int64
	JobTTL             time.Duration
}

type linkRenderSummaryBudgetTotals struct {
	Month string

	IncludedCredits  int64
	UsedCredits      int64
	RemainingCredits int64

	RequestedCredits int64
	DebitedCredits   int64

	OverBudget bool
}

func (b *linkRenderSummaryBudgetTotals) add(dec ai.BudgetDecision) {
	if b == nil {
		return
	}

	b.RequestedCredits += dec.RequestedCredits
	b.DebitedCredits += dec.DebitedCredits
	if dec.OverBudget {
		b.OverBudget = true
	}

	if strings.TrimSpace(dec.Month) != "" {
		b.Month = strings.TrimSpace(dec.Month)
		b.IncludedCredits = dec.IncludedCredits
		b.UsedCredits = dec.UsedCredits
		b.RemainingCredits = dec.RemainingCredits
	}
}

func linkRenderSummaryErrorResponse(reason string) publishJobModuleResponse {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "internal error"
	}
	return publishJobModuleResponse{
		Name:          "link_render_summary",
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Status:        statusError,
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           reason,
			RequestedCredits: 0,
			DebitedCredits:   0,
		},
	}
}

func linkRenderSummaryJobConfigFromInstance(instCfg instanceTrustConfig, pricingMultiplierBps int64) linkRenderSummaryJobConfig {
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow

	modelSet := modelSetDeterministic
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}

	requestBps := pricingMultiplierBps
	if requestBps <= 0 {
		requestBps = 10000
	}
	instanceBps := instCfg.AIPricingMultiplierBps
	if instanceBps <= 0 {
		instanceBps = 10000
	}
	combinedPricingBps := (requestBps * instanceBps) / 10000
	if combinedPricingBps <= 0 {
		combinedPricingBps = 10000
	}

	mode := strings.ToLower(strings.TrimSpace(instCfg.AIBatchingMode))
	switch mode {
	case aiBatchingModeInRequest, aiBatchingModeHybrid:
	default:
		mode = aiBatchingModeNone
	}

	maxItems := instCfg.AIBatchMaxItems
	if maxItems <= 0 {
		maxItems = 8
	}
	maxBytes := instCfg.AIBatchMaxTotalBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	return linkRenderSummaryJobConfig{
		AllowOverage:       allowOverage,
		ModelSet:           modelSet,
		CombinedPricingBps: combinedPricingBps,
		BatchingMode:       mode,
		BatchMaxItems:      maxItems,
		BatchMaxTotalBytes: maxBytes,
		JobTTL:             30 * 24 * time.Hour,
	}
}

func newLinkRenderSummaryResult(policy string, totalLinks int) linkRenderSummaryResult {
	return linkRenderSummaryResult{
		RenderPolicy:  strings.TrimSpace(policy),
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Summary: linkRenderSummarySummary{
			TotalLinks: totalLinks,
		},
		Links: make([]linkRenderSummaryLinkResult, 0, totalLinks),
	}
}

func applyLinkRenderSummaryCounts(sum *linkRenderSummarySummary, link linkRenderSummaryLinkResult) {
	if sum == nil {
		return
	}

	switch link.Status {
	case statusInvalid:
		sum.Invalid++
		return
	case statusBlocked:
		sum.Blocked++
		return
	case statusSkipped:
		sum.Skipped++
		return
	}

	sum.Candidates++

	switch link.Status {
	case statusOK:
		if link.Cached {
			sum.Cached++
		}
	case statusQueued:
		sum.Queued++
	case statusNotCheckedBudget:
		sum.NotCheckedBudget++
	case statusError:
		sum.Errors++
	}
}

func linkRenderSummaryStatus(sum linkRenderSummarySummary) string {
	if sum.NotCheckedBudget > 0 && (sum.Cached+sum.Generated+sum.Queued) == 0 {
		return statusNotCheckedBudget
	}
	if sum.Errors > 0 && (sum.Cached+sum.Generated+sum.Queued) == 0 {
		return statusError
	}
	return statusOK
}

func linkRenderSummaryCached(sum linkRenderSummarySummary) bool {
	return sum.Candidates > 0 && sum.Generated == 0 && sum.Queued == 0 && sum.Cached > 0
}

func linkRenderSummaryReason(budget linkRenderSummaryBudgetTotals, cached bool, sum linkRenderSummarySummary) string {
	if budget.DebitedCredits > 0 {
		if budget.OverBudget {
			return budgetReasonOverage
		}
		return budgetReasonDebited
	}
	if cached {
		return "cache_hit"
	}
	if sum.NotCheckedBudget > 0 {
		return "budget"
	}
	return statusQueued
}

func (s *Server) processLinkRenderSummaryLink(
	ctx *apptheory.Context,
	instanceSlug string,
	now time.Time,
	policy string,
	cfg linkRenderSummaryJobConfig,
	raw string,
	index int,
	budget *linkRenderSummaryBudgetTotals,
) (linkRenderSummaryLinkResult, *queuedRenderSummary) {
	raw = strings.TrimSpace(raw)
	analysis := analyzeLinkSafetyBasic(ctx.Context(), nil, raw)
	risk := strings.ToLower(strings.TrimSpace(analysis.Risk))
	normalized := strings.TrimSpace(analysis.NormalizedURL)
	if normalized == "" {
		normalized = raw
	}

	linkOut := linkRenderSummaryLinkResult{
		URL:           raw,
		NormalizedURL: normalized,
		Risk:          risk,
	}

	switch risk {
	case statusInvalid:
		linkOut.Status = statusInvalid
		return linkOut, nil
	case statusBlocked:
		linkOut.Status = statusBlocked
		return linkOut, nil
	}

	if !shouldRenderLink(policy, risk) {
		linkOut.Status = statusSkipped
		return linkOut, nil
	}

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)
	linkOut.RenderID = renderID

	artifact, status := s.loadRenderArtifactForSummary(ctx, renderID)
	if status != "" {
		linkOut.Status = status
		return linkOut, nil
	}

	inputs := buildRenderSummaryInputs(artifact, normalized, risk)

	resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
		InstanceSlug:         instanceSlug,
		RequestID:            strings.TrimSpace(ctx.RequestID),
		Module:               ai.RenderSummaryLLMModule,
		PolicyVersion:        ai.RenderSummaryLLMPolicyVersion,
		ModelSet:             cfg.ModelSet,
		CacheScope:           ai.CacheScopeInstance,
		Inputs:               inputs,
		BaseCredits:          linkRenderSummaryCreditCost,
		PricingMultiplierBps: cfg.CombinedPricingBps,
		AllowOverage:         cfg.AllowOverage,
		JobTTL:               cfg.JobTTL,
	})
	if err != nil {
		linkOut.Status = statusError
		return linkOut, nil
	}

	if budget != nil {
		budget.add(resp.Budget)
	}
	linkOut.SummaryJobID = strings.TrimSpace(resp.JobID)

	switch resp.Status {
	case ai.JobStatusOK:
		linkOut.Status = statusOK
		linkOut.Cached = true
		fillRenderSummaryFromResult(&linkOut, resp.Result)
		s.maybeMirrorSummaryToArtifact(ctx, artifact, linkOut.Summary, now, instanceSlug)
		return linkOut, nil
	case ai.JobStatusQueued:
		linkOut.Status = statusQueued
		q := queuedRenderSummary{
			Index:          index,
			JobID:          strings.TrimSpace(resp.JobID),
			Inputs:         inputs,
			EstimatedBytes: estimateRenderSummaryInputsBytes(inputs),
			Artifact:       artifact,
		}
		return linkOut, &q
	case ai.JobStatusNotCheckedBudget:
		linkOut.Status = statusNotCheckedBudget
		return linkOut, nil
	default:
		linkOut.Status = statusError
		return linkOut, nil
	}
}

func (s *Server) loadRenderArtifactForSummary(ctx *apptheory.Context, renderID string) (*models.RenderArtifact, string) {
	artifact, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
	if theoryErrors.IsNotFound(err) {
		return nil, statusQueued
	}
	if err != nil {
		return nil, statusError
	}
	if strings.TrimSpace(artifact.ErrorCode) != "" {
		return nil, statusError
	}
	if strings.TrimSpace(artifact.SnapshotObjectKey) == "" {
		return nil, statusQueued
	}
	return artifact, ""
}

func buildRenderSummaryInputs(artifact *models.RenderArtifact, normalized, risk string) ai.RenderSummaryInputsV1 {
	if artifact == nil {
		return ai.RenderSummaryInputsV1{
			RenderID:      rendering.RenderArtifactID(rendering.RenderPolicyVersion, strings.TrimSpace(normalized)),
			NormalizedURL: strings.TrimSpace(normalized),
			LinkRisk:      strings.TrimSpace(risk),
		}
	}
	inputs := ai.RenderSummaryInputsV1{
		RenderID:      strings.TrimSpace(artifact.ID),
		NormalizedURL: strings.TrimSpace(artifact.NormalizedURL),
		ResolvedURL:   strings.TrimSpace(artifact.ResolvedURL),
		LinkRisk:      strings.TrimSpace(risk),
		Text:          strings.TrimSpace(artifact.TextPreview),
	}
	if inputs.NormalizedURL == "" {
		inputs.NormalizedURL = strings.TrimSpace(normalized)
	}
	if !artifact.RenderedAt.IsZero() {
		inputs.RenderedAt = artifact.RenderedAt.UTC().Format(time.RFC3339Nano)
	}
	return inputs
}

func fillRenderSummaryFromResult(linkOut *linkRenderSummaryLinkResult, res *models.AIResult) {
	if linkOut == nil || res == nil || strings.TrimSpace(res.ResultJSON) == "" {
		return
	}
	var parsed ai.RenderSummaryResultV1
	if err := json.Unmarshal([]byte(res.ResultJSON), &parsed); err != nil {
		return
	}
	linkOut.Summary = strings.TrimSpace(parsed.ShortSummary)
	linkOut.Bullets = append([]string(nil), parsed.KeyBullets...)
	linkOut.Risks = append([]ai.RenderSummaryRisk(nil), parsed.Risks...)
}

func estimateRenderSummaryInputsBytes(inputs ai.RenderSummaryInputsV1) int64 {
	estimatedBytes := int64(len([]byte(inputs.Text)))
	estimatedBytes += int64(len(inputs.RenderID) + len(inputs.NormalizedURL) + len(inputs.ResolvedURL) + len(inputs.RenderedAt) + len(inputs.LinkRisk))
	if estimatedBytes <= 0 {
		return 1
	}
	return estimatedBytes
}

func (s *Server) maybeMirrorSummaryToArtifact(ctx *apptheory.Context, artifact *models.RenderArtifact, summary string, now time.Time, instanceSlug string) {
	if s == nil || ctx == nil || artifact == nil {
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	if strings.TrimSpace(artifact.Summary) != "" && strings.TrimSpace(artifact.SummaryPolicyVersion) == linkRenderSummaryPolicyVersion {
		return
	}

	artifact.SummaryPolicyVersion = linkRenderSummaryPolicyVersion
	artifact.Summary = summary
	artifact.SummarizedAt = now
	artifact.RequestID = strings.TrimSpace(ctx.RequestID)
	artifact.RequestedBy = strings.TrimSpace(instanceSlug)
	_ = artifact.UpdateKeys()
	_ = s.store.PutRenderArtifact(ctx.Context(), artifact)
}

func (s *Server) processQueuedRenderSummaries(ctx *apptheory.Context, instanceSlug string, now time.Time, cfg linkRenderSummaryJobConfig, queued []queuedRenderSummary, out *linkRenderSummaryResult) {
	if s == nil || ctx == nil || out == nil || len(queued) == 0 {
		return
	}

	lowerModelSet := strings.ToLower(strings.TrimSpace(cfg.ModelSet))
	inline := shouldInlineQueuedSummaries(lowerModelSet, cfg.BatchingMode, queued, cfg.BatchMaxItems, cfg.BatchMaxTotalBytes)

	shards := shardQueuedSummaries(queued, cfg.BatchMaxItems, cfg.BatchMaxTotalBytes)
	if inline {
		for _, shard := range shards {
			s.processRenderSummaryShard(ctx, instanceSlug, now, cfg.ModelSet, shard, out, true)
		}
		return
	}

	for _, shard := range shards {
		if cfg.BatchingMode == aiBatchingModeNone {
			for _, q := range shard {
				id := strings.TrimSpace(q.JobID)
				if err := s.enqueueRenderSummaryJobs(ctx, cfg.BatchingMode, []string{id}); err != nil {
					s.processRenderSummaryShard(ctx, instanceSlug, now, cfg.ModelSet, []queuedRenderSummary{q}, out, true)
				}
			}
			continue
		}

		ids := make([]string, 0, len(shard))
		for _, q := range shard {
			ids = append(ids, strings.TrimSpace(q.JobID))
		}
		if err := s.enqueueRenderSummaryJobs(ctx, cfg.BatchingMode, ids); err != nil {
			s.processRenderSummaryShard(ctx, instanceSlug, now, cfg.ModelSet, shard, out, true)
		}
	}
}

func shouldInlineQueuedSummaries(lowerModelSet string, mode string, queued []queuedRenderSummary, maxItems int64, maxBytes int64) bool {
	if !strings.HasPrefix(lowerModelSet, "openai:") {
		return true
	}
	if mode == aiBatchingModeInRequest {
		return true
	}
	if mode != aiBatchingModeHybrid {
		return false
	}
	if int64(len(queued)) > maxItems {
		return false
	}

	totalBytes := int64(0)
	for _, q := range queued {
		totalBytes += q.EstimatedBytes
	}
	return totalBytes <= maxBytes
}

func (s *Server) enqueueRenderSummaryJobs(ctx *apptheory.Context, mode string, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}
	if s == nil || s.queues == nil {
		return fmt.Errorf("ai queue not configured")
	}

	msg := ai.JobMessage{}
	if len(jobIDs) == 1 && mode == aiBatchingModeNone {
		msg.Kind = "ai_job"
		msg.JobID = jobIDs[0]
	} else {
		msg.Kind = "ai_job_batch"
		msg.JobIDs = append([]string(nil), jobIDs...)
	}
	return s.queues.enqueueAIJob(ctx.Context(), msg)
}

func (s *Server) processRenderSummaryShard(
	ctx *apptheory.Context,
	instanceSlug string,
	now time.Time,
	modelSet string,
	shard []queuedRenderSummary,
	out *linkRenderSummaryResult,
	allowLLM bool,
) {
	if s == nil || ctx == nil || out == nil || len(shard) == 0 {
		return
	}

	batchItems := make([]llm.RenderSummaryBatchItem, 0, len(shard))
	for _, q := range shard {
		batchItems = append(batchItems, llm.RenderSummaryBatchItem{
			ItemID: strings.TrimSpace(q.JobID),
			Input:  q.Inputs,
		})
	}

	results, usage, commonErrs := s.renderSummaryBatchResults(ctx, modelSet, allowLLM, batchItems)
	for _, q := range shard {
		id := strings.TrimSpace(q.JobID)
		res, itemErrs := resultForQueuedSummary(results, id, q.Inputs, commonErrs)
		s.persistQueuedSummaryResult(ctx, instanceSlug, now, modelSet, usage, itemErrs, q, res, out)
	}
}

func (s *Server) renderSummaryBatchResults(ctx *apptheory.Context, modelSet string, allowLLM bool, items []llm.RenderSummaryBatchItem) (map[string]ai.RenderSummaryResultV1, models.AIUsage, []models.AIError) {
	start := time.Now()
	results := map[string]ai.RenderSummaryResultV1{}
	usage := models.AIUsage{}
	commonErrs := []models.AIError{}

	lowerModelSet := strings.ToLower(strings.TrimSpace(modelSet))
	useDeterministic := true
	if allowLLM && strings.HasPrefix(lowerModelSet, "openai:") {
		apiKey, err := openAIAPIKey(ctx.Context())
		if err != nil || strings.TrimSpace(apiKey) == "" {
			commonErrs = append(commonErrs, models.AIError{Code: "llm_unavailable", Message: "LLM unavailable; used deterministic fallback", Retryable: false})
		} else {
			outMap, u, err := llm.RenderSummaryBatchOpenAI(ctx.Context(), apiKey, modelSet, items)
			if err != nil {
				commonErrs = append(commonErrs, models.AIError{Code: "llm_failed", Message: "LLM call failed; used deterministic fallback", Retryable: false})
			} else {
				results = outMap
				usage = u
				useDeterministic = false
			}
		}
	}

	if useDeterministic {
		for _, it := range items {
			results[it.ItemID] = ai.RenderSummaryDeterministicV1(it.Input)
		}
		usage = models.AIUsage{
			Provider:   modelSetDeterministic,
			Model:      modelSetDeterministic,
			ToolCalls:  0,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	return results, usage, commonErrs
}

func resultForQueuedSummary(results map[string]ai.RenderSummaryResultV1, jobID string, inputs ai.RenderSummaryInputsV1, commonErrs []models.AIError) (ai.RenderSummaryResultV1, []models.AIError) {
	res, ok := results[jobID]
	itemErrs := append([]models.AIError(nil), commonErrs...)
	if !ok || strings.TrimSpace(res.ShortSummary) == "" {
		res = ai.RenderSummaryDeterministicV1(inputs)
		itemErrs = append(itemErrs, models.AIError{Code: "llm_missing_output", Message: "LLM output missing; used deterministic fallback", Retryable: false})
	}
	return res, itemErrs
}

func (s *Server) persistQueuedSummaryResult(
	ctx *apptheory.Context,
	instanceSlug string,
	now time.Time,
	modelSet string,
	usage models.AIUsage,
	errs []models.AIError,
	q queuedRenderSummary,
	res ai.RenderSummaryResultV1,
	out *linkRenderSummaryResult,
) {
	if s == nil || ctx == nil || out == nil {
		return
	}

	b, err := json.Marshal(res)
	if err != nil {
		markQueuedSummaryError(out, q.Index)
		return
	}

	inputsHash, _ := ai.InputsHash(q.Inputs)
	id := strings.TrimSpace(q.JobID)
	item := &models.AIResult{
		ID:            id,
		InstanceSlug:  strings.TrimSpace(instanceSlug),
		Module:        ai.RenderSummaryLLMModule,
		PolicyVersion: ai.RenderSummaryLLMPolicyVersion,
		ModelSet:      modelSet,
		CacheScope:    string(ai.CacheScopeInstance),
		ScopeKey:      strings.TrimSpace(instanceSlug),
		InputsHash:    strings.TrimSpace(inputsHash),
		ResultJSON:    strings.TrimSpace(string(b)),
		Usage:         usage,
		Errors:        append([]models.AIError(nil), errs...),
		CreatedAt:     now,
		ExpiresAt:     now.Add(30 * 24 * time.Hour),
		JobID:         id,
		RequestID:     strings.TrimSpace(ctx.RequestID),
	}
	_ = item.UpdateKeys()

	if err := s.store.PutAIResult(ctx.Context(), item); err != nil {
		markQueuedSummaryError(out, q.Index)
		return
	}

	s.bestEffortUpdateAIJobStatusOK(ctx, id, now)
	applyQueuedSummaryOK(out, q.Index, res)
	out.Summary.Generated++
	out.Summary.Queued--

	s.maybeMirrorSummaryToArtifact(ctx, q.Artifact, res.ShortSummary, now, instanceSlug)
}

func (s *Server) bestEffortUpdateAIJobStatusOK(ctx *apptheory.Context, jobID string, now time.Time) {
	if s == nil || ctx == nil {
		return
	}
	job, err := s.store.GetAIJob(ctx.Context(), jobID)
	if err != nil || job == nil {
		return
	}
	job.Status = models.AIJobStatusOK
	job.ErrorCode = ""
	job.ErrorMessage = ""
	job.UpdatedAt = now
	job.RequestID = strings.TrimSpace(ctx.RequestID)
	_ = job.UpdateKeys()
	_ = s.store.PutAIJob(ctx.Context(), job)
}

func markQueuedSummaryError(out *linkRenderSummaryResult, index int) {
	if out == nil || index < 0 || index >= len(out.Links) {
		return
	}
	out.Links[index].Status = statusError
	out.Summary.Errors++
	out.Summary.Queued--
}

func applyQueuedSummaryOK(out *linkRenderSummaryResult, index int, res ai.RenderSummaryResultV1) {
	if out == nil || index < 0 || index >= len(out.Links) {
		return
	}
	out.Links[index].Status = statusOK
	out.Links[index].Cached = false
	out.Links[index].Summary = strings.TrimSpace(res.ShortSummary)
	out.Links[index].Bullets = append([]string(nil), res.KeyBullets...)
	out.Links[index].Risks = append([]ai.RenderSummaryRisk(nil), res.Risks...)
}

func shardQueuedSummaries(items []queuedRenderSummary, maxItems int64, maxTotalBytes int64) [][]queuedRenderSummary {
	if len(items) == 0 {
		return nil
	}
	if maxItems <= 0 {
		maxItems = 8
	}
	if maxTotalBytes <= 0 {
		maxTotalBytes = 64 * 1024
	}

	var shards [][]queuedRenderSummary
	var cur []queuedRenderSummary
	var curBytes int64

	flush := func() {
		if len(cur) == 0 {
			return
		}
		shards = append(shards, cur)
		cur = nil
		curBytes = 0
	}

	for _, it := range items {
		itemBytes := it.EstimatedBytes
		if itemBytes <= 0 {
			itemBytes = 1
		}

		if int64(len(cur)) >= maxItems || (curBytes > 0 && (curBytes+itemBytes) > maxTotalBytes) {
			flush()
		}

		cur = append(cur, it)
		curBytes += itemBytes
	}

	flush()
	return shards
}

func openAIAPIKey(ctx context.Context) (string, error) {
	if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
		return k, nil
	}
	return secrets.OpenAIServiceKey(ctx, nil)
}
