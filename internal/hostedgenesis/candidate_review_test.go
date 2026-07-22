package hostedgenesis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/equaltoai/lesser-host/internal/soul"
)

func TestDeclarationCandidateOwnerReviewLosslesslyExposesExactCanonicalCandidate(t *testing.T) {
	candidate := completeSentinelDeclarationCandidate(t)
	reviewText := candidate.Review.ReviewText

	recovered, err := RecoverDeclarationOwnerReviewCanonicalJSON(reviewText)
	if err != nil {
		t.Fatalf("recover exact canonical review payload: %v", err)
	}
	if recovered != candidate.CanonicalJSON {
		t.Fatalf("review canonical payload diverged\n got: %s\nwant: %s", recovered, candidate.CanonicalJSON)
	}
	if utf8.RuneCountInString(reviewText) > MaxDeclarationOwnerReviewRunes {
		t.Fatalf("review exceeds documented API limit: got=%d max=%d", utf8.RuneCountInString(reviewText), MaxDeclarationOwnerReviewRunes)
	}

	var canonical canonicalProducedDeclarations
	if err := json.Unmarshal([]byte(recovered), &canonical); err != nil {
		t.Fatalf("decode recovered canonical candidate: %v", err)
	}
	assertReviewContainsSentinelSemantics(t, reviewText, canonical)
}

func TestDeclarationCandidatePreviouslyHiddenSemanticEditsInvalidateStructuralAffirmation(t *testing.T) {
	original := completeSentinelDeclarationCandidate(t)
	oldAction := DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: original.Revision,
		CandidateHash: original.CandidateHash, ReviewHash: original.Review.ReviewHash,
	}

	tests := []struct {
		name    string
		section DeclarationSection
		mutate  func(*DeclarationCandidate)
	}{
		{"identity note", DeclarationSectionIdentity, func(c *DeclarationCandidate) { c.FiveBodies.Identity.Notes[0] = "identity-note-changed-sentinel" }},
		{"philosophy note", DeclarationSectionPhilosophy, func(c *DeclarationCandidate) { c.FiveBodies.Philosophy.Notes[0] = "philosophy-note-changed-sentinel" }},
		{"discipline note", DeclarationSectionDiscipline, func(c *DeclarationCandidate) { c.FiveBodies.Discipline.Notes[0] = "discipline-note-changed-sentinel" }},
		{"boundaries note", DeclarationSectionBoundaries, func(c *DeclarationCandidate) { c.FiveBodies.Boundaries.Notes[0] = "boundaries-note-changed-sentinel" }},
		{"soul note", DeclarationSectionSoul, func(c *DeclarationCandidate) { c.FiveBodies.Soul.Notes[0] = "soul-note-changed-sentinel" }},
		{"self description purpose", DeclarationSectionSoul, func(c *DeclarationCandidate) { c.SelfDescription.Purpose = "self-description-purpose-changed-sentinel" }},
		{"self description constraints", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.SelfDescription.Constraints = "self-description-constraints-changed-sentinel"
		}},
		{"self description commitments", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.SelfDescription.Commitments = "self-description-commitments-changed-sentinel"
		}},
		{"self description limitations", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.SelfDescription.Limitations = "self-description-limitations-changed-sentinel"
		}},
		{"capability identifier", DeclarationSectionSoul, func(c *DeclarationCandidate) { c.Capabilities[0].Capability = "capability_changed_sentinel" }},
		{"capability scope", DeclarationSectionSoul, func(c *DeclarationCandidate) { c.Capabilities[0].Scope = "capability-scope-changed-sentinel" }},
		{"capability constraints", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.Capabilities[0].Constraints["constraintSentinel"] = "capability-constraint-changed-sentinel"
		}},
		{"capability last validated", DeclarationSectionSoul, func(c *DeclarationCandidate) { c.Capabilities[0].LastValidated = "2043-02-03T04:05:06Z" }},
		{"capability validation ref", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.Capabilities[0].ValidationRef = "capability-validation-ref-changed-sentinel"
		}},
		{"capability degrades to", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.Capabilities[0].DegradesTo = "capability-degrades-to-changed-sentinel"
		}},
		{"transparency provider uncertainty", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.Transparency.ModelProviderUncertainty = "transparency-provider-changed-sentinel"
		}},
		{"transparency operational notes", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.Transparency.OperationalNotes = "transparency-operational-changed-sentinel"
		}},
		{"transparency self declared notice", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.Transparency.SelfDeclaredNotice = "transparency-notice-changed-sentinel"
		}},
		{"refusal-derived boundaries", DeclarationSectionSoul, func(c *DeclarationCandidate) {
			c.FiveBodies.Soul.Refusals[0].Invariant = "refusal-invariant-changed-sentinel remains exact"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := rerenderCandidateSemanticChange(t, original, tt.section, tt.mutate)
			if changed.CandidateHash == original.CandidateHash {
				t.Fatal("semantic edit did not change CandidateHash")
			}
			if changed.Review.ReviewText == original.Review.ReviewText || changed.Review.ReviewHash == original.Review.ReviewHash {
				t.Fatal("semantic edit did not change ReviewText and ReviewHash")
			}
			if _, err := ApplyDeclarationCandidateAction(changed, oldAction, changed.SourceTurnID, time.Unix(500, 0)); err == nil {
				t.Fatal("old structural affirmation accepted changed semantic candidate")
			}
		})
	}
}

