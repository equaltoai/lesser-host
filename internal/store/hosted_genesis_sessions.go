package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetHostedGenesisSession loads a Host-owned durable hosted-genesis session by
// tenant slug and conversation id. The tenant slug is part of the primary key;
// callers must not look up hosted genesis sessions by conversation id alone.
func (s *Store) GetHostedGenesisSession(ctx context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error) {
	instanceSlug = strings.TrimSpace(instanceSlug)
	conversationID = strings.TrimSpace(conversationID)
	if instanceSlug == "" || conversationID == "" {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.HostedGenesisSession
	if err := s.getByPKSK(
		ctx,
		&models.HostedGenesisSession{},
		models.HostedGenesisSessionPK(instanceSlug),
		models.HostedGenesisSessionSK(conversationID),
		&item,
	); err != nil {
		return nil, err
	}
	if err := hostedgenesis.NormalizePersistedDeclarationCandidate(item.DeclarationCandidate); err != nil {
		return nil, fmt.Errorf("normalize hosted genesis declaration candidate: %w", err)
	}
	return &item, nil
}

// CreateHostedGenesisSession writes a new durable hosted-genesis source-of-
// truth row. Use UpdateHostedGenesisSession for any later mutation so state
// transitions are guarded by the expected TableTheory version.
func (s *Store) CreateHostedGenesisSession(ctx context.Context, item *models.HostedGenesisSession) error {
	if s == nil || s.DB == nil || item == nil {
		return theoryErrors.ErrItemNotFound
	}
	if err := item.BeforeCreate(); err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Create(item)
		return nil
	})
}

// UpdateHostedGenesisSession updates a durable hosted-genesis source-of-truth
// row under an explicit optimistic-lock version and expected current status. A
// stale expectedVersion or expectedStatus must fail as a transaction condition
// error rather than silently overwriting state.
func (s *Store) UpdateHostedGenesisSession(ctx context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if s == nil || s.DB == nil || item == nil {
		return theoryErrors.ErrItemNotFound
	}
	expectedStatus, err := validateHostedGenesisSessionUpdate(item, expectedVersion, expectedStatus)
	if err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		addHostedGenesisSessionUpdate(tx, item, expectedVersion, expectedStatus)
		return nil
	})
}

// CheckpointHostedGenesisCandidate writes one accepted phase-local tool result
// under tenant/session/turn/version/revision/hash guards. The candidate is part
// of the authoritative HostedGenesisSession row, so the candidate checkpoint
// and session version advance in the same TableTheory transaction.
func (s *Store) CheckpointHostedGenesisCandidate(ctx context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, expectedTurnID string, expectedCandidateRevision int64, expectedCandidateHash string) error {
	if s == nil || s.DB == nil || item == nil || item.DeclarationCandidate == nil {
		return theoryErrors.ErrItemNotFound
	}
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	expectedCandidateHash = strings.TrimSpace(expectedCandidateHash)
	if expectedTurnID == "" || expectedCandidateRevision < 0 || expectedCandidateHash == "" || strings.TrimSpace(item.LatestTurnID) != expectedTurnID {
		return fmt.Errorf("hosted genesis candidate checkpoint guard is invalid")
	}
	expectedStatus, err := validateHostedGenesisSessionUpdate(item, expectedVersion, expectedStatus)
	if err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		addHostedGenesisSessionUpdate(tx, item, expectedVersion, expectedStatus,
			tabletheory.Condition("LatestTurnID", "=", expectedTurnID),
			tabletheory.Condition("CandidateRevision", "=", expectedCandidateRevision),
			tabletheory.Condition("CandidateHash", "=", expectedCandidateHash))
		return nil
	})
}

