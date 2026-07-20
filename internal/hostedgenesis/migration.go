package hostedgenesis

import (
	"fmt"
	"strings"
	"time"
)

const (
	LegacyStatusCompleted = "completed"
)

// LegacySessionInput is the safe, reduced view of an existing SOUL_REG /
// MINT_CONVERSATION record used to plan a HostedGenesisSession migration. It
// intentionally excludes raw transcript, prompt, and message-list fields.
type LegacySessionInput struct {
	InstanceSlug   string
	RegistrationID string
	AgentID        string
	ConversationID string
	Status         string
	StatusReason   string
	LatestTurnID   string
	MessageCount   int
	Model          string
	RequestID      string
	CorrelationID  string
	IdempotencyKey string
	RequestHash    string

	ProducedDeclarationsPresent bool
	ProducedDeclarationsValid   bool
	DeclarationID               string
	DeclarationHash             string
	DeclarationCheckpointRef    string

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt time.Time
}

// SessionSeed is a store-agnostic seed for a HostedGenesisSession row. Store
// adapters turn this into the TableTheory model while preserving the Host-owned
// durable contract.
type SessionSeed struct {
	InstanceSlug          string
	RegistrationID        string
	AgentID               string
	ConversationID        string
	Status                Status
	Model                 string
	LatestTurnID          string
	MessageCount          int
	TurnLedger            []TurnLedgerEntry
	DeclarationCheckpoint *DeclarationCheckpoint
	Failure               *Failure
	TraceIDs              *TraceIDs
	RequestID             string
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CompletedAt           time.Time
}

// MigrationPlan is a deterministic dry-run plan for backfilling one legacy
// hosted genesis conversation into the HostedGenesisSession source of truth.
type MigrationPlan struct {
	Seed           SessionSeed
	RecoveryAction RecoveryAction
	Notes          []string
}

// PlanLegacySessionMigration maps a reduced legacy conversation view to a
// HostedGenesisSession seed. Ambiguous in-flight legacy rows are mapped to a
// typed failed recovery state so user-visible recovery is deterministic and not
// inferred from SQS or transport state.
func PlanLegacySessionMigration(input LegacySessionInput) (MigrationPlan, error) {
	input = normalizeLegacySessionInput(input)
	if input.InstanceSlug == "" || input.RegistrationID == "" || input.AgentID == "" || input.ConversationID == "" {
		return MigrationPlan{}, fmt.Errorf("%w: missing legacy identity", ErrInvalidTurnLedger)
	}
	now := firstNonZeroTime(input.UpdatedAt, input.CompletedAt, input.CreatedAt, time.Now().UTC())
	seed := SessionSeed{
		InstanceSlug:   input.InstanceSlug,
		RegistrationID: input.RegistrationID,
		AgentID:        input.AgentID,
		ConversationID: input.ConversationID,
		LatestTurnID:   input.LatestTurnID,
		MessageCount:   input.MessageCount,
		Model:          input.Model,
		RequestID:      input.RequestID,
		TraceIDs: &TraceIDs{
			HostRequestID:  input.RequestID,
			CorrelationID:  input.CorrelationID,
			IdempotencyKey: input.IdempotencyKey,
		},
		CreatedAt:   firstNonZeroTime(input.CreatedAt, now),
		UpdatedAt:   now,
		CompletedAt: input.CompletedAt,
	}
	if seed.MessageCount <= 0 && seed.LatestTurnID != "" {
		seed.MessageCount = 1
	}
	if seed.LatestTurnID != "" {
		entry := TurnLedgerEntry{
			TurnID:         seed.LatestTurnID,
			IdempotencyKey: input.IdempotencyKey,
			RequestHash:    input.RequestHash,
			BillingLedgerRef: firstNonEmpty(
				"legacy-mint-conversation:"+input.ConversationID,
				input.RequestID,
			),
			MessageCount: seed.MessageCount,
			AcceptedAt:   firstNonZeroTime(input.CreatedAt, now),
		}
		if entry.IdempotencyKey == "" {
			entry.RequestHash = ""
		}
		if err := entry.Validate(); err == nil {
			seed.TurnLedger = []TurnLedgerEntry{entry}
		}
	}

	plan := classifyLegacySession(seed, input)
	plan.Notes = append(plan.Notes, "legacy state mapped to deterministic recovery; no SQS state imported")
	return plan, nil
}

func classifyLegacySession(seed SessionSeed, input LegacySessionInput) MigrationPlan {
	status := NormalizeStatus(input.Status)
	if status == StatusDeclarationReady || strings.EqualFold(input.Status, LegacyStatusCompleted) {
		return planLegacyTerminalSession(seed, input)
	}
	if status == StatusCreated || status == StatusInProgress || status == StatusAssistantTurnReady {
		return failMigration(seed, FailureCodeAssistantTurnFailed, RecoveryActionRetrySameStep)
	}
	if status == StatusDeclarationExtractionPending {
		return failMigration(seed, FailureCodeDeclarationExtractionFailed, RecoveryActionRetrySameStep)
	}
	if status == StatusFailed {
		code := failureCodeFromLegacyReason(input.StatusReason)
		return failMigration(seed, code, recoveryActionForFailure(code))
	}
	return failMigration(seed, FailureCodeInvalidCompletionState, RecoveryActionRefreshState)
}

