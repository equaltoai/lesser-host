package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fakeCompletionStore is an in-memory CompletionStore for idempotency proofs.
// It records the last conditional update's expected version + status and can be
// programmed to return a condition failure to simulate a stale/progressed
// session.
type fakeCompletionStore struct {
	session      *models.HostedGenesisSession
	conversation *models.SoulAgentMintConversation
	updateErr    error
	lastExpectV  int64
	lastExpectS  hostedgenesis.Status
	written      *models.HostedGenesisSession
}

func (f *fakeCompletionStore) GetHostedGenesisSession(_ context.Context, instanceSlug, conversationID string) (*models.HostedGenesisSession, error) {
	if f.session == nil || f.session.InstanceSlug != instanceSlug || f.session.ConversationID != conversationID {
		return nil, fmt.Errorf("not found")
	}
	return cloneSessionForCompletion(f.session), nil
}

func (f *fakeCompletionStore) UpdateHostedGenesisSession(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	f.lastExpectV = expectedVersion
	f.lastExpectS = expectedStatus
	if f.updateErr != nil {
		return f.updateErr
	}
	f.written = cloneSessionForCompletion(item)
	f.session = cloneSessionForCompletion(item)
	f.session.Version = expectedVersion + 1
	return nil
}

func (f *fakeCompletionStore) GetSoulAgentMintConversation(_ context.Context, agentID, conversationID string) (*models.SoulAgentMintConversation, error) {
	if f.session == nil || !strings.EqualFold(f.session.AgentID, agentID) || f.session.ConversationID != conversationID {
		return nil, fmt.Errorf("not found")
	}
	if f.conversation == nil {
		return &models.SoulAgentMintConversation{AgentID: agentID, ConversationID: conversationID, Status: models.SoulMintConversationStatusInProgress}, nil
	}
	copy := *f.conversation
	return &copy, nil
}

func (f *fakeCompletionStore) FailHostedGenesisSessionAndConversation(_ context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error {
	f.lastExpectV = expectedVersion
	f.lastExpectS = expectedStatus
	if f.updateErr != nil {
		return f.updateErr
	}
	if f.session == nil || f.session.Version != expectedVersion || hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus {
		return fmt.Errorf("conditional check failed")
	}
	failedSession := cloneSessionForCompletion(session)
	failedSession.Version = expectedVersion + 1
	f.written = failedSession
	f.session = cloneSessionForCompletion(failedSession)
	if conversation != nil {
		copy := *conversation
		f.conversation = &copy
	}
	return nil
}

func newFakeSession(slug, conv, turn string, status hostedgenesis.Status, version int64) *models.HostedGenesisSession {
	return &models.HostedGenesisSession{
		InstanceSlug:   slug,
		ConversationID: conv,
		RegistrationID: "reg-1",
		AgentID:        "agent-1",
		Status:         string(status),
		LatestTurnID:   turn,
		TurnLedger:     []hostedgenesis.TurnLedgerEntry{{TurnID: turn, MessageCount: 1, AcceptedAt: time.Now().UTC()}},
		MessageCount:   1,
		Version:        version,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}

func hex64() string {
	return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}

func TestRecordAssistantTurnReady_AppliesConditionalWrite(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)}
	w := NewCompletionWriter(store, func() time.Time { return time.Unix(1000, 0).UTC() })

	turn := CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}
	got, err := w.RecordAssistantTurnReady(context.Background(), turn, AssistantTurnCompletion{
		AssistantContent: "hello",
		MessageCount:     2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != string(hostedgenesis.StatusAssistantTurnReady) {
		t.Fatalf("expected assistant_turn_ready, got %q", got.Status)
	}
	if store.lastExpectV != 3 || store.lastExpectS != hostedgenesis.StatusInProgress {
		t.Fatalf("expected conditional on version=3 status=in_progress, got version=%d status=%q", store.lastExpectV, store.lastExpectS)
	}
	if got.AssistantCheckpointRef != hostedgenesis.CheckpointRef("assistant", "conv-1", "turn-1") {
		t.Fatalf("unexpected assistant checkpoint ref %q", got.AssistantCheckpointRef)
	}
	if got.MessageCount != 2 {
		t.Fatalf("expected message count 2, got %d", got.MessageCount)
	}
	if !got.UpdatedAt.Equal(time.Unix(1000, 0).UTC()) {
		t.Fatalf("expected clock applied, got %v", got.UpdatedAt)
	}
}

// TestRecordAssistantTurnReady_ReplayedTurnIDRejected proves the idempotency
// invariant required by H1.1: a second write for the same turn ID against a
// session that has already advanced past in_progress (here, to
// assistant_turn_ready) is rejected as a conflict, not silently re-applied.
func TestRecordAssistantTurnReady_ReplayedTurnIDRejected(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusAssistantTurnReady, 4)}
	w := NewCompletionWriter(store, nil)

	turn := CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}
	_, err := w.RecordAssistantTurnReady(context.Background(), turn, AssistantTurnCompletion{
		AssistantContent: "hello again",
		MessageCount:     2,
	})
	if err == nil {
		t.Fatal("expected conflict error on replayed turn against progressed status, got nil")
	}
	if !errors.Is(err, ErrCompletionConflict) {
		t.Fatalf("expected ErrCompletionConflict, got %v", err)
	}
	if store.written != nil {
		t.Fatalf("expected no write applied on conflict, but a write was recorded")
	}
}

