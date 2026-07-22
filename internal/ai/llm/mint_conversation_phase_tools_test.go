package llm

import (
	"encoding/json"
	"testing"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

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
