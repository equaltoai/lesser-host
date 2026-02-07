package aiworker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestCallModerationLLM_ErrorsAndSuccess(t *testing.T) {
	srv := &Server{}

	// Non-LLM model sets are skipped.
	if got, _, errs := srv.callModerationLLM(context.Background(), "deterministic", "job", nil, nil); got.Decision != "" || errs != nil {
		t.Fatalf("expected skip without errors, got=%#v errs=%#v", got, errs)
	}

	// Job id is required when configured for LLM.
	if _, _, errs := srv.callModerationLLM(context.Background(), "openai:gpt-test", "", func(context.Context, string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
		return nil, models.AIUsage{}, nil
	}, nil); len(errs) != 1 || errs[0].Code != "invalid_inputs" {
		t.Fatalf("expected invalid_inputs, got %#v", errs)
	}

	// Call must be provided.
	if _, _, errs := srv.callModerationLLM(context.Background(), "openai:gpt-test", "job", nil, nil); len(errs) != 1 || errs[0].Code != "internal_error" {
		t.Fatalf("expected internal_error, got %#v", errs)
	}

	t.Setenv("OPENAI_API_KEY", "k")

	// Call failure.
	if _, _, errs := srv.callModerationLLM(context.Background(), "openai:gpt-test", "job", func(context.Context, string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
		return nil, models.AIUsage{}, fmt.Errorf("boom")
	}, nil); len(errs) != 1 || errs[0].Code != "llm_failed" {
		t.Fatalf("expected llm_failed, got %#v", errs)
	}

	// Missing output.
	if _, _, errs := srv.callModerationLLM(context.Background(), "openai:gpt-test", "job", func(context.Context, string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
		return map[string]ai.ModerationResultV1{}, models.AIUsage{Provider: "openai"}, nil
	}, nil); len(errs) != 1 || errs[0].Code != "llm_missing_output" {
		t.Fatalf("expected llm_missing_output, got %#v", errs)
	}

	// Success.
	got, usage, errs := srv.callModerationLLM(context.Background(), "openai:gpt-test", "job", func(context.Context, string) (map[string]ai.ModerationResultV1, models.AIUsage, error) {
		return map[string]ai.ModerationResultV1{
			"job": {Kind: "moderation_text", Version: "v1", Decision: "allow"},
		}, models.AIUsage{Provider: "openai", Model: "gpt-test", TotalTokens: 1}, nil
	}, nil)
	if errs != nil || strings.TrimSpace(got.Decision) != "allow" {
		t.Fatalf("expected allow, got=%#v errs=%#v", got, errs)
	}
	if usage.Provider != "openai" || usage.TotalTokens != 1 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestSanitizeClaimVerifyCitations_FiltersAndBounds(t *testing.T) {
	t.Parallel()

	evidenceIDs := map[string]struct{}{"s1": {}}
	evText := strings.Repeat("the quick brown fox jumps over the lazy dog ", 50)
	evidenceText := map[string]string{"s1": evText}

	long := evText[:250]
	citations := []ai.ClaimVerifyCitationV1{
		{SourceID: " ", Quote: "x"},
		{SourceID: "missing", Quote: "brown fox jumps"},
		{SourceID: "s1", Quote: "not present"},
		{SourceID: "s1", Quote: "quick brown"},
		{SourceID: "s1", Quote: "quick brown fox"},
		{SourceID: "s1", Quote: long},
		{SourceID: "s1", Quote: "lazy dog"},
		{SourceID: "s1", Quote: "the quick"},
	}

	out := sanitizeClaimVerifyCitations(citations, evidenceIDs, evidenceText)
	if len(out) != 3 {
		t.Fatalf("expected 3 citations max, got %#v", out)
	}
	if out[0].SourceID != "s1" || out[0].Quote == "" {
		t.Fatalf("unexpected first citation: %#v", out[0])
	}
	if len(out[2].Quote) > 200 {
		t.Fatalf("expected quote bounded to 200, got len=%d", len(out[2].Quote))
	}
}

func TestApplyClaimVerifyRetrievalEffects_NoOpAndMerge(t *testing.T) {
	t.Parallel()

	start := time.Unix(100, 0).UTC()
	in := models.AIUsage{Provider: "openai", Model: "gpt"}

	if got := applyClaimVerifyRetrievalEffects(nil, in, start, true, "d", models.AIUsage{Provider: "openai"}); got.Provider != "openai" {
		t.Fatalf("expected nil result to no-op, got %#v", got)
	}

	res := &ai.ClaimVerifyResultV1{Kind: "claim_verify", Version: "v1"}
	got := applyClaimVerifyRetrievalEffects(res, in, start, true, " disc ", models.AIUsage{Provider: "openai", TotalTokens: 2})
	if res.Disclaimer != "disc" || len(res.Warnings) != 1 || res.Warnings[0] != "web_search_used" {
		t.Fatalf("unexpected retrieval effects: %#v", res)
	}
	if got.TotalTokens != 2 {
		t.Fatalf("expected merged usage, got %#v", got)
	}
}
