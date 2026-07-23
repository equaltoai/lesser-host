package hostedgenesis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/soul"
)

func TestDeclarationCandidateMalformedSectionCanBeRevisedInPlace(t *testing.T) {
	candidate := testDeclarationCandidate(t)
	bad := applyCandidatePayload(t, candidate, DeclarationToolIdentityPut, "call-identity-bad", declarationSectionPayload{}, time.Unix(101, 0))
	if bad.next != nil || bad.result.Accepted || len(bad.result.Errors) != 1 {
		t.Fatalf("expected a bounded rejection without mutation, got %#v", bad)
	}
	if got := bad.result.Errors[0]; got.Section != DeclarationSectionIdentity || got.Path != "fiveBodies.identity.summary" || got.Code != DeclarationCodeFiveBodyIdentity {
		t.Fatalf("unexpected actionable error: %#v", got)
	}
	if candidate.Revision != 0 || candidate.CurrentSection != DeclarationSectionIdentity {
		t.Fatalf("rejection mutated candidate: %#v", candidate)
	}

	good := applyCandidatePayload(t, candidate, DeclarationToolIdentityPut, "call-identity-good", declarationSectionPayload{
		Section: FiveBodySection{Summary: "I am the tenant-bound Hosted Genesis conversation actor."},
	}, time.Unix(102, 0))
	if !good.result.Accepted || good.next == nil || good.next.Revision != 1 || good.next.CurrentSection != DeclarationSectionPhilosophy {
		t.Fatalf("same-section revision did not advance: %#v", good)
	}
}

func TestDeclarationCandidateToolPayloadBindingFailsClosed(t *testing.T) {
	candidate := testDeclarationCandidate(t)
	revision := candidate.Revision
	tests := []struct {
		name    string
		payload json.RawMessage
		path    string
	}{
		{
			name:    "missing revision",
			payload: mustJSON(t, map[string]any{"candidateHash": candidate.CandidateHash, "section": testFiveBody().Identity}),
			path:    "candidate.revision",
		},
		{
			name: "mismatched hash",
			payload: mustJSON(t, declarationSectionPayload{CandidateRevision: &revision,
				CandidateHash: "sha256:" + strings.Repeat("f", 64), Section: testFiveBody().Identity}),
			path: "candidate.hash",
		},
		{
			name: "unknown field",
			payload: mustJSON(t, map[string]any{"candidateRevision": revision, "candidateHash": candidate.CandidateHash,
				"section": testFiveBody().Identity, "transcript": "must not enter durable state"}),
			path: "fiveBodies.identity",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, result, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
				ToolName: DeclarationToolIdentityPut, ToolCallID: "binding-" + tt.name,
				ExpectedRevision: candidate.Revision, ExpectedHash: candidate.CandidateHash,
				SourceTurnID: candidate.SourceTurnID, Payload: tt.payload,
			}, time.Unix(101, 0))
			if err != nil || next != nil || result.Accepted || len(result.Errors) != 1 || result.Errors[0].Path != tt.path {
				t.Fatalf("payload binding did not fail closed: next=%#v result=%#v err=%v", next, result, err)
			}
		})
	}
}

func TestDeclarationCandidateFiveToolsIdempotencyAndGuards(t *testing.T) {
	candidate := testDeclarationCandidate(t)
	initialHash := candidate.CandidateHash
	assertCandidateRejectsCrossTurnTool(t, candidate)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolIdentityPut, "identity", declarationSectionPayload{Section: testFiveBody().Identity}, 1)
	assertCandidateExactToolReplayIsIdempotent(t, candidate, initialHash)
	assertCandidateRejectsStaleToolBindings(t, candidate)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolPhilosophyPut, "philosophy", declarationSectionPayload{Section: testFiveBody().Philosophy}, 2)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolDisciplinePut, "discipline", declarationSectionPayload{Section: testFiveBody().Discipline}, 3)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolBoundariesPut, "boundaries", declarationSectionPayload{Section: testFiveBody().Boundaries}, 4)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolSoulPut, "soul", testSoulPayload(), 5)
	assertDeclarationCandidateFiveSectionsComplete(t, candidate)
}

