// Package completion provides the in-VM hosted-genesis workload's durable
// completion-write seam over the existing HostedGenesisSession store layer.
//
// It reuses the progression semantics of the control-plane
// persistHostedGenesisAcceptedAssistantTurn path — the same status transitions,
// AssistantCheckpointRef shape, and typed Failure envelope — but without the
// control-plane Server, idempotency, billing, or promotion coupling. Every write
// is idempotent per turn ID and conditional on the session's current status +
// optimistic-lock version: a replayed write for a turn whose session has already
// advanced past the expected status is rejected as ErrCompletionConflict rather
// than silently re-applied.
//
// The package lives under internal/hostedgenesis so the workload and tests
// depend on the hosted-genesis vocabulary, not the concrete *store.Store. The
// CompletionStore interface is satisfied by *store.Store.
package completion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// CompletionStore is the minimal HostedGenesisSession truth surface the in-VM
// genesis workload needs to durably record a turn outcome. It is satisfied by
// *store.Store; the workload never receives a raw AWS SDK client or a
// control-plane Server handle.
//
// GetHostedGenesisSession loads authoritative truth by tenant slug +
// conversation id. Failure convergence uses one guarded transaction across the
// authoritative session and its public projection.
type CompletionStore interface {
	GetHostedGenesisSession(ctx context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error)
	GetSoulAgentMintConversation(ctx context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error)
	FailHostedGenesisSessionAndConversation(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error
}

// CompletionTurn identifies the turn a completion write targets. The TurnID is
// the idempotency key: a second write for the same TurnID against a session that
// has already advanced past the expected status is rejected as a conflict rather
// than re-applied.
type CompletionTurn struct {
	InstanceSlug   string
	ConversationID string
	TurnID         string
	RequestID      string
}

// Validate fails closed if the turn is missing durable ids.
func (t CompletionTurn) Validate() error {
	if strings.TrimSpace(t.InstanceSlug) == "" ||
		strings.TrimSpace(t.ConversationID) == "" ||
		strings.TrimSpace(t.TurnID) == "" {
		return ErrInvalidCompletionTurn
	}
	return nil
}

// CompletionFailure is the typed failure the workload records when a turn or
// declaration phase fails. It maps to hostedgenesis.Failure with bounded
// recovery guidance authored by Host (clients never author recovery).
type CompletionFailure struct {
	Code      hostedgenesis.FailureCode
	Class     hostedgenesis.FailureClass
	Message   string
	Retryable bool
	Recovery  hostedgenesis.Recovery
}

// CompletionWriter records hosted-genesis turn outcomes to HostedGenesisSession
// truth through the existing store layer.
//
// Every write is idempotent per turn ID and conditional on the session's current
// status + optimistic-lock version. The clock is optional; if nil, time.Now UTC
// is used.
type CompletionWriter struct {
	store CompletionStore
	clock func() time.Time
}

// Sentinel errors for completion writes.
var (
	ErrInvalidCompletionTurn         = errors.New("hosted genesis completion turn is incomplete")
	ErrCompletionConflict            = errors.New("hosted genesis completion write conflicts with session state")
	ErrCompletionSessionMissing      = errors.New("hosted genesis completion session is missing")
	ErrCompletionConversationMissing = errors.New("hosted genesis completion conversation is missing")
)

// NewCompletionWriter constructs a CompletionWriter over the given store.
func NewCompletionWriter(store CompletionStore, clock func() time.Time) *CompletionWriter {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &CompletionWriter{store: store, clock: clock}
}

// RecordFailure transitions a non-terminal session to failed with a typed
// Failure envelope. A replay against an already-terminal session returns
// ErrCompletionConflict.
func (w *CompletionWriter) RecordFailure(ctx context.Context, turn CompletionTurn, failure CompletionFailure) (*models.HostedGenesisSession, error) {
	if w == nil || w.store == nil {
		return nil, ErrCompletionSessionMissing
	}
	if err := turn.Validate(); err != nil {
		return nil, err
	}
	f := hostedgenesis.Failure{
		Code:      failure.Code,
		Class:     failure.Class,
		Message:   hostedgenesis.FailureMessage(failure.Code),
		Retryable: failure.Retryable,
		Recovery:  failure.Recovery,
	}
	f.Recovery.Reason = hostedgenesis.SanitizeFailureReason(f.Code, f.Recovery.Reason)
	if err := f.Validate(); err != nil {
		return nil, err
	}

	session, err := w.store.GetHostedGenesisSession(ctx, turn.InstanceSlug, turn.ConversationID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCompletionSessionMissing, err)
	}
	expectedStatus := hostedgenesis.NormalizeStatus(session.Status)
	if completionFailureStatusTerminal(expectedStatus) {
		return nil, fmt.Errorf("%w: session is already terminal (%q)", ErrCompletionConflict, session.Status)
	}
	if err := assertTurnMatch(session, turn, expectedStatus); err != nil {
		return nil, err
	}
	f = applyPriorRecoveryBudget(f, session.Failure)
	if err := f.Validate(); err != nil {
		return nil, err
	}
	conversation, conversationErr := w.store.GetSoulAgentMintConversation(ctx, session.AgentID, session.ConversationID)
	if conversationErr != nil || conversation == nil {
		return nil, fmt.Errorf("%w: %v", ErrCompletionConversationMissing, conversationErr)
	}
	if !strings.EqualFold(strings.TrimSpace(conversation.AgentID), strings.TrimSpace(session.AgentID)) ||
		strings.TrimSpace(conversation.ConversationID) != strings.TrimSpace(session.ConversationID) {
		return nil, fmt.Errorf("%w: conversation identity does not match session", ErrCompletionConflict)
	}

	now := w.clock()
	progressed := cloneSessionForCompletion(session)
	progressed.Status = string(hostedgenesis.StatusFailed)
	progressed.Failure = &f
	progressed.RequestID = strings.TrimSpace(turn.RequestID)
	progressed.UpdatedAt = now
	progressed.CompletedAt = now
	failedConversation := *conversation
	failedConversation.Status = models.SoulMintConversationStatusFailed
	failedConversation.StatusReason = f.Recovery.Reason
	failedConversation.LatestTurnID = strings.TrimSpace(turn.TurnID)
	failedConversation.RequestID = strings.TrimSpace(turn.RequestID)
	failedConversation.UpdatedAt = now
	failedConversation.CompletedAt = now

	if err := w.store.FailHostedGenesisSessionAndConversation(ctx, progressed, session.Version, expectedStatus, &failedConversation); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCompletionConflict, err)
	}
	return progressed, nil
}

