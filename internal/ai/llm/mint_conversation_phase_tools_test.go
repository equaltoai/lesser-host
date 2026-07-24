package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

func TestMintConversationPhaseProviderLoopsRepairCurrentSectionAndReachText(t *testing.T) {
	for _, test := range []struct {
		name      string
		modelSet  string
		provider  string
		responses [][]byte
	}{
		{name: "openai", modelSet: "openai:gpt-test", provider: "openai", responses: openAIMintConversationPhaseResponses(t)},
		{name: "anthropic", modelSet: "anthropic:claude-test", provider: "anthropic", responses: anthropicMintConversationPhaseResponses(t)},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertMintConversationPhaseProviderRepair(t, test.modelSet, test.provider, test.responses)
		})
	}
}

func TestOpenAIMintConversationSoulPhaseRejectsLengthAsInvalidProviderOutput(t *testing.T) {
	response := mustJSONBytes(t, map[string]any{
		"id": "chatcmpl-soul-length", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "length",
			"message": map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []any{map[string]any{
					"id": "call-soul-length", "type": "function",
					"function": map[string]any{"name": hostedgenesis.DeclarationToolSoulPut, "arguments": `{"candidateRevision":4`},
				}},
			},
		}},
		"usage": map[string]any{
			"prompt_tokens": 100, "completion_tokens": 4096, "total_tokens": 4196,
			"completion_tokens_details": map[string]any{"reasoning_tokens": 1024},
		},
	})
	requestCount, requests := installMintConversationPhaseProvider(t, "openai", [][]byte{response})
	handlerCalls := 0
	var events []ProviderTelemetryEvent
	_, err := RunMintConversationPhase(t.Context(), "provider-test-key", MintConversationPhaseInput{
		ModelSet: "openai:gpt-test", SystemPrompt: "Construct the current section.",
		Messages: []MintConversationMessage{{Role: "user", Content: "Preserve all six refusal rules."}},
		Section:  hostedgenesis.DeclarationSectionSoul, CandidateRevision: 4,
		CandidateHash: "sha256:" + strings.Repeat("a", 64), SourceTurnID: "turn-soul",
	}, func(context.Context, MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error) {
		handlerCalls++
		return hostedgenesis.DeclarationToolResult{Accepted: true}, nil
	}, func(event ProviderTelemetryEvent) {
		events = append(events, event)
	})
	if err == nil || ProviderFailureClass(err) != string(hostedgenesis.FailureClassInvalidProviderOutput) {
		t.Fatalf("length finish reason was not classified as invalid provider output: class=%q err=%v", ProviderFailureClass(err), err)
	}
	if handlerCalls != 0 || *requestCount != 1 {
		t.Fatalf("incomplete tool output reached validation or retried: handler=%d requests=%d", handlerCalls, *requestCount)
	}
	if len(events) == 0 || events[len(events)-1].EventType != "provider_call_failed" ||
		events[len(events)-1].FailureClass != string(hostedgenesis.FailureClassInvalidProviderOutput) ||
		events[len(events)-1].StopReason != "length" {
		t.Fatalf("incomplete output telemetry was not content-free and classified: %#v", events)
	}
	var request struct {
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal((*requests)[0], &request); err != nil {
		t.Fatal(err)
	}
	if request.MaxCompletionTokens != mintConversationOpenAISoulMaxCompletionTokens {
		t.Fatalf("soul output budget drifted: got=%d want=%d", request.MaxCompletionTokens, mintConversationOpenAISoulMaxCompletionTokens)
	}
}

