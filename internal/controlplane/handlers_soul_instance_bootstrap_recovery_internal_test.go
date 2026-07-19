package controlplane

import (
	"context"
	"encoding/json"
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

func expectHostedGenesisAssistantTurnRetryDispatchWrite(t *testing.T, tdb *mintConversationTestDB, wantTurnID string) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		session := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
			session.LatestTurnID != wantTurnID ||
			session.Failure == nil ||
			session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
			session.Failure.Recovery.MaxAttempts != 1 {
			t.Fatalf("expected assistant retry session to redispatch latest turn with carried budget, got %#v", session)
		}
		if session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" || session.MicroVMLifecycleRef == nil {
			t.Fatalf("expected assistant retry dispatch to refresh MicroVM refs, got %#v", session)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		conv := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		if conv.Status != models.SoulMintConversationStatusInProgress ||
			conv.StatusReason != "" ||
			conv.LatestTurnID != wantTurnID ||
			!conv.CompletedAt.IsZero() {
			t.Fatalf("expected assistant retry conversation alignment, got %#v", conv)
		}
	})
	tb.On("Execute").Return(nil).Once()
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
