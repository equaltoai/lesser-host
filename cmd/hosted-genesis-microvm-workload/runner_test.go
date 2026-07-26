package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	session       *models.HostedGenesisSession
	conv          *models.SoulAgentMintConversation
	reg           *models.SoulAgentRegistration
	completion    *fakeCompletionStore
	checkpointErr error
	assistantErr  error
	checkpoints   []int64
	assistants    []int64
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
	if f.checkpointErr != nil {
		return f.checkpointErr
	}
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
	f.checkpoints = append(f.checkpoints, expectedVersion)
	if f.completion != nil {
		f.completion.session = cloneSessionForRunner(copy)
	}
	return nil
}
func (f *fakeTurnStore) RecordHostedGenesisAssistantTurnAndConversation(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, turnID string, revision int64, hash string, conversation *models.SoulAgentMintConversation) error {
	if f.assistantErr != nil {
		return f.assistantErr
	}
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
	f.assistants = append(f.assistants, expectedVersion)
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

func TestProviderKeyFailurePersistsSafeClass(t *testing.T) {
	setFiveBodyContractEnv(t)
	clearProviderEnv(t)
	setProviderSSMLoader(t, "openai", nil)
	store, comp, turn := baseTurnInput()
	phaseCalls := 0
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: func(context.Context, string, llm.MintConversationPhaseInput, llm.MintConversationPhaseToolHandler, llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
			phaseCalls++
			return llm.MintConversationPhaseOutput{}, nil
		}}
	if err := runner.runTurnAndPersist(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	if phaseCalls != 0 || comp.session.Failure == nil ||
		comp.session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
		comp.session.Failure.Class != hostedgenesis.FailureClassProviderAPIFailure {
		t.Fatalf("provider key failure was not safely classified before dispatch: calls=%d failure=%#v", phaseCalls, comp.session.Failure)
	}
}

func TestAssistantTurnPersistenceFailurePersistsSafeClass(t *testing.T) {
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	store.assistantErr = errors.New("injected assistant turn persistence failure")
	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock,
		phaseRunner: func(ctx context.Context, _ string, input llm.MintConversationPhaseInput, handler llm.MintConversationPhaseToolHandler, _ llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
			result, err := handler(ctx, llm.MintConversationPhaseToolCall{
				Name: hostedgenesis.DeclarationToolIdentityPut, CallID: "accepted",
				Arguments: boundPhaseToolArgs(t, input, `{"section":{"summary":"I am the tenant-bound conversation actor.","notes":[]}}`),
			})
			if err != nil || !result.Accepted {
				t.Fatalf("checkpoint rejected before assistant persistence failure: %#v err=%v", result, err)
			}
			return llm.MintConversationPhaseOutput{AssistantContent: "Let us construct philosophy next."}, nil
		}}
	if err := runner.runTurnAndPersist(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	if comp.session.Failure == nil ||
		comp.session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
		comp.session.Failure.Class != hostedgenesis.FailureClassAssistantTurnStore ||
		comp.session.DeclarationCandidate == nil ||
		comp.session.DeclarationCandidate.Revision != 1 {
		t.Fatalf("assistant persistence failure was not safely classified without losing the checkpoint: %#v", comp.session)
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
		phaseRunner: postToolProviderContinuationPhaseRunner(t)}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if hostedgenesis.NormalizeStatus(comp.session.Status) != hostedgenesis.StatusAssistantTurnReady ||
		comp.session.DeclarationCandidate == nil || comp.session.DeclarationCandidate.Revision != 1 ||
		comp.session.DeclarationCandidate.CurrentSection != hostedgenesis.DeclarationSectionPhilosophy {
		t.Fatalf("post-tool provider continuation did not reach assistant-ready: %#v", comp.session)
	}
	assertContentFreeProviderAttemptEvidence(t, comp.session.DeclarationCandidate.ProviderAttempts)
}

func TestSoulPhaseTruncatedSixRefusalOutputClassifiesEvidencePersistenceFailure(t *testing.T) {
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	candidate := runnerCandidateAtSoul(t, store.session.DeclarationCandidate)
	store.session.DeclarationCandidate = candidate
	store.session.CandidateRevision, store.session.CandidateHash, store.session.CandidatePhase = candidate.Revision, candidate.CandidateHash, string(candidate.Phase)
	comp.session = cloneSessionForRunner(store.session)
	store.checkpointErr = errors.New("injected provider attempt checkpoint failure")

	fullArguments := longSixRefusalSoulArguments(t, candidate)
	if len(fullArguments) <= 4096 {
		t.Fatalf("six-refusal soul arguments must exercise the long-output boundary, got %d bytes", len(fullArguments))
	}
	truncatedArguments := fullArguments[:4096]

	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			requests <- map[string]any{"decode_error": err.Error()}
		} else {
			requests <- body
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req_soul_length")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp-soul-length", "object": "response", "created_at": 1, "model": "gpt-test",
			"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"},
			"output": []any{map[string]any{
				"type": "function_call", "id": "fc-soul-length", "call_id": "call-soul-length",
				"name": hostedgenesis.DeclarationToolSoulPut, "arguments": string(truncatedArguments), "status": "incomplete",
			}},
			"usage": map[string]any{
				"input_tokens": 100, "output_tokens": 4096, "total_tokens": 4196,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 1024},
			},
		})
	}))
	defer server.Close()
	t.Setenv("OPENAI_BASE_URL", server.URL)
	if err := llm.ConfigureProviderHTTPTimeout(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}

	request := <-requests
	if decodeErr := request["decode_error"]; decodeErr != nil {
		t.Fatalf("decode OpenAI request: %v", decodeErr)
	}
	assertStrictSoulPhaseRequest(t, request)
	if comp.session.Failure == nil ||
		comp.session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed ||
		comp.session.Failure.Class != hostedgenesis.FailureClassProviderEvidenceStore {
		t.Fatalf("provider evidence persistence failure was not safely classified: %#v", comp.session.Failure)
	}
	if comp.session.DeclarationCandidate == nil ||
		comp.session.DeclarationCandidate.Revision != 4 ||
		comp.session.DeclarationCandidate.CurrentSection != hostedgenesis.DeclarationSectionSoul ||
		len(comp.session.DeclarationCandidate.ProviderAttempts) != 0 {
		t.Fatalf("failed soul phase changed the pinned candidate: %#v", comp.session.DeclarationCandidate)
	}
}

