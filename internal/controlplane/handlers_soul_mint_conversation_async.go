package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	// hostedGenesisAcceptedTurnRunTimeout bounds the synchronous assistant
	// runner retained for non-production/test seams. H2.1 deletes that path; the
	// production accept path dispatches the MicroVM and returns 202 well under
	// this budget.
	hostedGenesisAcceptedTurnRunTimeout = 90 * time.Second
	// hostedGenesisAcceptedTurnDispatchTimeout bounds the M16 controller run
	// dispatch on the production accept path. The accept path returns 202
	// accepted-pending; the assistant turn itself runs inside the MicroVM and is
	// not bounded by this timeout.
	hostedGenesisAcceptedTurnDispatchTimeout = 10 * time.Second
	hostedGenesisProviderUnknown             = "unknown"
)

type hostedGenesisTurnSession struct {
	conversationID     string
	turnID             string
	modelSet           string
	existingMessages   []soulMintConversationMessage
	existingUsage      models.AIUsage
	sessionIsNew       bool
	expectedStatus     hostedgenesis.Status
	expectedVersion    int64
	progressVersion    int64
	hasProgressVersion bool
	replayed           bool
	requestHash        string
	idempotency        *models.SoulMintConversationIdempotency
	conv               *models.SoulAgentMintConversation
	session            *models.HostedGenesisSession
}

func (s *Server) handleSoulMintConversationForRegistrationAsync(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, instanceSlug string) (*apptheory.Response, error) {
	if publishGuardErr := s.ensureMintConversationAgentNotPublished(ctx.Context(), regCtx.agentIDHex); publishGuardErr != nil {
		return nil, publishGuardErr
	}
	req, message, err := requireMintConversationMessage(ctx)
	if err != nil {
		return nil, err
	}
	if validationErr := validateHostedGenesisRequestIDs(&req); validationErr != nil {
		return nil, validationErr
	}

	now := time.Now().UTC()
	instanceSlug = firstNonEmpty(instanceSlug, regCtx.inst.Slug)
	session, loadErr := s.loadHostedGenesisTurnSession(ctx.Context(), regCtx, instanceSlug, req, message, strings.TrimSpace(ctx.RequestID), now)
	if loadErr != nil {
		return nil, loadErr
	}
	if session.modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	if session.replayed {
		if hostedGenesisReplayedTurnNeedsProgression(session) {
			apiKey, apiKeyErr := s.apiKeyForMintConversationModel(ctx.Context(), session.modelSet)
			if apiKeyErr != nil {
				return nil, apiKeyErr
			}
			progressedSession, progressedConv, status, progressErr := s.progressHostedGenesisAcceptedTurn(ctx.Context(), regCtx, session, session.conv, session.existingMessages, apiKey, strings.TrimSpace(ctx.RequestID))
			if progressErr != nil {
				return nil, progressErr
			}
			return hostedGenesisConversationJSONFromSession(status, progressedSession, progressedConv, hostedGenesisProjectionOptions{
				RegistrationID:  regCtx.reg.ID,
				RequestID:       strings.TrimSpace(ctx.RequestID),
				CollapseCreated: true,
				CorrelationID:   req.CorrelationID,
				IdempotencyKey:  req.IdempotencyKey,
				LesserRequestID: req.LesserRequestID,
			})
		}
		return hostedGenesisConversationJSONFromSession(http.StatusAccepted, session.session, session.conv, hostedGenesisProjectionOptions{
			RegistrationID:  regCtx.reg.ID,
			RequestID:       strings.TrimSpace(ctx.RequestID),
			CollapseCreated: true,
			CorrelationID:   req.CorrelationID,
			IdempotencyKey:  req.IdempotencyKey,
			LesserRequestID: req.LesserRequestID,
		})
	}
	apiKey, apiKeyErr := s.apiKeyForMintConversationModel(ctx.Context(), session.modelSet)
	if apiKeyErr != nil {
		// Validate provider configuration before accepting a paid hosted-genesis
		// execution handoff. Project 51 M4 deliberately keeps SQS out of this
		// user-visible path; execution/recovery authority is the durable session
		// plus AppTheory MicroVM execution/cache state.
		return nil, apiKeyErr
	}

	updatedMessages, messagesJSON, err := serializeHostedGenesisAcceptedTurn(session, message)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to serialize conversation"}
	}
	idem := buildHostedGenesisIdempotency(instanceSlug, regCtx, session, req, session.requestHash, now, strings.TrimSpace(ctx.RequestID))
	if appErr := s.persistHostedGenesisAcceptedTurn(ctx.Context(), regCtx, session, updatedMessages, messagesJSON, idem, firstNonEmpty(req.IdempotencyKey, ctx.RequestID), strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		return nil, appErr
	}

	conv := buildHostedGenesisAcceptedConversation(regCtx, session, req, messagesJSON, strings.TrimSpace(ctx.RequestID), now)

	// H1.4 (kills G10c): a promotion-update failure is surfaced (logged loudly),
	// not swallowed. Promotion is non-authoritative lifecycle metadata; a
	// failure here must not abort the accepted turn (the durable session and
	// idempotency write already committed), but it must not be silently dropped
	// either — the structured log makes the failed promotion observable so an
	// operator can reconcile the review-started lifecycle event. The previous
	// behavior logged only on the silent-continue path; the swallow is removed
	// by making the loud log the explicit, only error path.
	if appErr := s.saveHostedGenesisAcceptedPromotion(ctx.Context(), regCtx, session, strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		log.Printf("controlplane: hosted genesis accepted promotion update failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), appErr)
	}

	progressedSession, progressedConv, status, progressErr := s.progressHostedGenesisAcceptedTurn(ctx.Context(), regCtx, session, conv, updatedMessages, apiKey, strings.TrimSpace(ctx.RequestID))
	if progressErr != nil {
		return nil, progressErr
	}

	return hostedGenesisConversationJSONFromSession(status, progressedSession, progressedConv, hostedGenesisProjectionOptions{
		RegistrationID:  regCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
		CorrelationID:   req.CorrelationID,
		IdempotencyKey:  req.IdempotencyKey,
		LesserRequestID: req.LesserRequestID,
	})
}

