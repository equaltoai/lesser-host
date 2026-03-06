package llm

import (
	"strings"
	"testing"
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
}

func TestMintConversationDeclarationsPromptAndSchema(t *testing.T) {
	t.Parallel()

	prompt := mintConversationDeclarationsSystemPromptV1()
	if !strings.Contains(prompt, "single JSON object") {
		t.Fatalf("unexpected prompt: %q", prompt)
	}

	schema := mintConversationDeclarationsJSONSchemaV1()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level properties")
	}
	if _, ok := props["selfDescription"]; !ok {
		t.Fatalf("expected selfDescription schema")
	}
	if _, ok := props["capabilities"]; !ok {
		t.Fatalf("expected capabilities schema")
	}
	if _, ok := props["boundaries"]; !ok {
		t.Fatalf("expected boundaries schema")
	}
	if _, ok := props["transparency"]; !ok {
		t.Fatalf("expected transparency schema")
	}

	if _, _, err := MintConversationDeclarationsOpenAI(t.Context(), "k", "unsupported:model", MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported OpenAI declarations model error")
	}
	if _, _, err := MintConversationDeclarationsAnthropic(t.Context(), "k", "unsupported:model", MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported Anthropic declarations model error")
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
