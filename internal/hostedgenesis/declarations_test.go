package hostedgenesis

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/soul"
)

func TestMergeDeclaredCapabilitiesPreservesExtractedAndAddsDeclared(t *testing.T) {
	t.Parallel()

	got := MergeDeclaredCapabilities([]soul.CapabilityV2{
		{Capability: "travel_planning", Scope: "Draft itineraries.", ClaimLevel: "challenge-passed"},
		{Capability: "", Scope: "ignored", ClaimLevel: "self-declared"},
	}, []string{"simulacrum.hosted-first-default", "travel_planning", " SIMULACRUM.HOSTED-FIRST-DEFAULT "})

	if len(got) != 2 {
		t.Fatalf("expected extracted plus one declared fallback capability, got %#v", got)
	}
	if got[0].Capability != "travel_planning" || got[0].ClaimLevel != "challenge-passed" {
		t.Fatalf("expected valid extracted capability preserved, got %#v", got[0])
	}
	if got[1].Capability != "simulacrum.hosted-first-default" || got[1].ClaimLevel != declaredCapabilityClaimLevel || got[1].Scope != declaredCapabilityFallbackScope {
		t.Fatalf("expected normalized declared fallback capability, got %#v", got[1])
	}
}

func TestMergeDeclaredCapabilitiesRejectsInvalidDeclaredNames(t *testing.T) {
	t.Parallel()

	got := MergeDeclaredCapabilities(nil, []string{"", "has spaces", "\t", "valid.capability"})
	if len(got) != 1 || got[0].Capability != "valid.capability" {
		t.Fatalf("expected only valid declared capability, got %#v", got)
	}
}
