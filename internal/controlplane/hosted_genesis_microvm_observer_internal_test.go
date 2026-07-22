package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// TestRegistrationWaitOnlyReadObservesTerminalMicroVMAndConvergesGuardedHostTruth
// covers the Ptah registration route directly. Ordinary registration polling
// must issue exactly one AppTheory controller GET reconciliation, never run or
// queue work, and atomically converge both Host projections when the provider
// session is terminal or maximum-duration-expired.
func TestRegistrationWaitOnlyReadObservesTerminalMicroVMAndConvergesGuardedHostTruth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   runtimemicrovm.LifecycleState
		expired bool
	}{
		{name: "terminated", state: runtimemicrovm.StateTerminated},
		{name: "failed", state: runtimemicrovm.StateFailed},
		{name: "maximum duration expired", state: runtimemicrovm.StateStopped, expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runRegistrationWaitOnlyReadObservationCase(t, tc.state, tc.expired)
		})
	}
}

func runRegistrationWaitOnlyReadObservationCase(t *testing.T, state runtimemicrovm.LifecycleState, expired bool) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)
	dispatcher.observedState = state
	dispatcher.expired = expired
	reg := mintConversationHandleReg()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	staleSession, staleConversation, failedSession, failedConversation := registrationWaitObservationFixtures(t, reg)

	// Registration handler rows, CompletionWriter guarded preflight rows, then
	// post-transaction reload rows.
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, staleSession)
	stubSoulInstanceRecoverySession(t, tdb, staleSession)
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, failedSession)
	stubSoulInstanceRecoveryConversation(t, tdb, failedConversation)
	expectWaitObservationFailureTransaction(t, tdb, staleSession.Version, staleSession.LatestTurnID)

	resp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("registration wait-only read: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 registration projection, got %#v", resp)
	}
	if dispatcher.reconcileCalls != 1 || dispatcher.calls != 0 || dispatcher.queueCalls != 0 {
		t.Fatalf("registration poll must GET exactly once and never invoke/run/queue/nudge: reconcile=%d dispatch=%d queue=%d", dispatcher.reconcileCalls, dispatcher.calls, dispatcher.queueCalls)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusFailed || out.Conversation.Failure == nil || out.Conversation.Failure.Code != hostedGenesisFailureMicroVMUnavailable || !out.Conversation.Failure.Retryable {
		t.Fatalf("registration route did not converge retryable typed Host truth: %#v", out.Conversation)
	}
	tdb.db.AssertExpectations(t)
	tx, ok := tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder)
	if !ok || !tx.AssertExpectations(t) {
		t.Fatal("registration-route guarded observation transaction expectations were not satisfied")
	}
}