// TestRecordAssistantTurnReady_MismatchedTurnIDRejected proves a completion
// write for a turn ID that does not match the session's latest turn is rejected.
func TestRecordAssistantTurnReady_MismatchedTurnIDRejected(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)}
	w := NewCompletionWriter(store, nil)

	turn := CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-other", RequestID: "req-1"}
	_, err := w.RecordAssistantTurnReady(context.Background(), turn, AssistantTurnCompletion{
		AssistantContent: "hello",
		MessageCount:     2,
	})
	if err == nil {
		t.Fatal("expected conflict error on mismatched turn ID, got nil")
	}
	if !errors.Is(err, ErrCompletionConflict) {
		t.Fatalf("expected ErrCompletionConflict, got %v", err)
	}
}

// TestRecordAssistantTurnReady_StaleVersionRejected proves a store condition
// failure (stale version) surfaces as ErrCompletionConflict, not a silent
// overwrite.
func TestRecordAssistantTurnReady_StaleVersionRejected(t *testing.T) {
	store := &fakeCompletionStore{
		session:   newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3),
		updateErr: fmt.Errorf("conditional check failed"),
	}
	w := NewCompletionWriter(store, nil)

	turn := CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}
	_, err := w.RecordAssistantTurnReady(context.Background(), turn, AssistantTurnCompletion{
		AssistantContent: "hello",
		MessageCount:     2,
	})
	if err == nil {
		t.Fatal("expected conflict error on stale version, got nil")
	}
	if !errors.Is(err, ErrCompletionConflict) {
		t.Fatalf("expected ErrCompletionConflict, got %v", err)
	}
}

func TestRecordDeclarationReady_AppliesFromActorOwnedStatuses(t *testing.T) {
	for name, status := range map[string]hostedgenesis.Status{
		"in-progress actor finalization": hostedgenesis.StatusInProgress,
		"legacy pending extraction":      hostedgenesis.StatusDeclarationExtractionPending,
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", status, 5)}
			w := NewCompletionWriter(store, func() time.Time { return time.Unix(2000, 0).UTC() })

			cp := hostedgenesis.DeclarationCheckpoint{
				DeclarationID:   "decl-1",
				DeclarationHash: "sha256:" + hex64(),
				CheckpointRef:   "checkpoint://hosted-genesis/conv-1/declaration/turn-1",
				ProducedAt:      time.Unix(2000, 0).UTC(),
				RegistrationID:  "reg-1",
				ConversationID:  "conv-1",
				AgentID:         "agent-1",
				MessageCount:    2,
				RequestID:       "req-1",
			}
			got, err := w.RecordDeclarationReady(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, cp)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != string(hostedgenesis.StatusDeclarationReady) {
				t.Fatalf("expected declaration_ready, got %q", got.Status)
			}
			if got.DeclarationCheckpoint == nil || got.DeclarationCheckpoint.DeclarationID != "decl-1" {
				t.Fatalf("expected declaration checkpoint persisted, got %+v", got.DeclarationCheckpoint)
			}
			if store.lastExpectS != status {
				t.Fatalf("expected conditional on %s, got %q", status, store.lastExpectS)
			}
		})
	}
}

