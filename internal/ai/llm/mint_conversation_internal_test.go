package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	mintConversationTestAnthropicModel  = "claude-sonnet-4-6"
	mintConversationTestAuthoredByAgent = "agent"
)

func TestOpenAIModelSupportsTemperature(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"gpt-5-mini-2025-08-07", "gpt-5", "o1", "o3-mini", "o4-mini"} {
		if openAIModelSupportsTemperature(model) {
			t.Fatalf("expected %q to omit temperature", model)
		}
	}
	for _, model := range []string{"gpt-4o-mini", "gpt-4.1-mini"} {
		if !openAIModelSupportsTemperature(model) {
			t.Fatalf("expected %q to support temperature", model)
		}
	}
}

func TestSanitizeAnthropicToolSchemaMapKeepsConstraintLikePropertyNames(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"maxItems":  map[string]any{"type": "integer", "minimum": 0, "maximum": 10},
			"maxLength": map[string]any{"type": "string", "minLength": 1, "maxLength": 4},
			"tags": map[string]any{
				"type":     "array",
				"minItems": 1,
				"maxItems": 3,
				"items":    map[string]any{"type": "string", "maxLength": 12},
			},
		},
		"required": []string{"maxItems", "maxLength", "tags"},
	}

	out := sanitizeAnthropicToolSchemaMap(schema)
	props := requireSchemaMap(t, out, "properties")

	maxItemsProp := requireSchemaMap(t, props, "maxItems")
	for _, stripped := range []string{"minimum", "maximum"} {
		if _, exists := maxItemsProp[stripped]; exists {
			t.Fatalf("expected integer constraint %q stripped, got %#v", stripped, maxItemsProp)
		}
	}

	maxLengthProp := requireSchemaMap(t, props, "maxLength")
	for _, stripped := range []string{"minLength", "maxLength"} {
		if _, exists := maxLengthProp[stripped]; exists {
			t.Fatalf("expected string constraint %q stripped, got %#v", stripped, maxLengthProp)
		}
	}

	tags := requireSchemaMap(t, props, "tags")
	for _, stripped := range []string{"minItems", "maxItems"} {
		if _, exists := tags[stripped]; exists {
			t.Fatalf("expected array constraint %q stripped, got %#v", stripped, tags)
		}
	}
	items := requireSchemaMap(t, tags, "items")
	if _, exists := items["maxLength"]; exists {
		t.Fatalf("expected nested string constraint stripped, got %#v", items)
	}

	required, ok := out["required"].([]string)
	if !ok || len(required) != 3 {
		t.Fatalf("expected constraint-like property names preserved in required, got %#v", out["required"])
	}
}

func TestExtractJSONObjectFromText(t *testing.T) {
	t.Parallel()

	raw := "Here is the JSON you asked for:\n```json\n{\"ok\":true,\"nested\":{\"value\":1}}\n```\n"
	if got := extractJSONObjectFromText(raw); got != "{\"ok\":true,\"nested\":{\"value\":1}}" {
		t.Fatalf("unexpected extracted json: %q", got)
	}

	plain := "{\"direct\":true}"
	if got := extractJSONObjectFromText(plain); got != plain {
		t.Fatalf("unexpected plain json extraction: %q", got)
	}
}

func TestAnthropicHelpers_ModelTextAndUsage(t *testing.T) {
	t.Parallel()

	model, err := anthropicModelFromSet("anthropic:" + mintConversationTestAnthropicModel)
	if err != nil {
		t.Fatalf("model parse: %v", err)
	}
	if model != anthropic.Model(mintConversationTestAnthropicModel) {
		t.Fatalf("unexpected model: %q", model)
	}
	if _, unsupportedErr := anthropicModelFromSet("openai:gpt-5.4"); unsupportedErr == nil {
		t.Fatalf("expected unsupported model set error")
	}

	msg := requireAnthropicTestMessage(t)

	text, err := anthropicTextOutput(&msg)
	if err != nil {
		t.Fatalf("text output: %v", err)
	}
	if text != "Hello world" {
		t.Fatalf("unexpected text output: %q", text)
	}

	assertAnthropicUsage(t, anthropicUsageFromMessage(&msg, time.Now().Add(-25*time.Millisecond)))

	if _, emptyErr := anthropicTextOutput(&anthropic.Message{}); emptyErr == nil {
		t.Fatalf("expected empty response error")
	}
	if _, nilErr := anthropicTextOutput(nil); nilErr == nil {
		t.Fatalf("expected nil message error")
	}
}

