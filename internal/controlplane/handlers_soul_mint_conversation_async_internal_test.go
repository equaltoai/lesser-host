package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// TestH1_2_AcceptPathEnqueuesMicroVMDispatchAndReturns202 proves the production
// hosted genesis accept path durably commits the turn, enqueues a non-
// authoritative MicroVM dispatch command, and returns HTTP 202 accepted-pending
// without synchronously waiting for the MicroVM to become ready.
func TestH1_2_AcceptPathEnqueuesMicroVMDispatchAndReturns202(t *testing.T) {
	_, s, reg, dispatcher := h1d2AcceptPathFixture(t)
	s.hostedGenesisMicroVMDispatcher = dispatcher

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 accepted-pending, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("expected in_progress durable status, got %#v", out.Conversation)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("accept path must not synchronously dispatch the MicroVM, got %d calls", dispatcher.calls)
	}
	if dispatcher.queueCalls != 1 {
		t.Fatalf("expected exactly one non-authoritative MicroVM dispatch queue message, got %d", dispatcher.queueCalls)
	}
	if dispatcher.lastQueue.Step != hostedgenesis.StepMicroVMDispatch || dispatcher.lastQueue.RegistrationID != reg.ID || dispatcher.lastQueue.ConversationID == "" || dispatcher.lastQueue.TurnID == "" {
		t.Fatalf("expected dispatch queue message bound to accepted turn, got %#v", dispatcher.lastQueue)
	}
}

func TestH1_2_AcceptPathDoesNotPopulateMicroVMRefsBeforeWorkerDispatch(t *testing.T) {
	_, s, reg, dispatcher := h1d2AcceptPathFixture(t)
	s.hostedGenesisMicroVMDispatcher = dispatcher

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	_ = assertSoulInstanceMintConversationDispatchedResponse(t, resp)
	if dispatcher.queueCalls != 1 || dispatcher.calls != 0 {
		t.Fatalf("expected enqueue-only accept path, queue=%d dispatch=%d", dispatcher.queueCalls, dispatcher.calls)
	}
}

func TestHostedGenesisInProgressNudgeReturnsWaitOnlyProjection(t *testing.T) {
	const activeTurnID = "turn-active"

	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		dispatcher.queueCalls++
		dispatcher.lastQueue = msg
		return nil
	}
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"first accepted turn"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   activeTurnID,
		RequestID:      "req-original",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	session := hostedGenesisRecoverySessionFixture(t, reg, hostedgenesis.StatusInProgress, "")
	session.LatestTurnID = activeTurnID
	session.TurnLedger[0].TurnID = activeTurnID
	stubSoulInstanceRecoverySession(t, tdb, session)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{
			ConversationID: mintConversationTestConversationID,
			Message:        "second nudge while the first turn is still running",
		}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("in-progress nudge should return wait projection, got err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected wait-only 202 projection, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		out.Conversation.LatestTurnID != activeTurnID ||
		out.Conversation.MessageCount != 1 ||
		out.Conversation.PollAfterSeconds <= 0 {
		t.Fatalf("expected current in-progress turn projection with poll guidance, got %#v", out.Conversation)
	}
	if len(out.Conversation.Messages) != 1 || out.Conversation.Messages[0].Content != "first accepted turn" {
		t.Fatalf("wait-only projection must not append the nudge, got %#v", out.Conversation.Messages)
	}
	if dispatcher.queueCalls != 0 || dispatcher.calls != 0 {
		t.Fatalf("wait-only nudge must not dispatch a second MicroVM run, queue=%d dispatch=%d", dispatcher.queueCalls, dispatcher.calls)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
	tdb.qBudget.AssertNumberOfCalls(t, "First", 0)
}

func TestHostedGenesisStructuralAffirmationQueuesProviderFreeFinalizationTurn(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	candidate := controlplaneCompleteReviewCandidate(t, hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: soulInstanceBootstrapTestInstanceSlug, RegistrationID: reg.ID, AgentID: reg.AgentID,
		ConversationID: mintConversationTestConversationID, SourceTurnID: "turn-ready", Model: "anthropic:claude-sonnet-4-6",
	}, now)
	assertControlplaneReviewCandidateCanAffirm(t, candidate, "turn-next", now.Add(time.Minute))

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	conv := models.SoulAgentMintConversation{
		AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Model: "anthropic:claude-sonnet-4-6",
		Messages: encodeMintConversationBlob(`[{"role":"user","content":"define yourself"},{"role":"assistant","content":` + jsonString(candidate.Review.ReviewText) + `}]`),
		Status:   models.SoulMintConversationStatusAssistantTurnReady, LatestTurnID: "turn-ready", CreatedAt: now,
	}
	stubMintConversationConversation(t, tdb, conv)
	derived := hostedGenesisSessionFromLegacyConversationForTest(tdb, conv)
	assertControlplaneReviewFixtureMatches(t, derived.DeclarationCandidate, candidate)
	assertControlplaneReviewCandidateCanAffirm(t, derived.DeclarationCandidate, "turn-next", now.Add(time.Minute))
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, false)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Message: "I affirm.", CandidateAction: &hostedgenesis.DeclarationCandidateAction{
			Action: "affirm", CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
		}}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	assertProviderFreeFinalizationAccepted(t, resp, dispatcher)
}