func TestSoulPhaseLongSixRefusalOutputCompletesWithSizedBudget(t *testing.T) {
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	candidate := runnerCandidateAtSoul(t, store.session.DeclarationCandidate)
	store.session.DeclarationCandidate = candidate
	store.session.CandidateRevision, store.session.CandidateHash, store.session.CandidatePhase = candidate.Revision, candidate.CandidateHash, string(candidate.Phase)
	comp.session = cloneSessionForRunner(store.session)

	fullArguments := longSixRefusalSoulArguments(t, candidate)
	requests := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			requests <- map[string]any{"decode_error": err.Error()}
			return
		}
		requests <- body
		maxTokens, _ := body["max_output_tokens"].(float64)
		arguments, status, outputTokens := fullArguments, "completed", 6500
		if maxTokens < 8192 {
			arguments, status, outputTokens = fullArguments[:4096], "incomplete", 4096
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", "req_soul_sized")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "resp-soul-sized", "object": "response", "created_at": 1, "model": "gpt-test", "status": status,
			"output": []any{map[string]any{
				"type": "function_call", "id": "fc-soul-sized", "call_id": "call-soul-sized",
				"name": hostedgenesis.DeclarationToolSoulPut, "arguments": string(arguments), "status": status,
			}},
			"usage": map[string]any{
				"input_tokens": 100, "output_tokens": outputTokens, "total_tokens": 100 + outputTokens,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens_details": map[string]any{"reasoning_tokens": 1024},
			},
		})
	}))
	defer server.Close()
	t.Setenv("OPENAI_BASE_URL", server.URL)
	if err := llm.ConfigureProviderHTTPTimeout(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	runner := &turnRunner{store: store, writer: completion.NewCompletionWriter(comp, fixedClock), nowFunc: fixedClock}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if got := len(requests); got != 1 {
		t.Fatalf("sized soul phase made %d provider requests", got)
	}
	request := <-requests
	if decodeErr := request["decode_error"]; decodeErr != nil {
		t.Fatalf("decode OpenAI request: %v", decodeErr)
	}
	assertStrictSoulPhaseRequest(t, request)
	if hostedgenesis.NormalizeStatus(comp.session.Status) != hostedgenesis.StatusAssistantTurnReady ||
		comp.session.Failure != nil ||
		comp.session.DeclarationCandidate == nil ||
		comp.session.DeclarationCandidate.Revision != 5 ||
		comp.session.DeclarationCandidate.Phase != hostedgenesis.DeclarationCandidatePhaseReview ||
		len(comp.session.DeclarationCandidate.FiveBodies.Soul.Refusals) != 6 ||
		len(comp.session.DeclarationCandidate.ProviderAttempts) != 1 {
		t.Fatalf("long six-refusal soul phase did not reach durable review: %#v", comp.session)
	}
}

