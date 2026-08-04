package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	hostedGenesisMicroVMRecoveryPriorTurnID = "turn-microvm-prior"
	hostedGenesisMicroVMRecoveryTurnID      = "turn-microvm"
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
		Model:          "claude-sonnet-5",
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
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "claude-sonnet-5",
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

func applyProviderTimeoutRecoveryLifecycleRef(t *testing.T, session *models.HostedGenesisSession, microVMID string) {
	t.Helper()
	binding := session.MicroVMSessionBinding()
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, runtimemicrovm.ControllerResponse{
		Command: runtimemicrovm.CommandGet, TenantID: binding.TenantID(), Namespace: hostedgenesis.MicroVMNamespace,
		SessionID: binding.ConversationID, State: runtimemicrovm.StateRunning, DesiredState: runtimemicrovm.StateRunning,
		LifecycleState: runtimemicrovm.StateRunning, MicroVMID: microVMID, ProviderMicroVMID: microVMID,
		LastAction: runtimemicrovm.CommandGet, LastTransition: time.Now().UTC(), RegistryVersion: 7,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("build provider-timeout lifecycle ref: %v", err)
	}
	if err := session.ApplyMicroVMLifecycleRef(ref); err != nil {
		t.Fatalf("apply provider-timeout lifecycle ref: %v", err)
	}
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
		Model:          "claude-sonnet-5",
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
	if dispatcher.calls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected split assistant redispatch and no reconcile, got legacy_run=%d start=%d wait_invoke=%d reconcile=%d", dispatcher.calls, dispatcher.startCalls, dispatcher.waitAndInvokeCalls, dispatcher.reconcileCalls)
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

func TestSoulInstanceRecoverMintConversation_PersistsLifecycleBeforeDelayedProviderAttempt(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	now := time.Date(2026, 7, 23, 19, 40, 0, 0, time.UTC)
	session := completeBoundariesRecoverySession(t, reg, now)
	dispatcher := newDelayedProviderAttemptRecoveryDispatcher(t, &session)
	s.hostedGenesisMicroVMDispatcher = dispatcher

	expectDelayedProviderAttemptRecoveryReads(t, tdb, reg, session, now)
	expectDelayedProviderAttemptRecoveryWrites(t, tdb, dispatcher, session.Version)
	resp, recoverErr := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	assertDelayedProviderAttemptRecovery(t, resp, recoverErr, dispatcher)
}

func completeBoundariesRecoverySession(t *testing.T, reg models.SoulAgentRegistration, now time.Time) models.HostedGenesisSession {
	t.Helper()
	session := failedAssistantTurnRecoveryHostedGenesisSessionFixture(t, reg, now)
	session.Version = 41
	reviewed := controlplaneCompleteReviewCandidate(t, hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: session.InstanceSlug, RegistrationID: session.RegistrationID, AgentID: session.AgentID,
		ConversationID: session.ConversationID, SourceTurnID: session.LatestTurnID, Model: modelselection.CanonicalModelSet(session.Model),
	}, now.Add(-time.Minute))
	edited, err := hostedgenesis.ApplyDeclarationCandidateAction(reviewed, hostedgenesis.DeclarationCandidateAction{
		Action: "edit", Section: hostedgenesis.DeclarationSectionBoundaries,
		CandidateRevision: reviewed.Revision, CandidateHash: reviewed.CandidateHash, ReviewHash: reviewed.Review.ReviewHash,
	}, session.LatestTurnID, now)
	if err != nil {
		t.Fatalf("reopen complete candidate boundaries: %v", err)
	}
	if edited.Revision != 6 || edited.CurrentSection != hostedgenesis.DeclarationSectionBoundaries ||
		edited.Phase != hostedgenesis.DeclarationCandidatePhaseSection || len(edited.CompletedSections) != 5 {
		t.Fatalf("recovery fixture did not reopen boundaries from the complete five-section candidate: %#v", edited)
	}
	session.DeclarationCandidate = edited
	session.CandidateRevision, session.CandidateHash, session.CandidatePhase = edited.Revision, edited.CandidateHash, string(edited.Phase)
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("validate recovery session: %v", err)
	}
	return session
}

