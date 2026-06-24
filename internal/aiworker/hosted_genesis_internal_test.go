package aiworker

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeHostedGenesisStore struct {
	fakeAIStore

	mu       sync.Mutex
	reg      *models.SoulAgentRegistration
	domain   *models.Domain
	instance *models.Instance
	session  *models.HostedGenesisSession
	conv     *models.SoulAgentMintConversation
	idem     *models.SoulMintConversationIdempotency
	putCount int
}

const (
	hostedGenesisWorkerOpenAIKey   = "openai-test-key"
	hostedGenesisWorkerOpenAIModel = "openai:gpt-test"
)

func (f *fakeHostedGenesisStore) GetSoulAgentRegistration(_ context.Context, id string) (*models.SoulAgentRegistration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reg == nil || strings.TrimSpace(f.reg.ID) != strings.TrimSpace(id) {
		return nil, errNotFound
	}
	cp := *f.reg
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetDomain(_ context.Context, domain string) (*models.Domain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.domain == nil || !strings.EqualFold(strings.TrimSpace(f.domain.Domain), strings.TrimSpace(domain)) {
		return nil, errNotFound
	}
	cp := *f.domain
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetInstance(_ context.Context, slug string) (*models.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.instance == nil || !strings.EqualFold(strings.TrimSpace(f.instance.Slug), strings.TrimSpace(slug)) {
		return nil, errNotFound
	}
	cp := *f.instance
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetSoulAgentMintConversation(_ context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.conv == nil ||
		!strings.EqualFold(strings.TrimSpace(f.conv.AgentID), strings.TrimSpace(agentID)) ||
		strings.TrimSpace(f.conv.ConversationID) != strings.TrimSpace(conversationID) {
		return nil, errNotFound
	}
	cp := *f.conv
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetHostedGenesisSession(_ context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session == nil ||
		!strings.EqualFold(strings.TrimSpace(f.session.InstanceSlug), strings.TrimSpace(instanceSlug)) ||
		strings.TrimSpace(f.session.ConversationID) != strings.TrimSpace(conversationID) {
		return nil, errNotFound
	}
	cp := *f.session
	cp.TurnLedger = append([]hostedgenesis.TurnLedgerEntry(nil), f.session.TurnLedger...)
	return &cp, nil
}

func (f *fakeHostedGenesisStore) UpdateHostedGenesisSession(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if item == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session == nil || f.session.Version != expectedVersion || hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus {
		return errNotFound
	}
	cp := *item
	cp.Version = expectedVersion + 1
	cp.TurnLedger = append([]hostedgenesis.TurnLedgerEntry(nil), item.TurnLedger...)
	f.session = &cp
	return nil
}

func (f *fakeHostedGenesisStore) PutSoulAgentMintConversation(_ context.Context, item *models.SoulAgentMintConversation) error {
	if item == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *item
	f.conv = &cp
	f.putCount++
	return nil
}

func (f *fakeHostedGenesisStore) GetSoulMintConversationIdempotency(_ context.Context, instanceSlug string, registrationID string, idempotencyKey string) (*models.SoulMintConversationIdempotency, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idem == nil ||
		!strings.EqualFold(strings.TrimSpace(f.idem.InstanceSlug), strings.TrimSpace(instanceSlug)) ||
		strings.TrimSpace(f.idem.RegistrationID) != strings.TrimSpace(registrationID) ||
		strings.TrimSpace(f.idem.IdempotencyKey) != strings.TrimSpace(idempotencyKey) {
		return nil, errNotFound
	}
	cp := *f.idem
	return &cp, nil
}

func newHostedGenesisWorkerStore(turnID string) *fakeHostedGenesisStore {
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	return &fakeHostedGenesisStore{
		fakeAIStore: fakeAIStore{jobs: map[string]*models.AIJob{}, results: map[string]*models.AIResult{}},
		reg: &models.SoulAgentRegistration{
			ID:               "reg-worker",
			DomainNormalized: "agent.example",
			LocalID:          "agent",
			AgentID:          "0x" + strings.Repeat("44", 32),
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		domain: &models.Domain{
			Domain:       "agent.example",
			InstanceSlug: "inst-worker",
			Status:       models.DomainStatusVerified,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		instance: &models.Instance{
			Slug:      "inst-worker",
			Status:    models.InstanceStatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		},
		conv: &models.SoulAgentMintConversation{
			AgentID:        "0x" + strings.Repeat("44", 32),
			ConversationID: "conv-worker",
			Model:          "deterministic",
			Messages:       models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"reload-safe-user-turn"}]`),
			Status:         models.SoulMintConversationStatusInProgress,
			LatestTurnID:   turnID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		session: &models.HostedGenesisSession{
			InstanceSlug:   "inst-worker",
			RegistrationID: "reg-worker",
			AgentID:        "0x" + strings.Repeat("44", 32),
			ConversationID: "conv-worker",
			Status:         string(hostedgenesis.StatusInProgress),
			Model:          "deterministic",
			LatestTurnID:   turnID,
			MessageCount:   1,
			TurnLedger: []hostedgenesis.TurnLedgerEntry{{
				TurnID:         turnID,
				IdempotencyKey: "idem-worker",
				RequestHash:    strings.Repeat("a", 64),
				ChargedCredits: 10,
				MessageCount:   1,
				AcceptedAt:     now,
			}},
			RequestID: "req-host",
			CreatedAt: now,
			UpdatedAt: now,
		},
		idem: &models.SoulMintConversationIdempotency{
			InstanceSlug:   "inst-worker",
			RegistrationID: "reg-worker",
			AgentID:        "0x" + strings.Repeat("44", 32),
			ConversationID: "conv-worker",
			TurnID:         turnID,
			IdempotencyKey: "idem-worker",
			RequestHash:    strings.Repeat("a", 64),
			Status:         models.SoulMintConversationIdempotencyStatusProcessing,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
}

func hostedGenesisWorkerQueueMessage(step string, turnID string) hostedgenesis.QueueMessage {
	return hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           step,
		RegistrationID: "reg-worker",
		InstanceSlug:   "inst-worker",
		AgentID:        "0x" + strings.Repeat("44", 32),
		ConversationID: "conv-worker",
		TurnID:         turnID,
		RequestID:      "req-host",
		IdempotencyKey: "idem-worker",
	}
}

func hostedGenesisWorkerSQSMessage(t *testing.T, msg hostedgenesis.QueueMessage) events.SQSMessage {
	t.Helper()
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal queue message: %v", err)
	}
	return events.SQSMessage{Body: string(body)}
}

func TestHostedGenesisQueueMessageDispatchesDurableKinds(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{}, newHostedGenesisWorkerStore("turn-worker"), artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	ctx := &apptheory.EventContext{RequestID: "worker-req"}
	for _, body := range []string{
		`{bad json`,
		`{"kind":"other","step":"assistant_turn"}`,
		`{"kind":"hosted_genesis_conversation","step":"unknown"}`,
	} {
		if err := srv.handleHostedGenesisQueueMessage(ctx, events.SQSMessage{Body: body}); err != nil {
			t.Fatalf("expected non-durable queue body to drop, got %v", err)
		}
	}

	st := newHostedGenesisWorkerStore("turn-worker")
	srv = NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	err := srv.handleHostedGenesisQueueMessage(ctx, hostedGenesisWorkerSQSMessage(t, hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker")))
	if err != nil {
		t.Fatalf("unexpected assistant queue error: %v", err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureLLMUnavailable)
}

func TestHostedGenesisWorkerReloadsDurableTurnBeforeFailureWrite(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected worker error: %v", err)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.putCount != 1 || st.conv == nil {
		t.Fatalf("expected one fail-closed conversation write, putCount=%d conv=%#v", st.putCount, st.conv)
	}
	if st.conv.Status != models.SoulMintConversationStatusFailed || st.conv.StatusReason != hostedGenesisFailureLLMUnavailable {
		t.Fatalf("expected llm_unavailable failure after reload, got %#v", st.conv)
	}
	if decoded := models.DecodeSoulMintConversationBlob(st.conv.Messages); !strings.Contains(decoded, "reload-safe-user-turn") {
		t.Fatalf("worker lost persisted user turn on failure write: %q", decoded)
	}
	if strings.Contains(st.conv.Messages, "reload-safe-user-turn") {
		t.Fatalf("worker stored raw transcript instead of encoded private field: %q", st.conv.Messages)
	}
}

func TestHostedGenesisWorkerRejectsMismatchedIdempotencyTurn(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-original")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-replayed-wrong"))
	if err != nil {
		t.Fatalf("unexpected worker error: %v", err)
	}

	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureInvalidCompletionState)
}

func TestHostedGenesisAssistantTurnDropsStaleConversation(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Status = models.SoulMintConversationStatusAssistantTurnReady
	st.session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil || st.putCount != 0 {
		t.Fatalf("expected stale assistant turn to drop without write, put=%d err=%v", st.putCount, err)
	}
}

func TestHostedGenesisAssistantTurnRejectsInvalidTranscript(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Messages = models.EncodeSoulMintConversationBlob(`not-json`)
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected invalid transcript error: %v", err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureInvalidCompletionState)
}

func TestHostedGenesisJobValidationRejectsTenantBoundaryMismatch(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.domain.InstanceSlug = "other-instance"
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	reg, conv, session, err := srv.loadAndValidateHostedGenesisJob(context.Background(), st, hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil || reg != nil || conv != nil || session != nil {
		t.Fatalf("expected boundary mismatch to drop job, reg=%#v conv=%#v session=%#v err=%v", reg, conv, session, err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureTenantBoundaryViolation)
}

func TestHostedGenesisJobValidationAllowsMissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	msg := hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker")
	msg.IdempotencyKey = ""

	reg, conv, session, err := srv.loadAndValidateHostedGenesisJob(context.Background(), st, msg)
	if err != nil || reg == nil || conv == nil || session == nil {
		t.Fatalf("expected valid job without idempotency key, reg=%#v conv=%#v session=%#v err=%v", reg, conv, session, err)
	}
	if decoded := models.DecodeSoulMintConversationBlob(conv.Messages); !strings.Contains(decoded, "reload-safe-user-turn") {
		t.Fatalf("expected decoded durable transcript, got %q", decoded)
	}
}

func TestHostedGenesisJobValidationDropsRegistrationMismatch(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	msg := hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker")
	msg.AgentID = "0x" + strings.Repeat("55", 32)

	reg, conv, session, err := srv.loadAndValidateHostedGenesisJob(context.Background(), st, msg)
	if err != nil || reg != nil || conv != nil || session != nil || st.putCount != 0 {
		t.Fatalf("expected registration mismatch to drop without write, reg=%#v conv=%#v session=%#v put=%d err=%v", reg, conv, session, st.putCount, err)
	}
}

func TestHostedGenesisDeclarationExtractionProgressSafeFailures(t *testing.T) {
	t.Parallel()

	t.Run("user only conversation remains progress safe", func(t *testing.T) {
		t.Parallel()

		st := newHostedGenesisWorkerStore("turn-worker")
		st.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
		st.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
		srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

		err := srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
		if err != nil {
			t.Fatalf("unexpected declaration extraction error: %v", err)
		}
		assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureInvalidCompletionState)
	})

	t.Run("assistant transcript without provider key fails closed", func(t *testing.T) {
		t.Parallel()

		st := newHostedGenesisWorkerStore("turn-worker")
		st.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
		st.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
		st.conv.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"describe"},{"role":"assistant","content":"draft"}]`)
		srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
		ctx := &apptheory.EventContext{RequestID: "worker-req"}

		err := srv.handleHostedGenesisQueueMessage(ctx, hostedGenesisWorkerSQSMessage(t, hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker")))
		if err != nil {
			t.Fatalf("unexpected declaration queue error: %v", err)
		}
		assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureLLMUnavailable)
	})
}

func TestHostedGenesisDeclarationExtractionDropsNonPendingStatus(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Status = models.SoulMintConversationStatusAssistantTurnReady
	st.session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	st.conv.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"describe"},{"role":"assistant","content":"draft"}]`)
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
	if err != nil || st.putCount != 0 {
		t.Fatalf("expected non-pending extraction to drop without write, put=%d err=%v", st.putCount, err)
	}
}

func TestHostedGenesisDeclarationExtractionRejectsInvalidTranscript(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
	st.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	st.conv.Messages = models.EncodeSoulMintConversationBlob(`not-json`)
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected invalid extraction transcript error: %v", err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureInvalidCompletionState)
}

func TestHostedGenesisWorkerCompletesAssistantAndDeclarationTurns(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", hostedGenesisWorkerOpenAIKey)
	oldAssistant := runHostedGenesisAssistantModel
	oldDeclaration := runHostedGenesisDeclarationModel
	t.Cleanup(func() {
		runHostedGenesisAssistantModel = oldAssistant
		runHostedGenesisDeclarationModel = oldDeclaration
	})
	runHostedGenesisAssistantModel = func(_ context.Context, apiKey string, modelSet string, systemPrompt string, messages []llm.MintConversationMessage) (string, models.AIUsage, error) {
		if apiKey != hostedGenesisWorkerOpenAIKey || modelSet != hostedGenesisWorkerOpenAIModel || !strings.Contains(systemPrompt, "agent.example") || len(messages) != 1 {
			t.Fatalf("unexpected assistant model input: key=%q model=%q prompt=%q messages=%#v", apiKey, modelSet, systemPrompt, messages)
		}
		return "assistant ready", models.AIUsage{Provider: testProviderOpenAI, Model: "gpt-test", InputTokens: 2, OutputTokens: 3}, nil
	}
	runHostedGenesisDeclarationModel = func(_ context.Context, apiKey string, modelSet string, in llm.MintConversationDeclarationsInput) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
		if apiKey != hostedGenesisWorkerOpenAIKey || modelSet != hostedGenesisWorkerOpenAIModel || in.Registration.Domain != "agent.example" || len(in.Messages) != 2 {
			t.Fatalf("unexpected declaration model input: key=%q model=%q in=%#v", apiKey, modelSet, in)
		}
		return validHostedGenesisDraft(), models.AIUsage{Provider: testProviderOpenAI, Model: "gpt-test", TotalTokens: 11}, nil
	}

	assistantStore := newHostedGenesisWorkerStore("turn-worker")
	assistantStore.conv.Model = hostedGenesisWorkerOpenAIModel
	assistantStore.session.Model = hostedGenesisWorkerOpenAIModel
	srv := NewServer(config.Config{}, assistantStore, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected assistant success error: %v", err)
	}
	assertHostedGenesisAssistantReady(t, assistantStore)

	declarationStore := newHostedGenesisWorkerStore("turn-worker")
	declarationStore.conv.Model = hostedGenesisWorkerOpenAIModel
	declarationStore.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
	declarationStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	declarationStore.session.Model = hostedGenesisWorkerOpenAIModel
	declarationStore.conv.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"describe"},{"role":"assistant","content":"assistant ready"}]`)
	srv = NewServer(config.Config{}, declarationStore, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	err = srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected declaration success error: %v", err)
	}
	assertHostedGenesisDeclarationReady(t, declarationStore)
}

func TestHostedGenesisWorkerFailsClosedOnEmptyAssistantResponse(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", hostedGenesisWorkerOpenAIKey)
	oldAssistant := runHostedGenesisAssistantModel
	t.Cleanup(func() { runHostedGenesisAssistantModel = oldAssistant })
	runHostedGenesisAssistantModel = func(context.Context, string, string, string, []llm.MintConversationMessage) (string, models.AIUsage, error) {
		return "", models.AIUsage{}, nil
	}

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Model = hostedGenesisWorkerOpenAIModel
	st.session.Model = hostedGenesisWorkerOpenAIModel
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected empty assistant response error: %v", err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureAssistantTurnFailed)
}

func TestHostedGenesisWorkerFailsClosedOnInvalidDeclarationDraft(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", hostedGenesisWorkerOpenAIKey)
	oldDeclaration := runHostedGenesisDeclarationModel
	t.Cleanup(func() { runHostedGenesisDeclarationModel = oldDeclaration })
	runHostedGenesisDeclarationModel = func(context.Context, string, string, llm.MintConversationDeclarationsInput) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
		draft := validHostedGenesisDraft()
		draft.SelfDescription.Purpose = "short"
		return draft, models.AIUsage{}, nil
	}

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Model = hostedGenesisWorkerOpenAIModel
	st.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
	st.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	st.session.Model = hostedGenesisWorkerOpenAIModel
	st.conv.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"describe"},{"role":"assistant","content":"assistant ready"}]`)
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	err := srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected invalid declaration draft error: %v", err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureDeclarationExtractionFailed)
}

func TestHostedGenesisWorkerReturnsDeclarationModelError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", hostedGenesisWorkerOpenAIKey)
	oldDeclaration := runHostedGenesisDeclarationModel
	t.Cleanup(func() { runHostedGenesisDeclarationModel = oldDeclaration })
	runHostedGenesisDeclarationModel = func(context.Context, string, string, llm.MintConversationDeclarationsInput) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
		return llm.MintConversationDeclarationsDraft{}, models.AIUsage{}, context.Canceled
	}

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv.Model = hostedGenesisWorkerOpenAIModel
	st.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
	st.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	st.session.Model = hostedGenesisWorkerOpenAIModel
	st.conv.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"describe"},{"role":"assistant","content":"assistant ready"}]`)
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	err := srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
	if err == nil {
		t.Fatalf("expected declaration model error")
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureDeclarationExtractionFailed)
}

