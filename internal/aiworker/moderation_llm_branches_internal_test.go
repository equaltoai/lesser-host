package aiworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestRunModerationTextLLMV1_OpenAIAnthropicAndFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("c", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   "openai:gpt-test",
		InputsJSON: `{"text":"Alice is 30 years old."}`,
	}

	s := &Server{comprehend: fakeComprehend{}}

	t.Run("openai_success", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id":     jobID,
				"decision":    "allow",
				"categories":  []any{},
				"highlights":  []any{},
				"notes":       "",
			}},
		})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(openAIChatCompletionResponseJSON(t, "gpt-test", string(outPayload)))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("OPENAI_BASE_URL", ts.URL)

		outJSON, usage, errs, err := s.runModerationTextLLMV1(ctx, job)
		if err != nil || len(errs) != 0 {
			t.Fatalf("out=%q usage=%#v errs=%#v err=%v", outJSON, usage, errs, err)
		}
		if usage.Provider != "openai" {
			t.Fatalf("expected openai usage, got %#v", usage)
		}
		if !strings.Contains(outJSON, `"decision":"allow"`) {
			t.Fatalf("expected allow decision, got %q", outJSON)
		}
	})

	t.Run("anthropic_success", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "a")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")

			outPayload := map[string]any{
				"items": []any{map[string]any{
					"item_id":     jobID,
					"decision":    "review",
					"categories":  []any{},
					"highlights":  []any{},
					"notes":       "ok",
				}},
			}
			_, _ = w.Write(anthropicToolUseResponseJSON(t, "claude-test", "moderation_batch", outPayload))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("ANTHROPIC_BASE_URL", ts.URL)

		job2 := *job
		job2.ModelSet = "anthropic:claude-test"

		outJSON, usage, errs, err := s.runModerationTextLLMV1(ctx, &job2)
		if err != nil || len(errs) != 0 {
			t.Fatalf("out=%q usage=%#v errs=%#v err=%v", outJSON, usage, errs, err)
		}
		if usage.Provider != "anthropic" {
			t.Fatalf("expected anthropic usage, got %#v", usage)
		}
		if !strings.Contains(outJSON, `"decision":"review"`) {
			t.Fatalf("expected review decision, got %q", outJSON)
		}
	})

	t.Run("missing_output_fallback", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id":     "other",
				"decision":    "allow",
				"categories":  []any{},
				"highlights":  []any{},
				"notes":       "",
			}},
		})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(openAIChatCompletionResponseJSON(t, "gpt-test", string(outPayload)))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("OPENAI_BASE_URL", ts.URL)

		outJSON, _, errs, err := s.runModerationTextLLMV1(ctx, job)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(errs) != 1 || errs[0].Code != "llm_missing_output" {
			t.Fatalf("expected llm_missing_output, got %#v", errs)
		}
		if !strings.Contains(outJSON, `"decision":`) {
			t.Fatalf("expected decision in fallback output, got %q", outJSON)
		}
	})
}

func TestRunModerationImageLLMV1_OpenAIAndToolFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("d", 64)
	job := &models.AIJob{
		ID:         jobID,
		ModelSet:   "openai:gpt-test",
		InputsJSON: `{"object_key":"moderation/inst/obj","bytes":10,"content_type":"image/png"}`,
	}

	t.Run("openai_success", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id":     jobID,
				"decision":    "block",
				"categories":  []any{},
				"highlights":  []any{},
				"notes":       "",
			}},
		})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(openAIChatCompletionResponseJSON(t, "gpt-test", string(outPayload)))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("OPENAI_BASE_URL", ts.URL)

		s := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, rekognition: fakeRekognition{}}
		outJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, job)
		if err != nil || len(errs) != 0 {
			t.Fatalf("out=%q usage=%#v errs=%#v err=%v", outJSON, usage, errs, err)
		}
		if usage.Provider != "openai" {
			t.Fatalf("expected openai usage, got %#v", usage)
		}
		if !strings.Contains(outJSON, `"decision":"block"`) {
			t.Fatalf("expected block decision, got %q", outJSON)
		}
	})

	t.Run("deterministic_fallback_uses_tool_usage", func(t *testing.T) {
		s := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, rekognition: fakeRekognition{}}

		job2 := *job
		job2.ModelSet = deterministicValue
		outJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, &job2)
		if err != nil || len(errs) != 0 {
			t.Fatalf("out=%q usage=%#v errs=%#v err=%v", outJSON, usage, errs, err)
		}
		if strings.TrimSpace(usage.Provider) != "aws" {
			t.Fatalf("expected aws tool usage, got %#v", usage)
		}
		if !strings.Contains(outJSON, `"decision":`) {
			t.Fatalf("expected decision in output, got %q", outJSON)
		}
	})

	t.Run("tool_failed_sets_deterministic_usage", func(t *testing.T) {
		s := &Server{cfg: config.Config{ArtifactBucketName: ""}, rekognition: fakeRekognition{}}

		job2 := *job
		job2.ModelSet = deterministicValue
		outJSON, usage, errs, err := s.runModerationImageLLMV1(ctx, &job2)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(errs) != 1 || errs[0].Code != "tool_failed" {
			t.Fatalf("expected tool_failed, got %#v", errs)
		}
		if strings.TrimSpace(usage.Provider) != deterministicValue {
			t.Fatalf("expected deterministic usage, got %#v", usage)
		}
		if !strings.Contains(outJSON, `"decision":`) {
			t.Fatalf("expected decision in output, got %q", outJSON)
		}
	})
}