func expectDelayedProviderAttemptRecoveryReads(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration, session models.HostedGenesisSession, now time.Time) {
	t.Helper()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID,
		Model: session.Model, Messages: encodeMintConversationBlob(`[{"role":"user","content":"Revise boundaries."}]`),
		Status: models.SoulMintConversationStatusFailed, StatusReason: hostedGenesisFailureAssistantTurnFailed,
		LatestTurnID: session.LatestTurnID, ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID: "req-failed", CreatedAt: now.Add(-5 * time.Minute), UpdatedAt: now, CompletedAt: now,
	})
	stubSoulInstanceRecoverySession(t, tdb, session)
}

func assertDelayedProviderAttemptRecovery(t *testing.T, resp *apptheory.Response, recoverErr error, dispatcher *delayedProviderAttemptRecoveryDispatcher) {
	t.Helper()
	if recoverErr != nil || resp.Status != http.StatusAccepted {
		t.Fatalf("unexpected delayed provider-attempt recovery response=%#v err=%v", resp, recoverErr)
	}
	requireDelayedProviderAttemptCheckpoint(t, dispatcher)
	assertDelayedProviderAttemptDurable(t, dispatcher)
}

func requireDelayedProviderAttemptCheckpoint(t *testing.T, dispatcher *delayedProviderAttemptRecoveryDispatcher) {
	t.Helper()
	select {
	case checkpointErr := <-dispatcher.callbackDone:
		if checkpointErr != nil {
			t.Fatalf("current SDK attempt checkpoint did not persist: %v", checkpointErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delayed provider-attempt checkpoint")
	}
}

func assertDelayedProviderAttemptDurable(t *testing.T, dispatcher *delayedProviderAttemptRecoveryDispatcher) {
	t.Helper()
	durable := dispatcher.readSession()
	if dispatcher.monolithicCalls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 1 {
		t.Fatalf("recovery did not use split start/persist/wait-and-invoke orchestration: %#v", dispatcher)
	}
	if dispatcher.providerAttemptCheckpointConflict || dispatcher.assistantTurnFailed {
		t.Fatalf("delayed provider attempt hit the stale-version assistant_turn_failed path: %#v", dispatcher)
	}
	if durable == nil || durable.MicroVMLifecycleRef == nil ||
		hostedgenesis.NormalizeStatus(durable.Status) != hostedgenesis.StatusInProgress ||
		durable.DeclarationCandidate == nil || len(durable.DeclarationCandidate.ProviderAttempts) != 1 ||
		durable.DeclarationCandidate.ProviderAttempts[0].SDKAttemptOrdinal != 1 {
		t.Fatalf("current provider attempt/lifecycle state was not durable: %#v", durable)
	}
}

type delayedProviderAttemptRecoveryDispatcher struct {
	*stubMicroVMDispatcher

	mu                                sync.Mutex
	durable                           *models.HostedGenesisSession
	lifecyclePersisted                chan struct{}
	lifecyclePersistedOnce            sync.Once
	callbackDone                      chan error
	monolithicCalls                   int
	startCalls                        int
	waitAndInvokeCalls                int
	providerAttemptCheckpointConflict bool
	assistantTurnFailed               bool
}

func newDelayedProviderAttemptRecoveryDispatcher(t *testing.T, session *models.HostedGenesisSession) *delayedProviderAttemptRecoveryDispatcher {
	t.Helper()
	return &delayedProviderAttemptRecoveryDispatcher{
		stubMicroVMDispatcher: &stubMicroVMDispatcher{t: t},
		durable:               cloneHostedGenesisSession(session),
		lifecyclePersisted:    make(chan struct{}),
		callbackDone:          make(chan error, 1),
	}
}

func (d *delayedProviderAttemptRecoveryDispatcher) readSession() *models.HostedGenesisSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cloneHostedGenesisSession(d.durable)
}

func (d *delayedProviderAttemptRecoveryDispatcher) persistSession(session *models.HostedGenesisSession, version int64) {
	d.mu.Lock()
	persisted := cloneHostedGenesisSession(session)
	persisted.Version = version
	d.durable = persisted
	hasLifecycle := persisted.MicroVMLifecycleRef != nil
	d.mu.Unlock()
	if hasLifecycle {
		d.lifecyclePersistedOnce.Do(func() { close(d.lifecyclePersisted) })
	}
}

func (d *delayedProviderAttemptRecoveryDispatcher) StartMicroVMRun(_ context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.startCalls++
	if d.dispatchErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.dispatchErr
	}
	return delayedProviderAttemptDispatchResult(d.t, requestID, binding)
}