func TestProviderAttemptEvidenceCanRetryAfterCheckpointFailure(t *testing.T) {
	setFiveBodyContractEnv(t)
	store, _, turn := baseTurnInput()
	candidate := runnerCandidateAtSoul(t, store.session.DeclarationCandidate)
	store.session.DeclarationCandidate = candidate
	store.session.CandidateRevision, store.session.CandidateHash, store.session.CandidatePhase = candidate.Revision, candidate.CandidateHash, string(candidate.Phase)
	in := turnInput{session: cloneSessionForRunner(store.session)}
	runner := &turnRunner{store: store, nowFunc: fixedClock}
	event := llm.ProviderTelemetryEvent{
		Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "sdk_http_attempt",
		SDKAttemptOrdinal: 1, SDKRetryBudget: llm.DefaultProviderSDKRetryBudget, HTTPStatus: 200,
		ProviderRequestID: "req_provider_recovery", DurationMS: 17,
	}

	store.checkpointErr = errors.New("injected provider evidence checkpoint failure")
	err := runner.checkpointProviderAttemptEvidence(t.Context(), &in, turn, candidate.CurrentSection, candidate.Revision, candidate.CandidateHash, event)
	if err == nil {
		t.Fatal("expected injected provider evidence checkpoint failure")
	}
	if len(in.session.DeclarationCandidate.ProviderAttempts) != 0 || len(store.session.DeclarationCandidate.ProviderAttempts) != 0 {
		t.Fatalf("failed evidence checkpoint mutated candidate state: in=%#v store=%#v", in.session.DeclarationCandidate.ProviderAttempts, store.session.DeclarationCandidate.ProviderAttempts)
	}

	store.checkpointErr = nil
	if err := runner.checkpointProviderAttemptEvidence(t.Context(), &in, turn, candidate.CurrentSection, candidate.Revision, candidate.CandidateHash, event); err != nil {
		t.Fatalf("provider evidence retry did not recover: %v", err)
	}
	if len(in.session.DeclarationCandidate.ProviderAttempts) != 1 ||
		len(store.session.DeclarationCandidate.ProviderAttempts) != 1 ||
		store.session.DeclarationCandidate.ProviderAttempts[0].SDKAttemptOrdinal != 1 {
		t.Fatalf("provider evidence retry did not persist exactly once: in=%#v store=%#v", in.session.DeclarationCandidate.ProviderAttempts, store.session.DeclarationCandidate.ProviderAttempts)
	}
}

type providerAttemptRecoveryCase struct {
	name            string
	section         hostedgenesis.DeclarationSection
	durableOrdinals int64
	wantOrdinal     int64
}

func TestRecoveryRebasesProviderAttemptOrdinalForExactCandidateTuple(t *testing.T) {
	for _, test := range []providerAttemptRecoveryCase{
		{name: "soul", section: hostedgenesis.DeclarationSectionSoul, durableOrdinals: 4, wantOrdinal: 5},
		{name: "discipline", section: hostedgenesis.DeclarationSectionDiscipline, durableOrdinals: 1, wantOrdinal: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			runProviderAttemptRecoveryCase(t, test)
		})
	}
}

