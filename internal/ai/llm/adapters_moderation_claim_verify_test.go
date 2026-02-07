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

func TestModerationBatchOpenAI_AdapterIsCISafe(t *testing.T) {
	itemID := "item-1"

	outPayload, err := json.Marshal(moderationBatchOutput{
		Items: []moderationBatchOutputItem{{
			ItemID:   itemID,
			Decision: "allow",
			Categories: []ai.ModerationCategoryV1{{
				Code:       "pii",
				Severity:   "high",
				Summary:    "possible pii",
				Confidence: 0.9,
			}},
			Highlights: []string{"email address"},
			Notes:      "ok",
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

	calls := 0
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			calls++
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

	outText, usageText, err := ModerationTextBatchOpenAI(ctx, "sk-test", "openai:gpt-test", []ModerationTextBatchItem{{
		ItemID: itemID,
		Input:  ai.ModerationTextInputsV1{Text: "hello"},
	}})
	if err != nil {
		t.Fatalf("ModerationTextBatchOpenAI error: %v", err)
	}
	if usageText.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", usageText.Provider)
	}
	gotText, ok := outText[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if gotText.Kind != "moderation_text" || gotText.Version != "v1" || gotText.Decision != "allow" {
		t.Fatalf("unexpected moderation text result: %#v", gotText)
	}

	outImage, usageImage, err := ModerationImageBatchOpenAI(ctx, "sk-test", "openai:gpt-test", []ModerationImageBatchItem{{
		ItemID: itemID,
		Input:  ai.ModerationImageInputsV1{ObjectKey: "obj-1"},
	}})
	if err != nil {
		t.Fatalf("ModerationImageBatchOpenAI error: %v", err)
	}
	if usageImage.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", usageImage.Provider)
	}
	gotImage, ok := outImage[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if gotImage.Kind != "moderation_image" || gotImage.Version != "v1" || gotImage.Decision != "allow" {
		t.Fatalf("unexpected moderation image result: %#v", gotImage)
	}
	if calls != 2 {
		t.Fatalf("expected 2 OpenAI calls, got %d", calls)
	}
}

func TestModerationBatchAnthropic_AdapterIsCISafe(t *testing.T) {
	itemID := "item-1"

	respBytes, err := json.Marshal(map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-test",
		"content": []any{map[string]any{
			"type": "tool_use",
			"id":   "toolu_1",
			"name": "moderation_batch",
			"input": map[string]any{
				"items": []any{map[string]any{
					"item_id":   itemID,
					"decision":  "review",
					"notes":     "ok",
					"highlights": []any{},
					"categories": []any{},
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

	calls := 0
	anthropicHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			calls++
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

	out, usage, err := ModerationTextBatchAnthropic(ctx, "sk-ant-test", "anthropic:claude-test", []ModerationTextBatchItem{{
		ItemID: itemID,
		Input:  ai.ModerationTextInputsV1{Text: "hello"},
	}})
	if err != nil {
		t.Fatalf("ModerationTextBatchAnthropic error: %v", err)
	}
	if usage.Provider != "anthropic" {
		t.Fatalf("expected provider anthropic, got %q", usage.Provider)
	}
	got, ok := out[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if got.Kind != "moderation_text" || got.Version != "v1" || got.Decision != "review" {
		t.Fatalf("unexpected moderation result: %#v", got)
	}
	if calls != 1 {
		t.Fatalf("expected 1 anthropic call, got %d", calls)
	}
}

func TestClaimVerifyBatchOpenAI_AdapterIsCISafe(t *testing.T) {
	itemID := "item-1"

	outPayload, err := json.Marshal(claimVerifyBatchOutput{
		Items: []claimVerifyBatchOutputItem{{
			ItemID: itemID,
			Claims: []ai.ClaimVerifyClaimV1{{
				ClaimID:        "c1",
				Text:           "Example claim.",
				Classification: "checkable",
				Verdict:        "supported",
				Confidence:     0.8,
				Reason:         "Evidence supports the claim.",
				Citations: []ai.ClaimVerifyCitationV1{{
					SourceID: "s1",
					Quote:    "Example quote.",
				}},
			}},
			Warnings: []string{},
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

	out, usage, err := ClaimVerifyBatchOpenAI(ctx, "sk-test", "openai:gpt-test", []ClaimVerifyBatchItem{{
		ItemID: itemID,
		Input: ai.ClaimVerifyInputsV1{
			Text:     "hello",
			Claims:   []string{"Example claim."},
			Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "evidence"}},
		},
	}})
	if err != nil {
		t.Fatalf("ClaimVerifyBatchOpenAI error: %v", err)
	}
	if usage.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", usage.Provider)
	}
	got, ok := out[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if got.Kind != "claim_verify" || got.Version != "v1" || len(got.Claims) != 1 {
		t.Fatalf("unexpected claim verify result: %#v", got)
	}
}

func TestClaimVerifyBatchAnthropic_AdapterIsCISafe(t *testing.T) {
	itemID := "item-1"

	respBytes, err := json.Marshal(map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-test",
		"content": []any{map[string]any{
			"type": "tool_use",
			"id":   "toolu_1",
			"name": "claim_verify_batch",
			"input": map[string]any{
				"items": []any{map[string]any{
					"item_id": itemID,
					"claims": []any{map[string]any{
						"claim_id":        "c1",
						"text":            "Example claim.",
						"classification":  "checkable",
						"verdict":         "supported",
						"confidence":      0.9,
						"reason":          "ok",
						"citations":       []any{map[string]any{"source_id": "s1", "quote": "quote"}},
					}},
					"warnings": []any{},
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

	out, usage, err := ClaimVerifyBatchAnthropic(ctx, "sk-ant-test", "anthropic:claude-test", []ClaimVerifyBatchItem{{
		ItemID: itemID,
		Input: ai.ClaimVerifyInputsV1{
			Text:     "hello",
			Claims:   []string{"Example claim."},
			Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "evidence"}},
		},
	}})
	if err != nil {
		t.Fatalf("ClaimVerifyBatchAnthropic error: %v", err)
	}
	if usage.Provider != "anthropic" {
		t.Fatalf("expected provider anthropic, got %q", usage.Provider)
	}
	got, ok := out[itemID]
	if !ok {
		t.Fatalf("expected output for %q", itemID)
	}
	if got.Kind != "claim_verify" || got.Version != "v1" || len(got.Claims) != 1 {
		t.Fatalf("unexpected claim verify result: %#v", got)
	}
}

func TestClaimVerifyWebSearchEvidenceOpenAI_AdapterIsCISafe(t *testing.T) {
	outPayload, err := json.Marshal(claimVerifyWebSearchOutput{
		Sources: []claimVerifyWebSearchSource{
			{URL: "https://example.com/1", Title: "One", Text: "Excerpt 1"},
			{URL: "https://example.com/2", Title: "Two", Text: "Excerpt 2"},
		},
		Disclaimer: "",
	})
	if err != nil {
		t.Fatalf("marshal output payload: %v", err)
	}

	respBytes, err := json.Marshal(map[string]any{
		"id":                "resp_test",
		"object":            "response",
		"created_at":        123,
		"error":             map[string]any{"code": "", "message": ""},
		"incomplete_details": map[string]any{},
		"instructions":      "",
		"metadata":          map[string]any{},
		"model":             "gpt-test",
		"output": []any{map[string]any{
			"id":     "msg_test",
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        string(outPayload),
				"annotations": []any{},
			}},
		}},
		"parallel_tool_calls": false,
		"temperature":         0,
		"tool_choice":         "required",
		"tools":               []any{},
		"top_p":               1,
		"usage": map[string]any{
			"input_tokens":          11,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens":         22,
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
			"total_tokens":          33,
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

	evidence, disclaimer, usage, err := ClaimVerifyWebSearchEvidenceOpenAI(ctx, "sk-test", "openai:gpt-test", []string{"c1"}, "text", 1, "INVALID")
	if err != nil {
		t.Fatalf("ClaimVerifyWebSearchEvidenceOpenAI error: %v", err)
	}
	if usage.Provider != "openai" {
		t.Fatalf("expected provider openai, got %q", usage.Provider)
	}
	if len(evidence) != 1 || evidence[0].SourceID != "web_1" || evidence[0].URL != "https://example.com/1" {
		t.Fatalf("unexpected evidence: %#v", evidence)
	}
	if disclaimer == "" {
		t.Fatalf("expected default disclaimer")
	}
}

func TestNormalizeRenderSummaryRisk_DefaultsSeverity(t *testing.T) {
	t.Parallel()

	got := normalizeRenderSummaryRisk(ai.RenderSummaryRisk{
		Code:     " x ",
		Severity: "NOPE",
		Summary:  " y ",
	})
	if got.Code != "x" || got.Summary != "y" || got.Severity != renderSummarySeverityMedium {
		t.Fatalf("unexpected normalized risk: %#v", got)
	}
}