func assertCandidateRejectsCrossTurnTool(t *testing.T, candidate *DeclarationCandidate) {
	t.Helper()
	_, result, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
		ToolName: DeclarationToolIdentityPut, ToolCallID: "identity-wrong-turn", SourceTurnID: "turn-other",
		ExpectedRevision: candidate.Revision, ExpectedHash: candidate.CandidateHash, Payload: mustJSON(t, declarationSectionPayload{Section: testFiveBody().Identity}),
	}, time.Unix(109, 0))
	if err != nil || result.Accepted || len(result.Errors) != 1 || result.Errors[0].Path != "candidate.source_turn_id" {
		t.Fatalf("cross-turn tool call did not fail closed: %#v err=%v", result, err)
	}
}

func assertCandidateExactToolReplayIsIdempotent(t *testing.T, candidate *DeclarationCandidate, initialHash string) {
	t.Helper()
	initialRevision := int64(0)
	payload, _ := json.Marshal(declarationSectionPayload{
		CandidateRevision: &initialRevision,
		CandidateHash:     initialHash,
		Section:           testFiveBody().Identity,
	})
	next, result, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
		ToolName: DeclarationToolIdentityPut, ToolCallID: "identity", SourceTurnID: candidate.SourceTurnID,
		ExpectedRevision: 0, ExpectedHash: "ignored-on-exact-replay", Payload: payload,
	}, time.Unix(111, 0))
	if err != nil || next == nil || !result.Accepted || !result.Idempotent || next.Revision != 1 {
		t.Fatalf("exact duplicate was not idempotent: next=%#v result=%#v err=%v", next, result, err)
	}
}

func assertCandidateRejectsStaleToolBindings(t *testing.T, candidate *DeclarationCandidate) {
	t.Helper()
	_, stale, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
		ToolName: DeclarationToolPhilosophyPut, ToolCallID: "philosophy-stale", SourceTurnID: candidate.SourceTurnID,
		ExpectedRevision: 0, ExpectedHash: candidate.CandidateHash, Payload: mustJSON(t, declarationSectionPayload{Section: testFiveBody().Philosophy}),
	}, time.Unix(112, 0))
	if err != nil || stale.Accepted || len(stale.Errors) != 1 || stale.Errors[0].Path != "candidate.revision" {
		t.Fatalf("stale revision did not fail closed: %#v err=%v", stale, err)
	}
	_, staleHash, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
		ToolName: DeclarationToolPhilosophyPut, ToolCallID: "philosophy-stale-hash", SourceTurnID: candidate.SourceTurnID,
		ExpectedRevision: candidate.Revision, ExpectedHash: "sha256:" + strings.Repeat("0", 64), Payload: mustJSON(t, declarationSectionPayload{Section: testFiveBody().Philosophy}),
	}, time.Unix(113, 0))
	if err != nil || staleHash.Accepted || len(staleHash.Errors) != 1 || staleHash.Errors[0].Path != "candidate.hash" {
		t.Fatalf("stale hash did not fail closed: %#v err=%v", staleHash, err)
	}
}

func assertDeclarationCandidateFiveSectionsComplete(t *testing.T, candidate *DeclarationCandidate) {
	t.Helper()
	if candidate.Phase != DeclarationCandidatePhaseReview || candidate.Review == nil || len(candidate.CompletedSections) != 5 || len(candidate.SectionHashes) != 5 {
		t.Fatalf("all five tools did not produce accepted checkpoints: %#v", candidate)
	}
	if err := ValidateDeclarationCandidateComplete(candidate); err != nil {
		t.Fatalf("complete candidate invalid: %v", err)
	}
}

