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
	StatusCreated            Status = "created"
	StatusInProgress         Status = "in_progress"
	StatusAssistantTurnReady Status = "assistant_turn_ready"
	StatusDeclarationReady   Status = "declaration_ready"
	StatusPublished          Status = "published"
	StatusFailed             Status = "failed"
)

var (
	ErrInvalidStatusTransition      = errors.New("invalid hosted genesis status transition")
	ErrInvalidDeclarationGate       = errors.New("hosted genesis declaration checkpoint is not publish-ready")
	ErrInvalidPublicationCheckpoint = errors.New("hosted genesis publication checkpoint is invalid")
	ErrInvalidFailureRecovery       = errors.New("hosted genesis failure recovery is invalid")
)

// AllowedStatuses returns the durable status enum in contract order.
func AllowedStatuses() []Status {
	return []Status{
		StatusCreated,
		StatusInProgress,
		StatusAssistantTurnReady,
		StatusDeclarationReady,
		StatusPublished,
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
	if from == StatusPublished || from == StatusFailed {
		return fmt.Errorf("%w: terminal status %q cannot transition to %q", ErrInvalidStatusTransition, from, to)
	}
	if from == StatusDeclarationReady && to != StatusPublished {
		return fmt.Errorf("%w: publish-ready status %q cannot transition to %q", ErrInvalidStatusTransition, from, to)
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
			StatusDeclarationReady,
		},
		StatusAssistantTurnReady: {
			StatusInProgress,
			StatusDeclarationReady,
		},
		StatusDeclarationReady: {
			StatusPublished,
		},
	}
	for _, next := range legal[from] {
		if to == next {
			return nil
		}
	}
	return fmt.Errorf("%w: %q -> %q", ErrInvalidStatusTransition, from, to)
}

// PublicationCheckpoint is the bounded durable bridge between a declaration
// checkpoint and the exact registration publication it produced. It contains
// only tenant-bound identifiers, a digest, version, and timestamps; publication
// payloads and signing material never enter HostedGenesisSession.
type PublicationCheckpoint struct {
	_ struct{} `theorydb:"naming:snake_case"`

	RegistrationID       string    `theorydb:"attr:registration_id,omitempty" json:"registration_id"`
	ConversationID       string    `theorydb:"attr:conversation_id,omitempty" json:"conversation_id"`
	AgentID              string    `theorydb:"attr:agent_id,omitempty" json:"agent_id"`
	Version              int       `theorydb:"attr:version,omitempty" json:"version"`
	RegistrationSHA256   string    `theorydb:"attr:registration_sha256,omitempty" json:"registration_sha256"`
	RegistrationIssuedAt time.Time `theorydb:"attr:registration_issued_at,omitempty" json:"registration_issued_at"`
	PublishedAt          time.Time `json:"published_at,omitempty"`
}

// ValidatePrepared fails closed unless the reserved publication is bound to
// the authoritative session and can be replayed without changing content.
func (p PublicationCheckpoint) ValidatePrepared(registrationID string, conversationID string, agentID string) error {
	if strings.TrimSpace(p.RegistrationID) == "" ||
		strings.TrimSpace(p.RegistrationID) != strings.TrimSpace(registrationID) ||
		strings.TrimSpace(p.ConversationID) == "" ||
		strings.TrimSpace(p.ConversationID) != strings.TrimSpace(conversationID) ||
		strings.TrimSpace(p.AgentID) == "" ||
		!strings.EqualFold(strings.TrimSpace(p.AgentID), strings.TrimSpace(agentID)) ||
		p.Version <= 0 || p.RegistrationIssuedAt.IsZero() || !isSHA256HexDigest(p.RegistrationSHA256) {
		return ErrInvalidPublicationCheckpoint
	}
	return nil
}

// ValidatePublished additionally requires the durable publication timestamp.
func (p PublicationCheckpoint) ValidatePublished(registrationID string, conversationID string, agentID string) error {
	if err := p.ValidatePrepared(registrationID, conversationID, agentID); err != nil || p.PublishedAt.IsZero() {
		return ErrInvalidPublicationCheckpoint
	}
	return nil
}