// RecordHostedGenesisAssistantTurnAndConversation atomically advances the
// authoritative session and its conversation projection after a phase-local
// provider run. The candidate revision/hash/phase are conditions on the same
// transaction, so a stale provider completion cannot publish a transcript over
// a newer accepted section or another turn.
func (s *Store) RecordHostedGenesisAssistantTurnAndConversation(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, expectedTurnID string, expectedCandidateRevision int64, expectedCandidateHash string, conversation *models.SoulAgentMintConversation) error {
	if s == nil || s.DB == nil || session == nil || session.DeclarationCandidate == nil || conversation == nil {
		return theoryErrors.ErrItemNotFound
	}
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	expectedCandidateHash = strings.TrimSpace(expectedCandidateHash)
	candidate := session.DeclarationCandidate
	if !hostedGenesisAssistantCandidateGuardValid(session, candidate, expectedTurnID, expectedCandidateRevision, expectedCandidateHash) {
		return fmt.Errorf("hosted genesis assistant candidate guard is invalid")
	}
	if !hostedGenesisAssistantProjectionValid(session, conversation, expectedTurnID) {
		return fmt.Errorf("hosted genesis assistant projection is invalid")
	}
	expectedStatus, err := validateHostedGenesisSessionUpdate(session, expectedVersion, expectedStatus)
	if err != nil {
		return err
	}
	if err := conversation.BeforeUpdate(); err != nil {
		return err
	}
	return s.transactHostedGenesisCandidateProjection(ctx, session, expectedVersion, expectedStatus, expectedTurnID,
		expectedCandidateRevision, expectedCandidateHash, candidate.Phase, conversation, true, false)
}

// FinalizeHostedGenesisCandidateAndConversation atomically transitions an
// affirmed typed candidate to declaration_ready and projects its exact
// canonical bytes to the public conversation projection row. No provider output,
// transcript extraction, or time-dependent rendering participates in this
// transaction.
func (s *Store) FinalizeHostedGenesisCandidateAndConversation(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, expectedTurnID string, expectedCandidateRevision int64, expectedCandidateHash string, conversation *models.SoulAgentMintConversation) error {
	if s == nil || s.DB == nil || session == nil || session.DeclarationCandidate == nil || conversation == nil {
		return theoryErrors.ErrItemNotFound
	}
	expectedTurnID = strings.TrimSpace(expectedTurnID)
	expectedCandidateHash = strings.TrimSpace(expectedCandidateHash)
	candidate := session.DeclarationCandidate
	if !hostedGenesisFinalizationCandidateGuardValid(session, candidate, expectedTurnID, expectedCandidateRevision, expectedCandidateHash) {
		return fmt.Errorf("hosted genesis candidate finalization guard is invalid")
	}
	if !hostedGenesisFinalizationCheckpointValid(session, candidate) {
		return fmt.Errorf("hosted genesis candidate finalization checkpoint is invalid")
	}
	if !hostedGenesisFinalizationProjectionValid(session, candidate, conversation, expectedTurnID) {
		return fmt.Errorf("hosted genesis candidate finalization projection is invalid")
	}
	expectedStatus, err := validateHostedGenesisSessionUpdate(session, expectedVersion, expectedStatus)
	if err != nil {
		return err
	}
	if err := conversation.BeforeUpdate(); err != nil {
		return err
	}
	return s.transactHostedGenesisCandidateProjection(ctx, session, expectedVersion, expectedStatus, expectedTurnID,
		expectedCandidateRevision, expectedCandidateHash, hostedgenesis.DeclarationCandidatePhaseAffirmed, conversation, false, true)
}

func hostedGenesisAssistantCandidateGuardValid(session *models.HostedGenesisSession, candidate *hostedgenesis.DeclarationCandidate, expectedTurnID string, expectedRevision int64, expectedHash string) bool {
	return hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusAssistantTurnReady && expectedTurnID != "" &&
		strings.TrimSpace(session.LatestTurnID) == expectedTurnID && expectedRevision == candidate.Revision && expectedHash == candidate.CandidateHash &&
		session.CandidateRevision == candidate.Revision && session.CandidateHash == candidate.CandidateHash && session.CandidatePhase == string(candidate.Phase)
}