func assertMintConversationPhaseProviderRepair(t *testing.T, modelSet string, provider string, responses [][]byte) {
	t.Helper()
	requestCount, requests := installMintConversationPhaseProvider(t, provider, responses)
	handlerCalls := 0
	out, err := RunMintConversationPhase(context.Background(), "provider-test-key", MintConversationPhaseInput{
		ModelSet: modelSet, SystemPrompt: "Construct the current section.",
		Messages: []MintConversationMessage{{Role: "user", Content: "I am tenant bound."}},
		Section:  hostedgenesis.DeclarationSectionIdentity, CandidateRevision: 0,
		CandidateHash: "sha256:" + strings.Repeat("a", 64), SourceTurnID: "turn-1",
	}, func(_ context.Context, call MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error) {
		handlerCalls++
		if call.Name != hostedgenesis.DeclarationToolIdentityPut || call.CallID == "" || len(call.Arguments) == 0 {
			t.Fatalf("provider emitted an unbound tool call: %#v", call)
		}
		if handlerCalls == 1 {
			return hostedgenesis.DeclarationToolResult{Section: hostedgenesis.DeclarationSectionIdentity, Errors: []hostedgenesis.DeclarationValidationIssue{{
				Section: hostedgenesis.DeclarationSectionIdentity, Path: "fiveBodies.identity.summary", Code: hostedgenesis.DeclarationCodeFiveBodyIdentity,
			}}}, nil
		}
		return hostedgenesis.DeclarationToolResult{Accepted: true, Section: hostedgenesis.DeclarationSectionIdentity, Revision: 1, CandidateHash: "sha256:" + strings.Repeat("b", 64)}, nil
	}, nil)
	if err != nil || out.AssistantContent != "Identity accepted; continue to philosophy." {
		t.Fatalf("phase loop did not repair and continue: out=%#v err=%v", out, err)
	}
	if handlerCalls != 2 || *requestCount != 3 {
		t.Fatalf("phase loop counts diverged: handler=%d requests=%d", handlerCalls, *requestCount)
	}
	if out.Usage.Provider != provider || out.Usage.TotalTokens == 0 {
		t.Fatalf("phase usage missing provider identity: %#v", out.Usage)
	}
	assertMintConversationPhaseContinuationRequest(t, provider, *requests)
}

func installMintConversationPhaseProvider(t *testing.T, provider string, responses [][]byte) (*int, *[][]byte) {
	t.Helper()
	requestCount := 0
	requests := make([][]byte, 0, len(responses))
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read %s request body: %v", provider, err)
		}
		requests = append(requests, append([]byte(nil), body...))
		index := requestCount
		requestCount++
		if index >= len(responses) {
			t.Fatalf("unexpected extra %s request %d", provider, requestCount)
		}
		return providerTelemetryHTTPResponse(request, "application/json", responses[index]), nil
	})}
	switch provider {
	case "openai":
		t.Setenv("OPENAI_BASE_URL", "https://openai.example.test")
		openAIHTTPClient = client
		t.Cleanup(func() { openAIHTTPClient = nil })
	case "anthropic":
		t.Setenv("ANTHROPIC_BASE_URL", "https://anthropic.example.test")
		anthropicHTTPClient = client
		t.Cleanup(func() { anthropicHTTPClient = nil })
	default:
		t.Fatalf("unsupported test provider %q", provider)
	}
	return &requestCount, &requests
}

