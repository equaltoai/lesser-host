package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

var (
	errNotFound = errors.New("not found")
	errConflict = errors.New("conditional write failed")
)

type fakeTurnStore struct {
	session    *models.HostedGenesisSession
	conv       *models.SoulAgentMintConversation
	reg        *models.SoulAgentRegistration
	completion *fakeCompletionStore
}

func (f *fakeTurnStore) GetHostedGenesisSession(_ context.Context, _, _ string) (*models.HostedGenesisSession, error) {
	if f.session == nil {
		return nil, errNotFound
	}
	return cloneSessionForRunner(f.session), nil
}
func (f *fakeTurnStore) GetSoulAgentMintConversation(_ context.Context, _, _ string) (*models.SoulAgentMintConversation, error) {
	if f.conv == nil {
		return nil, errNotFound
	}
	copy := *f.conv
	return &copy, nil
}
func (f *fakeTurnStore) GetSoulAgentRegistration(_ context.Context, _ string) (*models.SoulAgentRegistration, error) {
	if f.reg == nil {
		return nil, errNotFound
	}
	copy := *f.reg
	return &copy, nil
}
func (f *fakeTurnStore) CheckpointHostedGenesisCandidate(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, turnID string, revision int64, hash string) error {
	current := f.session
	if current == nil || current.Version != expectedVersion || hostedgenesis.NormalizeStatus(current.Status) != expectedStatus || current.LatestTurnID != turnID || current.CandidateRevision != revision || current.CandidateHash != hash {
		return errConflict
	}
	copy := cloneSessionForRunner(item)
	copy.CandidateRevision = copy.DeclarationCandidate.Revision
	copy.CandidateHash = copy.DeclarationCandidate.CandidateHash
	copy.CandidatePhase = string(copy.DeclarationCandidate.Phase)
	copy.Version = expectedVersion + 1
	f.session = copy
	if f.completion != nil {
		f.completion.session = cloneSessionForRunner(copy)
	}
	return nil
}
func (f *fakeTurnStore) RecordHostedGenesisAssistantTurnAndConversation(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, turnID string, revision int64, hash string, conversation *models.SoulAgentMintConversation) error {
	current := f.session
	if current == nil || conversation == nil || current.Version != expectedVersion || hostedgenesis.NormalizeStatus(current.Status) != expectedStatus || current.LatestTurnID != turnID || current.CandidateRevision != revision || current.CandidateHash != hash || current.CandidatePhase != string(current.DeclarationCandidate.Phase) {
		return errConflict
	}
	copy := cloneSessionForRunner(item)
	copy.CandidateRevision = copy.DeclarationCandidate.Revision
	copy.CandidateHash = copy.DeclarationCandidate.CandidateHash
	copy.CandidatePhase = string(copy.DeclarationCandidate.Phase)
	copy.Version = expectedVersion + 1
	convCopy := *conversation
	f.session, f.conv = copy, &convCopy
	if f.completion != nil {
		f.completion.session, f.completion.conversation = cloneSessionForRunner(copy), &convCopy
	}
	return nil
}
func (f *fakeTurnStore) FinalizeHostedGenesisCandidateAndConversation(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, turnID string, revision int64, hash string, conversation *models.SoulAgentMintConversation) error {
	current := f.session
	if current == nil || current.Version != expectedVersion || hostedgenesis.NormalizeStatus(current.Status) != expectedStatus || current.LatestTurnID != turnID || current.CandidateRevision != revision || current.CandidateHash != hash || current.CandidatePhase != string(hostedgenesis.DeclarationCandidatePhaseAffirmed) {
		return errConflict
	}
	copy := cloneSessionForRunner(item)
	copy.CandidateRevision = copy.DeclarationCandidate.Revision
	copy.CandidateHash = copy.DeclarationCandidate.CandidateHash
	copy.CandidatePhase = string(copy.DeclarationCandidate.Phase)
	copy.Version = expectedVersion + 1
	f.session = copy
	convCopy := *conversation
	f.conv = &convCopy
	if f.completion != nil {
		f.completion.session, f.completion.conversation = cloneSessionForRunner(copy), &convCopy
	}
	return nil
}

type fakeCompletionStore struct {
	session      *models.HostedGenesisSession
	conversation *models.SoulAgentMintConversation
	lastWrite    *models.HostedGenesisSession
}

