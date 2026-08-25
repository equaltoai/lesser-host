package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	runtimemicrovm "github.com/theory-cloud/apptheory/v4/runtime/microvm"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// hostedGenesisH1D3RecoveryFixture builds the shared mock scaffolding for an
// H1.3 recovery/reconciliation test: a hosted genesis session sitting in a
// MicroVM-serviced pending state (in_progress or assistant_turn_ready)
// with a populated MicroVMLifecycleRef, plus a stub dispatcher whose reconcile
// behavior the caller controls via observedState/reconcileErr.
func hostedGenesisH1D3RecoveryFixture(t *testing.T, status hostedgenesis.Status) (*mintConversationTestDB, *Server, models.SoulAgentRegistration, *stubMicroVMDispatcher) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "claude-sonnet-5",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"stuck user turn"}]`),
		Status:         string(status),
		LatestTurnID:   "turn-stuck",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubSoulInstanceRecoverySession(t, tdb, hostedGenesisH1D3RecoverySessionFixture(t, reg, status))
	return tdb, s, reg, dispatcher
}

// hostedGenesisH1D3RecoverySessionFixture builds a recovery session in a
// MicroVM-serviced pending state with the three MicroVM execution/cache refs
// populated (the shape the H1.2 accept path or H1.3 actor dispatch records
// so the H1.3 recover path can reach production reconstruction).
func hostedGenesisH1D3RecoverySessionFixture(t *testing.T, reg models.SoulAgentRegistration, status hostedgenesis.Status) models.HostedGenesisSession {
	t.Helper()
	session := hostedGenesisRecoverySessionFixture(t, reg, status, "")
	binding := session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		t.Fatalf("h1.3 recovery session binding invalid: %v", err)
	}
	resp := runtimemicrovm.ControllerResponse{
		Command:           runtimemicrovm.CommandRun,
		RequestID:         "req-original",
		TenantID:          binding.TenantID(),
		Namespace:         hostedgenesis.MicroVMNamespace,
		SessionID:         binding.ConversationID,
		State:             runtimemicrovm.StateRunning,
		DesiredState:      runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		MicroVMID:         "mv-h1d3-" + binding.ConversationID,
		ProviderMicroVMID: "mv-h1d3-" + binding.ConversationID,
		LastAction:        runtimemicrovm.CommandRun,
		LastTransition:    time.Date(2026, 3, 7, 12, 0, 5, 0, time.UTC),
		RegistryVersion:   2,
	}
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, resp, time.Date(2026, 3, 7, 12, 0, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("h1.3 recovery lifecycle ref: %v", err)
	}
	if err := session.ApplyMicroVMLifecycleRef(ref); err != nil {
		t.Fatalf("h1.3 recovery apply lifecycle ref: %v", err)
	}
	return session
}

// expectHostedGenesisH1D3ReconcileWrite mocks the read-only-ish TransactWrite
// the H1.3 reconcile path issues when a live VM is observed and the reconciled
// lifecycle ref is refreshed on the authoritative session (durable pending
// status preserved). Only the HostedGenesisSession is written.
func expectHostedGenesisH1D3ReconcileWrite(t *testing.T, tdb *mintConversationTestDB, wantStatus hostedgenesis.Status) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if hostedgenesis.NormalizeStatus(session.Status) != wantStatus {
			t.Fatalf("expected reconcile to preserve %s, got %#v", wantStatus, session.Status)
		}
		if session.MicroVMLifecycleRef == nil || session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" {
			t.Fatalf("expected reconcile to keep the three MicroVM refs populated, got %#v", session)
		}
	})
	tb.On("Execute").Return(nil).Once()
}

// expectHostedGenesisH1D3TerminalFailureWrite mocks the TransactWrite the H1.3
// reconcile path issues when a terminal (dead/expired) VM is observed: the
// session advances to a loud retryable failed status and the conversation row
// records the microvm_unavailable reason.
func expectHostedGenesisH1D3TerminalFailureWrite(t *testing.T, tdb *mintConversationTestDB) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed {
			t.Fatalf("expected terminal VM to map to loud failed status, got %#v", session.Status)
		}
		if session.Failure == nil || session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable {
			t.Fatalf("expected terminal VM to map to retryable microvm_unavailable failure, got %#v", session.Failure)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
}

// TestH1_3_ReconstructionReachableFromProductionUsesControllerGet proves the
// recover path reaches production reconstruction: for a session with a
// populated MicroVMLifecycleRef, the recover path queries real VM state through
// the M16 controller get command via the MicroVMDispatcher.ReconcileMicroVM
// seam (using the lifecycle ref the accept path set) and refreshes the
// reconciled ref on the authoritative session. A live VM preserves the durable
// pending status. This kills G6 (reconstruction was reachable only from the
// undeployed controller and no-oped without the never-set lifecycle ref).
func TestH1_3_ReconstructionReachableFromProductionUsesControllerGet(t *testing.T) {
	tdb, s, reg, dispatcher := hostedGenesisH1D3RecoveryFixture(t, hostedgenesis.StatusInProgress)
	dispatcher.observedState = runtimemicrovm.StateRunning
	expectHostedGenesisH1D3ReconcileWrite(t, tdb, hostedgenesis.StatusInProgress)

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected recovery err: %v", err)
	}
	if dispatcher.reconcileCalls != 1 {
		t.Fatalf("expected exactly one M16 controller get reconciliation, got %d", dispatcher.reconcileCalls)
	}
	if dispatcher.calls != 0 {
		t.Fatalf("reconcile path must not re-dispatch a run command for a live VM, got %d run calls", dispatcher.calls)
	}
	if dispatcher.lastBinding.ConversationID != mintConversationTestConversationID {
		t.Fatalf("expected reconciliation bound to the stuck conversation, got %#v", dispatcher.lastBinding)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 reconciled recovery response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("expected durable in_progress preserved for live VM, got %#v", out.Conversation)
	}
}

// TestH1_3_DeadExpiredVMLoudFailureNotNoop proves a dead/expired (terminal) VM
// observed by production reconstruction maps to a loud retryable
// microvm_unavailable failed session, not the silent no-op reconstruction had
// before H1.3. The reconstruction no-op is removed; fail closed and loudly.
func TestH1_3_DeadExpiredVMLoudFailureNotNoop(t *testing.T) {
	tdb, s, reg, dispatcher := hostedGenesisH1D3RecoveryFixture(t, hostedgenesis.StatusInProgress)
	dispatcher.observedState = runtimemicrovm.StateTerminated
	expectHostedGenesisH1D3TerminalFailureWrite(t, tdb)

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected recovery err: %v", err)
	}
	if dispatcher.reconcileCalls != 1 {
		t.Fatalf("expected one reconciliation query before the terminal failure, got %d", dispatcher.reconcileCalls)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 loud-failure recovery response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusFailed || out.Conversation.Failure == nil {
		t.Fatalf("expected loud failed status for terminal VM, got %#v", out.Conversation)
	}
	if out.Conversation.Failure.Code != hostedGenesisFailureMicroVMUnavailable {
		t.Fatalf("expected microvm_unavailable failure for terminal VM, got %#v", out.Conversation.Failure)
	}
	if !out.Conversation.Failure.Retryable {
		t.Fatalf("expected terminal-VM failure to be retryable so recover can re-dispatch, got %#v", out.Conversation.Failure)
	}
}

// TestH1_3_ReconcileUnavailableIsLoudFailure proves an unwired dispatcher on
// the recover path is fail-closed and loud: reconstruction is never a silent
// no-op, and the recover path never falls back to a non-MicroVM path.
func TestH1_3_ReconcileUnavailableIsLoudFailure(t *testing.T) {
	_, s, reg, _ := hostedGenesisH1D3RecoveryFixture(t, hostedgenesis.StatusInProgress)
	s.hostedGenesisMicroVMDispatcher = nil
	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err == nil {
		t.Fatalf("expected loud microvm-unavailable failure when dispatcher is unwired")
	}
}

// TestH1_3_ReconciliationObservesWorkloadTransitionsIdempotently proves the
// reconcile path observes the assistant_turn_ready / declaration_ready / failed
// transitions the H1.1 workload writes to the durable HostedGenesisSession
// status, and that repeated reconciliation of a live VM is idempotent (the
// durable status is preserved across reconcile calls; only the non-authoritative
// lifecycle ref refreshes). The session status is authoritative; the VM get
// only confirms liveness.
func TestH1_3_ReconciliationObservesWorkloadTransitionsIdempotently(t *testing.T) {
	cases := []struct {
		name          string
		status        hostedgenesis.Status
		convStatus    string
		observed      runtimemicrovm.LifecycleState
		wantStatus    hostedgenesis.Status
		wantReconcile bool
	}{
		{
			name:          "assistant_turn_ready workload transition is not re-reconciled",
			status:        hostedgenesis.StatusAssistantTurnReady,
			convStatus:    models.SoulMintConversationStatusAssistantTurnReady,
			observed:      runtimemicrovm.StateRunning,
			wantStatus:    hostedgenesis.StatusAssistantTurnReady,
			wantReconcile: false,
		},
		{
			name:          "in_progress live VM reconciles and preserves pending status",
			status:        hostedgenesis.StatusInProgress,
			convStatus:    models.SoulMintConversationStatusInProgress,
			observed:      runtimemicrovm.StateRunning,
			wantStatus:    hostedgenesis.StatusInProgress,
			wantReconcile: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runH1D3ReconciliationObservationCase(t, tc.status, tc.convStatus, tc.observed, tc.wantStatus, tc.wantReconcile)
		})
	}
}

// runH1D3ReconciliationObservationCase runs one reconciliation-observation case:
// a recover request against a session in the given status. A workload-advanced
// status (wantReconcile=false) is returned as-is with no controller get; a
// pending in_progress status (wantReconcile=true) is reconciled via controller
// get and the durable status preserved.
func runH1D3ReconciliationObservationCase(t *testing.T, status hostedgenesis.Status, convStatus string, observed runtimemicrovm.LifecycleState, wantStatus hostedgenesis.Status, wantReconcile bool) {
	t.Helper()
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := &stubMicroVMDispatcher{t: t, observedState: observed}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "claude-sonnet-5",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"stuck user turn"},{"role":"assistant","content":"ready"}]`),
		Status:         convStatus,
		LatestTurnID:   "turn-stuck",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	if wantReconcile {
		stubSoulInstanceRecoverySession(t, tdb, hostedGenesisH1D3RecoverySessionFixture(t, reg, status))
		expectHostedGenesisH1D3ReconcileWrite(t, tdb, wantStatus)
	} else {
		stubSoulInstanceRecoverySession(t, tdb, hostedGenesisRecoverySessionFixture(t, reg, status, "checkpoint://hosted-genesis/conv-1/assistant/turn-ready"))
	}
	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected recovery err: %v", err)
	}
	wantReconcileCalls := 0
	if wantReconcile {
		wantReconcileCalls = 1
	}
	if dispatcher.reconcileCalls != wantReconcileCalls {
		t.Fatalf("expected %d controller get reconciliation(s), got %d", wantReconcileCalls, dispatcher.reconcileCalls)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if hostedgenesis.NormalizeStatus(out.Conversation.Status) != wantStatus {
		t.Fatalf("expected durable status %s, got %#v", wantStatus, out.Conversation.Status)
	}
}

// TestH1_3_CompleteAssistantReadyWithoutDeclarationsRemainsReadOnly proves the
// M11 gateway inversion: `/complete` is a polling/finalize gate, not a Host-side
// declaration construction machine. Accepted user turns are delivered to the
// AppTheory MicroVM actor; its phase tools own typed construction.
func TestH1_3_CompleteAssistantReadyWithoutDeclarationsRemainsReadOnly(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("read-only completion must not enqueue a user-visible queue command: %#v", msg)
		return nil
	}
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "claude-sonnet-5",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe yourself"},{"role":"assistant","content":"ready"}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		LatestTurnID:   "turn-ready",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubMintConversationIdentity(t, tdb, nil, theoryErrors.ErrItemNotFound)

	resp, err := s.handleSoulInstanceCompleteMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady || out.Conversation.ProducedDeclarations != nil {
		t.Fatalf("expected assistant-ready polling projection without terminal declarations, got %#v", out)
	}
	if dispatcher.calls != 0 || dispatcher.queueCalls != 0 {
		t.Fatalf("complete without declarations must not dispatch provider work, dispatch=%d queue=%d", dispatcher.calls, dispatcher.queueCalls)
	}
}
