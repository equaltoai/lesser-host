package aiworker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeHostedGenesisStore struct {
	fakeAIStore

	mu         sync.Mutex
	reg        *models.SoulAgentRegistration
	domain     *models.Domain
	instance   *models.Instance
	session    *models.HostedGenesisSession
	conv       *models.SoulAgentMintConversation
	idem       *models.SoulMintConversationIdempotency
	putCount   int
	regErr     error
	domainErr  error
	instErr    error
	sessionErr error
	convErr    error
	idemErr    error
	updateErr  error
	putErr     error
}

const (
	hostedGenesisWorkerOpenAIKey   = "openai-test-key"
	hostedGenesisWorkerOpenAIModel = "openai:gpt-test"
	hostedGenesisWorkerAgentDomain = "agent.example"
	hostedGenesisWorkerMutated     = "mutated"
)

func (f *fakeHostedGenesisStore) GetSoulAgentRegistration(_ context.Context, id string) (*models.SoulAgentRegistration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.regErr != nil {
		return nil, f.regErr
	}
	if f.reg == nil || strings.TrimSpace(f.reg.ID) != strings.TrimSpace(id) {
		return nil, theoryErrors.ErrItemNotFound
	}
	cp := *f.reg
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetDomain(_ context.Context, domain string) (*models.Domain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.domainErr != nil {
		return nil, f.domainErr
	}
	if f.domain == nil || !strings.EqualFold(strings.TrimSpace(f.domain.Domain), strings.TrimSpace(domain)) {
		return nil, theoryErrors.ErrItemNotFound
	}
	cp := *f.domain
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetInstance(_ context.Context, slug string) (*models.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.instErr != nil {
		return nil, f.instErr
	}
	if f.instance == nil || !strings.EqualFold(strings.TrimSpace(f.instance.Slug), strings.TrimSpace(slug)) {
		return nil, theoryErrors.ErrItemNotFound
	}
	cp := *f.instance
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetSoulAgentMintConversation(_ context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.convErr != nil {
		return nil, f.convErr
	}
	if f.conv == nil ||
		!strings.EqualFold(strings.TrimSpace(f.conv.AgentID), strings.TrimSpace(agentID)) ||
		strings.TrimSpace(f.conv.ConversationID) != strings.TrimSpace(conversationID) {
		return nil, theoryErrors.ErrItemNotFound
	}
	cp := *f.conv
	return &cp, nil
}

func (f *fakeHostedGenesisStore) GetHostedGenesisSession(_ context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	if f.session == nil ||
		!strings.EqualFold(strings.TrimSpace(f.session.InstanceSlug), strings.TrimSpace(instanceSlug)) ||
		strings.TrimSpace(f.session.ConversationID) != strings.TrimSpace(conversationID) {
		return nil, theoryErrors.ErrItemNotFound
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
	if f.updateErr != nil {
		return f.updateErr
	}
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
	if f.putErr != nil {
		return f.putErr
	}
	cp := *item
	f.conv = &cp
	f.putCount++
	return nil
}

func (f *fakeHostedGenesisStore) FailHostedGenesisSessionAndConversation(_ context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error {
	if session == nil || conversation == nil {
		return theoryErrors.ErrItemNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	if f.putErr != nil {
		return f.putErr
	}
	if f.session == nil || f.session.Version != expectedVersion || hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus {
		return theoryErrors.ErrConditionFailed
	}
	sessionCopy := *session
	sessionCopy.Version = expectedVersion + 1
	sessionCopy.TurnLedger = append([]hostedgenesis.TurnLedgerEntry(nil), session.TurnLedger...)
	conversationCopy := *conversation
	f.session = &sessionCopy
	f.conv = &conversationCopy
	f.putCount++
	return nil
}

func (f *fakeHostedGenesisStore) GetSoulMintConversationIdempotency(_ context.Context, instanceSlug string, registrationID string, idempotencyKey string) (*models.SoulMintConversationIdempotency, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idemErr != nil {
		return nil, f.idemErr
	}
	if f.idem == nil ||
		!strings.EqualFold(strings.TrimSpace(f.idem.InstanceSlug), strings.TrimSpace(instanceSlug)) ||
		strings.TrimSpace(f.idem.RegistrationID) != strings.TrimSpace(registrationID) ||
		strings.TrimSpace(f.idem.IdempotencyKey) != strings.TrimSpace(idempotencyKey) {
		return nil, theoryErrors.ErrItemNotFound
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
			DomainNormalized: hostedGenesisWorkerAgentDomain,
			LocalID:          "agent",
			AgentID:          "0x" + strings.Repeat("44", 32),
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		domain: &models.Domain{
			Domain:       hostedGenesisWorkerAgentDomain,
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

	st = newHostedGenesisWorkerStore("turn-worker")
	srv = NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher
	err = srv.handleHostedGenesisQueueMessage(ctx, hostedGenesisWorkerSQSMessage(t, hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker")))
	if err != nil {
		t.Fatalf("unexpected microvm queue error: %v", err)
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 1 {
		t.Fatalf("expected queue to dispatch microvm start+invoke, got start=%d invoke=%d", dispatcher.startCalls, dispatcher.invokeCalls)
	}
}

func TestHostedGenesisMicroVMDispatchStartsPersistsLifecycleAndInvokes(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected microvm dispatch error: %v", err)
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 1 || dispatcher.dispatchCalls != 0 {
		t.Fatalf("expected split start+invoke path, got start=%d invoke=%d dispatch=%d", dispatcher.startCalls, dispatcher.invokeCalls, dispatcher.dispatchCalls)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.putCount != 0 {
		t.Fatalf("microvm dispatch must not rewrite the conversation directly, putCount=%d", st.putCount)
	}
	if st.session == nil || hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusInProgress {
		t.Fatalf("expected in-progress session after dispatch, got %#v", st.session)
	}
	if st.session.MicroVMLifecycleRef == nil || st.session.MicroVMExecutionID == "" || st.session.ExecutionStateRef == "" {
		t.Fatalf("expected persisted non-authoritative lifecycle refs, got %#v", st.session)
	}
	if !strings.Contains(st.session.ExecutionStateRef, "#running@7") {
		t.Fatalf("expected running lifecycle execution ref, got %q", st.session.ExecutionStateRef)
	}
	if st.session.Version != 1 {
		t.Fatalf("expected exactly one session lifecycle update, got version %d", st.session.Version)
	}
}

func TestHostedGenesisMicroVMDispatchStartsFreshWhenPriorLifecycleExists(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	binding := st.session.MicroVMSessionBinding()
	previous, err := hostedGenesisWorkerMicroVMDispatchResult(t, "previous-req", binding, runtimemicrovm.CommandRun)
	if err != nil {
		t.Fatalf("seed lifecycle ref: %v", err)
	}
	if applyErr := st.session.ApplyMicroVMLifecycleRef(previous.LifecycleRef); applyErr != nil {
		t.Fatalf("apply previous lifecycle ref: %v", applyErr)
	}
	st.session.Version = 3
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err = srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected microvm dispatch error: %v", err)
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 1 || dispatcher.dispatchCalls != 0 {
		t.Fatalf("expected fresh start+invoke despite previous lifecycle, got start=%d invoke=%d dispatch=%d", dispatcher.startCalls, dispatcher.invokeCalls, dispatcher.dispatchCalls)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.session == nil || st.session.Version != 4 {
		t.Fatalf("expected fresh lifecycle persist to advance version, got %#v", st.session)
	}
	if !strings.Contains(st.session.ExecutionStateRef, "#running@7") {
		t.Fatalf("expected refreshed running lifecycle execution ref, got %q", st.session.ExecutionStateRef)
	}
}

func TestHostedGenesisMicroVMDispatchAllowsManagedStageAliasBoundary(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.reg.DomainNormalized = "dev.agent.example"
	st.domain.Domain = hostedGenesisWorkerAgentDomain
	st.domain.Type = models.DomainTypePrimary
	st.domain.VerificationMethod = "managed"
	st.instance.HostedBaseDomain = hostedGenesisWorkerAgentDomain
	srv := NewServer(config.Config{Stage: "lab"}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected managed-stage alias dispatch error: %v", err)
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 1 {
		t.Fatalf("expected managed-stage alias to pass boundary and dispatch, got start=%d invoke=%d", dispatcher.startCalls, dispatcher.invokeCalls)
	}
}

func TestNewHostedGenesisWorkerMicroVMDispatcherConfigPaths(t *testing.T) {
	t.Parallel()

	cfg := config.Config{HostedGenesisMicroVM: config.HostedGenesisMicroVMConfig{
		Enabled: true,
	}}
	if got := newHostedGenesisWorkerMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisWorkerMicroVMDispatcherOptions{}); got != nil {
		t.Fatalf("incomplete worker microvm config must fail closed, got %#v", got)
	}

	cfg.HostedGenesisMicroVM = config.HostedGenesisMicroVMConfig{
		Enabled:                true,
		ControllerEndpoint:     "https://microvm-controller.example.test",
		AuthTokenSSMParam:      "/lesser-host/test/microvm/auth-token",
		ImageRef:               "image:test",
		NetworkConnectorRef:    "network:test",
		IngressConnectorRefs:   []string{"ingress:test"},
		EgressConnectorRefs:    []string{"egress:test"},
		MaximumDurationSeconds: 60,
	}
	if got := newHostedGenesisWorkerMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisWorkerMicroVMDispatcherOptions{}); got != nil {
		t.Fatalf("missing token getter must fail closed, got %#v", got)
	}
	if got := newHostedGenesisWorkerMicroVMDispatcher(context.Background(), cfg, func(context.Context, string) (string, error) {
		return "", errors.New("ssm unavailable")
	}, hostedGenesisWorkerMicroVMDispatcherOptions{}); got != nil {
		t.Fatalf("token fetch failure must fail closed, got %#v", got)
	}
	if got := newHostedGenesisWorkerMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisWorkerMicroVMDispatcherOptions{
		ssmGetParameter: func(context.Context, string) (string, error) {
			return "  ", nil
		},
	}); got != nil {
		t.Fatalf("empty fetched token must fail closed, got %#v", got)
	}

	fetched := false
	fromSSM := newHostedGenesisWorkerMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisWorkerMicroVMDispatcherOptions{
		ssmGetParameter: func(_ context.Context, name string) (string, error) {
			fetched = true
			if name != cfg.HostedGenesisMicroVM.AuthTokenSSMParam {
				t.Fatalf("unexpected token parameter name %q", name)
			}
			return " fetched-token ", nil
		},
		httpClient: &http.Client{Timeout: time.Second},
	})
	if fromSSM == nil || !fetched {
		t.Fatalf("expected dispatcher from fetched token, dispatcher=%#v fetched=%t", fromSSM, fetched)
	}

	fromDirectToken := newHostedGenesisWorkerMicroVMDispatcher(context.Background(), cfg, nil, hostedGenesisWorkerMicroVMDispatcherOptions{
		authToken:  " direct-token ",
		httpClient: &http.Client{Timeout: time.Second},
		ssmGetParameter: func(context.Context, string) (string, error) {
			t.Fatalf("direct auth token path must not fetch SSM")
			return "", nil
		},
	})
	if fromDirectToken == nil {
		t.Fatalf("expected dispatcher from direct auth token")
	}
}

func TestHostedGenesisMicroVMDispatchFallbackDispatcherPersistsLifecycle(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &legacyOnlyHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected fallback microvm dispatch error: %v", err)
	}
	if dispatcher.dispatchCalls != 1 {
		t.Fatalf("expected exactly one fallback dispatch, got %d", dispatcher.dispatchCalls)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.session == nil || st.session.MicroVMLifecycleRef == nil || st.session.Version != 1 {
		t.Fatalf("expected persisted fallback lifecycle ref and version increment, got %#v", st.session)
	}
}

func TestHostedGenesisMicroVMDispatchHelpers(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	if !hostedGenesisMicroVMDispatchJobReady(st.conv, st.session, hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker")) {
		t.Fatalf("expected active session/latest turn to be dispatch ready")
	}
	st.session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	if hostedGenesisMicroVMDispatchJobReady(st.conv, st.session, hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker")) {
		t.Fatalf("assistant-ready session must not redispatch the MicroVM")
	}
	if hostedGenesisMicroVMDispatchJobReady(nil, st.session, hostedgenesis.QueueMessage{}) ||
		hostedGenesisMicroVMDispatchJobReady(st.conv, nil, hostedgenesis.QueueMessage{}) {
		t.Fatalf("nil conversation/session must not be dispatch ready")
	}

	st = newHostedGenesisWorkerStore("turn-worker")
	if got := mintConversationMessageCountWorker(st.conv); got != 1 {
		t.Fatalf("expected one decoded message, got %d", got)
	}
	st.conv.Messages = models.EncodeSoulMintConversationBlob(`not-json`)
	if got := mintConversationMessageCountWorker(st.conv); got != 0 {
		t.Fatalf("invalid transcript count must fail closed, got %d", got)
	}
	if got := mintConversationMessageCountWorker(nil); got != 0 {
		t.Fatalf("nil transcript count must be zero, got %d", got)
	}
}

func TestCloneHostedGenesisSessionForWorkerDeepCopiesMutableFields(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	binding := st.session.MicroVMSessionBinding()
	dispatch, err := hostedGenesisWorkerMicroVMDispatchResult(t, "worker-req", binding, runtimemicrovm.CommandRun)
	if err != nil {
		t.Fatalf("build lifecycle ref: %v", err)
	}
	source := st.session
	source.MicroVMLifecycleRef = &dispatch.LifecycleRef
	source.DeclarationCheckpoint = &hostedgenesis.DeclarationCheckpoint{
		DeclarationID:  "decl-1",
		CheckpointRef:  "s3://checkpoint",
		RegistrationID: source.RegistrationID,
		ConversationID: source.ConversationID,
		AgentID:        source.AgentID,
		RequestID:      "req-1",
	}
	source.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Message:   "failed",
		Retryable: true,
		Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep},
	}
	source.TraceIDs = &hostedgenesis.TraceIDs{HostRequestID: "host-req"}

	cloned := cloneHostedGenesisSessionForWorker(source)
	if cloned == source || cloned == nil {
		t.Fatalf("expected a distinct cloned session")
	}
	source.TurnLedger[0].TurnID = hostedGenesisWorkerMutated
	source.MicroVMLifecycleRef.SessionID = hostedGenesisWorkerMutated
	source.DeclarationCheckpoint.DeclarationID = hostedGenesisWorkerMutated
	source.Failure.Message = hostedGenesisWorkerMutated
	source.TraceIDs.HostRequestID = hostedGenesisWorkerMutated

	if cloned.TurnLedger[0].TurnID != "turn-worker" ||
		cloned.MicroVMLifecycleRef.SessionID != "conv-worker" ||
		cloned.DeclarationCheckpoint.DeclarationID != "decl-1" ||
		cloned.Failure.Message != "failed" ||
		cloned.TraceIDs.HostRequestID != "host-req" {
		t.Fatalf("clone shared mutable state with source: %#v", cloned)
	}
	if cloneHostedGenesisSessionForWorker(nil) != nil {
		t.Fatalf("nil session clone must stay nil")
	}
}

func TestHostedGenesisMicroVMDispatchFailsClosedWhenDispatcherUnavailable(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected microvm-unavailable handling error: %v", err)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureMicroVMUnavailable)
}

func TestHostedGenesisMicroVMDispatchFailsClosedOnInvokeError(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t, invokeErr: errors.New("invoke unavailable")}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected invoke failure handling error: %v", err)
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 1 {
		t.Fatalf("expected start then invoke attempt, got start=%d invoke=%d", dispatcher.startCalls, dispatcher.invokeCalls)
	}
	assertHostedGenesisWorkerFailure(t, st, hostedGenesisFailureMicroVMUnavailable)
}

func TestHostedGenesisMicroVMDispatchRetriesWhenFailurePersistenceFails(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	binding := st.session.MicroVMSessionBinding()
	dispatch, err := hostedGenesisWorkerMicroVMDispatchResult(t, "req-host", binding, runtimemicrovm.CommandRun)
	if err != nil {
		t.Fatalf("seed lifecycle ref: %v", err)
	}
	if applyErr := st.session.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); applyErr != nil {
		t.Fatalf("apply lifecycle ref: %v", applyErr)
	}
	st.updateErr = errors.New("dynamodb unavailable")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t, invokeErr: errors.New("workload preflight failed")}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err = srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err == nil {
		t.Fatal("expected lifecycle-persistence error so SQS retries the dispatch message")
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 0 {
		t.Fatalf("expected fresh lifecycle persist failure before invoke, got start=%d invoke=%d", dispatcher.startCalls, dispatcher.invokeCalls)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusInProgress || st.putCount != 0 {
		t.Fatalf("failed authoritative session write must not acknowledge or partially fail the conversation: session=%#v putCount=%d", st.session, st.putCount)
	}
}

func TestHostedGenesisMicroVMDispatchRetriesAuthoritativeReadFailures(t *testing.T) {
	readErr := errors.New("dynamodb read unavailable")
	tests := []struct {
		name   string
		inject func(*fakeHostedGenesisStore)
	}{
		{name: "registration", inject: func(st *fakeHostedGenesisStore) { st.regErr = readErr }},
		{name: "domain", inject: func(st *fakeHostedGenesisStore) { st.domainErr = readErr }},
		{name: "instance", inject: func(st *fakeHostedGenesisStore) { st.instErr = readErr }},
		{name: "session", inject: func(st *fakeHostedGenesisStore) { st.sessionErr = readErr }},
		{name: "conversation", inject: func(st *fakeHostedGenesisStore) { st.convErr = readErr }},
		{name: "idempotency", inject: func(st *fakeHostedGenesisStore) { st.idemErr = readErr }},
		{name: "session identity failure projection", inject: func(st *fakeHostedGenesisStore) {
			st.session.RegistrationID = "other-registration"
			st.convErr = readErr
		}},
		{name: "boundary failure projection", inject: func(st *fakeHostedGenesisStore) {
			st.domain.InstanceSlug = "other-instance"
			st.convErr = readErr
		}},
		{name: "idempotency failure projection", inject: func(st *fakeHostedGenesisStore) {
			st.idem.TurnID = "other-turn"
			st.convErr = readErr
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newHostedGenesisWorkerStore("turn-worker")
			tt.inject(st)
			srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
			dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
			srv.hostedGenesisMicroVMDispatcher = dispatcher

			err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
			if !errors.Is(err, readErr) {
				t.Fatalf("expected authoritative %s read error to remain retryable, got %v", tt.name, err)
			}
			if dispatcher.startCalls != 0 || dispatcher.invokeCalls != 0 || dispatcher.dispatchCalls != 0 {
				t.Fatalf("read failure must stop before MicroVM dispatch, got start=%d invoke=%d dispatch=%d", dispatcher.startCalls, dispatcher.invokeCalls, dispatcher.dispatchCalls)
			}
			if hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusInProgress || st.conv.Status != models.SoulMintConversationStatusInProgress {
				t.Fatalf("read failure must not partially mutate durable truth: session=%s conversation=%s", st.session.Status, st.conv.Status)
			}
		})
	}
}

func TestHostedGenesisMicroVMDispatchFailurePersistenceIsAtomic(t *testing.T) {
	st := newHostedGenesisWorkerStore("turn-worker")
	binding := st.session.MicroVMSessionBinding()
	dispatch, err := hostedGenesisWorkerMicroVMDispatchResult(t, "req-host", binding, runtimemicrovm.CommandRun)
	if err != nil {
		t.Fatalf("seed lifecycle ref: %v", err)
	}
	if applyErr := st.session.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); applyErr != nil {
		t.Fatalf("apply lifecycle ref: %v", applyErr)
	}
	st.putErr = errors.New("conversation write unavailable")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t, invokeErr: errors.New("workload preflight failed")}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err = srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err == nil {
		t.Fatal("expected atomic failure-persistence error so SQS retries the dispatch message")
	}
	if hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusInProgress || st.conv.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("failed atomic persistence must leave both rows retryable: session=%s conversation=%s", st.session.Status, st.conv.Status)
	}
}

func TestHostedGenesisMicroVMDispatchFailsSessionWhenConversationIsMissing(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.conv = nil
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err != nil {
		t.Fatalf("unexpected missing-conversation failure projection error: %v", err)
	}
	if dispatcher.startCalls != 0 || dispatcher.invokeCalls != 0 || dispatcher.dispatchCalls != 0 {
		t.Fatalf("missing conversation must stop before MicroVM dispatch, got start=%d invoke=%d dispatch=%d", dispatcher.startCalls, dispatcher.invokeCalls, dispatcher.dispatchCalls)
	}
	if hostedgenesis.NormalizeStatus(st.session.Status) != hostedgenesis.StatusFailed || st.session.Failure == nil || st.session.Failure.Code != hostedgenesis.FailureCodeInvalidCompletionState {
		t.Fatalf("missing conversation must fail authoritative session truth, got %#v", st.session)
	}
}

func TestHostedGenesisMicroVMDispatchRetriesWhenLifecyclePersistenceFails(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.updateErr = errors.New("dynamodb unavailable")
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	dispatcher := &stubHostedGenesisWorkerMicroVMDispatcher{t: t}
	srv.hostedGenesisMicroVMDispatcher = dispatcher

	err := srv.processHostedGenesisMicroVMDispatch(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepMicroVMDispatch, "turn-worker"))
	if err == nil {
		t.Fatal("expected lifecycle-persistence error so SQS retries the dispatch message")
	}
	if dispatcher.startCalls != 1 || dispatcher.invokeCalls != 0 {
		t.Fatalf("failed lifecycle persistence must stop before invoke, got start=%d invoke=%d", dispatcher.startCalls, dispatcher.invokeCalls)
	}
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

func TestHostedGenesisWorkerRetriesWhenTerminalFailureProjectionFails(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Server, *fakeHostedGenesisStore) error
	}{
		{
			name: "assistant prepare",
			run: func(srv *Server, _ *fakeHostedGenesisStore) error {
				return srv.processHostedGenesisAssistantTurn(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
			},
		},
		{
			name: "declaration prepare",
			run: func(srv *Server, st *fakeHostedGenesisStore) error {
				st.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
				st.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
				return srv.processHostedGenesisDeclarationExtraction(context.Background(), "worker-req", hostedGenesisWorkerQueueMessage(hostedgenesis.StepDeclarationExtraction, "turn-worker"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newHostedGenesisWorkerStore("turn-worker")
			st.conv.Messages = models.EncodeSoulMintConversationBlob(`not-json`)
			st.putErr = errors.New("failure projection unavailable")
			srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

			err := tt.run(srv, st)
			if !errors.Is(err, st.putErr) {
				t.Fatalf("expected terminal failure persistence error to remain retryable, got %v", err)
			}
			if hostedgenesis.NormalizeStatus(st.session.Status) == hostedgenesis.StatusFailed || st.conv.Status == models.SoulMintConversationStatusFailed {
				t.Fatalf("failed atomic terminal projection must leave both rows retryable: session=%s conversation=%s", st.session.Status, st.conv.Status)
			}
		})
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

func TestHostedGenesisJobValidationAtomicallyFailsSessionIdentityMismatch(t *testing.T) {
	t.Parallel()

	st := newHostedGenesisWorkerStore("turn-worker")
	st.session.RegistrationID = "other-registration"
	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	reg, conv, session, err := srv.loadAndValidateHostedGenesisJob(context.Background(), st, hostedGenesisWorkerQueueMessage(hostedgenesis.StepAssistantTurn, "turn-worker"))
	if err != nil || reg != nil || conv != nil || session != nil {
		t.Fatalf("expected session identity mismatch to drop job, reg=%#v conv=%#v session=%#v err=%v", reg, conv, session, err)
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
		if apiKey != hostedGenesisWorkerOpenAIKey || modelSet != hostedGenesisWorkerOpenAIModel || !strings.Contains(systemPrompt, hostedGenesisWorkerAgentDomain) || len(messages) != 1 {
			t.Fatalf("unexpected assistant model input: key=%q model=%q prompt=%q messages=%#v", apiKey, modelSet, systemPrompt, messages)
		}
		return "assistant ready", models.AIUsage{Provider: testProviderOpenAI, Model: "gpt-test", InputTokens: 2, OutputTokens: 3}, nil
	}
	runHostedGenesisDeclarationModel = func(_ context.Context, apiKey string, modelSet string, in llm.MintConversationDeclarationsInput) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
		if apiKey != hostedGenesisWorkerOpenAIKey || modelSet != hostedGenesisWorkerOpenAIModel || in.Registration.Domain != hostedGenesisWorkerAgentDomain || len(in.Messages) != 2 {
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
	assertHostedGenesisWorkerFailureWithReason(t, st, hostedGenesisFailureInvalidProducedDeclarations, string(hostedgenesis.DeclarationCodeSelfDescription))
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
	prompt := hostedGenesisSystemPrompt(reg)
	if !strings.Contains(prompt, hostedGenesisWorkerAgentDomain) || !strings.Contains(prompt, "planning") {
		t.Fatalf("system prompt omitted registration context: %q", prompt)
	}
	if prompt != mintprompt.MintConversationSystemPrompt(reg) || strings.Contains(prompt, "minted on-chain") || !strings.Contains(prompt, mintprompt.CanonicalFinalAffirmationQuestion) {
		t.Fatalf("direct worker prompt drifted from shared hosted genesis prompt: %q", prompt)
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

func TestHostedGenesisDeclarationsDraftBuilderAllowsEmptyCapabilities(t *testing.T) {
	t.Parallel()

	draft := validHostedGenesisDraft()
	draft.Capabilities = nil
	decl, err := buildHostedGenesisDeclarationsDraft(draft, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC), "anthropic:claude-sonnet-4-6", []string{"simulacrum.hosted-first-default"})
	if err != nil {
		t.Fatalf("unexpected empty-capabilities error: %v", err)
	}
	if len(decl.Capabilities) != 0 {
		t.Fatalf("expected no fallback capabilities, got %#v", decl.Capabilities)
	}
}

func TestHostedGenesisDeclarationsDraftBuilderDropsPlaceholder(t *testing.T) {
	t.Parallel()

	draft := validHostedGenesisDraft()
	draft.Capabilities = []soul.CapabilityV2{{Capability: "simulacrum.hosted-first-default", Scope: "placeholder", ClaimLevel: "self-declared"}}
	decl, err := buildHostedGenesisDeclarationsDraft(draft, time.Now(), "openai:gpt-5")
	if err != nil {
		t.Fatalf("unexpected placeholder-only capabilities error: %v", err)
	}
	if len(decl.Capabilities) != 0 {
		t.Fatalf("expected placeholder capability to be dropped, got %#v", decl.Capabilities)
	}
}

func TestHostedGenesisDeclarationsDraftBuilderRequiresBoundary(t *testing.T) {
	t.Parallel()

	noBoundaryDraft := validHostedGenesisDraft()
	noBoundaryDraft.Boundaries = nil
	if _, err := buildHostedGenesisDeclarationsDraft(noBoundaryDraft, time.Now(), "openai:gpt-5"); err == nil || err.Error() != string(hostedgenesis.DeclarationCodeBoundaries) {
		t.Fatalf("expected required boundary error, got %v", err)
	}
}

func validHostedGenesisDraft() llm.MintConversationDeclarationsDraft {
	return llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{
			Purpose: "Help users plan hosted soul genesis with explicit safety limits.",
		},
		Capabilities: []soul.CapabilityV2{
			{Capability: "hosted_genesis_planning", Scope: "Draft safe registration declarations."},
		},
		Boundaries: []llm.MintConversationBoundaryDraft{
			{Category: "refusal", Statement: "I will not reveal credentials.", Rationale: "protects operators"},
		},
	}
}

type stubHostedGenesisWorkerMicroVMDispatcher struct {
	t              *testing.T
	startCalls     int
	invokeCalls    int
	dispatchCalls  int
	reconcileCalls int
	startErr       error
	invokeErr      error
	dispatchErr    error
	lastBinding    hostedgenesis.MicroVMSessionBinding
}

type legacyOnlyHostedGenesisWorkerMicroVMDispatcher struct {
	t             *testing.T
	dispatchCalls int
	lastBinding   hostedgenesis.MicroVMSessionBinding
}

func (d *legacyOnlyHostedGenesisWorkerMicroVMDispatcher) DispatchMicroVMRun(_ context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.dispatchCalls++
	d.lastBinding = binding
	return hostedGenesisWorkerMicroVMDispatchResult(d.t, requestID, binding, runtimemicrovm.CommandRun)
}

func (d *legacyOnlyHostedGenesisWorkerMicroVMDispatcher) ReconcileMicroVM(_ context.Context, _ string, binding hostedgenesis.MicroVMSessionBinding, ref hostedgenesis.MicroVMLifecycleRef) (hostedgenesis.MicroVMReconcileResult, error) {
	d.t.Helper()
	d.lastBinding = binding
	return hostedgenesis.MicroVMReconcileResult{LifecycleRef: ref, SessionID: ref.SessionID}, nil
}

func (d *stubHostedGenesisWorkerMicroVMDispatcher) StartMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.startCalls++
	d.lastBinding = binding
	if d.startErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.startErr
	}
	return hostedGenesisWorkerMicroVMDispatchResult(d.t, requestID, binding, runtimemicrovm.CommandRun)
}

func (d *stubHostedGenesisWorkerMicroVMDispatcher) WaitAndInvokeMicroVMTurn(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.invokeCalls++
	d.lastBinding = binding
	if d.invokeErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.invokeErr
	}
	return hostedGenesisWorkerMicroVMDispatchResult(d.t, requestID, binding, runtimemicrovm.CommandGet)
}

func (d *stubHostedGenesisWorkerMicroVMDispatcher) DispatchMicroVMRun(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding) (hostedgenesis.MicroVMDispatchResult, error) {
	d.t.Helper()
	d.dispatchCalls++
	d.lastBinding = binding
	if d.dispatchErr != nil {
		return hostedgenesis.MicroVMDispatchResult{}, d.dispatchErr
	}
	return hostedGenesisWorkerMicroVMDispatchResult(d.t, requestID, binding, runtimemicrovm.CommandRun)
}

func (d *stubHostedGenesisWorkerMicroVMDispatcher) ReconcileMicroVM(ctx context.Context, requestID string, binding hostedgenesis.MicroVMSessionBinding, ref hostedgenesis.MicroVMLifecycleRef) (hostedgenesis.MicroVMReconcileResult, error) {
	d.t.Helper()
	d.reconcileCalls++
	d.lastBinding = binding
	return hostedgenesis.MicroVMReconcileResult{LifecycleRef: ref, SessionID: ref.SessionID}, nil
}

func hostedGenesisWorkerMicroVMDispatchResult(t *testing.T, requestID string, binding hostedgenesis.MicroVMSessionBinding, command runtimemicrovm.Command) (hostedgenesis.MicroVMDispatchResult, error) {
	t.Helper()
	if err := binding.Validate(); err != nil {
		t.Fatalf("stub microvm dispatcher received invalid binding: %v", err)
	}
	if strings.TrimSpace(requestID) == "" {
		t.Fatalf("stub microvm dispatcher received empty request id")
	}
	resp := runtimemicrovm.ControllerResponse{
		Command:           command,
		RequestID:         requestID,
		TenantID:          binding.TenantID(),
		Namespace:         hostedgenesis.MicroVMNamespace,
		SessionID:         strings.TrimSpace(binding.ConversationID),
		State:             runtimemicrovm.StateRunning,
		DesiredState:      runtimemicrovm.StateRunning,
		LifecycleState:    runtimemicrovm.StateRunning,
		MicroVMID:         "mv-worker-" + strings.TrimSpace(binding.ConversationID),
		ProviderMicroVMID: "mv-worker-" + strings.TrimSpace(binding.ConversationID),
		LastAction:        command,
		LastTransition:    time.Now().UTC(),
		RegistryVersion:   7,
	}
	ref, err := hostedgenesis.MicroVMLifecycleRefFromResponse(binding, resp, time.Now().UTC())
	if err != nil {
		t.Fatalf("stub microvm dispatcher failed to build lifecycle ref: %v", err)
	}
	return hostedgenesis.MicroVMDispatchResult{LifecycleRef: ref, SessionID: resp.SessionID}, nil
}

func assertHostedGenesisWorkerFailure(t *testing.T, st *fakeHostedGenesisStore, reason string) {
	assertHostedGenesisWorkerFailureWithReason(t, st, reason, reason)
}

func assertHostedGenesisWorkerFailureWithReason(t *testing.T, st *fakeHostedGenesisStore, reason, statusReason string) {
	t.Helper()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.putCount != 1 || st.conv == nil {
		t.Fatalf("expected one fail-closed conversation write, putCount=%d conv=%#v", st.putCount, st.conv)
	}
	if st.conv.Status != models.SoulMintConversationStatusFailed || st.conv.StatusReason != statusReason {
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