func (d *delayedProviderAttemptRecoveryDispatcher) WaitAndInvokeMicroVMTurn(_ context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.waitAndInvokeCalls++
	if d.invokeErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.invokeErr
	}
	preflight := d.readSession()
	if preflight == nil || preflight.MicroVMLifecycleRef == nil {
		return hostedgenesis.MicroVMDispatchResult{}, errors.New("workload invoked before lifecycle persistence")
	}
	d.startDelayedProviderAttempt(preflight)
	return delayedProviderAttemptDispatchResult(d.t, requestID, binding)
}

func (d *delayedProviderAttemptRecoveryDispatcher) DispatchMicroVMRun(_ context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.monolithicCalls++
	if d.dispatchErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.dispatchErr
	}
	preflight := d.readSession()
	d.startDelayedProviderAttempt(preflight)
	return delayedProviderAttemptDispatchResult(d.t, requestID, binding)
}

func (d *delayedProviderAttemptRecoveryDispatcher) startDelayedProviderAttempt(preflight *models.HostedGenesisSession) {
	go func() {
		select {
		case <-d.lifecyclePersisted:
		case <-time.After(time.Second):
			d.callbackDone <- errors.New("lifecycle persistence was not observed")
			return
		}
		d.callbackDone <- d.checkpointProviderAttempt(preflight)
	}()
}

func (d *delayedProviderAttemptRecoveryDispatcher) checkpointProviderAttempt(preflight *models.HostedGenesisSession) error {
	if preflight == nil || preflight.DeclarationCandidate == nil {
		return errors.New("provider attempt preflight is unavailable")
	}
	candidate := preflight.DeclarationCandidate
	next, err := hostedgenesis.ApplyDeclarationProviderAttempt(candidate, hostedgenesis.DeclarationProviderAttemptUpdate{
		Provider: "anthropic", Model: "claude-sonnet-5", Phase: "declaration_phase",
		Section: candidate.CurrentSection, SourceTurnID: preflight.LatestTurnID,
		CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash,
		SDKAttemptOrdinal: 1, SDKRetryBudget: 2, HTTPStatus: http.StatusOK,
		ProviderRequestID: "provider-request-redacted", DurationMS: 13,
	}, time.Date(2026, 7, 23, 19, 40, 13, 0, time.UTC))
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.durable == nil || d.durable.Version != preflight.Version {
		d.providerAttemptCheckpointConflict = true
		d.assistantTurnFailed = true
		return fmt.Errorf("checkpoint provider attempt evidence: optimistic version conflict (expected %d, current %d)", preflight.Version, d.durable.Version)
	}
	progressed := cloneHostedGenesisSession(preflight)
	progressed.DeclarationCandidate = next
	progressed.CandidateRevision, progressed.CandidateHash, progressed.CandidatePhase = next.Revision, next.CandidateHash, string(next.Phase)
	progressed.Version = preflight.Version + 1
	d.durable = progressed
	return nil
}

func delayedProviderAttemptDispatchResult(t *testing.T, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	t.Helper()
	if strings.TrimSpace(requestID) == "" {
		return hostedgenesis.MicroVMDispatchResult{}, errors.New("empty request id")
	}
	if err := binding.Validate(); err != nil {
		return hostedgenesis.MicroVMDispatchResult{}, err
	}
	response := runtimemicrovm.ControllerResponse{
		Command: runtimemicrovm.CommandRun, RequestID: requestID, TenantID: binding.TenantID(),
		Namespace: hostedgenesis.MicroVMNamespace, SessionID: binding.ConversationID,
		State: runtimemicrovm.StateRunning, DesiredState: runtimemicrovm.StateRunning,
		LifecycleState: runtimemicrovm.StateRunning, MicroVMID: "mv-delayed-provider",
		ProviderMicroVMID: "mv-delayed-provider", LastAction: runtimemicrovm.CommandRun,
		LastTransition: time.Date(2026, 7, 23, 19, 40, 1, 0, time.UTC), RegistryVersion: 1,
	}
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, response, response.LastTransition)
	if err != nil {
		return hostedgenesis.MicroVMDispatchResult{}, err
	}
	return hostedgenesis.MicroVMDispatchResult{LifecycleRef: ref, SessionID: response.SessionID}, nil
}

