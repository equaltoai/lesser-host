package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/soul"
)

func TestBuildMintConversationProducedDeclarations_FillsBoundaryMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	modelSet := "openai:gpt-5.4"

	draft := llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{
			Purpose:    "Provide customer support for a small business.",
			AuthoredBy: "agent",
		},
		Capabilities: []soul.CapabilityV2{
			{
				Capability: "customer-support",
				Scope:      "Answer FAQs and draft responses; escalate refunds.",
				ClaimLevel: "",
			},
		},
		Boundaries: []llm.MintConversationBoundaryDraft{
			{
				Category:  "refusal",
				Statement: "I will not provide legal advice.",
			},
		},
		Transparency: map[string]any{"note": "minted via conversation"},
	}

	decl, appErr := buildMintConversationProducedDeclarations(draft, now, modelSet)
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if strings.TrimSpace(decl.SelfDescription.MintingModel) != modelSet {
		t.Fatalf("expected mintingModel %q, got %q", modelSet, decl.SelfDescription.MintingModel)
	}
	if len(decl.Boundaries) != 1 {
		t.Fatalf("expected 1 boundary, got %d", len(decl.Boundaries))
	}
	if decl.Boundaries[0].AddedInVersion != "1" {
		t.Fatalf("expected addedInVersion=1, got %q", decl.Boundaries[0].AddedInVersion)
	}
	if decl.Boundaries[0].Signature != "0x00" {
		t.Fatalf("expected placeholder signature, got %q", decl.Boundaries[0].Signature)
	}
}

func TestParseAndValidateMintConversationDeclarations_RejectsInvalid(t *testing.T) {
	t.Parallel()

	raw := `{
  "selfDescription": {"purpose": "A valid purpose string.", "authoredBy": "agent"},
  "capabilities": [{"capability":"x","scope":"y","claimLevel":"self-declared"}],
  "boundaries": [{"id":"b1","category":"refusal","statement":"nope","addedAt":"2026-03-03T00:00:00Z","addedInVersion":"1","signature":""}],
  "transparency": {}
}`

	_, appErr := parseAndValidateMintConversationDeclarations(raw)
	if appErr == nil {
		t.Fatalf("expected error")
	}
	if appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected %s, got %s", appErrCodeBadRequest, appErr.Code)
	}
}

func TestParseAndValidateMintConversationDeclarations_AcceptsValid(t *testing.T) {
	t.Parallel()

	obj := soulMintConversationProducedDeclarations{
		SelfDescription: soul.SelfDescriptionV2{
			Purpose:    "A valid purpose string.",
			AuthoredBy: "agent",
		},
		Capabilities: []soul.CapabilityV2{
			{Capability: "x", Scope: "y", ClaimLevel: "self-declared"},
		},
		Boundaries: []soul.BoundaryV2{
			{ID: "b1", Category: "refusal", Statement: "nope", AddedAt: "2026-03-03T00:00:00Z", AddedInVersion: "1", Signature: "0x00"},
		},
		Transparency: map[string]any{},
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, appErr := parseAndValidateMintConversationDeclarations(string(b))
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
}
