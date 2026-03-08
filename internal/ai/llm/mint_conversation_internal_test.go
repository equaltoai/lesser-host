package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestMintConversationDeclarationsDraftParsingAndNormalization(t *testing.T) {
	t.Parallel()

	if _, err := parseMintConversationDeclarationsDraft("{"); err == nil {
		t.Fatalf("expected invalid json error")
	}

	raw := `{
		"selfDescription": {
			"purpose": "  Help people plan travel  ",
			"constraints": "  no booking  ",
			"commitments": "  explain uncertainty  ",
			"limitations": "  no legal advice  ",
			"authoredBy": " AGENT ",
			"mintingModel": "  openai:gpt-4o-mini  "
		},
		"capabilities": [
			{"capability":" itinerary-planning ","scope":" build routes ","claimLevel":" ","lastValidated":" 2026-03-05T00:00:00Z ","validationRef":" ref ","degradesTo":" email "},
			{"capability":" ","scope":"skip","claimLevel":"self-declared"},
			{"capability":"skip","scope":" ","claimLevel":"self-declared"}
		],
		"boundaries": [
			{"category":" REFUSAL ","statement":" I will not impersonate people. ","rationale":" safety "},
			{"category":" ","statement":"skip"},
			{"category":"scope_limit","statement":" "}
		]
	}`

	parsed, err := parseMintConversationDeclarationsDraft(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	norm := normalizeMintConversationDeclarationsDraft(parsed)
	if norm.SelfDescription.Purpose != "Help people plan travel" {
		t.Fatalf("unexpected purpose: %q", norm.SelfDescription.Purpose)
	}
	if norm.SelfDescription.AuthoredBy != "agent" {
		t.Fatalf("unexpected authoredBy: %q", norm.SelfDescription.AuthoredBy)
	}
	if len(norm.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(norm.Capabilities))
	}
	if norm.Capabilities[0].ClaimLevel != "self-declared" {
		t.Fatalf("expected default claimLevel, got %q", norm.Capabilities[0].ClaimLevel)
	}
	if len(norm.Boundaries) != 1 {
		t.Fatalf("expected 1 boundary, got %d", len(norm.Boundaries))
	}
	if norm.Boundaries[0].Category != "refusal" {
		t.Fatalf("unexpected boundary category: %q", norm.Boundaries[0].Category)
	}
	if norm.Transparency == nil {
		t.Fatalf("expected default transparency map")
	}

	withManyBoundaries := MintConversationDeclarationsDraft{
		Boundaries: []MintConversationBoundaryDraft{
			{Category: "refusal", Statement: "1"},
			{Category: "refusal", Statement: "2"},
			{Category: "refusal", Statement: "3"},
			{Category: "refusal", Statement: "4"},
			{Category: "refusal", Statement: "5"},
		},
	}
	capped := normalizeMintConversationDeclarationsDraft(withManyBoundaries)
	if len(capped.Boundaries) != maxMintConversationBoundaryDrafts {
		t.Fatalf("expected %d capped boundaries, got %d", maxMintConversationBoundaryDrafts, len(capped.Boundaries))
	}
}

func TestMintConversationDeclarationsPromptAndSchema(t *testing.T) {
	t.Parallel()

	prompt := mintConversationDeclarationsSystemPromptV1()
	if !strings.Contains(prompt, "single JSON object") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "2-4 boundaries") {
		t.Fatalf("expected prompt to mention boundary target, got %q", prompt)
	}

	schema := mintConversationDeclarationsJSONSchemaV1()
	props, hasProps := schema["properties"].(map[string]any)
	if !hasProps {
		t.Fatalf("expected top-level properties")
	}
	if _, exists := props["selfDescription"]; !exists {
		t.Fatalf("expected selfDescription schema")
	}
	if _, exists := props["capabilities"]; !exists {
		t.Fatalf("expected capabilities schema")
	}
	if _, exists := props["boundaries"]; !exists {
		t.Fatalf("expected boundaries schema")
	}
	if _, exists := props["transparency"]; !exists {
		t.Fatalf("expected transparency schema")
	}
	boundaries, ok := props["boundaries"].(map[string]any)
	if !ok {
		t.Fatalf("expected boundaries schema map")
	}
	if got, ok := boundaries["maxItems"].(int); !ok || got != maxMintConversationBoundaryDrafts {
		t.Fatalf("expected boundaries maxItems=%d, got %#v", maxMintConversationBoundaryDrafts, boundaries["maxItems"])
	}

	if _, _, err := MintConversationDeclarationsOpenAI(t.Context(), "k", "unsupported:model", MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported OpenAI declarations model error")
	}
	if _, _, err := MintConversationDeclarationsAnthropic(t.Context(), "k", "unsupported:model", MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported Anthropic declarations model error")
	}
}

func TestAnthropicToolInputSchemaSanitizesPermissiveAdditionalProperties(t *testing.T) {
	t.Parallel()

	schema := mintConversationDeclarationsJSONSchemaV1()
	param := anthropicToolInputSchemaFromJSONSchema(schema)

	raw, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if strings.Contains(string(raw), `"additionalProperties":true`) {
		t.Fatalf("expected anthropic schema to strip additionalProperties=true, got %s", raw)
	}
	if !strings.Contains(string(raw), `"additionalProperties":false`) {
		t.Fatalf("expected anthropic schema to preserve additionalProperties=false, got %s", raw)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}

	props := requireSchemaMap(t, decoded, "properties")
	transparency := requireSchemaMap(t, props, "transparency")
	assertSchemaBool(t, transparency, "additionalProperties", false)

	capabilities := requireSchemaMap(t, props, "capabilities")
	items := requireSchemaMap(t, capabilities, "items")
	capProps := requireSchemaMap(t, items, "properties")
	constraints := requireSchemaMap(t, capProps, "constraints")
	assertSchemaBool(t, constraints, "additionalProperties", false)

	selfDescription := requireSchemaMap(t, props, "selfDescription")
	assertSchemaBool(t, selfDescription, "additionalProperties", false)
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

	model, err := anthropicModelFromSet("anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("model parse: %v", err)
	}
	if model != anthropic.Model("claude-sonnet-4-6") {
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
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-sonnet-4-6",
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
		"anthropic:claude-sonnet-4-6",
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
	if usage.Provider != "anthropic" || usage.Model != "claude-sonnet-4-6" {
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
		"anthropic:claude-sonnet-4-6",
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
	err := json.Unmarshal([]byte(`{
		"content": [
			{"type": "text", "text": "Hello "},
			{"type": "text", "text": "world"},
			{"type": "tool_use", "id": "tool_1", "name": "ignored", "input": {}}
		],
		"model": "claude-sonnet-4-6",
		"usage": {
			"input_tokens": 11,
			"cache_creation_input_tokens": 2,
			"cache_read_input_tokens": 3,
			"output_tokens": 7
		}
	}`), &msg)
	if err != nil {
		t.Fatalf("unmarshal anthropic message: %v", err)
	}
	return msg
}

func assertAnthropicUsage(t *testing.T, usage models.AIUsage) {
	t.Helper()

	if usage.Provider != "anthropic" || usage.Model != "claude-sonnet-4-6" {
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

func assertSchemaBool(t *testing.T, src map[string]any, key string, want bool) {
	t.Helper()
	got, ok := src[key].(bool)
	if !ok || got != want {
		t.Fatalf("expected %s=%t, got %#v", key, want, src[key])
	}
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
