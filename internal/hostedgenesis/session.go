package hostedgenesis

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is the durable Host-owned state for a hosted genesis session. Transport
// status (HTTP/SSE/MicroVM lifecycle) must never be treated as this state.
type Status string

const (
	StatusCreated                      Status = "created"
	StatusInProgress                   Status = "in_progress"
	StatusAssistantTurnReady           Status = "assistant_turn_ready"
	StatusDeclarationExtractionPending Status = "declaration_extraction_pending"
	StatusDeclarationReady             Status = "declaration_ready"
	StatusFailed                       Status = "failed"
)

var (
	ErrInvalidStatusTransition = errors.New("invalid hosted genesis status transition")
	ErrInvalidDeclarationGate  = errors.New("hosted genesis declaration checkpoint is not publish-ready")
	ErrInvalidFailureRecovery  = errors.New("hosted genesis failure recovery is invalid")
)

// AllowedStatuses returns the durable status enum in contract order.
func AllowedStatuses() []Status {
	return []Status{
		StatusCreated,
		StatusInProgress,
		StatusAssistantTurnReady,
		StatusDeclarationExtractionPending,
		StatusDeclarationReady,
		StatusFailed,
	}
}

// NormalizeStatus trims and lowercases a status value for storage/comparison.
func NormalizeStatus(status string) Status {
	return Status(strings.ToLower(strings.TrimSpace(status)))
}

// IsAllowedStatus reports whether status is one of the locked durable states.
func IsAllowedStatus(status Status) bool {
	for _, allowed := range AllowedStatuses() {
		if status == allowed {
			return true
		}
	}
	return false
}

// ValidateTransition enforces the Host-owned state machine foundation. It is
// intentionally stricter than transport lifecycles: MicroVM run/get/suspend/resume/terminate
// events do not advance user-visible state unless Host writes one of these
// legal transitions to DynamoDB.
func ValidateTransition(from Status, to Status) error {
	from = NormalizeStatus(string(from))
	to = NormalizeStatus(string(to))
	if !IsAllowedStatus(from) || !IsAllowedStatus(to) {
		return fmt.Errorf("%w: unknown status %q -> %q", ErrInvalidStatusTransition, from, to)
	}
	if from == to {
		return nil
	}
	if from == StatusDeclarationReady || from == StatusFailed {
		return fmt.Errorf("%w: terminal status %q cannot transition to %q", ErrInvalidStatusTransition, from, to)
	}
	if to == StatusFailed {
		return nil
	}

	legal := map[Status][]Status{
		StatusCreated: {
			StatusInProgress,
		},
		StatusInProgress: {
			StatusAssistantTurnReady,
			StatusDeclarationExtractionPending,
			StatusDeclarationReady,
		},
		StatusAssistantTurnReady: {
			StatusInProgress,
			StatusDeclarationExtractionPending,
			StatusDeclarationReady,
		},
		StatusDeclarationExtractionPending: {
			StatusDeclarationReady,
		},
	}
	for _, next := range legal[from] {
		if to == next {
			return nil
		}
	}
	return fmt.Errorf("%w: %q -> %q", ErrInvalidStatusTransition, from, to)
}

// InstanceKeyReadStatus collapses pre-turn created records to in_progress for
// the Lesser instance-key route family. Lesser persists the durable conversation
// id and resumes/polls; it does not wait on a client-visible created state.
func InstanceKeyReadStatus(status Status) Status {
	if NormalizeStatus(string(status)) == StatusCreated {
		return StatusInProgress
	}
	return NormalizeStatus(string(status))
}

