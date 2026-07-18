package hostedgenesis

import (
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
	if got, err := ValidateAndNormalizeProducedCapabilities(nil); err != nil || len(got) != 0 {
		t.Fatalf("expected empty produced capabilities to be valid, got %#v %v", got, err)
	}
	if got, err := ValidateAndNormalizeProducedCapabilities([]soul.CapabilityV2{{Capability: "simulacrum.hosted-first-default", Scope: "retired", ClaimLevel: "self-declared"}}); err != nil || len(got) != 0 {
		t.Fatalf("expected retired placeholder to be filtered, got %#v %v", got, err)
	}
	if _, err := ValidateAndNormalizeProducedCapabilities([]soul.CapabilityV2{{Capability: "planning", Scope: "scope", ClaimLevel: "not-a-claim-level"}}); DeclarationValidationCodeFromError(err) != DeclarationCodeCapabilitiesBad {
		t.Fatalf("expected stable capabilities.invalid code, got %v", err)
	}
}
