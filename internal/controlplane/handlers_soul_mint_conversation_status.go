package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const hostedGenesisConversationVersion = "1"

const (
	hostedGenesisTranscriptMaxMessages     = 64
	hostedGenesisTranscriptMaxContentRunes = 8192
	hostedGenesisTranscriptRoleUser        = "user"
	hostedGenesisTranscriptRoleAssistant   = "assistant"
	hostedGenesisTranscriptRedactedContent = "[redacted: sensitive content]"
)

const (
	hostedGenesisFailureLLMUnavailable              = "llm_unavailable"
	hostedGenesisFailureAssistantTurnFailed         = "assistant_turn_failed"
	hostedGenesisFailureInvalidCompletionState      = "invalid_completion_state"
	hostedGenesisFailureMissingProducedDeclarations = "missing_produced_declarations"
	hostedGenesisFailureInvalidProducedDeclarations = "invalid_produced_declarations"
	hostedGenesisFailureTenantBoundaryViolation     = "tenant_boundary_violation"
	hostedGenesisFailureOperatorActionRequired      = "operator_action_required"
	hostedGenesisFailureMicroVMUnavailable          = "microvm_unavailable"
	hostedGenesisRecoveryRefreshState               = "refresh_state"
	hostedGenesisRecoveryRetrySameStep              = "retry_same_step"
	hostedGenesisRecoveryRestartSoulBootstrap       = "restart_soul_bootstrap"
	hostedGenesisRecoveryOperatorAction             = "operator_action"
	hostedGenesisDefaultPollAfterSeconds            = 2
)

type hostedGenesisConversationResponse struct {
	Version      string                              `json:"version"`
	RequestID    string                              `json:"request_id"`
	Conversation hostedGenesisConversationProjection `json:"conversation"`
}

type hostedGenesisConversationProjection struct {
	RegistrationID       string                             `json:"registration_id"`
	ConversationID       string                             `json:"conversation_id"`
	AgentID              string                             `json:"agent_id"`
	Status               string                             `json:"status"`
	LatestTurnID         string                             `json:"latest_turn_id,omitempty"`
	MessageCount         int                                `json:"message_count"`
	Messages             []hostedGenesisConversationMessage `json:"messages,omitempty"`
	MessagesTruncated    bool                               `json:"messages_truncated,omitempty"`
	MessagesRedacted     bool                               `json:"messages_redacted,omitempty"`
	ProducedDeclarations *hostedGenesisProducedDeclarations `json:"produced_declarations,omitempty"`
	DeclarationCandidate *hostedGenesisCandidateProjection  `json:"declaration_candidate,omitempty"`
	Failure              *hostedGenesisFailure              `json:"failure,omitempty"`
	PublishedVersion     int                                `json:"published_version,omitempty"`
	PublishedAt          *time.Time                         `json:"published_at,omitempty"`
	RequestID            string                             `json:"request_id"`
	TraceIDs             *hostedGenesisTraceIDs             `json:"trace_ids,omitempty"`
	PollAfterSeconds     int                                `json:"poll_after_seconds,omitempty"`
	CreatedAt            *time.Time                         `json:"created_at,omitempty"`
	UpdatedAt            *time.Time                         `json:"updated_at,omitempty"`
	CompletedAt          *time.Time                         `json:"completed_at,omitempty"`
}

type hostedGenesisCandidateProjection struct {
	Version           string                                  `json:"version"`
	Phase             hostedgenesis.DeclarationCandidatePhase `json:"phase"`
	CurrentSection    hostedgenesis.DeclarationSection        `json:"current_section,omitempty"`
	CompletedSections []hostedgenesis.DeclarationSection      `json:"completed_sections,omitempty"`
	Revision          int64                                   `json:"revision"`
	CandidateHash     string                                  `json:"candidate_hash"`
	Review            *hostedGenesisCandidateReview           `json:"review,omitempty"`
}