func hostedGenesisReplayedTurnNeedsProgression(session hostedGenesisTurnSession) bool {
	if !session.replayed || session.session == nil || session.conv == nil || len(session.existingMessages) == 0 {
		return false
	}
	if hostedgenesis.NormalizeStatus(session.session.Status) != hostedgenesis.StatusInProgress {
		return false
	}
	return strings.TrimSpace(session.session.AssistantCheckpointRef) == "" &&
		strings.TrimSpace(session.session.ExecutionStateRef) == "" &&
		strings.TrimSpace(session.session.MicroVMExecutionID) == "" &&
		session.session.MicroVMLifecycleRef == nil
}

type hostedGenesisAssistantRunInput struct {
	apiKey       string
	modelSet     string
	systemPrompt string
	messages     []soulMintConversationMessage
}

type hostedGenesisAssistantRunResult struct {
	fullResponse string
	usage        models.AIUsage
}

func (s *Server) progressHostedGenesisAcceptedTurn(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, apiKey string, requestID string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, int, *apptheory.AppError) {
	if session.session == nil || conv == nil {
		return nil, nil, 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	// H1.2: the production accept path dispatches the AppTheory M16 MicroVM
	// controller run command and returns 202 accepted-pending. The control plane
	// never makes a synchronous LLM HTTP call on this path; the assistant turn
	// runs inside the MicroVM and completion is reconstructed from Host truth.
	// The synchronous assistant runner is retained only behind an explicit
	// non-production/test guard (hostedGenesisSyncAssistantFallbackEnabled) until
	// H2.1 deletes it; production never sets that guard.
	if s.hostedGenesisMicroVMDispatcher == nil {
		if s.hostedGenesisSyncAssistantFallbackEnabled && s.hostedGenesisAssistantRunner != nil {
			return s.progressHostedGenesisAcceptedTurnSync(ctx, regCtx, session, conv, acceptedMessages, apiKey, requestID)
		}
		log.Printf("controlplane: hosted genesis microvm dispatcher unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID))
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		// H1.5 (carries forward G10a's explicit-status posture): the MicroVM-
		// unavailable accept-path returns an explicit 503, matching the error
		// mapper's 503 for appErrCodeMicroVMUnavailable. The returned int is the
		// response status used when the caller builds a non-error body; the typed
		// AppError is the authoritative surface and also maps to 503.
		return failedSession, failedConv, http.StatusServiceUnavailable, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM execution dispatch is unavailable"}
	}

	binding := session.session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis microvm binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		// H1.5: explicit 503 (matches the error mapper), not a silent 200.
		return failedSession, failedConv, http.StatusServiceUnavailable, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM execution dispatch is unavailable"}
	}

	runCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx), hostedGenesisAcceptedTurnDispatchTimeout)
	defer cancel()
	dispatch, dispatchErr := s.hostedGenesisMicroVMDispatcher.DispatchMicroVMRun(runCtx, strings.TrimSpace(requestID), binding)
	if dispatchErr != nil {
		log.Printf("controlplane: hosted genesis microvm dispatch failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), dispatchErr)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		// H1.5: explicit 503 (matches the error mapper), not a silent 200.
		return failedSession, failedConv, http.StatusServiceUnavailable, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM execution dispatch failed"}
	}

	progressedSession, progressedConv, appErr := s.persistHostedGenesisAcceptedMicroVMDispatch(ctx, session, conv, acceptedMessages, dispatch, requestID, time.Now().UTC())
	if appErr != nil {
		return nil, nil, 0, appErr
	}
	return progressedSession, progressedConv, http.StatusAccepted, nil
}

// progressHostedGenesisAcceptedTurnSync is the retained non-production/test-only
// synchronous assistant turn path. It is reachable only when
// hostedGenesisSyncAssistantFallbackEnabled is explicitly true AND no MicroVM
// dispatcher is wired; production never sets that guard. H2.1 deletes this path
// and the hostedGenesisAssistantRunner field.
func (s *Server) progressHostedGenesisAcceptedTurnSync(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, apiKey string, requestID string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, int, *apptheory.AppError) {
	runCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx), hostedGenesisAcceptedTurnRunTimeout)
	defer cancel()

	result, err := s.runHostedGenesisAcceptedAssistant(runCtx, hostedGenesisAssistantRunInput{
		apiKey:       strings.TrimSpace(apiKey),
		modelSet:     session.modelSet,
		systemPrompt: buildMintConversationSystemPrompt(regCtx.reg),
		messages:     append([]soulMintConversationMessage(nil), acceptedMessages...),
	})
	if err != nil || strings.TrimSpace(result.fullResponse) == "" {
		log.Printf("controlplane: hosted genesis assistant turn failed agent_hash=%s conversation_hash=%s provider=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), hostedGenesisProviderName(session.modelSet), err)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureAssistantTurnFailed, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		// H1.4 (kills G10a): a failed turn surfaces as a loud non-2xx typed
		// failure, not HTTP 200 with a failed body. The durable session is
		// persisted as a retryable failed turn above; the returned typed error
		// makes the public surface emit 502, never a silent 200-on-failure.
		return failedSession, failedConv, http.StatusBadGateway, &apptheory.AppError{Code: appErrCodeAssistantTurnFailed, Message: "hosted genesis assistant turn failed"}
	}

	progressedSession, progressedConv, appErr := s.persistHostedGenesisAcceptedAssistantTurn(ctx, session, conv, acceptedMessages, result, requestID, time.Now().UTC())
	if appErr != nil {
		return nil, nil, 0, appErr
	}
	return progressedSession, progressedConv, http.StatusOK, nil
}

