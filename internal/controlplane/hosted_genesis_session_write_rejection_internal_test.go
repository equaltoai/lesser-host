package controlplane

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// TestHostedGenesisWedgedLaneAdvanceIsTypedRecoverableNotInternal is the
// regression for equaltoai/lesser-host#1003.
//
// A lane whose durable HostedGenesisSession no longer satisfies its own model
// invariants (here: a partially written VM checkpoint left behind by a wedged
// turn) is rejected by BeforeUpdate inside the credit-debit transaction builder.
// That rejection used to escape as app.internal "failed to debit credits" and
// reach the instance as an untyped 500 soul_instance.internal: the wrong
// subsystem, and no recovery affordance. Because the rejection is deterministic
// on the stored row, every later advance repeated it and the lane could never
// reach the failed status the /recover path keys its recovery machinery off.
//
// The advance must now fail closed as a typed 409 soul_instance.conflict that
// names the recovery action, and must still not accept the turn or dispatch.
func TestHostedGenesisWedgedLaneAdvanceIsTypedRecoverableNotInternal(t *testing.T) {
	tdb, s, reg, dispatcher := hostedGenesisWedgedLaneFixture(t)

	_, err := s.handleSoulInstanceMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		mustMarshalJSON(t, soulMintConversationRequest{
			ConversationID: mintConversationTestConversationID,
			Message:        "advance the philosophy section",
		}),
		map[string]string{"id": reg.ID},
	))

	var appErr *apptheory.AppTheoryError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected a typed AppTheoryError, got %v", err)
	}
	if appErr.Code == soulInstanceBootstrapCodeInternal || appErr.StatusCode == http.StatusInternalServerError {
		t.Fatalf("wedged lane must not surface as an untyped internal error, got %s %d", appErr.Code, appErr.StatusCode)
	}
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected typed 409 %s, got %s %d", soulInstanceBootstrapCodeConflict, appErr.Code, appErr.StatusCode)
	}
	assertHostedGenesisRecoveryEnvelope(t, appErr.Details,
		hostedGenesisSessionWriteReasonInvalidState,
		hostedgenesis.RecoveryActionRestartSoulBootstrap,
		false,
	)
	if appErr.Details[hostedGenesisSessionWriteDetailRestartPath] != hostedGenesisRestartPath {
		t.Fatalf("restart recovery must name the restart path, got %#v", appErr.Details)
	}

	// Fail closed: the turn is not accepted and no MicroVM work is dispatched.
	if dispatcher.queueCalls != 0 || dispatcher.calls != 0 {
		t.Fatalf("rejected session write must not dispatch, queue=%d dispatch=%d", dispatcher.queueCalls, dispatcher.calls)
	}
	tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder).AssertNotCalled(t, "Create", mock.AnythingOfType("*models.SoulMintConversationIdempotency"), mock.Anything)
	tdb.db.TransactWriteBuilder.(*ttmocks.MockTransactionBuilder).AssertNotCalled(t, "UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything)
}

// TestHostedGenesisSessionWriteRejectionEnvelopes locks the two classifications
// a durable session write can produce. A model-invariant rejection is
// deterministic and needs a fresh bootstrap; an illegal status move is what a
// concurrent advance looks like and is retryable at the same step.
func TestHostedGenesisSessionWriteRejectionEnvelopes(t *testing.T) {
	cause := errors.New("declaration candidate binding does not match hosted genesis session")

	stateErr := hostedGenesisSessionWriteRejectionFrom(newHostedGenesisSessionStateRejection(cause))
	if stateErr == nil {
		t.Fatal("state rejection must be recoverable from the error chain")
	}
	if !errors.Is(stateErr, cause) {
		t.Fatalf("state rejection must wrap its cause, got %v", stateErr)
	}
	stateApp := stateErr.appTheoryError()
	if stateApp.Code != appErrCodeConflict || stateApp.StatusCode != http.StatusConflict {
		t.Fatalf("state rejection must be a conflict, got %s %d", stateApp.Code, stateApp.StatusCode)
	}
	assertHostedGenesisRecoveryEnvelope(t, stateApp.Details,
		hostedGenesisSessionWriteReasonInvalidState,
		hostedgenesis.RecoveryActionRestartSoulBootstrap,
		false,
	)

	transitionErr := hostedGenesisSessionWriteRejectionFrom(newHostedGenesisSessionTransitionRejection(hostedgenesis.ErrInvalidStatusTransition))
	if transitionErr == nil {
		t.Fatal("transition rejection must be recoverable from the error chain")
	}
	transitionApp := transitionErr.appTheoryError()
	assertHostedGenesisRecoveryEnvelope(t, transitionApp.Details,
		hostedGenesisSessionWriteReasonInvalidTransition,
		hostedgenesis.RecoveryActionRetrySameStep,
		true,
	)
	// retry_same_step is retried in place; it must not advertise a restart path.
	if _, ok := transitionApp.Details[hostedGenesisSessionWriteDetailRestartPath]; ok {
		t.Fatalf("retryable rejection must not advertise a restart path, got %#v", transitionApp.Details)
	}

	if hostedGenesisSessionWriteRejectionFrom(cause) != nil {
		t.Fatal("an unclassified error must not be read as a session write rejection")
	}
	if hostedGenesisSessionWriteRejectionFrom(nil) != nil {
		t.Fatal("a nil error must not be read as a session write rejection")
	}
}