type hostedGenesisCandidateReview struct {
	RendererVersion   string `json:"renderer_version"`
	CandidateRevision int64  `json:"candidate_revision"`
	CandidateHash     string `json:"candidate_hash"`
	ReviewHash        string `json:"review_hash"`
	ReviewText        string `json:"review_text"`
}

type hostedGenesisConversationMessage struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Order     int        `json:"order"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Redacted  bool       `json:"redacted,omitempty"`
}

type hostedGenesisProducedDeclarations struct {
	DeclarationID   string                                   `json:"declaration_id"`
	DeclarationHash string                                   `json:"declaration_hash"`
	ProducedAt      string                                   `json:"produced_at"`
	Declarations    soulMintConversationProducedDeclarations `json:"declarations"`
	Evidence        hostedGenesisDeclarationEvidence         `json:"evidence"`
}

type hostedGenesisDeclarationEvidence struct {
	Source         string `json:"source"`
	RegistrationID string `json:"registration_id"`
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
	MessageCount   int    `json:"message_count"`
	Model          string `json:"model,omitempty"`
	RequestID      string `json:"request_id"`
}

type hostedGenesisFailure struct {
	Code      string                       `json:"code"`
	Class     string                       `json:"class,omitempty"`
	Message   string                       `json:"message"`
	Retryable bool                         `json:"retryable"`
	Recovery  hostedGenesisFailureRecovery `json:"recovery"`
}

type hostedGenesisFailureRecovery struct {
	Action            string `json:"action"`
	MaxAttempts       int    `json:"max_attempts,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

