package ai

import (
	"strings"
	"testing"
)

func TestExtractClaimsDeterministicV1_TrimsDedupesAndLimits(t *testing.T) {
	t.Parallel()

	if got := ExtractClaimsDeterministicV1("   ", 10); len(got) != 0 {
		t.Fatalf("expected empty, got %#v", got)
	}

	long := strings.Repeat("a", 300)
	text := "  One  claim.  One claim.  " + long + ".  Two\tclaim!  "

	got := ExtractClaimsDeterministicV1(text, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 claims, got %#v", got)
	}
	if got[0] != "One claim" {
		t.Fatalf("expected normalized claim %q, got %q", "One claim", got[0])
	}
	if len(got[1]) != 240 {
		t.Fatalf("expected long claim trimmed to 240 chars, got len=%d", len(got[1]))
	}
	if got[2] != "Two claim!" {
		t.Fatalf("expected normalized claim %q, got %q", "Two claim!", got[2])
	}

	got = ExtractClaimsDeterministicV1(text, 1)
	if len(got) != 1 {
		t.Fatalf("expected maxClaims enforced, got %#v", got)
	}
}

func TestClassifyClaimDeterministic(t *testing.T) {
	t.Parallel()

	if got := classifyClaimDeterministic(""); got != "unclear" {
		t.Fatalf("expected unclear, got %q", got)
	}
	if got := classifyClaimDeterministic("I think this is best"); got != "opinion" {
		t.Fatalf("expected opinion, got %q", got)
	}
	if got := classifyClaimDeterministic("GDP is 3.2%"); got != "checkable" {
		t.Fatalf("expected checkable, got %q", got)
	}
	if got := classifyClaimDeterministic("hello world"); got != "unclear" {
		t.Fatalf("expected unclear, got %q", got)
	}
}

func TestClaimVerifyDeterministicV1_UsesProvidedClaims(t *testing.T) {
	t.Parallel()

	out := ClaimVerifyDeterministicV1(ClaimVerifyInputsV1{
		Claims: []string{"  GDP is 3.2% ", "", "I think it's best"},
	})

	if out.Kind != "claim_verify" || out.Version != "v1" {
		t.Fatalf("unexpected envelope: %#v", out)
	}
	if len(out.Claims) != 2 {
		t.Fatalf("expected 2 claims, got %#v", out.Claims)
	}

	if out.Claims[0].ClaimID != "c1" || out.Claims[0].Classification != "checkable" {
		t.Fatalf("unexpected claim[0]: %#v", out.Claims[0])
	}
	if out.Claims[1].ClaimID != "c2" || out.Claims[1].Classification != "opinion" {
		t.Fatalf("unexpected claim[1]: %#v", out.Claims[1])
	}
}

func TestClaimVerifyDeterministicV1_ExtractsClaimsFromText(t *testing.T) {
	t.Parallel()

	out := ClaimVerifyDeterministicV1(ClaimVerifyInputsV1{
		Text: "One claim. Two claim.  ",
	})
	if len(out.Claims) != 2 {
		t.Fatalf("expected 2 extracted claims, got %#v", out.Claims)
	}
	if out.Claims[0].Text != "One claim" || out.Claims[1].Text != "Two claim." {
		t.Fatalf("unexpected extracted claims: %#v", out.Claims)
	}
}
