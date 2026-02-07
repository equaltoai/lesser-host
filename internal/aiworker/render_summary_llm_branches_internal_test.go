package aiworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func openAIChatCompletionResponseJSON(t *testing.T, model string, content string) []byte {
	t.Helper()

	respBytes, err := json.Marshal(map[string]any{
		"id":      "chatcmpl_test",
		"object":  "chat.completion",
		"created": 123,
		"model":   model,
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
		}},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 20,
			"total_tokens":      30,
		},
	})
	if err != nil {
		t.Fatalf("marshal openai response: %v", err)
	}
	return respBytes
}

func anthropicToolUseResponseJSON(t *testing.T, model string, toolName string, input any) []byte {
	t.Helper()

	respBytes, err := json.Marshal(map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": model,
		"content": []any{map[string]any{
			"type":  "tool_use",
			"id":    "toolu_1",
			"name":  toolName,
			"input": input,
		}},
		"usage": map[string]any{
			"input_tokens":  11,
			"output_tokens": 22,
		},
	})
	if err != nil {
		t.Fatalf("marshal anthropic response: %v", err)
	}
	return respBytes
}

func TestServerRegister_RegistersSQSWhenConfigured(t *testing.T) {
	app := apptheory.New()
	srv := &Server{cfg: config.Config{SafetyQueueURL: "https://sqs.us-east-1.amazonaws.com/123456789012/lesser-host-safety"}}
	srv.Register(app)
}

func TestRenderSummaryBatchResults_OpenAIAnthropicAndFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	itemID := strings.Repeat("a", 64)
	items := []llm.RenderSummaryBatchItem{{
		ItemID: itemID,
		Input:  ai.RenderSummaryInputsV1{NormalizedURL: "https://example.com", Text: "hello"},
	}}

	t.Run("openai_success", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id":       itemID,
				"short_summary": "Example summary.",
				"key_bullets":   []any{"a", "b", "c"},
				"risks":         []any{},
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

		s := &Server{}
		res, usage, errs := s.renderSummaryBatchResults(ctx, "openai:gpt-test", items)
		if len(errs) != 0 {
			t.Fatalf("expected no errs, got %#v", errs)
		}
		if usage.Provider != "openai" {
			t.Fatalf("expected openai usage, got %#v", usage)
		}
		if got := strings.TrimSpace(res[itemID].ShortSummary); got != "Example summary." {
			t.Fatalf("expected summary from llm, got %q", got)
		}
	})

	t.Run("openai_error_fallback", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("OPENAI_BASE_URL", ts.URL)

		s := &Server{}
		res, usage, errs := s.renderSummaryBatchResults(ctx, "openai:gpt-test", items)
		if len(errs) != 1 || errs[0].Code != "llm_failed" {
			t.Fatalf("expected llm_failed, got %#v", errs)
		}
		if usage.Provider != deterministicValue {
			t.Fatalf("expected deterministic usage, got %#v", usage)
		}
		if strings.TrimSpace(res[itemID].ShortSummary) == "" {
			t.Fatalf("expected deterministic summary, got %#v", res[itemID])
		}
	})

	t.Run("anthropic_success", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "a")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")

			outPayload := map[string]any{
				"items": []any{map[string]any{
					"item_id":       itemID,
					"short_summary": "Example summary.",
					"key_bullets":   []any{"a", "b", "c"},
					"risks":         []any{},
				}},
			}
			_, _ = w.Write(anthropicToolUseResponseJSON(t, "claude-test", "render_summary_batch", outPayload))
		}))
		t.Cleanup(ts.Close)
		t.Setenv("ANTHROPIC_BASE_URL", ts.URL)

		s := &Server{}
		res, usage, errs := s.renderSummaryBatchResults(ctx, "anthropic:claude-test", items)
		if len(errs) != 0 {
			t.Fatalf("expected no errs, got %#v", errs)
		}
		if usage.Provider != "anthropic" {
			t.Fatalf("expected anthropic usage, got %#v", usage)
		}
		if got := strings.TrimSpace(res[itemID].ShortSummary); got != "Example summary." {
			t.Fatalf("expected summary from llm, got %q", got)
		}
	})

	t.Run("non_llm_deterministic", func(t *testing.T) {
		s := &Server{}
		res, usage, errs := s.renderSummaryBatchResults(ctx, "deterministic", items)
		if len(errs) != 0 {
			t.Fatalf("expected no errs, got %#v", errs)
		}
		if usage.Provider != deterministicValue {
			t.Fatalf("expected deterministic usage, got %#v", usage)
		}
		if strings.TrimSpace(res[itemID].ShortSummary) == "" {
			t.Fatalf("expected deterministic summary, got %#v", res[itemID])
		}
	})
}

func TestRunRenderSummaryLLMV1_LLMMissingOutputAndSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	jobID := strings.Repeat("b", 64)
	job := &models.AIJob{
		ID:         jobID,
		InstanceSlug: "inst",
		Module:      "render_summary_llm",
		PolicyVersion: "v1",
		ModelSet:    "openai:gpt-test",
		InputsJSON:  `{"normalized_url":"https://example.com/","text":"hello"}`,
	}

	t.Run("missing_output", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id":       "other",
				"short_summary": "",
				"key_bullets":   []any{},
				"risks":         []any{},
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

		s := &Server{}
		_, usage, errs, err := s.runRenderSummaryLLMV1(ctx, job)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(errs) != 1 || errs[0].Code != "llm_missing_output" {
			t.Fatalf("expected llm_missing_output, got %#v", errs)
		}
		if usage.Provider != deterministicValue {
			t.Fatalf("expected deterministic usage, got %#v", usage)
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id":       jobID,
				"short_summary": "Example summary.",
				"key_bullets":   []any{"a", "b", "c"},
				"risks":         []any{},
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

		s := &Server{}
		outJSON, usage, errs, err := s.runRenderSummaryLLMV1(ctx, job)
		if err != nil || len(errs) != 0 {
			t.Fatalf("out=%q usage=%#v errs=%#v err=%v", outJSON, usage, errs, err)
		}
		if usage.Provider != "openai" {
			t.Fatalf("expected openai usage, got %#v", usage)
		}
		if !strings.Contains(outJSON, "Example summary.") {
			t.Fatalf("expected summary in output, got %q", outJSON)
		}
	})
}
