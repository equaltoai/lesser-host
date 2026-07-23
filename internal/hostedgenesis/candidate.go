package hostedgenesis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/equaltoai/lesser-host/internal/soul"
)

const (
	DeclarationCandidateVersionV1  = "hosted-genesis-declaration-candidate.v1"
	DeclarationReviewRendererV1    = "hosted-genesis-owner-review.v1"
	MaxDeclarationToolRecords      = 64
	MaxDeclarationProviderAttempts = 64
	// MaxDeclarationOwnerReviewRunes matches candidate_review.review_text's
	// documented API maxLength. Renderers fail closed before a larger review
	// can enter durable state or a response projection.
	MaxDeclarationOwnerReviewRunes = 65536
)

const (
	declarationOwnerReviewCanonicalLength = "Canonical JSON byte length: "
	declarationOwnerReviewCanonicalBegin  = "-----BEGIN HOSTED GENESIS CANONICAL JSON-----"
	declarationOwnerReviewCanonicalEnd    = "-----END HOSTED GENESIS CANONICAL JSON-----"
)

var errDeclarationOwnerReviewTooLarge = errors.New("declaration owner review exceeds the documented API limit")

// DeclarationSection is one canonical five-body construction phase. The order
// is part of the durable protocol and must not be inferred from transcript text.
type DeclarationSection string

const (
	DeclarationSectionIdentity   DeclarationSection = "identity"
	DeclarationSectionPhilosophy DeclarationSection = "philosophy"
	DeclarationSectionDiscipline DeclarationSection = "discipline"
	DeclarationSectionBoundaries DeclarationSection = "boundaries"
	DeclarationSectionSoul       DeclarationSection = "soul"
)

var declarationSectionOrder = []DeclarationSection{
	DeclarationSectionIdentity,
	DeclarationSectionPhilosophy,
	DeclarationSectionDiscipline,
	DeclarationSectionBoundaries,
	DeclarationSectionSoul,
}

type DeclarationCandidatePhase string

const (
	DeclarationCandidatePhaseSection   DeclarationCandidatePhase = "section"
	DeclarationCandidatePhaseReview    DeclarationCandidatePhase = "review"
	DeclarationCandidatePhaseAffirmed  DeclarationCandidatePhase = "affirmed"
	DeclarationCandidatePhaseFinalized DeclarationCandidatePhase = "finalized"
)

const (
	DeclarationToolIdentityPut   = "declaration_identity_put"
	DeclarationToolPhilosophyPut = "declaration_philosophy_put"
	DeclarationToolDisciplinePut = "declaration_discipline_put"
	DeclarationToolBoundariesPut = "declaration_boundaries_put"
	DeclarationToolSoulPut       = "declaration_soul_put"
)

// DeclarationTransparency is the bounded satellite shape accepted by the soul
// phase. A typed shape prevents arbitrary provider objects from entering state.
type DeclarationTransparency struct {
	ModelProviderUncertainty string `json:"modelProviderUncertainty"`
	OperationalNotes         string `json:"operationalNotes"`
	SelfDeclaredNotice       string `json:"selfDeclaredNotice"`
}

// DeclarationToolRecord makes provider tool calls idempotent without retaining
// provider content or the provider's raw call identifier.
type DeclarationToolRecord struct {
	ToolCallHash  string             `json:"tool_call_hash"`
	InputHash     string             `json:"input_hash"`
	ToolName      string             `json:"tool_name"`
	Section       DeclarationSection `json:"section"`
	SourceTurnID  string             `json:"source_turn_id"`
	Revision      int64              `json:"revision"`
	SectionHash   string             `json:"section_hash"`
	CandidateHash string             `json:"candidate_hash"`
}

// DeclarationProviderAttempt is bounded, content-free durable evidence for
// one provider SDK HTTP attempt. It is deliberately outside CanonicalJSON, so
// recovery telemetry cannot change owner-reviewed semantic bytes.
type DeclarationProviderAttempt struct {
	Sequence          int64                       `json:"sequence"`
	Provider          string                      `json:"provider"`
	Model             string                      `json:"model"`
	Phase             string                      `json:"phase"`
	Section           DeclarationSection          `json:"section"`
	SourceTurnID      string                      `json:"source_turn_id"`
	CandidateRevision int64                       `json:"candidate_revision"`
	CandidateHash     string                      `json:"candidate_hash"`
	SDKAttemptOrdinal int64                       `json:"sdk_attempt_ordinal"`
	SDKRetryBudget    int                         `json:"sdk_retry_budget"`
	HTTPStatus        int                         `json:"http_status,omitempty"`
	ProviderRequestID string                      `json:"provider_request_id,omitempty"`
	ToolName          string                      `json:"tool_name,omitempty"`
	ToolCallHash      string                      `json:"tool_call_hash,omitempty"`
	ValidationCodes   []DeclarationValidationCode `json:"validation_codes,omitempty"`
	ValidationPaths   []string                    `json:"validation_paths,omitempty"`
	Accepted          bool                        `json:"accepted,omitempty"`
	OutputBytes       int                         `json:"output_bytes,omitempty"`
	OutputSHA256      string                      `json:"output_sha256,omitempty"`
	InputTokens       int64                       `json:"input_tokens,omitempty"`
	OutputTokens      int64                       `json:"output_tokens,omitempty"`
	TotalTokens       int64                       `json:"total_tokens,omitempty"`
	ToolCalls         int64                       `json:"tool_calls,omitempty"`
	StopReason        string                      `json:"stop_reason,omitempty"`
	FailureClass      FailureClass                `json:"failure_class,omitempty"`
	DurationMS        int64                       `json:"duration_ms,omitempty"`
	ObservedAt        time.Time                   `json:"observed_at"`
}

type DeclarationProviderAttemptUpdate struct {
	Provider          string
	Model             string
	Phase             string
	Section           DeclarationSection
	SourceTurnID      string
	CandidateRevision int64
	CandidateHash     string
	SDKAttemptOrdinal int64
	SDKRetryBudget    int
	HTTPStatus        int
	ProviderRequestID string
	ToolName          string
	ToolCallHash      string
	ValidationCodes   []DeclarationValidationCode
	ValidationPaths   []string
	Accepted          bool
	OutputBytes       int
	OutputSHA256      string
	InputTokens       int64
	OutputTokens      int64
	TotalTokens       int64
	ToolCalls         int64
	StopReason        string
	FailureClass      FailureClass
	DurationMS        int64
}

type DeclarationReviewCheckpoint struct {
	RendererVersion   string    `json:"renderer_version"`
	SchemaVersion     string    `json:"schema_version"`
	GuidanceVersion   string    `json:"guidance_version"`
	SourceTurnID      string    `json:"source_turn_id"`
	CandidateHash     string    `json:"candidate_hash"`
	ReviewHash        string    `json:"review_hash"`
	CandidateRevision int64     `json:"candidate_revision"`
	ReviewText        string    `json:"review_text"`
	ReviewedAt        time.Time `json:"reviewed_at"`
}

type DeclarationAffirmationCheckpoint struct {
	CandidateRevision int64     `json:"candidate_revision"`
	CandidateHash     string    `json:"candidate_hash"`
	ReviewHash        string    `json:"review_hash"`
	SourceTurnID      string    `json:"source_turn_id"`
	AffirmedAt        time.Time `json:"affirmed_at"`
}

