package hostedgenesis

import (
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/soul"
)

func TestMergeDeclaredCapabilitiesFiltersPlaceholderAndPreservesExtracted(t *testing.T) {
	t.Parallel()

	got := MergeDeclaredCapabilities([]soul.CapabilityV2{
		{Capability: "travel_planning", Scope: "Draft itineraries.", ClaimLevel: "challenge-passed"},
		{Capability: "", Scope: "ignored", ClaimLevel: "self-declared"},
	}, []string{"simulacrum.hosted-first-default", "travel_planning", " SIMULACRUM.HOSTED-FIRST-DEFAULT "})

	if len(got) != 1 {
		t.Fatalf("expected placeholder-free extracted capability, got %#v", got)
	}
	if got[0].Capability != "travel_planning" || got[0].ClaimLevel != "challenge-passed" {
		t.Fatalf("expected valid extracted capability preserved, got %#v", got[0])
	}
}

func TestMergeDeclaredCapabilitiesRejectsInvalidDeclaredNames(t *testing.T) {
	t.Parallel()

	got := MergeDeclaredCapabilities(nil, []string{"", "has spaces", "\t", "valid.capability"})
	if len(got) != 1 || got[0].Capability != "valid.capability" {
		t.Fatalf("expected only valid declared capability, got %#v", got)
	}
}

func TestValidateAndNormalizeProducedCapabilitiesAllowsEmptyAndSanitizesInvalid(t *testing.T) {
	if got, err := ValidateAndNormalizeProducedCapabilities(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("expected empty produced capabilities to be valid, got %#v %v", got, err)
	}
	if got, err := ValidateAndNormalizeProducedCapabilities([]soul.CapabilityV2{{Capability: "simulacrum.hosted-first-default", Scope: "retired", ClaimLevel: "self-declared"}}); err != nil || len(got) != 0 {
		t.Fatalf("expected retired placeholder to be filtered, got %#v %v", got, err)
	}
	validated, err := ValidateAndNormalizeProducedCapabilities([]soul.CapabilityV2{
		{Capability: "Hosted Genesis Planning", Scope: "Plan bounded Hosted Genesis conversations.", ClaimLevel: "self-declared", LastValidated: "2026-07-21T17:00:00Z"},
		{Capability: "hosted_genesis_planning", Scope: "Duplicate evidence.", ClaimLevel: "self-declared"},
	})
	if err != nil || len(validated) != 1 || validated[0].Capability != "hosted_genesis_planning" {
		t.Fatalf("expected deterministic canonicalization and deduplication, got %#v %v", validated, err)
	}
	if err := validated[0].Validate(); err != nil {
		t.Fatalf("normalized output must satisfy CapabilityV2: %v", err)
	}
	if _, err := ValidateAndNormalizeProducedCapabilities([]soul.CapabilityV2{{
		Capability: "multilingual_scope",
		Scope:      strings.Repeat("é", MaxProducedCapabilityScopeLength),
		ClaimLevel: "self-declared",
	}}); err != nil {
		t.Fatalf("JSON Schema length is measured in characters, not UTF-8 bytes: %v", err)
	}

	privateValue := "private-transcript-provider-output"
	tests := []struct {
		name string
		caps []soul.CapabilityV2
		code DeclarationValidationCode
	}{
		{name: "too many", caps: makeProducedCapabilities(MaxProducedCapabilities + 1), code: DeclarationCodeCapabilitiesTooMany},
		{name: "identifier", caps: []soul.CapabilityV2{{Capability: privateValue + ":invalid", Scope: "scope", ClaimLevel: "self-declared"}}, code: DeclarationCodeCapabilityIdentifier},
		{name: "scope", caps: []soul.CapabilityV2{{Capability: "planning", Scope: " \t ", ClaimLevel: "self-declared", ValidationRef: privateValue}}, code: DeclarationCodeCapabilityScope},
		{name: "claim level", caps: []soul.CapabilityV2{{Capability: "planning", Scope: "scope", ClaimLevel: privateValue}}, code: DeclarationCodeCapabilityClaimLevel},
		{name: "last validated", caps: []soul.CapabilityV2{{Capability: "planning", Scope: "scope", ClaimLevel: "self-declared", LastValidated: privateValue}}, code: DeclarationCodeCapabilityLastValidated},
		{name: "validation ref", caps: []soul.CapabilityV2{{Capability: "planning", Scope: "scope", ClaimLevel: "self-declared", ValidationRef: strings.Repeat(privateValue, 20)}}, code: DeclarationCodeCapabilityValidationRef},
		{name: "degrades to", caps: []soul.CapabilityV2{{Capability: "planning", Scope: "scope", ClaimLevel: "self-declared", DegradesTo: strings.Repeat(privateValue, 20)}}, code: DeclarationCodeCapabilityDegradesTo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotErr := ValidateAndNormalizeProducedCapabilities(tt.caps)
			if got := DeclarationValidationCodeFromError(gotErr); got != tt.code {
				t.Fatalf("expected %q, got %q (%v)", tt.code, got, gotErr)
			}
			if strings.Contains(gotErr.Error(), privateValue) {
				t.Fatalf("bounded failure leaked raw provider content: %q", gotErr)
			}
		})
	}
}

func makeProducedCapabilities(count int) []soul.CapabilityV2 {
	out := make([]soul.CapabilityV2, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, soul.CapabilityV2{Capability: "planning", Scope: "scope", ClaimLevel: "self-declared"})
	}
	return out
}