func hostedGenesisAssistantProjectionValid(session *models.HostedGenesisSession, conversation *models.SoulAgentMintConversation, expectedTurnID string) bool {
	return strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(conversation.AgentID)) &&
		strings.TrimSpace(session.ConversationID) == strings.TrimSpace(conversation.ConversationID) &&
		strings.TrimSpace(conversation.LatestTurnID) == expectedTurnID && strings.TrimSpace(conversation.Status) == models.SoulMintConversationStatusAssistantTurnReady
}

func hostedGenesisFinalizationCandidateGuardValid(session *models.HostedGenesisSession, candidate *hostedgenesis.DeclarationCandidate, expectedTurnID string, expectedRevision int64, expectedHash string) bool {
	return candidate.Phase == hostedgenesis.DeclarationCandidatePhaseFinalized && session.CandidateRevision == candidate.Revision &&
		session.CandidateHash == candidate.CandidateHash && expectedTurnID != "" && strings.TrimSpace(session.LatestTurnID) == expectedTurnID &&
		expectedRevision == candidate.Revision && expectedHash == candidate.CandidateHash
}

func hostedGenesisFinalizationCheckpointValid(session *models.HostedGenesisSession, candidate *hostedgenesis.DeclarationCandidate) bool {
	return hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusDeclarationReady && session.DeclarationCheckpoint != nil &&
		strings.TrimSpace(session.DeclarationCheckpoint.DeclarationHash) == candidate.CandidateHash
}

func hostedGenesisFinalizationProjectionValid(session *models.HostedGenesisSession, candidate *hostedgenesis.DeclarationCandidate, conversation *models.SoulAgentMintConversation, expectedTurnID string) bool {
	return strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(conversation.AgentID)) &&
		strings.TrimSpace(session.ConversationID) == strings.TrimSpace(conversation.ConversationID) && strings.TrimSpace(conversation.LatestTurnID) == expectedTurnID &&
		strings.TrimSpace(models.DecodeSoulMintConversationBlob(conversation.ProducedDeclarations)) == candidate.CanonicalJSON &&
		strings.TrimSpace(conversation.Status) == models.SoulMintConversationStatusDeclarationReady
}

func (s *Store) transactHostedGenesisCandidateProjection(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, expectedTurnID string, expectedRevision int64, expectedHash string, expectedPhase hostedgenesis.DeclarationCandidatePhase, conversation *models.SoulAgentMintConversation, includeMessages bool, includeDeclarations bool) error {
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		addHostedGenesisSessionUpdate(tx, session, expectedVersion, expectedStatus,
			tabletheory.Condition("LatestTurnID", "=", expectedTurnID),
			tabletheory.Condition("CandidateRevision", "=", expectedRevision),
			tabletheory.Condition("CandidateHash", "=", expectedHash),
			tabletheory.Condition("CandidatePhase", "=", string(expectedPhase)))
		addHostedGenesisConversationProjectionUpdate(tx, conversation, expectedStatus, expectedTurnID, includeMessages, includeDeclarations)
		return nil
	})
}

func addHostedGenesisConversationProjectionUpdate(tx core.TransactionBuilder, conversation *models.SoulAgentMintConversation, expectedStatus hostedgenesis.Status, expectedTurnID string, includeMessages bool, includeDeclarations bool) {
	tx.UpdateWithBuilder(conversation, func(ub core.UpdateBuilder) error {
		if includeMessages {
			ub.Set("Messages", conversation.Messages)
		}
		if includeDeclarations {
			ub.Set("ProducedDeclarations", conversation.ProducedDeclarations)
		}
		ub.Set("Usage", conversation.Usage)
		ub.Set("Status", conversation.Status)
		ub.Set("StatusReason", conversation.StatusReason)
		ub.Set("LatestTurnID", conversation.LatestTurnID)
		ub.Set("RequestID", conversation.RequestID)
		ub.Set("UpdatedAt", conversation.UpdatedAt)
		ub.Set("CompletedAt", conversation.CompletedAt)
		return nil
	}, tabletheory.IfExists(),
		tabletheory.Condition("Status", "=", string(expectedStatus)),
		tabletheory.Condition("LatestTurnID", "=", expectedTurnID))
}

