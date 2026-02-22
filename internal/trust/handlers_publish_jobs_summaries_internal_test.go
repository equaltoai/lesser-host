package trust

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestLinkRenderSummaryBudgetTotalsAdd(t *testing.T) {
	t.Parallel()

	var b *linkRenderSummaryBudgetTotals
	b.add(ai.BudgetDecision{RequestedCredits: 1})

	b = &linkRenderSummaryBudgetTotals{}
	b.add(ai.BudgetDecision{RequestedCredits: 2, DebitedCredits: 1})
	b.add(ai.BudgetDecision{OverBudget: true, RequestedCredits: 3, DebitedCredits: 3, Month: " 2026-01 ", IncludedCredits: 10, UsedCredits: 4, RemainingCredits: 6})
	if b.RequestedCredits != 5 || b.DebitedCredits != 4 || !b.OverBudget {
		t.Fatalf("unexpected totals: %#v", b)
	}
	if b.Month != "2026-01" || b.IncludedCredits != 10 || b.UsedCredits != 4 || b.RemainingCredits != 6 {
		t.Fatalf("unexpected month snapshot: %#v", b)
	}
}

func TestLinkRenderSummaryErrorResponse(t *testing.T) {
	t.Parallel()

	out := linkRenderSummaryErrorResponse("")
	if out.Status != statusError || out.Budget.Reason == "" {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestLinkRenderSummaryJobConfigFromInstance(t *testing.T) {
	t.Parallel()

	cfg := linkRenderSummaryJobConfigFromInstance(instanceTrustConfig{
		OveragePolicy:          overagePolicyAllow,
		AIEnabled:              true,
		AIModelSet:             " openai:gpt ",
		AIPricingMultiplierBps: 20000,
		AIBatchingMode:         "bad",
		AIBatchMaxItems:        0,
		AIBatchMaxTotalBytes:   0,
		AIMaxInflightJobs:      99,
	}, 5000)

	if !cfg.AllowOverage {
		t.Fatalf("expected allow overage")
	}
	if cfg.ModelSet != "openai:gpt" {
		t.Fatalf("unexpected model set: %q", cfg.ModelSet)
	}
	if cfg.CombinedPricingBps != 10000 {
		t.Fatalf("unexpected combined pricing: %d", cfg.CombinedPricingBps)
	}
	if cfg.BatchingMode != aiBatchingModeNone || cfg.BatchMaxItems != 8 || cfg.BatchMaxTotalBytes != 64*1024 {
		t.Fatalf("unexpected batching defaults: %#v", cfg)
	}
	if cfg.MaxInflightJobs != 99 {
		t.Fatalf("unexpected max inflight: %d", cfg.MaxInflightJobs)
	}
}

func TestNewLinkRenderSummaryResultAndCounts(t *testing.T) {
	t.Parallel()

	out := newLinkRenderSummaryResult(" suspicious ", 3)
	if out.RenderPolicy != "suspicious" || out.Summary.TotalLinks != 3 || len(out.Links) != 0 {
		t.Fatalf("unexpected result: %#v", out)
	}

	sum := linkRenderSummarySummary{}
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusInvalid})
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusBlocked})
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusSkipped})
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusOK, Cached: true})
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusQueued})
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusNotCheckedBudget})
	applyLinkRenderSummaryCounts(&sum, linkRenderSummaryLinkResult{Status: statusError})
	if sum.Invalid != 1 || sum.Blocked != 1 || sum.Skipped != 1 {
		t.Fatalf("unexpected terminal counts: %#v", sum)
	}
	if sum.Candidates != 4 || sum.Cached != 1 || sum.Queued != 1 || sum.NotCheckedBudget != 1 || sum.Errors != 1 {
		t.Fatalf("unexpected candidate counts: %#v", sum)
	}
}