// DeclarationCandidate is the authoritative typed declaration under
// construction. CanonicalJSON is the exact byte string finalization publishes;
// CandidateHash always authenticates those bytes.
type DeclarationCandidate struct {
	Version           string                            `json:"version"`
	InstanceSlug      string                            `json:"instance_slug"`
	RegistrationID    string                            `json:"registration_id"`
	AgentID           string                            `json:"agent_id"`
	ConversationID    string                            `json:"conversation_id"`
	SourceTurnID      string                            `json:"source_turn_id"`
	SchemaVersion     string                            `json:"schema_version"`
	GuidanceVersion   string                            `json:"guidance_version"`
	Model             string                            `json:"model"`
	Phase             DeclarationCandidatePhase         `json:"phase"`
	CurrentSection    DeclarationSection                `json:"current_section,omitempty"`
	CompletedSections []DeclarationSection              `json:"completed_sections,omitempty"`
	FiveBodies        FiveBodyDeclaration               `json:"five_bodies"`
	SelfDescription   soul.SelfDescriptionV2            `json:"self_description"`
	Capabilities      []soul.CapabilityV2               `json:"capabilities,omitempty"`
	Transparency      DeclarationTransparency           `json:"transparency"`
	Revision          int64                             `json:"revision"`
	SectionHashes     map[string]string                 `json:"section_hashes,omitempty"`
	CandidateHash     string                            `json:"candidate_hash"`
	CanonicalJSON     string                            `json:"canonical_json"`
	ToolRecords       []DeclarationToolRecord           `json:"tool_records,omitempty"`
	ProviderAttempts  []DeclarationProviderAttempt      `json:"provider_attempts,omitempty"`
	Review            *DeclarationReviewCheckpoint      `json:"review,omitempty"`
	Affirmation       *DeclarationAffirmationCheckpoint `json:"affirmation,omitempty"`
	EstablishedAt     time.Time                         `json:"established_at"`
	UpdatedAt         time.Time                         `json:"updated_at"`
}

type DeclarationCandidateBinding struct {
	InstanceSlug   string
	RegistrationID string
	AgentID        string
	ConversationID string
	SourceTurnID   string
	Model          string
}