func runProviderAttemptRecoveryCase(t *testing.T, test providerAttemptRecoveryCase) {
	t.Helper()
	setFiveBodyContractEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	store, comp, turn := baseTurnInput()
	candidate := runnerCandidateAtSection(t, store.session.DeclarationCandidate, test.section)
	candidate = runnerCandidateWithProviderAttempts(t, candidate, test.durableOrdinals)
	initialVersion := store.session.Version
	initialLatestTurnID := store.session.LatestTurnID
	initialAttemptCount := len(candidate.ProviderAttempts)
	store.session.DeclarationCandidate = candidate
	store.session.CandidateRevision = candidate.Revision
	store.session.CandidateHash = candidate.CandidateHash
	store.session.CandidatePhase = string(candidate.Phase)
	store.session.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Class:     hostedgenesis.FailureClassProviderEvidenceStore,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeAssistantTurnFailed),
		Retryable: true,
		Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep},
	}
	comp.session = cloneSessionForRunner(store.session)

	runner := &turnRunner{
		store:       store,
		writer:      completion.NewCompletionWriter(comp, fixedClock),
		nowFunc:     fixedClock,
		phaseRunner: recoveryPhaseRunner(t, test, turn, candidate),
	}
	if err := runner.runTurnAndPersist(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	assertProviderAttemptRecovery(t, store, turn, candidate, initialVersion, initialLatestTurnID, initialAttemptCount, test.wantOrdinal)
}

func recoveryPhaseRunner(t *testing.T, test providerAttemptRecoveryCase, turn completion.CompletionTurn, candidate *hostedgenesis.DeclarationCandidate) declarationPhaseRunner {
	t.Helper()
	return func(_ context.Context, _ string, input llm.MintConversationPhaseInput, _ llm.MintConversationPhaseToolHandler, sink llm.ProviderTelemetrySink) (llm.MintConversationPhaseOutput, error) {
		if input.SourceTurnID != turn.TurnID || input.Section != test.section ||
			input.CandidateRevision != candidate.Revision || input.CandidateHash != candidate.CandidateHash {
			t.Fatalf("provider input lost exact candidate tuple: %#v", input)
		}
		sink(llm.ProviderTelemetryEvent{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "sdk_http_attempt",
			SDKAttemptOrdinal: 1, SDKRetryBudget: llm.DefaultProviderSDKRetryBudget, HTTPStatus: 200,
			ProviderRequestID: "req_provider_recovery", DurationMS: 17,
		})
		sink(llm.ProviderTelemetryEvent{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "provider_call_completed",
			OutputBytes: 24, OutputSHA256: strings.Repeat("c", 64), TotalTokens: 11,
		})
		return llm.MintConversationPhaseOutput{AssistantContent: "Recovery turn completed."}, nil
	}
}

func assertProviderAttemptRecovery(t *testing.T, store *fakeTurnStore, turn completion.CompletionTurn, candidate *hostedgenesis.DeclarationCandidate, initialVersion int64, initialLatestTurnID string, initialAttemptCount int, wantOrdinal int64) {
	t.Helper()
	got := store.session
	if hostedgenesis.NormalizeStatus(got.Status) != hostedgenesis.StatusAssistantTurnReady {
		t.Fatalf("recovery status = %q, want assistant_turn_ready", got.Status)
	}
	if got.Failure != nil {
		t.Fatalf("recovery retained failure: %#v", got.Failure)
	}
	if len(got.DeclarationCandidate.ProviderAttempts) != initialAttemptCount+1 {
		t.Fatalf("recovery persisted %d attempts, want exactly %d", len(got.DeclarationCandidate.ProviderAttempts), initialAttemptCount+1)
	}
	attempt := got.DeclarationCandidate.ProviderAttempts[len(got.DeclarationCandidate.ProviderAttempts)-1]
	if attempt.SDKAttemptOrdinal != wantOrdinal {
		t.Fatalf("recovery SDK ordinal = %d, want %d: %#v", attempt.SDKAttemptOrdinal, wantOrdinal, attempt)
	}
	if attempt.OutputBytes != 24 || attempt.OutputSHA256 != strings.Repeat("c", 64) || attempt.TotalTokens != 11 {
		t.Fatalf("zero-ordinal completion did not enrich rebased attempt: %#v", attempt)
	}
	assertProviderAttemptRecoveryVersions(t, store, initialVersion)
	assertProviderAttemptRecoveryBindings(t, got, turn, candidate, initialLatestTurnID)
}