func TestRegistrationWaitOnlyReadLeavesLiveMicroVMStateUnchanged(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)
	dispatcher.observedState = runtimemicrovm.StateRunning
	reg := mintConversationHandleReg()

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	staleSession, staleConversation, _, _ := registrationWaitObservationFixtures(t, reg)
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, staleSession)

	resp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("live registration wait-only read: %v", err)
	}
	if dispatcher.reconcileCalls != 1 || dispatcher.calls != 0 || dispatcher.queueCalls != 0 {
		t.Fatalf("live registration poll must GET exactly once and never invoke/run/queue/nudge: reconcile=%d dispatch=%d queue=%d", dispatcher.reconcileCalls, dispatcher.calls, dispatcher.queueCalls)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode live registration response: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress || out.Conversation.Failure != nil {
		t.Fatalf("live provider observation must leave Host truth unchanged: %#v", out.Conversation)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

// TestRegistrationObservationThenRecoveryReplaysAcceptedTurnFromPriorActorCheckpoint
// is the deployed Ptah sequence: the current paid turn is durably accepted,
// the provider dies before it can author a current-turn VM checkpoint, a
// registration GET converges Host truth without dispatching, and the explicit
// recovery action replays that same accepted turn through the official
// AppTheory MicroVM run seam from the prior completed actor checkpoint.
func TestRegistrationObservationThenRecoveryReplaysAcceptedTurnFromPriorActorCheckpoint(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	staleSession, staleConversation, failedSession, failedConversation := microVMUnavailableRecoverySequenceFixtures(t, reg)
	durableSession := *cloneHostedGenesisSession(&failedSession)
	originalLedger := encodeMicroVMRecoveryTurnLedger(t, staleSession.TurnLedger)
	dispatcher := &stateReadingMicroVMDispatcher{
		stubMicroVMDispatcher: &stubMicroVMDispatcher{t: t, observedState: runtimemicrovm.StateTerminated},
		readSession: func() *models.HostedGenesisSession {
			return cloneHostedGenesisSession(&durableSession)
		},
		expectedFailureCode:       hostedgenesis.FailureCodeMicroVMUnavailable,
		expectedRemainingAttempts: 2,
	}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	s.hostedGenesisAssistantRunner = func(_ context.Context, in hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		t.Fatalf("microvm_unavailable recovery must not use the synchronous assistant fallback: %#v", in)
		return hostedGenesisAssistantRunResult{}, nil
	}

	// Registration handler rows, observer guarded preflight rows, and observer
	// reload rows.
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, staleSession)
	stubSoulInstanceRecoverySession(t, tdb, staleSession)
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, failedSession)
	stubSoulInstanceRecoveryConversation(t, tdb, failedConversation)

	// The subsequent explicit recovery reads the converged rows.
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, failedConversation)
	stubSoulInstanceRecoverySession(t, tdb, failedSession)

	expectWaitObservationFailureTransaction(t, tdb, staleSession.Version, staleSession.LatestTurnID)
	expectMicroVMUnavailableRecoverySequenceTransactions(t, tdb, failedSession, originalLedger, func(session *models.HostedGenesisSession) {
		durableSession = *cloneHostedGenesisSession(session)
	})

	registrationResp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("registration observation: %v", err)
	}
	assertMicroVMUnavailableRegistrationObservation(t, registrationResp, dispatcher)
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 1)

	recoveryResp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("recover prior-checkpoint accepted turn: %v", err)
	}
	recovered := assertMicroVMUnavailableRecoveryDispatch(t, recoveryResp, dispatcher, staleSession)
	assertMicroVMRecoveryMessagesUnchanged(t, recovered.Conversation.Messages)
	assertMicroVMRecoveryDurableState(t, durableSession, staleSession, originalLedger)
	if recovered.Conversation.MessagesTruncated {
		t.Fatalf("recovery unexpectedly truncated the accepted transcript")
	}
	tdb.qBudget.AssertNumberOfCalls(t, "First", 0)
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 3)
	assertHostedGenesisResponseNoForbiddenValues(t, recoveryResp.Body, hostedGenesisStatusForbiddenValues())
}

func assertMicroVMUnavailableRegistrationObservation(t *testing.T, resp *apptheory.Response, dispatcher *stateReadingMicroVMDispatcher) {
	t.Helper()
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 registration observation, got %#v", resp)
	}
	if dispatcher.reconcileCalls != 1 || dispatcher.calls != 0 || dispatcher.queueCalls != 0 {
		t.Fatalf("registration read must GET exactly once without dispatch/nudge: reconcile=%d run=%d queue=%d", dispatcher.reconcileCalls, dispatcher.calls, dispatcher.queueCalls)
	}
	observed := decodeMicroVMRecoveryResponse(t, resp)
	if observed.Conversation.Status != models.SoulMintConversationStatusFailed ||
		observed.Conversation.Failure == nil ||
		observed.Conversation.Failure.Code != hostedGenesisFailureMicroVMUnavailable ||
		!observed.Conversation.Failure.Retryable ||
		observed.Conversation.Failure.Recovery.Action != hostedGenesisRecoveryRetrySameStep ||
		observed.Conversation.Failure.Recovery.MaxAttempts != 3 {
		t.Fatalf("registration observation did not author the typed retry contract: %#v", observed.Conversation)
	}
}

