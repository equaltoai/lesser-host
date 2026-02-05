package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai"
)

func TestRenderSummaryBatchOpenAI_AdapterIsCISafe(t *testing.T) {
	itemID := "item-1"
	wantSummary := "Example summary."

	outPayload, err := json.Marshal(renderSummaryBatchOutput{
		Items: []renderSummaryBatchOutputItem{{
			ItemID:       itemID,
			ShortSummary: wantSummary,
			KeyBullets:   []string{"a", "b", "c"},
			Risks:        []ai.RenderSummaryRisk{},
		}},
	})
	if err != nil {
		t.Fatalf("marshal output payload: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		resp := map[string]any{
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
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	old := os.Getenv("OPENAI_BASE_URL")
	t.Cleanup(func() { _ = os.Setenv("OPENAI_BASE_URL", old) })
	_ = os.Setenv("OPENAI_BASE_URL", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, usage, err := RenderSummaryBatchOpenAI(ctx, "sk-test", "openai:gpt-test", []RenderSummaryBatchItem{{
		ItemID: itemID,
		Input:  ai.RenderSummaryInputsV1{NormalizedURL: "https://example.com", Text: "hello"},
	}})
	if err != nil {
		t.Fatalf("RenderSummaryBatchOpenAI error: %v", err)
	}
	if usage.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", usage.Provider)
	}
	got, ok := out[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if got.ShortSummary != wantSummary {
		t.Fatalf("expected short summary %q, got %q", wantSummary, got.ShortSummary)
	}
}

func TestRenderSummaryBatchAnthropic_AdapterIsCISafe(t *testing.T) {
	itemID := "item-1"
	wantSummary := "Example summary."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")

		resp := map[string]any{
			"id":    "msg_test",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-test",
			"content": []any{map[string]any{
				"type": "tool_use",
				"id":   "toolu_1",
				"name": "render_summary_batch",
				"input": map[string]any{
					"items": []any{map[string]any{
						"item_id":       itemID,
						"short_summary": wantSummary,
						"key_bullets":   []any{"a", "b", "c"},
						"risks":         []any{},
					}},
				},
			}},
			"usage": map[string]any{
				"input_tokens":  11,
				"output_tokens": 22,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	old := os.Getenv("ANTHROPIC_BASE_URL")
	t.Cleanup(func() { _ = os.Setenv("ANTHROPIC_BASE_URL", old) })
	_ = os.Setenv("ANTHROPIC_BASE_URL", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, usage, err := RenderSummaryBatchAnthropic(ctx, "sk-ant-test", "anthropic:claude-test", []RenderSummaryBatchItem{{
		ItemID: itemID,
		Input:  ai.RenderSummaryInputsV1{NormalizedURL: "https://example.com", Text: "hello"},
	}})
	if err != nil {
		t.Fatalf("RenderSummaryBatchAnthropic error: %v", err)
	}
	if usage.Provider != "anthropic" {
		t.Fatalf("expected provider anthropic, got %q", usage.Provider)
	}
	got, ok := out[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if got.ShortSummary != wantSummary {
		t.Fatalf("expected short summary %q, got %q", wantSummary, got.ShortSummary)
	}
}