// NewDeclarationCandidate creates revision zero with a stable hash. New lanes
// receive this state before their first provider invocation; old lanes without
// it are rejected by the hard-cutover gate.
func NewDeclarationCandidate(binding DeclarationCandidateBinding, establishedAt time.Time) (*DeclarationCandidate, error) {
	if establishedAt.IsZero() {
		return nil, errors.New("declaration candidate requires established_at")
	}
	c := &DeclarationCandidate{
		Version:         DeclarationCandidateVersionV1,
		InstanceSlug:    strings.ToLower(strings.TrimSpace(binding.InstanceSlug)),
		RegistrationID:  strings.TrimSpace(binding.RegistrationID),
		AgentID:         strings.ToLower(strings.TrimSpace(binding.AgentID)),
		ConversationID:  strings.TrimSpace(binding.ConversationID),
		SourceTurnID:    strings.TrimSpace(binding.SourceTurnID),
		SchemaVersion:   DeclarationSchemaVersionV2,
		GuidanceVersion: GuidanceVersionV2,
		Model:           strings.TrimSpace(binding.Model),
		Phase:           DeclarationCandidatePhaseSection,
		CurrentSection:  DeclarationSectionIdentity,
		SectionHashes:   map[string]string{},
		Capabilities:    []soul.CapabilityV2{},
		EstablishedAt:   establishedAt.UTC(),
		UpdatedAt:       establishedAt.UTC(),
	}
	if err := c.refreshCanonical(); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

type DeclarationValidationIssue struct {
	Section DeclarationSection        `json:"section"`
	Path    string                    `json:"path"`
	Code    DeclarationValidationCode `json:"code"`
}

type DeclarationToolRequest struct {
	ToolName         string
	ToolCallID       string
	ExpectedRevision int64
	ExpectedHash     string
	SourceTurnID     string
	Payload          json.RawMessage
}

type DeclarationToolResult struct {
	Accepted      bool                         `json:"accepted"`
	Idempotent    bool                         `json:"idempotent,omitempty"`
	Section       DeclarationSection           `json:"section"`
	Revision      int64                        `json:"revision"`
	SectionHash   string                       `json:"section_hash,omitempty"`
	CandidateHash string                       `json:"candidate_hash"`
	Errors        []DeclarationValidationIssue `json:"errors,omitempty"`
}

type declarationSectionPayload struct {
	CandidateRevision *int64          `json:"candidateRevision,omitempty"`
	CandidateHash     string          `json:"candidateHash,omitempty"`
	Section           FiveBodySection `json:"section"`
}

type declarationSoulPayload struct {
	CandidateRevision *int64                  `json:"candidateRevision,omitempty"`
	CandidateHash     string                  `json:"candidateHash,omitempty"`
	Section           FiveBodySoulBody        `json:"section"`
	SelfDescription   soul.SelfDescriptionV2  `json:"selfDescription"`
	Capabilities      []soul.CapabilityV2     `json:"capabilities"`
	Transparency      DeclarationTransparency `json:"transparency"`
}

func DeclarationSectionForTool(toolName string) (DeclarationSection, bool) {
	switch strings.TrimSpace(toolName) {
	case DeclarationToolIdentityPut:
		return DeclarationSectionIdentity, true
	case DeclarationToolPhilosophyPut:
		return DeclarationSectionPhilosophy, true
	case DeclarationToolDisciplinePut:
		return DeclarationSectionDiscipline, true
	case DeclarationToolBoundariesPut:
		return DeclarationSectionBoundaries, true
	case DeclarationToolSoulPut:
		return DeclarationSectionSoul, true
	default:
		return "", false
	}
}

func DeclarationToolForSection(section DeclarationSection) (string, bool) {
	switch section {
	case DeclarationSectionIdentity:
		return DeclarationToolIdentityPut, true
	case DeclarationSectionPhilosophy:
		return DeclarationToolPhilosophyPut, true
	case DeclarationSectionDiscipline:
		return DeclarationToolDisciplinePut, true
	case DeclarationSectionBoundaries:
		return DeclarationToolBoundariesPut, true
	case DeclarationSectionSoul:
		return DeclarationToolSoulPut, true
	default:
		return "", false
	}
}

// ApplyDeclarationTool validates one phase locally and returns machine-readable
// errors. It never mutates the input candidate on rejection.
func ApplyDeclarationTool(candidate *DeclarationCandidate, request DeclarationToolRequest, now time.Time) (*DeclarationCandidate, DeclarationToolResult, error) {
	if candidate == nil {
		return nil, DeclarationToolResult{}, errors.New("typed declaration candidate is required")
	}
	preflight, rejection := preflightDeclarationTool(candidate, request)
	if rejection != nil {
		return nil, *rejection, nil
	}
	if preflight.replay != nil {
		return candidate.Clone(), *preflight.replay, nil
	}

	updated := candidate.Clone()
	updated.SourceTurnID = preflight.sourceTurnID
	validationIssues := applyDeclarationToolPayload(updated, preflight.section, request)
	if len(validationIssues) > 0 {
		return nil, candidate.resultFor(preflight.section, validationIssues...), nil
	}

	updated.Revision++
	updated.Review = nil
	updated.Affirmation = nil
	updated.UpdatedAt = now.UTC()
	updated.markCompleted(preflight.section)
	sectionBytes, err := updated.sectionBytes(preflight.section)
	if err != nil {
		return nil, DeclarationToolResult{}, err
	}
	sectionHash := hashBytes(sectionBytes)
	updated.SectionHashes[string(preflight.section)] = sectionHash
	if next, found := updated.nextIncompleteSection(); found {
		updated.CurrentSection = next
		updated.Phase = DeclarationCandidatePhaseSection
	} else {
		updated.CurrentSection = ""
		updated.Phase = DeclarationCandidatePhaseReview
	}
	if err := updated.refreshCanonical(); err != nil {
		return nil, DeclarationToolResult{}, err
	}
	if updated.Phase == DeclarationCandidatePhaseReview {
		if err := ValidateDeclarationCandidateComplete(updated); err != nil {
			code := DeclarationValidationCodeFromError(err)
			return nil, candidate.resultFor(preflight.section, issueForCode(declarationSectionForValidationCode(preflight.section, code), code)), nil
		}
		review, err := RenderDeclarationOwnerReview(updated, request.SourceTurnID, now)
		if err != nil {
			if errors.Is(err, errDeclarationOwnerReviewTooLarge) {
				return nil, candidate.resultFor(preflight.section, issue(preflight.section, "candidate.review_text", DeclarationCodeInvalid)), nil
			}
			return nil, DeclarationToolResult{}, err
		}
		updated.Review = &review
	}
	record := DeclarationToolRecord{
		ToolCallHash: preflight.callHash, InputHash: preflight.inputHash, ToolName: request.ToolName, Section: preflight.section,
		SourceTurnID: preflight.sourceTurnID, Revision: updated.Revision,
		SectionHash: sectionHash, CandidateHash: updated.CandidateHash,
	}
	updated.ToolRecords = append(updated.ToolRecords, record)
	if err := updated.Validate(); err != nil {
		return nil, DeclarationToolResult{}, err
	}
	return updated, DeclarationToolResult{
		Accepted: true, Section: preflight.section, Revision: updated.Revision,
		SectionHash: sectionHash, CandidateHash: updated.CandidateHash,
	}, nil
}

type declarationToolPreflight struct {
	section      DeclarationSection
	callHash     string
	inputHash    string
	sourceTurnID string
	replay       *DeclarationToolResult
}

func preflightDeclarationTool(candidate *DeclarationCandidate, request DeclarationToolRequest) (declarationToolPreflight, *DeclarationToolResult) {
	section, ok := DeclarationSectionForTool(request.ToolName)
	if !ok {
		result := candidate.resultFor(section, issue(section, "tool.name", DeclarationCodeInvalid))
		return declarationToolPreflight{}, &result
	}
	preflight := declarationToolPreflight{
		section: section, callHash: hashText(strings.TrimSpace(request.ToolCallID)), inputHash: hashBytes(request.Payload),
		sourceTurnID: strings.TrimSpace(request.SourceTurnID),
	}
	if strings.TrimSpace(request.ToolCallID) == "" || preflight.sourceTurnID == "" {
		return preflight, declarationToolRejection(candidate, section, "tool.call_id")
	}
	if preflight.sourceTurnID != candidate.SourceTurnID {
		return preflight, declarationToolRejection(candidate, section, "candidate.source_turn_id")
	}
	if replay, rejection := declarationToolReplay(candidate, request, preflight); replay != nil || rejection != nil {
		preflight.replay = replay
		return preflight, rejection
	}
	if len(candidate.ToolRecords) >= MaxDeclarationToolRecords {
		return preflight, declarationToolRejection(candidate, section, "tool.call_id")
	}
	if candidate.Phase != DeclarationCandidatePhaseSection || candidate.CurrentSection != section {
		return preflight, declarationToolRejection(candidate, section, "candidate.current_section")
	}
	if candidate.Revision != request.ExpectedRevision {
		return preflight, declarationToolRejection(candidate, section, "candidate.revision")
	}
	if strings.TrimSpace(candidate.CandidateHash) != strings.TrimSpace(request.ExpectedHash) {
		return preflight, declarationToolRejection(candidate, section, "candidate.hash")
	}
	return preflight, nil
}

func declarationToolReplay(candidate *DeclarationCandidate, request DeclarationToolRequest, preflight declarationToolPreflight) (*DeclarationToolResult, *DeclarationToolResult) {
	for _, record := range candidate.ToolRecords {
		if record.ToolCallHash != preflight.callHash {
			continue
		}
		if record.InputHash != preflight.inputHash || record.ToolName != request.ToolName || record.Section != preflight.section || record.SourceTurnID != preflight.sourceTurnID {
			return nil, declarationToolRejection(candidate, preflight.section, "tool.call_id")
		}
		return &DeclarationToolResult{
			Accepted: true, Idempotent: true, Section: record.Section, Revision: record.Revision,
			SectionHash: record.SectionHash, CandidateHash: record.CandidateHash,
		}, nil
	}
	return nil, nil
}

func declarationToolRejection(candidate *DeclarationCandidate, section DeclarationSection, path string) *DeclarationToolResult {
	result := candidate.resultFor(section, issue(section, path, DeclarationCodeInvalid))
	return &result
}

func applyDeclarationToolPayload(updated *DeclarationCandidate, section DeclarationSection, request DeclarationToolRequest) []DeclarationValidationIssue {
	if section == DeclarationSectionSoul {
		return applyDeclarationSoulPayload(updated, request)
	}
	var payload declarationSectionPayload
	if err := decodeDeclarationToolPayload(request.Payload, &payload); err != nil {
		return []DeclarationValidationIssue{issue(section, "fiveBodies."+string(section), DeclarationCodeInvalid)}
	}
	if bindingIssue := declarationPayloadBindingIssue(section, payload.CandidateRevision, payload.CandidateHash, request); bindingIssue != nil {
		return []DeclarationValidationIssue{*bindingIssue}
	}
	setCandidateSection(updated, section, normalizeFiveBodySection(payload.Section))
	if err := validateDeclarationSection(section, updated.FiveBodies); err != nil {
		return []DeclarationValidationIssue{issueForCode(section, DeclarationValidationCodeFromError(err))}
	}
	return nil
}

func applyDeclarationSoulPayload(updated *DeclarationCandidate, request DeclarationToolRequest) []DeclarationValidationIssue {
	section := DeclarationSectionSoul
	var payload declarationSoulPayload
	if err := decodeDeclarationToolPayload(request.Payload, &payload); err != nil {
		return []DeclarationValidationIssue{issue(section, "fiveBodies.soul", DeclarationCodeInvalid)}
	}
	if bindingIssue := declarationPayloadBindingIssue(section, payload.CandidateRevision, payload.CandidateHash, request); bindingIssue != nil {
		return []DeclarationValidationIssue{*bindingIssue}
	}
	updated.FiveBodies.Soul = NormalizeFiveBodyDeclaration(FiveBodyDeclaration{Soul: payload.Section}).Soul
	updated.SelfDescription = normalizeCandidateSelfDescription(payload.SelfDescription, updated.FiveBodies, updated.Model)
	updated.Transparency = normalizeCandidateTransparency(payload.Transparency)
	capabilities, issues := validateCandidateSoulSatellites(section, payload.Capabilities, updated.SelfDescription, updated.Transparency)
	updated.Capabilities = capabilities
	if err := validateDeclarationSection(section, updated.FiveBodies); err != nil {
		issues = append(issues, issueForCode(section, DeclarationValidationCodeFromError(err)))
	}
	return issues
}

func declarationPayloadBindingIssue(section DeclarationSection, revision *int64, candidateHash string, request DeclarationToolRequest) *DeclarationValidationIssue {
	if revision == nil || *revision != request.ExpectedRevision {
		value := issue(section, "candidate.revision", DeclarationCodeInvalid)
		return &value
	}
	if strings.TrimSpace(candidateHash) != strings.TrimSpace(request.ExpectedHash) {
		value := issue(section, "candidate.hash", DeclarationCodeInvalid)
		return &value
	}
	return nil
}

func decodeDeclarationToolPayload(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("declaration tool payload contains multiple values")
		}
		return err
	}
	return nil
}

// ApplyDeclarationProviderAttempt appends or enriches content-free provider
// evidence without changing the semantic candidate revision or hash. An SDK
// attempt update appends a new record; later tool/completion observations bind
// to the most recent record for the exact turn/section/revision/hash tuple.
func ApplyDeclarationProviderAttempt(candidate *DeclarationCandidate, update DeclarationProviderAttemptUpdate, now time.Time) (*DeclarationCandidate, error) {
	provider := strings.ToLower(strings.TrimSpace(update.Provider))
	model := strings.TrimSpace(update.Model)
	if err := validateDeclarationProviderAttemptUpdate(candidate, update, provider, model, now); err != nil {
		return nil, err
	}
	updated := candidate.Clone()
	index, err := declarationProviderAttemptIndex(updated, update, provider, model, now)
	if err != nil {
		return nil, err
	}
	attempt := &updated.ProviderAttempts[index]
	applyDeclarationProviderAttemptMetadata(attempt, update, now)
	updated.UpdatedAt = now.UTC()
	if err := updated.Validate(); err != nil {
		return nil, err
	}
	return updated, nil
}