func assertMicroVMUnavailableRecoveryDispatch(t *testing.T, resp *apptheory.Response, dispatcher *stateReadingMicroVMDispatcher, stale models.HostedGenesisSession) hostedGenesisConversationResponse {
	t.Helper()
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 accepted recovery, got %#v", resp)
	}
	if dispatcher.reconcileCalls != 1 || dispatcher.calls != 1 || dispatcher.queueCalls != 0 {
		t.Fatalf("recovery must issue exactly one official run with no queue/nudge: reconcile=%d run=%d queue=%d", dispatcher.reconcileCalls, dispatcher.calls, dispatcher.queueCalls)
	}
	if dispatcher.lastBinding.ConversationID != stale.ConversationID || dispatcher.lastBinding.TurnID != stale.LatestTurnID {
		t.Fatalf("recovery did not replay the same durable accepted turn: %#v", dispatcher.lastBinding)
	}
	recovered := decodeMicroVMRecoveryResponse(t, resp)
	if recovered.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		recovered.Conversation.LatestTurnID != stale.LatestTurnID ||
		recovered.Conversation.MessageCount != stale.MessageCount ||
		recovered.Conversation.Failure != nil ||
		recovered.Conversation.PollAfterSeconds <= 0 {
		t.Fatalf("recovery did not preserve the accepted turn projection: %#v", recovered.Conversation)
	}
	return recovered
}

func decodeMicroVMRecoveryResponse(t *testing.T, resp *apptheory.Response) hostedGenesisConversationResponse {
	t.Helper()
	var decoded hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &decoded); err != nil {
		t.Fatalf("decode hosted genesis response: %v", err)
	}
	return decoded
}

func assertMicroVMRecoveryMessagesUnchanged(t *testing.T, messages []hostedGenesisConversationMessage) {
	t.Helper()
	wantMessages := []struct{ role, content string }{
		{role: "user", content: "describe the agent"},
		{role: "assistant", content: "prior actor answer"},
		{role: "user", content: "current accepted turn"},
	}
	if len(messages) != len(wantMessages) {
		t.Fatalf("recovery duplicated or removed an owner message: %#v", messages)
	}
	for i, want := range wantMessages {
		if messages[i].Role != want.role || messages[i].Content != want.content {
			t.Fatalf("recovery changed message %d: %#v", i, messages[i])
		}
	}
}

func assertMicroVMRecoveryDurableState(t *testing.T, durable models.HostedGenesisSession, stale models.HostedGenesisSession, originalLedger []byte) {
	t.Helper()
	gotLedger := encodeMicroVMRecoveryTurnLedger(t, durable.TurnLedger)
	if string(gotLedger) != string(originalLedger) || len(durable.TurnLedger) != 2 {
		t.Fatalf("recovery must not append a ledger entry: %s", gotLedger)
	}
	if durable.InputCheckpointRef != stale.InputCheckpointRef ||
		durable.VMCheckpoint == nil ||
		durable.VMCheckpoint.LatestTurnID != hostedGenesisMicroVMRecoveryPriorTurnID ||
		durable.Failure == nil ||
		durable.Failure.Recovery.MaxAttempts != 2 {
		t.Fatalf("recovery did not preserve checkpoints and decrement exactly one retry: %#v", durable)
	}
}

func encodeMicroVMRecoveryTurnLedger(t *testing.T, ledger []hostedgenesis.TurnLedgerEntry) []byte {
	t.Helper()
	encoded, err := json.Marshal(ledger)
	if err != nil {
		t.Fatalf("encode recovery turn ledger: %v", err)
	}
	return encoded
}