func assertProviderAttemptRecoveryVersions(t *testing.T, store *fakeTurnStore, initialVersion int64) {
	t.Helper()
	if store.session.Version != initialVersion+3 || len(store.checkpoints) != 2 ||
		store.checkpoints[0] != initialVersion || store.checkpoints[1] != initialVersion+1 ||
		len(store.assistants) != 1 || store.assistants[0] != initialVersion+2 {
		t.Fatalf("recovery versions were not monotonic: initial=%d final=%d checkpoints=%v assistants=%v", initialVersion, store.session.Version, store.checkpoints, store.assistants)
	}
}

func assertProviderAttemptRecoveryBindings(t *testing.T, got *models.HostedGenesisSession, turn completion.CompletionTurn, candidate *hostedgenesis.DeclarationCandidate, initialLatestTurnID string) {
	t.Helper()
	if got.LatestTurnID != initialLatestTurnID || got.LatestTurnID != turn.TurnID {
		t.Fatalf("recovery changed LatestTurnID: got=%q initial=%q turn=%q", got.LatestTurnID, initialLatestTurnID, turn.TurnID)
	}
	if got.InstanceSlug != candidate.InstanceSlug || got.RegistrationID != candidate.RegistrationID ||
		!strings.EqualFold(got.AgentID, candidate.AgentID) || got.ConversationID != candidate.ConversationID {
		t.Fatalf("recovery changed candidate owner bindings: session=%#v candidate=%#v", got, candidate)
	}
	if got.CandidateRevision != candidate.Revision || got.CandidateHash != candidate.CandidateHash ||
		got.CandidatePhase != string(candidate.Phase) || got.DeclarationCandidate.SourceTurnID != turn.TurnID {
		t.Fatalf("recovery changed candidate state bindings: session=%#v candidate=%#v", got, candidate)
	}
}

func TestDeclarationProviderAttemptOrdinalBaseUsesExactTupleMaximum(t *testing.T) {
	attempts := []hostedgenesis.DeclarationProviderAttempt{
		{SourceTurnID: "turn-1", Section: hostedgenesis.DeclarationSectionSoul, CandidateRevision: 4, CandidateHash: "sha256:current", SDKAttemptOrdinal: 4},
		{SourceTurnID: "turn-1", Section: hostedgenesis.DeclarationSectionSoul, CandidateRevision: 4, CandidateHash: "sha256:current", SDKAttemptOrdinal: 2},
		{SourceTurnID: "turn-other", Section: hostedgenesis.DeclarationSectionSoul, CandidateRevision: 4, CandidateHash: "sha256:current", SDKAttemptOrdinal: 91},
		{SourceTurnID: "turn-1", Section: hostedgenesis.DeclarationSectionDiscipline, CandidateRevision: 4, CandidateHash: "sha256:current", SDKAttemptOrdinal: 92},
		{SourceTurnID: "turn-1", Section: hostedgenesis.DeclarationSectionSoul, CandidateRevision: 3, CandidateHash: "sha256:current", SDKAttemptOrdinal: 93},
		{SourceTurnID: "turn-1", Section: hostedgenesis.DeclarationSectionSoul, CandidateRevision: 4, CandidateHash: "sha256:other", SDKAttemptOrdinal: 94},
	}
	if got := declarationProviderAttemptOrdinalBase(attempts, " turn-1 ", hostedgenesis.DeclarationSectionSoul, 4, " sha256:current "); got != 4 {
		t.Fatalf("exact-tuple ordinal base = %d, want 4", got)
	}
}