func TestLinkRenderSummaryStatusCachedReason(t *testing.T) {
	t.Parallel()

	if got := linkRenderSummaryStatus(linkRenderSummarySummary{NotCheckedBudget: 1}); got != statusNotCheckedBudget {
		t.Fatalf("unexpected status: %q", got)
	}
	if got := linkRenderSummaryStatus(linkRenderSummarySummary{Errors: 1}); got != statusError {
		t.Fatalf("unexpected status: %q", got)
	}
	if got := linkRenderSummaryStatus(linkRenderSummarySummary{Candidates: 1, Cached: 1}); got != statusOK {
		t.Fatalf("unexpected status: %q", got)
	}

	if !linkRenderSummaryCached(linkRenderSummarySummary{Candidates: 1, Cached: 1}) {
		t.Fatalf("expected cached summary")
	}
	if linkRenderSummaryCached(linkRenderSummarySummary{Candidates: 1, Cached: 1, Generated: 1}) {
		t.Fatalf("expected not cached when generated present")
	}

	reason := linkRenderSummaryReason(linkRenderSummaryBudgetTotals{DebitedCredits: 1, OverBudget: true}, false, linkRenderSummarySummary{})
	if reason != budgetReasonOverage {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestBuildRenderSummaryInputsAndFillFromResult(t *testing.T) {
	t.Parallel()

	in := buildRenderSummaryInputs(nil, "inst", " "+testURLExampleCom+" ", "low")
	if in.NormalizedURL != testURLExampleCom || in.RenderID == "" {
		t.Fatalf("unexpected inputs: %#v", in)
	}

	renderedAt := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)
	in = buildRenderSummaryInputs(&models.RenderArtifact{
		ID:            "rid",
		NormalizedURL: "",
		ResolvedURL:   "https://example.com/real",
		TextPreview:   "text",
		RenderedAt:    renderedAt,
	}, "inst", "https://fallback", "medium")
	if in.RenderID != "rid" || in.NormalizedURL != "https://fallback" || in.ResolvedURL == "" || in.Text != "text" || in.RenderedAt == "" {
		t.Fatalf("unexpected inputs: %#v", in)
	}

	out := linkRenderSummaryLinkResult{}
	fillRenderSummaryFromResult(&out, &models.AIResult{ResultJSON: "not json"})
	if out.Summary != "" {
		t.Fatalf("expected no change on invalid json: %#v", out)
	}

	b, _ := json.Marshal(ai.RenderSummaryResultV1{
		ShortSummary: " ok ",
		KeyBullets:   []string{"a", "b"},
		Risks:        []ai.RenderSummaryRisk{{Code: "r1", Severity: riskLow, Summary: "s"}},
	})
	fillRenderSummaryFromResult(&out, &models.AIResult{ResultJSON: string(b)})
	if out.Summary != "ok" || len(out.Bullets) != 2 || len(out.Risks) != 1 {
		t.Fatalf("unexpected filled result: %#v", out)
	}
}

func TestEstimateRenderSummaryInputsBytes(t *testing.T) {
	t.Parallel()

	if got := estimateRenderSummaryInputsBytes(ai.RenderSummaryInputsV1{}); got != 1 {
		t.Fatalf("expected min 1, got %d", got)
	}
	if got := estimateRenderSummaryInputsBytes(ai.RenderSummaryInputsV1{Text: "xx"}); got <= 1 {
		t.Fatalf("expected >1, got %d", got)
	}
}

func TestShouldInlineQueuedSummaries(t *testing.T) {
	t.Parallel()

	queued := []queuedRenderSummary{{EstimatedBytes: 10}, {EstimatedBytes: 20}}

	if !shouldInlineQueuedSummaries("deterministic", aiBatchingModeNone, queued, 8, 64*1024) {
		t.Fatalf("expected inline for non-openai/anthropic")
	}
	if !shouldInlineQueuedSummaries("openai:gpt", aiBatchingModeInRequest, queued, 8, 64*1024) {
		t.Fatalf("expected inline for in_request")
	}
	if shouldInlineQueuedSummaries("openai:gpt", aiBatchingModeWorker, queued, 8, 64*1024) {
		t.Fatalf("expected not inline for worker")
	}
	if shouldInlineQueuedSummaries("openai:gpt", aiBatchingModeHybrid, make([]queuedRenderSummary, 9), 8, 64*1024) {
		t.Fatalf("expected not inline when too many items")
	}
	if shouldInlineQueuedSummaries("openai:gpt", aiBatchingModeHybrid, queued, 8, 16) {
		t.Fatalf("expected not inline when too many bytes")
	}
}

func TestEnqueueRenderSummaryJobs_BuildsKindsAndValidatesDeps(t *testing.T) {
	t.Parallel()

	var s *Server
	if err := s.enqueueRenderSummaryJobs(&apptheory.Context{}, aiBatchingModeNone, []string{"x"}); err == nil {
		t.Fatalf("expected error for nil server")
	}

	s = &Server{queues: &queueClient{safetyQueueURL: ""}}
	if err := s.enqueueRenderSummaryJobs(&apptheory.Context{}, aiBatchingModeNone, []string{"x"}); err == nil {
		t.Fatalf("expected error for missing queue url")
	}
	if err := s.enqueueRenderSummaryJobs(&apptheory.Context{}, aiBatchingModeWorker, []string{"a", "b"}); err == nil {
		t.Fatalf("expected error for missing queue url")
	}
	if err := s.enqueueRenderSummaryJobs(&apptheory.Context{}, aiBatchingModeNone, nil); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

func TestRenderSummaryBatchDeterministicAndResults(t *testing.T) {
	t.Parallel()

	items := []llm.RenderSummaryBatchItem{
		{ItemID: "a", Input: ai.RenderSummaryInputsV1{NormalizedURL: "https://a", Text: "hello"}},
		{ItemID: "b", Input: ai.RenderSummaryInputsV1{NormalizedURL: "https://b", Text: "world"}},
	}

	results := renderSummaryBatchDeterministic(items)
	if len(results) != 2 || results["a"].ShortSummary == "" {
		t.Fatalf("unexpected results: %#v", results)
	}

	s := &Server{}
	outMap, usage, errs := s.renderSummaryBatchResults(nil, "openai:gpt", false, items)
	if len(errs) != 0 || len(outMap) != 2 {
		t.Fatalf("unexpected batch output: map=%#v errs=%#v", outMap, errs)
	}
	if usage.Provider != modelSetDeterministic || usage.Model != modelSetDeterministic {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestResultForQueuedSummaryFallback(t *testing.T) {
	t.Parallel()

	res, errs := resultForQueuedSummary(map[string]ai.RenderSummaryResultV1{}, "missing", ai.RenderSummaryInputsV1{Text: "x"}, nil)
	if res.ShortSummary == "" || len(errs) == 0 {
		t.Fatalf("expected fallback + errs, got res=%#v errs=%#v", res, errs)
	}
}

func TestQueuedSummaryApplyAndShard(t *testing.T) {
	t.Parallel()

	out := &linkRenderSummaryResult{
		Summary: linkRenderSummarySummary{Queued: 1},
		Links:   []linkRenderSummaryLinkResult{{Status: statusQueued}},
	}

	markQueuedSummaryError(out, 0)
	if out.Links[0].Status != statusError || out.Summary.Errors != 1 || out.Summary.Queued != 0 {
		t.Fatalf("unexpected markQueuedSummaryError: %#v", out)
	}

	out.Summary.Queued = 1
	out.Links[0].Status = statusQueued
	applyQueuedSummaryOK(out, 0, ai.RenderSummaryResultV1{ShortSummary: " ok ", KeyBullets: []string{"a"}})
	if out.Links[0].Status != statusOK || out.Links[0].Cached || out.Links[0].Summary != "ok" || len(out.Links[0].Bullets) != 1 {
		t.Fatalf("unexpected applyQueuedSummaryOK: %#v", out)
	}

	items := []queuedRenderSummary{{EstimatedBytes: 10}, {EstimatedBytes: 10}, {EstimatedBytes: 10}}
	shards := shardQueuedSummaries(items, 2, 15)
	if len(shards) < 2 {
		t.Fatalf("expected sharding, got %#v", shards)
	}
}

func TestAPIKeyEnvShortCircuit(t *testing.T) {
	t.Parallel()

	prev := os.Getenv("OPENAI_API_KEY")
	t.Cleanup(func() { _ = os.Setenv("OPENAI_API_KEY", prev) })
	_ = os.Setenv("OPENAI_API_KEY", "k")
	k, err := openAIAPIKey(context.Background())
	if err != nil || k != "k" {
		t.Fatalf("unexpected key: %q err=%v", k, err)
	}

	prevA := os.Getenv("ANTHROPIC_API_KEY")
	prevC := os.Getenv("CLAUDE_API_KEY")
	t.Cleanup(func() {
		_ = os.Setenv("ANTHROPIC_API_KEY", prevA)
		_ = os.Setenv("CLAUDE_API_KEY", prevC)
	})
	_ = os.Setenv("ANTHROPIC_API_KEY", "a")
	_ = os.Setenv("CLAUDE_API_KEY", "c")
	k, err = anthropicAPIKey(context.Background())
	if err != nil || k != "a" {
		t.Fatalf("unexpected key: %q err=%v", k, err)
	}
}

func TestRenderSummaryPaths_UsesRenderIDFunc(t *testing.T) {
	t.Parallel()

	id := rendering.RenderArtifactIDForInstance(rendering.RenderPolicyVersion, "inst", testURLExampleCom)
	if len(id) != 64 {
		t.Fatalf("expected hex id, got %q", id)
	}
}
