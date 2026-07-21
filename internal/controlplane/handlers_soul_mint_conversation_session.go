package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func hostedGenesisConversationJSONFromSession(status int, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, opts hostedGenesisProjectionOptions) (*apptheory.Response, error) {
	return apptheory.JSON(status, buildHostedGenesisConversationResponseFromSession(session, conv, opts))
}

func buildHostedGenesisConversationResponseFromSession(session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, opts hostedGenesisProjectionOptions) hostedGenesisConversationResponse {
	if session == nil {
		return hostedGenesisConversationResponse{Version: hostedGenesisConversationVersion, RequestID: strings.TrimSpace(opts.RequestID)}
	}
	input := session.ToProjectionInput()
	if isHostedGenesisProgressStatus(string(hostedgenesis.InstanceKeyReadStatus(hostedgenesis.Status(session.Status)))) {
		input.PollAfterSeconds = hostedGenesisDefaultPollAfterSeconds
	}
	projection, err := hostedgenesis.NewConversationProjection(input, opts.CollapseCreated)
	if err != nil {
		projection = hostedgenesis.ConversationProjection{
			RegistrationID:   session.RegistrationID,
			ConversationID:   session.ConversationID,
			AgentID:          session.AgentID,
			Status:           hostedgenesis.StatusFailed,
			MessageCount:     session.MessageCount,
			Failure:          hostedGenesisInvalidProjectionFailure(),
			PublishedVersion: 0,
			RequestID:        session.RequestID,
			PollAfterSeconds: 0,
			CreatedAt:        session.CreatedAt,
			UpdatedAt:        session.UpdatedAt,
			CompletedAt:      session.CompletedAt,
		}
	}
	requestID := firstNonEmpty(opts.RequestID, projection.RequestID, session.RequestID)
	trace := hostedGenesisTraceIDs{
		HostRequestID:   firstNonEmpty(requestID, traceHostRequestID(projection.TraceIDs)),
		CorrelationID:   firstNonEmpty(opts.CorrelationID, traceCorrelationID(projection.TraceIDs)),
		IdempotencyKey:  firstNonEmpty(opts.IdempotencyKey, traceIdempotencyKey(projection.TraceIDs)),
		LesserRequestID: firstNonEmpty(opts.LesserRequestID, traceLesserRequestID(projection.TraceIDs)),
	}
	responseProjection := hostedGenesisConversationProjection{
		RegistrationID:   projection.RegistrationID,
		ConversationID:   projection.ConversationID,
		AgentID:          strings.ToLower(strings.TrimSpace(projection.AgentID)),
		Status:           string(projection.Status),
		LatestTurnID:     projection.LatestTurnID,
		MessageCount:     projection.MessageCount,
		RequestID:        requestID,
		TraceIDs:         nilIfEmptyTrace(trace),
		PollAfterSeconds: projection.PollAfterSeconds,
		CreatedAt:        timePtrIfSet(projection.CreatedAt),
		UpdatedAt:        timePtrIfSet(projection.UpdatedAt),
		CompletedAt:      timePtrIfSet(projection.CompletedAt),
		PublishedVersion: projection.PublishedVersion,
		PublishedAt:      timePtrIfSet(projection.PublishedAt),
	}
	if hostedGenesisStatusIncludesMessages(string(projection.Status)) {
		messages, bounded, redacted := buildHostedGenesisConversationMessages(session, conv)
		if len(messages) > 0 {
			responseProjection.Messages = messages
		}
		responseProjection.MessagesTruncated = bounded
		responseProjection.MessagesRedacted = redacted
	}
	if projection.Status == hostedgenesis.StatusDeclarationReady {
		responseProjection.ProducedDeclarations = buildHostedGenesisProducedDeclarationsFromSession(session, conv, requestID)
	}
	if projection.Status == hostedgenesis.StatusFailed {
		responseProjection.Failure = hostedGenesisFailureFromSessionForSession(session, projection.Failure)
	}
	return hostedGenesisConversationResponse{
		Version:      hostedGenesisConversationVersion,
		RequestID:    requestID,
		Conversation: responseProjection,
	}
}

func hostedGenesisInvalidProjectionFailure() *hostedgenesis.Failure {
	return &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeInvalidCompletionState,
		Message:   hostedGenesisFailureMessage(hostedGenesisFailureInvalidCompletionState),
		Retryable: false,
		Recovery: hostedgenesis.Recovery{
			Action: hostedgenesis.RecoveryActionRefreshState,
			Reason: hostedGenesisFailureInvalidCompletionState,
		},
	}
}

