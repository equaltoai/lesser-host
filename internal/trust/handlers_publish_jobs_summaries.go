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
	jobID string,
	renderPolicy string,
	instCfg instanceTrustConfig,
	pricingMultiplierBps int64,
	canonicalLinks []string,
) publishJobModuleResponse {
	if s == nil || s.store == nil || s.store.DB == nil {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "internal error",
				RequestedCredits: 0,
				DebitedCredits:   0,
			},
		}
	}
	if ctx == nil {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "internal error",
				RequestedCredits: 0,
				DebitedCredits:   0,
			},
		}
	}
	if s.artifacts == nil {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "artifact store not configured",
				RequestedCredits: 0,
				DebitedCredits:   0,
			},
		}
	}

	now := time.Now().UTC()

	policy := strings.ToLower(strings.TrimSpace(renderPolicy))
	if policy != "always" && policy != "suspicious" {
		policy = "suspicious"
	}

	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == "allow"
	modelSet := "deterministic"
	if instCfg.AIEnabled && strings.TrimSpace(instCfg.AIModelSet) != "" {
		modelSet = strings.TrimSpace(instCfg.AIModelSet)
	}

	// Combine request-level pricing and instance AI multiplier.
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

	out := linkRenderSummaryResult{
		RenderPolicy:  policy,
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Summary: linkRenderSummarySummary{
			TotalLinks: len(canonicalLinks),
		},
		Links: make([]linkRenderSummaryLinkResult, 0, len(canonicalLinks)),
	}

	var queued []queuedRenderSummary

	var budgetMonth string
	var budgetIncluded int64
	var budgetUsed int64
	var budgetRemaining int64
	var budgetRequested int64
	var budgetDebited int64
	var budgetOver bool

	for _, raw := range canonicalLinks {
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
		case "invalid":
			out.Summary.Invalid++
			linkOut.Status = "invalid"
			out.Links = append(out.Links, linkOut)
			continue
		case "blocked":
			out.Summary.Blocked++
			linkOut.Status = "blocked"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if !shouldRenderLink(policy, risk) {
			out.Summary.Skipped++
			linkOut.Status = "skipped"
			out.Links = append(out.Links, linkOut)
			continue
		}

		out.Summary.Candidates++

		renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)
		linkOut.RenderID = renderID

		artifact, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
		if theoryErrors.IsNotFound(err) {
			out.Summary.Queued++
			linkOut.Status = "queued"
			out.Links = append(out.Links, linkOut)
			continue
		}
		if err != nil {
			out.Summary.Errors++
			linkOut.Status = "error"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if strings.TrimSpace(artifact.ErrorCode) != "" {
			out.Summary.Errors++
			linkOut.Status = "error"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if strings.TrimSpace(artifact.SnapshotObjectKey) == "" {
			out.Summary.Queued++
			linkOut.Status = "queued"
			out.Links = append(out.Links, linkOut)
			continue
		}

		inputs := ai.RenderSummaryInputsV1{
			RenderID:      strings.TrimSpace(artifact.ID),
			NormalizedURL: strings.TrimSpace(artifact.NormalizedURL),
			ResolvedURL:   strings.TrimSpace(artifact.ResolvedURL),
			LinkRisk:      risk,
			Text:          strings.TrimSpace(artifact.TextPreview),
		}
		if inputs.NormalizedURL == "" {
			inputs.NormalizedURL = normalized
		}
		if !artifact.RenderedAt.IsZero() {
			inputs.RenderedAt = artifact.RenderedAt.UTC().Format(time.RFC3339Nano)
		}

		resp, err := s.ai.GetOrQueue(ctx.Context(), ai.Request{
			InstanceSlug:         strings.TrimSpace(instanceSlug),
			RequestID:            strings.TrimSpace(ctx.RequestID),
			Module:               ai.RenderSummaryLLMModule,
			PolicyVersion:        ai.RenderSummaryLLMPolicyVersion,
			ModelSet:             modelSet,
			CacheScope:           ai.CacheScopeInstance,
			Inputs:               inputs,
			BaseCredits:          linkRenderSummaryCreditCost,
			PricingMultiplierBps: combinedPricingBps,
			AllowOverage:         allowOverage,
			JobTTL:               30 * 24 * time.Hour,
		})
		if err != nil {
			out.Summary.Errors++
			linkOut.Status = "error"
			out.Links = append(out.Links, linkOut)
			continue
		}

		budgetRequested += resp.Budget.RequestedCredits
		budgetDebited += resp.Budget.DebitedCredits
		if resp.Budget.OverBudget {
			budgetOver = true
		}
		if strings.TrimSpace(resp.Budget.Month) != "" {
			budgetMonth = strings.TrimSpace(resp.Budget.Month)
			budgetIncluded = resp.Budget.IncludedCredits
			budgetUsed = resp.Budget.UsedCredits
			budgetRemaining = resp.Budget.RemainingCredits
		}

		linkOut.SummaryJobID = strings.TrimSpace(resp.JobID)

		switch resp.Status {
		case ai.JobStatusOK:
			out.Summary.Cached++
			linkOut.Status = "ok"
			linkOut.Cached = true

			if resp.Result != nil && strings.TrimSpace(resp.Result.ResultJSON) != "" {
				var parsed ai.RenderSummaryResultV1
				if err := json.Unmarshal([]byte(resp.Result.ResultJSON), &parsed); err == nil {
					linkOut.Summary = strings.TrimSpace(parsed.ShortSummary)
					linkOut.Bullets = append([]string(nil), parsed.KeyBullets...)
					linkOut.Risks = append([]ai.RenderSummaryRisk(nil), parsed.Risks...)
				}
			}

			// Best-effort mirror short summary into the render artifact for backwards-compatible consumers.
			if strings.TrimSpace(linkOut.Summary) != "" &&
				(strings.TrimSpace(artifact.Summary) == "" || strings.TrimSpace(artifact.SummaryPolicyVersion) != linkRenderSummaryPolicyVersion) {
				artifact.SummaryPolicyVersion = linkRenderSummaryPolicyVersion
				artifact.Summary = strings.TrimSpace(linkOut.Summary)
				artifact.SummarizedAt = now
				artifact.RequestID = strings.TrimSpace(ctx.RequestID)
				artifact.RequestedBy = strings.TrimSpace(instanceSlug)
				_ = artifact.UpdateKeys()
				_ = s.store.PutRenderArtifact(ctx.Context(), artifact)
			}
		case ai.JobStatusQueued:
			out.Summary.Queued++
			linkOut.Status = "queued"
			estimatedBytes := int64(len([]byte(inputs.Text)))
			estimatedBytes += int64(len(inputs.RenderID) + len(inputs.NormalizedURL) + len(inputs.ResolvedURL) + len(inputs.RenderedAt) + len(inputs.LinkRisk))
			queued = append(queued, queuedRenderSummary{
				Index:          len(out.Links),
				JobID:          strings.TrimSpace(resp.JobID),
				Inputs:         inputs,
				EstimatedBytes: estimatedBytes,
				Artifact:       artifact,
			})
		case ai.JobStatusNotCheckedBudget:
			out.Summary.NotCheckedBudget++
			linkOut.Status = "not_checked_budget"
		default:
			out.Summary.Errors++
			linkOut.Status = "error"
		}

		out.Links = append(out.Links, linkOut)
	}

	if len(queued) > 0 {
		mode := strings.ToLower(strings.TrimSpace(instCfg.AIBatchingMode))
		if mode == "" {
			mode = "none"
		}

		maxItems := instCfg.AIBatchMaxItems
		if maxItems <= 0 {
			maxItems = 8
		}

		maxBytes := instCfg.AIBatchMaxTotalBytes
		if maxBytes <= 0 {
			maxBytes = 64 * 1024
		}

		totalBytes := int64(0)
		for _, q := range queued {
			totalBytes += q.EstimatedBytes
		}

		lowerModelSet := strings.ToLower(strings.TrimSpace(modelSet))

		inline := false
		if !strings.HasPrefix(lowerModelSet, "openai:") {
			// Deterministic or unsupported provider; compute immediately.
			inline = true
		} else if mode == "in_request" {
			inline = true
		} else if mode == "hybrid" {
			inline = int64(len(queued)) <= maxItems && totalBytes <= maxBytes
		}

		processShard := func(shard []queuedRenderSummary, allowLLM bool) {
			if len(shard) == 0 {
				return
			}

			batchItems := make([]llm.RenderSummaryBatchItem, 0, len(shard))
			for _, q := range shard {
				batchItems = append(batchItems, llm.RenderSummaryBatchItem{
					ItemID: strings.TrimSpace(q.JobID),
					Input:  q.Inputs,
				})
			}

			start := time.Now()
			results := map[string]ai.RenderSummaryResultV1{}
			usage := models.AIUsage{}
			commonErrs := []models.AIError{}

			useDeterministic := true
			if allowLLM && strings.HasPrefix(lowerModelSet, "openai:") {
				apiKey, err := openAIAPIKey(ctx.Context())
				if err != nil || strings.TrimSpace(apiKey) == "" {
					commonErrs = append(commonErrs, models.AIError{Code: "llm_unavailable", Message: "LLM unavailable; used deterministic fallback", Retryable: false})
				} else {
					outMap, u, err := llm.RenderSummaryBatchOpenAI(ctx.Context(), apiKey, modelSet, batchItems)
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
				for _, it := range batchItems {
					results[it.ItemID] = ai.RenderSummaryDeterministicV1(it.Input)
				}
				usage = models.AIUsage{
					Provider:   "deterministic",
					Model:      "deterministic",
					ToolCalls:  0,
					DurationMs: time.Since(start).Milliseconds(),
				}
			}

			for _, q := range shard {
				id := strings.TrimSpace(q.JobID)
				res, ok := results[id]
				itemErrs := append([]models.AIError(nil), commonErrs...)
				if !ok || strings.TrimSpace(res.ShortSummary) == "" {
					res = ai.RenderSummaryDeterministicV1(q.Inputs)
					itemErrs = append(itemErrs, models.AIError{Code: "llm_missing_output", Message: "LLM output missing; used deterministic fallback", Retryable: false})
				}

				b, err := json.Marshal(res)
				if err != nil {
					if q.Index >= 0 && q.Index < len(out.Links) {
						out.Links[q.Index].Status = "error"
						out.Summary.Errors++
						out.Summary.Queued--
					}
					continue
				}

				inputsHash, _ := ai.InputsHash(q.Inputs)

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
					Errors:        append([]models.AIError(nil), itemErrs...),
					CreatedAt:     now,
					ExpiresAt:     now.Add(30 * 24 * time.Hour),
					JobID:         id,
					RequestID:     strings.TrimSpace(ctx.RequestID),
				}
				_ = item.UpdateKeys()

				if err := s.store.PutAIResult(ctx.Context(), item); err != nil {
					if q.Index >= 0 && q.Index < len(out.Links) {
						out.Links[q.Index].Status = "error"
						out.Summary.Errors++
						out.Summary.Queued--
					}
					continue
				}

				// Best-effort job status update (worker also handles existing results).
				if job, err := s.store.GetAIJob(ctx.Context(), id); err == nil && job != nil {
					job.Status = models.AIJobStatusOK
					job.ErrorCode = ""
					job.ErrorMessage = ""
					job.UpdatedAt = now
					job.RequestID = strings.TrimSpace(ctx.RequestID)
					_ = job.UpdateKeys()
					_ = s.store.PutAIJob(ctx.Context(), job)
				}

				if q.Index >= 0 && q.Index < len(out.Links) {
					out.Links[q.Index].Status = "ok"
					out.Links[q.Index].Cached = false
					out.Links[q.Index].Summary = strings.TrimSpace(res.ShortSummary)
					out.Links[q.Index].Bullets = append([]string(nil), res.KeyBullets...)
					out.Links[q.Index].Risks = append([]ai.RenderSummaryRisk(nil), res.Risks...)
				}
				out.Summary.Generated++
				out.Summary.Queued--

				if q.Artifact != nil && strings.TrimSpace(res.ShortSummary) != "" &&
					(strings.TrimSpace(q.Artifact.Summary) == "" || strings.TrimSpace(q.Artifact.SummaryPolicyVersion) != linkRenderSummaryPolicyVersion) {
					q.Artifact.SummaryPolicyVersion = linkRenderSummaryPolicyVersion
					q.Artifact.Summary = strings.TrimSpace(res.ShortSummary)
					q.Artifact.SummarizedAt = now
					q.Artifact.RequestID = strings.TrimSpace(ctx.RequestID)
					q.Artifact.RequestedBy = strings.TrimSpace(instanceSlug)
					_ = q.Artifact.UpdateKeys()
					_ = s.store.PutRenderArtifact(ctx.Context(), q.Artifact)
				}
			}
		}

		shards := shardQueuedSummaries(queued, maxItems, maxBytes)
		if inline {
			for _, shard := range shards {
				processShard(shard, true)
			}
		} else {
			enqueue := func(jobIDs []string) error {
				if len(jobIDs) == 0 {
					return nil
				}
				if s.queues == nil {
					return fmt.Errorf("safety queue not configured")
				}

				msg := ai.JobMessage{}
				if len(jobIDs) == 1 && mode == "none" {
					msg.Kind = "ai_job"
					msg.JobID = jobIDs[0]
				} else {
					msg.Kind = "ai_job_batch"
					msg.JobIDs = append([]string(nil), jobIDs...)
				}
				return s.queues.enqueueAIJob(ctx.Context(), msg)
			}

			for _, shard := range shards {
				if mode == "none" {
					for _, q := range shard {
						id := strings.TrimSpace(q.JobID)
						if err := enqueue([]string{id}); err != nil {
							processShard([]queuedRenderSummary{q}, true)
						}
					}
					continue
				}

				ids := make([]string, 0, len(shard))
				for _, q := range shard {
					ids = append(ids, strings.TrimSpace(q.JobID))
				}
				if err := enqueue(ids); err != nil {
					processShard(shard, true)
				}
			}
		}
	}

	status := "ok"
	if out.Summary.NotCheckedBudget > 0 && (out.Summary.Cached+out.Summary.Generated+out.Summary.Queued) == 0 {
		status = "not_checked_budget"
	} else if out.Summary.Errors > 0 && (out.Summary.Cached+out.Summary.Generated+out.Summary.Queued) == 0 {
		status = "error"
	}

	cached := out.Summary.Candidates > 0 && out.Summary.Generated == 0 && out.Summary.Queued == 0 && out.Summary.Cached > 0

	month := strings.TrimSpace(budgetMonth)
	if month == "" {
		month = now.Format("2006-01")
	}
	reason := "queued"
	if budgetDebited > 0 {
		reason = "debited"
		if budgetOver {
			reason = "overage"
		}
	} else if cached {
		reason = "cache_hit"
	} else if out.Summary.NotCheckedBudget > 0 {
		reason = "budget"
	}

	return publishJobModuleResponse{
		Name:          "link_render_summary",
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Status:        status,
		Cached:        cached,
		Budget: budgetDecision{
			Allowed:          status != "not_checked_budget",
			OverBudget:       budgetOver,
			Reason:           reason,
			Month:            month,
			IncludedCredits:  budgetIncluded,
			UsedCredits:      budgetUsed,
			RemainingCredits: budgetRemaining,
			RequestedCredits: budgetRequested,
			DebitedCredits:   budgetDebited,
		},
		Result: out,
	}
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