type hostedGenesisTraceIDs struct {
	HostRequestID   string `json:"host_request_id,omitempty"`
	CorrelationID   string `json:"correlation_id,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
	LesserRequestID string `json:"lesser_request_id,omitempty"`
}

type mintConversationStatusProjection struct {
	ConversationID              string
	Status                      string
	Reason                      string
	ProducedDeclarationsPresent bool
	ProducedDeclarationsValid   bool
	RequestID                   string
	MessageCount                int
}

type hostedGenesisProjectionOptions struct {
	RegistrationID  string
	RequestID       string
	CollapseCreated bool
	CorrelationID   string
	IdempotencyKey  string
	LesserRequestID string
}

func mintConversationStatusProjectionFromModel(conv *models.SoulAgentMintConversation, collapseCreated bool) mintConversationStatusProjection {
	if conv == nil {
		return mintConversationStatusProjection{Status: models.SoulMintConversationStatusFailed, Reason: hostedGenesisFailureInvalidCompletionState}
	}
	decodeMintConversationFields(conv)
	present, valid := mintConversationProducedDeclarationsState(conv)
	status := strings.ToLower(strings.TrimSpace(conv.Status))
	reason := strings.TrimSpace(conv.StatusReason)
	if reason == "" {
		switch status {
		case models.SoulMintConversationStatusCompleted, models.SoulMintConversationStatusDeclarationReady:
			if !present {
				reason = hostedGenesisFailureMissingProducedDeclarations
			} else if !valid {
				reason = hostedGenesisFailureInvalidProducedDeclarations
			}
		}
	}

	switch status {
	case models.SoulMintConversationStatusCompleted, models.SoulMintConversationStatusDeclarationReady:
		if valid {
			status = models.SoulMintConversationStatusDeclarationReady
		} else {
			status = models.SoulMintConversationStatusFailed
			if reason == "" {
				reason = hostedGenesisFailureInvalidProducedDeclarations
			}
		}
	case models.SoulMintConversationStatusCreated:
		if collapseCreated {
			status = models.SoulMintConversationStatusInProgress
		}
	case models.SoulMintConversationStatusInProgress,
		models.SoulMintConversationStatusAssistantTurnReady,
		models.SoulMintConversationStatusFailed:
		// locked status; no rewrite.
	default:
		status = models.SoulMintConversationStatusFailed
		if reason == "" {
			reason = hostedGenesisFailureInvalidCompletionState
		}
	}
	if status == models.SoulMintConversationStatusFailed && reason == "" {
		reason = hostedGenesisFailureAssistantTurnFailed
	}

	return mintConversationStatusProjection{
		ConversationID:              strings.TrimSpace(conv.ConversationID),
		Status:                      status,
		Reason:                      reason,
		ProducedDeclarationsPresent: present,
		ProducedDeclarationsValid:   valid,
		RequestID:                   strings.TrimSpace(conv.RequestID),
		MessageCount:                mintConversationMessageCount(conv),
	}
}

func buildHostedGenesisConversationResponse(conv *models.SoulAgentMintConversation, opts hostedGenesisProjectionOptions) hostedGenesisConversationResponse {
	if conv == nil {
		return hostedGenesisConversationResponse{Version: hostedGenesisConversationVersion, RequestID: strings.TrimSpace(opts.RequestID)}
	}
	decodeMintConversationFields(conv)
	status := mintConversationStatusProjectionFromModel(conv, opts.CollapseCreated)
	requestID := firstNonEmpty(opts.RequestID, status.RequestID, conv.RequestID)
	trace := hostedGenesisTraceIDs{
		HostRequestID:   requestID,
		CorrelationID:   firstNonEmpty(opts.CorrelationID, conv.CorrelationID),
		IdempotencyKey:  firstNonEmpty(opts.IdempotencyKey, conv.IdempotencyKey),
		LesserRequestID: strings.TrimSpace(opts.LesserRequestID),
	}
	projection := hostedGenesisConversationProjection{
		RegistrationID: strings.TrimSpace(opts.RegistrationID),
		ConversationID: strings.TrimSpace(conv.ConversationID),
		AgentID:        strings.ToLower(strings.TrimSpace(conv.AgentID)),
		Status:         status.Status,
		LatestTurnID:   strings.TrimSpace(conv.LatestTurnID),
		MessageCount:   status.MessageCount,
		RequestID:      requestID,
		TraceIDs:       nilIfEmptyTrace(trace),
		CreatedAt:      timePtrIfSet(conv.CreatedAt),
		UpdatedAt:      timePtrIfSet(firstTime(conv.UpdatedAt, conv.CompletedAt, conv.CreatedAt)),
		CompletedAt:    timePtrIfSet(conv.CompletedAt),
	}
	if isHostedGenesisProgressStatus(status.Status) {
		projection.PollAfterSeconds = hostedGenesisDefaultPollAfterSeconds
	}
	if status.Status == models.SoulMintConversationStatusDeclarationReady && status.ProducedDeclarationsValid {
		if produced := buildHostedGenesisProducedDeclarations(conv, strings.TrimSpace(opts.RegistrationID), requestID, status.MessageCount); produced != nil {
			projection.ProducedDeclarations = produced
		}
	}
	if status.Status == models.SoulMintConversationStatusFailed {
		projection.Failure = hostedGenesisFailureFromReason(status.Reason)
	}
	return hostedGenesisConversationResponse{
		Version:      hostedGenesisConversationVersion,
		RequestID:    requestID,
		Conversation: projection,
	}
}

func buildHostedGenesisConversationMessages(session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) ([]hostedGenesisConversationMessage, bool, bool) {
	if session == nil || conv == nil || !hostedGenesisConversationMatchesSession(session, conv) {
		return nil, false, false
	}
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conv.Messages))
	if raw == "" {
		return nil, false, false
	}

	var messages []soulMintConversationMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil || len(messages) == 0 {
		return nil, false, false
	}

	start := 0
	bounded := false
	if len(messages) > hostedGenesisTranscriptMaxMessages {
		start = len(messages) - hostedGenesisTranscriptMaxMessages
		bounded = true
	}
	out := make([]hostedGenesisConversationMessage, 0, len(messages)-start)
	redacted := false
	for i := start; i < len(messages); i++ {
		role := hostedGenesisTranscriptRole(messages[i].Role)
		content := strings.TrimSpace(messages[i].Content)
		if role == "" || content == "" {
			continue
		}
		messageRedacted := hostedGenesisTranscriptContentUnsafe(content)
		if messageRedacted {
			content = hostedGenesisTranscriptRedactedContent
			redacted = true
		}
		content, truncated := hostedGenesisTranscriptBoundContent(content)
		if truncated {
			bounded = true
		}
		order := i + 1
		out = append(out, hostedGenesisConversationMessage{
			ID:        hostedGenesisTranscriptMessageID(order),
			Role:      role,
			Content:   content,
			Order:     order,
			CreatedAt: hostedGenesisTranscriptMessageCreatedAt(session, role, order),
			Truncated: truncated,
			Redacted:  messageRedacted,
		})
	}
	if len(out) == 0 {
		return nil, bounded, redacted
	}
	return out, bounded, redacted
}

func hostedGenesisConversationMatchesSession(session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) bool {
	if session == nil || conv == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(conv.AgentID)) &&
		strings.TrimSpace(session.ConversationID) == strings.TrimSpace(conv.ConversationID)
}

func hostedGenesisTranscriptRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case hostedGenesisTranscriptRoleUser, hostedGenesisTranscriptRoleAssistant:
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func hostedGenesisTranscriptBoundContent(content string) (string, bool) {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= hostedGenesisTranscriptMaxContentRunes {
		return string(runes), false
	}
	return string(runes[:hostedGenesisTranscriptMaxContentRunes]), true
}

func hostedGenesisTranscriptMessageID(order int) string {
	if order < 1 {
		order = 1
	}
	return fmt.Sprintf("msg_%06d", order)
}

func hostedGenesisTranscriptMessageCreatedAt(session *models.HostedGenesisSession, role string, order int) *time.Time {
	if session == nil || role != hostedGenesisTranscriptRoleUser || order <= 0 {
		return nil
	}
	for _, entry := range session.TurnLedger {
		entry = entry.Normalize()
		if entry.MessageCount == order {
			return timePtrIfSet(entry.AcceptedAt)
		}
	}
	return nil
}

var (
	hostedGenesisTranscriptPEMPrivateKey = regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	hostedGenesisTranscriptBearerValue   = regexp.MustCompile(`(?i)(?:authorization\s*:\s*)?bearer\s+[a-z0-9._~+/=-]{20,}`)
	hostedGenesisTranscriptAWSAccessKey  = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
	hostedGenesisTranscriptProviderKey   = regexp.MustCompile(`(?i)\b(?:sk-(?:ant-)?[a-z0-9_-]{20,}|gh[pousr]_[a-z0-9]{20,})\b`)
	hostedGenesisTranscriptJWT           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	hostedGenesisTranscriptSecretValue   = regexp.MustCompile(`(?i)\b(?:aws[_ -]?secret[_ -]?access[_ -]?key|aws[_ -]?access[_ -]?key[_ -]?id|aws[_ -]?session[_ -]?token|x-amz-security-token|secretaccesskey|microvm[_ -]?endpoint[_ -]?token|instance[_ -]?api[_ -]?key|raw[_ -]?instance[_ -]?key|private[_ -]?key|seed[_ -]?phrase|signing[_ -]?material|api[_ -]?key)\b\s*(?::|=|\bis\b)\s*["']?\S{8,}`)
	hostedGenesisTranscriptSensitiveARN  = regexp.MustCompile(`(?i)\barn:aws:(?:iam|sts)::[0-9]{12}:[^\s]+`)
	hostedGenesisTranscriptSSMPath       = regexp.MustCompile(`(?i)(?:^|\s)/lesser-host/[a-z0-9_./-]+`)
)

// hostedGenesisTranscriptContentUnsafe detects secret-shaped values, not mere
// discussion of security vocabulary. A sentence such as "never share a private
// key or bearer token" is safe to project; a PEM key, token-shaped bearer value,
// credential assignment, account IAM/STS ARN, or Host SSM path is not.
func hostedGenesisTranscriptContentUnsafe(content string) bool {
	return hostedGenesisTranscriptPEMPrivateKey.MatchString(content) ||
		hostedGenesisTranscriptBearerValue.MatchString(content) ||
		hostedGenesisTranscriptAWSAccessKey.MatchString(content) ||
		hostedGenesisTranscriptProviderKey.MatchString(content) ||
		hostedGenesisTranscriptJWT.MatchString(content) ||
		hostedGenesisTranscriptSecretValue.MatchString(content) ||
		hostedGenesisTranscriptSensitiveARN.MatchString(content) ||
		hostedGenesisTranscriptSSMPath.MatchString(content)
}

func hostedGenesisStatusIncludesMessages(status string) bool {
	switch strings.TrimSpace(status) {
	case models.SoulMintConversationStatusCreated,
		models.SoulMintConversationStatusInProgress,
		models.SoulMintConversationStatusAssistantTurnReady,
		models.SoulMintConversationStatusFailed:
		return true
	default:
		return false
	}
}

func isHostedGenesisProgressStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case models.SoulMintConversationStatusCreated,
		models.SoulMintConversationStatusInProgress,
		models.SoulMintConversationStatusAssistantTurnReady:
		return true
	default:
		return false
	}
}