func validateDeclarationProviderAttemptUpdate(candidate *DeclarationCandidate, update DeclarationProviderAttemptUpdate, provider, model string, now time.Time) error {
	if candidate == nil || now.IsZero() || strings.TrimSpace(update.SourceTurnID) == "" || update.CandidateRevision < 0 || strings.TrimSpace(update.CandidateHash) == "" {
		return errors.New("declaration provider attempt binding is invalid")
	}
	if _, ok := DeclarationToolForSection(update.Section); !ok || provider == "" || model == "" || update.Phase != "declaration_phase" {
		return errors.New("declaration provider attempt metadata is invalid")
	}
	if !strings.EqualFold(candidate.Model, provider+":"+model) {
		return errors.New("declaration provider attempt model binding mismatch")
	}
	return nil
}

func declarationProviderAttemptIndex(candidate *DeclarationCandidate, update DeclarationProviderAttemptUpdate, provider, model string, now time.Time) (int, error) {
	if update.SDKAttemptOrdinal <= 0 {
		return existingDeclarationProviderAttemptIndex(candidate.ProviderAttempts, update)
	}
	directBinding := update.CandidateRevision == candidate.Revision && update.CandidateHash == candidate.CandidateHash &&
		candidate.Phase == DeclarationCandidatePhaseSection && candidate.CurrentSection == update.Section
	if update.SourceTurnID != candidate.SourceTurnID || (!directBinding && !declarationProviderContinuationBound(candidate, update)) {
		return -1, errors.New("declaration provider attempt candidate binding mismatch")
	}
	if declarationProviderAttemptOrdinalIsStale(candidate.ProviderAttempts, update) {
		return -1, errors.New("declaration provider attempt ordinal is stale")
	}
	if len(candidate.ProviderAttempts) >= MaxDeclarationProviderAttempts {
		return -1, errors.New("declaration provider attempt limit exceeded")
	}
	candidate.ProviderAttempts = append(candidate.ProviderAttempts, DeclarationProviderAttempt{
		Sequence: int64(len(candidate.ProviderAttempts) + 1), Provider: provider,
		Model: model, Phase: update.Phase, Section: update.Section,
		SourceTurnID: strings.TrimSpace(update.SourceTurnID), CandidateRevision: update.CandidateRevision,
		CandidateHash: strings.TrimSpace(update.CandidateHash), SDKAttemptOrdinal: update.SDKAttemptOrdinal,
		SDKRetryBudget: update.SDKRetryBudget, ObservedAt: now.UTC(),
	})
	return len(candidate.ProviderAttempts) - 1, nil
}

func declarationProviderAttemptOrdinalIsStale(attempts []DeclarationProviderAttempt, update DeclarationProviderAttemptUpdate) bool {
	for _, prior := range attempts {
		if prior.SourceTurnID == update.SourceTurnID && prior.Section == update.Section && prior.CandidateRevision == update.CandidateRevision &&
			prior.CandidateHash == update.CandidateHash && prior.SDKAttemptOrdinal >= update.SDKAttemptOrdinal {
			return true
		}
	}
	return false
}

func existingDeclarationProviderAttemptIndex(attempts []DeclarationProviderAttempt, update DeclarationProviderAttemptUpdate) (int, error) {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.SourceTurnID == strings.TrimSpace(update.SourceTurnID) && attempt.Section == update.Section &&
			attempt.CandidateRevision == update.CandidateRevision && attempt.CandidateHash == strings.TrimSpace(update.CandidateHash) {
			return index, nil
		}
	}
	return -1, errors.New("declaration provider attempt observation has no SDK attempt")
}

func applyDeclarationProviderAttemptMetadata(attempt *DeclarationProviderAttempt, update DeclarationProviderAttemptUpdate, now time.Time) {
	if update.HTTPStatus != 0 {
		attempt.HTTPStatus = update.HTTPStatus
	}
	if value := strings.TrimSpace(update.ProviderRequestID); value != "" {
		attempt.ProviderRequestID = value
	}
	if value := strings.TrimSpace(update.ToolName); value != "" {
		attempt.ToolName = value
		attempt.ToolCallHash = strings.TrimSpace(update.ToolCallHash)
		attempt.ValidationCodes = append([]DeclarationValidationCode(nil), update.ValidationCodes...)
		attempt.ValidationPaths = append([]string(nil), update.ValidationPaths...)
		attempt.Accepted = update.Accepted
	}
	attempt.OutputBytes = maxCandidateInt(attempt.OutputBytes, update.OutputBytes)
	if value := strings.TrimSpace(update.OutputSHA256); value != "" {
		attempt.OutputSHA256 = value
	}
	attempt.InputTokens = maxCandidateInt64(attempt.InputTokens, update.InputTokens)
	attempt.OutputTokens = maxCandidateInt64(attempt.OutputTokens, update.OutputTokens)
	attempt.TotalTokens = maxCandidateInt64(attempt.TotalTokens, update.TotalTokens)
	attempt.ToolCalls = maxCandidateInt64(attempt.ToolCalls, update.ToolCalls)
	if value := strings.TrimSpace(update.StopReason); value != "" {
		attempt.StopReason = value
	}
	if strings.TrimSpace(string(update.FailureClass)) != "" {
		attempt.FailureClass = NormalizeFailureClass(string(update.FailureClass))
	}
	attempt.DurationMS = maxCandidateInt64(attempt.DurationMS, update.DurationMS)
	attempt.ObservedAt = now.UTC()
}

func (c *DeclarationCandidate) resultFor(section DeclarationSection, issues ...DeclarationValidationIssue) DeclarationToolResult {
	return DeclarationToolResult{Section: section, Revision: c.Revision, CandidateHash: c.CandidateHash, Errors: issues}
}

func issue(section DeclarationSection, path string, code DeclarationValidationCode) DeclarationValidationIssue {
	if !isDeclarationValidationCode(code) {
		code = DeclarationCodeInvalid
	}
	return DeclarationValidationIssue{Section: section, Path: path, Code: code}
}

func issueForCode(section DeclarationSection, code DeclarationValidationCode) DeclarationValidationIssue {
	path := "fiveBodies." + string(section) + ".summary"
	switch code {
	case DeclarationCodeSoulRefusals, DeclarationCodeSoulRefusalsBad:
		path = "fiveBodies.soul.refusals"
	case DeclarationCodeSelfDescription:
		path = "selfDescription"
	case DeclarationCodeCapabilities, DeclarationCodeCapabilitiesBad, DeclarationCodeCapabilitiesTooMany,
		DeclarationCodeCapabilityIdentifier, DeclarationCodeCapabilityScope, DeclarationCodeCapabilityClaimLevel,
		DeclarationCodeCapabilityLastValidated, DeclarationCodeCapabilityValidationRef, DeclarationCodeCapabilityDegradesTo:
		path = "capabilities"
	case DeclarationCodeTransparency:
		path = "transparency"
	}
	return issue(section, path, code)
}

// declarationSectionForValidationCode keeps final cross-section validation
// actionable. Section-local validation normally prevents an earlier body from
// reaching this gate, but a guarded-state corruption must still report the
// authoritative section rather than incorrectly blaming the current tool.
func declarationSectionForValidationCode(fallback DeclarationSection, code DeclarationValidationCode) DeclarationSection {
	switch code {
	case DeclarationCodeFiveBodyIdentity:
		return DeclarationSectionIdentity
	case DeclarationCodeFiveBodyPhilosophy:
		return DeclarationSectionPhilosophy
	case DeclarationCodeFiveBodyDiscipline:
		return DeclarationSectionDiscipline
	case DeclarationCodeFiveBodyBoundaries:
		return DeclarationSectionBoundaries
	case DeclarationCodeFiveBodySoul, DeclarationCodeSoulRefusals, DeclarationCodeSoulRefusalsBad,
		DeclarationCodeSelfDescription, DeclarationCodeCapabilities, DeclarationCodeCapabilitiesBad,
		DeclarationCodeCapabilitiesTooMany, DeclarationCodeCapabilityIdentifier, DeclarationCodeCapabilityScope,
		DeclarationCodeCapabilityClaimLevel, DeclarationCodeCapabilityLastValidated,
		DeclarationCodeCapabilityValidationRef, DeclarationCodeCapabilityDegradesTo,
		DeclarationCodeTransparency, DeclarationCodeBoundaries, DeclarationCodeBoundariesBad:
		return DeclarationSectionSoul
	default:
		return fallback
	}
}