func TestHostedGenesisExactAdvertisedBoundariesEditQueuesOnlySelectedSection(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)
	now := time.Date(2026, 7, 23, 11, 30, 30, 0, time.UTC)
	candidate := controlplaneCompleteReviewCandidate(t, hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: soulInstanceBootstrapTestInstanceSlug, RegistrationID: reg.ID, AgentID: reg.AgentID,
		ConversationID: mintConversationTestConversationID, SourceTurnID: "turn-live-review", Model: "anthropic:claude-sonnet-4-6",
	}, now)
	candidate = liveShapedNullCapabilitiesReviewCandidate(t, candidate)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	conv := models.SoulAgentMintConversation{
		AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID, Model: "anthropic:claude-sonnet-4-6",
		Messages: encodeMintConversationBlob(`[{"role":"user","content":"define yourself"},{"role":"assistant","content":` + jsonString(candidate.Review.ReviewText) + `}]`),
		Status:   models.SoulMintConversationStatusAssistantTurnReady, LatestTurnID: "turn-live-review", CreatedAt: now,
	}
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		target, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok {
			t.Fatal("conversation mock target has unexpected type")
		}
		*target = conv
	}).Once()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		session := hostedGenesisSessionFromLegacyConversationForTest(tdb, conv)
		session.DeclarationCandidate = candidate.Clone()
		session.CandidateRevision = candidate.Revision
		session.CandidateHash = candidate.CandidateHash
		session.CandidatePhase = string(candidate.Phase)
		target, ok := args.Get(0).(*models.HostedGenesisSession)
		if !ok {
			t.Fatal("hosted genesis session mock target has unexpected type")
		}
		*target = session
	}).Once()
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, false)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{
			ConversationID: mintConversationTestConversationID,
			Message:        "Revise boundaries: retain the owner-supplied B10 and regenerate the exact review.",
			CandidateAction: &hostedgenesis.DeclarationCandidateAction{
				Action: "edit", Section: hostedgenesis.DeclarationSectionBoundaries,
				CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
			},
		}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("exact advertised edit returned %d: %#v", resp.Status, resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	got := out.Conversation.DeclarationCandidate
	if got == nil || got.Phase != hostedgenesis.DeclarationCandidatePhaseSection ||
		got.CurrentSection != hostedgenesis.DeclarationSectionBoundaries || got.Revision != candidate.Revision+1 ||
		len(got.CompletedSections) != 5 || got.Review != nil {
		t.Fatalf("exact edit did not reopen only boundaries: %#v", got)
	}
	if dispatcher.queueCalls != 1 || dispatcher.calls != 0 || dispatcher.lastQueue.TurnID == "" {
		t.Fatalf("edit must queue one AppTheory MicroVM actor turn only: %#v", dispatcher)
	}
}