// persistHostedGenesisAcceptedMicroVMDispatch records the durable in_progress
// HostedGenesisSession after a successful M16 controller run dispatch, applying
// the non-authoritative MicroVM execution/cache lifecycle ref via
// ApplyMicroVMLifecycleRef so the three MicroVM fields
// (MicroVMExecutionID/ExecutionStateRef/MicroVMLifecycleRef) are populated on
// the authoritative Host row. The conversation stays in_progress with no inline
// assistant message: the turn is pending inside the MicroVM and completion is
// reconstructed from Host truth by the recovery/stuck-turn path.
func (s *Server) persistHostedGenesisAcceptedMicroVMDispatch(ctx context.Context, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, dispatch hostedgenesis.MicroVMDispatchResult, requestID string, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppError) {
	progressedSession := cloneHostedGenesisSession(session.session)
	progressedConv := cloneSoulAgentMintConversation(conv)
	if err := progressedSession.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); err != nil {
		log.Printf("controlplane: hosted genesis microvm lifecycle ref rejected agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(conv.AgentID), soulMintInstanceReadAuditHash(conv.ConversationID), err)
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to record microvm dispatch"}
	}
	progressedSession.Status = string(hostedgenesis.StatusInProgress)
	progressedSession.RequestID = strings.TrimSpace(requestID)
	progressedSession.UpdatedAt = now
	progressedSession.CompletedAt = time.Time{}
	progressedConv.RequestID = strings.TrimSpace(requestID)
	progressedConv.UpdatedAt = now
	if appErr := s.persistHostedGenesisProgression(ctx, session, progressedSession, progressedConv, string(progressedConv.Messages), "", progressedConv.Usage, now); appErr != nil {
		return nil, nil, appErr
	}
	return progressedSession, progressedConv, nil
}

func (s *Server) runHostedGenesisAcceptedAssistant(ctx context.Context, in hostedGenesisAssistantRunInput) (hostedGenesisAssistantRunResult, error) {
	if s != nil && s.hostedGenesisAssistantRunner != nil {
		return s.hostedGenesisAssistantRunner(ctx, in)
	}
	llmMessages := make([]llm.MintConversationMessage, 0, len(in.messages))
	for _, m := range in.messages {
		llmMessages = append(llmMessages, llm.MintConversationMessage{
			Role:    strings.ToLower(strings.TrimSpace(m.Role)),
			Content: strings.TrimSpace(m.Content),
		})
	}
	modelSet := strings.TrimSpace(in.modelSet)
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		full, usage, err := llm.StreamMintConversationOpenAI(ctx, strings.TrimSpace(in.apiKey), modelSet, in.systemPrompt, llmMessages, func(string) {})
		return hostedGenesisAssistantRunResult{fullResponse: full, usage: usage}, err
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		full, usage, err := llm.StreamMintConversationAnthropic(ctx, strings.TrimSpace(in.apiKey), modelSet, in.systemPrompt, llmMessages, func(string) {})
		return hostedGenesisAssistantRunResult{fullResponse: full, usage: usage}, err
	default:
		return hostedGenesisAssistantRunResult{}, fmt.Errorf("unsupported model set")
	}
}

func (s *Server) persistHostedGenesisAcceptedAssistantTurn(ctx context.Context, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, result hostedGenesisAssistantRunResult, requestID string, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppError) {
	assistantMessage := soulMintConversationMessage{Role: "assistant", Content: strings.TrimSpace(result.fullResponse)}
	updatedMessages := append(append([]soulMintConversationMessage(nil), acceptedMessages...), assistantMessage)
	messagesJSON, err := json.Marshal(updatedMessages)
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to serialize conversation"}
	}
	progressedSession := cloneHostedGenesisSession(session.session)
	progressedConv := cloneSoulAgentMintConversation(conv)
	usage := addAIUsage(session.existingUsage, result.usage)
	progressedSession.Status = string(hostedgenesis.StatusAssistantTurnReady)
	progressedSession.MessageCount = hostedGenesisMaxInt(progressedSession.MessageCount, len(updatedMessages))
	progressedSession.AssistantCheckpointRef = fmt.Sprintf("checkpoint://hosted-genesis/%s/assistant/%s", progressedSession.ConversationID, session.turnID)
	progressedSession.Failure = nil
	progressedSession.RequestID = strings.TrimSpace(requestID)
	progressedSession.UpdatedAt = now
	progressedSession.CompletedAt = time.Time{}
	progressedConv.Messages = string(messagesJSON)
	progressedConv.Usage = usage
	progressedConv.Status = models.SoulMintConversationStatusAssistantTurnReady
	progressedConv.StatusReason = ""
	progressedConv.LatestTurnID = session.turnID
	progressedConv.RequestID = strings.TrimSpace(requestID)
	progressedConv.UpdatedAt = now
	if appErr := s.persistHostedGenesisProgression(ctx, session, progressedSession, progressedConv, string(messagesJSON), "", usage, now); appErr != nil {
		return nil, nil, appErr
	}
	return progressedSession, progressedConv, nil
}