func TestDeclarationCandidateOwnerReviewOverflowStaysInSectionRepairLoop(t *testing.T) {
	candidate := testDeclarationCandidate(t)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolIdentityPut, "identity", declarationSectionPayload{Section: testFiveBody().Identity}, 1)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolPhilosophyPut, "philosophy", declarationSectionPayload{Section: testFiveBody().Philosophy}, 2)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolDisciplinePut, "discipline", declarationSectionPayload{Section: testFiveBody().Discipline}, 3)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolBoundariesPut, "boundaries", declarationSectionPayload{Section: testFiveBody().Boundaries}, 4)

	payload := testSoulPayload()
	payload.SelfDescription.Purpose = strings.Repeat("p", MaxDeclarationOwnerReviewRunes)
	revision := candidate.Revision
	payload.CandidateRevision = &revision
	payload.CandidateHash = candidate.CandidateHash
	next, result, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
		ToolName: DeclarationToolSoulPut, ToolCallID: "soul-review-overflow", SourceTurnID: candidate.SourceTurnID,
		ExpectedRevision: candidate.Revision, ExpectedHash: candidate.CandidateHash, Payload: mustJSON(t, payload),
	}, time.Unix(105, 0))
	if err != nil || next != nil || result.Accepted || len(result.Errors) != 1 {
		t.Fatalf("review overflow left bounded repair loop: next=%#v result=%#v err=%v", next, result, err)
	}
	if got := result.Errors[0]; got.Section != DeclarationSectionSoul || got.Path != "candidate.review_text" || got.Code != DeclarationCodeInvalid {
		t.Fatalf("unexpected review overflow repair issue: %#v", got)
	}
}

func completeSentinelDeclarationCandidate(t *testing.T) *DeclarationCandidate {
	t.Helper()
	candidate, err := NewDeclarationCandidate(DeclarationCandidateBinding{
		InstanceSlug: "acme", RegistrationID: "reg-sentinel", AgentID: "0xabc", ConversationID: "conv-sentinel",
		SourceTurnID: "turn-sentinel", Model: "openai:model-self-description-sentinel",
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	bodies := sentinelFiveBody()
	candidate = acceptCandidateSection(t, candidate, DeclarationToolIdentityPut, "sentinel-identity", declarationSectionPayload{Section: bodies.Identity}, 1)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolPhilosophyPut, "sentinel-philosophy", declarationSectionPayload{Section: bodies.Philosophy}, 2)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolDisciplinePut, "sentinel-discipline", declarationSectionPayload{Section: bodies.Discipline}, 3)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolBoundariesPut, "sentinel-boundaries", declarationSectionPayload{Section: bodies.Boundaries}, 4)
	return acceptCandidateSection(t, candidate, DeclarationToolSoulPut, "sentinel-soul", declarationSoulPayload{
		Section: bodies.Soul,
		SelfDescription: soul.SelfDescriptionV2{
			Purpose: "self-description-purpose-sentinel", Constraints: "self-description-constraints-sentinel",
			Commitments: "self-description-commitments-sentinel", Limitations: "self-description-limitations-sentinel",
			AuthoredBy: "agent", MintingModel: "openai:model-self-description-sentinel",
		},
		Capabilities: []soul.CapabilityV2{{
			Capability: "capability_sentinel", Scope: "capability-scope-sentinel",
			Constraints: map[string]any{"constraintSentinel": "capability-constraint-value-sentinel"},
			ClaimLevel:  "self-declared", LastValidated: "2042-01-02T03:04:05Z",
			ValidationRef: "capability-validation-ref-sentinel", DegradesTo: "capability-degrades-to-sentinel",
		}},
		Transparency: DeclarationTransparency{
			ModelProviderUncertainty: "transparency-provider-uncertainty-sentinel",
			OperationalNotes:         "transparency-operational-notes-sentinel",
			SelfDeclaredNotice:       "transparency-self-declared-notice-sentinel",
		},
	}, 5)
}