func TestHostedGenesisWorkerRequiresStoreAndContext(t *testing.T) {
	t.Parallel()

	srv := &Server{}
	if err := srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker")); err == nil {
		t.Fatalf("expected assistant turn store initialization error")
	}
	if err := srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker")); err == nil {
		t.Fatalf("expected declaration extraction store initialization error")
	}
	if err := srv.handleHostedGenesisQueueMessage(nil, events.SQSMessage{}); err == nil {
		t.Fatalf("expected nil event context error")
	}
}

func TestHostedGenesisAPIKeyUsesProviderEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", " openai-env ")
	openAIKey, err := hostedGenesisAPIKey(context.Background(), "openai:gpt-5")
	if err != nil || openAIKey != "openai-env" {
		t.Fatalf("expected openai env key, key=%q err=%v", openAIKey, err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_API_KEY", " claude-env ")
	claudeKey, err := hostedGenesisAPIKey(context.Background(), "anthropic:claude-sonnet-4-6")
	if err != nil || claudeKey != "claude-env" {
		t.Fatalf("expected claude env key, key=%q err=%v", claudeKey, err)
	}
	if _, err := hostedGenesisAPIKey(context.Background(), "deterministic"); err == nil {
		t.Fatalf("expected unsupported model error")
	}
}

func TestHostedGenesisModelRunnersRejectUnsupportedModel(t *testing.T) {
	t.Parallel()

	if _, _, err := runHostedGenesisAssistantModel(context.Background(), "key", "deterministic", "system", nil); err == nil {
		t.Fatalf("expected unsupported assistant model error")
	}
	if _, _, err := runHostedGenesisDeclarationModel(context.Background(), "key", "deterministic", llm.MintConversationDeclarationsInput{}); err == nil {
		t.Fatalf("expected unsupported declaration model error")
	}
}

func TestHostedGenesisTranscriptAndStatusHelpers(t *testing.T) {
	t.Parallel()

	messages, err := decodeHostedGenesisMessages(models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"hello"},{"role":"assistant","content":"ready"}]`))
	if err != nil || !hostedGenesisHasAssistant(messages) {
		t.Fatalf("expected decoded assistant transcript, messages=%#v err=%v", messages, err)
	}
	if _, err := decodeHostedGenesisMessages(`not-json`); err == nil {
		t.Fatalf("expected invalid transcript json error")
	}

	if !hostedGenesisDomainActive(&models.Domain{Status: models.DomainStatusVerified}) ||
		!hostedGenesisDomainActive(&models.Domain{Status: models.DomainStatusActive}) ||
		hostedGenesisDomainActive(&models.Domain{Status: models.DomainStatusPending}) {
		t.Fatalf("domain active helper mismatch")
	}
	if hostedGenesisProvider("anthropic:claude") != "anthropic" || hostedGenesisProvider("deterministic") != "unknown" {
		t.Fatalf("provider helper mismatch")
	}
	if got := hostedGenesisAuditHash(" agent-1 "); !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+16 {
		t.Fatalf("unexpected audit hash: %q", got)
	}
	if hostedGenesisAuditHash("") != "" || firstNonEmptyWorker(" ", "\n") != "" {
		t.Fatalf("empty helper inputs should stay empty")
	}
	var srv *Server
	if st, ok := srv.hostedGenesisStore(); ok || st != nil {
		t.Fatalf("nil server should not expose hosted genesis store")
	}
}

func TestHostedGenesisPromptAndUsageHelpers(t *testing.T) {
	t.Parallel()

	reg := newHostedGenesisWorkerStore("turn-worker").reg
	reg.Capabilities = []string{"planning"}
	if prompt := hostedGenesisSystemPrompt(reg); !strings.Contains(prompt, "agent.example") || !strings.Contains(prompt, "planning") {
		t.Fatalf("system prompt omitted registration context: %q", prompt)
	}

	usage := addAIUsageWorker(models.AIUsage{Provider: testProviderOpenAI, InputTokens: 1}, models.AIUsage{Model: "gpt", InputTokens: 2, OutputTokens: 3, DurationMs: 4, ToolCalls: 1})
	if usage.Provider != testProviderOpenAI || usage.Model != "gpt" || usage.InputTokens != 3 || usage.OutputTokens != 3 || usage.TotalTokens != 5 || usage.DurationMs != 4 || usage.ToolCalls != 1 {
		t.Fatalf("usage merge mismatch: %#v", usage)
	}
}

func TestHostedGenesisDeclarationsDraftBuilder(t *testing.T) {
	t.Parallel()

	decl, err := buildHostedGenesisDeclarationsDraft(validHostedGenesisDraft(), time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC), "anthropic:claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("unexpected declarations draft error: %v", err)
	}
	if decl.SelfDescription.AuthoredBy != "agent" ||
		decl.SelfDescription.MintingModel != "anthropic:claude-sonnet-4-6" ||
		len(decl.Capabilities) != 1 ||
		len(decl.Boundaries) != 1 ||
		decl.Transparency == nil {
		t.Fatalf("unexpected produced declarations: %#v", decl)
	}
	badDraft := validHostedGenesisDraft()
	badDraft.SelfDescription.Purpose = "short"
	if _, err := buildHostedGenesisDeclarationsDraft(badDraft, time.Now(), "openai:gpt-5"); err == nil {
		t.Fatalf("expected invalid self-description error")
	}
}

func validHostedGenesisDraft() llm.MintConversationDeclarationsDraft {
	return llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{
			Purpose: "Help users plan hosted soul genesis with explicit safety limits.",
		},
		Capabilities: []soul.CapabilityV2{
			{Capability: "hosted_genesis_planning", Scope: "Draft safe registration declarations."},
			{Capability: "", Scope: "ignored"},
		},
		Boundaries: []llm.MintConversationBoundaryDraft{
			{Category: "refusal", Statement: "I will not reveal credentials.", Rationale: "protects operators"},
			{Category: "", Statement: ""},
		},
	}
}

func assertHostedGenesisWorkerFailure(t *testing.T, st *fakeHostedGenesisStore, reason string) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.putCount != 1 || st.conv == nil {
		t.Fatalf("expected one fail-closed conversation write, putCount=%d conv=%#v", st.putCount, st.conv)
	}
	if st.conv.Status != models.SoulMintConversationStatusFailed || st.conv.StatusReason != reason {
		t.Fatalf("expected %s failure, got %#v", reason, st.conv)
	}
	if st.session == nil || hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusFailed || st.session.Failure == nil || string(st.session.Failure.Code) != reason {
		t.Fatalf("expected session %s failure, got %#v", reason, st.session)
	}
}

func assertHostedGenesisAssistantReady(t *testing.T, st *fakeHostedGenesisStore) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.putCount != 1 || st.conv == nil {
		t.Fatalf("expected one assistant write, putCount=%d conv=%#v", st.putCount, st.conv)
	}
	if st.conv.Status != models.SoulMintConversationStatusAssistantTurnReady || st.conv.StatusReason != "" {
		t.Fatalf("expected assistant-ready status, got %#v", st.conv)
	}
	if st.session == nil || hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusAssistantTurnReady || st.session.AssistantCheckpointRef == "" {
		t.Fatalf("expected assistant-ready session checkpoint, got %#v", st.session)
	}
	decoded := models.DecodeSoulMintConversationBlob(st.conv.Messages)
	if !strings.Contains(decoded, "assistant ready") || strings.Contains(st.conv.Messages, "assistant ready") {
		t.Fatalf("assistant transcript encoding mismatch raw=%q decoded=%q", st.conv.Messages, decoded)
	}
	if st.conv.Usage.TotalTokens != 5 || st.conv.RequestID != "req-host" {
		t.Fatalf("assistant metadata mismatch: %#v", st.conv)
	}
}

func assertHostedGenesisDeclarationReady(t *testing.T, st *fakeHostedGenesisStore) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.putCount != 1 || st.conv == nil {
		t.Fatalf("expected one declaration write, putCount=%d conv=%#v", st.putCount, st.conv)
	}
	if st.conv.Status != models.SoulMintConversationStatusDeclarationReady || st.conv.CompletedAt.IsZero() {
		t.Fatalf("expected declaration-ready status, got %#v", st.conv)
	}
	if st.session == nil || hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusDeclarationReady || st.session.DeclarationCheckpoint == nil {
		t.Fatalf("expected declaration-ready session checkpoint, got %#v", st.session)
	}
	decoded := models.DecodeSoulMintConversationBlob(st.conv.ProducedDeclarations)
	if !strings.Contains(decoded, "hosted_genesis_planning") || strings.Contains(st.conv.ProducedDeclarations, "hosted_genesis_planning") {
		t.Fatalf("declaration encoding mismatch raw=%q decoded=%q", st.conv.ProducedDeclarations, decoded)
	}
	if st.conv.Usage.TotalTokens != 11 || st.conv.RequestID != "req-host" {
		t.Fatalf("declaration metadata mismatch: %#v", st.conv)
	}
}