func TestAnthropicJSONTextBatch_AdapterIsCISafe(t *testing.T) {
	type anthropicPrompt struct {
		Topic string `json:"topic"`
	}
	type anthropicParsed struct {
		Answer string `json:"answer"`
	}

	respBytes, err := json.Marshal(map[string]any{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"model":       mintConversationTestAnthropicModel,
		"stop_reason": "end_turn",
		"content": []any{map[string]any{
			"type": "text",
			"text": "Here is the result:\n```json\n{\"answer\":\"ready\"}\n```",
		}},
		"usage": map[string]any{
			"input_tokens":                11,
			"cache_creation_input_tokens": 2,
			"cache_read_input_tokens":     3,
			"output_tokens":               7,
		},
	})
	if err != nil {
		t.Fatalf("marshal anthropic response: %v", err)
	}

	old := os.Getenv("ANTHROPIC_BASE_URL")
	t.Cleanup(func() { _ = os.Setenv("ANTHROPIC_BASE_URL", old) })
	_ = os.Setenv("ANTHROPIC_BASE_URL", "https://anthropic.example.test")

	var requestBody string
	anthropicHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				return nil, readErr
			}
			requestBody = string(body)
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

	out, usage, err := anthropicJSONTextBatch(
		ctx,
		"sk-ant-test",
		"anthropic:"+mintConversationTestAnthropicModel,
		anthropicPrompt{Topic: "soul"},
		anthropicJSONTextBatchConfig{
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"answer": map[string]any{"type": "string"},
				},
				"required": []string{"answer"},
			},
			SystemPrompt: "Return only JSON.",
		},
		func(raw string) (anthropicParsed, error) {
			var parsed anthropicParsed
			return parsed, json.Unmarshal([]byte(raw), &parsed)
		},
		func(parsed anthropicParsed) anthropicParsed { return parsed },
	)
	if err != nil {
		t.Fatalf("anthropic json text batch: %v", err)
	}
	if out.Answer != "ready" {
		t.Fatalf("unexpected parsed answer: %#v", out)
	}
	if usage.Provider != testProviderAnthropic || usage.Model != mintConversationTestAnthropicModel {
		t.Fatalf("unexpected usage identity: %#v", usage)
	}
	if usage.InputTokens != 16 || usage.OutputTokens != 7 || usage.TotalTokens != 23 || usage.ToolCalls != 1 {
		t.Fatalf("unexpected usage metadata: %#v", usage)
	}
	if !strings.Contains(requestBody, "Return only valid JSON matching this schema exactly:") {
		t.Fatalf("expected schema prompt in request body, got %s", requestBody)
	}
	if !strings.Contains(requestBody, `\"topic\":\"soul\"`) {
		t.Fatalf("expected marshaled prompt in request body, got %s", requestBody)
	}
}

func TestAnthropicJSONTextBatch_InvalidSchemaFailsBeforeRequest(t *testing.T) {
	type anthropicParsed struct {
		Answer string `json:"answer"`
	}

	_, _, err := anthropicJSONTextBatch(
		t.Context(),
		"sk-ant-test",
		"anthropic:"+mintConversationTestAnthropicModel,
		map[string]any{"topic": "soul"},
		anthropicJSONTextBatchConfig{
			Schema: map[string]any{"bad": make(chan int)},
		},
		func(raw string) (anthropicParsed, error) {
			var parsed anthropicParsed
			return parsed, json.Unmarshal([]byte(raw), &parsed)
		},
		func(parsed anthropicParsed) anthropicParsed { return parsed },
	)
	if err == nil {
		t.Fatalf("expected schema marshal error")
	}
}

