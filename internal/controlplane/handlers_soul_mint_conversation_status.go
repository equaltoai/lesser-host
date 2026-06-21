package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const hostedGenesisConversationVersion = "1"

const (
	hostedGenesisFailureLLMUnavailable              = "llm_unavailable"
	hostedGenesisFailureAssistantTurnFailed         = "assistant_turn_failed"
	hostedGenesisFailureDeclarationExtractionFailed = "declaration_extraction_failed"
	hostedGenesisFailureInvalidCompletionState      = "invalid_completion_state"
	hostedGenesisFailureMissingProducedDeclarations = "missing_produced_declarations"
	hostedGenesisFailureInvalidProducedDeclarations = "invalid_produced_declarations"
	hostedGenesisFailureTenantBoundaryViolation     = "tenant_boundary_violation"
	hostedGenesisFailureOperatorActionRequired      = "operator_action_required"
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
	ProducedDeclarations *hostedGenesisProducedDeclarations `json:"produced_declarations,omitempty"`
	Failure              *hostedGenesisFailure              `json:"failure,omitempty"`
	RequestID            string                             `json:"request_id"`
	TraceIDs             *hostedGenesisTraceIDs             `json:"trace_ids,omitempty"`
	PollAfterSeconds     int                                `json:"poll_after_seconds,omitempty"`
	CreatedAt            *time.Time                         `json:"created_at,omitempty"`
	UpdatedAt            *time.Time                         `json:"updated_at,omitempty"`
	CompletedAt          *time.Time                         `json:"completed_at,omitempty"`
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
		models.SoulMintConversationStatusDeclarationExtractionPending,
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

func hostedGenesisConversationJSON(status int, conv *models.SoulAgentMintConversation, opts hostedGenesisProjectionOptions) (*apptheory.Response, error) {
	return apptheory.JSON(status, buildHostedGenesisConversationResponse(conv, opts))
}

func isHostedGenesisProgressStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case models.SoulMintConversationStatusCreated,
		models.SoulMintConversationStatusInProgress,
		models.SoulMintConversationStatusAssistantTurnReady,
		models.SoulMintConversationStatusDeclarationExtractionPending:
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
	retryable := code == hostedGenesisFailureLLMUnavailable || code == hostedGenesisFailureAssistantTurnFailed || code == hostedGenesisFailureDeclarationExtractionFailed
	recovery := hostedGenesisFailureRecovery{Action: hostedGenesisRecoveryRefreshState, Reason: code}
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
	switch strings.TrimSpace(reason) {
	case hostedGenesisFailureLLMUnavailable,
		hostedGenesisFailureAssistantTurnFailed,
		hostedGenesisFailureDeclarationExtractionFailed,
		hostedGenesisFailureInvalidCompletionState,
		hostedGenesisFailureMissingProducedDeclarations,
		hostedGenesisFailureInvalidProducedDeclarations,
		hostedGenesisFailureTenantBoundaryViolation,
		hostedGenesisFailureOperatorActionRequired:
		return strings.TrimSpace(reason)
	default:
		return hostedGenesisFailureAssistantTurnFailed
	}
}

func hostedGenesisFailureMessage(code string) string {
	switch code {
	case hostedGenesisFailureLLMUnavailable:
		return "Assistant turn failed before declaration extraction."
	case hostedGenesisFailureDeclarationExtractionFailed:
		return "Declaration extraction failed."
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
	default:
		return "Assistant turn failed before declaration extraction."
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

func hostedGenesisProgressResponse(ctx *apptheory.Context, status int, regID string, conv *models.SoulAgentMintConversation) (*apptheory.Response, error) {
	requestID := ""
	if ctx != nil {
		requestID = strings.TrimSpace(ctx.RequestID)
	}
	if status == 0 {
		status = http.StatusAccepted
	}
	return hostedGenesisConversationJSON(status, conv, hostedGenesisProjectionOptions{RegistrationID: regID, RequestID: requestID, CollapseCreated: true})
}

func hostedGenesisRequestHash(registrationID string, conversationID string, model string, message string) string {
	payload := map[string]string{
		"registration_id": strings.TrimSpace(registrationID),
		"conversation_id": strings.TrimSpace(conversationID),
		"model":           strings.TrimSpace(model),
		"message_hash":    sha256HexTrimmed(message),
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

func hostedGenesisBadRequest(field string) *apptheory.AppError {
	return &apptheory.AppError{Code: "app.bad_request", Message: fmt.Sprintf("%s is invalid", field)}
}
