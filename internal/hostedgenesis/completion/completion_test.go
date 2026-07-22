package completion

import (
	"context"
	"encoding/json"
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

func TestRecordFailure_AppliesTypedFailure(t *testing.T) {
	store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Class:     hostedgenesis.FailureClassProviderTimeout,
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
	if got.Failure == nil || got.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed || got.Failure.Class != hostedgenesis.FailureClassProviderTimeout {
		t.Fatalf("expected typed failure persisted, got %+v", got.Failure)
	}
	if !got.Failure.Retryable {
		t.Fatalf("expected retryable failure")
	}
}

func TestRecordFailurePersistsOnlyCanonicalContentFreeProviderClasses(t *testing.T) {
	for _, class := range []hostedgenesis.FailureClass{
		hostedgenesis.FailureClassProviderTimeout,
		hostedgenesis.FailureClassProviderAPIFailure,
		hostedgenesis.FailureClassInvalidProviderOutput,
		hostedgenesis.FailureClassParseValidation,
	} {
		t.Run(string(class), func(t *testing.T) {
			store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)}
			got, err := NewCompletionWriter(store, nil).RecordFailure(context.Background(), CompletionTurn{
				InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1",
			}, CompletionFailure{
				Code: hostedgenesis.FailureCodeAssistantTurnFailed, Class: class,
				Message: "private provider response must not persist", Retryable: true,
				Recovery: hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep, MaxAttempts: 2, RetryAfterSeconds: 5, Reason: "private provider response must not persist"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Failure == nil || got.Failure.Class != class || got.Failure.Message != hostedgenesis.FailureMessage(hostedgenesis.FailureCodeAssistantTurnFailed) || got.Failure.Recovery.Reason != string(hostedgenesis.FailureCodeAssistantTurnFailed) {
				t.Fatalf("unexpected canonical failure persistence: %#v", got.Failure)
			}
			raw, marshalErr := json.Marshal(got.Failure)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if strings.Contains(string(raw), "private provider response") {
				t.Fatalf("durable failure leaked provider error detail: %s", raw)
			}
		})
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

func TestRecordFailure_CarriesCurrentSectionRetryBudget(t *testing.T) {
	prior := &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeAssistantTurnFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       2,
			RetryAfterSeconds: 30,
			Reason:            string(hostedgenesis.FailureCodeAssistantTurnFailed),
		},
	}
	session := newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, 3)
	session.Failure = prior
	store := &fakeCompletionStore{session: session}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
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
		got.Failure.Recovery.Reason != string(hostedgenesis.FailureCodeAssistantTurnFailed) {
		t.Fatalf("expected current-section retry budget carry-forward, got %#v", got.Failure)
	}
}

func TestRecordFailure_ExhaustedCurrentSectionRetryBecomesRestart(t *testing.T) {
	assertExhaustedRetryBecomesRestart(t, hostedgenesis.FailureCodeAssistantTurnFailed, 3)
}

func TestRecordFailure_ExhaustedMicroVMUnavailableRetryBecomesRestart(t *testing.T) {
	assertExhaustedRetryBecomesRestart(t, hostedgenesis.FailureCodeMicroVMUnavailable, 51)
}

func TestRecordFailure_SuspendedVMOnExhaustedCurrentSectionRetryStaysExhausted(t *testing.T) {
	prior := &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeAssistantTurnFailed),
		Retryable: false,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeAssistantTurnFailed),
		},
	}
	session := newFakeSession("trenchcoat", "K-JYArykVuog3gq-2lHBJw", "turn_bMLVA5B9Sb-J4U8AgzYNWA", hostedgenesis.StatusInProgress, 51)
	session.Failure = prior
	store := &fakeCompletionStore{session: session}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{
		InstanceSlug: "trenchcoat", ConversationID: "K-JYArykVuog3gq-2lHBJw", TurnID: "turn_bMLVA5B9Sb-J4U8AgzYNWA", RequestID: "req-observe-suspended",
	}, CompletionFailure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeMicroVMUnavailable),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Failure == nil ||
		got.Failure.Code != hostedgenesis.FailureCodeMicroVMUnavailable ||
		got.Failure.Retryable ||
		got.Failure.Recovery.Action != hostedgenesis.RecoveryActionRestartSoulBootstrap ||
		got.Failure.Recovery.MaxAttempts != 0 ||
		got.Failure.Recovery.RetryAfterSeconds != 0 {
		t.Fatalf("suspended observation must not resurrect exhausted declaration recovery, got %#v", got.Failure)
	}
}

func assertExhaustedRetryBecomesRestart(t *testing.T, code hostedgenesis.FailureCode, version int64) {
	t.Helper()
	prior := &hostedgenesis.Failure{
		Code:      code,
		Message:   hostedgenesis.FailureMessage(code),
		Retryable: false,
		Recovery: hostedgenesis.Recovery{
			Action: hostedgenesis.RecoveryActionRetrySameStep,
			Reason: string(code),
		},
	}
	session := newFakeSession("acme", "conv-1", "turn-1", hostedgenesis.StatusInProgress, version)
	session.Failure = prior
	store := &fakeCompletionStore{session: session}
	w := NewCompletionWriter(store, nil)

	got, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-exhausted"}, CompletionFailure{
		Code:      code,
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(code),
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
		t.Fatalf("expected exhausted %s retry to require a fresh soul bootstrap, got %#v", code, got.Failure)
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
	for _, status := range []hostedgenesis.Status{hostedgenesis.StatusFailed, hostedgenesis.StatusPublished} {
		t.Run(string(status), func(t *testing.T) {
			store := &fakeCompletionStore{session: newFakeSession("acme", "conv-1", "turn-1", status, 4)}
			w := NewCompletionWriter(store, nil)

			_, err := w.RecordFailure(context.Background(), CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}, CompletionFailure{
				Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
				Message:   "provider timed out",
				Retryable: true,
				Recovery:  hostedgenesis.Recovery{Action: hostedgenesis.RecoveryActionRetrySameStep, MaxAttempts: 3, RetryAfterSeconds: 5, Reason: "provider timed out"},
			})
			if err == nil {
				t.Fatalf("expected conflict on replay against %s, got nil", status)
			}
			if !errors.Is(err, ErrCompletionConflict) {
				t.Fatalf("expected ErrCompletionConflict, got %v", err)
			}
		})
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
