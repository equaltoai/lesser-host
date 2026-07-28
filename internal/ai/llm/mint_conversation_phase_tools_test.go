package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
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

func TestHostedGenesisAliasesRouteWithMediumReasoningEffort(t *testing.T) {
	for _, test := range []hostedGenesisAliasTestCase{
		{name: "openai", alias: modelselection.AliasOpenAI, provider: modelselection.ProviderOpenAI, model: "gpt-5.6-luna"},
		{name: "anthropic", alias: modelselection.AliasAnthropic, provider: modelselection.ProviderAnthropic, model: "claude-sonnet-5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertHostedGenesisAliasProviderRequest(t, test)
		})
	}
}

type hostedGenesisAliasTestCase struct {
	name     string
	alias    string
	provider string
	model    string
}

func assertHostedGenesisAliasProviderRequest(t *testing.T, test hostedGenesisAliasTestCase) {
	t.Helper()
	responses := openAIMintConversationPhaseResponses(t)
	if test.provider == modelselection.ProviderAnthropic {
		responses = anthropicMintConversationPhaseResponses(t)
	}
	_, requests := installMintConversationPhaseProvider(t, test.provider, [][]byte{responses[2]})
	_, err := RunMintConversationPhase(t.Context(), "provider-test-key", MintConversationPhaseInput{
		ModelSet: test.alias, SystemPrompt: "Construct the current section.",
		Messages: []MintConversationMessage{{Role: "user", Content: "I am tenant bound."}},
		Section:  hostedgenesis.DeclarationSectionIdentity, CandidateRevision: 0,
		CandidateHash: "sha256:" + strings.Repeat("a", 64), SourceTurnID: "turn-alias",
	}, func(context.Context, MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error) {
		t.Fatal("alias effort test should complete with provider text, not a tool call")
		return hostedgenesis.DeclarationToolResult{}, nil
	}, nil)
	if err != nil {
		t.Fatalf("alias phase failed: %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("expected one provider request, got %d", len(*requests))
	}
	var body map[string]any
	if err := json.Unmarshal((*requests)[0], &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != test.model {
		t.Fatalf("provider model = %#v, want %q", body["model"], test.model)
	}
	assertHostedGenesisAliasEffort(t, test.provider, body)
}

func assertHostedGenesisAliasEffort(t *testing.T, provider string, body map[string]any) {
	t.Helper()
	if provider == modelselection.ProviderOpenAI {
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != modelselection.ReasoningEffortMedium {
			t.Fatalf("OpenAI reasoning = %#v", body["reasoning"])
		}
		return
	}
	outputConfig, ok := body["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != modelselection.ReasoningEffortMedium {
		t.Fatalf("Anthropic output_config = %#v", body["output_config"])
	}
}

func TestSoulPhaseCapabilitySchemaMatchesHostValidationContract(t *testing.T) {
	schema := producedCapabilitiesSchema()
	items, ok := schema["items"].(map[string]any)
	if !ok {
		t.Fatalf("capability schema items missing: %#v", schema)
	}
	properties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("capability schema properties missing: %#v", items)
	}
	assertSchemaField := func(name string, want map[string]any) {
		t.Helper()
		field, ok := properties[name].(map[string]any)
		if !ok {
			t.Fatalf("capability field %s missing: %#v", name, properties)
		}
		for key, value := range want {
			if field[key] != value {
				t.Fatalf("capability field %s %s drifted: got=%#v want=%#v", name, key, field[key], value)
			}
		}
		if description, _ := field["description"].(string); !strings.Contains(description, "Example:") {
			t.Fatalf("capability field %s lacks a concrete example: %#v", name, field)
		}
	}
	assertSchemaField("capability", map[string]any{
		"minLength": 1, "maxLength": hostedgenesis.MaxProducedCapabilityIdentifierLength,
		"pattern": hostedgenesis.ProducedCapabilityEvidencePattern,
	})
	assertSchemaField("scope", map[string]any{
		"minLength": 1, "maxLength": hostedgenesis.MaxProducedCapabilityScopeLength,
		"pattern": hostedgenesis.ProducedCapabilityNonWhitespacePattern,
	})
	assertSchemaField("claimLevel", map[string]any{"description": `Use exactly "self-declared". Example: "self-declared".`})
	assertSchemaField("lastValidated", map[string]any{
		"maxLength": hostedgenesis.MaxProducedCapabilityLastValidatedLength,
		"pattern":   hostedgenesis.ProducedCapabilityOptionalRFC3339Pattern,
	})
	assertSchemaField("validationRef", map[string]any{"maxLength": hostedgenesis.MaxProducedCapabilityMetadataLength})
	assertSchemaField("degradesTo", map[string]any{"maxLength": hostedgenesis.MaxProducedCapabilityMetadataLength})
}

func TestOpenAIMintConversationSoulPhaseRejectsLengthAsInvalidProviderOutput(t *testing.T) {
	response := mustJSONBytes(t, map[string]any{
		"id": "resp-soul-length", "object": "response", "created_at": 1, "model": "gpt-test",
		"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"},
		"output": []any{map[string]any{
			"type": "function_call", "id": "fc-soul-length", "call_id": "call-soul-length",
			"name": hostedgenesis.DeclarationToolSoulPut, "arguments": `{"candidateRevision":4`, "status": "incomplete",
		}},
		"usage": map[string]any{
			"input_tokens": 100, "output_tokens": 4096, "total_tokens": 4196,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 1024},
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
		MaxOutputTokens int64 `json:"max_output_tokens"`
	}
	if err := json.Unmarshal((*requests)[0], &request); err != nil {
		t.Fatal(err)
	}
	if request.MaxOutputTokens != mintConversationOpenAISoulMaxCompletionTokens {
		t.Fatalf("soul output budget drifted: got=%d want=%d", request.MaxOutputTokens, mintConversationOpenAISoulMaxCompletionTokens)
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
	}, mintConversationPhaseRepairHandler(t, &handlerCalls), nil)
	assertMintConversationPhaseRepairOutput(t, provider, out, err, handlerCalls, *requestCount)
	assertMintConversationPhaseContinuationRequest(t, provider, *requests)
}