func liveShapedNullCapabilitiesReviewCandidate(t *testing.T, candidate *hostedgenesis.DeclarationCandidate) *hostedgenesis.DeclarationCandidate {
	t.Helper()
	legacy := candidate.Clone()
	legacy.Capabilities = nil
	legacy.CanonicalJSON = strings.Replace(legacy.CanonicalJSON, `"capabilities":[]`, `"capabilities":null`, 1)
	if legacy.CanonicalJSON == candidate.CanonicalJSON {
		t.Fatal("review fixture did not create the deployed null capabilities shape")
	}
	canonicalDigest := sha256.Sum256([]byte(legacy.CanonicalJSON))
	legacy.CandidateHash = fmt.Sprintf("sha256:%x", canonicalDigest)
	reviewText := fmt.Sprintf(
		"Hosted Genesis owner review\n\nReview the exact canonical JSON below. Structural affirmation binds this review text, these canonical bytes, and the candidate revision.\n\nCandidate revision: %d\nCandidate hash: %s\nCanonical JSON byte length: %d\n-----BEGIN HOSTED GENESIS CANONICAL JSON-----\n%s\n-----END HOSTED GENESIS CANONICAL JSON-----\n",
		legacy.Revision, legacy.CandidateHash, len(legacy.CanonicalJSON), legacy.CanonicalJSON,
	)
	reviewDigest := sha256.Sum256([]byte(reviewText))
	legacy.Review.CandidateHash = legacy.CandidateHash
	legacy.Review.ReviewHash = fmt.Sprintf("sha256:%x", reviewDigest)
	legacy.Review.ReviewText = reviewText
	if err := legacy.Validate(); err == nil {
		t.Fatal("deployed null-capabilities representation should be invalid before exact edit repair")
	}
	return legacy
}

func assertControlplaneReviewCandidateCanAffirm(t *testing.T, candidate *hostedgenesis.DeclarationCandidate, turnID string, now time.Time) {
	t.Helper()
	if _, err := hostedgenesis.ApplyDeclarationCandidateAction(candidate, hostedgenesis.DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
	}, turnID, now); err != nil {
		t.Fatalf("review candidate cannot affirm directly: %v", err)
	}
}

func assertControlplaneReviewFixtureMatches(t *testing.T, derived *hostedgenesis.DeclarationCandidate, expected *hostedgenesis.DeclarationCandidate) {
	t.Helper()
	if derived == nil || derived.Review == nil || derived.CandidateHash != expected.CandidateHash || derived.Review.ReviewHash != expected.Review.ReviewHash {
		t.Fatalf("review fixture drift: derived=%#v expected=%#v", derived, expected)
	}
}

func assertProviderFreeFinalizationAccepted(t *testing.T, resp *apptheory.Response, dispatcher *stubMicroVMDispatcher) {
	t.Helper()
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected structural affirmation 202, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress || out.Conversation.DeclarationCandidate == nil || out.Conversation.DeclarationCandidate.Phase != hostedgenesis.DeclarationCandidatePhaseAffirmed {
		t.Fatalf("expected structurally affirmed in_progress turn, got %#v", out.Conversation)
	}
	if dispatcher.queueCalls != 1 || dispatcher.calls != 0 {
		t.Fatalf("affirmation must queue only the MicroVM actor, queue=%d dispatch=%d", dispatcher.queueCalls, dispatcher.calls)
	}
	if dispatcher.lastQueue.Step != hostedgenesis.StepMicroVMDispatch || dispatcher.lastQueue.TurnID == "" {
		t.Fatalf("unbound finalization turn: %#v", dispatcher.lastQueue)
	}
}

