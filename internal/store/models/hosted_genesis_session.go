package models

import (
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/modelselection"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

// HostedGenesisSession stores Host's durable source of truth for a hosted/off-
// chain genesis conversation. AppTheory MicroVM records are execution/cache
// state only and are reconstructible from this row.
//
// Keys are tenant-scoped by managed instance slug:
//
//	PK: HOSTED_GENESIS#INSTANCE#{instanceSlug}
//	SK: SESSION#{conversationId}
//
// GSI keys also include the instance slug so registration/agent lookups do not
// cross tenant boundaries.
type HostedGenesisSession struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`
	GSI2PK string `theorydb:"index:gsi2,pk,attr:gsi2PK" json:"-"`
	GSI2SK string `theorydb:"index:gsi2,sk,attr:gsi2SK" json:"-"`

	Version int64 `theorydb:"version" json:"version"`

	InstanceSlug   string `theorydb:"attr:instanceSlug" json:"instance_slug"`
	RegistrationID string `theorydb:"attr:registrationId" json:"registration_id"`
	AgentID        string `theorydb:"attr:agentId" json:"agent_id"`
	ConversationID string `theorydb:"attr:conversationId" json:"conversation_id"`
	Status         string `theorydb:"attr:status" json:"status"`
	Model          string `theorydb:"attr:model" json:"model,omitempty"`

	LatestTurnID string                          `theorydb:"attr:latestTurnId" json:"latest_turn_id,omitempty"`
	MessageCount int                             `theorydb:"attr:messageCount" json:"message_count"`
	TurnLedger   []hostedgenesis.TurnLedgerEntry `theorydb:"attr:turnLedger" json:"turn_ledger,omitempty"`

	// Checkpoint references are ids only. Raw prompts, message lists, transcripts,
	// provider credentials, Instance API keys, wallet signatures, SSM values, AWS
	// credentials, and MicroVM endpoint tokens must not be persisted here.
	InputCheckpointRef     string                             `theorydb:"attr:inputCheckpointRef" json:"input_checkpoint_ref,omitempty"`
	AssistantCheckpointRef string                             `theorydb:"attr:assistantCheckpointRef" json:"assistant_checkpoint_ref,omitempty"`
	ExecutionStateRef      string                             `theorydb:"attr:executionStateRef" json:"execution_state_ref,omitempty"`
	MicroVMExecutionID     string                             `theorydb:"attr:microVmExecutionId" json:"microvm_execution_id,omitempty"`
	MicroVMLifecycleRef    *hostedgenesis.MicroVMLifecycleRef `theorydb:"attr:microVmLifecycleRef" json:"-"`

	DeclarationCheckpoint *hostedgenesis.DeclarationCheckpoint `theorydb:"attr:declarationCheckpoint" json:"declaration_checkpoint,omitempty"`
	DeclarationCandidate  *hostedgenesis.DeclarationCandidate  `theorydb:"attr:declarationCandidate" json:"declaration_candidate,omitempty"`
	CandidateRevision     int64                                `theorydb:"attr:candidateRevision" json:"candidate_revision,omitempty"`
	CandidateHash         string                               `theorydb:"attr:candidateHash" json:"candidate_hash,omitempty"`
	CandidatePhase        string                               `theorydb:"attr:candidatePhase" json:"candidate_phase,omitempty"`
	Publication           *hostedgenesis.PublicationCheckpoint `theorydb:"attr:publication" json:"publication,omitempty"`
	Failure               *hostedgenesis.Failure               `theorydb:"attr:failure" json:"failure,omitempty"`
	TraceIDs              *hostedgenesis.TraceIDs              `theorydb:"attr:traceIds" json:"trace_ids,omitempty"`
	VMCheckpoint          *hostedgenesis.VMCheckpointMetadata  `theorydb:"attr:vmCheckpoint" json:"vm_checkpoint,omitempty"`

	RequestID string `theorydb:"attr:requestId" json:"request_id,omitempty"`

	CreatedAt   time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt   time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
	CompletedAt time.Time `theorydb:"attr:completedAt" json:"completed_at,omitempty"`
}

// TableName returns the database table name for HostedGenesisSession.
func (HostedGenesisSession) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating HostedGenesisSession.
func (s *HostedGenesisSession) BeforeCreate() error {
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	if strings.TrimSpace(s.Status) == "" {
		s.Status = string(hostedgenesis.StatusCreated)
	}
	return s.validateAndUpdateKeys()
}

// BeforeUpdate updates keys and mutable timestamps before updating HostedGenesisSession.
func (s *HostedGenesisSession) BeforeUpdate() error {
	s.UpdatedAt = time.Now().UTC()
	return s.validateAndUpdateKeys()
}

// UpdateKeys normalizes durable ids and recomputes table/index keys.
func (s *HostedGenesisSession) UpdateKeys() error {
	s.InstanceSlug = normalizeSlug(s.InstanceSlug)
	s.RegistrationID = strings.TrimSpace(s.RegistrationID)
	s.AgentID = strings.ToLower(strings.TrimSpace(s.AgentID))
	s.ConversationID = strings.TrimSpace(s.ConversationID)
	s.Status = string(hostedgenesis.NormalizeStatus(s.Status))
	s.Model = strings.TrimSpace(s.Model)
	s.LatestTurnID = strings.TrimSpace(s.LatestTurnID)
	s.InputCheckpointRef = hostedgenesis.NormalizeCheckpointRef(s.InputCheckpointRef)
	s.AssistantCheckpointRef = hostedgenesis.NormalizeCheckpointRef(s.AssistantCheckpointRef)
	s.ExecutionStateRef = strings.TrimSpace(s.ExecutionStateRef)
	s.MicroVMExecutionID = strings.TrimSpace(s.MicroVMExecutionID)
	for i := range s.TurnLedger {
		s.TurnLedger[i] = s.TurnLedger[i].Normalize()
	}
	if s.DeclarationCheckpoint != nil {
		s.DeclarationCheckpoint.CheckpointRef = hostedgenesis.NormalizeCheckpointRef(s.DeclarationCheckpoint.CheckpointRef)
	}
	if err := s.bindDeclarationCandidate(); err != nil {
		return err
	}
	if s.MicroVMLifecycleRef != nil {
		binding := s.MicroVMSessionBinding()
		if err := s.MicroVMLifecycleRef.Validate(binding); err != nil {
			return err
		}
	}
	if s.VMCheckpoint != nil {
		checkpoint := s.VMCheckpoint.Normalize()
		if err := checkpoint.Validate(); err != nil {
			return err
		}
		s.VMCheckpoint = &checkpoint
	}
	s.RequestID = strings.TrimSpace(s.RequestID)

	s.PK = HostedGenesisSessionPK(s.InstanceSlug)
	s.SK = HostedGenesisSessionSK(s.ConversationID)
	s.GSI1PK = HostedGenesisSessionRegistrationGSI1PK(s.InstanceSlug, s.RegistrationID)
	s.GSI1SK = fmt.Sprintf("%s#%s", s.CreatedAt.UTC().Format(time.RFC3339Nano), s.ConversationID)
	s.GSI2PK = HostedGenesisSessionAgentGSI2PK(s.InstanceSlug, s.AgentID)
	s.GSI2SK = fmt.Sprintf("%s#%s", s.CreatedAt.UTC().Format(time.RFC3339Nano), s.ConversationID)
	return nil
}

func (s *HostedGenesisSession) bindDeclarationCandidate() error {
	if s.DeclarationCandidate == nil {
		if s.CandidateRevision != 0 || strings.TrimSpace(s.CandidateHash) != "" || strings.TrimSpace(s.CandidatePhase) != "" {
			return fmt.Errorf("declaration candidate checkpoint exists without candidate state")
		}
		return nil
	}
	candidate := s.DeclarationCandidate.Clone()
	if err := candidate.Validate(); err != nil {
		return fmt.Errorf("declaration candidate: %w", err)
	}
	if !s.declarationCandidateBindingMatches(candidate) {
		return fmt.Errorf("declaration candidate binding does not match hosted genesis session")
	}
	s.DeclarationCandidate = candidate
	s.CandidateRevision = candidate.Revision
	s.CandidateHash = candidate.CandidateHash
	s.CandidatePhase = string(candidate.Phase)
	return nil
}

func (s *HostedGenesisSession) declarationCandidateBindingMatches(candidate *hostedgenesis.DeclarationCandidate) bool {
	return candidate.InstanceSlug == s.InstanceSlug && candidate.RegistrationID == s.RegistrationID &&
		strings.EqualFold(candidate.AgentID, s.AgentID) && candidate.ConversationID == s.ConversationID &&
		strings.EqualFold(modelselection.CanonicalModelSet(candidate.Model), modelselection.CanonicalModelSet(s.Model)) &&
		candidate.SourceTurnID == s.LatestTurnID
}

func (s *HostedGenesisSession) validateAndUpdateKeys() error {
	if err := s.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", s.InstanceSlug); err != nil {
		return err
	}
	if err := requireNonEmpty("registrationId", s.RegistrationID); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", s.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("conversationId", s.ConversationID); err != nil {
		return err
	}
	if !hostedgenesis.IsAllowedStatus(hostedgenesis.Status(s.Status)) {
		return fmt.Errorf("status is invalid")
	}
	if err := s.validateDurableStatusContract(); err != nil {
		return err
	}
	if err := s.validateTurnLedger(); err != nil {
		return err
	}
	return nil
}

func (s *HostedGenesisSession) validateDurableStatusContract() error {
	switch hostedgenesis.Status(s.Status) {
	case hostedgenesis.StatusDeclarationReady:
		if err := hostedgenesis.CanPublish(hostedgenesis.PublishGateInput{
			Status:                hostedgenesis.Status(s.Status),
			RegistrationID:        s.RegistrationID,
			ConversationID:        s.ConversationID,
			AgentID:               s.AgentID,
			DeclarationCheckpoint: s.DeclarationCheckpoint,
		}); err != nil {
			return err
		}
		if s.Publication != nil {
			if err := s.Publication.ValidatePrepared(s.RegistrationID, s.ConversationID, s.AgentID); err != nil {
				return err
			}
		}
		return nil
	case hostedgenesis.StatusPublished:
		if s.Failure != nil {
			return hostedgenesis.ErrInvalidPublicationCheckpoint
		}
		if err := hostedgenesis.CanPublish(hostedgenesis.PublishGateInput{
			Status:                hostedgenesis.StatusDeclarationReady,
			RegistrationID:        s.RegistrationID,
			ConversationID:        s.ConversationID,
			AgentID:               s.AgentID,
			DeclarationCheckpoint: s.DeclarationCheckpoint,
		}); err != nil {
			return err
		}
		if s.Publication == nil {
			return hostedgenesis.ErrInvalidPublicationCheckpoint
		}
		if err := s.Publication.ValidatePublished(s.RegistrationID, s.ConversationID, s.AgentID); err != nil {
			return err
		}
		return nil
	case hostedgenesis.StatusFailed:
		if s.Failure == nil {
			return hostedgenesis.ErrInvalidFailureRecovery
		}
		if err := s.Failure.Validate(); err != nil {
			return err
		}
		return nil
	default:
		return nil
	}
}

func (s *HostedGenesisSession) validateTurnLedger() error {
	if len(s.TurnLedger) == 0 {
		return nil
	}
	if err := hostedgenesis.ValidateTurnLedger(s.TurnLedger); err != nil {
		return err
	}
	last := s.TurnLedger[len(s.TurnLedger)-1].Normalize()
	if s.LatestTurnID != "" && s.LatestTurnID != last.TurnID {
		return hostedgenesis.ErrInvalidTurnLedger
	}
	if s.MessageCount < last.MessageCount {
		return hostedgenesis.ErrInvalidTurnLedger
	}
	return nil
}

// ToProjectionInput converts the store model to a compact hostedgenesis
// projection input without exposing database keys or execution-cache details.
func (s *HostedGenesisSession) ToProjectionInput() hostedgenesis.ProjectionInput {
	if s == nil {
		return hostedgenesis.ProjectionInput{}
	}
	return hostedgenesis.ProjectionInput{
		RegistrationID:        s.RegistrationID,
		ConversationID:        s.ConversationID,
		AgentID:               s.AgentID,
		Status:                hostedgenesis.Status(s.Status),
		LatestTurnID:          s.LatestTurnID,
		MessageCount:          s.MessageCount,
		DeclarationCheckpoint: s.DeclarationCheckpoint,
		Failure:               s.Failure,
		Publication:           s.Publication,
		RequestID:             s.RequestID,
		TraceIDs:              s.TraceIDs,
		CreatedAt:             s.CreatedAt,
		UpdatedAt:             s.UpdatedAt,
		CompletedAt:           s.CompletedAt,
	}
}

// MicroVMSessionBinding rebuilds the AppTheory MicroVM controller binding from
// Host's authoritative HostedGenesisSession ids. Registry/cache state is not
// required to reconstruct this envelope.
func (s *HostedGenesisSession) MicroVMSessionBinding() hostedgenesis.MicroVMSessionBinding {
	if s == nil {
		return hostedgenesis.MicroVMSessionBinding{}
	}
	turnID := s.LatestTurnID
	if turnID == "" && len(s.TurnLedger) > 0 {
		turnID = s.TurnLedger[len(s.TurnLedger)-1].TurnID
	}
	return hostedgenesis.MicroVMSessionBinding{
		InstanceSlug:   normalizeSlug(s.InstanceSlug),
		RegistrationID: strings.TrimSpace(s.RegistrationID),
		AgentID:        strings.ToLower(strings.TrimSpace(s.AgentID)),
		ConversationID: strings.TrimSpace(s.ConversationID),
		TurnID:         strings.TrimSpace(turnID),
	}
}

// ApplyMicroVMLifecycleRef records non-authoritative AppTheory execution/cache
// state after validating that it maps back to this Host session.
func (s *HostedGenesisSession) ApplyMicroVMLifecycleRef(ref hostedgenesis.MicroVMLifecycleRef) error {
	if s == nil {
		return hostedgenesis.ErrInvalidMicroVMLifecycleRef
	}
	binding := s.MicroVMSessionBinding()
	if err := ref.Validate(binding); err != nil {
		return err
	}
	s.MicroVMLifecycleRef = &ref
	s.ExecutionStateRef = hostedgenesis.FormatMicroVMExecutionStateRef(ref)
	s.MicroVMExecutionID = strings.TrimSpace(ref.MicroVMID)
	return nil
}

// GetPK returns the partition key for HostedGenesisSession.
func (s *HostedGenesisSession) GetPK() string { return s.PK }

// GetSK returns the sort key for HostedGenesisSession.
func (s *HostedGenesisSession) GetSK() string { return s.SK }

// HostedGenesisSessionPK returns the tenant-scoped session partition key.
func HostedGenesisSessionPK(instanceSlug string) string {
	return fmt.Sprintf("HOSTED_GENESIS#INSTANCE#%s", normalizeSlug(instanceSlug))
}

// HostedGenesisSessionSK returns the session sort key.
func HostedGenesisSessionSK(conversationID string) string {
	return fmt.Sprintf("SESSION#%s", strings.TrimSpace(conversationID))
}

// HostedGenesisSessionRegistrationGSI1PK returns the tenant-scoped registration index key.
func HostedGenesisSessionRegistrationGSI1PK(instanceSlug string, registrationID string) string {
	return fmt.Sprintf("HOSTED_GENESIS#INSTANCE#%s#REGISTRATION#%s", normalizeSlug(instanceSlug), strings.TrimSpace(registrationID))
}

// HostedGenesisSessionAgentGSI2PK returns the tenant-scoped agent index key.
func HostedGenesisSessionAgentGSI2PK(instanceSlug string, agentID string) string {
	return fmt.Sprintf("HOSTED_GENESIS#INSTANCE#%s#AGENT#%s", normalizeSlug(instanceSlug), strings.ToLower(strings.TrimSpace(agentID)))
}

// HostedGenesisSessionUpdateFields enumerates fields that can change under an
// expected-version update. Version is omitted so TableTheory owns optimistic
// lock increments.
func HostedGenesisSessionUpdateFields() []string {
	return []string{
		"Status",
		"Model",
		"LatestTurnID",
		"MessageCount",
		"TurnLedger",
		"InputCheckpointRef",
		"AssistantCheckpointRef",
		"ExecutionStateRef",
		"MicroVMExecutionID",
		"MicroVMLifecycleRef",
		"DeclarationCheckpoint",
		"DeclarationCandidate",
		"CandidateRevision",
		"CandidateHash",
		"CandidatePhase",
		"Publication",
		"Failure",
		"TraceIDs",
		"VMCheckpoint",
		"RequestID",
		"UpdatedAt",
		"CompletedAt",
	}
}

// NewHostedGenesisSessionFromSeed converts a store-agnostic migration/runtime
// seed into the TableTheory model without importing raw transcript fields.
func NewHostedGenesisSessionFromSeed(seed hostedgenesis.SessionSeed) *HostedGenesisSession {
	return &HostedGenesisSession{
		InstanceSlug:          seed.InstanceSlug,
		RegistrationID:        seed.RegistrationID,
		AgentID:               seed.AgentID,
		ConversationID:        seed.ConversationID,
		Status:                string(seed.Status),
		Model:                 seed.Model,
		LatestTurnID:          seed.LatestTurnID,
		MessageCount:          seed.MessageCount,
		TurnLedger:            append([]hostedgenesis.TurnLedgerEntry(nil), seed.TurnLedger...),
		DeclarationCheckpoint: seed.DeclarationCheckpoint,
		Failure:               seed.Failure,
		TraceIDs:              seed.TraceIDs,
		RequestID:             seed.RequestID,
		CreatedAt:             seed.CreatedAt,
		UpdatedAt:             seed.UpdatedAt,
		CompletedAt:           seed.CompletedAt,
	}
}

func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}
