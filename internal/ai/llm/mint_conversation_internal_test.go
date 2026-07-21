package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	mintConversationTestAnthropicModel  = "claude-sonnet-4-6"
	mintConversationTestAuthoredByAgent = "agent"
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
			{"capability":" Itinerary Planning ","scope":" build routes ","claimLevel":" self-declared ","lastValidated":" 2026-03-05T00:00:00Z ","validationRef":" ref ","degradesTo":" email "},
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
	if norm.SelfDescription.AuthoredBy != mintConversationTestAuthoredByAgent {
		t.Fatalf("unexpected authoredBy: %q", norm.SelfDescription.AuthoredBy)
	}
	if len(norm.Capabilities) != 3 {
		t.Fatalf("normalization must preserve malformed rows for fail-closed validation, got %d", len(norm.Capabilities))
	}
	if norm.Capabilities[0].Capability != "itinerary_planning" || norm.Capabilities[0].ClaimLevel != "self-declared" {
		t.Fatalf("expected canonical capability identifier, got %#v", norm.Capabilities[0])
	}
	if norm.Capabilities[1].Capability != "" || norm.Capabilities[2].Scope != "" {
		t.Fatalf("malformed capability rows must remain visible to validation, got %#v", norm.Capabilities)
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

func TestMintConversationDeclarationsConfigFailsClosedWithoutFiveBody(t *testing.T) {
	t.Parallel()

	// Historical pre-five-body version strings are adversarial fixtures only;
	// deployed code has no constant or builder for them.
	for name, input := range map[string]MintConversationDeclarationsInput{
		"empty":      {},
		"historical": {SchemaVersion: "soul-mint-conversation-declaration.v1", GuidanceVersion: "soul-mint-conversation-guidance.v1"},
		"unknown":    {SchemaVersion: "v3"},
	} {
		if _, err := mintConversationDeclarationsConfig(input); !errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) {
			t.Fatalf("%s: expected fail-closed unconfigured contract, got %v", name, err)
		}
		if _, _, err := MintConversationDeclarationsOpenAI(t.Context(), "k", "openai:gpt-4.1-mini", input); !errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) {
			t.Fatalf("%s: expected OpenAI extraction to fail closed before any provider call, got %v", name, err)
		}
		if _, _, err := MintConversationDeclarationsAnthropic(t.Context(), "k", "anthropic:"+mintConversationTestAnthropicModel, input); !errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) {
			t.Fatalf("%s: expected Anthropic extraction to fail closed before any provider call, got %v", name, err)
		}
	}

	fiveBody := MintConversationDeclarationsInput{
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
	}
	if _, _, err := MintConversationDeclarationsOpenAI(t.Context(), "k", "unsupported:model", fiveBody); err == nil {
		t.Fatalf("expected unsupported OpenAI declarations model error")
	}
	if _, _, err := MintConversationDeclarationsAnthropic(t.Context(), "k", "unsupported:model", fiveBody); err == nil {
		t.Fatalf("expected unsupported Anthropic declarations model error")
	}
}

func TestMintConversationDeclarationsFiveBodyPromptAndSchema(t *testing.T) {
	t.Parallel()

	contract := hostedgenesis.FiveBodyDeclarationContract()
	input := MintConversationDeclarationsInput{SchemaVersion: contract.SchemaVersion, GuidanceVersion: contract.GuidanceVersion}
	cfg, cfgErr := mintConversationDeclarationsConfig(input)
	if cfgErr != nil {
		t.Fatalf("five-body config: %v", cfgErr)
	}
	if cfg.schemaName != "soul_five_body_declarations" || !strings.Contains(cfg.systemPrompt, contract.SchemaVersion) || !strings.Contains(cfg.systemPrompt, contract.GuidanceVersion) {
		t.Fatalf("unexpected v2 config: %#v prompt=%q", cfg, cfg.systemPrompt)
	}
	if strings.Contains(strings.ToLower(cfg.systemPrompt), "re-deriv") {
		t.Fatalf("v2 extraction prompt must not teach re-derivation: %q", cfg.systemPrompt)
	}
	for _, want := range []string{"five first-class bodies", "identity", "philosophy", "discipline", "boundaries", "soul.refusals", `claimLevel "` + mintConversationClaimLevelSelfDeclared + `"`, "canonical lowercase machine identifier", "RFC3339"} {
		if !strings.Contains(cfg.systemPrompt, want) {
			t.Fatalf("expected v2 extraction prompt to mention %q, got %q", want, cfg.systemPrompt)
		}
	}

	schema := cfg.schema
	props := requireSchemaMap(t, schema, "properties")
	if _, exists := props["selfDescription"]; exists {
		t.Fatalf("v2 schema should make fiveBodies first-class instead of extracting legacy selfDescription directly")
	}
	fiveBodies := requireSchemaMap(t, props, "fiveBodies")
	fiveProps := requireSchemaMap(t, fiveBodies, "properties")
	for _, body := range []string{"identity", "philosophy", "discipline", "boundaries", "soul"} {
		if _, exists := fiveProps[body]; !exists {
			t.Fatalf("expected fiveBodies.%s schema, got %#v", body, fiveProps)
		}
	}
	soulSchema := requireSchemaMap(t, fiveProps, "soul")
	soulProps := requireSchemaMap(t, soulSchema, "properties")
	refusals := requireSchemaMap(t, soulProps, "refusals")
	if got, ok := refusals["minItems"].(int); !ok || got != 3 {
		t.Fatalf("expected soul.refusals minItems=3, got %#v", refusals["minItems"])
	}
	assertMintConversationCapabilitySchema(t, props)
	assertOpenAIStrictObjectSchema(t, schema)
}