// TestRecordDeclarationReady_ReplayRejected proves a second declaration write
// for a session already at declaration_ready is rejected.
func TestRecordDeclarationReady_ReplayRejected(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusDeclarationReady, 6)}
	w := NewCompletionWriter(store, nil)

	cp := hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl-1",
		DeclarationHash: "sha256:" + hex64(),
		CheckpointRef:   "checkpoint://hosted-genesis/conv-1/declaration/turn-1",
		ProducedAt:      time.Unix(2000, 0).UTC(),
		RegistrationID:  "reg-1",
		ConversationID:  "conv-1",
		AgentID:         "agent-1",
		MessageCount:    2,
		RequestID:       "req-1",
	}
	_, err := w.RecordDeclarationReady(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, cp)
	if err == nil {
		t.Fatal("expected conflict on replay against declaration_ready, got nil")
	}
	if !errors.Is(err, ErrCompletionConflict) {
		t.Fatalf("expected ErrCompletionConflict, got %v", err)
	}
}

func TestRecordFailure_AppliesTypedFailure(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   "provider timed out",
		Retryable: true,
		Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep, MaxAttempts: 3, RetryAfterSeconds: 5, Reason: "provider timed out"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != string(hostedgenesis.StatusFailed) {
		t.Fatalf("expected failed, got %q", got.Status)
	}
	if got.Failure == nil || got.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed {
		t.Fatalf("expected typed failure persisted, got %+v", got.Failure)
	}
	if !got.Failure.Retryable {
		t.Fatalf("expected retryable failure")
	}
}

func TestRecordFailureAlignsConversationLatestTurnID(t *testing.T) {
	store := &fakeCompletionStore{
		session: newFakeSession("acme", "conv-1", "turn-2", hostedgenesis.StatusInProgress, 3),
		conversation: &models.SoulAgentMintConversation{
			AgentID:        "agent-1",
			ConversationID: "conv-1",
			Status:         models.SoulMintConversationStatusInProgress,
			LatestTurnID:   "turn-stale",
		},
	}
	w := NewCompletionWriter(store, nil)

	_, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-2", RequestID: "req-2"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Retryable: true,
		Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep, MaxAttempts: 3, RetryAfterSeconds: 5, Reason: string(hostedgenesis.FailureCodeAssistantTurnFailed)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.conversation == nil ||
		store.conversation.Status != models.SoulMintConversationStatusFailed ||
		store.conversation.LatestTurnID != "turn-2" ||
		store.conversation.StatusReason != string(hostedgenesis.FailureCodeAssistantTurnFailed) {
		t.Fatalf("expected failed compatibility conversation aligned to authoritative turn, got %#v", store.conversation)
	}
}

func TestRecordFailure_CarriesDeclarationRetryBudget(t *testing.T) {
	prior := &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       2,
			RetryAfterSeconds: 30,
			Reason:            string(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		},
	}
	session := newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusDeclarationExtractionPending, 3)
	session.Failure = prior
	store := &fakeCompletionStore{session: session}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 30,
			Reason:            "provider returned partial private JSON",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Failure == nil ||
		got.Failure.Recovery.MaxAttempts != 2 ||
		got.Failure.Recovery.Action != hostedgenesis.RecoveryActionRetrySameStep ||
		got.Failure.Recovery.Reason != string(hostedgenesis.FailureCodeDeclarationExtractionFailed) {
		t.Fatalf("expected declaration retry budget carry-forward, got %#v", got.Failure)
	}
}

