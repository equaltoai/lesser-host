package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// TestH1_2_AcceptPathDispatchesControllerRunAndReturns202 proves the production
// hosted genesis accept path dispatches the AppTheory M16 MicroVM controller
// run command through the MicroVMDispatcher seam and returns HTTP 202
// accepted-pending with the durable session in_progress (no synchronous control
// plane LLM call).
func TestH1_2_AcceptPathDispatchesControllerRunAndReturns202(t *testing.T) {
	tdb, s, reg, dispatcher := h1d2AcceptPathFixture(t)
	s.hostedGenesisMicroVMDispatcher = dispatcher
	h1d2ExpectAcceptPathProgression(t, tdb, hostedgenesis.StatusInProgress)

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
	if dispatcher.calls != 1 {
		t.Fatalf("expected exactly one M16 controller run dispatch, got %d", dispatcher.calls)
	}
	if dispatcher.lastBinding.ConversationID == "" || dispatcher.lastBinding.RegistrationID != reg.ID {
		t.Fatalf("expected dispatch bound to the accepted turn's session, got %#v", dispatcher.lastBinding)
	}
}

// TestH1_2_AcceptPathPopulatesThreeMicroVMFieldsViaApplyLifecycleRef proves a
// successful dispatch populates MicroVMExecutionID, ExecutionStateRef, and
// MicroVMLifecycleRef on the authoritative HostedGenesisSession via
// ApplyMicroVMLifecycleRef (the lifecycle ref is validated against the binding).
func TestH1_2_AcceptPathPopulatesThreeMicroVMFieldsViaApplyLifecycleRef(t *testing.T) {
	tdb, s, reg, dispatcher := h1d2AcceptPathFixture(t)
	s.hostedGenesisMicroVMDispatcher = dispatcher
	var captured *models.HostedGenesisSession
	h1d2ExpectAcceptPathProgressionCapture(t, tdb, &captured)

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202, got %#v", resp)
	}
	if captured == nil {
		t.Fatalf("expected persisted in_progress session capture")
	}
	if captured.MicroVMExecutionID == "" || captured.ExecutionStateRef == "" || captured.MicroVMLifecycleRef == nil {
		t.Fatalf("expected all three MicroVM execution/cache refs populated, got %#v", captured)
	}
	if !strings.HasPrefix(captured.ExecutionStateRef, "microvm://") {
		t.Fatalf("expected ExecutionStateRef formatted by FormatMicroVMExecutionStateRef, got %q", captured.ExecutionStateRef)
	}
	if captured.MicroVMLifecycleRef.SessionID != dispatcher.lastBinding.ConversationID {
		t.Fatalf("expected lifecycle ref bound to dispatched session, got %#v", captured.MicroVMLifecycleRef)
	}
	if captured.MicroVMLifecycleRef.SourceOfTruth != hostedgenesis.MicroVMSourceOfTruth {
		t.Fatalf("expected lifecycle ref source of truth, got %#v", captured.MicroVMLifecycleRef)
	}
}

// TestH1_2_MicroVMUnavailableIsLoudFailureNotSyncLLMFallthrough proves that
// when the MicroVM dispatcher is unwired (the production misconfiguration
// case), the accept path fails closed and loudly with a typed 503
// microvm-unavailable error and persists a retryable failed session, rather
// than silently falling back to a synchronous control-plane LLM call.
func TestH1_2_MicroVMUnavailableIsLoudFailureNotSyncLLMFallthrough(t *testing.T) {
	tdb, s, reg, _ := h1d2AcceptPathFixture(t)
	// No dispatcher wired: production must not fall back to sync LLM.
	s.hostedGenesisMicroVMDispatcher = nil
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
		t.Fatalf("accept path must not fall back to synchronous LLM when MicroVM dispatch is unavailable")
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
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("accept path must not enqueue SQS authority: %#v", msg)
		return nil
	}
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)
	tdb.qMintIdem.On("First", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(theoryErrors.ErrItemNotFound).Once()
	expectSoulInstanceMintConversationDebit(t, tdb, reg.AgentID, true)
	dispatcher := &stubMicroVMDispatcher{t: t}
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

func h1d2ExpectAcceptPathProgressionCapture(t *testing.T, tdb *mintConversationTestDB, captured **models.HostedGenesisSession) {
	t.Helper()
	tb, _ := tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder)
	if tb == nil {
		tb = new(ttmocks.MockTransactionBuilder)
		tdb.db.TransactWriteBuilder = tb
	}
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		*captured = testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if hostedgenesis.NormalizeStatus((*captured).Status) != hostedgenesis.StatusInProgress {
			t.Fatalf("expected in_progress dispatched session, got %#v", *captured)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
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