func assertMintConversationCapabilitySchema(t *testing.T, declarationProperties map[string]any) {
	t.Helper()

	capabilities := requireSchemaMap(t, declarationProperties, "capabilities")
	capabilityItems := requireSchemaMap(t, capabilities, "items")
	capabilityProps := requireSchemaMap(t, capabilityItems, "properties")
	capabilityName := requireSchemaMap(t, capabilityProps, "capability")
	if capabilityName["pattern"] != hostedgenesis.ProducedCapabilityEvidencePattern || capabilityName["maxLength"] != hostedgenesis.MaxProducedCapabilityIdentifierLength {
		t.Fatalf("provider schema must admit only bounded canonicalizable capability evidence, got %#v", capabilityName)
	}
	capabilityScope := requireSchemaMap(t, capabilityProps, "scope")
	if capabilityScope["pattern"] != hostedgenesis.ProducedCapabilityNonWhitespacePattern || capabilityScope["maxLength"] != hostedgenesis.MaxProducedCapabilityScopeLength {
		t.Fatalf("provider schema must require a bounded non-empty scope, got %#v", capabilityScope)
	}
	lastValidated := requireSchemaMap(t, capabilityProps, "lastValidated")
	if lastValidated["pattern"] != hostedgenesis.ProducedCapabilityOptionalRFC3339Pattern {
		t.Fatalf("provider schema must constrain lastValidated to empty or RFC3339, got %#v", lastValidated)
	}
}

func TestMintConversationDeclarationsCapabilityContractSurvivesHostValidation(t *testing.T) {
	t.Parallel()

	parsed := MintConversationDeclarationsDraft{Capabilities: []soul.CapabilityV2{{
		Capability:    "Hosted Genesis Conversation Planning",
		Scope:         "Translate affirmed five-body interview evidence into declarations.",
		ClaimLevel:    "self-declared",
		LastValidated: "2026-07-21T17:00:00Z",
	}}}
	normalized := normalizeMintConversationDeclarationsDraft(parsed)
	if len(normalized.Capabilities) != 1 || normalized.Capabilities[0].Capability != "hosted_genesis_conversation_planning" {
		t.Fatalf("expected deterministic human-evidence canonicalization, got %#v", normalized.Capabilities)
	}

	validated, err := hostedgenesis.ValidateAndNormalizeProducedCapabilities(normalized.Capabilities)
	if err != nil {
		t.Fatalf("provider-contract output must survive Host validation: %v", err)
	}
	if len(validated) != 1 {
		t.Fatalf("expected one validated capability, got %#v", validated)
	}
	if err := validated[0].Validate(); err != nil {
		t.Fatalf("validated producer output must be a valid CapabilityV2: %v", err)
	}
}