func requireAnthropicTestMessage(t *testing.T) anthropic.Message {
	t.Helper()

	var msg anthropic.Message
	err := json.Unmarshal([]byte(fmt.Sprintf(`{
		"content": [
			{"type": "text", "text": "Hello "},
			{"type": "text", "text": "world"},
			{"type": "tool_use", "id": "tool_1", "name": "ignored", "input": {}}
		],
		"model": %q,
		"usage": {
			"input_tokens": 11,
			"cache_creation_input_tokens": 2,
			"cache_read_input_tokens": 3,
			"output_tokens": 7
		}
	}`, mintConversationTestAnthropicModel)), &msg)
	if err != nil {
		t.Fatalf("unmarshal anthropic message: %v", err)
	}
	return msg
}

func assertAnthropicUsage(t *testing.T, usage models.AIUsage) {
	t.Helper()

	if usage.Provider != testProviderAnthropic || usage.Model != mintConversationTestAnthropicModel {
		t.Fatalf("unexpected usage identity: %#v", usage)
	}
	if usage.InputTokens != 16 || usage.OutputTokens != 7 || usage.TotalTokens != 23 {
		t.Fatalf("unexpected usage counts: %#v", usage)
	}
	if usage.ToolCalls != 1 || usage.DurationMs <= 0 {
		t.Fatalf("unexpected usage metadata: %#v", usage)
	}
}

func requireSchemaMap(t *testing.T, src map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := src[key].(map[string]any)
	if !ok {
		t.Fatalf("expected %s schema", key)
	}
	return value
}

func TestMintConversationStreamingHelpers(t *testing.T) {
	t.Parallel()

	openAIMessages := buildOpenAIConversationMessages("  system prompt  ", []MintConversationMessage{
		{Role: " user ", Content: " hello "},
		{Role: "assistant", Content: " world "},
		{Role: "ignored", Content: "skip"},
		{Role: "user", Content: "   "},
	})
	if len(openAIMessages) != 3 {
		t.Fatalf("expected 3 OpenAI messages, got %d", len(openAIMessages))
	}

	anthropicMessages := buildAnthropicConversationMessages([]MintConversationMessage{
		{Role: " user ", Content: " hello "},
		{Role: "assistant", Content: " world "},
		{Role: "ignored", Content: "skip"},
		{Role: "user", Content: "   "},
	})
	if len(anthropicMessages) != 2 {
		t.Fatalf("expected 2 Anthropic messages, got %d", len(anthropicMessages))
	}

	if _, _, err := StreamMintConversationOpenAI(t.Context(), "k", "unsupported:model", "system", nil, nil); err == nil {
		t.Fatalf("expected unsupported OpenAI model error")
	}
	if _, _, err := StreamMintConversationAnthropic(t.Context(), "k", "unsupported:model", "system", nil, nil); err == nil {
		t.Fatalf("expected unsupported Anthropic model error")
	}
}

func TestOpenAIMintConversationStreamParamsCapsOutputTokens(t *testing.T) {
	t.Parallel()

	params := newOpenAIMintConversationStreamParams("gpt-test", "system", []MintConversationMessage{
		{Role: "user", Content: "hello"},
	})
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if !strings.Contains(string(body), `"max_completion_tokens":4096`) {
		t.Fatalf("expected max_completion_tokens cap in request params, got %s", string(body))
	}
	if !strings.Contains(string(body), `"stream_options"`) || !strings.Contains(string(body), `"include_usage":true`) {
		t.Fatalf("expected streaming usage options in request params, got %s", string(body))
	}
}