func validateDeclarationSection(section DeclarationSection, bodies FiveBodyDeclaration) error {
	switch section {
	case DeclarationSectionIdentity:
		if strings.TrimSpace(bodies.Identity.Summary) == "" {
			return NewDeclarationValidationError(DeclarationCodeFiveBodyIdentity)
		}
	case DeclarationSectionPhilosophy:
		if strings.TrimSpace(bodies.Philosophy.Summary) == "" {
			return NewDeclarationValidationError(DeclarationCodeFiveBodyPhilosophy)
		}
	case DeclarationSectionDiscipline:
		if strings.TrimSpace(bodies.Discipline.Summary) == "" {
			return NewDeclarationValidationError(DeclarationCodeFiveBodyDiscipline)
		}
	case DeclarationSectionBoundaries:
		if strings.TrimSpace(bodies.Boundaries.Summary) == "" {
			return NewDeclarationValidationError(DeclarationCodeFiveBodyBoundaries)
		}
	case DeclarationSectionSoul:
		if strings.TrimSpace(bodies.Soul.Summary) == "" {
			return NewDeclarationValidationError(DeclarationCodeFiveBodySoul)
		}
		return ValidateFiveBodyRefusals(bodies.Soul.Refusals)
	default:
		return NewDeclarationValidationError(DeclarationCodeInvalid)
	}
	return nil
}

func validateCandidateSoulSatellites(section DeclarationSection, capabilities []soul.CapabilityV2, description soul.SelfDescriptionV2, transparency DeclarationTransparency) ([]soul.CapabilityV2, []DeclarationValidationIssue) {
	var issues []DeclarationValidationIssue
	if err := description.Validate(); err != nil {
		issues = append(issues, issueForCode(section, DeclarationCodeSelfDescription))
	}
	normalized, err := ValidateAndNormalizeProducedCapabilities(capabilities)
	if err != nil {
		issues = append(issues, issueForCode(section, DeclarationValidationCodeFromError(err)))
	}
	if strings.TrimSpace(transparency.SelfDeclaredNotice) == "" {
		issues = append(issues, issueForCode(section, DeclarationCodeTransparency))
	}
	return normalized, issues
}

func normalizeCandidateSelfDescription(in soul.SelfDescriptionV2, bodies FiveBodyDeclaration, model string) soul.SelfDescriptionV2 {
	in.Purpose = firstNonEmptyCandidate(in.Purpose, bodies.Identity.Summary)
	in.Constraints = firstNonEmptyCandidate(in.Constraints, bodies.Boundaries.Summary)
	in.Commitments = firstNonEmptyCandidate(in.Commitments, bodies.Philosophy.Summary)
	in.Limitations = firstNonEmptyCandidate(in.Limitations, bodies.Soul.Summary)
	in.AuthoredBy = "agent"
	in.MintingModel = strings.TrimSpace(model)
	return in
}

func normalizeCandidateTransparency(in DeclarationTransparency) DeclarationTransparency {
	return DeclarationTransparency{
		ModelProviderUncertainty: trimRunes(strings.TrimSpace(in.ModelProviderUncertainty), 1200),
		OperationalNotes:         trimRunes(strings.TrimSpace(in.OperationalNotes), 1200),
		SelfDeclaredNotice:       trimRunes(strings.TrimSpace(in.SelfDeclaredNotice), 1200),
	}
}

func setCandidateSection(candidate *DeclarationCandidate, section DeclarationSection, value FiveBodySection) {
	switch section {
	case DeclarationSectionIdentity:
		candidate.FiveBodies.Identity = value
	case DeclarationSectionPhilosophy:
		candidate.FiveBodies.Philosophy = value
	case DeclarationSectionDiscipline:
		candidate.FiveBodies.Discipline = value
	case DeclarationSectionBoundaries:
		candidate.FiveBodies.Boundaries = value
	}
}

func (c *DeclarationCandidate) markCompleted(section DeclarationSection) {
	seen := false
	for _, completed := range c.CompletedSections {
		if completed == section {
			seen = true
			break
		}
	}
	if !seen {
		c.CompletedSections = append(c.CompletedSections, section)
	}
	sort.SliceStable(c.CompletedSections, func(i, j int) bool {
		return declarationSectionIndex(c.CompletedSections[i]) < declarationSectionIndex(c.CompletedSections[j])
	})
}

func (c *DeclarationCandidate) nextIncompleteSection() (DeclarationSection, bool) {
	completed := map[DeclarationSection]bool{}
	for _, section := range c.CompletedSections {
		completed[section] = true
	}
	for _, section := range declarationSectionOrder {
		if !completed[section] {
			return section, true
		}
	}
	return "", false
}

func declarationSectionIndex(section DeclarationSection) int {
	for i, candidate := range declarationSectionOrder {
		if candidate == section {
			return i
		}
	}
	return len(declarationSectionOrder)
}

type canonicalProducedDeclarations struct {
	SchemaVersion     string                  `json:"schemaVersion"`
	GuidanceVersion   string                  `json:"guidanceVersion"`
	FiveBodies        FiveBodyDeclaration     `json:"fiveBodies"`
	SelfDescription   soul.SelfDescriptionV2  `json:"selfDescription"`
	Capabilities      []soul.CapabilityV2     `json:"capabilities"`
	Boundaries        []soul.BoundaryV2       `json:"boundaries"`
	Transparency      DeclarationTransparency `json:"transparency"`
	AdversarialReview *AdversarialReview      `json:"adversarialReview,omitempty"`
}

func (c *DeclarationCandidate) refreshCanonical() error {
	if c == nil {
		return errors.New("declaration candidate is required")
	}
	doc := canonicalProducedDeclarations{
		SchemaVersion: c.SchemaVersion, GuidanceVersion: c.GuidanceVersion,
		FiveBodies: c.FiveBodies, SelfDescription: c.SelfDescription,
		Capabilities: append([]soul.CapabilityV2(nil), c.Capabilities...),
		Boundaries:   FiveBodyBoundariesDeterministic(c.FiveBodies.Soul.Refusals, c.EstablishedAt),
		Transparency: c.Transparency,
	}
	if len(c.CompletedSections) == len(declarationSectionOrder) {
		review, err := BuildAdversarialReviewV2(c.FiveBodies)
		if err != nil {
			return err
		}
		doc.AdversarialReview = &review
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	c.CanonicalJSON = string(body)
	c.CandidateHash = hashBytes(body)
	return nil
}

// FiveBodyBoundariesDeterministic derives stable publication rows from the
// accepted refusal bytes and the candidate's persisted establishment time.
func FiveBodyBoundariesDeterministic(refusals []FiveBodyRefusalRule, establishedAt time.Time) []soul.BoundaryV2 {
	refusals = NormalizeFiveBodyDeclaration(FiveBodyDeclaration{Soul: FiveBodySoulBody{Refusals: refusals}}).Soul.Refusals
	out := make([]soul.BoundaryV2, 0, len(refusals))
	for i, refusal := range refusals {
		seed, _ := json.Marshal(refusal)
		digest := sha256.Sum256(seed)
		statement := fmt.Sprintf("I will not allow bypass %q because it violates %q; closest safe path: %s.", refusal.Bypass, refusal.Invariant, refusal.ClosestSafePath)
		out = append(out, soul.BoundaryV2{
			ID: fmt.Sprintf("mint-refusal-%02d-%s", i+1, hex.EncodeToString(digest[:6])), Category: "refusal",
			Statement: statement, Rationale: fmt.Sprintf("Five-body soul refusal: invariant=%s", refusal.Invariant),
			AddedAt: establishedAt.UTC().Format(time.RFC3339), AddedInVersion: "1", Signature: "0x00",
		})
	}
	return out
}

func ValidateDeclarationCandidateComplete(candidate *DeclarationCandidate) error {
	if candidate == nil || len(candidate.CompletedSections) != len(declarationSectionOrder) {
		return NewDeclarationValidationError(DeclarationCodeInvalid)
	}
	if err := ValidateFiveBodyDeclaration(candidate.FiveBodies); err != nil {
		return err
	}
	if err := candidate.SelfDescription.Validate(); err != nil {
		return NewDeclarationValidationError(DeclarationCodeSelfDescription)
	}
	if _, err := ValidateAndNormalizeProducedCapabilities(candidate.Capabilities); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.Transparency.SelfDeclaredNotice) == "" {
		return NewDeclarationValidationError(DeclarationCodeTransparency)
	}
	boundaries := FiveBodyBoundariesDeterministic(candidate.FiveBodies.Soul.Refusals, candidate.EstablishedAt)
	if len(boundaries) < 3 {
		return NewDeclarationValidationError(DeclarationCodeBoundaries)
	}
	for i := range boundaries {
		if err := boundaries[i].Validate(); err != nil {
			return NewDeclarationValidationError(DeclarationCodeBoundariesBad)
		}
	}
	return nil
}