func isSHA256HexDigest(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
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
	_ struct{} `theorydb:"naming:snake_case"`

	DeclarationID   string    `theorydb:"attr:declaration_id,omitempty" json:"declaration_id"`
	DeclarationHash string    `theorydb:"attr:declaration_hash,omitempty" json:"declaration_hash"`
	CheckpointRef   string    `theorydb:"attr:checkpoint_ref,omitempty" json:"checkpoint_ref"`
	ProducedAt      time.Time `theorydb:"attr:produced_at,omitempty" json:"produced_at"`
	RegistrationID  string    `theorydb:"attr:registration_id,omitempty" json:"registration_id"`
	ConversationID  string    `theorydb:"attr:conversation_id,omitempty" json:"conversation_id"`
	AgentID         string    `theorydb:"attr:agent_id,omitempty" json:"agent_id"`
	MessageCount    int       `theorydb:"attr:message_count,omitempty" json:"message_count"`
	Model           string    `json:"model,omitempty"`
	SchemaVersion   string    `json:"schema_version,omitempty"`
	GuidanceVersion string    `json:"guidance_version,omitempty"`
	RequestID       string    `theorydb:"attr:request_id,omitempty" json:"request_id"`
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

// declarationCheckpointVersionsValid requires the checkpoint to carry the
// exact canonical five-body contract versions. Missing or unknown versions
// fail closed: a checkpoint without an explicit five-body contract cannot
// authorize declaration_ready.
func declarationCheckpointVersionsValid(schemaVersion string, guidanceVersion string) bool {
	contract, err := ParseFiveBodyDeclarationContract(schemaVersion, guidanceVersion)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(schemaVersion), contract.SchemaVersion) &&
		strings.EqualFold(strings.TrimSpace(guidanceVersion), contract.GuidanceVersion)
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
	FailureCodeInvalidCompletionState      FailureCode = "invalid_completion_state"
	FailureCodeMissingProducedDeclarations FailureCode = "missing_produced_declarations"
	FailureCodeInvalidProducedDeclarations FailureCode = "invalid_produced_declarations"
	FailureCodeTenantBoundaryViolation     FailureCode = "tenant_boundary_violation"
	FailureCodeOperatorActionRequired      FailureCode = "operator_action_required"
	FailureCodeMicroVMUnavailable          FailureCode = "microvm_unavailable"
)

// FailureClass is a content-free, bounded explanation of where a provider-backed
// declaration phase failed. It is deliberately separate from FailureCode:
// the code drives recovery while the class lets operators distinguish timeout,
// provider API, provider-output, parse/validation, and persistence boundaries
// without ever persisting SDK or store error text, prompts, transcripts, tool
// arguments, or output.
type FailureClass string

const (
	FailureClassProviderTimeout       FailureClass = "provider_timeout"
	FailureClassProviderCanceled      FailureClass = "provider_canceled"
	FailureClassProviderAPIFailure    FailureClass = "provider_api_failure"
	FailureClassInvalidProviderOutput FailureClass = "invalid_provider_output"
	FailureClassParseValidation       FailureClass = "parse_validation_failure"
	FailureClassProviderEvidenceStore FailureClass = "provider_evidence_persistence_failure"
	FailureClassAssistantTurnStore    FailureClass = "assistant_turn_persistence_failure"
)

// Recovery is the typed recovery envelope exposed on failed compact projections.
type Recovery struct {
	_ struct{} `theorydb:"naming:snake_case"`

	Action            RecoveryAction `theorydb:"attr:action,omitempty" json:"action"`
	MaxAttempts       int            `json:"max_attempts,omitempty"`
	RetryAfterSeconds int            `json:"retry_after_seconds,omitempty"`
	Reason            string         `json:"reason,omitempty"`
}

// Failure is the durable failed-state evidence for HostedGenesisSession.
type Failure struct {
	_ struct{} `theorydb:"naming:snake_case"`

	Code      FailureCode  `theorydb:"attr:code,omitempty" json:"code"`
	Class     FailureClass `json:"class,omitempty"`
	Message   string       `theorydb:"attr:message,omitempty" json:"message"`
	Retryable bool         `theorydb:"attr:retryable,omitempty" json:"retryable"`
	Recovery  Recovery     `theorydb:"attr:recovery,omitempty" json:"recovery"`
}