func assertHostedGenesisRecoveryEnvelope(t *testing.T, details map[string]any, wantReason string, wantAction hostedgenesis.RecoveryAction, wantRetryable bool) {
	t.Helper()
	if details[hostedGenesisSessionWriteDetailReason] != wantReason {
		t.Fatalf("expected reason %q, got %#v", wantReason, details)
	}
	if details[hostedGenesisSessionWriteDetailRecoveryAction] != string(wantAction) {
		t.Fatalf("expected recovery action %q, got %#v", wantAction, details)
	}
	if details[hostedGenesisSessionWriteDetailRetryable] != wantRetryable {
		t.Fatalf("expected retryable %v, got %#v", wantRetryable, details)
	}
}

// hostedGenesisWedgedLaneFixture stubs a lane that already accepted its first
// turn and then wedged, leaving a durable VM checkpoint that no longer
// validates. The session status still accepts a turn, so the request reaches
// the accepted-turn persistence exactly as a real advance does.
func hostedGenesisWedgedLaneFixture(t *testing.T) (*mintConversationTestDB, *Server, models.SoulAgentRegistration, *stubMicroVMDispatcher) {
	t.Helper()
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

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)

	session := hostedGenesisRecoverySessionFixture(t, reg, hostedgenesis.StatusAssistantTurnReady, "")
	// The wedge: a checkpoint the durable row still carries but that no longer
	// passes validation (the runtime label never landed). BeforeUpdate rejects
	// it on every subsequent write, which is what made the lane permanent.
	checkpoint := hostedgenesis.VMCheckpointMetadata{
		Sequence:     1,
		Ref:          hostedgenesis.CheckpointRef("vm-actor", session.ConversationID, "wedged-1-"+session.LatestTurnID),
		Hash:         "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Step:         "assistant_turn",
		Action:       "run",
		StatusFrom:   string(hostedgenesis.StatusInProgress),
		StatusTo:     string(hostedgenesis.StatusAssistantTurnReady),
		Runtime:      "",
		LatestTurnID: session.LatestTurnID,
	}
	if checkpoint.Validate() == nil {
		t.Fatal("fixture must supply a checkpoint the session model rejects")
	}
	session.VMCheckpoint = &checkpoint
	stubSoulInstanceRecoverySession(t, tdb, session)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          session.Model,
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"define yourself"},{"role":"assistant","content":"identity accepted"}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		LatestTurnID:   session.LatestTurnID,
		CreatedAt:      session.CreatedAt,
		UpdatedAt:      session.UpdatedAt,
	})
	expectHostedGenesisWedgedLaneDebitAttempt(t, tdb)
	return tdb, s, reg, dispatcher
}

// expectHostedGenesisWedgedLaneDebitAttempt stubs the credit debit up to the
// point the transaction builder runs. The builder aborts on the rejected
// session write, so nothing after the usage ledger entry is ever staged and the
// transaction never executes.
func expectHostedGenesisWedgedLaneDebitAttempt(t *testing.T, tdb *mintConversationTestDB) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{
			InstanceSlug:    soulInstanceBootstrapTestInstanceSlug,
			Month:           time.Now().UTC().Format("2006-01"),
			IncludedCredits: 100,
			UsedCredits:     0,
		}
	}).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("Put", mock.AnythingOfType("*models.UsageLedgerEntry"), mock.Anything).Return(tb).Once()
}