func RenderDeclarationOwnerReview(candidate *DeclarationCandidate, sourceTurnID string, reviewedAt time.Time) (DeclarationReviewCheckpoint, error) {
	if err := ValidateDeclarationCandidateComplete(candidate); err != nil {
		return DeclarationReviewCheckpoint{}, err
	}
	var b strings.Builder
	b.WriteString("Hosted Genesis owner review\n\n")
	b.WriteString("Review the exact canonical JSON below. Structural affirmation binds this review text, these canonical bytes, and the candidate revision.\n\n")
	fmt.Fprintf(&b, "Candidate revision: %d\nCandidate hash: %s\n", candidate.Revision, candidate.CandidateHash)
	fmt.Fprintf(&b, "%s%d\n%s\n", declarationOwnerReviewCanonicalLength, len(candidate.CanonicalJSON), declarationOwnerReviewCanonicalBegin)
	b.WriteString(candidate.CanonicalJSON)
	fmt.Fprintf(&b, "\n%s\n", declarationOwnerReviewCanonicalEnd)
	text := b.String()
	if utf8.RuneCountInString(text) > MaxDeclarationOwnerReviewRunes {
		return DeclarationReviewCheckpoint{}, errDeclarationOwnerReviewTooLarge
	}
	return DeclarationReviewCheckpoint{
		RendererVersion: DeclarationReviewRendererV1, SchemaVersion: candidate.SchemaVersion,
		GuidanceVersion: candidate.GuidanceVersion, SourceTurnID: strings.TrimSpace(sourceTurnID),
		CandidateHash: candidate.CandidateHash, ReviewHash: hashText(text), CandidateRevision: candidate.Revision,
		ReviewText: text, ReviewedAt: reviewedAt.UTC(),
	}, nil
}

// RecoverDeclarationOwnerReviewCanonicalJSON reverses the deterministic owner
// review envelope to the exact CanonicalJSON byte string authenticated by
// CandidateHash. The byte-counted block is authoritative; no provider renders,
// interprets, or reconstructs declaration content during review or recovery.
func RecoverDeclarationOwnerReviewCanonicalJSON(reviewText string) (string, error) {
	if !strings.HasPrefix(reviewText, "Hosted Genesis owner review\n\n") || utf8.RuneCountInString(reviewText) > MaxDeclarationOwnerReviewRunes {
		return "", errors.New("declaration owner review envelope is invalid")
	}
	beginToken := "\n" + declarationOwnerReviewCanonicalBegin + "\n"
	beginIndex := strings.Index(reviewText, beginToken)
	if beginIndex < 0 {
		return "", errors.New("declaration owner review canonical block is missing")
	}
	header := reviewText[:beginIndex]
	lengthIndex := strings.LastIndex(header, declarationOwnerReviewCanonicalLength)
	if lengthIndex < 0 {
		return "", errors.New("declaration owner review canonical length is missing")
	}
	lengthText := header[lengthIndex+len(declarationOwnerReviewCanonicalLength):]
	if strings.Contains(lengthText, "\n") || lengthText == "" {
		return "", errors.New("declaration owner review canonical length is invalid")
	}
	canonicalLength, err := strconv.Atoi(lengthText)
	if err != nil || canonicalLength <= 0 || canonicalLength > len(reviewText) {
		return "", errors.New("declaration owner review canonical length is invalid")
	}
	canonicalStart := beginIndex + len(beginToken)
	canonicalEnd := canonicalStart + canonicalLength
	if canonicalEnd > len(reviewText) {
		return "", errors.New("declaration owner review canonical payload is truncated")
	}
	canonicalJSON := reviewText[canonicalStart:canonicalEnd]
	if reviewText[canonicalEnd:] != "\n"+declarationOwnerReviewCanonicalEnd+"\n" || !json.Valid([]byte(canonicalJSON)) {
		return "", errors.New("declaration owner review canonical payload is invalid")
	}
	return canonicalJSON, nil
}

type DeclarationCandidateAction struct {
	Action            string             `json:"action"`
	Section           DeclarationSection `json:"section,omitempty"`
	CandidateRevision int64              `json:"candidate_revision"`
	CandidateHash     string             `json:"candidate_hash"`
	ReviewHash        string             `json:"review_hash,omitempty"`
}

func ApplyDeclarationCandidateAction(candidate *DeclarationCandidate, action DeclarationCandidateAction, sourceTurnID string, now time.Time) (*DeclarationCandidate, error) {
	if candidate == nil || candidate.Revision != action.CandidateRevision || candidate.CandidateHash != strings.TrimSpace(action.CandidateHash) {
		return nil, errors.New("declaration candidate action binding mismatch")
	}
	updated := candidate.Clone()
	switch strings.ToLower(strings.TrimSpace(action.Action)) {
	case "edit":
		if err := applyDeclarationCandidateEdit(updated, action, sourceTurnID, now); err != nil {
			return nil, err
		}
	case "affirm":
		if err := applyDeclarationCandidateAffirmation(updated, action, sourceTurnID, now); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("declaration candidate action is invalid")
	}
	if err := updated.Validate(); err != nil {
		return nil, err
	}
	return updated, nil
}

func applyDeclarationCandidateEdit(candidate *DeclarationCandidate, action DeclarationCandidateAction, sourceTurnID string, now time.Time) error {
	if !declarationReviewMatchesAction(candidate, action) {
		return errors.New("declaration edit does not match the reviewed candidate")
	}
	if _, ok := DeclarationToolForSection(action.Section); !ok {
		return errors.New("declaration candidate edit section is invalid")
	}
	candidate.Revision++
	candidate.Phase = DeclarationCandidatePhaseSection
	candidate.CurrentSection = action.Section
	candidate.Review = nil
	candidate.Affirmation = nil
	candidate.SourceTurnID = strings.TrimSpace(sourceTurnID)
	candidate.UpdatedAt = now.UTC()
	return nil
}

func applyDeclarationCandidateAffirmation(candidate *DeclarationCandidate, action DeclarationCandidateAction, sourceTurnID string, now time.Time) error {
	if !declarationReviewMatchesAction(candidate, action) {
		return errors.New("declaration affirmation does not match the reviewed candidate")
	}
	candidate.Affirmation = &DeclarationAffirmationCheckpoint{
		CandidateRevision: candidate.Revision, CandidateHash: candidate.CandidateHash,
		ReviewHash: candidate.Review.ReviewHash, SourceTurnID: strings.TrimSpace(sourceTurnID), AffirmedAt: now.UTC(),
	}
	candidate.Phase = DeclarationCandidatePhaseAffirmed
	candidate.SourceTurnID = strings.TrimSpace(sourceTurnID)
	candidate.UpdatedAt = now.UTC()
	return nil
}