func controlplaneCompleteReviewCandidate(t *testing.T, binding hostedgenesis.DeclarationCandidateBinding, now time.Time) *hostedgenesis.DeclarationCandidate {
	t.Helper()
	candidate, err := hostedgenesis.NewDeclarationCandidate(binding, now)
	if err != nil {
		t.Fatal(err)
	}
	calls := []struct{ name, body string }{
		{hostedgenesis.DeclarationToolIdentityPut, `{"section":{"summary":"I am the tenant-bound Hosted Genesis actor.","notes":[]}}`},
		{hostedgenesis.DeclarationToolPhilosophyPut, `{"section":{"summary":"I prefer auditable durable truth over implicit authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolDisciplinePut, `{"section":{"summary":"I ground, act, record, and re-ground at each checkpoint.","notes":[]}}`},
		{hostedgenesis.DeclarationToolBoundariesPut, `{"section":{"summary":"I remain within the managed instance and require owner authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolSoulPut, `{"section":{"summary":"Exact reviewed truth is load-bearing.","notes":[],"refusals":[{"bypass":"skip the candidate hash check","invariant":"exact reviewed bytes remain authoritative","closestSafePath":"submit a matching structural affirmation"},{"bypass":"reuse another tenant session","invariant":"tenant and session guards must match","closestSafePath":"restart in the correct managed instance"},{"bypass":"call a provider after affirmation","invariant":"finalization remains deterministic","closestSafePath":"publish the exact affirmed candidate bytes"}]},"selfDescription":{"purpose":"Construct a typed Hosted Genesis declaration.","constraints":"Remain tenant bound.","commitments":"Preserve exact durable truth.","limitations":"No provider after affirmation.","authoredBy":"agent","mintingModel":"anthropic:claude-sonnet-4-6"},"capabilities":[],"transparency":{"modelProviderUncertainty":"Provider content is self-declared.","operationalNotes":"Host validates every section.","selfDeclaredNotice":"Self-declared until publication."}}`},
	}
	for i, call := range calls {
		var payload map[string]any
		if err := json.Unmarshal([]byte(call.body), &payload); err != nil {
			t.Fatal(err)
		}
		payload["candidateRevision"] = candidate.Revision
		payload["candidateHash"] = candidate.CandidateHash
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		next, result, err := hostedgenesis.ApplyDeclarationTool(candidate, hostedgenesis.DeclarationToolRequest{
			ToolName: call.name, ToolCallID: fmt.Sprintf("call-%d", i), ExpectedRevision: candidate.Revision,
			ExpectedHash: candidate.CandidateHash, SourceTurnID: binding.SourceTurnID, Payload: payloadBytes,
		}, now.Add(time.Duration(i)*time.Second))
		if err != nil || !result.Accepted {
			t.Fatalf("candidate tool %s failed: %#v err=%v", call.name, result, err)
		}
		candidate = next
	}
	return candidate
}

// TestH1_2_MicroVMQueueUnavailableIsLoudFailureNotSyncLLMFallthrough proves that
// when the dispatch queue is unavailable, the accept path fails closed and
// loudly with a typed 503 microvm-unavailable error and persists a retryable
// failed session rather than silently falling back to a synchronous LLM call.
func TestH1_2_MicroVMQueueUnavailableIsLoudFailureNotSyncLLMFallthrough(t *testing.T) {
	tdb, s, reg, _ := h1d2AcceptPathFixture(t)
	s.enqueueHostedGenesisMessage = nil
	h1d2ExpectAcceptPathProgression(t, tdb, hostedgenesis.StatusFailed)

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err == nil {
		t.Fatalf("expected loud microvm-unavailable failure, got response %#v", resp)
	}
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeMicroVMUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected typed microvm-unavailable 503, got %#v", appErr)
	}
}

// TestH1_2_NonProductionSyncFallbackGuard proves the retained synchronous
// assistant runner stays reachable ONLY through the explicit non-production
// referenced and covered until H2.1 deletes it; production never sets the guard
// so production cannot reach this branch.
// TestH1_2_NonProductionSyncFallbackFailurePersistsTypedFailure covers the
// non-production sync fallback's failure branch (provider error) so the
// retained sync path's failure handling and provider-name logging stay covered
// until H2.1 deletes the path. H1.4 (kills G10a): a failed turn surfaces as a
// loud non-2xx typed failure (502 assistant_turn_failed), not HTTP 200 with a
// failed body. The durable session is still persisted as a retryable failed
// turn before the error is returned.
// h1d2AcceptPathFixture builds the shared mock scaffolding for a fresh hosted
// genesis accept turn and returns a stub dispatcher not yet wired onto the
// server (callers wire it to control the dispatch outcome).
func h1d2AcceptPathFixture(t *testing.T) (*mintConversationTestDB, *Server, models.SoulAgentRegistration, *stubMicroVMDispatcher) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	// The retained non-production sync fallback builds the five-body system
	// prompt through the fail-closed env selector; there is no legacy prompt.
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, hostedgenesis.DeclarationSchemaVersionV2)
	t.Setenv(hostedgenesis.EnvGuidanceVersion, hostedgenesis.GuidanceVersionV2)
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qMintIdem.On("First", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(theoryErrors.ErrItemNotFound).Once()
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, true)
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		dispatcher.queueCalls++
		dispatcher.lastQueue = msg
		return nil
	}
	return tdb, s, reg, dispatcher
}

func h1d2AcceptPathRequest(t *testing.T, reg models.SoulAgentRegistration) *apptheory.Context {
	t.Helper()
	return newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{Model: "anthropic:claude-sonnet-4-6", Message: soulInstanceBootstrapTestConversationMessage, IdempotencyKey: soulInstanceBootstrapTestIdempotencyKey, CorrelationID: "corr-1"}),
		map[string]string{"id": reg.ID},
	)
}

