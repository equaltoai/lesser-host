package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const hostedGenesisMicroVMRecoveryTurnID = "turn-microvm"

func TestSoulInstanceRecoverMintConversation_RetriggersStuckTurn(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"stuck user turn"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   "turn-stuck",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubSoulInstanceRecoverySession(t, tdb, hostedGenesisRecoverySessionFixture(t, reg, hostedgenesis.StatusInProgress, ""))
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusInProgress)

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected recovery err: %v", err)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("expected recovery to redispatch the stuck turn via the MicroVM controller, got %d dispatch calls", dispatcher.calls)
	}
	if dispatcher.lastBinding.ConversationID != mintConversationTestConversationID {
		t.Fatalf("expected recovery dispatch bound to the stuck conversation, got %#v", dispatcher.lastBinding)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 dispatched recovery response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("expected in_progress dispatched recovery, got %#v", out.Conversation)
	}
	for _, msg := range out.Conversation.Messages {
		if msg.Role == hostedGenesisTranscriptRoleAssistant {
			t.Fatalf("recovery dispatch must not append an inline assistant message: %#v", out.Conversation.Messages)
		}
	}
	if strings.Contains(string(resp.Body), mintConversationInstanceReadTestRawKey) {
		t.Fatalf("recovery response leaked credential material: %s", string(resp.Body))
	}
}

