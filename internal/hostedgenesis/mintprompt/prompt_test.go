package mintprompt

import (
	"errors"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func mustFiveBodyPrompt(t *testing.T, reg *models.SoulAgentRegistration) string {
	t.Helper()
	got, err := MintConversationSystemPromptForContract(reg, hostedgenesis.FiveBodyDeclarationContract())
	if err != nil {
		t.Fatalf("five-body prompt: %v", err)
	}
	return got
}

func TestMintConversationSystemPromptForContract_FailsClosedWithoutFiveBody(t *testing.T) {
	for _, contract := range []hostedgenesis.DeclarationContract{
		{},
		{SchemaVersion: "soul-mint-conversation-declaration.v1", GuidanceVersion: "soul-mint-conversation-guidance.v1"},
		{SchemaVersion: "v3"},
	} {
		got, err := MintConversationSystemPromptForContract(&models.SoulAgentRegistration{}, contract)
		if !errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) || got != "" {
			t.Fatalf("expected fail-closed unconfigured contract for %#v, got prompt=%q err=%v", contract, got, err)
		}
	}
}

func TestMintConversationSystemPrompt_ContainsCoreInstructions(t *testing.T) {
	got := mustFiveBodyPrompt(t, &models.SoulAgentRegistration{})
	if !strings.Contains(got, "Soul Registry minting assistant") {
		t.Fatalf("expected core minting-assistant instruction, got: %q", got)
	}
	for _, want := range []string{
		"Phase 1 — identity",
		"Phase 2 — philosophy",
		"Phase 3 — discipline",
		"Phase 4 — boundaries",
		"Phase 5 — soul",
		"Capabilities: concrete abilities only",
		"Transparency: model/provider uncertainty",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected prompt to mention %q, got: %q", want, got)
		}
	}
}

func TestMintConversationSystemPrompt_HostedOffchainHygiene(t *testing.T) {
	got := mustFiveBodyPrompt(t, &models.SoulAgentRegistration{})
	for _, want := range []string{
		"hosted/off-chain",
		`claimLevel "self-declared"`,
		CanonicalFinalAffirmationQuestion,
		InjectionHardeningLine,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected prompt to contain %q, got: %q", want, got)
		}
	}
	for _, forbidden := range []string{"minted on-chain", "peer-validated", "operator-attested"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("prompt advertised stale/unsupported language %q: %q", forbidden, got)
		}
	}
}

func TestMintConversationSystemPrompt_FiveBodyContract(t *testing.T) {
	got := mustFiveBodyPrompt(t, &models.SoulAgentRegistration{})
	for _, want := range []string{
		hostedgenesis.DeclarationSchemaVersionV2,
		hostedgenesis.GuidanceVersionV2,
		"Phase 1 — identity",
		"Phase 2 — philosophy",
		"Phase 3 — discipline",
		"Phase 4 — boundaries",
		"Phase 5 — soul",
		"Capabilities: concrete abilities only",
		"Transparency: model/provider uncertainty",
		CanonicalFinalAffirmationQuestion,
		InjectionHardeningLine,
		`claimLevel "self-declared"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected v2 prompt to contain %q, got: %q", want, got)
		}
	}
	if strings.Count(got, "Ground -> Act -> Record -> Re-ground") != 2 {
		t.Fatalf("expected cadence defined once and echoed once, got prompt: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "re-deriv") {
		t.Fatalf("v2 prompt must not teach re-derivation: %q", got)
	}
	for _, forbidden := range []string{"minted on-chain", "peer-validated", "operator-attested"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("v2 prompt advertised stale/unsupported language %q: %q", forbidden, got)
		}
	}
}

func TestMintConversationSystemPrompt_IncludesRegistrationContext(t *testing.T) {
	reg := &models.SoulAgentRegistration{
		DomainNormalized: "acme.example",
		LocalID:          "acme",
		Capabilities:     []string{"reasoning", "tool-use"},
	}
	got := mustFiveBodyPrompt(t, reg)
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
	got := mustFiveBodyPrompt(t, reg)
	if strings.Contains(got, "Local ID") {
		t.Fatalf("expected prompt to omit Local ID when empty, got: %q", got)
	}
	if strings.Contains(got, "Declared capabilities") {
		t.Fatalf("expected prompt to omit capabilities when empty, got: %q", got)
	}
}

func TestMintConversationSystemPrompt_FiltersRetiredHostedCapability(t *testing.T) {
	reg := &models.SoulAgentRegistration{Capabilities: []string{"simulacrum.hosted-first-default", "planning"}}
	got := mustFiveBodyPrompt(t, reg)
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