// planLegacyTerminalSession maps every legacy terminal conversation to a
// deterministic restart_soul_bootstrap recovery. A pre-five-body declaration
// cannot authorize declaration_ready: the declaration checkpoint requires the
// exact five-body contract versions, which no legacy conversation carries, so
// there is no migration path that seeds declaration_ready.
func planLegacyTerminalSession(seed SessionSeed, input LegacySessionInput) MigrationPlan {
	if input.ProducedDeclarationsPresent || input.ProducedDeclarationsValid {
		return failMigration(seed, FailureCodeInvalidProducedDeclarations, RecoveryActionRestartSoulBootstrap)
	}
	return failMigration(seed, FailureCodeMissingProducedDeclarations, RecoveryActionRestartSoulBootstrap)
}

func failMigration(seed SessionSeed, code FailureCode, action RecoveryAction) MigrationPlan {
	seed.Status = StatusFailed
	seed.CompletedAt = firstNonZeroTime(seed.CompletedAt, seed.UpdatedAt, time.Now().UTC())
	seed.Failure = &Failure{
		Code:      code,
		Message:   failureMessage(code),
		Retryable: action == RecoveryActionRetrySameStep || action == RecoveryActionRefreshState,
		Recovery: Recovery{
			Action:            action,
			MaxAttempts:       maxAttemptsForRecovery(action),
			RetryAfterSeconds: retryAfterForRecovery(action),
			Reason:            string(code),
		},
	}
	return MigrationPlan{Seed: seed, RecoveryAction: action}
}

func normalizeLegacySessionInput(input LegacySessionInput) LegacySessionInput {
	input.InstanceSlug = strings.ToLower(strings.TrimSpace(input.InstanceSlug))
	input.RegistrationID = strings.TrimSpace(input.RegistrationID)
	input.AgentID = strings.ToLower(strings.TrimSpace(input.AgentID))
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	input.StatusReason = strings.TrimSpace(input.StatusReason)
	input.LatestTurnID = strings.TrimSpace(input.LatestTurnID)
	input.Model = strings.TrimSpace(input.Model)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.RequestHash = strings.ToLower(strings.TrimSpace(input.RequestHash))
	input.DeclarationID = strings.TrimSpace(input.DeclarationID)
	input.DeclarationHash = strings.ToLower(strings.TrimSpace(input.DeclarationHash))
	input.DeclarationCheckpointRef = strings.TrimSpace(input.DeclarationCheckpointRef)
	return input
}

func failureCodeFromLegacyReason(reason string) FailureCode {
	switch FailureCode(strings.TrimSpace(reason)) {
	case FailureCodeLLMUnavailable,
		FailureCodeAssistantTurnFailed,
		FailureCodeDeclarationExtractionFailed,
		FailureCodeInvalidCompletionState,
		FailureCodeMissingProducedDeclarations,
		FailureCodeInvalidProducedDeclarations,
		FailureCodeTenantBoundaryViolation,
		FailureCodeOperatorActionRequired:
		return FailureCode(strings.TrimSpace(reason))
	default:
		return FailureCodeInvalidCompletionState
	}
}

func recoveryActionForFailure(code FailureCode) RecoveryAction {
	switch code {
	case FailureCodeMissingProducedDeclarations, FailureCodeInvalidProducedDeclarations:
		return RecoveryActionRestartSoulBootstrap
	case FailureCodeTenantBoundaryViolation, FailureCodeOperatorActionRequired:
		return RecoveryActionOperatorAction
	case FailureCodeLLMUnavailable, FailureCodeAssistantTurnFailed, FailureCodeDeclarationExtractionFailed:
		return RecoveryActionRetrySameStep
	default:
		return RecoveryActionRefreshState
	}
}

func failureMessage(code FailureCode) string {
	switch code {
	case FailureCodeLLMUnavailable:
		return "Assistant provider was unavailable."
	case FailureCodeAssistantTurnFailed:
		return "Assistant turn did not reach declaration extraction."
	case FailureCodeDeclarationExtractionFailed:
		return "Declaration extraction did not complete."
	case FailureCodeMissingProducedDeclarations:
		return "Produced declarations are missing."
	case FailureCodeInvalidProducedDeclarations:
		return "Produced declarations are invalid."
	case FailureCodeTenantBoundaryViolation:
		return "Conversation failed instance boundary validation."
	case FailureCodeOperatorActionRequired:
		return "Operator action is required."
	default:
		return "Conversation cannot be completed from the migrated state."
	}
}

func maxAttemptsForRecovery(action RecoveryAction) int {
	if action == RecoveryActionRetrySameStep {
		return 3
	}
	return 0
}

func retryAfterForRecovery(action RecoveryAction) int {
	if action == RecoveryActionRetrySameStep {
		return 30
	}
	return 0
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