func TestSoulInstanceRecoverMintConversation_NonStuckIsNoop(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	s.hostedGenesisAssistantRunner = func(_ context.Context, in hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		t.Fatalf("non-stuck recovery must not rerun assistant turn: %#v", in)
		return hostedGenesisAssistantRunResult{}, nil
	}

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"already done"},{"role":"assistant","content":"ready"}]`),
		Status:         models.SoulMintConversationStatusAssistantTurnReady,
		LatestTurnID:   "turn-ready",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
	})
	stubSoulInstanceRecoverySession(t, tdb, hostedGenesisRecoverySessionFixture(t, reg, hostedgenesis.StatusAssistantTurnReady, "checkpoint://hosted-genesis/conv-1/assistant/turn-ready"))

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected recovery err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 noop response, got %#v", resp)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusAssistantTurnReady ||
		len(out.Conversation.Messages) != 2 ||
		out.Conversation.Messages[1].Content != "ready" {
		t.Fatalf("expected current assistant-ready state without recovery mutation, got %#v", out.Conversation)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func TestSoulInstanceRecoverMintConversation_RestartRequiredReturnsActionableConflict(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubFailedRecoveryLegacyMintConversation(t, tdb, reg, now)
	session := failedRecoveryHostedGenesisSessionFixture(t, reg, now)
	session.Failure = &hostedgenesis.Failure{
		Code:    hostedgenesis.FailureCodeInvalidProducedDeclarations,
		Message: hostedgenesis.FailureMessage(hostedgenesis.FailureCodeInvalidProducedDeclarations),
		Recovery: hostedgenesis.Recovery{
			Action: hostedgenesis.RecoveryActionRestartSoulBootstrap,
			Reason: string(hostedgenesis.DeclarationCodeCapabilities),
		},
	}
	stubSoulInstanceRecoverySession(t, tdb, session)

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected restart-required conflict, got %#v", appErr)
	}
	if appErr.Details["recovery_action"] != string(hostedgenesis.RecoveryActionRestartSoulBootstrap) ||
		appErr.Details["restart_path"] != "/api/v1/soul/instance/agents/register/begin" {
		t.Fatalf("expected actionable restart details, got %#v", appErr.Details)
	}
	if strings.Contains(appErr.Message, "capabilities") || strings.Contains(appErr.Message, "transcript") {
		t.Fatalf("conflict message leaked declaration detail: %#v", appErr)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func TestSoulInstanceRecoverMintConversation_RetriesFailedDeclarationExtraction(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"},{"role":"assistant","content":"ready for declaration"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureDeclarationExtractionFailed,
		LatestTurnID:   "turn-secret",
		ChargedCredits: 110,
		RequestID:      "req-failed",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	})
	stubSoulInstanceRecoverySession(t, tdb, failedRecoveryHostedGenesisSessionFixture(t, reg, now))
	expectSoulInstanceMintConversationExtractionDebit(t, tdb)
	expectHostedGenesisExtractionDispatchWrite(t, tdb)

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected declaration extraction recovery err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 retry response, got %#v", resp)
	}
	if dispatcher.calls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected one extraction redispatch and no reconcile, got run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	if dispatcher.lastBinding.ConversationID != mintConversationTestConversationID || dispatcher.lastBinding.TurnID != "turn-secret" {
		t.Fatalf("expected retry dispatch bound to latest failed turn, got %#v", dispatcher.lastBinding)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusDeclarationExtractionPending || out.Conversation.Failure != nil {
		t.Fatalf("expected actionable declaration extraction retry, got %#v", out.Conversation)
	}
	assertHostedGenesisResponseNoForbiddenValues(t, resp.Body, hostedGenesisStatusForbiddenValues())
}

func TestSoulInstanceRecoverMintConversation_RetriesFailedAssistantTurn(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureAssistantTurnFailed,
		LatestTurnID:   "turn-assistant",
		ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID:      "req-failed",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	})
	session := failedAssistantTurnRecoveryHostedGenesisSessionFixture(t, reg, now)
	stubSoulInstanceRecoverySession(t, tdb, session)
	expectHostedGenesisAssistantTurnRetryDispatchWrite(t, tdb, "turn-assistant")

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected assistant turn recovery err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 assistant retry response, got %#v", resp)
	}
	if dispatcher.calls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected one assistant redispatch and no reconcile, got run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	if dispatcher.lastBinding.ConversationID != mintConversationTestConversationID || dispatcher.lastBinding.TurnID != "turn-assistant" {
		t.Fatalf("expected assistant retry dispatch bound to latest failed turn, got %#v", dispatcher.lastBinding)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		out.Conversation.LatestTurnID != "turn-assistant" ||
		out.Conversation.Failure != nil ||
		out.Conversation.PollAfterSeconds <= 0 {
		t.Fatalf("expected actionable assistant turn retry projection, got %#v", out.Conversation)
	}
	assertHostedGenesisResponseNoForbiddenValues(t, resp.Body, hostedGenesisStatusForbiddenValues())
	tdb.qBudget.AssertNumberOfCalls(t, "First", 0)
}

func TestSoulInstanceRecoverMintConversation_AssistantRetryPersistsPendingBeforeDispatch(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	durableSession := failedAssistantTurnRecoveryHostedGenesisSessionFixture(t, reg, now)
	dispatcher := &stateReadingMicroVMDispatcher{
		stubMicroVMDispatcher: &stubMicroVMDispatcher{t: t},
		readSession: func() *models.HostedGenesisSession {
			return cloneHostedGenesisSession(&durableSession)
		},
	}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureAssistantTurnFailed,
		LatestTurnID:   "turn-assistant",
		ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID:      "req-failed",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	})
	stubSoulInstanceRecoverySession(t, tdb, durableSession)
	expectHostedGenesisAssistantTurnRetryDispatchWriteWithCapture(t, tdb, "turn-assistant", func(session *models.HostedGenesisSession) {
		durableSession = *cloneHostedGenesisSession(session)
	})

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected assistant turn recovery err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 assistant retry response, got %#v", resp)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("expected one assistant redispatch, got %d", dispatcher.calls)
	}
}

type stateReadingMicroVMDispatcher struct {
	*stubMicroVMDispatcher
	readSession         func() *models.HostedGenesisSession
	expectedFailureCode hostedgenesis.FailureCode
}

func (d *stateReadingMicroVMDispatcher) DispatchMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	session := d.readSession()
	if session == nil {
		d.t.Fatalf("workload test double could not read durable Host state")
	}
	expectedFailureCode := d.expectedFailureCode
	if expectedFailureCode == "" {
		expectedFailureCode = hostedgenesis.FailureCodeAssistantTurnFailed
	}
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != binding.TurnID ||
		session.Failure == nil ||
		session.Failure.Code != expectedFailureCode ||
		session.Failure.Recovery.MaxAttempts != 1 {
		d.t.Fatalf("workload observed old or invalid durable retry state during dispatch: %#v", session)
	}
	if expectedFailureCode == hostedgenesis.FailureCodeMicroVMUnavailable && session.VMCheckpoint == nil {
		d.t.Fatalf("microvm relaunch did not expose the validated VM checkpoint to the workload: %#v", session)
	}
	return d.stubMicroVMDispatcher.DispatchMicroVMRun(ctx, requestID, binding)
}

func TestSoulInstanceRecoverMintConversation_FailedAssistantRetryDispatchErrorPersistsLoudFailure(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	dispatcher := &stubMicroVMDispatcher{t: t, dispatchErr: errors.New("controller invoke failed")}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureAssistantTurnFailed,
		LatestTurnID:   "turn-assistant",
		ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID:      "req-failed",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	})
	session := failedAssistantTurnRecoveryHostedGenesisSessionFixture(t, reg, now)
	stubSoulInstanceRecoverySession(t, tdb, session)
	expectHostedGenesisAssistantTurnRetryDispatchFailureWrites(t, tdb, "turn-assistant")

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeMicroVMUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected loud microvm_unavailable dispatch failure, got %#v", appErr)
	}
	if dispatcher.calls != 1 {
		t.Fatalf("expected dispatch attempt after durable retry transition, got %d", dispatcher.calls)
	}
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 2)
}

func TestSoulInstanceRecoverMintConversation_SalvagesPendingAssistantRetryWithLifecycleRef(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status:         models.SoulMintConversationStatusInProgress,
		LatestTurnID:   "turn-stuck",
		ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID:      "req-live-bad",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC),
	})
	session := hostedGenesisH1D3RecoverySessionFixture(t, reg, hostedgenesis.StatusInProgress)
	session.Failure = failedAssistantTurnRecoveryFailure()
	session.AssistantCheckpointRef = ""
	stubSoulInstanceRecoverySession(t, tdb, session)
	expectHostedGenesisAssistantTurnRetryDispatchWrite(t, tdb, "turn-stuck")

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected pending assistant retry salvage err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 salvage redispatch response, got %#v", resp)
	}
	if dispatcher.calls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected live-bad shape to redispatch instead of reconciling pending forever, run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress || out.Conversation.Failure != nil || out.Conversation.PollAfterSeconds <= 0 {
		t.Fatalf("expected unambiguous pending retry projection without exposed failure, got %#v", out.Conversation)
	}
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 2)
}

func TestSoulInstanceRecoverMintConversation_RelaunchesMicroVMUnavailableFromCheckpoint(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	durableSession := failedMicroVMUnavailableRecoverySessionFixture(t, reg, now)
	dispatcher := &stateReadingMicroVMDispatcher{
		stubMicroVMDispatcher: &stubMicroVMDispatcher{t: t},
		expectedFailureCode:   hostedgenesis.FailureCodeMicroVMUnavailable,
		readSession: func() *models.HostedGenesisSession {
			return cloneHostedGenesisSession(&durableSession)
		},
	}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureMicroVMUnavailable,
		LatestTurnID:   hostedGenesisMicroVMRecoveryTurnID,
		ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID:      "req-failed",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	})
	stubSoulInstanceRecoverySession(t, tdb, durableSession)
	expectHostedGenesisMicroVMRetryDispatchWriteWithCapture(t, tdb, hostedGenesisMicroVMRecoveryTurnID, func(session *models.HostedGenesisSession) {
		durableSession = *cloneHostedGenesisSession(session)
	})

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected microvm-unavailable recovery err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 microvm relaunch response, got %#v", resp)
	}
	if dispatcher.calls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected one relaunch dispatch and no reconcile, got run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	if dispatcher.lastBinding.ConversationID != mintConversationTestConversationID || dispatcher.lastBinding.TurnID != hostedGenesisMicroVMRecoveryTurnID {
		t.Fatalf("expected relaunch dispatch bound to failed turn, got %#v", dispatcher.lastBinding)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		out.Conversation.LatestTurnID != hostedGenesisMicroVMRecoveryTurnID ||
		out.Conversation.Failure != nil ||
		out.Conversation.PollAfterSeconds <= 0 {
		t.Fatalf("expected actionable microvm relaunch projection, got %#v", out.Conversation)
	}
	assertHostedGenesisResponseNoForbiddenValues(t, resp.Body, hostedGenesisStatusForbiddenValues())
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 2)
}

func TestSoulInstanceRecoverMintConversation_RejectsMicroVMRelaunchWithoutCheckpoint(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureMicroVMUnavailable,
		LatestTurnID:   hostedGenesisMicroVMRecoveryTurnID,
		ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID:      "req-failed",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	})
	session := failedMicroVMUnavailableRecoverySessionFixture(t, reg, now)
	session.VMCheckpoint = nil
	stubSoulInstanceRecoverySession(t, tdb, session)

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected missing checkpoint conflict, got %#v", appErr)
	}
	if dispatcher.calls != 0 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("checkpoint-less retry must not dispatch, got run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func TestHostedGenesisMicroVMRecoveryCheckpointValidationGuardsDurableInvariants(t *testing.T) {
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	valid := failedMicroVMUnavailableRecoverySessionFixture(t, reg, now)
	if err := validateHostedGenesisMicroVMRecoveryCheckpoint(&valid); err != nil {
		t.Fatalf("valid recovery checkpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*models.HostedGenesisSession){
		"latest turn mismatch": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.LatestTurnID = "turn-other"
		},
		"turn missing from ledger": func(session *models.HostedGenesisSession) {
			session.TurnLedger = nil
		},
		"conversation ref mismatch": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.Ref = hostedgenesis.CheckpointRef("vm-actor", "other-conversation", fmt.Sprintf("%s-%d-%s", session.VMCheckpoint.Step, session.VMCheckpoint.Sequence, session.VMCheckpoint.LatestTurnID))
		},
		"failed checkpoint status": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.StatusTo = string(hostedgenesis.StatusFailed)
		},
		"checkpoint ahead of version budget": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.Sequence = session.Version + 2
		},
	} {
		t.Run(name, func(t *testing.T) {
			session := *cloneHostedGenesisSession(&valid)
			mutate(&session)
			if err := validateHostedGenesisMicroVMRecoveryCheckpoint(&session); err == nil {
				t.Fatalf("expected invalid recovery checkpoint")
			}
		})
	}
}

func TestSoulInstanceRecoverMintConversation_ExhaustedDeclarationExtractionRetryRequiresRestart(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubFailedRecoveryLegacyMintConversation(t, tdb, reg, now)
	session := failedRecoveryHostedGenesisSessionFixture(t, reg, now)
	session.Failure.Recovery.MaxAttempts = 0
	stubSoulInstanceRecoverySession(t, tdb, session)

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected exhausted retry conflict, got %#v", appErr)
	}
	if appErr.Details["recovery_action"] != string(hostedgenesis.RecoveryActionRestartSoulBootstrap) {
		t.Fatalf("expected restart action for exhausted retry, got %#v", appErr.Details)
	}
	if dispatcher.calls != 0 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("exhausted retry must not dispatch, got run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func TestSoulInstanceRecoverMintConversation_NonRetryableDeclarationExtractionRequiresRestart(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubFailedRecoveryLegacyMintConversation(t, tdb, reg, now)
	session := failedRecoveryHostedGenesisSessionFixture(t, reg, now)
	session.Failure.Retryable = false
	session.Failure.Recovery.MaxAttempts = 2
	stubSoulInstanceRecoverySession(t, tdb, session)

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeConflict || appErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected unsafe retry conflict, got %#v", appErr)
	}
	if appErr.Details["recovery_action"] != string(hostedgenesis.RecoveryActionRestartSoulBootstrap) {
		t.Fatalf("expected restart action for unsafe retry, got %#v", appErr.Details)
	}
	if dispatcher.calls != 0 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("unsafe retry must not dispatch, got run=%d reconcile=%d", dispatcher.calls, dispatcher.reconcileCalls)
	}
	tdb.db.AssertNotCalled(t, "TransactWrite", mock.Anything, mock.Anything)
}

func TestSoulInstanceGetRegistrationMintConversation_ReturnsRecoveryWithoutQueueOrLeaks(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("status/recovery reads must not depend on hosted genesis SQS: %#v", msg)
		return nil
	}
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	stubFailedRecoveryLegacyMintConversation(t, tdb, reg, now)
	stubFailedRecoveryHostedGenesisSession(t, tdb, reg, now)

	resp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertHostedGenesisFailedRecoveryProjection(t, out.Conversation)
	assertHostedGenesisResponseNoForbiddenValues(t, resp.Body, hostedGenesisStatusForbiddenValues())
	assertHostedGenesisStatusAuditNoLeaks(t, tdb, append(hostedGenesisStatusForbiddenValues(), soulInstanceBootstrapTestInstanceSlug, reg.AgentID, mintConversationTestConversationID))
}

func stubSoulInstanceRecoveryConversation(t *testing.T, tdb *mintConversationTestDB, conv models.SoulAgentMintConversation) {
	t.Helper()
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		*dest = conv
	}).Once()
}

func stubSoulInstanceRecoverySession(t *testing.T, tdb *mintConversationTestDB, session models.HostedGenesisSession) {
	t.Helper()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = session
	}).Once()
}

func hostedGenesisRecoverySessionFixture(t *testing.T, reg models.SoulAgentRegistration, status hostedgenesis.Status, assistantCheckpointRef string) models.HostedGenesisSession {
	t.Helper()
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	session := models.HostedGenesisSession{
		Version:                3,
		InstanceSlug:           soulInstanceBootstrapTestInstanceSlug,
		RegistrationID:         reg.ID,
		AgentID:                reg.AgentID,
		ConversationID:         mintConversationTestConversationID,
		Status:                 string(status),
		Model:                  "anthropic:claude-sonnet-4-6",
		LatestTurnID:           "turn-stuck",
		MessageCount:           1,
		AssistantCheckpointRef: strings.TrimSpace(assistantCheckpointRef),
		TurnLedger: []hostedgenesis.TurnLedgerEntry{{
			TurnID:         "turn-stuck",
			ChargedCredits: soulMintConversationStreamBaseCredits,
			MessageCount:   1,
			AcceptedAt:     now,
		}},
		RequestID: "req-original",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if status == hostedgenesis.StatusAssistantTurnReady {
		session.LatestTurnID = "turn-ready"
		session.MessageCount = 2
		session.TurnLedger[0].TurnID = "turn-ready"
	}
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("session fixture: %v", err)
	}
	return session
}

func stubFailedRecoveryLegacyMintConversation(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration, now time.Time) {
	t.Helper()
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"raw transcript secret"},{"role":"assistant","content":"private answer"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		LatestTurnID:   "turn-secret",
		RequestID:      "req-failed",
		CorrelationID:  "corr-safe",
		IdempotencyKey: "legacy-row-has-explicit-session",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
	})
}

func stubFailedRecoveryHostedGenesisSession(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration, now time.Time) {
	t.Helper()
	dbSession := failedRecoveryHostedGenesisSessionFixture(t, reg, now)
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = dbSession
	}).Once()
}

func failedRecoveryHostedGenesisSessionFixture(t *testing.T, reg models.SoulAgentRegistration, now time.Time) models.HostedGenesisSession {
	t.Helper()
	session := models.HostedGenesisSession{
		InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
		RegistrationID: reg.ID,
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         string(hostedgenesis.StatusFailed),
		Model:          "anthropic:claude-sonnet-4-6",
		LatestTurnID:   "turn-secret",
		MessageCount:   2,
		TurnLedger:     failedRecoveryTurnLedger(now),
		RequestID:      "req-failed",
		TraceIDs:       failedRecoveryTraceIDs(),
		Failure:        failedRecoveryDeclarationFailure(),
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	}
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("session fixture: %v", err)
	}
	return session
}

func failedRecoveryTurnLedger(now time.Time) []hostedgenesis.TurnLedgerEntry {
	return []hostedgenesis.TurnLedgerEntry{{
		TurnID:         "turn-secret",
		IdempotencyKey: "idem-failed",
		RequestHash:    strings.Repeat("a", 64),
		ChargedCredits: soulMintConversationStreamBaseCredits,
		MessageCount:   2,
		AcceptedAt:     now.Add(-5 * time.Minute),
	}}
}

func failedRecoveryTraceIDs() *hostedgenesis.TraceIDs {
	return &hostedgenesis.TraceIDs{HostRequestID: "req-failed", CorrelationID: "corr-safe", IdempotencyKey: "idem-failed"}
}

func failedRecoveryDeclarationFailure() *hostedgenesis.Failure {
	return &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Message:   hostedGenesisFailureMessage(hostedGenesisFailureDeclarationExtractionFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			Reason:            hostedGenesisFailureDeclarationExtractionFailed,
			MaxAttempts:       3,
			RetryAfterSeconds: 30,
		},
	}
}

func failedAssistantTurnRecoveryHostedGenesisSessionFixture(t *testing.T, reg models.SoulAgentRegistration, now time.Time) models.HostedGenesisSession {
	t.Helper()
	session := models.HostedGenesisSession{
		InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
		RegistrationID: reg.ID,
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         string(hostedgenesis.StatusFailed),
		Model:          "anthropic:claude-sonnet-4-6",
		LatestTurnID:   "turn-assistant",
		MessageCount:   1,
		TurnLedger: []hostedgenesis.TurnLedgerEntry{{
			TurnID:         "turn-assistant",
			ChargedCredits: soulMintConversationStreamBaseCredits,
			MessageCount:   1,
			AcceptedAt:     now.Add(-5 * time.Minute),
		}},
		RequestID:   "req-failed",
		TraceIDs:    &hostedgenesis.TraceIDs{HostRequestID: "req-failed", CorrelationID: "corr-safe"},
		Failure:     failedAssistantTurnRecoveryFailure(),
		CreatedAt:   time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:   now,
		CompletedAt: now,
	}
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("session fixture: %v", err)
	}
	return session
}

func failedAssistantTurnRecoveryFailure() *hostedgenesis.Failure {
	return &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   hostedGenesisFailureMessage(hostedGenesisFailureAssistantTurnFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			Reason:            hostedGenesisFailureAssistantTurnFailed,
			MaxAttempts:       2,
			RetryAfterSeconds: 5,
		},
	}
}

func failedMicroVMUnavailableRecoverySessionFixture(t *testing.T, reg models.SoulAgentRegistration, now time.Time) models.HostedGenesisSession {
	t.Helper()
	session := failedAssistantTurnRecoveryHostedGenesisSessionFixture(t, reg, now)
	session.LatestTurnID = hostedGenesisMicroVMRecoveryTurnID
	session.MessageCount = 1
	session.TurnLedger = []hostedgenesis.TurnLedgerEntry{{
		TurnID:         hostedGenesisMicroVMRecoveryTurnID,
		ChargedCredits: soulMintConversationStreamBaseCredits,
		MessageCount:   1,
		AcceptedAt:     now.Add(-5 * time.Minute),
	}}
	session.Failure = failedMicroVMUnavailableRecoveryFailure()
	checkpoint, err := hostedgenesis.NewVMCheckpointMetadata(hostedgenesis.VMCheckpointInput{
		ConversationID:     session.ConversationID,
		LatestTurnID:       hostedGenesisMicroVMRecoveryTurnID,
		RequestID:          "req-checkpoint",
		Sequence:           session.Version + 1,
		Step:               "assistant_turn",
		Action:             "ask",
		StatusFrom:         hostedgenesis.StatusInProgress,
		StatusTo:           hostedgenesis.StatusAssistantTurnReady,
		Runtime:            "hosted-genesis-microvm-workload/v1",
		ProviderFamily:     "anthropic",
		ModelID:            "claude-sonnet-4-6",
		AdditionalHashSalt: "checkpoint-salt",
	})
	if err != nil {
		t.Fatalf("microvm recovery checkpoint fixture: %v", err)
	}
	session.VMCheckpoint = &checkpoint
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("microvm unavailable fixture: %v", err)
	}
	return session
}

func failedMicroVMUnavailableRecoveryFailure() *hostedgenesis.Failure {
	return &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Message:   hostedGenesisFailureMessage(hostedGenesisFailureMicroVMUnavailable),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			Reason:            hostedGenesisFailureMicroVMUnavailable,
			MaxAttempts:       2,
			RetryAfterSeconds: 5,
		},
	}
}

func expectHostedGenesisAssistantTurnRetryDispatchWrite(t *testing.T, tdb *mintConversationTestDB, wantTurnID string) {
	t.Helper()
	expectHostedGenesisAssistantTurnRetryDispatchWriteWithCapture(t, tdb, wantTurnID, nil)
}

func expectHostedGenesisAssistantTurnRetryDispatchWriteWithCapture(t *testing.T, tdb *mintConversationTestDB, wantTurnID string, capture func(*models.HostedGenesisSession)) {
	t.Helper()
	expectHostedGenesisRetryDispatchWriteWithCapture(t, tdb, wantTurnID, "assistant", assertHostedGenesisAssistantRetryPendingSession, assertHostedGenesisAssistantRetryLifecycleSession, capture)
}

func expectHostedGenesisMicroVMRetryDispatchWriteWithCapture(t *testing.T, tdb *mintConversationTestDB, wantTurnID string, capture func(*models.HostedGenesisSession)) {
	t.Helper()
	expectHostedGenesisRetryDispatchWriteWithCapture(t, tdb, wantTurnID, "microvm", assertHostedGenesisMicroVMRetryPendingSession, assertHostedGenesisMicroVMRetryLifecycleSession, capture)
}

func expectHostedGenesisRetryDispatchWriteWithCapture(
	t *testing.T,
	tdb *mintConversationTestDB,
	wantTurnID string,
	label string,
	assertPending func(*testing.T, *models.HostedGenesisSession, string),
	assertLifecycle func(*testing.T, *models.HostedGenesisSession, string),
	capture func(*models.HostedGenesisSession),
) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertPending(t, session, wantTurnID)
		if capture != nil {
			capture(session)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		conv := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		if conv.Status != models.SoulMintConversationStatusInProgress ||
			conv.StatusReason != "" ||
			conv.LatestTurnID != wantTurnID ||
			!conv.CompletedAt.IsZero() {
			t.Fatalf("expected %s retry conversation alignment, got %#v", label, conv)
		}
	})
	tb.On("Execute").Return(nil).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertLifecycle(t, session, wantTurnID)
		if capture != nil {
			capture(session)
		}
	})
	tb.On("Execute").Return(nil).Once()
}

func expectHostedGenesisAssistantTurnRetryDispatchFailureWrites(t *testing.T, tdb *mintConversationTestDB, wantTurnID string) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertHostedGenesisAssistantRetryPendingSession(t, session, wantTurnID)
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertHostedGenesisAssistantRetryFailedSession(t, session, wantTurnID)
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
}

func assertHostedGenesisAssistantRetryPendingSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected assistant retry session to persist latest turn with carried budget, got %#v", session)
	}
	if session.MicroVMExecutionID != "" || session.ExecutionStateRef != "" || session.MicroVMLifecycleRef != nil {
		t.Fatalf("retry-pending state must be durable before MicroVM dispatch refs are known, got %#v", session)
	}
}

func assertHostedGenesisAssistantRetryLifecycleSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected assistant retry lifecycle write to preserve carried budget, got %#v", session)
	}
	if session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" || session.MicroVMLifecycleRef == nil {
		t.Fatalf("expected assistant retry dispatch to refresh MicroVM refs, got %#v", session)
	}
}

func assertHostedGenesisMicroVMRetryPendingSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected microvm retry session to persist latest turn with carried budget, got %#v", session)
	}
	if session.VMCheckpoint == nil {
		t.Fatalf("microvm retry must preserve validated VM checkpoint for relaunch/replay: %#v", session)
	}
	if session.MicroVMExecutionID != "" || session.ExecutionStateRef != "" || session.MicroVMLifecycleRef != nil {
		t.Fatalf("retry-pending state must be durable before MicroVM dispatch refs are known, got %#v", session)
	}
}

func assertHostedGenesisMicroVMRetryLifecycleSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected microvm retry lifecycle write to preserve carried budget, got %#v", session)
	}
	if session.VMCheckpoint == nil {
		t.Fatalf("microvm retry lifecycle write must preserve VM checkpoint, got %#v", session)
	}
	if session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" || session.MicroVMLifecycleRef == nil {
		t.Fatalf("expected microvm retry dispatch to refresh MicroVM refs, got %#v", session)
	}
}

func assertHostedGenesisAssistantRetryFailedSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
		!session.Failure.Retryable ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected loud retryable assistant failure after rejected dispatch, got %#v", session)
	}
}

func assertHostedGenesisFailedRecoveryProjection(t *testing.T, conversation hostedGenesisConversationProjection) {
	t.Helper()
	if conversation.Status != models.SoulMintConversationStatusFailed || conversation.Failure == nil {
		t.Fatalf("expected durable failed recovery guidance, got %#v", conversation)
	}
	if conversation.Failure.Code != hostedGenesisFailureDeclarationExtractionFailed || conversation.Failure.Recovery.Action != hostedGenesisRecoveryRetrySameStep {
		t.Fatalf("expected declaration-extraction retry guidance, got %#v", conversation.Failure)
	}
}

func hostedGenesisStatusForbiddenValues() []string {
	return []string{
		"raw transcript secret",
		"private answer",
		mintConversationInstanceReadTestRawKey,
		"Bearer ",
		"provider_secret",
		"microvm_endpoint_token",
		"shell-auth-token",
	}
}

func assertHostedGenesisResponseNoForbiddenValues(t *testing.T, body []byte, forbiddenValues []string) {
	t.Helper()
	text := string(body)
	for _, forbidden := range forbiddenValues {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status/recovery response leaked forbidden value %q: %s", forbidden, text)
		}
	}
}

func assertHostedGenesisStatusAuditNoLeaks(t *testing.T, tdb *mintConversationTestDB, forbiddenValues []string) {
	t.Helper()
	if len(tdb.auditModels) == 0 {
		t.Fatalf("expected instance status read audit event")
	}
	auditText := ""
	for _, entry := range tdb.auditModels {
		auditText += entry.Actor + "\n" + entry.Action + "\n" + entry.Target + "\n"
	}
	assertHostedGenesisAuditNoForbiddenValues(t, auditText, forbiddenValues)
	if !strings.Contains(auditText, "soul.mint_conversation.instance_read") || !strings.Contains(auditText, "sha256:") || !strings.Contains(auditText, "status=200") {
		t.Fatalf("expected safe hashed audit categories/status only, got %s", auditText)
	}
}

func assertHostedGenesisAuditNoForbiddenValues(t *testing.T, auditText string, forbiddenValues []string) {
	t.Helper()
	for _, forbidden := range append(forbiddenValues, "raw transcript secret", "private answer") {
		if strings.Contains(auditText, forbidden) {
			t.Fatalf("audit event leaked forbidden value %q: %s", forbidden, auditText)
		}
	}
}
