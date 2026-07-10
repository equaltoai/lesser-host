package controlplane

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// TestH1_4_ExpiredVMSessionMapsToLoudFailureNotNoop proves H1.4's lifecycle-
// state coverage extends past H1.3's terminated/failed mapping: a session whose
// observed lifecycle state is non-terminal (e.g. stopped) but whose
// controller-reported expiry has passed is dead/expired and maps to a loud
// retryable microvm_unavailable failed session, not a preserved pending status.
// The MicroVMReconcileResult.Terminal flag is driven by expiry-in-the-past, not
// only by IsTerminalState.
func TestH1_4_ExpiredVMSessionMapsToLoudFailureNotNoop(t *testing.T) {
	tdb, s, reg, dispatcher := hostedGenesisH1D3RecoveryFixture(t, hostedgenesis.StatusInProgress)
	// Non-terminal observed state, but the stub reports the session as expired
	// (mirrors a controller get whose ExpiresAt is in the past).
	dispatcher.observedState = runtimemicrovm.StateStopped
	dispatcher.expired = true
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
		t.Fatalf("expected one reconciliation query for the expired VM, got %d", dispatcher.reconcileCalls)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200 loud-failure recovery response, got %#v", resp)
	}
}

// TestH1_4_RecoveryFallthroughIsDispatchOnlyNoSyncRerun proves the recover
// fallthrough (a session with no MicroVM lifecycle ref) re-dispatches the stuck
// turn through the MicroVM controller run command and NEVER re-runs a turn
// synchronously. Even when the retained non-production sync guard is enabled,
// recovery is dispatch-only: the sync assistant runner is not consulted. A live
// dispatcher returns 202; the sync LLM is never invoked.
func TestH1_4_RecoveryFallthroughIsDispatchOnlyNoSyncRerun(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := stubHostedGenesisMicroVMDispatcher(t, s)
	// Enable the retained non-production sync guard: recovery must STILL be
	// dispatch-only and never reach the sync assistant runner.
	s.hostedGenesisSyncAssistantFallbackEnabled = true
	syncLLMCalled := false
	s.hostedGenesisAssistantRunner = func(_ context.Context, _ hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		syncLLMCalled = true
		return hostedGenesisAssistantRunResult{}, nil
	}

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
	// No lifecycle ref fixture: routes through the dispatch-only fallthrough.
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
	if syncLLMCalled {
		t.Fatalf("recover fallthrough must never re-run a turn synchronously, even with the sync guard enabled")
	}
	if dispatcher.calls != 1 {
		t.Fatalf("expected recovery fallthrough to re-dispatch via the MicroVM controller, got %d dispatch calls", dispatcher.calls)
	}
	if resp.Status != http.StatusAccepted {
		t.Fatalf("expected 202 dispatched recovery response, got %#v", resp)
	}
}

// TestH1_4_RecoveryFallthroughUnwiredDispatcherIsLoudNoSyncRerun proves that
// when the MicroVM dispatcher is unwired on the recover fallthrough, recovery
// fails closed and loudly (typed microvm_unavailable) and NEVER falls back to a
// synchronous LLM call. The sync guard being enabled does not change this:
// recovery is dispatch-only.
func TestH1_4_RecoveryFallthroughUnwiredDispatcherIsLoudNoSyncRerun(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	s.hostedGenesisMicroVMDispatcher = nil
	s.hostedGenesisSyncAssistantFallbackEnabled = true
	syncLLMCalled := false
	s.hostedGenesisAssistantRunner = func(_ context.Context, _ hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		syncLLMCalled = true
		return hostedGenesisAssistantRunResult{}, nil
	}

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
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusFailed)

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err == nil {
		t.Fatalf("expected loud microvm-unavailable failure when recovery dispatcher is unwired")
	}
	if syncLLMCalled {
		t.Fatalf("recover fallthrough must not fall back to a synchronous LLM when the dispatcher is unwired")
	}
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeMicroVMUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected typed microvm_unavailable 503, got %#v", appErr)
	}
}

// TestH1_4_RecoveryFallthroughDispatchErrorIsLoudNoSyncRerun proves a rejected
// MicroVM run dispatch on the recover fallthrough surfaces as a loud typed
// microvm_unavailable failure (persisted failed session + 503), never a sync
// LLM fallback. Covers the dispatch-error branch of dispatchHostedGenesisRecoveryTurn.
func TestH1_4_RecoveryFallthroughDispatchErrorIsLoudNoSyncRerun(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	reg := mintConversationHandleReg()
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test-key")
	dispatcher := &stubMicroVMDispatcher{t: t, dispatchErr: errors.New("controller run rejected")}
	s.hostedGenesisMicroVMDispatcher = dispatcher
	s.hostedGenesisSyncAssistantFallbackEnabled = true
	syncLLMCalled := false
	s.hostedGenesisAssistantRunner = func(_ context.Context, _ hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
		syncLLMCalled = true
		return hostedGenesisAssistantRunResult{}, nil
	}

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
	expectSoulInstanceMintConversationProgression(t, tdb, hostedgenesis.StatusFailed)

	_, err := s.handleSoulInstanceRecoverMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err == nil {
		t.Fatalf("expected loud microvm-unavailable failure when recovery dispatch is rejected")
	}
	if syncLLMCalled {
		t.Fatalf("recover fallthrough must not fall back to a synchronous LLM when the dispatch is rejected")
	}
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeMicroVMUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected typed microvm_unavailable 503, got %#v", appErr)
	}
}