func (f *fakeCompletionStore) GetHostedGenesisSession(_ context.Context, _, _ string) (*models.HostedGenesisSession, error) {
	if f.session == nil {
		return nil, errNotFound
	}
	return cloneSessionForRunner(f.session), nil
}
func (f *fakeCompletionStore) UpdateHostedGenesisSession(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if f.session == nil || f.session.Version != expectedVersion || hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus {
		return errConflict
	}
	copy := cloneSessionForRunner(item)
	copy.Version = expectedVersion + 1
	f.session, f.lastWrite = copy, copy
	return nil
}
func (f *fakeCompletionStore) GetSoulAgentMintConversation(_ context.Context, agentID, conversationID string) (*models.SoulAgentMintConversation, error) {
	if f.session == nil || f.session.AgentID != agentID || f.session.ConversationID != conversationID {
		return nil, errNotFound
	}
	if f.conversation == nil {
		return &models.SoulAgentMintConversation{AgentID: agentID, ConversationID: conversationID, Status: models.SoulMintConversationStatusInProgress}, nil
	}
	copy := *f.conversation
	return &copy, nil
}
func (f *fakeCompletionStore) FailHostedGenesisSessionAndConversation(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error {
	if f.session == nil || f.session.Version != expectedVersion || hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus {
		return errConflict
	}
	copy := cloneSessionForRunner(item)
	copy.Version = expectedVersion + 1
	f.session, f.lastWrite = copy, copy
	if conversation != nil {
		convCopy := *conversation
		f.conversation = &convCopy
	}
	return nil
}

func setFiveBodyContractEnv(t *testing.T) {
	t.Helper()
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, hostedgenesis.DeclarationSchemaVersionV2)
	t.Setenv(hostedgenesis.EnvGuidanceVersion, hostedgenesis.GuidanceVersionV2)
}