func (s *Server) persistHostedGenesisAcceptedTurnFailure(ctx context.Context, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, reason string, requestID string, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppError) {
	messagesJSON, err := json.Marshal(acceptedMessages)
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to serialize conversation"}
	}
	failedSession := cloneHostedGenesisSession(session.session)
	failedConv := cloneSoulAgentMintConversation(conv)
	failedSession.Status = string(hostedgenesis.StatusFailed)
	failedSession.Failure = hostedGenesisSessionFailureFromReason(reason)
	failedSession.RequestID = strings.TrimSpace(requestID)
	failedSession.UpdatedAt = now
	failedSession.CompletedAt = now
	failedConv.Messages = string(messagesJSON)
	failedConv.Status = models.SoulMintConversationStatusFailed
	failedConv.StatusReason = strings.TrimSpace(reason)
	failedConv.LatestTurnID = session.turnID
	failedConv.RequestID = strings.TrimSpace(requestID)
	failedConv.UpdatedAt = now
	failedConv.CompletedAt = now
	if appErr := s.persistHostedGenesisProgression(ctx, session, failedSession, failedConv, string(messagesJSON), strings.TrimSpace(reason), failedConv.Usage, now); appErr != nil {
		return nil, nil, appErr
	}
	return failedSession, failedConv, nil
}

func (s *Server) persistHostedGenesisProgression(ctx context.Context, accepted hostedGenesisTurnSession, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, messagesJSON string, statusReason string, usage models.AIUsage, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || session == nil || conv == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	updateConv := &models.SoulAgentMintConversation{
		AgentID:        conv.AgentID,
		ConversationID: conv.ConversationID,
	}
	_ = updateConv.UpdateKeys()
	expectedVersion := hostedGenesisAcceptedTurnPostPersistVersion(accepted)
	expectedStatus := hostedgenesis.StatusInProgress
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		if err := addHostedGenesisSessionWrite(tx, session, false, expectedVersion, expectedStatus); err != nil {
			return err
		}
		tx.UpdateWithBuilder(updateConv, func(ub core.UpdateBuilder) error {
			ub.Set("Messages", encodeMintConversationBlob(messagesJSON))
			ub.Set("Status", conv.Status)
			ub.Set("StatusReason", strings.TrimSpace(statusReason))
			ub.Set("LatestTurnID", conv.LatestTurnID)
			ub.Set("RequestID", strings.TrimSpace(conv.RequestID))
			ub.Set("UpdatedAt", now)
			ub.Set("CompletedAt", conv.CompletedAt)
			ub.Set("Usage", usage)
			return nil
		}, tabletheory.IfExists())
		return nil
	}); err != nil {
		log.Printf("controlplane: hosted genesis progression persist failed agent_hash=%s conversation_hash=%s status=%s err=%v", soulMintInstanceReadAuditHash(conv.AgentID), soulMintInstanceReadAuditHash(conv.ConversationID), conv.Status, err)
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update conversation"}
	}
	return nil
}

func hostedGenesisAcceptedTurnPostPersistVersion(session hostedGenesisTurnSession) int64 {
	if session.hasProgressVersion {
		return session.progressVersion
	}
	if session.sessionIsNew {
		if session.session != nil {
			return session.session.Version
		}
		return 0
	}
	return session.expectedVersion + 1
}

func hostedGenesisSessionFailureFromReason(reason string) *hostedgenesis.Failure {
	projected := hostedGenesisFailureFromReason(reason)
	if projected == nil {
		projected = hostedGenesisFailureFromReason(hostedGenesisFailureAssistantTurnFailed)
	}
	return &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCode(projected.Code),
		Message:   projected.Message,
		Retryable: projected.Retryable,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryAction(projected.Recovery.Action),
			MaxAttempts:       projected.Recovery.MaxAttempts,
			RetryAfterSeconds: projected.Recovery.RetryAfterSeconds,
			Reason:            projected.Recovery.Reason,
		},
	}
}

func hostedGenesisProviderName(modelSet string) string {
	modelSet = strings.ToLower(strings.TrimSpace(modelSet))
	switch {
	case strings.HasPrefix(modelSet, "openai:"):
		return "openai"
	case strings.HasPrefix(modelSet, "anthropic:"):
		return "anthropic"
	default:
		return hostedGenesisProviderUnknown
	}
}

func cloneSoulAgentMintConversation(conv *models.SoulAgentMintConversation) *models.SoulAgentMintConversation {
	if conv == nil {
		return nil
	}
	copy := *conv
	return &copy
}

func hostedGenesisMaxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func serializeHostedGenesisAcceptedTurn(session hostedGenesisTurnSession, message string) ([]soulMintConversationMessage, string, error) {
	updatedMessages := append(append([]soulMintConversationMessage(nil), session.existingMessages...), soulMintConversationMessage{Role: "user", Content: message})
	messagesJSON, err := json.Marshal(updatedMessages)
	return updatedMessages, string(messagesJSON), err
}

func buildHostedGenesisAcceptedConversation(regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, req soulMintConversationRequest, messagesJSON string, requestID string, now time.Time) *models.SoulAgentMintConversation {
	conv := session.conv
	if conv == nil {
		conv = &models.SoulAgentMintConversation{AgentID: regCtx.agentIDHex, ConversationID: session.conversationID, Model: session.modelSet, CreatedAt: now}
	}
	conv.Messages = messagesJSON
	conv.Status = models.SoulMintConversationStatusInProgress
	conv.LatestTurnID = session.turnID
	conv.RequestID = strings.TrimSpace(requestID)
	conv.CorrelationID = strings.TrimSpace(req.CorrelationID)
	conv.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	conv.UpdatedAt = now
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	return conv
}