func sentinelFiveBody() FiveBodyDeclaration {
	return FiveBodyDeclaration{
		Identity:   FiveBodySection{Summary: "identity-summary-sentinel remains concrete", Notes: []string{"identity-note-sentinel"}},
		Philosophy: FiveBodySection{Summary: "philosophy-summary-sentinel prefers exact review", Notes: []string{"philosophy-note-sentinel"}},
		Discipline: FiveBodySection{Summary: "discipline-summary-sentinel follows durable checkpoints", Notes: []string{"discipline-note-sentinel"}},
		Boundaries: FiveBodySection{Summary: "boundaries-summary-sentinel stays tenant bound", Notes: []string{"boundaries-note-sentinel"}},
		Soul: FiveBodySoulBody{
			Summary: "soul-summary-sentinel binds exact publication", Notes: []string{"soul-note-sentinel"},
			Refusals: []FiveBodyRefusalRule{
				{Bypass: "refusal-one-bypass-sentinel action", Invariant: "refusal-one-invariant-sentinel remains exact", ClosestSafePath: "refusal-one-safe-path-sentinel review"},
				{Bypass: "refusal-two-bypass-sentinel action", Invariant: "refusal-two-invariant-sentinel stays tenant bound", ClosestSafePath: "refusal-two-safe-path-sentinel restart"},
				{Bypass: "refusal-three-bypass-sentinel action", Invariant: "refusal-three-invariant-sentinel stays provider free", ClosestSafePath: "refusal-three-safe-path-sentinel publish"},
			},
		},
	}
}

func rerenderCandidateSemanticChange(t *testing.T, original *DeclarationCandidate, section DeclarationSection, mutate func(*DeclarationCandidate)) *DeclarationCandidate {
	t.Helper()
	changed := original.Clone()
	mutate(changed)
	sectionBytes, err := changed.sectionBytes(section)
	if err != nil {
		t.Fatal(err)
	}
	changed.SectionHashes[string(section)] = hashBytes(sectionBytes)
	if refreshErr := changed.refreshCanonical(); refreshErr != nil {
		t.Fatal(refreshErr)
	}
	review, err := RenderDeclarationOwnerReview(changed, changed.SourceTurnID, changed.Review.ReviewedAt)
	if err != nil {
		t.Fatal(err)
	}
	changed.Review = &review
	if err := changed.Validate(); err != nil {
		t.Fatalf("changed candidate is invalid: %v", err)
	}
	return changed
}

func assertReviewContainsSentinelSemantics(t *testing.T, reviewText string, canonical canonicalProducedDeclarations) {
	t.Helper()
	visible := []string{
		canonical.SchemaVersion, canonical.GuidanceVersion,
		canonical.SelfDescription.Purpose, canonical.SelfDescription.Constraints, canonical.SelfDescription.Commitments,
		canonical.SelfDescription.Limitations, canonical.SelfDescription.AuthoredBy, canonical.SelfDescription.MintingModel,
		canonical.Transparency.ModelProviderUncertainty, canonical.Transparency.OperationalNotes, canonical.Transparency.SelfDeclaredNotice,
	}
	for _, section := range []FiveBodySection{
		canonical.FiveBodies.Identity, canonical.FiveBodies.Philosophy, canonical.FiveBodies.Discipline, canonical.FiveBodies.Boundaries,
		{Summary: canonical.FiveBodies.Soul.Summary, Notes: canonical.FiveBodies.Soul.Notes},
	} {
		visible = append(visible, section.Summary)
		visible = append(visible, section.Notes...)
	}
	for _, capability := range canonical.Capabilities {
		visible = append(visible, capability.Capability, capability.Scope, capability.ClaimLevel, capability.LastValidated, capability.ValidationRef, capability.DegradesTo)
		for key, value := range capability.Constraints {
			visible = append(visible, key)
			if text, ok := value.(string); ok {
				visible = append(visible, text)
			}
		}
	}
	for _, refusal := range canonical.FiveBodies.Soul.Refusals {
		visible = append(visible, refusal.Bypass, refusal.Invariant, refusal.ClosestSafePath)
	}
	for _, boundary := range canonical.Boundaries {
		visible = append(visible, boundary.ID, boundary.Category, boundary.Statement, boundary.Rationale, boundary.AddedAt, boundary.AddedInVersion, boundary.Signature)
	}
	if canonical.AdversarialReview == nil {
		t.Fatal("canonical adversarial review is missing")
	}
	visible = append(visible, canonical.AdversarialReview.Version, canonical.AdversarialReview.Reviewer,
		canonical.AdversarialReview.Result, canonical.AdversarialReview.Report)
	for _, finding := range canonical.AdversarialReview.Findings {
		visible = append(visible, finding.Finding, finding.Refutation, finding.Report)
	}
	for _, value := range visible {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(reviewText, string(encoded)) {
			t.Fatalf("hash-covered semantic value is not visible in review: %q", value)
		}
	}
}

func TestRecoverDeclarationOwnerReviewCanonicalJSONRejectsMalformedEnvelope(t *testing.T) {
	for _, reviewText := range []string{"", "Hosted Genesis owner review", "Canonical JSON byte length: nope"} {
		if _, err := RecoverDeclarationOwnerReviewCanonicalJSON(reviewText); err == nil {
			t.Fatalf("malformed review envelope recovered: %q", reviewText)
		}
	}
}