func expectDelayedProviderAttemptRecoveryWrites(t *testing.T, tdb *mintConversationTestDB, dispatcher *delayedProviderAttemptRecoveryDispatcher, initialVersion int64) {
	t.Helper()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		pending := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertHostedGenesisAssistantRetryPendingSession(t, pending, "turn-assistant")
		dispatcher.persistSession(pending, initialVersion+1)
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		progressed := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		assertHostedGenesisAssistantRetryLifecycleSession(t, progressed, "turn-assistant")
		dispatcher.persistSession(progressed, initialVersion+2)
	})
	tb.On("Execute").Return(nil).Once()
}

func TestSoulInstanceRecoverMintConversation_AssistantProviderTimeoutUsesFreshRuntimeWithoutSecondDebit(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	dispatcher := &stubMicroVMDispatcher{t: t}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	now := time.Date(2026, 7, 22, 11, 36, 16, 0, time.UTC)

	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	stubSoulInstanceRecoveryConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID: reg.AgentID, ConversationID: mintConversationTestConversationID,
		Model: "claude-sonnet-5", Messages: encodeMintConversationBlob(`[{"role":"user","content":"describe the agent"}]`),
		Status: models.SoulMintConversationStatusFailed, StatusReason: hostedGenesisFailureAssistantTurnFailed,
		LatestTurnID: "turn-assistant", ChargedCredits: soulMintConversationStreamBaseCredits,
		RequestID: "req-failed", CreatedAt: now.Add(-5 * time.Minute), UpdatedAt: now, CompletedAt: now,
	})
	session := failedAssistantTurnRecoveryHostedGenesisSessionFixture(t, reg, now)
	session.Version = 51
	session.Failure.Class = hostedgenesis.FailureClassProviderTimeout
	session.Failure.Recovery.MaxAttempts = 1
	applyProviderTimeoutRecoveryLifecycleRef(t, &session, "microvm-predeploy-assistant")
	stubSoulInstanceRecoverySession(t, tdb, session)
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		progressed := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		if progressed.Failure == nil || progressed.Failure.Recovery.MaxAttempts != 0 || progressed.MicroVMLifecycleRef == nil || progressed.MicroVMLifecycleRef.ImageVersion != "29" {
			t.Fatalf("expected atomic fresh assistant retry identity with consumed final attempt, got %#v", progressed)
		}
	})
	tb.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tb).Once()
	tb.On("Execute").Return(nil).Once()

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey}, nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil || resp.Status != http.StatusAccepted {
		t.Fatalf("unexpected assistant provider-timeout recovery response=%#v err=%v", resp, err)
	}
	if dispatcher.prepareFreshCalls != 1 || dispatcher.invokeCalls != 1 || dispatcher.calls != 0 {
		t.Fatalf("expected fresh prepare and one invoke without legacy run: %#v", dispatcher)
	}
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
		Model:          "claude-sonnet-5",
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
	if dispatcher.calls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 1 {
		t.Fatalf("expected one split assistant redispatch, got legacy_run=%d start=%d wait_invoke=%d", dispatcher.calls, dispatcher.startCalls, dispatcher.waitAndInvokeCalls)
	}
}

type stateReadingMicroVMDispatcher struct {
	*stubMicroVMDispatcher
	readSession               func() *models.HostedGenesisSession
	expectedFailureCode       hostedgenesis.FailureCode
	expectedRemainingAttempts int
}

func (d *stateReadingMicroVMDispatcher) StartMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.assertDurableRetryState(false, binding)
	return d.stubMicroVMDispatcher.StartMicroVMRun(ctx, requestID, binding)
}

func (d *stateReadingMicroVMDispatcher) WaitAndInvokeMicroVMTurn(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.assertDurableRetryState(true, binding)
	return d.stubMicroVMDispatcher.WaitAndInvokeMicroVMTurn(ctx, requestID, binding)
}

