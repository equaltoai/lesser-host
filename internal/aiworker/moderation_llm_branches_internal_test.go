package aiworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestRunModerationTextLLMV1_OpenAISuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("c", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   "openai:gpt-test",
		InputsJSON: `{"text":"Alice is 30 years old."}`,
	}

	s := &Server{comprehend: fakeComprehend{}}

	t.Setenv("OPENAI_API_KEY", "k")

	outPayload, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{
			"item_id":    jobID,
			"decision":   testDecisionAllow,
			"categories": []any{},
			"highlights": []any{},
			"notes":      "",
		}},
	})
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openAIChatCompletionResponseJSON(t, "gpt-test", string(outPayload)))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("OPENAI_BASE_URL", ts.URL)

	outJSON, usage, errs, err := s.runModerationTextLLMV1(ctx, job)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, testProviderOpenAI, usage.Provider)
	require.Contains(t, outJSON, `"decision":"`+testDecisionAllow+`"`)
}

func TestRunModerationTextLLMV1_AnthropicSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("c", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   "anthropic:claude-test",
		InputsJSON: `{"text":"Alice is 30 years old."}`,
	}

	s := &Server{comprehend: fakeComprehend{}}

	t.Setenv("ANTHROPIC_API_KEY", "a")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")

		outPayload := map[string]any{
			"items": []any{map[string]any{
				"item_id":    jobID,
				"decision":   testDecisionReview,
				"categories": []any{},
				"highlights": []any{},
				"notes":      "ok",
			}},
		}
		_, _ = w.Write(anthropicToolUseResponseJSON(t, "claude-test", "moderation_batch", outPayload))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("ANTHROPIC_BASE_URL", ts.URL)

	outJSON, usage, errs, err := s.runModerationTextLLMV1(ctx, job)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, testProviderAnthropic, usage.Provider)
	require.Contains(t, outJSON, `"decision":"`+testDecisionReview+`"`)
}

func TestRunModerationTextLLMV1_MissingOutputFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("c", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   "openai:gpt-test",
		InputsJSON: `{"text":"Alice is 30 years old."}`,
	}

	s := &Server{comprehend: fakeComprehend{}}

	t.Setenv("OPENAI_API_KEY", "k")

	outPayload, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{
			"item_id":    "other",
			"decision":   testDecisionAllow,
			"categories": []any{},
			"highlights": []any{},
			"notes":      "",
		}},
	})
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openAIChatCompletionResponseJSON(t, "gpt-test", string(outPayload)))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("OPENAI_BASE_URL", ts.URL)

	outJSON, _, errs, err := s.runModerationTextLLMV1(ctx, job)
	require.NoError(t, err)
	require.Len(t, errs, 1)
	require.Equal(t, aiErrorCodeLLMMissingOutput, errs[0].Code)
	require.Contains(t, outJSON, `"decision":`)
}

func TestRunModerationImageLLMV1_OpenAISuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("d", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   "openai:gpt-test",
		InputsJSON: `{"object_key":"moderation/inst/obj","bytes":10,"content_type":"image/png"}`,
	}

	t.Setenv("OPENAI_API_KEY", "k")

	outPayload, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{
			"item_id":    jobID,
			"decision":   testDecisionBlock,
			"categories": []any{},
			"highlights": []any{},
			"notes":      "",
		}},
	})
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openAIChatCompletionResponseJSON(t, "gpt-test", string(outPayload)))
	}))
	t.Cleanup(ts.Close)
	t.Setenv("OPENAI_BASE_URL", ts.URL)

	s := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, rekognition: fakeRekognition{}}
	outJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, job)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, testProviderOpenAI, usage.Provider)
	require.Contains(t, outJSON, `"decision":"`+testDecisionBlock+`"`)
}

func TestRunModerationImageLLMV1_DeterministicFallbackUsesToolUsage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("d", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   deterministicValue,
		InputsJSON: `{"object_key":"moderation/inst/obj","bytes":10,"content_type":"image/png"}`,
	}

	s := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, rekognition: fakeRekognition{}}

	outJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, job)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Equal(t, testProviderAWS, strings.TrimSpace(usage.Provider))
	require.Contains(t, outJSON, `"decision":`)
}

func TestRunModerationImageLLMV1_ToolFailedSetsDeterministicUsage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("d", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   deterministicValue,
		InputsJSON: `{"object_key":"moderation/inst/obj","bytes":10,"content_type":"image/png"}`,
	}

	s := &Server{cfg: config.Config{ArtifactBucketName: ""}, rekognition: fakeRekognition{}}

	outJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, job)
	require.NoError(t, err)
	require.Len(t, errs, 1)
	require.Equal(t, "tool_failed", errs[0].Code)
	require.Equal(t, deterministicValue, strings.TrimSpace(usage.Provider))
	require.Contains(t, outJSON, `"decision":`)
}