// FailHostedGenesisSessionAndConversation atomically projects a terminal
// hosted-genesis failure into the Host-owned session truth and its bounded
// public conversation-list projection row. When the accepted turn carried an
// idempotency key, the same guarded transaction also closes that exact
// reservation as failed; an exact already-failed reservation is accepted so a
// recovered turn can converge without reopening or replacing its accepted-turn
// identity. No other terminal status is accepted, and the row retains its
// existing seven-day TTL.
// The session's version and current status guard the entire transaction so a
// late valid completion cannot be overwritten by a stale worker failure.
func (s *Store) FailHostedGenesisSessionAndConversation(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error {
	if s == nil || s.DB == nil || session == nil || conversation == nil {
		return theoryErrors.ErrItemNotFound
	}
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed ||
		strings.TrimSpace(conversation.Status) != models.SoulMintConversationStatusFailed {
		return fmt.Errorf("hosted genesis failure transaction requires failed session and conversation states")
	}
	if !strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(conversation.AgentID)) ||
		strings.TrimSpace(session.ConversationID) != strings.TrimSpace(conversation.ConversationID) {
		return fmt.Errorf("hosted genesis failure transaction requires matching session and conversation identity")
	}
	expectedStatus, err := validateHostedGenesisFailureUpdate(session, expectedVersion, expectedStatus)
	if err != nil {
		return err
	}
	if conversationErr := conversation.BeforeUpdate(); conversationErr != nil {
		return conversationErr
	}
	idempotency, err := hostedGenesisFailureIdempotency(session)
	if err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		addHostedGenesisSessionFailureUpdate(tx, session, expectedVersion, expectedStatus,
			tabletheory.Condition("LatestTurnID", "=", strings.TrimSpace(session.LatestTurnID)))
		tx.UpdateWithBuilder(conversation, func(ub core.UpdateBuilder) error {
			ub.Set("Status", conversation.Status)
			ub.Set("StatusReason", conversation.StatusReason)
			ub.Set("LatestTurnID", conversation.LatestTurnID)
			ub.Set("RequestID", conversation.RequestID)
			ub.Set("UpdatedAt", conversation.UpdatedAt)
			ub.Set("CompletedAt", conversation.CompletedAt)
			return nil
		}, tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", string(expectedStatus)),
			tabletheory.Condition("LatestTurnID", "=", strings.TrimSpace(conversation.LatestTurnID)))
		if idempotency != nil {
			tx.UpdateWithBuilder(idempotency, func(ub core.UpdateBuilder) error {
				ub.Set("Status", models.SoulMintConversationIdempotencyStatusFailed)
				ub.Set("RequestID", idempotency.RequestID)
				ub.Set("UpdatedAt", idempotency.UpdatedAt)
				return nil
			}, tabletheory.IfExists(),
				tabletheory.Condition("Status", "IN", []string{
					models.SoulMintConversationIdempotencyStatusProcessing,
					models.SoulMintConversationIdempotencyStatusFailed,
				}),
				tabletheory.Condition("RegistrationID", "=", idempotency.RegistrationID),
				tabletheory.Condition("AgentID", "=", idempotency.AgentID),
				tabletheory.Condition("ConversationID", "=", idempotency.ConversationID),
				tabletheory.Condition("TurnID", "=", idempotency.TurnID),
				tabletheory.Condition("IdempotencyKey", "=", idempotency.IdempotencyKey),
				tabletheory.Condition("RequestHash", "=", idempotency.RequestHash))
		}
		return nil
	})
}