func TestDeclarationCandidateReviewAffirmationAndDeterministicBytes(t *testing.T) {
	candidate := completeTestDeclarationCandidate(t)
	beforeJSON, beforeHash, beforeReview := candidate.CanonicalJSON, candidate.CandidateHash, candidate.Review.ReviewHash
	assertCandidateRejectsStaleAffirmation(t, candidate)
	affirmed := affirmExactReviewedCandidate(t, candidate)
	if affirmed.CanonicalJSON != beforeJSON || affirmed.CandidateHash != beforeHash || affirmed.Review.ReviewHash != beforeReview {
		t.Fatal("affirmation changed the exact reviewed candidate")
	}
	assertCandidateEditInvalidatesReview(t, candidate)
	assertCandidateFinalizationIsDeterministic(t, affirmed, beforeJSON, beforeHash)
}

func assertCandidateRejectsStaleAffirmation(t *testing.T, candidate *DeclarationCandidate) {
	t.Helper()
	if _, err := ApplyDeclarationCandidateAction(candidate, DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: candidate.Revision - 1, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
	}, candidate.SourceTurnID, time.Unix(300, 0)); err == nil {
		t.Fatal("expected stale revision affirmation to fail")
	}
	if _, err := ApplyDeclarationCandidateAction(candidate, DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: "sha256:" + strings.Repeat("f", 64),
	}, candidate.SourceTurnID, time.Unix(300, 0)); err == nil {
		t.Fatal("expected mismatched review affirmation to fail")
	}
}

func affirmExactReviewedCandidate(t *testing.T, candidate *DeclarationCandidate) *DeclarationCandidate {
	t.Helper()
	affirmed, err := ApplyDeclarationCandidateAction(candidate, DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
	}, candidate.SourceTurnID, time.Unix(300, 0))
	if err != nil || affirmed.Phase != DeclarationCandidatePhaseAffirmed || affirmed.Affirmation == nil {
		t.Fatalf("matching structural affirmation rejected: %#v err=%v", affirmed, err)
	}
	return affirmed
}

func assertCandidateEditInvalidatesReview(t *testing.T, candidate *DeclarationCandidate) {
	t.Helper()
	edited, err := ApplyDeclarationCandidateAction(candidate, DeclarationCandidateAction{
		Action: "edit", Section: DeclarationSectionPhilosophy, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
	}, candidate.SourceTurnID, time.Unix(301, 0))
	if err != nil || edited.Phase != DeclarationCandidatePhaseSection || edited.CurrentSection != DeclarationSectionPhilosophy || edited.Review != nil || edited.Affirmation != nil {
		t.Fatalf("edit did not invalidate review/affirmation: %#v err=%v", edited, err)
	}
}

func assertCandidateFinalizationIsDeterministic(t *testing.T, affirmed *DeclarationCandidate, beforeJSON string, beforeHash string) {
	t.Helper()
	clone := affirmed.Clone()
	if clone.CanonicalJSON != affirmed.CanonicalJSON || clone.CandidateHash != affirmed.CandidateHash {
		t.Fatal("process-recovery clone changed finalization bytes")
	}
	boundariesA := FiveBodyBoundariesDeterministic(affirmed.FiveBodies.Soul.Refusals, affirmed.EstablishedAt)
	boundariesB := FiveBodyBoundariesDeterministic(affirmed.FiveBodies.Soul.Refusals, affirmed.EstablishedAt)
	if string(mustJSON(t, boundariesA)) != string(mustJSON(t, boundariesB)) {
		t.Fatal("deterministic boundary rendering diverged")
	}
	finalized, err := FinalizeDeclarationCandidate(affirmed, affirmed.SourceTurnID, time.Unix(302, 0))
	if err != nil || finalized.Phase != DeclarationCandidatePhaseFinalized || finalized.CanonicalJSON != beforeJSON || finalized.CandidateHash != beforeHash {
		t.Fatalf("provider-free finalization changed candidate truth: %#v err=%v", finalized, err)
	}
}