func buildHostedGenesisProducedDeclarations(conv *models.SoulAgentMintConversation, registrationID string, requestID string, messageCount int) *hostedGenesisProducedDeclarations {
	if conv == nil {
		return nil
	}
	raw := strings.TrimSpace(conv.ProducedDeclarations)
	if raw == "" {
		return nil
	}
	decl, appErr := parseAndValidateMintConversationDeclarations(raw)
	if appErr != nil {
		return nil
	}
	sum := sha256.Sum256([]byte(raw))
	declHash := hex.EncodeToString(sum[:])
	producedAt := conv.CompletedAt
	if producedAt.IsZero() {
		producedAt = firstTime(conv.UpdatedAt, conv.CreatedAt)
	}
	return &hostedGenesisProducedDeclarations{
		DeclarationID:   "decl_" + declHash[:16],
		DeclarationHash: "sha256:" + declHash,
		ProducedAt:      producedAt.UTC().Format(time.RFC3339Nano),
		Declarations:    decl,
		Evidence: hostedGenesisDeclarationEvidence{
			Source:         "host_conversation",
			RegistrationID: strings.TrimSpace(registrationID),
			ConversationID: strings.TrimSpace(conv.ConversationID),
			AgentID:        strings.ToLower(strings.TrimSpace(conv.AgentID)),
			MessageCount:   messageCount,
			Model:          strings.TrimSpace(conv.Model),
			RequestID:      strings.TrimSpace(requestID),
		},
	}
}

