package controlplane

import (
	"context"
	"encoding/json"
	"errors"
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

func TestHostedGenesisFinalAffirmationQueuesMicroVMActorTurn(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)
	finalPrompt := "Do you affirm this declaration as the foundation of your minted soul? If there is anything here you would correct, qualify, or strike before it is inscribed, name it now."

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"define yourself"},{"role":"assistant","content":` + jsonString(finalPrompt) + `}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		LatestTurnID:   "turn-ready",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, false)

	resp, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{ConversationID: mintConversationTestConversationID, Message: "I affirm."}),
		map[string]string{"id": reg.ID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected final affirmation to return 202 accepted actor turn, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("expected final affirmation to remain an accepted in_progress actor turn, got %#v", out.Conversation)
	}
	if dispatcher.queueCalls != 1 {
		t.Fatalf("final affirmation must enqueue the accepted turn to the MicroVM actor, queued %#v", dispatcher.lastQueue)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("final affirmation must not synchronously dispatch a Host extraction job, got %d dispatcher calls", dispatcher.calls)
	}
	if dispatcher.lastQueue.Step != hostedgenesis.StepMicroVMDispatch ||
		dispatcher.lastQueue.ConversationID != mintConversationTestConversationID ||
		dispatcher.lastQueue.TurnID == "" {
		t.Fatalf("final affirmation queue message must be bound to the accepted actor turn, got %#v", dispatcher.lastQueue)
	}
	if len(out.Conversation.Messages) == 0 || out.Conversation.Messages[len(out.Conversation.Messages)-1].Content != "I affirm." {
		t.Fatalf("final affirmation must remain in the durable transcript, got %#v", out.Conversation.Messages)
	}
}

// TestH1_2_MicroVMQueueUnavailableIsLoudFailureNotSyncLLMFallthrough proves that
// when the dispatch queue is unavailable, the accept path fails closed and
// loudly with a typed 503 microvm-unavailable error and persists a retryable
// failed session rather than silently falling back to a synchronous LLM call.
func TestH1_2_MicroVMQueueUnavailableIsLoudFailureNotSyncLLMFallthrough(t *testing.T) {
	tdb, s, reg, _ := h1d2AcceptPathFixture(t)
	s.enqueueHostedGenesisMessage = nil
	syncLLMCalled := false
	s.hostedGenesisAssistantRunner = func(_ context.Context, _ hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		syncLLMCalled = true
		return hostedGenesisAssistantRunResult{}, nil
	}
	h1d2ExpectAcceptPathProgression(t, tdb, hostedgenesis.StatusFailed)

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err == nil {
		t.Fatalf("expected loud microvm-unavailable failure, got response %#v", resp)
	}
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeMicroVMUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected typed microvm-unavailable 503, got %#v", appErr)
	}
	if syncLLMCalled {
		t.Fatalf("accept path must not fall back to synchronous LLM when MicroVM queue dispatch is unavailable")
	}
}

// TestH1_2_NonProductionSyncFallbackGuard proves the retained synchronous
// assistant runner stays reachable ONLY through the explicit non-production
// guard (hostedGenesisSyncAssistantFallbackEnabled). This keeps the sync path
// referenced and covered until H2.1 deletes it; production never sets the guard
// so production cannot reach this branch.
func TestH1_2_NonProductionSyncFallbackGuard(t *testing.T) {
	tdb, s, reg, _ := h1d2AcceptPathFixture(t)
	// No dispatcher wired, but the non-production sync fallback guard is set.
	s.hostedGenesisMicroVMDispatcher = nil
	s.hostedGenesisSyncAssistantFallbackEnabled = true
	stubHostedGenesisAssistantRunner(t, s, "assistant reply", nil)
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusAssistantTurnReady)

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := assertSoulInstanceMintConversationAcceptedResponse(t, resp)
	if out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady {
		t.Fatalf("expected sync fallback to reach assistant_turn_ready, got %#v", out.Conversation)
	}
}

// TestH1_2_NonProductionSyncFallbackFailurePersistsTypedFailure covers the
// non-production sync fallback's failure branch (provider error) so the
// retained sync path's failure handling and provider-name logging stay covered
// until H2.1 deletes the path. H1.4 (kills G10a): a failed turn surfaces as a
// loud non-2xx typed failure (502 assistant_turn_failed), not HTTP 200 with a
// failed body. The durable session is still persisted as a retryable failed
// turn before the error is returned.
func TestH1_2_NonProductionSyncFallbackFailurePersistsTypedFailure(t *testing.T) {
	tdb, s, reg, _ := h1d2AcceptPathFixture(t)
	s.hostedGenesisMicroVMDispatcher = nil
	s.hostedGenesisSyncAssistantFallbackEnabled = true
	stubHostedGenesisAssistantRunner(t, s, "", errors.New("provider unavailable"))
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusFailed)

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err == nil {
		t.Fatalf("expected loud assistant-turn-failed error, got response %#v", resp)
	}
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeAssistantTurnFailed || appErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected typed assistant_turn_failed 502, got %#v", appErr)
	}
}

// h1d2AcceptPathFixture builds the shared mock scaffolding for a fresh hosted
// genesis accept turn and returns a stub dispatcher not yet wired onto the
// server (callers wire it to control the dispatch outcome).
func h1d2AcceptPathFixture(t *testing.T) (*mintConversationTestDB, *Server, models.SoulAgentRegistration, *stubMicroVMDispatcher) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
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