func baseTurnInput() (*fakeTurnStore, *fakeCompletionStore, completion.CompletionTurn) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	turn := completion.CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}
	candidate, _ := hostedgenesis.NewDeclarationCandidate(hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: "acme", RegistrationID: "reg-1", AgentID: "agent-1", ConversationID: "conv-1", SourceTurnID: "turn-1", Model: "openai:gpt-test",
	}, now)
	session := &models.HostedGenesisSession{
		InstanceSlug: "acme", ConversationID: "conv-1", RegistrationID: "reg-1", AgentID: "agent-1", Model: "openai:gpt-test",
		Status: string(hostedgenesis.StatusInProgress), LatestTurnID: "turn-1", MessageCount: 1, Version: 2,
		TurnLedger:           []hostedgenesis.TurnLedgerEntry{{TurnID: "turn-1", MessageCount: 1, AcceptedAt: now}},
		DeclarationCandidate: candidate, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, CandidatePhase: string(candidate.Phase),
		CreatedAt: now, UpdatedAt: now, RequestID: "req-1",
	}
	conv := &models.SoulAgentMintConversation{AgentID: "agent-1", ConversationID: "conv-1", Model: "openai:gpt-test", Status: models.SoulMintConversationStatusInProgress, LatestTurnID: "turn-1", Messages: models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"hello"}]`)}
	reg := &models.SoulAgentRegistration{ID: "reg-1", AgentID: "agent-1", DomainNormalized: "acme.example", LocalID: "acme"}
	comp := &fakeCompletionStore{session: cloneSessionForRunner(session), conversation: conv}
	store := &fakeTurnStore{session: session, conv: conv, reg: reg, completion: comp}
	return store, comp, turn
}

func TestRunTurnMalformedSectionReturnsErrorsThenSameSectionSucceeds(t *testing.T) {
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	phaseCalls := 0
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: func(ctx context.Context, _ string, input llm.MintConversationPhaseInput, handler llm.MintConversationPhaseToolHandler, _ llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
			phaseCalls++
			bad, err := handler(ctx, llm.MintConversationPhaseToolCall{Name: hostedgenesis.DeclarationToolIdentityPut, CallID: "bad", Arguments: boundPhaseToolArgs(t, input, `{"section":{"summary":"","notes":[]}}`)})
			if err != nil || bad.Accepted || len(bad.Errors) != 1 || bad.Errors[0].Path != "fiveBodies.identity.summary" {
				t.Fatalf("missing actionable error: %#v err=%v", bad, err)
			}
			good, err := handler(ctx, llm.MintConversationPhaseToolCall{Name: hostedgenesis.DeclarationToolIdentityPut, CallID: "good", Arguments: boundPhaseToolArgs(t, input, `{"section":{"summary":"I am the tenant-bound conversation actor.","notes":[]}}`)})
			if err != nil || !good.Accepted || good.Revision != 1 {
				t.Fatalf("same-section revision rejected: %#v err=%v", good, err)
			}
			return llm.MintConversationPhaseOutput{AssistantContent: "Let us construct philosophy next.", Usage: models.AIUsage{Provider: "openai", Model: "gpt-test", TotalTokens: 12}}, nil
		}}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if phaseCalls != 1 || comp.session.DeclarationCandidate == nil || comp.session.DeclarationCandidate.Revision != 1 || comp.session.DeclarationCandidate.CurrentSection != hostedgenesis.DeclarationSectionPhilosophy || hostedgenesis.NormalizeStatus(comp.session.Status) != hostedgenesis.StatusAssistantTurnReady {
		t.Fatalf("typed phase did not persist: calls=%d session=%#v", phaseCalls, comp.session)
	}
	if !strings.Contains(models.DecodeSoulMintConversationBlob(store.conv.Messages), "philosophy next") {
		t.Fatalf("assistant projection missing: %#v", store.conv)
	}
}

func TestProviderFailurePreservesAcceptedSectionForRecovery(t *testing.T) {
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: func(ctx context.Context, _ string, input llm.MintConversationPhaseInput, handler llm.MintConversationPhaseToolHandler, _ llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
			result, err := handler(ctx, llm.MintConversationPhaseToolCall{Name: hostedgenesis.DeclarationToolIdentityPut, CallID: "accepted", Arguments: boundPhaseToolArgs(t, input, `{"section":{"summary":"I am the tenant-bound conversation actor.","notes":[]}}`)})
			if err != nil || !result.Accepted {
				t.Fatalf("checkpoint rejected before provider failure: %#v err=%v", result, err)
			}
			return llm.MintConversationPhaseOutput{}, context.DeadlineExceeded
		}}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if comp.session.Failure == nil || comp.session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed || comp.session.DeclarationCandidate == nil || comp.session.DeclarationCandidate.Revision != 1 || comp.session.DeclarationCandidate.CurrentSection != hostedgenesis.DeclarationSectionPhilosophy {
		t.Fatalf("accepted section was lost on provider failure: %#v", comp.session)
	}
}

func TestReviewCheckpointRecoveryRendersStoredReviewWithoutProvider(t *testing.T) {
	setFiveBodyContractEnv(t)
	store, comp, turn := baseTurnInput()
	candidate := completeRunnerCandidate(t, store.session.DeclarationCandidate)
	store.session.DeclarationCandidate = candidate
	store.session.CandidateRevision, store.session.CandidateHash, store.session.CandidatePhase = candidate.Revision, candidate.CandidateHash, string(candidate.Phase)
	comp.session = cloneSessionForRunner(store.session)
	providerCalls := 0
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: func(context.Context, string, llm.MintConversationPhaseInput, llm.MintConversationPhaseToolHandler, llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
			providerCalls++
			return llm.MintConversationPhaseOutput{}, errors.New("must not be called")
		}}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if providerCalls != 0 {
		t.Fatalf("review recovery called provider %d times", providerCalls)
	}
	if hostedgenesis.NormalizeStatus(comp.session.Status) != hostedgenesis.StatusAssistantTurnReady || comp.session.DeclarationCandidate.Phase != hostedgenesis.DeclarationCandidatePhaseReview {
		t.Fatalf("stored review did not converge to assistant-ready: %#v", comp.session)
	}
	var messages []llm.MintConversationMessage
	err := json.Unmarshal([]byte(models.DecodeSoulMintConversationBlob(store.conv.Messages)), &messages)
	if err != nil || len(messages) != 2 || messages[1].Content != candidate.Review.ReviewText {
		t.Fatalf("deterministic owner review was not projected exactly: messages=%#v err=%v", messages, err)
	}
}

func TestDeclarationPhasePersistsContentFreeProviderAttemptEvidence(t *testing.T) {
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: contentFreeProviderAttemptPhaseRunner(t)}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	assertContentFreeProviderAttemptEvidence(t, comp.session.DeclarationCandidate.ProviderAttempts)
}

func contentFreeProviderAttemptPhaseRunner(t *testing.T) declarationPhaseRunner {
	t.Helper()
	return func(ctx context.Context, _ string, input llm.MintConversationPhaseInput, handler llm.MintConversationPhaseToolHandler, sink llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
		sink(llm.ProviderTelemetryEvent{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "sdk_http_attempt",
			SDKAttemptOrdinal: 1, SDKRetryBudget: llm.DefaultProviderSDKRetryBudget, HTTPStatus: 200,
			ProviderRequestID: "req_provider_1", DurationMS: 31,
		})
		result, err := handler(ctx, llm.MintConversationPhaseToolCall{Name: hostedgenesis.DeclarationToolIdentityPut, CallID: "accepted", Arguments: boundPhaseToolArgs(t, input, `{"section":{"summary":"I am the tenant-bound conversation actor.","notes":[]}}`)})
		if err != nil || !result.Accepted {
			t.Fatalf("checkpoint rejected: %#v err=%v", result, err)
		}
		sink(llm.ProviderTelemetryEvent{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "tool_validation_completed",
			ToolName: hostedgenesis.DeclarationToolIdentityPut, ToolCallHash: "sha256:" + strings.Repeat("a", 64), Accepted: true,
		})
		sink(llm.ProviderTelemetryEvent{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "provider_call_completed",
			OutputBytes: 38, OutputSHA256: strings.Repeat("b", 64), InputTokens: 20, OutputTokens: 8, TotalTokens: 28,
		})
		return llm.MintConversationPhaseOutput{AssistantContent: "Let us construct philosophy next."}, nil
	}
}

func assertContentFreeProviderAttemptEvidence(t *testing.T, attempts []hostedgenesis.DeclarationProviderAttempt) {
	t.Helper()
	if len(attempts) != 1 {
		t.Fatalf("expected one durable SDK attempt, got %#v", attempts)
	}
	attempt := attempts[0]
	if attempt.SDKAttemptOrdinal != 1 || attempt.SDKRetryBudget != llm.DefaultProviderSDKRetryBudget || attempt.HTTPStatus != 200 ||
		attempt.ProviderRequestID != "req_provider_1" || attempt.ToolName != hostedgenesis.DeclarationToolIdentityPut || !attempt.Accepted ||
		attempt.OutputBytes != 38 || attempt.TotalTokens != 28 || attempt.OutputSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("durable attempt evidence was incomplete: %#v", attempt)
	}
	if strings.Contains(mustMarshal(attempt), "tenant-bound conversation actor") || strings.Contains(mustMarshal(attempt), "Let us construct") {
		t.Fatalf("durable attempt evidence retained provider content: %#v", attempt)
	}
}

func TestAffirmedFinalizationMakesZeroProviderCallsAndIsStable(t *testing.T) {
	setFiveBodyContractEnv(t)
	store, comp, turn := baseTurnInput()
	candidate := completeRunnerCandidate(t, store.session.DeclarationCandidate)
	affirmed, err := hostedgenesis.ApplyDeclarationCandidateAction(candidate, hostedgenesis.DeclarationCandidateAction{
		Action: "affirm", CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash, ReviewHash: candidate.Review.ReviewHash,
	}, turn.TurnID, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	store.session.DeclarationCandidate = affirmed
	store.session.CandidateRevision, store.session.CandidateHash, store.session.CandidatePhase = affirmed.Revision, affirmed.CandidateHash, string(affirmed.Phase)
	comp.session = cloneSessionForRunner(store.session)
	providerCalls := 0
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: func(context.Context, string, llm.MintConversationPhaseInput, llm.MintConversationPhaseToolHandler, llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
			providerCalls++
			return llm.MintConversationPhaseOutput{}, errors.New("must not be called")
		}}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if providerCalls != 0 {
		t.Fatalf("provider called %d times after affirmation", providerCalls)
	}
	if hostedgenesis.NormalizeStatus(store.session.Status) != hostedgenesis.StatusDeclarationReady || store.session.DeclarationCandidate.Phase != hostedgenesis.DeclarationCandidatePhaseFinalized || store.session.DeclarationCheckpoint.DeclarationHash != affirmed.CandidateHash || models.DecodeSoulMintConversationBlob(store.conv.ProducedDeclarations) != affirmed.CanonicalJSON {
		t.Fatalf("deterministic finalization diverged: session=%#v conv=%#v", store.session, store.conv)
	}
	beforeBytes, beforeHash, beforeVersion := store.conv.ProducedDeclarations, store.session.DeclarationCheckpoint.DeclarationHash, store.session.Version
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if providerCalls != 0 || store.conv.ProducedDeclarations != beforeBytes || store.session.DeclarationCheckpoint.DeclarationHash != beforeHash || store.session.Version != beforeVersion {
		t.Fatalf("repeated finalization changed terminal truth: calls=%d session=%#v", providerCalls, store.session)
	}
}

func TestLegacyLaneWithoutTypedCandidateRestartsBootstrap(t *testing.T) {
	setFiveBodyContractEnv(t)
	store, comp, turn := baseTurnInput()
	store.session.DeclarationCandidate = nil
	store.session.CandidateRevision, store.session.CandidateHash, store.session.CandidatePhase = 0, "", ""
	comp.session = cloneSessionForRunner(store.session)
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if comp.session.Failure == nil || comp.session.Failure.Recovery.Action != hostedgenesis.RecoveryActionRestartSoulBootstrap {
		t.Fatalf("hard cutover did not require restart_soul_bootstrap: %#v", comp.session)
	}
}

func completeRunnerCandidate(t *testing.T, candidate *hostedgenesis.DeclarationCandidate) *hostedgenesis.DeclarationCandidate {
	t.Helper()
	calls := []struct {
		name string
		body string
	}{
		{hostedgenesis.DeclarationToolIdentityPut, `{"section":{"summary":"I am the tenant-bound Hosted Genesis actor.","notes":[]}}`},
		{hostedgenesis.DeclarationToolPhilosophyPut, `{"section":{"summary":"I prefer auditable durable truth over implicit authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolDisciplinePut, `{"section":{"summary":"I ground, act, record, and re-ground at each checkpoint.","notes":[]}}`},
		{hostedgenesis.DeclarationToolBoundariesPut, `{"section":{"summary":"I remain within the managed instance and require owner authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolSoulPut, `{"section":{"summary":"Exact reviewed truth is load-bearing.","notes":[],"refusals":[{"bypass":"skip the candidate hash check","invariant":"exact reviewed bytes remain authoritative","closestSafePath":"submit a matching structural affirmation"},{"bypass":"reuse another tenant session","invariant":"tenant and session guards must match","closestSafePath":"restart in the correct managed instance"},{"bypass":"call a provider after affirmation","invariant":"finalization remains deterministic","closestSafePath":"publish the exact affirmed candidate bytes"}]},"selfDescription":{"purpose":"Construct a typed Hosted Genesis declaration.","constraints":"Remain tenant bound.","commitments":"Preserve exact durable truth.","limitations":"No provider after affirmation.","authoredBy":"agent","mintingModel":"openai:gpt-test"},"capabilities":[{"capability":"hosted_genesis","scope":"Construct a typed declaration.","claimLevel":"self-declared","lastValidated":"","validationRef":"","degradesTo":""}],"transparency":{"modelProviderUncertainty":"Provider content is self-declared.","operationalNotes":"Host validates every section.","selfDeclaredNotice":"Self-declared until publication."}}`},
	}
	for i, call := range calls {
		payload := bindCandidateToolPayload(t, candidate, call.body)
		next, result, err := hostedgenesis.ApplyDeclarationTool(candidate, hostedgenesis.DeclarationToolRequest{
			ToolName: call.name, ToolCallID: fmt.Sprintf("call-%d", i), ExpectedRevision: candidate.Revision,
			ExpectedHash: candidate.CandidateHash, SourceTurnID: "turn-1", Payload: payload,
		}, fixedClock().Add(time.Duration(i)*time.Second))
		if err != nil || !result.Accepted || next == nil {
			t.Fatalf("complete candidate tool %s failed: result=%#v err=%v", call.name, result, err)
		}
		candidate = next
	}
	return candidate
}

func boundPhaseToolArgs(t *testing.T, input llm.MintConversationPhaseInput, raw string) json.RawMessage {
	t.Helper()
	return bindToolPayload(t, input.CandidateRevision, input.CandidateHash, raw)
}

func bindCandidateToolPayload(t *testing.T, candidate *hostedgenesis.DeclarationCandidate, raw string) json.RawMessage {
	t.Helper()
	return bindToolPayload(t, candidate.Revision, candidate.CandidateHash, raw)
}

func bindToolPayload(t *testing.T, revision int64, hash, raw string) json.RawMessage {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	payload["candidateRevision"] = revision
	payload["candidateHash"] = hash
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func fixedClock() time.Time { return time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC) }

func mustMarshal(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(body)
}

// Keep this typed value available to lifecycle/telemetry tests that share the
// package; production declaration construction is exclusively candidate based.
var _ = soul.SelfDescriptionV2{}