func microVMUnavailableRecoverySequenceFixtures(t *testing.T, reg models.SoulAgentRegistration) (models.HostedGenesisSession, models.SoulAgentMintConversation, models.HostedGenesisSession, models.SoulAgentMintConversation) {
	t.Helper()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	failedSession := failedMicroVMUnavailableRecoverySessionFixture(t, reg, now)
	liveSession := hostedGenesisH1D3RecoverySessionFixture(t, reg, hostedgenesis.StatusInProgress)
	failedSession.ExecutionStateRef = liveSession.ExecutionStateRef
	failedSession.MicroVMExecutionID = liveSession.MicroVMExecutionID
	failedSession.MicroVMLifecycleRef = liveSession.MicroVMLifecycleRef
	failedSession.Failure.Recovery.MaxAttempts = 3

	staleSession := *cloneHostedGenesisSession(&failedSession)
	staleSession.Status = string(hostedgenesis.StatusInProgress)
	staleSession.Version = failedSession.Version - 1
	staleSession.Failure = nil
	staleSession.CompletedAt = time.Time{}

	staleConversation := models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"},{"role":"assistant","content":"prior actor answer"},{"role":"user","content":"current accepted turn"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   staleSession.LatestTurnID,
		ChargedCredits: 2 * soulMintConversationStreamBaseCredits,
		RequestID:      "req-current-accepted",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
	}
	failedConversation := staleConversation
	failedConversation.Status = models.SoulMintConversationStatusFailed
	failedConversation.StatusReason = hostedGenesisFailureMicroVMUnavailable
	failedConversation.CompletedAt = now
	return staleSession, staleConversation, failedSession, failedConversation
}

func registrationWaitObservationFixtures(t *testing.T, reg models.SoulAgentRegistration) (models.HostedGenesisSession, models.SoulAgentMintConversation, models.HostedGenesisSession, models.SoulAgentMintConversation) {
	t.Helper()
	staleSession := hostedGenesisH1D3RecoverySessionFixture(t, reg, hostedgenesis.StatusInProgress)
	staleConversation := models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"private pending turn"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   "turn-stuck",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	}
	failedSession := staleSession
	failedSession.Status = string(hostedgenesis.StatusFailed)
	failedSession.Version++
	failedSession.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeMicroVMUnavailable),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeMicroVMUnavailable),
		},
	}
	failedConversation := staleConversation
	failedConversation.Status = models.SoulMintConversationStatusFailed
	failedConversation.StatusReason = string(hostedgenesis.FailureCodeMicroVMUnavailable)
	return staleSession, staleConversation, failedSession, failedConversation
}

// TestWaitOnlyReadObservesTerminalMicroVMAndConvergesGuardedHostTruth proves
// ordinary client polling remains wait-only: it issues AppTheory's canonical
// controller GET reconciliation, never dispatches/nudges a turn, and atomically
// converges a stale in_progress session plus compatibility projection to the
// typed microvm_unavailable failure under exact status/version/turn guards.
func TestWaitOnlyReadObservesTerminalMicroVMAndConvergesGuardedHostTruth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   runtimemicrovm.LifecycleState
		expired bool
	}{
		{name: "terminated", state: runtimemicrovm.StateTerminated},
		{name: "failed", state: runtimemicrovm.StateFailed},
		{name: "maximum duration expired", state: runtimemicrovm.StateStopped, expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runWaitOnlyReadObservationCase(t, tc.state, tc.expired)
		})
	}
}