// TestH1_4_MicroVMReconcileIsTerminalClassification is in the hostedgenesis
// package (dispatch_test.go) because microVMReconcileIsTerminal is unexported.
// See TestH1_4_MicroVMReconcileIsTerminalClassification in that package.

// TestH1_4_ProductionQueueFailureSurfacesNon2xxNot200 proves the production
// async accept path's durable dispatch handoff failure surfaces as a loud
// non-2xx typed failure (503 microvm_unavailable), not HTTP 200 with a failed
// body. Worker-side controller dispatch failure is covered in aiworker.
func TestH1_4_ProductionQueueFailureSurfacesNon2xxNot200(t *testing.T) {
	tdb, s, reg, dispatcher := h1d2AcceptPathFixture(t)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		dispatcher.queueCalls++
		dispatcher.lastQueue = msg
		return errors.New("sqs unavailable")
	}
	h1d2ExpectAcceptPathProgression(t, tdb, hostedgenesis.StatusFailed)

	resp, err := s.handleSoulInstanceMintConversation(h1d2AcceptPathRequest(t, reg))
	if err == nil {
		t.Fatalf("expected loud microvm-unavailable failure, got response %#v", resp)
	}
	appErr := requireAppTheoryError(t, err)
	if appErr.Code != soulInstanceBootstrapCodeMicroVMUnavailable || appErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected typed microvm_unavailable 503, got %#v", appErr)
	}
	if appErr.StatusCode == http.StatusOK {
		t.Fatalf("turn failure must not surface as HTTP 200 (G10a): %#v", appErr)
	}
}

// TestH1_4_CompatHydrateErrorIsSurfacedNotSwallowed proves G10b is killed: a
// compat-conversation hydrate error that is NOT a benign not-found is surfaced
// (logged loudly) rather than silently swallowed. The hydrate helper logs the
// real storage error and returns without poisoning the turn session; a benign
// not-found stays a silent no-op (no compat row exists for a new turn).
func TestH1_4_CompatHydrateErrorIsSurfacedNotSwallowed(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	storageErr := errors.New("dynamodb provisioned throughput exceeded")
	// Non-not-found hydrate error: the compat conversation load fails with a
	// real storage error (not ErrItemNotFound).
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(storageErr).Once()

	var buf bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		if previousOutput != nil {
			log.SetOutput(previousOutput)
			return
		}
		log.SetOutput(os.Stderr)
	})

	session := &hostedGenesisTurnSession{conversationID: mintConversationTestConversationID}
	s.hydrateHostedGenesisCompatibilityConversation(context.Background(), session, "0xagent")

	logged := buf.String()
	if !strings.Contains(logged, "compat conversation hydrate failed") {
		t.Fatalf("expected compat hydrate error to be surfaced (logged), got log: %s", logged)
	}
	if !strings.Contains(logged, storageErr.Error()) {
		t.Fatalf("expected the storage error to be logged, got log: %s", logged)
	}
	// The hydrate error must not poison the turn session (no conv populated).
	if session.conv != nil {
		t.Fatalf("hydrate error must not populate a compat conversation: %#v", session.conv)
	}
}

// TestH1_4_CompatHydrateNotFoundStaysBenignNoop proves a benign not-found during
// compat-conversation hydrate is NOT logged as a failure (a new turn has no
// prior compat row) — the G10b kill surfaces only real errors, not the expected
// absent-row case.
func TestH1_4_CompatHydrateNotFoundStaysBenignNoop(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	var buf bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		if previousOutput != nil {
			log.SetOutput(previousOutput)
			return
		}
		log.SetOutput(os.Stderr)
	})

	session := &hostedGenesisTurnSession{conversationID: mintConversationTestConversationID}
	s.hydrateHostedGenesisCompatibilityConversation(context.Background(), session, "0xagent")

	if strings.Contains(buf.String(), "compat conversation hydrate failed") {
		t.Fatalf("benign not-found must not be logged as a hydrate failure: %s", buf.String())
	}
}