// TraceIDs are client-safe correlation ids. They must never contain bearer
// tokens, Instance API keys, wallet signatures, provider secrets, or MicroVM
// endpoint credentials.
type TraceIDs struct {
	HostRequestID   string `json:"host_request_id,omitempty"`
	CorrelationID   string `json:"correlation_id,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
	LesserRequestID string `json:"lesser_request_id,omitempty"`
}

// DeclarationCheckpoint is the durable publish-readiness evidence stored on a
// HostedGenesisSession. It carries ids, hashes, and checkpoint references only;
// raw transcripts and provider credentials are outside the Host truth record.
type DeclarationCheckpoint struct {
	DeclarationID   string    `json:"declaration_id"`
	DeclarationHash string    `json:"declaration_hash"`
	CheckpointRef   string    `json:"checkpoint_ref"`
	ProducedAt      time.Time `json:"produced_at"`
	RegistrationID  string    `json:"registration_id"`
	ConversationID  string    `json:"conversation_id"`
	AgentID         string    `json:"agent_id"`
	MessageCount    int       `json:"message_count"`
	Model           string    `json:"model,omitempty"`
	SchemaVersion   string    `json:"schema_version,omitempty"`
	GuidanceVersion string    `json:"guidance_version,omitempty"`
	RequestID       string    `json:"request_id"`
}

// Validate fails closed unless the checkpoint can authorize declaration_ready
// publish/finalize decisions without consulting transport state.
func (c DeclarationCheckpoint) Validate() error {
	if strings.TrimSpace(c.DeclarationID) == "" ||
		strings.TrimSpace(c.CheckpointRef) == "" ||
		strings.TrimSpace(c.RegistrationID) == "" ||
		strings.TrimSpace(c.ConversationID) == "" ||
		strings.TrimSpace(c.AgentID) == "" ||
		strings.TrimSpace(c.RequestID) == "" ||
		c.MessageCount <= 0 || c.ProducedAt.IsZero() {
		return ErrInvalidDeclarationGate
	}
	if !isSHA256Digest(c.DeclarationHash) {
		return ErrInvalidDeclarationGate
	}
	if !declarationCheckpointVersionsValid(c.SchemaVersion, c.GuidanceVersion) {
		return ErrInvalidDeclarationGate
	}
	return nil
}

func declarationCheckpointVersionsValid(schemaVersion string, guidanceVersion string) bool {
	schemaVersion = strings.TrimSpace(schemaVersion)
	guidanceVersion = strings.TrimSpace(guidanceVersion)
	if schemaVersion == "" && guidanceVersion == "" {
		return true
	}
	contract := DeclarationContractFromVersions(schemaVersion, guidanceVersion).Normalize()
	return strings.EqualFold(schemaVersion, contract.SchemaVersion) &&
		strings.EqualFold(guidanceVersion, contract.GuidanceVersion)
}

func isSHA256Digest(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// RecoveryAction is server-authored bounded recovery guidance for a failed
// hosted genesis session. Clients choose between these actions; they do not
// author arbitrary recovery instructions.
type RecoveryAction string

const (
	RecoveryActionRefreshState         RecoveryAction = "refresh_state"
	RecoveryActionRetrySameStep        RecoveryAction = "retry_same_step"
	RecoveryActionRestartSoulBootstrap RecoveryAction = "restart_soul_bootstrap"
	RecoveryActionOperatorAction       RecoveryAction = "operator_action"
)

// FailureCode names the bounded failure class persisted in Host state.
type FailureCode string

const (
	FailureCodeLLMUnavailable              FailureCode = "llm_unavailable"
	FailureCodeAssistantTurnFailed         FailureCode = "assistant_turn_failed"
	FailureCodeDeclarationExtractionFailed FailureCode = "declaration_extraction_failed"
	FailureCodeInvalidCompletionState      FailureCode = "invalid_completion_state"
	FailureCodeMissingProducedDeclarations FailureCode = "missing_produced_declarations"
	FailureCodeInvalidProducedDeclarations FailureCode = "invalid_produced_declarations"
	FailureCodeTenantBoundaryViolation     FailureCode = "tenant_boundary_violation"
	FailureCodeOperatorActionRequired      FailureCode = "operator_action_required"
	FailureCodeMicroVMUnavailable          FailureCode = "microvm_unavailable"
)

// Recovery is the typed recovery envelope exposed on failed compact projections.
type Recovery struct {
	Action            RecoveryAction `json:"action"`
	MaxAttempts       int            `json:"max_attempts,omitempty"`
	RetryAfterSeconds int            `json:"retry_after_seconds,omitempty"`
	Reason            string         `json:"reason,omitempty"`
}

// Failure is the durable failed-state evidence for HostedGenesisSession.
type Failure struct {
	Code      FailureCode `json:"code"`
	Message   string      `json:"message"`
	Retryable bool        `json:"retryable"`
	Recovery  Recovery    `json:"recovery"`
}

// FailureMessage returns the fixed public message for a failure code. Provider
// errors and declaration payloads must never replace these messages.
func FailureMessage(code FailureCode) string {
	switch code {
	case FailureCodeLLMUnavailable:
		return "Assistant turn failed before declaration extraction."
	case FailureCodeAssistantTurnFailed:
		return "Assistant turn failed before declaration extraction."
	case FailureCodeDeclarationExtractionFailed:
		return "Declaration extraction failed."
	case FailureCodeMissingProducedDeclarations:
		return "Produced declarations are missing."
	case FailureCodeInvalidProducedDeclarations:
		return "Produced declarations are invalid."
	case FailureCodeTenantBoundaryViolation:
		return "Conversation failed instance boundary validation."
	case FailureCodeOperatorActionRequired:
		return "Operator action is required."
	case FailureCodeMicroVMUnavailable:
		return "MicroVM execution dispatch is unavailable."
	default:
		return "Conversation cannot be completed from the current state."
	}
}

// SanitizeFailureReason keeps only the bounded declaration field code when it
// is useful to the caller. All other arbitrary reasons collapse to the typed
// failure code, preventing provider errors, transcripts, and private
// declarations from entering durable status or API projections.
func SanitizeFailureReason(code FailureCode, reason string) string {
	reason = strings.TrimSpace(reason)
	if code == FailureCodeInvalidProducedDeclarations && IsDeclarationValidationCode(reason) {
		return reason
	}
	return string(code)
}

// Validate fails closed unless failure recovery is server-authored and bounded.
func (f Failure) Validate() error {
	if !isAllowedFailureCode(f.Code) || strings.TrimSpace(f.Message) == "" {
		return ErrInvalidFailureRecovery
	}
	if !isAllowedRecoveryAction(f.Recovery.Action) {
		return ErrInvalidFailureRecovery
	}
	if f.Recovery.MaxAttempts < 0 || f.Recovery.MaxAttempts > 10 {
		return ErrInvalidFailureRecovery
	}
	if f.Recovery.RetryAfterSeconds < 0 || f.Recovery.RetryAfterSeconds > 3600 {
		return ErrInvalidFailureRecovery
	}
	return nil
}

func isAllowedFailureCode(code FailureCode) bool {
	switch code {
	case FailureCodeLLMUnavailable,
		FailureCodeAssistantTurnFailed,
		FailureCodeDeclarationExtractionFailed,
		FailureCodeInvalidCompletionState,
		FailureCodeMissingProducedDeclarations,
		FailureCodeInvalidProducedDeclarations,
		FailureCodeTenantBoundaryViolation,
		FailureCodeOperatorActionRequired,
		FailureCodeMicroVMUnavailable:
		return true
	default:
		return false
	}
}

func isAllowedRecoveryAction(action RecoveryAction) bool {
	switch action {
	case RecoveryActionRefreshState,
		RecoveryActionRetrySameStep,
		RecoveryActionRestartSoulBootstrap,
		RecoveryActionOperatorAction:
		return true
	default:
		return false
	}
}

// CanFinalize enforces the declaration_ready gate used before publish/finalize.
func CanFinalize(status Status, checkpoint *DeclarationCheckpoint) error {
	if checkpoint == nil {
		return ErrInvalidDeclarationGate
	}
	if err := CanPublish(PublishGateInput{
		Status:                status,
		RegistrationID:        checkpoint.RegistrationID,
		ConversationID:        checkpoint.ConversationID,
		AgentID:               checkpoint.AgentID,
		DeclarationCheckpoint: checkpoint,
	}); err != nil {
		return ErrInvalidDeclarationGate
	}
	return nil
}

// ConversationProjection is the compact session view safe for Host/Lesser
// contracts and tests. It intentionally has no raw transcript, message-list,
// credential, token, signature, or provider-secret fields.
type ConversationProjection struct {
	RegistrationID        string                 `json:"registration_id"`
	ConversationID        string                 `json:"conversation_id"`
	AgentID               string                 `json:"agent_id"`
	Status                Status                 `json:"status"`
	LatestTurnID          string                 `json:"latest_turn_id,omitempty"`
	MessageCount          int                    `json:"message_count"`
	DeclarationCheckpoint *DeclarationCheckpoint `json:"declaration_checkpoint,omitempty"`
	Failure               *Failure               `json:"failure,omitempty"`
	RequestID             string                 `json:"request_id"`
	TraceIDs              *TraceIDs              `json:"trace_ids,omitempty"`
	PollAfterSeconds      int                    `json:"poll_after_seconds,omitempty"`
	CreatedAt             time.Time              `json:"created_at,omitempty"`
	UpdatedAt             time.Time              `json:"updated_at,omitempty"`
	CompletedAt           time.Time              `json:"completed_at,omitempty"`
}

// ProjectionInput contains durable HostedGenesisSession fields needed to build
// a compact client-safe projection without depending on a store package type.
type ProjectionInput struct {
	RegistrationID        string
	ConversationID        string
	AgentID               string
	Status                Status
	LatestTurnID          string
	MessageCount          int
	DeclarationCheckpoint *DeclarationCheckpoint
	Failure               *Failure
	RequestID             string
	TraceIDs              *TraceIDs
	PollAfterSeconds      int
	CreatedAt             time.Time
	UpdatedAt             time.Time
	CompletedAt           time.Time
}

// NewConversationProjection builds a compact projection and applies Lesser
// instance-key status semantics when requested.
func NewConversationProjection(input ProjectionInput, collapseCreatedForInstanceKey bool) (ConversationProjection, error) {
	status := NormalizeStatus(string(input.Status))
	if !IsAllowedStatus(status) {
		return ConversationProjection{}, fmt.Errorf("unknown hosted genesis status %q", input.Status)
	}
	if collapseCreatedForInstanceKey {
		status = InstanceKeyReadStatus(status)
	}
	if status == StatusDeclarationReady {
		if err := CanFinalize(status, input.DeclarationCheckpoint); err != nil {
			return ConversationProjection{}, err
		}
	}
	if status == StatusFailed {
		if input.Failure == nil {
			return ConversationProjection{}, ErrInvalidFailureRecovery
		}
		if err := input.Failure.Validate(); err != nil {
			return ConversationProjection{}, err
		}
	}
	return ConversationProjection{
		RegistrationID:        strings.TrimSpace(input.RegistrationID),
		ConversationID:        strings.TrimSpace(input.ConversationID),
		AgentID:               strings.TrimSpace(input.AgentID),
		Status:                status,
		LatestTurnID:          strings.TrimSpace(input.LatestTurnID),
		MessageCount:          input.MessageCount,
		DeclarationCheckpoint: input.DeclarationCheckpoint,
		Failure:               input.Failure,
		RequestID:             strings.TrimSpace(input.RequestID),
		TraceIDs:              input.TraceIDs,
		PollAfterSeconds:      input.PollAfterSeconds,
		CreatedAt:             input.CreatedAt,
		UpdatedAt:             input.UpdatedAt,
		CompletedAt:           input.CompletedAt,
	}, nil
}
