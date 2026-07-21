package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

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
