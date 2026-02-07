package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRenderSummaryBatchOpenAI_AdapterIsCISafe(t *testing.T) {
	itemID := testItemID
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

	respBytes, err := json.Marshal(map[string]any{
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
	if err != nil {
		t.Fatalf("marshal openai response: %v", err)
	}

	old := os.Getenv("OPENAI_BASE_URL")
	t.Cleanup(func() { _ = os.Setenv("OPENAI_BASE_URL", old) })
	_ = os.Setenv("OPENAI_BASE_URL", "https://openai.example.test")

	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(respBytes)),
				Request:    r,
			}, nil
		}),
	}
	t.Cleanup(func() { openAIHTTPClient = nil })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, usage, err := RenderSummaryBatchOpenAI(ctx, "sk-test", "openai:gpt-test", []RenderSummaryBatchItem{{
		ItemID: itemID,
		Input:  ai.RenderSummaryInputsV1{NormalizedURL: "https://example.com", Text: "hello"},
	}})
	if err != nil {
		t.Fatalf("RenderSummaryBatchOpenAI error: %v", err)
	}
	if usage.Provider != testProviderOpenAI {
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
	itemID := testItemID
	wantSummary := "Example summary."

	respBytes, err := json.Marshal(map[string]any{
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
	})
	if err != nil {
		t.Fatalf("marshal anthropic response: %v", err)
	}

	old := os.Getenv("ANTHROPIC_BASE_URL")
	t.Cleanup(func() { _ = os.Setenv("ANTHROPIC_BASE_URL", old) })
	_ = os.Setenv("ANTHROPIC_BASE_URL", "https://anthropic.example.test")

	anthropicHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(respBytes)),
				Request:    r,
			}, nil
		}),
	}
	t.Cleanup(func() { anthropicHTTPClient = nil })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, usage, err := RenderSummaryBatchAnthropic(ctx, "sk-ant-test", "anthropic:claude-test", []RenderSummaryBatchItem{{
		ItemID: itemID,
		Input:  ai.RenderSummaryInputsV1{NormalizedURL: "https://example.com", Text: "hello"},
	}})
	if err != nil {
		t.Fatalf("RenderSummaryBatchAnthropic error: %v", err)
	}
	if usage.Provider != testProviderAnthropic {
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