// validateHostedGenesisFailureUpdate validates only the bounded terminal fields
// that FailHostedGenesisSessionAndConversation writes. The candidate and other
// unchanged durable state remain authoritative in DynamoDB and are deliberately
// not reserialized, so a permanently invalid candidate can be terminalized
// without weakening candidate validation on any candidate-mutating path.
func validateHostedGenesisFailureUpdate(item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) (hostedgenesis.Status, error) {
	if item == nil || expectedVersion < 0 {
		return "", fmt.Errorf("expected version must be non-negative")
	}
	expectedStatus = hostedgenesis.NormalizeStatus(string(expectedStatus))
	if !hostedgenesis.IsAllowedStatus(expectedStatus) {
		return "", hostedgenesis.ErrInvalidStatusTransition
	}
	if hostedgenesis.NormalizeStatus(item.Status) != hostedgenesis.StatusFailed || item.Failure == nil {
		return "", fmt.Errorf("hosted genesis failure update requires bounded failed state")
	}
	if err := item.Failure.Validate(); err != nil {
		return "", err
	}
	if err := hostedgenesis.ValidateTransition(expectedStatus, hostedgenesis.StatusFailed); err != nil {
		return "", err
	}
	if strings.TrimSpace(item.InstanceSlug) == "" || strings.TrimSpace(item.RegistrationID) == "" ||
		strings.TrimSpace(item.AgentID) == "" || strings.TrimSpace(item.ConversationID) == "" {
		return "", fmt.Errorf("hosted genesis failure update identity is incomplete")
	}
	if item.PK != models.HostedGenesisSessionPK(item.InstanceSlug) ||
		item.SK != models.HostedGenesisSessionSK(item.ConversationID) {
		return "", fmt.Errorf("hosted genesis failure update keys do not match session identity")
	}
	return expectedStatus, nil
}

func addHostedGenesisSessionFailureUpdate(tx core.TransactionBuilder, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, extraConditions ...core.TransactCondition) {
	conditions := []core.TransactCondition{
		tabletheory.IfExists(),
		tabletheory.AtVersion(expectedVersion),
		tabletheory.Condition("Status", "=", string(expectedStatus)),
	}
	conditions = append(conditions, extraConditions...)
	tx.UpdateWithBuilder(item, func(ub core.UpdateBuilder) error {
		ub.Set("Status", item.Status)
		ub.Set("Failure", item.Failure)
		ub.Set("RequestID", item.RequestID)
		ub.Set("UpdatedAt", item.UpdatedAt)
		ub.Set("CompletedAt", item.CompletedAt)
		ub.Add("Version", int64(1))
		return nil
	}, conditions...)
}

func hostedGenesisFailureIdempotency(session *models.HostedGenesisSession) (*models.SoulMintConversationIdempotency, error) {
	if session == nil || session.TraceIDs == nil || strings.TrimSpace(session.TraceIDs.IdempotencyKey) == "" {
		return nil, nil
	}
	idempotencyKey := strings.TrimSpace(session.TraceIDs.IdempotencyKey)
	latestTurnID := strings.TrimSpace(session.LatestTurnID)
	var accepted hostedgenesis.TurnLedgerEntry
	found := false
	for i := len(session.TurnLedger) - 1; i >= 0; i-- {
		entry := session.TurnLedger[i].Normalize()
		if entry.TurnID == latestTurnID && entry.IdempotencyKey == idempotencyKey {
			accepted = entry
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("hosted genesis failure idempotency binding is absent from the accepted turn ledger")
	}
	item := &models.SoulMintConversationIdempotency{
		InstanceSlug:   session.InstanceSlug,
		RegistrationID: session.RegistrationID,
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		TurnID:         accepted.TurnID,
		IdempotencyKey: accepted.IdempotencyKey,
		RequestHash:    accepted.RequestHash,
		RequestID:      session.RequestID,
		Status:         models.SoulMintConversationIdempotencyStatusFailed,
		UpdatedAt:      session.UpdatedAt.UTC(),
	}
	// UpdateKeys is sufficient here: the existing row's CreatedAt/TTL must not be
	// rewritten by a terminal-status projection. The transaction conditions bind
	// this key-only update to the exact accepted turn/hash.
	if err := item.UpdateKeys(); err != nil {
		return nil, err
	}
	return item, nil
}

// PublishHostedGenesisSessionAndConversation atomically records terminal
// publication truth in HostedGenesisSession and its public conversation
// row. Both writes are guarded by the same declaration_ready expectation, and
// the authoritative session additionally uses TableTheory's version guard.
func (s *Store) PublishHostedGenesisSessionAndConversation(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error {
	if s == nil || s.DB == nil || session == nil || conversation == nil {
		return theoryErrors.ErrItemNotFound
	}
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusPublished ||
		strings.TrimSpace(conversation.Status) != models.SoulMintConversationStatusPublished {
		return fmt.Errorf("hosted genesis publication transaction requires published session and conversation states")
	}
	if !strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(conversation.AgentID)) ||
		strings.TrimSpace(session.ConversationID) != strings.TrimSpace(conversation.ConversationID) {
		return fmt.Errorf("hosted genesis publication transaction requires matching session and conversation identity")
	}
	expectedStatus, err := validateHostedGenesisSessionUpdate(session, expectedVersion, expectedStatus)
	if err != nil {
		return err
	}
	if err := conversation.BeforeUpdate(); err != nil {
		return err
	}
	return s.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		addHostedGenesisSessionUpdate(tx, session, expectedVersion, expectedStatus)
		tx.UpdateWithBuilder(conversation, func(ub core.UpdateBuilder) error {
			ub.Set("Status", conversation.Status)
			ub.Set("StatusReason", conversation.StatusReason)
			ub.Set("RequestID", conversation.RequestID)
			ub.Set("UpdatedAt", conversation.UpdatedAt)
			ub.Set("CompletedAt", conversation.CompletedAt)
			return nil
		}, tabletheory.IfExists(), tabletheory.Condition("Status", "=", string(expectedStatus)))
		return nil
	})
}