func mintConversationPhaseRepairHandler(t *testing.T, handlerCalls *int) func(context.Context, MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error) {
	t.Helper()
	return func(_ context.Context, call MintConversationPhaseToolCall) (hostedgenesis.DeclarationToolResult, error) {
		*handlerCalls++
		assertMintConversationPhaseToolCall(t, call)
		if *handlerCalls == 1 {
			return hostedgenesis.DeclarationToolResult{Section: hostedgenesis.DeclarationSectionIdentity, Errors: []hostedgenesis.DeclarationValidationIssue{{
				Section: hostedgenesis.DeclarationSectionIdentity, Path: "fiveBodies.identity.summary", Code: hostedgenesis.DeclarationCodeFiveBodyIdentity,
			}}}, nil
		}
		return hostedgenesis.DeclarationToolResult{Accepted: true, Section: hostedgenesis.DeclarationSectionIdentity, Revision: 1, CandidateHash: "sha256:" + strings.Repeat("b", 64)}, nil
	}
}

func assertMintConversationPhaseToolCall(t *testing.T, call MintConversationPhaseToolCall) {
	t.Helper()
	if call.Name != hostedgenesis.DeclarationToolIdentityPut {
		t.Fatalf("provider emitted an unexpected tool: %#v", call)
	}
	if call.CallID == "" || len(call.Arguments) == 0 {
		t.Fatalf("provider emitted an unbound tool call: %#v", call)
	}
}

func assertMintConversationPhaseRepairOutput(t *testing.T, provider string, out MintConversationPhaseOutput, err error, handlerCalls int, requestCount int) {
	t.Helper()
	if err != nil {
		t.Fatalf("phase loop did not repair and continue: out=%#v err=%v", out, err)
	}
	if out.AssistantContent != "Identity accepted; continue to philosophy." {
		t.Fatalf("phase loop did not reach continuation text: out=%#v", out)
	}
	if handlerCalls != 2 || requestCount != 3 {
		t.Fatalf("phase loop counts diverged: handler=%d requests=%d", handlerCalls, requestCount)
	}
	if out.Usage.Provider != provider || out.Usage.TotalTokens == 0 {
		t.Fatalf("phase usage missing provider identity: %#v", out.Usage)
	}
	if provider == testProviderOpenAI {
		assertOpenAIMintConversationUsage(t, out.Usage)
	}
}

func assertOpenAIMintConversationUsage(t *testing.T, usage models.AIUsage) {
	t.Helper()
	if usage.InputTokens != 9 {
		t.Fatalf("Responses input usage mapping drifted: %#v", usage)
	}
	if usage.OutputTokens != 6 || usage.TotalTokens != 15 || usage.ToolCalls != 3 {
		t.Fatalf("Responses usage mapping drifted: %#v", usage)
	}
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
		request := decodeMintConversationPhaseRequest(t, provider, index, body)
		if len(request.Tools) != 1 {
			t.Fatalf("%s request %d lost its section-local tool declaration", provider, index+1)
		}
		if provider == testProviderOpenAI {
			assertOpenAIMintConversationPhaseRequest(t, index, body, request)
		}
		if index == len(requests)-1 {
			assertMintConversationPhaseFinalToolChoice(t, provider, request.ToolChoice)
		}
	}
}

type mintConversationPhaseRequest struct {
	Input      []json.RawMessage `json:"input"`
	Tools      []json.RawMessage `json:"tools"`
	ToolChoice json.RawMessage   `json:"tool_choice"`
}