func TestDeclarationCandidateProviderAttemptsAreBoundedDurableAndNonSemantic(t *testing.T) {
	candidate := testDeclarationCandidate(t)
	if _, err := ApplyDeclarationProviderAttempt(candidate, DeclarationProviderAttemptUpdate{
		Provider: "anthropic", Model: "gpt-5", Phase: "declaration_phase", Section: DeclarationSectionIdentity,
		SourceTurnID: candidate.SourceTurnID, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash,
		SDKAttemptOrdinal: 1, SDKRetryBudget: 2,
	}, time.Unix(199, 0)); err == nil {
		t.Fatal("cross-provider attempt binding did not fail closed")
	}
	semanticJSON, semanticHash := candidate.CanonicalJSON, candidate.CandidateHash
	withAttempt, err := ApplyDeclarationProviderAttempt(candidate, DeclarationProviderAttemptUpdate{
		Provider: "openai", Model: "gpt-5", Phase: "declaration_phase", Section: DeclarationSectionIdentity,
		SourceTurnID: candidate.SourceTurnID, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash,
		SDKAttemptOrdinal: 1, SDKRetryBudget: 2, HTTPStatus: 200, ProviderRequestID: "req_provider_1", DurationMS: 42,
	}, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	withTool, err := ApplyDeclarationProviderAttempt(withAttempt, DeclarationProviderAttemptUpdate{
		Provider: "openai", Model: "gpt-5", Phase: "declaration_phase", Section: DeclarationSectionIdentity,
		SourceTurnID: candidate.SourceTurnID, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash,
		ToolName: DeclarationToolIdentityPut, ToolCallHash: hashText("provider-call-1"),
		ValidationCodes: []DeclarationValidationCode{DeclarationCodeFiveBodyIdentity},
		ValidationPaths: []string{"fiveBodies.identity.summary"}, DurationMS: 45,
	}, time.Unix(201, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(withTool.ProviderAttempts) != 1 || withTool.ProviderAttempts[0].SDKAttemptOrdinal != 1 ||
		withTool.ProviderAttempts[0].ToolName != DeclarationToolIdentityPut || len(withTool.ProviderAttempts[0].ValidationCodes) != 1 {
		t.Fatalf("provider attempt evidence was not enriched: %#v", withTool.ProviderAttempts)
	}
	if withTool.CanonicalJSON != semanticJSON || withTool.CandidateHash != semanticHash || withTool.Revision != candidate.Revision {
		t.Fatal("operational provider evidence changed semantic candidate bytes")
	}
	clone := withTool.Clone()
	clone.ProviderAttempts[0].ValidationPaths[0] = "changed"
	if withTool.ProviderAttempts[0].ValidationPaths[0] == "changed" {
		t.Fatal("provider attempt clone shared nested validation metadata")
	}
}

func TestDeclarationCandidateProviderContinuationRequiresAcceptedToolCheckpoint(t *testing.T) {
	candidate := testDeclarationCandidate(t)
	anchorRevision, anchorHash := candidate.Revision, candidate.CandidateHash
	attemptUpdate := DeclarationProviderAttemptUpdate{
		Provider: "openai", Model: "gpt-5", Phase: "declaration_phase", Section: DeclarationSectionIdentity,
		SourceTurnID: candidate.SourceTurnID, CandidateRevision: anchorRevision, CandidateHash: anchorHash,
		SDKAttemptOrdinal: 1, SDKRetryBudget: 2, HTTPStatus: 200, ProviderRequestID: "req_provider_1",
	}
	withAttempt, err := ApplyDeclarationProviderAttempt(candidate, attemptUpdate, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	applied := applyCandidatePayload(t, withAttempt, DeclarationToolIdentityPut, "provider-call-1",
		declarationSectionPayload{Section: testFiveBody().Identity}, time.Unix(201, 0))
	if !applied.result.Accepted {
		t.Fatalf("identity checkpoint was rejected: %#v", applied.result)
	}
	progressed := applied.next
	accepted, err := ApplyDeclarationProviderAttempt(progressed, DeclarationProviderAttemptUpdate{
		Provider: "openai", Model: "gpt-5", Phase: "declaration_phase", Section: DeclarationSectionIdentity,
		SourceTurnID: candidate.SourceTurnID, CandidateRevision: anchorRevision, CandidateHash: anchorHash,
		ToolName: DeclarationToolIdentityPut, ToolCallHash: hashText("provider-call-1"), Accepted: true,
	}, time.Unix(202, 0))
	if err != nil {
		t.Fatal(err)
	}
	continuation := attemptUpdate
	continuation.SDKAttemptOrdinal = 2
	continuation.ProviderRequestID = "req_provider_2"
	if !declarationProviderContinuationBound(accepted, continuation) {
		t.Fatal("exact accepted tool checkpoint did not bind the continuation")
	}
	continued, err := ApplyDeclarationProviderAttempt(accepted, continuation, time.Unix(203, 0))
	if err != nil {
		t.Fatalf("accepted post-tool provider continuation was rejected: %v", err)
	}
	if len(continued.ProviderAttempts) != 2 || continued.ProviderAttempts[1].SDKAttemptOrdinal != 2 ||
		continued.Revision != 1 || continued.CurrentSection != DeclarationSectionPhilosophy {
		t.Fatalf("post-tool provider continuation evidence diverged: %#v", continued)
	}

	continuation.SDKAttemptOrdinal = 3
	if _, err := ApplyDeclarationProviderAttempt(continued, continuation, time.Unix(204, 0)); err != nil {
		t.Fatalf("bounded SDK retry for the same continuation was rejected: %v", err)
	}
	continuation.SDKAttemptOrdinal = 2
	if _, err := ApplyDeclarationProviderAttempt(continued, continuation, time.Unix(205, 0)); err == nil {
		t.Fatal("stale continuation attempt ordinal did not fail closed")
	}

	requireDeclarationProviderContinuationRejected(t, progressed, continuation, time.Unix(206, 0),
		"continuation without accepted tool evidence did not fail closed")
	wrongCall := accepted.Clone()
	wrongCall.ProviderAttempts[0].ToolCallHash = hashText("different-provider-call")
	requireDeclarationProviderContinuationRejected(t, wrongCall, continuation, time.Unix(207, 0),
		"continuation with mismatched tool checkpoint evidence did not fail closed")
	wrongProvider := accepted.Clone()
	wrongProvider.ProviderAttempts[0].Provider = "anthropic"
	requireDeclarationProviderContinuationRejected(t, wrongProvider, continuation, time.Unix(208, 0),
		"continuation with mismatched provider evidence did not fail closed")
	jumpedRevision := accepted.Clone()
	jumpedRevision.Revision++
	requireDeclarationProviderContinuationRejected(t, jumpedRevision, continuation, time.Unix(209, 0),
		"continuation across more than one semantic revision did not fail closed")
}

func requireDeclarationProviderContinuationRejected(t *testing.T, candidate *DeclarationCandidate, update DeclarationProviderAttemptUpdate, now time.Time, message string) {
	t.Helper()
	if _, err := ApplyDeclarationProviderAttempt(candidate, update, now); err == nil {
		t.Fatal(message)
	}
}

type candidateApply struct {
	next   *DeclarationCandidate
	result DeclarationToolResult
}

func testDeclarationCandidate(t *testing.T) *DeclarationCandidate {
	t.Helper()
	candidate, err := NewDeclarationCandidate(DeclarationCandidateBinding{
		InstanceSlug: "acme", RegistrationID: "reg-1", AgentID: "0xabc", ConversationID: "conv-1", SourceTurnID: "turn-1", Model: "openai:gpt-5",
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func completeTestDeclarationCandidate(t *testing.T) *DeclarationCandidate {
	t.Helper()
	candidate := testDeclarationCandidate(t)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolIdentityPut, "identity", declarationSectionPayload{Section: testFiveBody().Identity}, 1)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolPhilosophyPut, "philosophy", declarationSectionPayload{Section: testFiveBody().Philosophy}, 2)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolDisciplinePut, "discipline", declarationSectionPayload{Section: testFiveBody().Discipline}, 3)
	candidate = acceptCandidateSection(t, candidate, DeclarationToolBoundariesPut, "boundaries", declarationSectionPayload{Section: testFiveBody().Boundaries}, 4)
	return acceptCandidateSection(t, candidate, DeclarationToolSoulPut, "soul", testSoulPayload(), 5)
}

func acceptCandidateSection(t *testing.T, candidate *DeclarationCandidate, tool, callID string, payload any, wantRevision int64) *DeclarationCandidate {
	t.Helper()
	got := applyCandidatePayload(t, candidate, tool, callID, payload, time.Unix(100+wantRevision, 0))
	if !got.result.Accepted || got.next == nil || got.next.Revision != wantRevision {
		t.Fatalf("tool %s rejected: next=%#v result=%#v", tool, got.next, got.result)
	}
	return got.next
}

func applyCandidatePayload(t *testing.T, candidate *DeclarationCandidate, tool, callID string, payload any, now time.Time) candidateApply {
	t.Helper()
	revision := candidate.Revision
	switch typed := payload.(type) {
	case declarationSectionPayload:
		typed.CandidateRevision = &revision
		typed.CandidateHash = candidate.CandidateHash
		payload = typed
	case declarationSoulPayload:
		typed.CandidateRevision = &revision
		typed.CandidateHash = candidate.CandidateHash
		payload = typed
	default:
		t.Fatalf("unsupported declaration payload type %T", payload)
	}
	next, result, err := ApplyDeclarationTool(candidate, DeclarationToolRequest{
		ToolName: tool, ToolCallID: callID, ExpectedRevision: candidate.Revision, ExpectedHash: candidate.CandidateHash,
		SourceTurnID: candidate.SourceTurnID, Payload: mustJSON(t, payload),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return candidateApply{next: next, result: result}
}

func testFiveBody() FiveBodyDeclaration {
	return FiveBodyDeclaration{
		Identity:   FiveBodySection{Summary: "I am the tenant-bound Hosted Genesis conversation actor."},
		Philosophy: FiveBodySection{Summary: "I prefer auditable, narrow actions over convenient implicit authority."},
		Discipline: FiveBodySection{Summary: "I ground, act, record durable boundaries, and re-ground at each checkpoint."},
		Boundaries: FiveBodySection{Summary: "I remain within the managed instance and ask the owner at authority boundaries."},
		Soul: FiveBodySoulBody{Summary: "My commitments bind conversation, review, and publication to exact durable truth.", Refusals: []FiveBodyRefusalRule{
			{Bypass: "skip the candidate hash check", Invariant: "exact reviewed bytes remain authoritative", ClosestSafePath: "submit a matching structural affirmation"},
			{Bypass: "reuse another tenant session", Invariant: "tenant and conversation guards must match", ClosestSafePath: "restart within the correct managed instance"},
			{Bypass: "call the provider after affirmation", Invariant: "finalization is deterministic and provider-free", ClosestSafePath: "publish the already affirmed candidate bytes"},
		}},
	}
}

func testSoulPayload() declarationSoulPayload {
	bodies := testFiveBody()
	return declarationSoulPayload{
		Section:         bodies.Soul,
		SelfDescription: soul.SelfDescriptionV2{Purpose: bodies.Identity.Summary, Constraints: bodies.Boundaries.Summary, Commitments: bodies.Philosophy.Summary, Limitations: bodies.Soul.Summary},
		Capabilities:    []soul.CapabilityV2{{Capability: "hosted_genesis_conversation", Scope: "Construct a typed five-body declaration with an owner.", ClaimLevel: "self-declared"}},
		Transparency:    DeclarationTransparency{ModelProviderUncertainty: "Provider output is self-declared.", OperationalNotes: "Host validates each section.", SelfDeclaredNotice: "This candidate is self-declared until publication."},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
