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
	session     *models.HostedGenesisSession
	updateErr   error
	lastExpectV int64
	lastExpectS hostedgenesis.Status
	written     *models.HostedGenesisSession
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

func TestRecordDeclarationReady_AppliesFromPending(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusDeclarationExtractionPending, 5)}
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
	if store.lastExpectS != hostedgenesis.StatusDeclarationExtractionPending {
		t.Fatalf("expected conditional on declaration_extraction_pending, got %q", store.lastExpectS)
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