func hostedGenesisFailureFromReason(reason string) *hostedGenesisFailure {
	code := normalizeHostedGenesisFailureCode(reason)
	retryable := code == hostedGenesisFailureLLMUnavailable || code == hostedGenesisFailureAssistantTurnFailed || code == hostedGenesisFailureMicroVMUnavailable
	recovery := hostedGenesisFailureRecovery{Action: hostedGenesisRecoveryRefreshState, Reason: code}
	if hostedgenesis.IsDeclarationValidationCode(reason) {
		recovery.Reason = strings.TrimSpace(reason)
	}
	if retryable {
		recovery.Action = hostedGenesisRecoveryRetrySameStep
		recovery.MaxAttempts = 3
		recovery.RetryAfterSeconds = 30
	}
	if code == hostedGenesisFailureTenantBoundaryViolation || code == hostedGenesisFailureOperatorActionRequired {
		recovery.Action = hostedGenesisRecoveryOperatorAction
	}
	if code == hostedGenesisFailureMissingProducedDeclarations || code == hostedGenesisFailureInvalidProducedDeclarations {
		recovery.Action = hostedGenesisRecoveryRestartSoulBootstrap
	}
	return &hostedGenesisFailure{
		Code:      code,
		Message:   hostedGenesisFailureMessage(code),
		Retryable: retryable,
		Recovery:  recovery,
	}
}