func declarationReviewMatchesAction(candidate *DeclarationCandidate, action DeclarationCandidateAction) bool {
	return candidate.Phase == DeclarationCandidatePhaseReview && candidate.Review != nil &&
		candidate.Review.CandidateRevision == action.CandidateRevision &&
		candidate.Review.CandidateHash == strings.TrimSpace(action.CandidateHash) &&
		candidate.Review.ReviewHash == strings.TrimSpace(action.ReviewHash)
}

// FinalizeDeclarationCandidate performs the provider-free terminal transition.
// It does not render or rewrite declaration bytes: CanonicalJSON and
// CandidateHash remain exactly the values structurally affirmed by the owner.
func FinalizeDeclarationCandidate(candidate *DeclarationCandidate, sourceTurnID string, now time.Time) (*DeclarationCandidate, error) {
	if candidate == nil || candidate.Phase != DeclarationCandidatePhaseAffirmed || candidate.Review == nil || candidate.Affirmation == nil {
		return nil, errors.New("declaration candidate is not affirmed")
	}
	if strings.TrimSpace(sourceTurnID) == "" || candidate.Affirmation.SourceTurnID != strings.TrimSpace(sourceTurnID) {
		return nil, errors.New("declaration finalization turn does not match affirmation")
	}
	if err := ValidateDeclarationCandidateComplete(candidate); err != nil {
		return nil, err
	}
	updated := candidate.Clone()
	updated.Phase = DeclarationCandidatePhaseFinalized
	updated.UpdatedAt = now.UTC()
	if err := updated.Validate(); err != nil {
		return nil, err
	}
	if updated.CanonicalJSON != candidate.CanonicalJSON || updated.CandidateHash != candidate.CandidateHash {
		return nil, errors.New("declaration finalization changed canonical bytes")
	}
	return updated, nil
}

func (c *DeclarationCandidate) Validate() error {
	for _, validate := range []func(*DeclarationCandidate) error{
		validateDeclarationCandidateCore,
		validateDeclarationCandidateCompletedSections,
		validateDeclarationCandidatePhase,
		validateDeclarationCandidateToolRecords,
		validateDeclarationCandidateProviderAttempts,
		validateDeclarationCandidateCanonicalBindings,
	} {
		if err := validate(c); err != nil {
			return err
		}
	}
	return nil
}

func validateDeclarationCandidateCore(c *DeclarationCandidate) error {
	if c == nil || c.Version != DeclarationCandidateVersionV1 || c.SchemaVersion != DeclarationSchemaVersionV2 || c.GuidanceVersion != GuidanceVersionV2 {
		return errors.New("declaration candidate version is invalid")
	}
	if !declarationCandidateBindingsComplete(c) {
		return errors.New("declaration candidate binding is incomplete")
	}
	if c.Revision < 0 || len(c.ToolRecords) > MaxDeclarationToolRecords || len(c.ProviderAttempts) > MaxDeclarationProviderAttempts {
		return errors.New("declaration candidate revision is invalid")
	}
	return nil
}

func declarationCandidateBindingsComplete(c *DeclarationCandidate) bool {
	return c.InstanceSlug != "" && c.RegistrationID != "" && c.AgentID != "" && c.ConversationID != "" &&
		c.SourceTurnID != "" && c.Model != "" && !c.EstablishedAt.IsZero() && !c.UpdatedAt.IsZero()
}

func validateDeclarationCandidateCompletedSections(c *DeclarationCandidate) error {
	completed := make(map[DeclarationSection]bool, len(c.CompletedSections))
	for index, section := range c.CompletedSections {
		if declarationSectionIndex(section) == len(declarationSectionOrder) || completed[section] {
			return errors.New("declaration candidate completed sections are invalid")
		}
		completed[section] = true
		if len(c.CompletedSections) != len(declarationSectionOrder) && section != declarationSectionOrder[index] {
			return errors.New("declaration candidate completed sections are out of order")
		}
		sectionBytes, err := c.sectionBytes(section)
		if err != nil || c.SectionHashes[string(section)] != hashBytes(sectionBytes) {
			return errors.New("declaration candidate section hash mismatch")
		}
	}
	return nil
}

func validateDeclarationCandidatePhase(c *DeclarationCandidate) error {
	switch c.Phase {
	case DeclarationCandidatePhaseSection:
		return validateDeclarationCandidateSectionPhase(c)
	case DeclarationCandidatePhaseReview:
		return validateDeclarationCandidateReviewPhase(c)
	case DeclarationCandidatePhaseAffirmed, DeclarationCandidatePhaseFinalized:
		return validateDeclarationCandidateAffirmedPhase(c)
	default:
		return errors.New("declaration candidate phase is invalid")
	}
}

func validateDeclarationCandidateSectionPhase(c *DeclarationCandidate) error {
	if _, ok := DeclarationToolForSection(c.CurrentSection); !ok || c.Review != nil || c.Affirmation != nil {
		return errors.New("declaration candidate section phase is invalid")
	}
	if len(c.CompletedSections) == len(declarationSectionOrder) {
		return nil
	}
	if expected, ok := c.nextIncompleteSection(); !ok || expected != c.CurrentSection {
		return errors.New("declaration candidate current section is invalid")
	}
	return nil
}

func validateDeclarationCandidateReviewPhase(c *DeclarationCandidate) error {
	if c.CurrentSection != "" || len(c.CompletedSections) != len(declarationSectionOrder) || c.Review == nil || c.Affirmation != nil {
		return errors.New("declaration candidate review phase is invalid")
	}
	return nil
}

func validateDeclarationCandidateAffirmedPhase(c *DeclarationCandidate) error {
	if c.CurrentSection != "" || len(c.CompletedSections) != len(declarationSectionOrder) || c.Review == nil || c.Affirmation == nil {
		return errors.New("declaration candidate affirmed phase is invalid")
	}
	return nil
}

func validateDeclarationCandidateToolRecords(c *DeclarationCandidate) error {
	seenCalls := make(map[string]bool, len(c.ToolRecords))
	for _, record := range c.ToolRecords {
		if record.ToolCallHash == "" || record.InputHash == "" || record.SourceTurnID == "" || record.Revision <= 0 || record.Revision > c.Revision ||
			record.SectionHash == "" || record.CandidateHash == "" || seenCalls[record.ToolCallHash] {
			return errors.New("declaration candidate tool record is invalid")
		}
		section, ok := DeclarationSectionForTool(record.ToolName)
		if !ok || section != record.Section {
			return errors.New("declaration candidate tool binding is invalid")
		}
		seenCalls[record.ToolCallHash] = true
	}
	return nil
}

func validateDeclarationCandidateProviderAttempts(c *DeclarationCandidate) error {
	for index, attempt := range c.ProviderAttempts {
		if err := validateDeclarationProviderAttempt(attempt, int64(index+1)); err != nil {
			return err
		}
	}
	return nil
}

func validateDeclarationProviderAttempt(attempt DeclarationProviderAttempt, expectedSequence int64) error {
	if !declarationProviderAttemptBindingValid(attempt, expectedSequence) || !declarationProviderAttemptMetricsValid(attempt) {
		return errors.New("declaration provider attempt is invalid")
	}
	if _, ok := DeclarationToolForSection(attempt.Section); !ok || !safeCandidateMetadata(attempt.ProviderRequestID, 160) || !safeCandidateMetadata(attempt.StopReason, 80) {
		return errors.New("declaration provider attempt metadata is unsafe")
	}
	if attempt.OutputSHA256 != "" && !isSHA256HexDigest(attempt.OutputSHA256) {
		return errors.New("declaration provider output hash is invalid")
	}
	if err := validateDeclarationProviderToolEvidence(attempt); err != nil {
		return err
	}
	if attempt.FailureClass != "" && NormalizeFailureClass(string(attempt.FailureClass)) != attempt.FailureClass {
		return errors.New("declaration provider failure class is invalid")
	}
	return nil
}