func h1d2ExpectAcceptPathProgression(t *testing.T, tdb *mintConversationTestDB, wantStatus hostedgenesis.Status) {
	t.Helper()
	expectSoulInstanceMintConversationProgression(t, tdb, wantStatus)
}

// assertHostedGenesisProgressedSession asserts the persisted progressed session
// matches the expected H1.2 status shape (dispatched in_progress carries the
// three MicroVM refs; failed carries a retryable assistant/microvm failure).
func assertHostedGenesisProgressedSession(t *testing.T, session *models.HostedGenesisSession, wantStatus hostedgenesis.Status) {
	t.Helper()
	switch wantStatus {
	case hostedgenesis.StatusAssistantTurnReady:
		assertHostedGenesisAssistantReadySession(t, session)
	case hostedgenesis.StatusInProgress:
		assertHostedGenesisDispatchedSession(t, session)
	case hostedgenesis.StatusFailed:
		assertHostedGenesisFailedSession(t, session)
	}
}

func assertHostedGenesisAssistantReadySession(t *testing.T, session *models.HostedGenesisSession) {
	t.Helper()
	if session.AssistantCheckpointRef == "" || session.MessageCount < 2 || session.Failure != nil {
		t.Fatalf("assistant-ready session must carry checkpoint/message count and no failure: %#v", session)
	}
}

func assertHostedGenesisDispatchedSession(t *testing.T, session *models.HostedGenesisSession) {
	t.Helper()
	if session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" || session.MicroVMLifecycleRef == nil {
		t.Fatalf("in_progress dispatched session must carry the three MicroVM execution/cache refs: %#v", session)
	}
	if session.AssistantCheckpointRef != "" || session.Failure != nil {
		t.Fatalf("in_progress dispatched session must not carry an assistant checkpoint or failure: %#v", session)
	}
}

func assertHostedGenesisFailedSession(t *testing.T, session *models.HostedGenesisSession) {
	t.Helper()
	if session.Failure == nil || !session.Failure.Retryable {
		t.Fatalf("failed session must carry retryable failure: %#v", session)
	}
	if session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed && session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable {
		t.Fatalf("failed session must carry assistant or microvm-unavailable failure: %#v", session)
	}
}

// assertSoulInstanceMintConversationDispatchedResponse asserts the H1.2 accept
// path returned 202 accepted-pending with the durable session in_progress and
// no inline assistant message (the turn runs inside the MicroVM).
func assertSoulInstanceMintConversationDispatchedResponse(t *testing.T, resp *apptheory.Response) hostedGenesisConversationResponse {
	t.Helper()
	if resp.Status != http.StatusAccepted || !strings.Contains(resp.Headers["content-type"][0], "application/json") {
		t.Fatalf("expected JSON 202 dispatched response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.ConversationID == "" ||
		out.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		out.Conversation.RequestID != "req-instance-bootstrap" {
		t.Fatalf("expected in_progress dispatched durable status, got %#v", out)
	}
	if out.Conversation.TraceIDs == nil ||
		out.Conversation.TraceIDs.IdempotencyKey != soulInstanceBootstrapTestIdempotencyKey ||
		out.Conversation.TraceIDs.CorrelationID != "corr-1" {
		t.Fatalf("expected trace ids, got %#v", out.Conversation.TraceIDs)
	}
	for _, msg := range out.Conversation.Messages {
		if msg.Role == hostedGenesisTranscriptRoleAssistant {
			t.Fatalf("dispatched 202 response must not carry an inline assistant message: %#v", out.Conversation.Messages)
		}
	}
	if strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("response leaked credential material: %s", string(resp.Body))
	}
	return out
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestHostedGenesisProviderName(t *testing.T) {
	cases := map[string]string{
		"openai:gpt-5.4":                 "openai",
		"  Anthropic:claude-sonnet-4-6 ": "anthropic",
		"unknown:foo":                    hostedGenesisProviderUnknown,
		"":                               hostedGenesisProviderUnknown,
	}
	for in, want := range cases {
		if got := hostedGenesisProviderName(in); got != want {
			t.Fatalf("hostedGenesisProviderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostedGenesisMaxInt(t *testing.T) {
	if hostedGenesisMaxInt(1, 2) != 2 || hostedGenesisMaxInt(3, 2) != 3 || hostedGenesisMaxInt(5, 5) != 5 {
		t.Fatalf("hostedGenesisMaxInt returned unexpected value")
	}
}
