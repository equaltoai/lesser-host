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
	assertOpenAIStrictObjectSchema(t, schema)

	if _, _, err := MintConversationDeclarationsOpenAI(t.Context(), "k", "unsupported:model", MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported OpenAI declarations model error")
	}
	if _, _, err := MintConversationDeclarationsAnthropic(t.Context(), "k", "unsupported:model", MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported Anthropic declarations model error")
	}
}

func TestMintConversationDeclarationsOpenAI_OmitsTemperatureForGPT5(t *testing.T) {
	outPayload := `{
		"selfDescription":{"purpose":"Confirm hosted genesis","constraints":"stay scoped","commitments":"be concise","limitations":"test only","authoredBy":"agent","mintingModel":"openai:gpt-5-mini-2025-08-07"},
		"capabilities":[{"capability":"hosted_genesis_confirmation","scope":"confirm API MicroVM turn","claimLevel":"self-declared","lastValidated":"","validationRef":"","degradesTo":""}],
		"boundaries":[{"category":"scope_limit","statement":"Only confirm the API proof.","rationale":"test scope"}],
		"transparency":{"modelProviderUncertainty":"unit-test","operationalNotes":"fake response"}
	}`
	respBytes, err := json.Marshal(map[string]any{
		"id":      "chatcmpl_test",
		"object":  "chat.completion",
		"created": 123,
		"model":   "gpt-5-mini-2025-08-07",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": outPayload,
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

	var requestBody map[string]any
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if decodeErr := json.NewDecoder(r.Body).Decode(&requestBody); decodeErr != nil {
				t.Fatalf("decode openai request: %v", decodeErr)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(respBytes)),
				Request:    r,
			}, nil
		}),
	}
	t.Cleanup(func() { openAIHTTPClient = nil })

	_, _, err = MintConversationDeclarationsOpenAI(t.Context(), "sk-test", "openai:gpt-5-mini-2025-08-07", MintConversationDeclarationsInput{
		Registration: MintConversationRegistrationContext{AgentID: "agent_123"},
		Messages: []MintConversationMessage{
			{Role: "user", Content: "confirm"},
			{Role: "assistant", Content: "confirmed"},
		},
	})
	if err != nil {
		t.Fatalf("MintConversationDeclarationsOpenAI: %v", err)
	}
	if _, exists := requestBody["temperature"]; exists {
		t.Fatalf("gpt-5-family requests must omit unsupported temperature, got body %#v", requestBody)
	}
}

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
	if _, exists := capProps["constraints"]; exists {
		t.Fatalf("strict mint declarations capability schema must not expose free-form constraints")
	}

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

func assertOpenAIStrictObjectSchema(t *testing.T, schema map[string]any) {
	t.Helper()
	assertOpenAIStrictObjectSchemaAt(t, "root", schema)
}

func assertOpenAIStrictObjectSchemaAt(t *testing.T, path string, schema map[string]any) {
	t.Helper()
	if schema == nil {
		return
	}
	if schemaType(schema) == "object" {
		assertOpenAIStrictObjectRequired(t, path, schema)
	}
	for key, value := range schema {
		assertOpenAIStrictSchemaValue(t, path, key, value)
	}
}

func assertOpenAIStrictObjectRequired(t *testing.T, path string, schema map[string]any) {
	t.Helper()
	assertSchemaBool(t, schema, "additionalProperties", false)
	props, _ := schema["properties"].(map[string]any)
	required, ok := schemaRequiredSet(schema)
	if !ok {
		t.Fatalf("%s: object schema missing required list", path)
	}
	for name := range props {
		if !required[name] {
			t.Fatalf("%s: strict object property %q missing from required", path, name)
		}
	}
}

func assertOpenAIStrictSchemaValue(t *testing.T, path string, key string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		assertOpenAIStrictObjectSchemaAt(t, path+"."+key, typed)
	case []any:
		for idx, item := range typed {
			child, ok := item.(map[string]any)
			if !ok {
				continue
			}
			assertOpenAIStrictObjectSchemaAt(t, fmt.Sprintf("%s.%s[%d]", path, key, idx), child)
		}
	}
}

func schemaType(schema map[string]any) string {
	typ, _ := schema["type"].(string)
	return typ
}

func schemaRequiredSet(schema map[string]any) (map[string]bool, bool) {
	switch req := schema["required"].(type) {
	case []string:
		out := make(map[string]bool, len(req))
		for _, name := range req {
			out[name] = true
		}
		return out, true
	case []any:
		out := make(map[string]bool, len(req))
		for _, raw := range req {
			name, ok := raw.(string)
			if ok {
				out[name] = true
			}
		}
		return out, true
	default:
		return nil, false
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