func (d *stateReadingMicroVMDispatcher) assertDurableRetryState(requireLifecycle bool, binding hostedgenesis.MicroVMSessionBinding) {
	d.t.Helper()
	session := d.readSession()
	if session == nil {
		d.t.Fatalf("workload test double could not read durable Host state")
		return
	}
	expectedFailureCode := d.expectedFailureCode
	if expectedFailureCode == "" {
		expectedFailureCode = hostedgenesis.FailureCodeAssistantTurnFailed
	}
	expectedRemainingAttempts := d.expectedRemainingAttempts
	if expectedRemainingAttempts == 0 {
		expectedRemainingAttempts = 1
	}
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != binding.TurnID ||
		session.Failure == nil ||
		session.Failure.Code != expectedFailureCode ||
		session.Failure.Recovery.MaxAttempts != expectedRemainingAttempts {
		d.t.Fatalf("workload observed old or invalid durable retry state during dispatch: %#v", session)
	}
	if expectedFailureCode == hostedgenesis.FailureCodeMicroVMUnavailable && session.VMCheckpoint == nil {
		d.t.Fatalf("microvm relaunch did not expose the validated VM checkpoint to the workload: %#v", session)
	}
	if requireLifecycle && session.MicroVMLifecycleRef == nil {
		d.t.Fatalf("workload invocation observed retry state before lifecycle persistence: %#v", session)
	}
	if !requireLifecycle && session.MicroVMLifecycleRef != nil {
		d.t.Fatalf("MicroVM start observed lifecycle state before the controller returned it: %#v", session)
	}
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
		Model:          "claude-sonnet-5",
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
	if dispatcher.calls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 0 {
		t.Fatalf("expected failed split start after durable retry transition, got legacy_run=%d start=%d wait_invoke=%d", dispatcher.calls, dispatcher.startCalls, dispatcher.waitAndInvokeCalls)
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
		Model:          "claude-sonnet-5",
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
	if dispatcher.calls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected live-bad shape to use split redispatch instead of reconciling pending forever, legacy_run=%d start=%d wait_invoke=%d reconcile=%d", dispatcher.calls, dispatcher.startCalls, dispatcher.waitAndInvokeCalls, dispatcher.reconcileCalls)
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
		Model:          "claude-sonnet-5",
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
	if dispatcher.calls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected one split relaunch dispatch and no reconcile, got legacy_run=%d start=%d wait_invoke=%d reconcile=%d", dispatcher.calls, dispatcher.startCalls, dispatcher.waitAndInvokeCalls, dispatcher.reconcileCalls)
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

func TestSoulInstanceRecoverMintConversation_RetriesMicroVMPreflightFailureWithoutCheckpoint(t *testing.T) {
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
		Model:          "claude-sonnet-5",
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
	// A store_preflight failure happens before the workload can produce an
	// actor VM checkpoint. Recovery must retry the accepted turn from its
	// durable session binding rather than treating the missing checkpoint as a
	// request to restart the entire soul bootstrap.
	session.VMCheckpoint = nil
	stubSoulInstanceRecoverySession(t, tdb, session)
	expectHostedGenesisRetryDispatchWriteWithCapture(
		t, tdb, hostedGenesisMicroVMRecoveryTurnID, "microvm-preflight",
		assertHostedGenesisMicroVMPreflightRetryPendingSession,
		assertHostedGenesisMicroVMPreflightRetryLifecycleSession,
		nil,
	)

	resp, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected store-preflight recovery err: %v", err)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 store-preflight retry response, got %#v", resp)
	}
	if dispatcher.calls != 0 || dispatcher.startCalls != 1 || dispatcher.waitAndInvokeCalls != 1 || dispatcher.reconcileCalls != 0 {
		t.Fatalf("expected one split store-preflight retry dispatch and no reconcile, got legacy_run=%d start=%d wait_invoke=%d reconcile=%d", dispatcher.calls, dispatcher.startCalls, dispatcher.waitAndInvokeCalls, dispatcher.reconcileCalls)
	}
	if dispatcher.lastBinding.ConversationID != mintConversationTestConversationID || dispatcher.lastBinding.TurnID != hostedGenesisMicroVMRecoveryTurnID {
		t.Fatalf("expected retry dispatch bound to failed turn, got %#v", dispatcher.lastBinding)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Conversation.Status != models.SoulMintConversationStatusInProgress ||
		out.Conversation.LatestTurnID != hostedGenesisMicroVMRecoveryTurnID ||
		out.Conversation.Failure != nil ||
		out.Conversation.PollAfterSeconds <= 0 {
		t.Fatalf("expected actionable store-preflight retry projection, got %#v", out.Conversation)
	}
	assertHostedGenesisResponseNoForbiddenValues(t, resp.Body, hostedGenesisStatusForbiddenValues())
	tdb.db.AssertNumberOfCalls(t, "TransactWrite", 2)
}

func TestHostedGenesisMicroVMRecoveryCheckpointValidationGuardsDurableInvariants(t *testing.T) {
	reg := mintConversationHandleReg()
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	valid := failedMicroVMUnavailableRecoverySessionFixture(t, reg, now)
	if err := validateHostedGenesisMicroVMRecoveryCheckpoint(&valid); err != nil {
		t.Fatalf("valid recovery checkpoint rejected: %v", err)
	}

	for name, mutate := range map[string]func(*models.HostedGenesisSession){
		"current turn missing from ledger": func(session *models.HostedGenesisSession) {
			session.TurnLedger = session.TurnLedger[:1]
		},
		"checkpoint turn missing from ledger": func(session *models.HostedGenesisSession) {
			session.TurnLedger = session.TurnLedger[1:]
		},
		"current input checkpoint missing from session": func(session *models.HostedGenesisSession) {
			session.InputCheckpointRef = ""
		},
		"current input checkpoint missing from ledger": func(session *models.HostedGenesisSession) {
			session.TurnLedger[1].InputCheckpointRef = ""
		},
		"current input checkpoint cross conversation": func(session *models.HostedGenesisSession) {
			session.InputCheckpointRef = hostedgenesis.CheckpointRef("input", "other-conversation", session.LatestTurnID)
		},
		"conversation ref mismatch": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.Ref = hostedgenesis.CheckpointRef("vm-actor", "other-conversation", fmt.Sprintf("%s-%d-%s", session.VMCheckpoint.Step, session.VMCheckpoint.Sequence, session.VMCheckpoint.LatestTurnID))
		},
		"malformed checkpoint ref": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.Ref = ""
		},
		"current turn checkpoint is not prior": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.LatestTurnID = session.LatestTurnID
			session.VMCheckpoint.Ref = hostedgenesis.CheckpointRef("vm-actor", session.ConversationID, fmt.Sprintf("%s-%d-%s", session.VMCheckpoint.Step, session.VMCheckpoint.Sequence, session.VMCheckpoint.LatestTurnID))
		},
		"failed checkpoint status": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.StatusTo = string(hostedgenesis.StatusFailed)
		},
		"terminal declaration checkpoint": func(session *models.HostedGenesisSession) {
			session.VMCheckpoint.StatusTo = string(hostedgenesis.StatusDeclarationReady)
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
	if len(out.Conversation.Messages) != 2 || out.Conversation.Messages[0].Content != hostedGenesisBenignCredentialSafetyProse || out.Conversation.Messages[0].Redacted || out.Conversation.Messages[1].Content != hostedGenesisTranscriptRedactedContent || !out.Conversation.Messages[1].Redacted || !out.Conversation.MessagesRedacted {
		t.Fatalf("failed recovery projection must preserve safe operator context and redact the secret-shaped entry: %#v", out.Conversation)
	}
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
		Model:                  "claude-sonnet-5",
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
	setHostedGenesisRecoveryCandidate(t, &session)
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
		Model:          "claude-sonnet-5",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"` + hostedGenesisBenignCredentialSafetyProse + `"},{"role":"assistant","content":"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345"}]`),
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
		Model:          "claude-sonnet-5",
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
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   hostedGenesisFailureMessage(hostedGenesisFailureAssistantTurnFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			Reason:            hostedGenesisFailureAssistantTurnFailed,
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
		Model:          "claude-sonnet-5",
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
	setHostedGenesisRecoveryCandidate(t, &session)
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
	session.Version = 38
	session.LatestTurnID = hostedGenesisMicroVMRecoveryTurnID
	session.MessageCount = 3
	session.InputCheckpointRef = hostedgenesis.CheckpointRef("input", session.ConversationID, hostedGenesisMicroVMRecoveryTurnID)
	session.TurnLedger = []hostedgenesis.TurnLedgerEntry{
		{
			TurnID:             hostedGenesisMicroVMRecoveryPriorTurnID,
			InputCheckpointRef: hostedgenesis.CheckpointRef("input", session.ConversationID, hostedGenesisMicroVMRecoveryPriorTurnID),
			ChargedCredits:     soulMintConversationStreamBaseCredits,
			MessageCount:       1,
			AcceptedAt:         now.Add(-10 * time.Minute),
		},
		{
			TurnID:             hostedGenesisMicroVMRecoveryTurnID,
			InputCheckpointRef: session.InputCheckpointRef,
			ChargedCredits:     soulMintConversationStreamBaseCredits,
			MessageCount:       3,
			AcceptedAt:         now.Add(-5 * time.Minute),
		},
	}
	session.Failure = failedMicroVMUnavailableRecoveryFailure()
	checkpoint, err := hostedgenesis.NewVMCheckpointMetadata(hostedgenesis.VMCheckpointInput{
		ConversationID:     session.ConversationID,
		LatestTurnID:       hostedGenesisMicroVMRecoveryPriorTurnID,
		RequestID:          "req-checkpoint",
		Sequence:           session.Version - 1,
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
	setHostedGenesisRecoveryCandidate(t, &session)
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("microvm unavailable fixture: %v", err)
	}
	return session
}

func setHostedGenesisRecoveryCandidate(t *testing.T, session *models.HostedGenesisSession) {
	t.Helper()
	if session == nil {
		t.Fatal("recovery candidate fixture requires a session")
		return
	}
	candidate, err := hostedgenesis.NewDeclarationCandidate(hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug:   session.InstanceSlug,
		RegistrationID: session.RegistrationID,
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		SourceTurnID:   session.LatestTurnID,
		Model:          session.Model,
	}, session.CreatedAt)
	if err != nil {
		t.Fatalf("build typed recovery candidate: %v", err)
	}
	session.DeclarationCandidate = candidate
	session.CandidateRevision = candidate.Revision
	session.CandidateHash = candidate.CandidateHash
	session.CandidatePhase = string(candidate.Phase)
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

func assertHostedGenesisMicroVMPreflightRetryPendingSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected microvm preflight retry session to persist latest turn with carried budget, got %#v", session)
	}
	if session.VMCheckpoint != nil {
		t.Fatalf("store-preflight retry must not invent an actor VM checkpoint: %#v", session)
	}
	if session.MicroVMExecutionID != "" || session.ExecutionStateRef != "" || session.MicroVMLifecycleRef != nil {
		t.Fatalf("retry-pending state must be durable before MicroVM dispatch refs are known, got %#v", session)
	}
}

func assertHostedGenesisMicroVMPreflightRetryLifecycleSession(t *testing.T, session *models.HostedGenesisSession, wantTurnID string) {
	t.Helper()
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.LatestTurnID != wantTurnID ||
		session.Failure == nil ||
		session.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable ||
		session.Failure.Recovery.MaxAttempts != 1 {
		t.Fatalf("expected microvm preflight retry lifecycle write to preserve carried budget, got %#v", session)
	}
	if session.VMCheckpoint != nil {
		t.Fatalf("store-preflight retry lifecycle write must not invent an actor VM checkpoint: %#v", session)
	}
	if session.MicroVMExecutionID == "" || session.ExecutionStateRef == "" || session.MicroVMLifecycleRef == nil {
		t.Fatalf("expected microvm preflight retry dispatch to refresh MicroVM refs, got %#v", session)
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
	if conversation.Failure.Code != hostedGenesisFailureAssistantTurnFailed || conversation.Failure.Recovery.Action != hostedGenesisRecoveryRetrySameStep {
		t.Fatalf("expected assistant-turn retry guidance, got %#v", conversation.Failure)
	}
}

func hostedGenesisStatusForbiddenValues() []string {
	return []string{
		"abcdefghijklmnopqrstuvwxyz012345",
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
	for _, forbidden := range append(forbiddenValues, hostedGenesisBenignCredentialSafetyProse) {
		if strings.Contains(auditText, forbidden) {
			t.Fatalf("audit event leaked forbidden value %q: %s", forbidden, auditText)
		}
	}
}