func declarationProviderAttemptBindingValid(attempt DeclarationProviderAttempt, expectedSequence int64) bool {
	return attempt.Sequence == expectedSequence && attempt.Provider != "" && attempt.Model != "" &&
		attempt.Phase == "declaration_phase" && attempt.SourceTurnID != "" && attempt.CandidateRevision >= 0 &&
		isSHA256Digest(attempt.CandidateHash) && attempt.SDKAttemptOrdinal > 0 && attempt.SDKRetryBudget >= 0 && attempt.SDKRetryBudget <= 8
}

func declarationProviderAttemptMetricsValid(attempt DeclarationProviderAttempt) bool {
	statusValid := attempt.HTTPStatus == 0 || (attempt.HTTPStatus >= 100 && attempt.HTTPStatus <= 599)
	return statusValid && attempt.DurationMS >= 0 && !attempt.ObservedAt.IsZero() && attempt.OutputBytes >= 0 &&
		attempt.InputTokens >= 0 && attempt.OutputTokens >= 0 && attempt.TotalTokens >= 0 && attempt.ToolCalls >= 0
}

func validateDeclarationProviderToolEvidence(attempt DeclarationProviderAttempt) error {
	if attempt.ToolName == "" {
		return nil
	}
	section, ok := DeclarationSectionForTool(attempt.ToolName)
	if !ok || section != attempt.Section || !isSHA256Digest(attempt.ToolCallHash) || len(attempt.ValidationCodes) != len(attempt.ValidationPaths) || len(attempt.ValidationCodes) > 16 {
		return errors.New("declaration provider tool evidence is invalid")
	}
	for index, code := range attempt.ValidationCodes {
		if !isDeclarationValidationCode(code) || !safeCandidateMetadata(attempt.ValidationPaths[index], 256) {
			return errors.New("declaration provider validation evidence is invalid")
		}
	}
	return nil
}

func validateDeclarationCandidateCanonicalBindings(c *DeclarationCandidate) error {
	copy := c.Clone()
	if err := copy.refreshCanonical(); err != nil {
		return err
	}
	if copy.CandidateHash != c.CandidateHash || copy.CanonicalJSON != c.CanonicalJSON {
		return errors.New("declaration candidate canonical hash mismatch")
	}
	if err := validateDeclarationReviewBinding(c); err != nil {
		return err
	}
	if c.Affirmation != nil {
		if c.Review == nil || c.Affirmation.SourceTurnID == "" || c.Affirmation.AffirmedAt.IsZero() || c.Affirmation.CandidateRevision != c.Revision || c.Affirmation.CandidateHash != c.CandidateHash || c.Affirmation.ReviewHash != c.Review.ReviewHash {
			return errors.New("declaration affirmation binding mismatch")
		}
	}
	return nil
}

func validateDeclarationReviewBinding(c *DeclarationCandidate) error {
	if c.Review == nil {
		return nil
	}
	if c.Review.RendererVersion != DeclarationReviewRendererV1 || c.Review.SchemaVersion != c.SchemaVersion || c.Review.GuidanceVersion != c.GuidanceVersion ||
		c.Review.SourceTurnID == "" || c.Review.ReviewedAt.IsZero() || c.Review.CandidateRevision != c.Revision || c.Review.CandidateHash != c.CandidateHash || c.Review.ReviewHash != hashText(c.Review.ReviewText) {
		return errors.New("declaration review binding mismatch")
	}
	reviewCanonicalJSON, err := RecoverDeclarationOwnerReviewCanonicalJSON(c.Review.ReviewText)
	if err != nil || reviewCanonicalJSON != c.CanonicalJSON {
		return errors.New("declaration review canonical payload mismatch")
	}
	rendered, err := RenderDeclarationOwnerReview(c, c.Review.SourceTurnID, c.Review.ReviewedAt)
	if err != nil || rendered.ReviewText != c.Review.ReviewText || rendered.ReviewHash != c.Review.ReviewHash {
		return errors.New("declaration review rendering mismatch")
	}
	return nil
}

func (c *DeclarationCandidate) Clone() *DeclarationCandidate {
	if c == nil {
		return nil
	}
	copy := *c
	copy.CompletedSections = append([]DeclarationSection(nil), c.CompletedSections...)
	if c.Capabilities != nil {
		copy.Capabilities = make([]soul.CapabilityV2, len(c.Capabilities))
		for i := range c.Capabilities {
			copy.Capabilities[i] = c.Capabilities[i]
		}
	}
	for i := range copy.Capabilities {
		if c.Capabilities[i].Constraints != nil {
			copy.Capabilities[i].Constraints = make(map[string]any, len(c.Capabilities[i].Constraints))
			for key, value := range c.Capabilities[i].Constraints {
				copy.Capabilities[i].Constraints[key] = value
			}
		}
	}
	copy.FiveBodies.Identity.Notes = append([]string(nil), c.FiveBodies.Identity.Notes...)
	copy.FiveBodies.Philosophy.Notes = append([]string(nil), c.FiveBodies.Philosophy.Notes...)
	copy.FiveBodies.Discipline.Notes = append([]string(nil), c.FiveBodies.Discipline.Notes...)
	copy.FiveBodies.Boundaries.Notes = append([]string(nil), c.FiveBodies.Boundaries.Notes...)
	copy.FiveBodies.Soul.Notes = append([]string(nil), c.FiveBodies.Soul.Notes...)
	copy.FiveBodies.Soul.Refusals = append([]FiveBodyRefusalRule(nil), c.FiveBodies.Soul.Refusals...)
	copy.SectionHashes = make(map[string]string, len(c.SectionHashes))
	for key, value := range c.SectionHashes {
		copy.SectionHashes[key] = value
	}
	copy.ToolRecords = append([]DeclarationToolRecord(nil), c.ToolRecords...)
	copy.ProviderAttempts = make([]DeclarationProviderAttempt, len(c.ProviderAttempts))
	for index := range c.ProviderAttempts {
		copy.ProviderAttempts[index] = c.ProviderAttempts[index]
		copy.ProviderAttempts[index].ValidationCodes = append([]DeclarationValidationCode(nil), c.ProviderAttempts[index].ValidationCodes...)
		copy.ProviderAttempts[index].ValidationPaths = append([]string(nil), c.ProviderAttempts[index].ValidationPaths...)
	}
	if c.Review != nil {
		review := *c.Review
		copy.Review = &review
	}
	if c.Affirmation != nil {
		affirmation := *c.Affirmation
		copy.Affirmation = &affirmation
	}
	return &copy
}

func (c *DeclarationCandidate) sectionBytes(section DeclarationSection) ([]byte, error) {
	switch section {
	case DeclarationSectionIdentity:
		return json.Marshal(c.FiveBodies.Identity)
	case DeclarationSectionPhilosophy:
		return json.Marshal(c.FiveBodies.Philosophy)
	case DeclarationSectionDiscipline:
		return json.Marshal(c.FiveBodies.Discipline)
	case DeclarationSectionBoundaries:
		return json.Marshal(c.FiveBodies.Boundaries)
	case DeclarationSectionSoul:
		return json.Marshal(declarationSoulPayload{Section: c.FiveBodies.Soul, SelfDescription: c.SelfDescription, Capabilities: c.Capabilities, Transparency: c.Transparency})
	default:
		return nil, errors.New("declaration section is invalid")
	}
}

func hashBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func hashText(value string) string { return hashBytes([]byte(value)) }

func firstNonEmptyCandidate(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func safeCandidateMetadata(value string, limit int) bool {
	if value == "" {
		return true
	}
	if value != strings.TrimSpace(value) || len(value) > limit {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func maxCandidateInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxCandidateInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
