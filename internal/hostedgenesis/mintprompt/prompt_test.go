package mintprompt

import (
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestMintConversationSystemPrompt_ContainsCoreInstructions(t *testing.T) {
	got := MintConversationSystemPrompt(&models.SoulAgentRegistration{})
	if !strings.Contains(got, "Soul Registry minting assistant") {
		t.Fatalf("expected core minting-assistant instruction, got: %q", got)
	}
	for _, want := range []string{"Self-Description", "Capabilities", "Boundaries", "Transparency"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected prompt to mention %q, got: %q", want, got)
		}
	}
}

func TestMintConversationSystemPrompt_IncludesRegistrationContext(t *testing.T) {
	reg := &models.SoulAgentRegistration{
		DomainNormalized: "acme.example",
		LocalID:          "acme",
		Capabilities:     []string{"reasoning", "tool-use"},
	}
	got := MintConversationSystemPrompt(reg)
	if !strings.Contains(got, "acme.example") {
		t.Fatalf("expected prompt to include domain, got: %q", got)
	}
	if !strings.Contains(got, "acme") {
		t.Fatalf("expected prompt to include local id, got: %q", got)
	}
	if !strings.Contains(got, "reasoning") || !strings.Contains(got, "tool-use") {
		t.Fatalf("expected prompt to include declared capabilities, got: %q", got)
	}
}

func TestMintConversationSystemPrompt_OmitsEmptyContext(t *testing.T) {
	reg := &models.SoulAgentRegistration{DomainNormalized: "acme.example"}
	got := MintConversationSystemPrompt(reg)
	if strings.Contains(got, "Local ID") {
		t.Fatalf("expected prompt to omit Local ID when empty, got: %q", got)
	}
	if strings.Contains(got, "Declared capabilities") {
		t.Fatalf("expected prompt to omit capabilities when empty, got: %q", got)
	}
}

func TestMintConversationSystemPrompt_FiltersRetiredHostedCapability(t *testing.T) {
	reg := &models.SoulAgentRegistration{Capabilities: []string{"simulacrum.hosted-first-default", "planning"}}
	got := MintConversationSystemPrompt(reg)
	if strings.Contains(got, "simulacrum.hosted-first-default") {
		t.Fatalf("prompt included retired placeholder capability: %q", got)
	}
	if !strings.Contains(got, "planning") {
		t.Fatalf("prompt omitted real declared capability: %q", got)
	}
}

func TestSanitizePromptInline_StripsControlCharsAndTruncates(t *testing.T) {
	if got := SanitizePromptInline("a\x00b\x1fc", 0); got != "a b c" {
		t.Fatalf("expected control chars replaced with spaces, got %q", got)
	}
	if got := SanitizePromptInline("abcdefghij", 4); got != "abcd" {
		t.Fatalf("expected truncation to 4, got %q", got)
	}
	if got := SanitizePromptInline("   ", 4); got != "" {
		t.Fatalf("expected empty after trim, got %q", got)
	}
}

func TestQuotePromptValue_JSONQuotes(t *testing.T) {
	got := QuotePromptValue(`he said "hi"`)
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("expected JSON-quoted value, got %q", got)
	}
	if !strings.Contains(got, `\"hi\"`) {
		t.Fatalf("expected inner quotes escaped, got %q", got)
	}
	if got := QuotePromptValue("   "); got != "" {
		t.Fatalf("expected empty for blank input, got %q", got)
	}
}
