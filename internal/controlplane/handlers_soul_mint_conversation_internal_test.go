package controlplane

import (
	"encoding/json"
	"github.com/equaltoai/lesser-host/internal/soul"
	"testing"
)

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
		return
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

func TestParseAndValidateMintConversationDeclarations_AcceptsHostedContractEmptyArrays(t *testing.T) {
	t.Parallel()

	raw := `{
	  "selfDescription": {"purpose": "A valid purpose string.", "authoredBy": "agent"},
	  "capabilities": [],
	  "boundaries": [],
	  "transparency": {}
	}`

	got, appErr := parseAndValidateMintConversationDeclarations(raw)
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if got.Capabilities == nil {
		t.Fatalf("expected capabilities to remain a concrete empty array")
	}
	if got.Boundaries == nil {
		t.Fatalf("expected boundaries to remain a concrete empty array")
	}
}