func TestMintConversationDeclarationsFiveBodyNormalization(t *testing.T) {
	t.Parallel()

	contract := hostedgenesis.FiveBodyDeclarationContract()
	parsed := MintConversationDeclarationsDraft{
		SchemaVersion:   contract.SchemaVersion,
		GuidanceVersion: contract.GuidanceVersion,
		FiveBodies: hostedgenesis.FiveBodyDeclaration{
			Identity:   hostedgenesis.FiveBodySection{Summary: " identity "},
			Philosophy: hostedgenesis.FiveBodySection{Summary: " philosophy "},
			Discipline: hostedgenesis.FiveBodySection{Summary: " discipline "},
			Boundaries: hostedgenesis.FiveBodySection{Summary: " boundaries "},
			Soul: hostedgenesis.FiveBodySoulBody{
				Summary: " soul ",
				Refusals: []hostedgenesis.FiveBodyRefusalRule{{
					Bypass:          " Skip checksum verification ",
					Invariant:       " Consumer release verification remains mandatory ",
					ClosestSafePath: " Run certification against the published artifact ",
				}},
			},
		},
	}
	norm := normalizeMintConversationDeclarationsDraft(parsed)
	if norm.SchemaVersion != contract.SchemaVersion || norm.GuidanceVersion != contract.GuidanceVersion {
		t.Fatalf("expected stable v2 versions, got %#v", norm)
	}
	if norm.FiveBodies.Identity.Summary != "identity" || norm.FiveBodies.Soul.Refusals[0].Bypass != "Skip checksum verification" {
		t.Fatalf("expected normalized five-body fields, got %#v", norm.FiveBodies)
	}
}

func TestMintConversationDeclarationsProviderParityUsesStrictAnthropicTool(t *testing.T) {
	fixture := mintConversationDeclarationDraftFixture()
	openAIContent, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal declaration fixture: %v", err)
	}
	openAIResp, err := json.Marshal(map[string]any{
		"id":      "chatcmpl_test",
		"object":  "chat.completion",
		"created": 123,
		"model":   "gpt-4.1-mini",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": string(openAIContent),
			},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30},
	})
	if err != nil {
		t.Fatalf("marshal openai response: %v", err)
	}
	anthropicResp := anthropicDeclarationToolResponse(t, fixture, "tool_use")

	input := MintConversationDeclarationsInput{
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
		Registration: MintConversationRegistrationContext{
			Domain:               "agent.example",
			LocalID:              "agent",
			AgentID:              "0x" + strings.Repeat("11", 32),
			DeclaredCapabilities: []string{"hosted_genesis_planning"},
		},
		Messages: []MintConversationMessage{
			{Role: "user", Content: "I help with Hosted Genesis Planning and last validated that ability at 2026-07-21T17:00:00Z."},
			{Role: "assistant", Content: "Draft: five bodies, one capability, three refusals."},
		},
	}

	oldOpenAIBase := os.Getenv("OPENAI_BASE_URL")
	oldAnthropicBase := os.Getenv("ANTHROPIC_BASE_URL")
	t.Cleanup(func() {
		_ = os.Setenv("OPENAI_BASE_URL", oldOpenAIBase)
		_ = os.Setenv("ANTHROPIC_BASE_URL", oldAnthropicBase)
		openAIHTTPClient = nil
		anthropicHTTPClient = nil
	})
	_ = os.Setenv("OPENAI_BASE_URL", "https://openai.example.test")
	_ = os.Setenv("ANTHROPIC_BASE_URL", "https://anthropic.example.test")

	var openAIRequest string
	openAIHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			return nil, readErr
		}
		openAIRequest = string(body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(openAIResp)), Request: r}, nil
	})}
	openOut, _, err := MintConversationDeclarationsOpenAI(t.Context(), "sk-test", "openai:gpt-4.1-mini", input)
	if err != nil {
		t.Fatalf("MintConversationDeclarationsOpenAI: %v", err)
	}

	var anthropicRequest string
	anthropicHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			return nil, readErr
		}
		anthropicRequest = string(body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(anthropicResp)), Request: r}, nil
	})}
	anthropicOut, usage, err := MintConversationDeclarationsAnthropic(t.Context(), "sk-ant-test", "anthropic:"+mintConversationTestAnthropicModel, input)
	if err != nil {
		t.Fatalf("MintConversationDeclarationsAnthropic: %v", err)
	}

	assertMintConversationDeclarationDraftsEquivalent(t, openOut, anthropicOut)
	if usage.Provider != testProviderAnthropic || usage.Model != mintConversationTestAnthropicModel || usage.ToolCalls != 1 {
		t.Fatalf("unexpected anthropic usage: %#v", usage)
	}
	for _, want := range []string{`"json_schema"`, `"strict":true`, `"fiveBodies"`, `"capabilities"`, `"transparency"`, `"refusals"`, "Canonical Host capability", "RFC3339"} {
		if !strings.Contains(openAIRequest, want) {
			t.Fatalf("OpenAI strict schema request missing %s: %s", want, openAIRequest)
		}
	}
	for _, want := range []string{`"tool_choice"`, `"soul_five_body_declarations"`, `"strict":true`, `"max_tokens":8192`, `"fiveBodies"`, `"capabilities"`, `"transparency"`, `"refusals"`, "Canonical Host capability", "RFC3339"} {
		if !strings.Contains(anthropicRequest, want) {
			t.Fatalf("Anthropic strict tool request missing %s: %s", want, anthropicRequest)
		}
	}
	assertProviderParityArrayConstraintHandling(t, openAIRequest, anthropicRequest)
}