func hostedGenesisFailureFromSession(failure *hostedgenesis.Failure) *hostedGenesisFailure {
	return hostedGenesisFailureFromSessionForSession(nil, failure)
}

func hostedGenesisFailureFromSessionForSession(session *models.HostedGenesisSession, failure *hostedgenesis.Failure) *hostedGenesisFailure {
	if failure == nil {
		return hostedGenesisFailureFromReason(hostedGenesisFailureInvalidCompletionState)
	}
	if hostedGenesisDeclarationExtractionRetriesExhausted(session) {
		failure = hostedGenesisExhaustedRetryFailure(failure, hostedGenesisFailureDeclarationExtractionFailed)
	}
	if hostedGenesisAssistantTurnRetriesExhausted(session) {
		failure = hostedGenesisExhaustedRetryFailure(failure, hostedGenesisFailureAssistantTurnFailed)
	}
	code := failure.Code
	if hostedgenesis.IsDeclarationValidationCode(failure.Recovery.Reason) {
		code = hostedgenesis.FailureCodeInvalidProducedDeclarations
	}
	reason := hostedgenesis.SanitizeFailureReason(code, failure.Recovery.Reason)
	return &hostedGenesisFailure{
		Code:      string(code),
		Class:     string(failure.Class),
		Message:   hostedgenesis.FailureMessage(code),
		Retryable: failure.Retryable,
		Recovery: hostedGenesisFailureRecovery{
			Action:            string(failure.Recovery.Action),
			MaxAttempts:       failure.Recovery.MaxAttempts,
			RetryAfterSeconds: failure.Recovery.RetryAfterSeconds,
			Reason:            reason,
		},
	}
}

func hostedGenesisExhaustedRetryFailure(failure *hostedgenesis.Failure, reason string) *hostedgenesis.Failure {
	if failure == nil {
		return nil
	}
	exhausted := *failure
	exhausted.Retryable = false
	exhausted.Recovery.Action = hostedgenesis.RecoveryActionRestartSoulBootstrap
	exhausted.Recovery.MaxAttempts = 0
	exhausted.Recovery.RetryAfterSeconds = 0
	exhausted.Recovery.Reason = strings.TrimSpace(reason)
	return &exhausted
}

func buildHostedGenesisProducedDeclarationsFromSession(session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, requestID string) *hostedGenesisProducedDeclarations {
	if session == nil || session.DeclarationCheckpoint == nil || conv == nil {
		return nil
	}
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conv.ProducedDeclarations))
	if raw == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(raw))
	if want := strings.TrimSpace(session.DeclarationCheckpoint.DeclarationHash); !strings.EqualFold(want, "sha256:"+hex.EncodeToString(sum[:])) {
		return nil
	}
	decl, appErr := parseAndValidateMintConversationDeclarations(raw)
	if appErr != nil {
		return nil
	}
	producedAt := session.DeclarationCheckpoint.ProducedAt
	if producedAt.IsZero() {
		producedAt = firstTime(session.CompletedAt, session.UpdatedAt, session.CreatedAt)
	}
	return &hostedGenesisProducedDeclarations{
		DeclarationID:   strings.TrimSpace(session.DeclarationCheckpoint.DeclarationID),
		DeclarationHash: strings.TrimSpace(session.DeclarationCheckpoint.DeclarationHash),
		ProducedAt:      producedAt.UTC().Format(time.RFC3339Nano),
		Declarations:    decl,
		Evidence: hostedGenesisDeclarationEvidence{
			Source:         "host_conversation",
			RegistrationID: strings.TrimSpace(session.DeclarationCheckpoint.RegistrationID),
			ConversationID: strings.TrimSpace(session.DeclarationCheckpoint.ConversationID),
			AgentID:        strings.ToLower(strings.TrimSpace(session.DeclarationCheckpoint.AgentID)),
			MessageCount:   session.DeclarationCheckpoint.MessageCount,
			Model:          strings.TrimSpace(session.DeclarationCheckpoint.Model),
			RequestID:      firstNonEmpty(requestID, session.DeclarationCheckpoint.RequestID),
		},
	}
}

func traceHostRequestID(trace *hostedgenesis.TraceIDs) string {
	if trace == nil {
		return ""
	}
	return trace.HostRequestID
}

func traceCorrelationID(trace *hostedgenesis.TraceIDs) string {
	if trace == nil {
		return ""
	}
	return trace.CorrelationID
}

func traceIdempotencyKey(trace *hostedgenesis.TraceIDs) string {
	if trace == nil {
		return ""
	}
	return trace.IdempotencyKey
}

func traceLesserRequestID(trace *hostedgenesis.TraceIDs) string {
	if trace == nil {
		return ""
	}
	return trace.LesserRequestID
}