func normalizeHostedGenesisFailureCode(reason string) string {
	if hostedgenesis.IsDeclarationValidationCode(reason) {
		return hostedGenesisFailureInvalidProducedDeclarations
	}
	switch strings.TrimSpace(reason) {
	case hostedGenesisFailureLLMUnavailable,
		hostedGenesisFailureAssistantTurnFailed,
		hostedGenesisFailureInvalidCompletionState,
		hostedGenesisFailureMissingProducedDeclarations,
		hostedGenesisFailureInvalidProducedDeclarations,
		hostedGenesisFailureTenantBoundaryViolation,
		hostedGenesisFailureOperatorActionRequired,
		hostedGenesisFailureMicroVMUnavailable:
		return strings.TrimSpace(reason)
	default:
		return hostedGenesisFailureAssistantTurnFailed
	}
}

func hostedGenesisFailureMessage(code string) string {
	switch code {
	case hostedGenesisFailureLLMUnavailable:
		return "Assistant declaration phase could not start."
	case hostedGenesisFailureInvalidCompletionState:
		return "Conversation cannot be completed from the current state."
	case hostedGenesisFailureMissingProducedDeclarations:
		return "Produced declarations are missing."
	case hostedGenesisFailureInvalidProducedDeclarations:
		return "Produced declarations are invalid."
	case hostedGenesisFailureTenantBoundaryViolation:
		return "Conversation failed instance boundary validation."
	case hostedGenesisFailureOperatorActionRequired:
		return "Operator action is required."
	case hostedGenesisFailureMicroVMUnavailable:
		return "MicroVM execution dispatch is unavailable."
	default:
		return "Assistant declaration phase failed."
	}
}

func mintConversationMessageCount(conv *models.SoulAgentMintConversation) int {
	if conv == nil {
		return 0
	}
	decodeMintConversationFields(conv)
	if strings.TrimSpace(conv.Messages) == "" {
		return 0
	}
	var messages []soulMintConversationMessage
	if err := json.Unmarshal([]byte(conv.Messages), &messages); err != nil {
		return 0
	}
	return len(messages)
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func timePtrIfSet(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	v := value.UTC()
	return &v
}

func nilIfEmptyTrace(trace hostedGenesisTraceIDs) *hostedGenesisTraceIDs {
	if strings.TrimSpace(trace.HostRequestID) == "" && strings.TrimSpace(trace.CorrelationID) == "" && strings.TrimSpace(trace.IdempotencyKey) == "" && strings.TrimSpace(trace.LesserRequestID) == "" {
		return nil
	}
	return &trace
}

func hostedGenesisRequestHash(registrationID string, conversationID string, model string, message string, candidateActions ...*hostedgenesis.DeclarationCandidateAction) string {
	payload := map[string]any{
		"registration_id": strings.TrimSpace(registrationID),
		"conversation_id": strings.TrimSpace(conversationID),
		"model":           strings.TrimSpace(model),
		"message_hash":    sha256HexTrimmed(message),
	}
	if len(candidateActions) > 0 && candidateActions[0] != nil {
		payload["candidate_action"] = candidateActions[0]
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hostedGenesisSafeToken(value string, maxLen int) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if maxLen <= 0 {
		maxLen = 128
	}
	if len(value) > maxLen {
		return "", false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return "", false
		}
	}
	return value, true
}

func hostedGenesisBadRequest(field string) *apptheory.AppTheoryError {
	return newAppTheoryError("app.bad_request", fmt.Sprintf("%s is invalid", field))
}