func (s *Server) saveHostedGenesisAcceptedPromotion(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, requestID string, now time.Time) *apptheory.AppError {
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx, regCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(regCtx.reg, now))
	previousPromotion := cloneSoulAgentPromotion(promotion)
	promotion = updateSoulAgentPromotionForConversation(promotion, session.conversationID, models.SoulMintConversationStatusInProgress, now)
	if appErr := s.saveSoulAgentPromotion(ctx, promotion); appErr != nil {
		return appErr
	}
	if shouldEmitSoulPromotionReviewStartedEvent(previousPromotion, promotion, session.conversationID) {
		if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx, buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
			EventType:      models.SoulAgentPromotionEventTypeReviewStarted,
			RequestID:      strings.TrimSpace(requestID),
			ConversationID: session.conversationID,
			OccurredAt:     now,
		})); appErr != nil {
			return appErr
		}
	}
	return nil
}

func validateHostedGenesisRequestIDs(req *soulMintConversationRequest) error {
	if req == nil {
		return nil
	}
	if strings.TrimSpace(req.ConversationID) != "" && !soulMintInstanceReadConversationIDSafe(req.ConversationID) {
		return hostedGenesisBadRequest("conversation_id")
	}
	var ok bool
	if req.IdempotencyKey, ok = hostedGenesisSafeToken(req.IdempotencyKey, 128); !ok {
		return hostedGenesisBadRequest("idempotency_key")
	}
	if req.CorrelationID, ok = hostedGenesisSafeToken(req.CorrelationID, 128); !ok {
		return hostedGenesisBadRequest("correlation_id")
	}
	if req.LesserRequestID, ok = hostedGenesisSafeToken(req.LesserRequestID, 128); !ok {
		return hostedGenesisBadRequest("lesser_request_id")
	}
	return nil
}

func (s *Server) loadHostedGenesisTurnSession(ctx context.Context, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppError) {
	session := hostedGenesisTurnSession{
		conversationID: strings.TrimSpace(req.ConversationID),
		modelSet:       strings.TrimSpace(req.Model),
	}
	if strings.TrimSpace(instanceSlug) == "" {
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := s.applyHostedGenesisIdempotencyLookup(ctx, &session, instanceSlug, regCtx, req); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	if appErr := assignHostedGenesisTurnID(&session); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	if session.conversationID == "" {
		return newHostedGenesisTurnSession(session, regCtx, instanceSlug, req, message, requestID, now)
	}
	return s.loadExistingHostedGenesisTurnSession(ctx, session, regCtx, instanceSlug, req, message, requestID, now)
}

func (s *Server) applyHostedGenesisIdempotencyLookup(ctx context.Context, session *hostedGenesisTurnSession, instanceSlug string, regCtx mintConversationRegistrationContext, req soulMintConversationRequest) *apptheory.AppError {
	if session == nil || strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil
	}
	item, err := s.getHostedGenesisIdempotency(ctx, instanceSlug, regCtx.reg.ID, req.IdempotencyKey)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to load idempotency key"}
	}
	if item == nil || err != nil {
		return nil
	}
	if session.conversationID != "" && session.conversationID != strings.TrimSpace(item.ConversationID) {
		return &apptheory.AppError{Code: "app.conflict", Message: "idempotency key already used for a different conversation"}
	}
	session.conversationID = strings.TrimSpace(item.ConversationID)
	session.idempotency = item
	return nil
}

func assignHostedGenesisTurnID(session *hostedGenesisTurnSession) *apptheory.AppError {
	if session == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	turn, err := newToken(16)
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to create turn id"}
	}
	session.turnID = "turn_" + turn
	return nil
}

func newHostedGenesisTurnSession(session hostedGenesisTurnSession, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppError) {
	if session.modelSet == "" {
		session.modelSet = defaultSoulMintConversationModel
	}
	token, err := newToken(16)
	if err != nil {
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.internal", Message: "failed to create conversation id"}
	}
	session.conversationID = token
	session.sessionIsNew = true
	session.session = &models.HostedGenesisSession{
		InstanceSlug:   instanceSlug,
		RegistrationID: regCtx.reg.ID,
		AgentID:        regCtx.agentIDHex,
		ConversationID: session.conversationID,
		Status:         string(hostedgenesis.StatusCreated),
		Model:          session.modelSet,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	session.expectedStatus = hostedgenesis.StatusCreated
	return finishHostedGenesisTurnSession(regCtx.reg.ID, session, req, message, requestID, now)
}

func (s *Server) loadExistingHostedGenesisTurnSession(ctx context.Context, session hostedGenesisTurnSession, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppError) {
	hostSession, err := s.store.GetHostedGenesisSession(ctx, instanceSlug, session.conversationID)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
		}
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.internal", Message: "failed to load conversation"}
	}
	session.session = hostSession
	session.expectedStatus = hostedgenesis.NormalizeStatus(hostSession.Status)
	session.expectedVersion = hostSession.Version
	if appErr := requireHostedGenesisSessionAcceptsTurn(hostSession); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	if appErr := applyHostedGenesisSessionModel(&session, hostSession.Model); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	s.hydrateHostedGenesisCompatibilityConversation(ctx, &session, regCtx.agentIDHex)
	if session.modelSet == "" {
		session.modelSet = defaultSoulMintConversationModel
	}
	return finishHostedGenesisTurnSession(regCtx.reg.ID, session, req, message, requestID, now)
}

func requireHostedGenesisSessionAcceptsTurn(session *models.HostedGenesisSession) *apptheory.AppError {
	status := hostedgenesis.NormalizeStatus(session.Status)
	if status == hostedgenesis.StatusInProgress || status == hostedgenesis.StatusAssistantTurnReady || status == hostedgenesis.StatusCreated {
		return nil
	}
	return &apptheory.AppError{Code: "app.conflict", Message: "conversation cannot accept a new turn"}
}

func applyHostedGenesisSessionModel(session *hostedGenesisTurnSession, storedModel string) *apptheory.AppError {
	if session == nil || strings.TrimSpace(storedModel) == "" {
		return nil
	}
	if session.modelSet != "" && !strings.EqualFold(storedModel, session.modelSet) {
		return &apptheory.AppError{Code: "app.conflict", Message: "cannot change model for an existing conversation"}
	}
	session.modelSet = strings.TrimSpace(storedModel)
	return nil
}