func assertProviderParityArrayConstraintHandling(t *testing.T, openAIRequest, anthropicRequest string) {
	t.Helper()
	if !strings.Contains(openAIRequest, `"maxItems"`) {
		t.Fatalf("OpenAI strict schema request should keep maxItems: %s", openAIRequest)
	}
	for _, banned := range []string{`"maxItems"`, `"minItems"`} {
		if strings.Contains(anthropicRequest, banned) {
			t.Fatalf("Anthropic strict tool request must strip %s (Anthropic rejects it for custom tools): %s", banned, anthropicRequest)
		}
	}
}

func TestMintConversationDeclarationsAnthropicRejectsTruncatedStopReason(t *testing.T) {
	respBytes, err := json.Marshal(map[string]any{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"model":       mintConversationTestAnthropicModel,
		"stop_reason": "max_tokens",
		"content":     []any{map[string]any{"type": "text", "text": `{"selfDescription":`}},
		"usage":       map[string]any{"input_tokens": 11, "output_tokens": 8192},
	})
	if err != nil {
		t.Fatalf("marshal anthropic truncation response: %v", err)
	}

	oldBase := os.Getenv("ANTHROPIC_BASE_URL")
	t.Cleanup(func() {
		_ = os.Setenv("ANTHROPIC_BASE_URL", oldBase)
		anthropicHTTPClient = nil
	})
	_ = os.Setenv("ANTHROPIC_BASE_URL", "https://anthropic.example.test")
	anthropicHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(respBytes)), Request: r}, nil
	})}

	_, _, err = MintConversationDeclarationsAnthropic(t.Context(), "sk-ant-test", "anthropic:"+mintConversationTestAnthropicModel, MintConversationDeclarationsInput{
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
	})
	if err == nil || !strings.Contains(err.Error(), "response truncated") || strings.Contains(err.Error(), "invalid json output") || strings.Contains(err.Error(), "capabilities") {
		t.Fatalf("expected stable truncation failure before JSON/declaration validation, got %v", err)
	}
}

func anthropicDeclarationToolResponse(t *testing.T, fixture map[string]any, stopReason string) []byte {
	t.Helper()
	respBytes, err := json.Marshal(map[string]any{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"model":       mintConversationTestAnthropicModel,
		"stop_reason": stopReason,
		"content": []any{map[string]any{
			"type":  "tool_use",
			"id":    "toolu_1",
			"name":  "soul_five_body_declarations",
			"input": fixture,
		}},
		"usage": map[string]any{"input_tokens": 11, "cache_creation_input_tokens": 2, "cache_read_input_tokens": 3, "output_tokens": 7},
	})
	if err != nil {
		t.Fatalf("marshal anthropic response: %v", err)
	}
	return respBytes
}

func mintConversationDeclarationDraftFixture() map[string]any {
	section := func(summary string) map[string]any {
		return map[string]any{"summary": summary, "notes": []any{}}
	}
	return map[string]any{
		"schemaVersion":   hostedgenesis.DeclarationSchemaVersionV2,
		"guidanceVersion": hostedgenesis.GuidanceVersionV2,
		"fiveBodies": map[string]any{
			"identity":   section("I help operators prepare hosted genesis declarations."),
			"philosophy": section("Stay scoped, explain uncertainty, prefer bounded authority."),
			"discipline": section("Follow the named cadence and collect evidence before acting."),
			"boundaries": section("I will not publish, sign, or deploy on behalf of the operator."),
			"soul": map[string]any{
				"summary": "Preserve the human publish gate even under schedule pressure.",
				"notes":   []any{},
				"refusals": []any{
					map[string]any{"bypass": "Publish without the human gate", "invariant": "hosted genesis requires explicit human publication", "closestSafePath": "return declaration_ready and wait for finalize authorization"},
					map[string]any{"bypass": "Skip checksum verification for a release", "invariant": "consumer release verification must fail closed", "closestSafePath": "run certification against the exact published artifact"},
					map[string]any{"bypass": "Log a raw instance API key for debugging", "invariant": "raw credentials never enter Host logs", "closestSafePath": "compare the sha256 key hash and log a redacted correlation id"},
				},
			},
		},
		"capabilities": []any{map[string]any{
			"capability":    "Hosted Genesis Planning",
			"scope":         "Draft safe hosted genesis registration declarations.",
			"claimLevel":    "self-declared",
			"lastValidated": "2026-07-21T17:00:00Z",
			"validationRef": "",
			"degradesTo":    "",
		}},
		"transparency": map[string]any{
			"modelProviderUncertainty": "Provider chosen by Host model set.",
			"operationalNotes":         "Generated from a bounded transcript fixture.",
			"selfDeclaredNotice":       "All declarations are self-declared.",
		},
	}
}