// TestH1_4_PromotionUpdateErrorIsSurfacedNotSwallowed proves G10c is killed:
// saveSoulAgentPromotion returns a non-nil error when the promotion store fails
// (surfaced to the caller, which logs it loudly), rather than swallowing the
// error into nil. The accept path treats promotion as non-fatal metadata and
// continues, but the error is observable, not silent. A standalone mock store
// is used so the promotion CreateOrUpdate error is not shadowed by the shared
// test DB's benign Maybe() default.
func TestH1_4_PromotionUpdateErrorIsSurfacedNotSwallowed(t *testing.T) {
	db := ttmocks.NewMockExtendedDB()
	qPromotion := new(ttmocks.MockQuery)
	promotionErr := errors.New("promotion store unavailable")
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(qPromotion).Maybe()
	// loadOrFallbackSoulAgentPromotion: the lookup misses and falls back to the
	// built promotion, then saveSoulAgentPromotion calls CreateOrUpdate which
	// fails loudly.
	qPromotion.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qPromotion).Maybe()
	qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	qPromotion.On("CreateOrUpdate").Return(promotionErr).Once()

	s := &Server{store: store.New(db)}
	promotion := &models.SoulAgentPromotion{
		AgentID:        "0x" + strings.Repeat("11", 32),
		RegistrationID: "reg-1",
	}
	appErr := s.saveSoulAgentPromotion(context.Background(), promotion)
	if appErr == nil {
		t.Fatalf("expected promotion update failure to be surfaced (non-nil error), not swallowed")
	}
}

// TestH1_4_G10SilentFallbacksAreGone is the grep-proof structural guard that
// the three G10 silent-fallback branches are removed and replaced with loud
// paths. It asserts the canonical loud markers exist in the production source
// and the canonical swallow markers do not.
func TestH1_4_G10SilentFallbacksAreGone(t *testing.T) {
	asyncSrc := mustReadControlplaneSource(t, "handlers_soul_mint_conversation_async.go")

	// G10a: a failed turn must surface a typed error, not return 200 with nil.
	if strings.Contains(asyncSrc, "hostedGenesisFailureAssistantTurnFailed, requestID, time.Now().UTC())\n\t\tif appErr != nil {\n\t\t\treturn nil, nil, 0, appErr\n\t\t}\n\t\treturn failedSession, failedConv, http.StatusOK, nil") {
		t.Fatalf("G10a swallow remains: sync turn-failure still returns http.StatusOK, nil")
	}
	if !strings.Contains(asyncSrc, "appErrCodeAssistantTurnFailed") {
		t.Fatalf("G10a loud path missing: appErrCodeAssistantTurnFailed not referenced on the async turn-failure path")
	}

	// G10b: the compat-conversation hydrate swallow is replaced by a loud log.
	if strings.Contains(asyncSrc, "if err != nil || conv == nil {\n\t\treturn\n\t}") {
		t.Fatalf("G10b swallow remains: hydrate still swallows err with a single early return")
	}
	if !strings.Contains(asyncSrc, "compat conversation hydrate failed") {
		t.Fatalf("G10b loud path missing: hydrate failure log marker absent")
	}

	// G10c: the promotion-update swallow is replaced by a loud, explicit log.
	if strings.Contains(asyncSrc, "session accepted without promotion update") {
		t.Fatalf("G10c swallow marker remains: old 'accepted without promotion update' log still present")
	}
	if !strings.Contains(asyncSrc, "accepted promotion update failed") {
		t.Fatalf("G10c loud path missing: promotion update failure log marker absent")
	}

	bootstrapSrc := mustReadControlplaneSource(t, "handlers_soul_instance_bootstrap.go")
	if !strings.Contains(bootstrapSrc, "soulInstanceBootstrapCodeAssistantTurnFailed") {
		t.Fatalf("G10a loud mapping missing: soulInstanceBootstrapCodeAssistantTurnFailed not defined")
	}
	if !strings.Contains(bootstrapSrc, "appErrCodeAssistantTurnFailed") {
		t.Fatalf("G10a loud mapping missing: appErrCodeAssistantTurnFailed not defined")
	}
}

// mustReadControlplaneSource reads a controlplane source file for the G10
// grep-proof structural guard. It fails the test if the file cannot be read.
func mustReadControlplaneSource(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(controlplaneSourceDir(t), name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// controlplaneSourceDir resolves the on-disk directory of the controlplane
// package source from the test binary's location. Tests run with the package
// source present in the working tree, so the source files are readable.
func controlplaneSourceDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// The test binary runs from internal/controlplane; confirm by checking for
	// one of the package's source files. Fall back to a relative path.
	if _, err := os.Stat(filepath.Join(wd, "handlers_soul_mint_conversation_async.go")); err == nil {
		return wd
	}
	return filepath.Join(wd, "internal", "controlplane")
}