func TestRecordFailure_ExhaustedDeclarationRetryBecomesRestart(t *testing.T) {
	prior := &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		Retryable: false,
		Recovery: hostedgenesis.Recovery{
			Action: hostedgenesis.RecoveryActionRetrySameStep,
			Reason: string(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		},
	}
	session := newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusDeclarationExtractionPending, 3)
	session.Failure = prior
	store := &fakeCompletionStore{session: session}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 30,
			Reason:            string(hostedgenesis.FailureCodeDeclarationExtractionFailed),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Failure == nil ||
		got.Failure.Retryable ||
		got.Failure.Recovery.Action != hostedgenesis.RecoveryActionRestartSoulBootstrap ||
		got.Failure.Recovery.MaxAttempts != 0 ||
		got.Failure.Recovery.RetryAfterSeconds != 0 {
		t.Fatalf("expected exhausted declaration retry to become restart guidance, got %#v", got.Failure)
	}
}

func TestRecordFailure_SanitizesProviderAndDeclarationDetailsAcrossProjections(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3), conversation: &models.SoulAgentMintConversation{
		AgentID:        "agent-1",
		ConversationID: "conv-1",
		Status:         models.SoulMintConversationStatusInProgress,
	}}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:     hostedgenesis.FailureCodeInvalidProducedDeclarations,
		Message:  "provider leaked secret transcript and private declaration",
		Recovery: hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRestartSoulBootstrap, Reason: string(hostedgenesis.DeclarationCodeCapabilities)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Failure == nil || got.Failure.Message != hostedgenesis.FailureMessage(hostedgenesis.FailureCodeInvalidProducedDeclarations) {
		t.Fatalf("expected fixed failure message, got %#v", got.Failure)
	}
	if got.Failure.Recovery.Reason != string(hostedgenesis.DeclarationCodeCapabilities) {
		t.Fatalf("expected only stable declaration detail, got %#v", got.Failure.Recovery)
	}
	if store.conversation == nil || store.conversation.Status != models.SoulMintConversationStatusFailed || store.conversation.StatusReason != string(hostedgenesis.DeclarationCodeCapabilities) {
		t.Fatalf("expected compatibility failure projection, got %#v", store.conversation)
	}
	encoded := fmt.Sprintf("%#v %#v", got.Failure, store.conversation)
	if strings.Contains(encoded, "secret transcript") || strings.Contains(encoded, "private declaration") {
		t.Fatalf("failure projection leaked private details: %s", encoded)
	}
}

func TestRecordFailure_ReplayAgainstTerminalRejected(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusFailed, 4)}
	w := NewCompletionWriter(store, nil)

	_, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   "provider timed out",
		Retryable: true,
		Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep, MaxAttempts: 3, RetryAfterSeconds: 5, Reason: "provider timed out"},
	})
	if err == nil {
		t.Fatal("expected conflict on replay against failed, got nil")
	}
	if !errors.Is(err, ErrCompletionConflict) {
		t.Fatalf("expected ErrCompletionConflict, got %v", err)
	}
}

func TestRecordAssistantTurnReady_MissingSessionRejected(t *testing.T) {
	store := &fakeCompletionStore{session: nil}
	w := NewCompletionWriter(store, nil)
	_, err := w.RecordAssistantTurnReady(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-missing", TurnID: "turn-1"}, AssistantTurnCompletion{AssistantContent: "hi"})
	if err == nil {
		t.Fatal("expected missing-session error, got nil")
	}
	if !errors.Is(err, ErrCompletionSessionMissing) {
		t.Fatalf("expected ErrCompletionSessionMissing, got %v", err)
	}
}

func TestRecordAssistantTurnReady_EmptyContentRejected(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)}
	w := NewCompletionWriter(store, nil)
	_, err := w.RecordAssistantTurnReady(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1"}, AssistantTurnCompletion{AssistantContent: "  "})
	if err == nil {
		t.Fatal("expected error for empty assistant content, got nil")
	}
}

func TestCompletionTurn_Validate(t *testing.T) {
	cases := []struct {
		name string
		turn CompletionTurn
		ok   bool
	}{
		{"valid", CompletionTurn{InstanceSlug: "a", ConversationID: "c", TurnID: "t"}, true},
		{"missing slug", CompletionTurn{ConversationID: "c", TurnID: "t"}, false},
		{"missing conv", CompletionTurn{InstanceSlug: "a", TurnID: "t"}, false},
		{"missing turn", CompletionTurn{InstanceSlug: "a", ConversationID: "c"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.turn.Validate()
			if c.ok && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

var _ = strings.TrimSpace // keep strings import for future assertion helpers