func assertMintConversationDeclarationDraftsEquivalent(t *testing.T, openOut MintConversationDeclarationsDraft, anthropicOut MintConversationDeclarationsDraft) {
	t.Helper()
	if openOut.SchemaVersion != hostedgenesis.DeclarationSchemaVersionV2 || anthropicOut.SchemaVersion != hostedgenesis.DeclarationSchemaVersionV2 ||
		openOut.GuidanceVersion != hostedgenesis.GuidanceVersionV2 || anthropicOut.GuidanceVersion != hostedgenesis.GuidanceVersionV2 {
		t.Fatalf("contract version mismatch: openai=%#v anthropic=%#v", openOut, anthropicOut)
	}
	if openOut.FiveBodies.Identity.Summary != anthropicOut.FiveBodies.Identity.Summary || openOut.FiveBodies.Soul.Summary != anthropicOut.FiveBodies.Soul.Summary {
		t.Fatalf("five-body mismatch: openai=%#v anthropic=%#v", openOut.FiveBodies, anthropicOut.FiveBodies)
	}
	if len(openOut.FiveBodies.Soul.Refusals) != 3 || len(anthropicOut.FiveBodies.Soul.Refusals) != 3 {
		t.Fatalf("refusal floor mismatch: openai=%#v anthropic=%#v", openOut.FiveBodies.Soul.Refusals, anthropicOut.FiveBodies.Soul.Refusals)
	}
	if len(openOut.Capabilities) != 1 || len(anthropicOut.Capabilities) != 1 || openOut.Capabilities[0].Capability != "hosted_genesis_planning" || openOut.Capabilities[0].Capability != anthropicOut.Capabilities[0].Capability || anthropicOut.Capabilities[0].ClaimLevel != mintConversationClaimLevelSelfDeclared {
		t.Fatalf("capability mismatch: openai=%#v anthropic=%#v", openOut.Capabilities, anthropicOut.Capabilities)
	}
	if openOut.Transparency["operationalNotes"] != anthropicOut.Transparency["operationalNotes"] {
		t.Fatalf("transparency mismatch: openai=%#v anthropic=%#v", openOut.Transparency, anthropicOut.Transparency)
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
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
		Registration:    MintConversationRegistrationContext{AgentID: "agent_123"},
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

	schema := mintConversationDeclarationsJSONSchemaV2(hostedgenesis.FiveBodyDeclarationContract())
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

	fiveBodies := requireSchemaMap(t, props, "fiveBodies")
	assertSchemaBool(t, fiveBodies, "additionalProperties", false)
}

func TestAnthropicToolInputSchemaStripsUnsupportedConstraints(t *testing.T) {
	t.Parallel()

	contract := hostedgenesis.FiveBodyDeclarationContract()
	for name, schema := range map[string]map[string]any{
		"v2": mintConversationDeclarationsJSONSchemaV2(contract),
	} {
		source, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("%s: marshal source schema: %v", name, err)
		}
		if !strings.Contains(string(source), `"maxItems"`) {
			t.Fatalf("%s: expected source schema to keep maxItems for the OpenAI strict path, got %s", name, source)
		}

		raw, err := json.Marshal(anthropicToolInputSchemaFromJSONSchema(schema))
		if err != nil {
			t.Fatalf("%s: marshal anthropic schema: %v", name, err)
		}
		for _, keyword := range []string{`"maxItems"`, `"minItems"`, `"maxLength"`, `"minLength"`} {
			if strings.Contains(string(raw), keyword) {
				t.Fatalf("%s: expected anthropic schema to strip %s (Anthropic strict custom tools reject it), got %s", name, keyword, raw)
			}
		}
		if !strings.Contains(string(raw), `"additionalProperties":false`) {
			t.Fatalf("%s: expected anthropic schema to keep additionalProperties=false, got %s", name, raw)
		}

		after, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("%s: re-marshal source schema: %v", name, err)
		}
		if string(after) != string(source) {
			t.Fatalf("%s: sanitizing must not mutate the shared provider schema:\nbefore=%s\nafter=%s", name, source, after)
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