func decodeMintConversationPhaseRequest(t *testing.T, provider string, index int, body []byte) mintConversationPhaseRequest {
	t.Helper()
	var request mintConversationPhaseRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode %s request %d: %v", provider, index+1, err)
	}
	return request
}

func assertOpenAIMintConversationPhaseRequest(t *testing.T, index int, body []byte, request mintConversationPhaseRequest) {
	t.Helper()
	wantInputItems := []int{2, 5, 8}[index]
	if len(request.Input) != wantInputItems {
		t.Fatalf("openai request %d changed the Responses conversation input: got=%d want=%d body=%s", index+1, len(request.Input), wantInputItems, body)
	}
	inputTypes, inputRoles := decodeOpenAIMintConversationInput(t, index, request.Input)
	if index == 0 {
		if inputRoles[0] != "system" || inputRoles[1] != mintConversationUserRole {
			t.Fatalf("openai request 1 lost system/conversation messages: roles=%#v types=%#v", inputRoles, inputTypes)
		}
	} else if !containsSchemaField(inputTypes, "reasoning") || !containsSchemaField(inputTypes, openAIResponseFunctionCallType) || !containsSchemaField(inputTypes, "function_call_output") {
		t.Fatalf("openai request %d lost Responses tool-loop items: %#v", index+1, inputTypes)
	}
	if index < 2 {
		var choice string
		if err := json.Unmarshal(request.ToolChoice, &choice); err != nil || choice != "auto" {
			t.Fatalf("openai request %d did not retain automatic tool choice: choice=%s err=%v", index+1, request.ToolChoice, err)
		}
	}
}

func decodeOpenAIMintConversationInput(t *testing.T, index int, input []json.RawMessage) ([]string, []string) {
	t.Helper()
	inputTypes := make([]string, 0, len(input))
	inputRoles := make([]string, 0, len(input))
	for _, raw := range input {
		var item struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("decode openai request %d input item: %v", index+1, err)
		}
		inputTypes = append(inputTypes, item.Type)
		inputRoles = append(inputRoles, item.Role)
	}
	return inputTypes, inputRoles
}

func assertMintConversationPhaseFinalToolChoice(t *testing.T, provider string, raw json.RawMessage) {
	t.Helper()
	switch provider {
	case testProviderOpenAI:
		var choice string
		if err := json.Unmarshal(raw, &choice); err != nil || choice != "none" {
			t.Fatalf("openai continuation did not disable the declared tool: choice=%s err=%v", raw, err)
		}
	case "anthropic":
		var choice struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &choice); err != nil || choice.Type != "none" {
			t.Fatalf("anthropic continuation did not disable the declared tool: choice=%s err=%v", raw, err)
		}
	default:
		t.Fatalf("unsupported test provider %q", provider)
	}
}

func openAIMintConversationPhaseResponses(t *testing.T) [][]byte {
	t.Helper()
	return [][]byte{
		mustJSONBytes(t, openAIMintConversationPhaseToolResponse("openai-call-reject", 2, 1)),
		mustJSONBytes(t, openAIMintConversationPhaseToolResponse("openai-call-accept", 3, 2)),
		mustJSONBytes(t, map[string]any{
			"id": "resp-text", "object": "response", "created_at": 1, "model": "gpt-test", "status": "completed",
			"output": []any{map[string]any{
				"type": "message", "id": "msg-text", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": "Identity accepted; continue to philosophy.", "annotations": []any{}, "logprobs": []any{}}},
			}},
			"usage": map[string]any{"input_tokens": 4, "output_tokens": 3, "total_tokens": 7,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 0}},
		}),
	}
}

func openAIMintConversationPhaseToolResponse(callID string, inputTokens int, outputTokens int) map[string]any {
	arguments := `{"candidateRevision":0,"candidateHash":"sha256:` + strings.Repeat("a", 64) + `","section":{"summary":"I am tenant bound.","notes":[]}}`
	return map[string]any{
		"id": "resp-" + callID, "object": "response", "created_at": 1, "model": "gpt-test", "status": "completed",
		"output": []any{
			map[string]any{"type": "reasoning", "id": "reasoning-" + callID, "summary": []any{}},
			map[string]any{"type": "function_call", "id": "fc-" + callID, "call_id": callID, "name": hostedgenesis.DeclarationToolIdentityPut, "arguments": arguments, "status": "completed"},
		},
		"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens, "total_tokens": inputTokens + outputTokens,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0}},
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
	if openAITool.OfFunction == nil || openAITool.OfFunction.Name != wantName || anthropicTool.GetName() == nil || *anthropicTool.GetName() != wantName {
		var openAIName string
		if openAITool.OfFunction != nil {
			openAIName = openAITool.OfFunction.Name
		}
		t.Fatalf("provider tool names diverged: openai=%q anthropic=%v want=%q", openAIName, anthropicTool.GetName(), wantName)
	}
	openAISchema := mustJSONBytes(t, openAITool.OfFunction.Parameters)
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