func runWaitOnlyReadObservationCase(t *testing.T, state runtimemicrovm.LifecycleState, expired bool) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	identity := testMintConversationIdentity()
	identity.AgentID = reg.AgentID
	identity.Domain = reg.DomainNormalized
	dispatcher := &stubMicroVMDispatcher{t: t, observedState: state, expired: expired}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, identity, nil)
	stubMintConversationInstanceDomain(t, tdb, identity.Domain, soulInstanceBootstrapTestInstanceSlug)
	staleSession := hostedGenesisH1D3RecoverySessionFixture(t, reg, hostedgenesis.StatusInProgress)
	staleConversation := models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"private pending turn"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   "turn-stuck",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	}
	failedSession := staleSession
	failedSession.Status = string(hostedgenesis.StatusFailed)
	failedSession.Version++
	failedSession.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeMicroVMUnavailable),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeMicroVMUnavailable),
		},
	}
	failedConversation := staleConversation
	failedConversation.Status = models.SoulMintConversationStatusFailed
	failedConversation.StatusReason = string(hostedgenesis.FailureCodeMicroVMUnavailable)

	// Initial read-handler rows, CompletionWriter's guarded preflight rows, then
	// the post-transaction reload rows.
	stubSoulInstanceRecoverySession(t, tdb, staleSession)
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, staleSession)
	stubSoulInstanceRecoveryConversation(t, tdb, staleConversation)
	stubSoulInstanceRecoverySession(t, tdb, failedSession)
	stubSoulInstanceRecoveryConversation(t, tdb, failedConversation)
	expectWaitObservationFailureTransaction(t, tdb, staleSession.Version, staleSession.LatestTurnID)

	resp, err := s.handleSoulInstanceGetMintConversation(newMintConversationInstanceReadContext(
		reg.AgentID,
		mintConversationTestConversationID,
		nil,
	))
	if err != nil {
		t.Fatalf("wait-only read: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 wait-only projection, got %#v", resp)
	}
	if dispatcher.reconcileCalls != 1 || dispatcher.calls != 0 || dispatcher.queueCalls != 0 {
		t.Fatalf("client poll must observe exactly once and never nudge: reconcile=%d dispatch=%d queue=%d", dispatcher.reconcileCalls, dispatcher.calls, dispatcher.queueCalls)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusFailed || out.Conversation.Failure == nil || out.Conversation.Failure.Code != hostedGenesisFailureMicroVMUnavailable {
		t.Fatalf("terminal AppTheory observation did not converge typed Host truth: %#v", out.Conversation)
	}
	tdb.db.AssertExpectations(t)
	tx, ok := tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder)
	if !ok || !tx.AssertExpectations(t) {
		t.Fatal("guarded wait-observation transaction expectations were not satisfied")
	}
}

func expectWaitObservationFailureTransaction(t *testing.T, tdb *mintConversationTestDB, expectedVersion int64, expectedTurn string) {
	t.Helper()
	tx := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tx
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return observerHasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			observerHasVersionCondition(conditions, expectedVersion) &&
			observerHasFieldCondition(conditions, "Status", string(hostedgenesis.StatusInProgress)) &&
			observerHasFieldCondition(conditions, "LatestTurnID", expectedTurn)
	})).Return(tx).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed || session.Failure == nil || session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable {
			t.Fatalf("unexpected session failure projection: %#v", session)
		}
	})
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return observerHasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			observerHasFieldCondition(conditions, "Status", models.SoulMintConversationStatusInProgress) &&
			observerHasFieldCondition(conditions, "LatestTurnID", expectedTurn)
	})).Return(tx).Once()
	tx.On("Execute").Return(nil).Once()
}

func expectMicroVMUnavailableRecoverySequenceTransactions(
	t *testing.T,
	tdb *mintConversationTestDB,
	failed models.HostedGenesisSession,
	originalLedger []byte,
	capture func(*models.HostedGenesisSession),
) {
	t.Helper()
	tx, ok := tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder)
	if !ok || tx == nil {
		t.Fatal("registration observation transaction builder is unavailable")
	}
	// One transaction for observer convergence and two for guarded recovery:
	// failed -> in_progress before dispatch, then the fresh lifecycle ref.
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Times(3)
	expectMicroVMRecoveryPendingSessionWrite(t, tx, failed, originalLedger, capture)
	expectMicroVMRecoveryPendingConversationWrite(t, tx, failed.LatestTurnID)
	tx.On("Execute").Return(nil).Once()
	expectMicroVMRecoveryLifecycleWrite(t, tx, failed, originalLedger, capture)
	tx.On("Execute").Return(nil).Once()
}

func expectMicroVMRecoveryPendingSessionWrite(t *testing.T, tx *ttmocks.MockTransactionBuilder, failed models.HostedGenesisSession, originalLedger []byte, capture func(*models.HostedGenesisSession)) {
	t.Helper()
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return microVMRecoveryPendingConditionsMatch(conditions, failed.Version)
	})).Return(tx).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertMicroVMRecoveryPendingSession(t, session, failed, originalLedger)
		if capture != nil {
			capture(session)
		}
	})
}

func microVMRecoveryPendingConditionsMatch(conditions []core.TransactCondition, expectedVersion int64) bool {
	return observerHasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
		observerHasVersionCondition(conditions, expectedVersion) &&
		observerHasFieldCondition(conditions, "Status", string(hostedgenesis.StatusFailed))
}