func (s *Server) hydrateHostedGenesisCompatibilityConversation(ctx context.Context, session *hostedGenesisTurnSession, agentIDHex string) {
	if session == nil {
		return
	}
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", session.conversationID))
	// H1.4 (kills G10b): a compat-conversation hydrate error is surfaced, not
	// swallowed. A not-found or absent compat conversation is benign (a new
	// turn has no prior compat row) and remains a no-op; any other load error
	// is a real storage failure and is logged loudly so it is not silently
	// masked as "no compat conversation".
	if err != nil {
		if !theoryErrors.IsNotFound(err) {
			log.Printf("controlplane: hosted genesis compat conversation hydrate failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
		}
		return
	}
	if conv == nil {
		return
	}
	decodeMintConversationFields(conv)
	if strings.TrimSpace(conv.Model) != "" && session.modelSet == "" {
		session.modelSet = strings.TrimSpace(conv.Model)
	}
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &session.existingMessages)
	}
	session.existingUsage = conv.Usage
	session.conv = conv
}

func finishHostedGenesisTurnSession(registrationID string, session hostedGenesisTurnSession, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppError) {
	session.requestHash = hostedGenesisRequestHash(registrationID, session.conversationID, session.modelSet, message)
	if appErr := validateHostedGenesisIdempotencyRequestHash(registrationID, &session, req, message); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	return applyHostedGenesisAcceptedTurnToSession(session, req, session.requestHash, requestID, now)
}

func validateHostedGenesisIdempotencyRequestHash(registrationID string, session *hostedGenesisTurnSession, req soulMintConversationRequest, message string) *apptheory.AppError {
	if session == nil || session.idempotency == nil {
		return nil
	}
	storedHash := strings.TrimSpace(session.idempotency.RequestHash)
	if storedHash == strings.TrimSpace(session.requestHash) {
		return nil
	}
	modelSet := firstNonEmpty(req.Model, session.modelSet)
	requestedHash := hostedGenesisRequestHash(registrationID, req.ConversationID, modelSet, message)
	withoutConversationHash := hostedGenesisRequestHash(registrationID, "", modelSet, message)
	if storedHash == strings.TrimSpace(requestedHash) || storedHash == strings.TrimSpace(withoutConversationHash) {
		session.requestHash = storedHash
		return nil
	}
	return &apptheory.AppError{Code: "app.conflict", Message: "idempotency key already used for a different request"}
}