func validateHostedGenesisSessionUpdate(item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) (hostedgenesis.Status, error) {
	if expectedVersion < 0 {
		return "", fmt.Errorf("expected version must be non-negative")
	}
	expectedStatus = hostedgenesis.NormalizeStatus(string(expectedStatus))
	if !hostedgenesis.IsAllowedStatus(expectedStatus) {
		return "", hostedgenesis.ErrInvalidStatusTransition
	}
	if err := item.BeforeUpdate(); err != nil {
		return "", err
	}
	if err := hostedgenesis.ValidateTransition(expectedStatus, hostedgenesis.Status(item.Status)); err != nil {
		return "", err
	}
	return expectedStatus, nil
}

func addHostedGenesisSessionUpdate(tx core.TransactionBuilder, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, extraConditions ...core.TransactCondition) {
	conditions := []core.TransactCondition{
		tabletheory.IfExists(),
		tabletheory.AtVersion(expectedVersion),
		tabletheory.Condition("Status", "=", string(expectedStatus)),
	}
	conditions = append(conditions, extraConditions...)
	tx.UpdateWithBuilder(item, func(ub core.UpdateBuilder) error {
		ub.Set("Status", item.Status)
		ub.Set("Model", item.Model)
		ub.Set("LatestTurnID", item.LatestTurnID)
		ub.Set("MessageCount", item.MessageCount)
		ub.Set("TurnLedger", item.TurnLedger)
		ub.Set("InputCheckpointRef", item.InputCheckpointRef)
		ub.Set("AssistantCheckpointRef", item.AssistantCheckpointRef)
		ub.Set("ExecutionStateRef", item.ExecutionStateRef)
		ub.Set("MicroVMExecutionID", item.MicroVMExecutionID)
		ub.Set("MicroVMLifecycleRef", item.MicroVMLifecycleRef)
		ub.Set("DeclarationCheckpoint", item.DeclarationCheckpoint)
		ub.Set("DeclarationCandidate", item.DeclarationCandidate)
		ub.Set("CandidateRevision", item.CandidateRevision)
		ub.Set("CandidateHash", item.CandidateHash)
		ub.Set("CandidatePhase", item.CandidatePhase)
		ub.Set("Publication", item.Publication)
		setOrRemoveHostedGenesisFailure(ub, item.Failure)
		ub.Set("TraceIDs", item.TraceIDs)
		ub.Set("VMCheckpoint", item.VMCheckpoint)
		ub.Set("RequestID", item.RequestID)
		ub.Set("UpdatedAt", item.UpdatedAt)
		ub.Set("CompletedAt", item.CompletedAt)
		ub.Add("Version", int64(1))
		return nil
	}, conditions...)
}