func assertMicroVMRecoveryPendingSession(t *testing.T, session *models.HostedGenesisSession, failed models.HostedGenesisSession, originalLedger []byte) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != failed.LatestTurnID ||
		session.InputCheckpointRef != failed.InputCheckpointRef ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable ||
		session.Failure.Recovery.MaxAttempts != 2 ||
		session.VMCheckpoint == nil ||
		session.VMCheckpoint.LatestTurnID != hostedGenesisMicroVMRecoveryPriorTurnID {
		t.Fatalf("unexpected guarded retry-pending session: %#v", session)
	}
	if session.MicroVMExecutionID != "" || session.ExecutionStateRef != "" || session.MicroVMLifecycleRef != nil || session.AssistantCheckpointRef != "" || session.DeclarationCheckpoint != nil {
		t.Fatalf("retry-pending transition did not clear stale execution refs: %#v", session)
	}
	assertMicroVMRecoveryLedgerMatches(t, session, originalLedger)
}

func expectMicroVMRecoveryPendingConversationWrite(t *testing.T, tx *ttmocks.MockTransactionBuilder, latestTurnID string) {
	t.Helper()
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return observerHasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			observerHasFieldCondition(conditions, "Status", models.SoulMintConversationStatusFailed)
	})).Return(tx).Once().Run(func(args mock.Arguments) {
		conversation := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		if conversation.Status != models.SoulMintConversationStatusInProgress ||
			conversation.StatusReason != "" ||
			conversation.LatestTurnID != latestTurnID ||
			!conversation.CompletedAt.IsZero() {
			t.Fatalf("unexpected guarded retry-pending conversation: %#v", conversation)
		}
	})
}

func expectMicroVMRecoveryLifecycleWrite(t *testing.T, tx *ttmocks.MockTransactionBuilder, failed models.HostedGenesisSession, originalLedger []byte, capture func(*models.HostedGenesisSession)) {
	t.Helper()
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.MatchedBy(func(conditions []core.TransactCondition) bool {
		return observerHasConditionKind(conditions, core.TransactConditionKindPrimaryKeyExists) &&
			observerHasVersionCondition(conditions, failed.Version+1) &&
			observerHasFieldCondition(conditions, "Status", string(hostedgenesis.StatusInProgress))
	})).Return(tx).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" || session.MicroVMLifecycleRef == nil {
			t.Fatalf("official MicroVM run did not replace execution refs: %#v", session)
		}
		if session.MicroVMExecutionID == failed.MicroVMExecutionID || session.ExecutionStateRef == failed.ExecutionStateRef {
			t.Fatalf("official MicroVM run reused stale execution refs: %#v", session)
		}
		if session.Failure == nil || session.Failure.Recovery.MaxAttempts != 2 || session.InputCheckpointRef != failed.InputCheckpointRef {
			t.Fatalf("lifecycle write lost retry budget or current input: %#v", session)
		}
		assertMicroVMRecoveryLedgerMatches(t, session, originalLedger)
		if capture != nil {
			capture(session)
		}
	})
}

func assertMicroVMRecoveryLedgerMatches(t *testing.T, session *models.HostedGenesisSession, originalLedger []byte) {
	t.Helper()
	got := encodeMicroVMRecoveryTurnLedger(t, session.TurnLedger)
	if string(got) != string(originalLedger) {
		t.Fatalf("retry changed the durable turn ledger: %s", got)
	}
}

func observerHasConditionKind(conditions []core.TransactCondition, kind core.TransactConditionKind) bool {
	for _, condition := range conditions {
		if condition.Kind == kind {
			return true
		}
	}
	return false
}

func observerHasVersionCondition(conditions []core.TransactCondition, version int64) bool {
	for _, condition := range conditions {
		if condition.Kind == core.TransactConditionKindVersionEquals && condition.Value == version {
			return true
		}
	}
	return false
}

func observerHasFieldCondition(conditions []core.TransactCondition, field string, value any) bool {
	for _, condition := range conditions {
		if condition.Kind == core.TransactConditionKindField && condition.Field == field && condition.Operator == "=" && condition.Value == value {
			return true
		}
	}
	return false
}