func assertStrictSoulPhaseRequest(t *testing.T, request map[string]any) {
	t.Helper()
	if got, ok := request["max_output_tokens"].(float64); !ok || got != 8192 {
		t.Fatalf("OpenAI soul phase output cap changed: %#v", request["max_output_tokens"])
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("soul phase must expose exactly one tool: %#v", request["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != hostedgenesis.DeclarationToolSoulPut || tool["strict"] != true {
		t.Fatalf("soul phase tool is not strict: %#v", tool)
	}
	parameters, _ := tool["parameters"].(map[string]any)
	properties, _ := parameters["properties"].(map[string]any)
	section, _ := properties["section"].(map[string]any)
	sectionProperties, _ := section["properties"].(map[string]any)
	refusals, _ := sectionProperties["refusals"].(map[string]any)
	if refusals["minItems"] != float64(3) || refusals["maxItems"] != float64(8) {
		t.Fatalf("soul refusal bounds changed: %#v", refusals)
	}
}

func longSixRefusalSoulArguments(t *testing.T, candidate *hostedgenesis.DeclarationCandidate) []byte {
	t.Helper()
	refusals := make([]map[string]string, 0, 6)
	for i := 1; i <= 6; i++ {
		refusals = append(refusals, map[string]string{
			"bypass":          fmt.Sprintf("Bypass %d: %s", i, strings.Repeat("skip the tenant-bound durable candidate guard; ", 7)),
			"invariant":       fmt.Sprintf("Invariant %d: %s", i, strings.Repeat("exact reviewed state and authority remain load-bearing; ", 7)),
			"closestSafePath": fmt.Sprintf("Safe path %d: %s", i, strings.Repeat("return to the guarded owner review and submit bounded evidence; ", 7)),
		})
	}
	payload := map[string]any{
		"candidateRevision": candidate.Revision,
		"candidateHash":     candidate.CandidateHash,
		"section": map[string]any{
			"summary": strings.Repeat("Exact tenant-bound reviewed truth remains authoritative. ", 24),
			"notes": []string{
				strings.Repeat("Every mutation remains bound to the current turn, revision, and candidate hash. ", 5),
				strings.Repeat("Provider output is validated before any candidate checkpoint is accepted. ", 5),
			},
			"refusals": refusals,
		},
		"selfDescription": map[string]any{
			"purpose":      "Construct the exact typed Hosted Genesis declaration with the owner.",
			"constraints":  "Remain tenant-bound and preserve candidate validation.",
			"commitments":  "Keep reviewed durable truth authoritative at every checkpoint.",
			"limitations":  "Cannot bypass owner authority, candidate bindings, or validation.",
			"authoredBy":   "agent",
			"mintingModel": "openai:gpt-test",
		},
		"capabilities": []any{map[string]any{
			"capability": "hosted_genesis", "scope": "Construct a typed declaration.",
			"claimLevel": "self-declared", "lastValidated": "", "validationRef": "", "degradesTo": "",
		}},
		"transparency": map[string]any{
			"modelProviderUncertainty": "Provider output is self-declared.",
			"operationalNotes":         "Host validates every section and binding.",
			"selfDeclaredNotice":       "Self-declared until exact owner affirmation and publication.",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func postToolProviderContinuationPhaseRunner(t *testing.T) declarationPhaseRunner {
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
			ToolName: hostedgenesis.DeclarationToolIdentityPut, ToolCallHash: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("accepted"))), Accepted: true,
		})
		sink(llm.ProviderTelemetryEvent{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", EventType: "sdk_http_attempt",
			SDKAttemptOrdinal: 2, SDKRetryBudget: llm.DefaultProviderSDKRetryBudget, HTTPStatus: 200,
			ProviderRequestID: "req_provider_2", DurationMS: 29,
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
	if len(attempts) != 2 {
		t.Fatalf("expected tool and continuation SDK attempts, got %#v", attempts)
	}
	toolAttempt, continuationAttempt := attempts[0], attempts[1]
	if !validTestToolAttemptEvidence(toolAttempt) {
		t.Fatalf("durable tool attempt evidence was incomplete: %#v", toolAttempt)
	}
	if !validTestContinuationAttemptEvidence(continuationAttempt) {
		t.Fatalf("durable continuation attempt evidence was incomplete: %#v", continuationAttempt)
	}
	for _, attempt := range attempts {
		if strings.Contains(mustMarshal(attempt), "tenant-bound conversation actor") || strings.Contains(mustMarshal(attempt), "Let us construct") {
			t.Fatalf("durable attempt evidence retained provider content: %#v", attempt)
		}
	}
}

func validTestToolAttemptEvidence(attempt hostedgenesis.DeclarationProviderAttempt) bool {
	return attempt.SDKAttemptOrdinal == 1 && attempt.SDKRetryBudget == llm.DefaultProviderSDKRetryBudget &&
		attempt.HTTPStatus == 200 && attempt.ProviderRequestID == "req_provider_1" &&
		attempt.ToolName == hostedgenesis.DeclarationToolIdentityPut && attempt.Accepted
}

func validTestContinuationAttemptEvidence(attempt hostedgenesis.DeclarationProviderAttempt) bool {
	return attempt.SDKAttemptOrdinal == 2 && attempt.SDKRetryBudget == llm.DefaultProviderSDKRetryBudget &&
		attempt.HTTPStatus == 200 && attempt.ProviderRequestID == "req_provider_2" &&
		attempt.OutputBytes == 38 && attempt.TotalTokens == 28 &&
		attempt.OutputSHA256 == strings.Repeat("b", 64) && attempt.ToolName == ""
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

func runnerCandidateAtSoul(t *testing.T, candidate *hostedgenesis.DeclarationCandidate) *hostedgenesis.DeclarationCandidate {
	t.Helper()
	return runnerCandidateAtSection(t, candidate, hostedgenesis.DeclarationSectionSoul)
}

func runnerCandidateAtSection(t *testing.T, candidate *hostedgenesis.DeclarationCandidate, section hostedgenesis.DeclarationSection) *hostedgenesis.DeclarationCandidate {
	t.Helper()
	calls := []struct {
		name string
		body string
	}{
		{hostedgenesis.DeclarationToolIdentityPut, `{"section":{"summary":"I am the tenant-bound Hosted Genesis actor.","notes":[]}}`},
		{hostedgenesis.DeclarationToolPhilosophyPut, `{"section":{"summary":"I prefer auditable durable truth over implicit authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolDisciplinePut, `{"section":{"summary":"I ground, act, record, and re-ground at each checkpoint.","notes":[]}}`},
		{hostedgenesis.DeclarationToolBoundariesPut, `{"section":{"summary":"I remain within the managed instance and require owner authority.","notes":[]}}`},
	}
	for i, call := range calls {
		if candidate.CurrentSection == section {
			return candidate
		}
		payload := bindCandidateToolPayload(t, candidate, call.body)
		next, result, err := hostedgenesis.ApplyDeclarationTool(candidate, hostedgenesis.DeclarationToolRequest{
			ToolName: call.name, ToolCallID: fmt.Sprintf("soul-boundary-%d", i), ExpectedRevision: candidate.Revision,
			ExpectedHash: candidate.CandidateHash, SourceTurnID: "turn-1", Payload: payload,
		}, fixedClock().Add(time.Duration(i)*time.Second))
		if err != nil || !result.Accepted || next == nil {
			t.Fatalf("advance candidate to soul with %s: result=%#v err=%v", call.name, result, err)
		}
		candidate = next
	}
	if candidate.CurrentSection != section {
		t.Fatalf("cannot advance runner candidate to section %q", section)
	}
	return candidate
}

func runnerCandidateWithProviderAttempts(t *testing.T, candidate *hostedgenesis.DeclarationCandidate, ordinals int64) *hostedgenesis.DeclarationCandidate {
	t.Helper()
	for ordinal := int64(1); ordinal <= ordinals; ordinal++ {
		next, err := hostedgenesis.ApplyDeclarationProviderAttempt(candidate, hostedgenesis.DeclarationProviderAttemptUpdate{
			Provider: "openai", Model: "gpt-test", Phase: "declaration_phase", Section: candidate.CurrentSection,
			SourceTurnID: candidate.SourceTurnID, CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash,
			SDKAttemptOrdinal: ordinal, SDKRetryBudget: llm.DefaultProviderSDKRetryBudget, HTTPStatus: 200,
			ProviderRequestID: fmt.Sprintf("req_prior_%d", ordinal), DurationMS: 10 + ordinal,
		}, fixedClock().Add(time.Duration(ordinal)*time.Second))
		if err != nil {
			t.Fatalf("seed durable provider attempt %d: %v", ordinal, err)
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