func assertMintConversationPhaseContinuationRequest(t *testing.T, provider string, requests [][]byte) {
	t.Helper()
	if len(requests) != 3 {
		t.Fatalf("expected three %s requests, got %d", provider, len(requests))
	}
	for index, body := range requests {
		var request struct {
			Tools      []json.RawMessage `json:"tools"`
			ToolChoice json.RawMessage   `json:"tool_choice"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode %s request %d: %v", provider, index+1, err)
		}
		if len(request.Tools) != 1 {
			t.Fatalf("%s request %d lost its section-local tool declaration", provider, index+1)
		}
		if index != len(requests)-1 {
			continue
		}
		switch provider {
		case "openai":
			var choice string
			if err := json.Unmarshal(request.ToolChoice, &choice); err != nil || choice != "none" {
				t.Fatalf("openai continuation did not disable the declared tool: choice=%s err=%v", request.ToolChoice, err)
			}
		case "anthropic":
			var choice struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(request.ToolChoice, &choice); err != nil || choice.Type != "none" {
				t.Fatalf("anthropic continuation did not disable the declared tool: choice=%s err=%v", request.ToolChoice, err)
			}
		default:
			t.Fatalf("unsupported test provider %q", provider)
		}
	}
}

func openAIMintConversationPhaseResponses(t *testing.T) [][]byte {
	t.Helper()
	return [][]byte{
		mustJSONBytes(t, openAIMintConversationPhaseToolResponse("openai-call-reject", 2, 1)),
		mustJSONBytes(t, openAIMintConversationPhaseToolResponse("openai-call-accept", 3, 2)),
		mustJSONBytes(t, map[string]any{
			"id": "chatcmpl-text", "object": "chat.completion", "created": 1, "model": "gpt-test",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "Identity accepted; continue to philosophy."}}},
			"usage":   map[string]any{"prompt_tokens": 4, "completion_tokens": 3, "total_tokens": 7},
		}),
	}
}

func openAIMintConversationPhaseToolResponse(callID string, inputTokens int, outputTokens int) map[string]any {
	arguments := `{"candidateRevision":0,"candidateHash":"sha256:` + strings.Repeat("a", 64) + `","section":{"summary":"I am tenant bound.","notes":[]}}`
	return map[string]any{
		"id": "chatcmpl-" + callID, "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{
			"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": callID, "type": "function", "function": map[string]any{"name": hostedgenesis.DeclarationToolIdentityPut, "arguments": arguments}}},
		}}},
		"usage": map[string]any{"prompt_tokens": inputTokens, "completion_tokens": outputTokens, "total_tokens": inputTokens + outputTokens},
	}
}

func anthropicMintConversationPhaseResponses(t *testing.T) [][]byte {
	t.Helper()
	return [][]byte{
		mustJSONBytes(t, anthropicMintConversationPhaseToolResponse("anthropic-call-reject", 2, 1)),
		mustJSONBytes(t, anthropicMintConversationPhaseToolResponse("anthropic-call-accept", 3, 2)),
		mustJSONBytes(t, map[string]any{
			"id": "msg-text", "type": "message", "role": "assistant", "model": "claude-test", "stop_reason": "end_turn", "stop_sequence": nil,
			"content": []any{map[string]any{"type": "text", "text": "Identity accepted; continue to philosophy."}},
			"usage":   map[string]any{"input_tokens": 4, "output_tokens": 3},
		}),
	}
}

func anthropicMintConversationPhaseToolResponse(callID string, inputTokens int, outputTokens int) map[string]any {
	return map[string]any{
		"id": "msg-" + callID, "type": "message", "role": "assistant", "model": "claude-test", "stop_reason": "tool_use", "stop_sequence": nil,
		"content": []any{map[string]any{"type": "tool_use", "id": callID, "name": hostedgenesis.DeclarationToolIdentityPut, "input": map[string]any{
			"candidateRevision": 0, "candidateHash": "sha256:" + strings.Repeat("a", 64), "section": map[string]any{"summary": "I am tenant bound.", "notes": []any{}},
		}}},
		"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
	}
}

func TestMintConversationPhaseToolsOpenAIAnthropicParity(t *testing.T) {
	for _, section := range []hostedgenesis.DeclarationSection{
		hostedgenesis.DeclarationSectionIdentity,
		hostedgenesis.DeclarationSectionPhilosophy,
		hostedgenesis.DeclarationSectionDiscipline,
		hostedgenesis.DeclarationSectionBoundaries,
		hostedgenesis.DeclarationSectionSoul,
	} {
		section := section
		t.Run(string(section), func(t *testing.T) {
			assertMintConversationPhaseToolParity(t, section)
		})
	}
}

func TestMintConversationPhasePromptRejectsTruncationForBothProviderPaths(t *testing.T) {
	input := MintConversationPhaseInput{
		SystemPrompt: "five-body contract", Section: hostedgenesis.DeclarationSectionBoundaries,
		CandidateRevision: 5, CandidateHash: "sha256:" + strings.Repeat("a", 64), SourceTurnID: "turn-review-edit",
	}
	prompt := mintConversationPhaseSystemPrompt(input)
	for _, required := range []string{
		"summary at most 2400 Unicode characters",
		"notes at most 8 items of at most 480 Unicode characters each",
		"Preserve every owner-supplied item for the current section.",
		"Host rejects over-limit fields instead of truncating them.",
		hostedgenesis.DeclarationToolBoundariesPut,
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("shared OpenAI/Anthropic section prompt missing %q: %s", required, prompt)
		}
	}
	input.Section = hostedgenesis.DeclarationSectionSoul
	if prompt := mintConversationPhaseSystemPrompt(input); !strings.Contains(prompt, "refusals 3-8 items") ||
		!strings.Contains(prompt, "each at most 480 Unicode characters") {
		t.Fatalf("shared soul prompt omits exact refusal limits: %s", prompt)
	}
}

func TestMintConversationPhaseToolSchemaCarriesCanonicalSectionLimits(t *testing.T) {
	section := mintConversationPhaseToolSchema(hostedgenesis.DeclarationSectionBoundaries)
	properties := requireSchemaMap(t, section, "properties")
	body := requireSchemaMap(t, properties, "section")
	bodyProperties := requireSchemaMap(t, body, "properties")
	summary := requireSchemaMap(t, bodyProperties, "summary")
	notes := requireSchemaMap(t, bodyProperties, "notes")
	note := requireSchemaMap(t, notes, "items")
	if summary["maxLength"] != mintConversationPhaseSummaryMaxRunes ||
		notes["maxItems"] != mintConversationPhaseNotesMaxItems ||
		note["maxLength"] != mintConversationPhaseNoteMaxRunes {
		t.Fatalf("section tool schema limits drifted: %#v", body)
	}

	soulSchema := mintConversationPhaseToolSchema(hostedgenesis.DeclarationSectionSoul)
	soulProperties := requireSchemaMap(t, soulSchema, "properties")
	soulBody := requireSchemaMap(t, soulProperties, "section")
	refusals := requireSchemaMap(t, requireSchemaMap(t, soulBody, "properties"), "refusals")
	refusalProperties := requireSchemaMap(t, requireSchemaMap(t, refusals, "items"), "properties")
	if refusals["minItems"] != mintConversationPhaseRefusalsMinItems ||
		refusals["maxItems"] != mintConversationPhaseRefusalsMaxItems ||
		requireSchemaMap(t, refusalProperties, "bypass")["maxLength"] != mintConversationPhaseRefusalFieldMaxRunes ||
		requireSchemaMap(t, refusalProperties, "invariant")["maxLength"] != mintConversationPhaseRefusalFieldMaxRunes ||
		requireSchemaMap(t, refusalProperties, "closestSafePath")["maxLength"] != mintConversationPhaseRefusalFieldMaxRunes {
		t.Fatalf("soul tool schema limits drifted: %#v", soulBody)
	}
}

func assertMintConversationPhaseToolParity(t *testing.T, section hostedgenesis.DeclarationSection) {
	t.Helper()
	wantName, ok := hostedgenesis.DeclarationToolForSection(section)
	if !ok {
		t.Fatal("missing tool mapping")
	}
	openAITool := openAIMintConversationPhaseTool(section)
	anthropicTool := anthropicMintConversationPhaseTool(section)
	if openAITool.Function.Name != wantName || anthropicTool.GetName() == nil || *anthropicTool.GetName() != wantName {
		t.Fatalf("provider tool names diverged: openai=%q anthropic=%v want=%q", openAITool.Function.Name, anthropicTool.GetName(), wantName)
	}
	openAISchema := mustJSONBytes(t, openAITool.Function.Parameters)
	anthropicSchema := mustJSONBytes(t, map[string]any{
		"type": "object", "properties": anthropicTool.GetInputSchema().Properties, "required": anthropicTool.GetInputSchema().Required,
		"additionalProperties": false,
	})
	var openAIShape, anthropicShape any
	if err := json.Unmarshal(openAISchema, &openAIShape); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(anthropicSchema, &anthropicShape); err != nil {
		t.Fatal(err)
	}
	if string(mustCanonicalJSON(t, openAIShape)) != string(mustCanonicalJSON(t, anthropicShape)) {
		t.Fatalf("provider schemas diverged\nopenai=%s\nanthropic=%s", openAISchema, anthropicSchema)
	}
	assertMintConversationPhaseToolBindings(t, mintConversationPhaseToolSchema(section))
}

func mustJSONBytes(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertMintConversationPhaseToolBindings(t *testing.T, schema map[string]any) {
	t.Helper()
	required, ok := schema["required"].([]string)
	if !ok || !containsSchemaField(required, "candidateRevision") || !containsSchemaField(required, "candidateHash") {
		t.Fatalf("tool schema is not structurally bound to the candidate: %#v", schema)
	}
}

func TestMintConversationPhaseInputRejectsUnboundToolState(t *testing.T) {
	for _, input := range []MintConversationPhaseInput{
		{},
		{Section: hostedgenesis.DeclarationSectionIdentity, CandidateRevision: -1, CandidateHash: "sha256:x", SourceTurnID: "turn"},
		{Section: hostedgenesis.DeclarationSectionIdentity, CandidateRevision: 0, CandidateHash: "", SourceTurnID: "turn"},
		{Section: hostedgenesis.DeclarationSectionIdentity, CandidateRevision: 0, CandidateHash: "sha256:x", SourceTurnID: ""},
	} {
		if err := validateMintConversationPhaseInput(input); err == nil {
			t.Fatalf("expected invalid phase input: %#v", input)
		}
	}
}

func mustCanonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func containsSchemaField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
