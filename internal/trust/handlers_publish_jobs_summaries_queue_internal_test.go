package trust

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestProcessQueuedRenderSummaries_InlinesDeterministicAndStoresResults(t *testing.T) {
	tdb := newSummariesFlowTestDB()

	tdb.qAIRes.On("CreateOrUpdate").Return(nil).Twice()

	var mu sync.Mutex
	jobCalls := 0
	tdb.qAIJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		mu.Lock()
		jobCalls++
		n := jobCalls
		mu.Unlock()

		dest := args.Get(0).(*models.AIJob)
		*dest = models.AIJob{ID: "job" + string(rune('0'+n))}
	}).Twice()
	tdb.qAIJob.On("CreateOrUpdate").Return(nil).Twice()

	// One artifact gets mirrored.
	tdb.qRender.On("CreateOrUpdate").Return(nil).Once()

	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	now := time.Unix(100, 0).UTC()

	out := &linkRenderSummaryResult{
		Summary: linkRenderSummarySummary{Queued: 2},
		Links: []linkRenderSummaryLinkResult{
			{Status: statusQueued},
			{Status: statusQueued},
		},
	}

	queued := []queuedRenderSummary{
		{
			Index:          0,
			JobID:          "job1",
			Inputs:         ai.RenderSummaryInputsV1{NormalizedURL: "https://a/", Text: "hello"},
			EstimatedBytes: 10,
			Artifact:       &models.RenderArtifact{ID: "rid"},
		},
		{
			Index:          1,
			JobID:          "job2",
			Inputs:         ai.RenderSummaryInputsV1{NormalizedURL: "https://b/", Text: "world"},
			EstimatedBytes: 10,
		},
	}

	s.processQueuedRenderSummaries(ctx, "inst", now, linkRenderSummaryJobConfig{
		ModelSet:         modelSetDeterministic,
		BatchingMode:     aiBatchingModeWorker,
		BatchMaxItems:    8,
		BatchMaxTotalBytes: 64 * 1024,
	}, queued, out)

	if out.Summary.Generated != 2 || out.Summary.Queued != 0 {
		t.Fatalf("unexpected summary counts: %#v", out.Summary)
	}
	if out.Links[0].Status != statusOK || strings.TrimSpace(out.Links[0].Summary) == "" {
		t.Fatalf("expected first link ok with summary, got %#v", out.Links[0])
	}
	if out.Links[1].Status != statusOK || strings.TrimSpace(out.Links[1].Summary) == "" {
		t.Fatalf("expected second link ok with summary, got %#v", out.Links[1])
	}
}

func TestProcessQueuedRenderSummaries_EnqueueFails_FallsBackToInlineLLM(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "k")

	outPayload, err := json.Marshal(map[string]any{
		"items": []any{
			map[string]any{
				"item_id":       "job1",
				"short_summary": "Summary A",
				"key_bullets":   []any{"a"},
				"risks":         []any{},
			},
			map[string]any{
				"item_id":       "job2",
				"short_summary": "Summary B",
				"key_bullets":   []any{"b"},
				"risks":         []any{},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal output payload: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"created": 123,
			"model":   "gpt-test",
			"choices": []any{map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": string(outPayload),
				},
			}},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL)

	tdb := newSummariesFlowTestDB()

	tdb.qAIRes.On("CreateOrUpdate").Return(nil).Twice()
	tdb.qAIJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.AIJob)
		*dest = models.AIJob{ID: "x"}
	}).Twice()
	tdb.qAIJob.On("CreateOrUpdate").Return(nil).Twice()

	s := &Server{
		store:  store.New(tdb.db),
		queues: nil, // force enqueue error to take fallback path
	}
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	now := time.Unix(100, 0).UTC()

	out := &linkRenderSummaryResult{
		Summary: linkRenderSummarySummary{Queued: 2},
		Links: []linkRenderSummaryLinkResult{
			{Status: statusQueued},
			{Status: statusQueued},
		},
	}

	queued := []queuedRenderSummary{
		{Index: 0, JobID: "job1", Inputs: ai.RenderSummaryInputsV1{NormalizedURL: "https://a/", Text: "hello"}, EstimatedBytes: 10},
		{Index: 1, JobID: "job2", Inputs: ai.RenderSummaryInputsV1{NormalizedURL: "https://b/", Text: "world"}, EstimatedBytes: 10},
	}

	s.processQueuedRenderSummaries(ctx, "inst", now, linkRenderSummaryJobConfig{
		ModelSet:           "openai:gpt-test",
		BatchingMode:       aiBatchingModeWorker,
		BatchMaxItems:      8,
		BatchMaxTotalBytes: 64 * 1024,
	}, queued, out)

	if out.Summary.Generated != 2 || out.Summary.Queued != 0 {
		t.Fatalf("unexpected summary counts: %#v", out.Summary)
	}
	if out.Links[0].Summary != "Summary A" || out.Links[1].Summary != "Summary B" {
		t.Fatalf("unexpected summaries: %#v", out.Links)
	}
}

func TestTryRenderSummaryBatchLLM_Anthropic_ReturnsLLMFailedOnHTTPError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "a")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	s := &Server{}
	items := []llm.RenderSummaryBatchItem{{ItemID: "job1", Input: ai.RenderSummaryInputsV1{NormalizedURL: "https://a/", Text: "hello"}}}
	_, _, errs, ok := s.tryRenderSummaryBatchLLM(context.Background(), "anthropic:claude-test", items)
	if ok || len(errs) != 1 || errs[0].Code != "llm_failed" {
		t.Fatalf("expected llm_failed, ok=%v errs=%#v", ok, errs)
	}
}
