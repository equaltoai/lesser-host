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
			wantName, ok := hostedgenesis.DeclarationToolForSection(section)
			if !ok {
				t.Fatal("missing tool mapping")
			}
			openAITool := openAIMintConversationPhaseTool(section)
			anthropicTool := anthropicMintConversationPhaseTool(section)
			if openAITool.Function.Name != wantName || anthropicTool.GetName() == nil || *anthropicTool.GetName() != wantName {
				t.Fatalf("provider tool names diverged: openai=%q anthropic=%v want=%q", openAITool.Function.Name, anthropicTool.GetName(), wantName)
			}
			openAISchema, err := json.Marshal(openAITool.Function.Parameters)
			if err != nil {
				t.Fatal(err)
			}
			anthropicSchema, err := json.Marshal(map[string]any{
				"type": "object", "properties": anthropicTool.GetInputSchema().Properties, "required": anthropicTool.GetInputSchema().Required,
				"additionalProperties": false,
			})
			if err != nil {
				t.Fatal(err)
			}
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
			schema := mintConversationPhaseToolSchema(section)
			required, ok := schema["required"].([]string)
			if !ok || !containsSchemaField(required, "candidateRevision") || !containsSchemaField(required, "candidateHash") {
				t.Fatalf("tool schema is not structurally bound to the candidate: %#v", schema)
			}
		})
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