func applyHostedGenesisAcceptedTurnToSession(session hostedGenesisTurnSession, req soulMintConversationRequest, reqHash string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppError) {
	if session.session == nil {
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	incoming := hostedgenesis.TurnLedgerEntry{
		TurnID:             strings.TrimSpace(session.turnID),
		IdempotencyKey:     strings.TrimSpace(req.IdempotencyKey),
		RequestHash:        strings.TrimSpace(reqHash),
		InputCheckpointRef: fmt.Sprintf("checkpoint://hosted-genesis/%s/input/%s", session.conversationID, session.turnID),
		BillingLedgerRef:   fmt.Sprintf("usage://hosted-genesis/%s/%s", session.conversationID, session.turnID),
		ChargedCredits:     soulMintConversationStreamBaseCredits,
		MessageCount:       session.session.MessageCount + 1,
		AcceptedAt:         now,
	}
	if incoming.IdempotencyKey == "" {
		incoming.RequestHash = ""
	}
	decision, err := hostedgenesis.ApplyTurnLedger(session.session.TurnLedger, incoming)
	if err != nil {
		if errors.Is(err, hostedgenesis.ErrIdempotencyConflict) {
			return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.conflict", Message: "idempotency key already used for a different request"}
		}
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.conflict", Message: "conversation cannot accept a new turn"}
	}
	session.turnID = decision.Turn.TurnID
	session.replayed = decision.Replayed
	if decision.Replayed {
		return session, nil
	}
	session.session.Status = string(hostedgenesis.StatusInProgress)
	session.session.Model = session.modelSet
	session.session.TurnLedger = decision.Entries
	session.session.LatestTurnID = decision.LatestTurnID
	session.session.MessageCount = decision.MessageCount
	session.session.InputCheckpointRef = decision.Turn.InputCheckpointRef
	session.session.AssistantCheckpointRef = ""
	session.session.Failure = nil
	session.session.RequestID = strings.TrimSpace(requestID)
	session.session.TraceIDs = &hostedgenesis.TraceIDs{
		HostRequestID:   strings.TrimSpace(requestID),
		CorrelationID:   strings.TrimSpace(req.CorrelationID),
		IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
		LesserRequestID: strings.TrimSpace(req.LesserRequestID),
	}
	session.session.UpdatedAt = now
	session.session.CompletedAt = time.Time{}
	return session, nil
}

func buildHostedGenesisIdempotency(instanceSlug string, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, req soulMintConversationRequest, reqHash string, now time.Time, requestID string) *models.SoulMintConversationIdempotency {
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil
	}
	item := &models.SoulMintConversationIdempotency{
		InstanceSlug:   strings.TrimSpace(instanceSlug),
		RegistrationID: strings.TrimSpace(regCtx.reg.ID),
		AgentID:        strings.TrimSpace(regCtx.agentIDHex),
		ConversationID: strings.TrimSpace(session.conversationID),
		TurnID:         strings.TrimSpace(session.turnID),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		RequestHash:    strings.TrimSpace(reqHash),
		RequestID:      strings.TrimSpace(requestID),
		CorrelationID:  strings.TrimSpace(req.CorrelationID),
		Status:         models.SoulMintConversationIdempotencyStatusProcessing,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_ = item.UpdateKeys()
	return item
}

func (s *Server) getHostedGenesisIdempotency(ctx context.Context, instanceSlug string, registrationID string, idempotencyKey string) (*models.SoulMintConversationIdempotency, error) {
	var item models.SoulMintConversationIdempotency
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulMintConversationIdempotency{}).
		Where("PK", "=", models.SoulMintConversationIdempotencyPK(instanceSlug, registrationID, idempotencyKey)).
		Where("SK", "=", "STATE").
		First(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Server) persistHostedGenesisAcceptedTurn(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, updatedMessages []soulMintConversationMessage, messagesJSON string, idem *models.SoulMintConversationIdempotency, ledgerRequestID string, hostRequestID string, now time.Time) *apptheory.AppError {
	extraWrites := func(tx core.TransactionBuilder, creditsRequested int64) error {
		sessionForWrite := cloneHostedGenesisSession(session.session)
		if len(sessionForWrite.TurnLedger) > 0 {
			sessionForWrite.TurnLedger[len(sessionForWrite.TurnLedger)-1].ChargedCredits = creditsRequested
		}
		if err := addHostedGenesisSessionWrite(tx, sessionForWrite, session.sessionIsNew, session.expectedVersion, session.expectedStatus); err != nil {
			return err
		}
		if idem != nil {
			tx.Create(idem)
		}
		if session.sessionIsNew {
			conv := &models.SoulAgentMintConversation{
				AgentID:        regCtx.agentIDHex,
				ConversationID: session.conversationID,
				Model:          session.modelSet,
				Messages:       encodeMintConversationBlob(messagesJSON),
				Status:         models.SoulMintConversationStatusInProgress,
				LatestTurnID:   session.turnID,
				RequestID:      strings.TrimSpace(hostRequestID),
				ChargedCredits: creditsRequested,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if idem != nil {
				conv.CorrelationID = idem.CorrelationID
				conv.IdempotencyKey = idem.IdempotencyKey
			}
			_ = conv.UpdateKeys()
			tx.Create(conv)
			return nil
		}

		update := &models.SoulAgentMintConversation{
			AgentID:        regCtx.agentIDHex,
			ConversationID: session.conversationID,
		}
		_ = update.UpdateKeys()
		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("ChargedCredits", creditsRequested)
			ub.Set("Messages", encodeMintConversationBlob(messagesJSON))
			ub.Set("Status", models.SoulMintConversationStatusInProgress)
			ub.Set("StatusReason", "")
			ub.Set("LatestTurnID", session.turnID)
			ub.Set("RequestID", strings.TrimSpace(hostRequestID))
			ub.Set("UpdatedAt", now)
			if idem != nil {
				ub.Set("CorrelationID", strings.TrimSpace(idem.CorrelationID))
				ub.Set("IdempotencyKey", strings.TrimSpace(idem.IdempotencyKey))
			}
			return nil
		}, tabletheory.IfExists())
		return nil
	}

	if _, appErr := s.debitSoulMintConversationCredits(
		ctx,
		regCtx.inst,
		soulMintConversationStreamModule,
		session.conversationID,
		strings.TrimSpace(ledgerRequestID),
		soulMintConversationStreamBaseCredits,
		now,
		extraWrites,
	); appErr != nil {
		return appErr
	}
	_ = updatedMessages
	return nil
}

func cloneHostedGenesisSession(session *models.HostedGenesisSession) *models.HostedGenesisSession {
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
	if session.Failure != nil {
		failure := *session.Failure
		copy.Failure = &failure
	}
	if session.TraceIDs != nil {
		trace := *session.TraceIDs
		copy.TraceIDs = &trace
	}
	return &copy
}

func addHostedGenesisSessionWrite(tx core.TransactionBuilder, session *models.HostedGenesisSession, create bool, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if tx == nil || session == nil {
		return fmt.Errorf("hosted genesis session write is invalid")
	}
	if create {
		if err := session.BeforeCreate(); err != nil {
			return err
		}
		tx.Create(session)
		return nil
	}
	if err := session.BeforeUpdate(); err != nil {
		return err
	}
	if err := hostedgenesis.ValidateTransition(expectedStatus, hostedgenesis.Status(session.Status)); err != nil {
		return err
	}
	tx.UpdateWithBuilder(session, func(ub core.UpdateBuilder) error {
		ub.Set("Status", session.Status)
		ub.Set("Model", session.Model)
		ub.Set("LatestTurnID", session.LatestTurnID)
		ub.Set("MessageCount", session.MessageCount)
		ub.Set("TurnLedger", session.TurnLedger)
		ub.Set("InputCheckpointRef", session.InputCheckpointRef)
		ub.Set("AssistantCheckpointRef", session.AssistantCheckpointRef)
		ub.Set("ExecutionStateRef", session.ExecutionStateRef)
		ub.Set("MicroVMExecutionID", session.MicroVMExecutionID)
		ub.Set("MicroVMLifecycleRef", session.MicroVMLifecycleRef)
		ub.Set("DeclarationCheckpoint", session.DeclarationCheckpoint)
		ub.Set("Failure", session.Failure)
		ub.Set("TraceIDs", session.TraceIDs)
		ub.Set("RequestID", session.RequestID)
		ub.Set("UpdatedAt", session.UpdatedAt)
		ub.Set("CompletedAt", session.CompletedAt)
		ub.Add("Version", int64(1))
		return nil
	}, tabletheory.IfExists(), tabletheory.AtVersion(expectedVersion), tabletheory.Condition("Status", "=", string(expectedStatus)))
	return nil
}