func completionFailureStatusTerminal(status hostedgenesis.Status) bool {
	switch status {
	case hostedgenesis.StatusFailed, hostedgenesis.StatusDeclarationReady, hostedgenesis.StatusPublished:
		return true
	default:
		return false
	}
}

func applyPriorRecoveryBudget(next hostedgenesis.Failure, prior *hostedgenesis.Failure) hostedgenesis.Failure {
	if prior == nil ||
		!isBoundedRecoveryFailure(next.Code) ||
		!isBoundedRecoveryFailure(prior.Code) ||
		(next.Code != prior.Code && next.Code != hostedgenesis.FailureCodeMicroVMUnavailable) {
		return next
	}
	switch prior.Recovery.Action {
	case hostedgenesis.RecoveryActionRestartSoulBootstrap:
		next.Retryable = false
		next.Recovery.Action = hostedgenesis.RecoveryActionRestartSoulBootstrap
		next.Recovery.MaxAttempts = 0
		next.Recovery.RetryAfterSeconds = 0
	case hostedgenesis.RecoveryActionRetrySameStep:
		if prior.Recovery.MaxAttempts < next.Recovery.MaxAttempts {
			next.Recovery.MaxAttempts = prior.Recovery.MaxAttempts
		}
		if next.Recovery.MaxAttempts <= 0 {
			next.Retryable = false
			next.Recovery.Action = hostedgenesis.RecoveryActionRestartSoulBootstrap
			next.Recovery.MaxAttempts = 0
			next.Recovery.RetryAfterSeconds = 0
		}
	}
	return next
}

func isBoundedRecoveryFailure(code hostedgenesis.FailureCode) bool {
	switch code {
	case hostedgenesis.FailureCodeAssistantTurnFailed,
		hostedgenesis.FailureCodeMicroVMUnavailable:
		return true
	default:
		return false
	}
}

// assertTurnMatch enforces the per-turn idempotency precondition. The loaded
// session's LatestTurnID (or last ledger entry's TurnID) must equal the
// completion turn's TurnID, and the session status must equal the expected
// status the caller will condition the write on. A mismatch is a conflict.
func assertTurnMatch(session *models.HostedGenesisSession, turn CompletionTurn, expectedStatus hostedgenesis.Status) error {
	if session == nil {
		return ErrCompletionSessionMissing
	}
	sessionTurn := strings.TrimSpace(session.LatestTurnID)
	if sessionTurn == "" && len(session.TurnLedger) > 0 {
		sessionTurn = strings.TrimSpace(session.TurnLedger[len(session.TurnLedger)-1].TurnID)
	}
	if sessionTurn != strings.TrimSpace(turn.TurnID) {
		return fmt.Errorf("%w: session turn %q does not match completion turn %q", ErrCompletionConflict, sessionTurn, turn.TurnID)
	}
	if hostedgenesis.NormalizeStatus(session.Status) != expectedStatus {
		return fmt.Errorf("%w: session status %q does not match expected %q", ErrCompletionConflict, session.Status, expectedStatus)
	}
	return nil
}

// cloneSessionForCompletion copies the loaded session for a progression write,
// deep-copying nested pointer fields. Mirrors control-plane
// cloneHostedGenesisSession.
func cloneSessionForCompletion(session *models.HostedGenesisSession) *models.HostedGenesisSession {
	if session == nil {
		return nil
	}
	copy := *session
	copy.TurnLedger = append([]hostedgenesis.TurnLedgerEntry(nil), session.TurnLedger...)
	if session.MicroVMLifecycleRef != nil {
		ref := *session.MicroVMLifecycleRef
		copy.MicroVMLifecycleRef = &ref
	}
	if session.DeclarationCheckpoint != nil {
		cp := *session.DeclarationCheckpoint
		copy.DeclarationCheckpoint = &cp
	}
	copy.DeclarationCandidate = session.DeclarationCandidate.Clone()
	if session.Failure != nil {
		failure := *session.Failure
		copy.Failure = &failure
	}
	if session.TraceIDs != nil {
		trace := *session.TraceIDs
		copy.TraceIDs = &trace
	}
	if session.VMCheckpoint != nil {
		checkpoint := *session.VMCheckpoint
		copy.VMCheckpoint = &checkpoint
	}
	return &copy
}