// FailureMessage returns the fixed public message for a failure code. Provider
// errors and declaration payloads must never replace these messages.
func FailureMessage(code FailureCode) string {
	switch code {
	case FailureCodeLLMUnavailable:
		return "Assistant declaration phase could not start."
	case FailureCodeAssistantTurnFailed:
		return "Assistant declaration phase failed."
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
	if f.Class != "" && !isAllowedFailureClass(f.Class) {
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

// NormalizeFailureClass accepts only the locked content-free class vocabulary.
// Unknown strings collapse to the provider API boundary rather than becoming
// arbitrary durable/error-detail text.
func NormalizeFailureClass(value string) FailureClass {
	class := FailureClass(strings.ToLower(strings.TrimSpace(value)))
	if isAllowedFailureClass(class) {
		return class
	}
	return FailureClassProviderAPIFailure
}

func isAllowedFailureClass(class FailureClass) bool {
	switch class {
	case FailureClassProviderTimeout,
		FailureClassProviderCanceled,
		FailureClassProviderAPIFailure,
		FailureClassInvalidProviderOutput,
		FailureClassParseValidation,
		FailureClassProviderEvidenceStore,
		FailureClassAssistantTurnStore:
		return true
	default:
		return false
	}
}

func isAllowedFailureCode(code FailureCode) bool {
	switch code {
	case FailureCodeLLMUnavailable,
		FailureCodeAssistantTurnFailed,
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
	PublishedVersion      int                    `json:"published_version,omitempty"`
	PublishedAt           time.Time              `json:"published_at,omitempty"`
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
	Publication           *PublicationCheckpoint
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
	if err := validateConversationProjectionStatus(status, input); err != nil {
		return ConversationProjection{}, err
	}
	publishedVersion, publishedAt, declarationCheckpoint, pollAfterSeconds := conversationProjectionPublicationFields(status, input)
	return ConversationProjection{
		RegistrationID:        strings.TrimSpace(input.RegistrationID),
		ConversationID:        strings.TrimSpace(input.ConversationID),
		AgentID:               strings.TrimSpace(input.AgentID),
		Status:                status,
		LatestTurnID:          strings.TrimSpace(input.LatestTurnID),
		MessageCount:          input.MessageCount,
		DeclarationCheckpoint: declarationCheckpoint,
		Failure:               input.Failure,
		PublishedVersion:      publishedVersion,
		PublishedAt:           publishedAt,
		RequestID:             strings.TrimSpace(input.RequestID),
		TraceIDs:              input.TraceIDs,
		PollAfterSeconds:      pollAfterSeconds,
		CreatedAt:             input.CreatedAt,
		UpdatedAt:             input.UpdatedAt,
		CompletedAt:           input.CompletedAt,
	}, nil
}

func validateConversationProjectionStatus(status Status, input ProjectionInput) error {
	switch status {
	case StatusDeclarationReady:
		return CanFinalize(status, input.DeclarationCheckpoint)
	case StatusFailed:
		if input.Failure == nil {
			return ErrInvalidFailureRecovery
		}
		return input.Failure.Validate()
	case StatusPublished:
		if input.DeclarationCheckpoint == nil || input.Failure != nil || input.Publication == nil {
			return ErrInvalidPublicationCheckpoint
		}
		if err := CanPublish(PublishGateInput{
			Status:                StatusDeclarationReady,
			RegistrationID:        input.RegistrationID,
			ConversationID:        input.ConversationID,
			AgentID:               input.AgentID,
			DeclarationCheckpoint: input.DeclarationCheckpoint,
		}); err != nil {
			return ErrInvalidPublicationCheckpoint
		}
		return input.Publication.ValidatePublished(input.RegistrationID, input.ConversationID, input.AgentID)
	default:
		return nil
	}
}

func conversationProjectionPublicationFields(status Status, input ProjectionInput) (int, time.Time, *DeclarationCheckpoint, int) {
	if status != StatusPublished || input.Publication == nil {
		return 0, time.Time{}, input.DeclarationCheckpoint, input.PollAfterSeconds
	}
	return input.Publication.Version, input.Publication.PublishedAt, nil, 0
}