func (s *Server) startHostedGenesisDeclarationExtraction(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) error {
	if convCtx.session == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	now := time.Now().UTC()
	// H1.3 (kills G7): the pending extraction is serviced by dispatching a
	// follow-on M16 controller run command on the same MicroVM session via the
	// MicroVMDispatcher seam. The dispatch fires exactly once, on the transition
	// into declaration_extraction_pending (the debit/persist block below). An
	// already-pending session has already dispatched its extraction; re-entry is
	// a no-op here and the recover path reconciles the in-VM extraction. The
	// pending status is no longer a permanent trap: the VM services the
	// extraction or the session fails loudly. An unwired dispatcher is
	// fail-closed and loud; there is no silent fallback to a non-MicroVM
	// extraction path. M4 demotes hosted-genesis SQS to operator/backfill
	// recovery only; this path does not rely on queue delivery, DLQ state, or
	// AI-worker liveness.
	transitioned := hostedgenesis.NormalizeStatus(convCtx.session.Status) != hostedgenesis.StatusDeclarationExtractionPending
	if !transitioned {
		return nil
	}
	expectedVersion := convCtx.session.Version
	expectedStatus := hostedgenesis.NormalizeStatus(convCtx.session.Status)
	convCtx.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	convCtx.session.RequestID = strings.TrimSpace(ctx.RequestID)
	convCtx.session.UpdatedAt = now
	creditsDebited, appErr := s.debitSoulMintConversationCredits(
		ctx.Context(),
		convCtx.inst,
		soulMintConversationExtractModule,
		convCtx.conversationID,
		firstNonEmpty(convCtx.conv.IdempotencyKey, ctx.RequestID),
		soulMintConversationExtractBaseCredits,
		now,
		func(tx core.TransactionBuilder, creditsRequested int64) error {
			if err := addHostedGenesisSessionWrite(tx, convCtx.session, false, expectedVersion, expectedStatus); err != nil {
				return err
			}
			if convCtx.conv != nil {
				update := &models.SoulAgentMintConversation{AgentID: convCtx.agentIDHex, ConversationID: convCtx.conversationID}
				_ = update.UpdateKeys()
				tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
					ub.Add("ChargedCredits", creditsRequested)
					ub.Set("Status", models.SoulMintConversationStatusDeclarationExtractionPending)
					ub.Set("StatusReason", "")
					ub.Set("RequestID", strings.TrimSpace(ctx.RequestID))
					ub.Set("UpdatedAt", now)
					return nil
				}, tabletheory.IfExists())
			}
			return nil
		},
	)
	if appErr != nil {
		return appErr
	}
	if convCtx.conv != nil {
		convCtx.conv.ChargedCredits += creditsDebited
		convCtx.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
		convCtx.conv.StatusReason = ""
		convCtx.conv.RequestID = strings.TrimSpace(ctx.RequestID)
		convCtx.conv.UpdatedAt = now
	}
	return s.dispatchHostedGenesisDeclarationExtraction(ctx, convCtx, now)
}

// dispatchHostedGenesisDeclarationExtraction issues the follow-on M16
// controller run command that services a declaration_extraction_pending session
// inside the MicroVM. It refreshes the non-authoritative lifecycle ref on the
// authoritative HostedGenesisSession so the recover path can reconstruct the
// extraction. It fails closed and loudly when the dispatcher is unwired or the
// dispatch is rejected; it never falls back to a synchronous control-plane
// extraction path.
func (s *Server) dispatchHostedGenesisDeclarationExtraction(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, now time.Time) error {
	if s.hostedGenesisMicroVMDispatcher == nil {
		log.Printf("controlplane: hosted genesis extraction dispatch unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID))
		return &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM extraction dispatch is unavailable"}
	}
	binding := convCtx.session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis extraction binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM extraction binding is invalid"}
	}
	runCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisAcceptedTurnDispatchTimeout)
	defer cancel()
	dispatch, dispatchErr := s.hostedGenesisMicroVMDispatcher.DispatchMicroVMRun(runCtx, strings.TrimSpace(ctx.RequestID), binding)
	if dispatchErr != nil {
		log.Printf("controlplane: hosted genesis extraction dispatch failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), dispatchErr)
		return &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM extraction dispatch failed"}
	}
	progressedSession := cloneHostedGenesisSession(convCtx.session)
	if err := progressedSession.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); err != nil {
		log.Printf("controlplane: hosted genesis extraction lifecycle ref rejected agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return &apptheory.AppError{Code: "app.internal", Message: "failed to record microvm extraction dispatch"}
	}
	progressedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedSession.UpdatedAt = now
	if err := s.persistHostedGenesisExtractionDispatch(ctx.Context(), convCtx.session, progressedSession, convCtx.session.Version, now); err != nil {
		return err
	}
	convCtx.session = progressedSession
	return nil
}

// persistHostedGenesisExtractionDispatch records the refreshed MicroVM
// lifecycle ref on the authoritative HostedGenesisSession after the follow-on
// extraction run dispatch. The durable status stays
// declaration_extraction_pending; only the non-authoritative execution/cache
// ref is refreshed under expected-version/expected-status optimistic locking.
func (s *Server) persistHostedGenesisExtractionDispatch(ctx context.Context, accepted *models.HostedGenesisSession, progressed *models.HostedGenesisSession, expectedVersion int64, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || accepted == nil || progressed == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		return addHostedGenesisSessionWrite(tx, progressed, false, expectedVersion, hostedgenesis.StatusDeclarationExtractionPending)
	}); err != nil {
		log.Printf("controlplane: hosted genesis extraction dispatch persist failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(progressed.AgentID), soulMintInstanceReadAuditHash(progressed.ConversationID), err)
		return &apptheory.AppError{Code: "app.internal", Message: "failed to persist microvm extraction dispatch"}
	}
	return nil
}
