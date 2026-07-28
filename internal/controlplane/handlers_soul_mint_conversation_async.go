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

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
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
	waitOnly           bool
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
	if appErr := requireHostedGenesisAcceptedTurnModel(session); appErr != nil {
		return nil, appErr
	}
	if appErr := requireHostedGenesisMicroVMBindingReady(regCtx, session.session, instanceSlug, session.conversationID); appErr != nil {
		return nil, appErr
	}
	if session.waitOnly || session.replayed {
		return s.handleHostedGenesisWaitOnlyOrReplayedTurn(ctx, regCtx, session, req)
	}
	updatedMessages, messagesJSON, err := serializeHostedGenesisAcceptedTurn(session, message)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to serialize conversation")
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

	progressedSession, progressedConv, status, progressErr := s.progressHostedGenesisAcceptedTurn(ctx.Context(), regCtx, session, conv, updatedMessages, strings.TrimSpace(ctx.RequestID))
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

func (s *Server) handleHostedGenesisWaitOnlyOrReplayedTurn(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, req soulMintConversationRequest) (*apptheory.Response, error) {
	if session.waitOnly {
		return s.handleHostedGenesisWaitOnlyTurn(ctx, regCtx, session, req)
	}
	return s.handleHostedGenesisReplayedTurn(ctx, regCtx, session, req)
}

func requireHostedGenesisAcceptedTurnModel(session hostedGenesisTurnSession) *apptheory.AppTheoryError {
	if session.waitOnly || session.modelSet != "" {
		return nil
	}
	return newAppTheoryError("app.bad_request", "model is required")
}

func (s *Server) handleHostedGenesisReplayedTurn(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, req soulMintConversationRequest) (*apptheory.Response, error) {
	if hostedGenesisReplayedTurnNeedsProgression(session) {
		progressedSession, progressedConv, status, progressErr := s.progressHostedGenesisAcceptedTurn(ctx.Context(), regCtx, session, session.conv, session.existingMessages, strings.TrimSpace(ctx.RequestID))
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

func (s *Server) handleHostedGenesisWaitOnlyTurn(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, req soulMintConversationRequest) (*apptheory.Response, error) {
	return hostedGenesisConversationJSONFromSession(http.StatusAccepted, session.session, session.conv, hostedGenesisProjectionOptions{
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

func (s *Server) progressHostedGenesisAcceptedTurn(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, requestID string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, int, *apptheory.AppTheoryError) {
	if session.session == nil || conv == nil {
		return nil, nil, 0, newAppTheoryError("app.internal", "internal error")
	}
	if err := session.session.MicroVMSessionBinding().Validate(); err != nil {
		log.Printf("controlplane: hosted genesis microvm binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusServiceUnavailable, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM execution dispatch is unavailable")
	}
	if s.enqueueHostedGenesisMessage == nil {
		log.Printf("controlplane: hosted genesis microvm dispatch queue unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID))
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusServiceUnavailable, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM execution dispatch queue is unavailable")
	}
	msg := hostedGenesisMicroVMDispatchQueueMessage(regCtx, session, requestID)
	if err := s.enqueueHostedGenesisMessage(ctx, msg); err != nil {
		log.Printf("controlplane: hosted genesis microvm dispatch enqueue failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx, session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, requestID, time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusServiceUnavailable, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM execution dispatch enqueue failed")
	}
	return cloneHostedGenesisSession(session.session), cloneSoulAgentMintConversation(conv), http.StatusAccepted, nil
}

func hostedGenesisMicroVMDispatchQueueMessage(regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, requestID string) hostedgenesis.QueueMessage {
	msg := hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           hostedgenesis.StepMicroVMDispatch,
		RegistrationID: strings.TrimSpace(regCtx.reg.ID),
		InstanceSlug:   strings.TrimSpace(regCtx.inst.Slug),
		AgentID:        strings.TrimSpace(regCtx.agentIDHex),
		ConversationID: strings.TrimSpace(session.conversationID),
		TurnID:         strings.TrimSpace(session.turnID),
		RequestID:      strings.TrimSpace(requestID),
	}
	if session.idempotency != nil {
		msg.IdempotencyKey = strings.TrimSpace(session.idempotency.IdempotencyKey)
		msg.CorrelationID = strings.TrimSpace(session.idempotency.CorrelationID)
	}
	return msg
}

// persistHostedGenesisAcceptedMicroVMDispatch records the durable in_progress
// HostedGenesisSession after a successful M16 controller run dispatch, applying
// the non-authoritative MicroVM execution/cache lifecycle ref via
// ApplyMicroVMLifecycleRef so the three MicroVM fields
// (MicroVMExecutionID/ExecutionStateRef/MicroVMLifecycleRef) are populated on
// the authoritative Host row. The conversation stays in_progress with no inline
// assistant message: the turn is pending inside the MicroVM and completion is
// reconstructed from Host truth by the recovery/stuck-turn path.
func (s *Server) persistHostedGenesisAcceptedMicroVMDispatch(ctx context.Context, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, dispatch hostedgenesis.MicroVMDispatchResult, requestID string, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	progressedSession := cloneHostedGenesisSession(session.session)
	progressedConv := cloneSoulAgentMintConversation(conv)
	if err := progressedSession.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); err != nil {
		log.Printf("controlplane: hosted genesis microvm lifecycle ref rejected agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(conv.AgentID), soulMintInstanceReadAuditHash(conv.ConversationID), err)
		return nil, nil, newAppTheoryError("app.internal", "failed to record microvm dispatch")
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

func (s *Server) persistHostedGenesisAcceptedTurnFailure(ctx context.Context, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage, reason string, requestID string, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	messagesJSON, err := json.Marshal(acceptedMessages)
	if err != nil {
		return nil, nil, newAppTheoryError("app.internal", "failed to serialize conversation")
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

func (s *Server) persistHostedGenesisProgression(ctx context.Context, accepted hostedGenesisTurnSession, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, messagesJSON string, statusReason string, usage models.AIUsage, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || session == nil || conv == nil {
		return newAppTheoryError("app.internal", "internal error")
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
		return newAppTheoryError("app.internal", "failed to update conversation")
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

func (s *Server) saveHostedGenesisAcceptedPromotion(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, requestID string, now time.Time) *apptheory.AppTheoryError {
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

func (s *Server) loadHostedGenesisTurnSession(ctx context.Context, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppTheoryError) {
	session := hostedGenesisTurnSession{
		conversationID: strings.TrimSpace(req.ConversationID),
		modelSet:       strings.TrimSpace(req.Model),
	}
	if strings.TrimSpace(instanceSlug) == "" {
		return hostedGenesisTurnSession{}, newAppTheoryError("app.internal", "internal error")
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

func (s *Server) applyHostedGenesisIdempotencyLookup(ctx context.Context, session *hostedGenesisTurnSession, instanceSlug string, regCtx mintConversationRegistrationContext, req soulMintConversationRequest) *apptheory.AppTheoryError {
	if session == nil || strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil
	}
	item, err := s.getHostedGenesisIdempotency(ctx, instanceSlug, regCtx.reg.ID, req.IdempotencyKey)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return newAppTheoryError("app.internal", "failed to load idempotency key")
	}
	if item == nil || err != nil {
		return nil
	}
	if session.conversationID != "" && session.conversationID != strings.TrimSpace(item.ConversationID) {
		return newAppTheoryError("app.conflict", "idempotency key already used for a different conversation")
	}
	session.conversationID = strings.TrimSpace(item.ConversationID)
	session.idempotency = item
	return nil
}

func assignHostedGenesisTurnID(session *hostedGenesisTurnSession) *apptheory.AppTheoryError {
	if session == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	turn, err := newToken(16)
	if err != nil {
		return newAppTheoryError("app.internal", "failed to create turn id")
	}
	session.turnID = "turn_" + turn
	return nil
}

func newHostedGenesisTurnSession(session hostedGenesisTurnSession, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppTheoryError) {
	if session.modelSet == "" {
		session.modelSet = defaultSoulMintConversationModel
	}
	token, err := newToken(16)
	if err != nil {
		return hostedGenesisTurnSession{}, newAppTheoryError("app.internal", "failed to create conversation id")
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

func (s *Server) loadExistingHostedGenesisTurnSession(ctx context.Context, session hostedGenesisTurnSession, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppTheoryError) {
	hostSession, err := s.store.GetHostedGenesisSession(ctx, instanceSlug, session.conversationID)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return hostedGenesisTurnSession{}, newAppTheoryError("app.not_found", "conversation not found")
		}
		return hostedGenesisTurnSession{}, newAppTheoryError("app.internal", "failed to load conversation")
	}
	session.session = hostSession
	session.expectedStatus = hostedgenesis.NormalizeStatus(hostSession.Status)
	session.expectedVersion = hostSession.Version
	if appErr := hydrateHostedGenesisSessionRouteBinding(hostSession, regCtx, instanceSlug, session.conversationID); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	s.hydrateHostedGenesisConversationProjection(ctx, &session, regCtx.agentIDHex)
	if hostedGenesisStatusRequiresWait(session.expectedStatus) {
		session.waitOnly = true
		return session, nil
	}
	turnAccepting := hostedGenesisStatusAcceptsTurn(session.expectedStatus)
	replayOnly := session.idempotency != nil && hostedGenesisStatusAcceptsIdempotentReplay(session.expectedStatus)
	if !turnAccepting && !replayOnly {
		return hostedGenesisTurnSession{}, newAppTheoryError("app.conflict", "conversation cannot accept a new turn")
	}
	if turnAccepting {
		if appErr := requireHostedGenesisSessionAcceptsTurn(hostSession); appErr != nil {
			return hostedGenesisTurnSession{}, appErr
		}
	}
	if appErr := applyHostedGenesisSessionModel(&session, hostSession.Model); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	if session.modelSet == "" {
		session.modelSet = defaultSoulMintConversationModel
	}
	finished, appErr := finishHostedGenesisTurnSession(regCtx.reg.ID, session, req, message, requestID, now)
	if appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	if !turnAccepting && !finished.replayed {
		return hostedGenesisTurnSession{}, newAppTheoryError("app.conflict", "conversation cannot accept a new turn")
	}
	return finished, nil
}

func requireHostedGenesisSessionAcceptsTurn(session *models.HostedGenesisSession) *apptheory.AppTheoryError {
	if hostedGenesisStatusAcceptsTurn(hostedgenesis.NormalizeStatus(session.Status)) {
		return nil
	}
	return newAppTheoryError("app.conflict", "conversation cannot accept a new turn")
}

type hostedGenesisRouteBinding struct {
	instanceSlug   string
	registrationID string
	agentID        string
	conversationID string
}

func hydrateHostedGenesisSessionRouteBinding(session *models.HostedGenesisSession, regCtx mintConversationRegistrationContext, instanceSlug string, conversationID string) *apptheory.AppTheoryError {
	if session == nil || regCtx.reg == nil {
		return newAppTheoryError(appTheoryCodeInternal, "internal error")
	}
	binding, appErr := hostedGenesisRouteBindingFromContext(session, regCtx, instanceSlug, conversationID)
	if appErr != nil {
		return appErr
	}
	if !hostedGenesisRouteBindingMatches(session.InstanceSlug, binding.instanceSlug, strings.ToLower) ||
		!hostedGenesisRouteBindingMatches(session.RegistrationID, binding.registrationID, strings.TrimSpace) ||
		!hostedGenesisRouteBindingMatches(session.AgentID, binding.agentID, normalizeHostedGenesisAgentID) ||
		!hostedGenesisRouteBindingMatches(session.ConversationID, binding.conversationID, strings.TrimSpace) {
		return newAppTheoryError(appTheoryCodeConflict, "conversation binding mismatch")
	}
	session.InstanceSlug = binding.instanceSlug
	session.RegistrationID = binding.registrationID
	session.AgentID = binding.agentID
	session.ConversationID = binding.conversationID
	_ = session.UpdateKeys()
	return nil
}

func hostedGenesisRouteBindingFromContext(session *models.HostedGenesisSession, regCtx mintConversationRegistrationContext, instanceSlug string, conversationID string) (hostedGenesisRouteBinding, *apptheory.AppTheoryError) {
	routeSlug := instanceSlug
	if routeSlug == "" && regCtx.inst != nil {
		routeSlug = regCtx.inst.Slug
	}
	routeSlug = strings.ToLower(strings.TrimSpace(routeSlug))
	routeRegistrationID := strings.TrimSpace(regCtx.reg.ID)
	routeAgentID := normalizeHostedGenesisAgentID(firstNonEmpty(regCtx.agentIDHex, regCtx.reg.AgentID))
	routeConversationID := strings.TrimSpace(firstNonEmpty(conversationID, session.ConversationID))
	if routeSlug == "" || routeRegistrationID == "" || routeAgentID == "" || routeConversationID == "" {
		return hostedGenesisRouteBinding{}, newAppTheoryError(appTheoryCodeInternal, "internal error")
	}
	return hostedGenesisRouteBinding{
		instanceSlug:   routeSlug,
		registrationID: routeRegistrationID,
		agentID:        routeAgentID,
		conversationID: routeConversationID,
	}, nil
}

func hostedGenesisRouteBindingMatches(current string, target string, normalize func(string) string) bool {
	current = normalize(current)
	return current == "" || current == target
}

func normalizeHostedGenesisAgentID(agentID string) string {
	return strings.ToLower(strings.TrimSpace(agentID))
}

func requireHostedGenesisMicroVMBindingReady(regCtx mintConversationRegistrationContext, session *models.HostedGenesisSession, instanceSlug string, conversationID string) *apptheory.AppTheoryError {
	if appErr := hydrateHostedGenesisSessionRouteBinding(session, regCtx, instanceSlug, conversationID); appErr != nil {
		return appErr
	}
	if err := session.MicroVMSessionBinding().Validate(); err != nil {
		log.Printf("controlplane: hosted genesis microvm binding invalid before accept persistence agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(conversationID), err)
		return newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM execution dispatch is unavailable")
	}
	return nil
}

func hostedGenesisStatusAcceptsTurn(status hostedgenesis.Status) bool {
	switch status {
	case hostedgenesis.StatusAssistantTurnReady, hostedgenesis.StatusCreated:
		return true
	default:
		return false
	}
}

func hostedGenesisStatusRequiresWait(status hostedgenesis.Status) bool {
	switch status {
	case hostedgenesis.StatusInProgress:
		return true
	default:
		return false
	}
}

func hostedGenesisStatusAcceptsIdempotentReplay(status hostedgenesis.Status) bool {
	switch status {
	case hostedgenesis.StatusDeclarationReady, hostedgenesis.StatusPublished:
		return true
	default:
		return false
	}
}

func applyHostedGenesisSessionModel(session *hostedGenesisTurnSession, storedModel string) *apptheory.AppTheoryError {
	if session == nil || strings.TrimSpace(storedModel) == "" {
		return nil
	}
	if session.modelSet != "" && !strings.EqualFold(storedModel, session.modelSet) {
		return newAppTheoryError("app.conflict", "cannot change model for an existing conversation")
	}
	session.modelSet = strings.TrimSpace(storedModel)
	return nil
}

func (s *Server) hydrateHostedGenesisConversationProjection(ctx context.Context, session *hostedGenesisTurnSession, agentIDHex string) {
	if session == nil {
		return
	}
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", session.conversationID))
	// H1.4 (kills G10b): a public-projection hydrate error is surfaced, not
	// swallowed. A not-found or absent public conversation projection is benign (a new
	// turn has no prior public projection row) and remains a no-op; any other load error
	// is a real storage failure and is logged loudly so it is not silently
	// masked as "no public conversation projection".
	if err != nil {
		if !theoryErrors.IsNotFound(err) {
			log.Printf("controlplane: hosted genesis public conversation projection hydrate failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
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

func finishHostedGenesisTurnSession(registrationID string, session hostedGenesisTurnSession, req soulMintConversationRequest, message string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppTheoryError) {
	session.requestHash = hostedGenesisRequestHash(registrationID, session.conversationID, session.modelSet, message, req.CandidateAction)
	if appErr := validateHostedGenesisIdempotencyRequestHash(registrationID, &session, req, message); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
	}
	return applyHostedGenesisAcceptedTurnToSession(session, req, session.requestHash, requestID, now)
}

func validateHostedGenesisIdempotencyRequestHash(registrationID string, session *hostedGenesisTurnSession, req soulMintConversationRequest, message string) *apptheory.AppTheoryError {
	if session == nil || session.idempotency == nil {
		return nil
	}
	storedHash := strings.TrimSpace(session.idempotency.RequestHash)
	if storedHash == strings.TrimSpace(session.requestHash) {
		return nil
	}
	modelSet := firstNonEmpty(req.Model, session.modelSet)
	requestedHash := hostedGenesisRequestHash(registrationID, req.ConversationID, modelSet, message, req.CandidateAction)
	withoutConversationHash := hostedGenesisRequestHash(registrationID, "", modelSet, message, req.CandidateAction)
	if storedHash == strings.TrimSpace(requestedHash) || storedHash == strings.TrimSpace(withoutConversationHash) {
		session.requestHash = storedHash
		return nil
	}
	return newAppTheoryError("app.conflict", "idempotency key already used for a different request")
}

func applyHostedGenesisAcceptedTurnToSession(session hostedGenesisTurnSession, req soulMintConversationRequest, reqHash string, requestID string, now time.Time) (hostedGenesisTurnSession, *apptheory.AppTheoryError) {
	if session.session == nil {
		return hostedGenesisTurnSession{}, newAppTheoryError("app.internal", "internal error")
	}
	if !session.sessionIsNew && session.session.DeclarationCandidate == nil {
		return hostedGenesisTurnSession{}, newAppTheoryError("app.conflict", "restart_soul_bootstrap is required for a legacy hosted genesis lane")
	}
	incoming := hostedgenesis.TurnLedgerEntry{
		TurnID:             strings.TrimSpace(session.turnID),
		IdempotencyKey:     strings.TrimSpace(req.IdempotencyKey),
		RequestHash:        strings.TrimSpace(reqHash),
		InputCheckpointRef: hostedgenesis.CheckpointRef("input", session.conversationID, session.turnID),
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
			return hostedGenesisTurnSession{}, newAppTheoryError("app.conflict", "idempotency key already used for a different request")
		}
		return hostedGenesisTurnSession{}, newAppTheoryError("app.conflict", "conversation cannot accept a new turn")
	}
	session.turnID = decision.Turn.TurnID
	session.replayed = decision.Replayed
	if decision.Replayed {
		return session, nil
	}
	if appErr := advanceHostedGenesisCandidateForAcceptedTurn(&session, req.CandidateAction, decision.Turn.TurnID, now); appErr != nil {
		return hostedGenesisTurnSession{}, appErr
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

func advanceHostedGenesisCandidateForAcceptedTurn(session *hostedGenesisTurnSession, action *hostedgenesis.DeclarationCandidateAction, turnID string, now time.Time) *apptheory.AppTheoryError {
	if appErr := validateHostedGenesisCandidateActionPhase(session.session.DeclarationCandidate, action); appErr != nil {
		return appErr
	}
	if session.sessionIsNew {
		candidate, err := newHostedGenesisCandidateForAcceptedTurn(*session, turnID, now)
		if err != nil {
			return newAppTheoryError("app.internal", "failed to initialize typed declaration candidate")
		}
		session.session.DeclarationCandidate = candidate
	}
	if action != nil {
		candidate, err := hostedgenesis.ApplyDeclarationCandidateAction(session.session.DeclarationCandidate, *action, turnID, now)
		if err != nil {
			return newAppTheoryError("app.conflict", "candidate_action does not match the exact owner review")
		}
		session.session.DeclarationCandidate = candidate
		return nil
	}
	candidate := session.session.DeclarationCandidate.Clone()
	candidate.SourceTurnID = turnID
	candidate.UpdatedAt = now
	if err := candidate.Validate(); err != nil {
		return newAppTheoryError("app.conflict", "typed declaration candidate cannot bind the accepted turn")
	}
	session.session.DeclarationCandidate = candidate
	return nil
}

func validateHostedGenesisCandidateActionPhase(candidate *hostedgenesis.DeclarationCandidate, action *hostedgenesis.DeclarationCandidateAction) *apptheory.AppTheoryError {
	if candidate == nil {
		return nil
	}
	if candidate.Phase == hostedgenesis.DeclarationCandidatePhaseReview && action == nil {
		return newAppTheoryError("app.conflict", "a structurally bound candidate_action is required for owner review")
	}
	if candidate.Phase != hostedgenesis.DeclarationCandidatePhaseReview && action != nil {
		return newAppTheoryError("app.conflict", "candidate_action is only valid for the current owner review")
	}
	return nil
}

func newHostedGenesisCandidateForAcceptedTurn(session hostedGenesisTurnSession, turnID string, now time.Time) (*hostedgenesis.DeclarationCandidate, error) {
	return hostedgenesis.NewDeclarationCandidate(hostedgenesis.DeclarationCandidateBinding{
		InstanceSlug: session.session.InstanceSlug, RegistrationID: session.session.RegistrationID,
		AgentID: session.session.AgentID, ConversationID: session.session.ConversationID,
		SourceTurnID: turnID, Model: session.modelSet,
	}, now)
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

func (s *Server) persistHostedGenesisAcceptedTurn(ctx context.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, updatedMessages []soulMintConversationMessage, messagesJSON string, idem *models.SoulMintConversationIdempotency, ledgerRequestID string, hostRequestID string, now time.Time) *apptheory.AppTheoryError {
	// A durable session row that fails its own model invariants (or an illegal
	// status move) aborts the transaction builder before anything is written.
	// That rejection is captured here so it is reported as the typed, recoverable
	// hosted genesis conflict it is, instead of being laundered by the credit
	// debit into an untyped app.internal "failed to debit credits" (#1003).
	var sessionWriteRejection error
	extraWrites := func(tx core.TransactionBuilder, creditsRequested int64) error {
		sessionForWrite := cloneHostedGenesisSession(session.session)
		if len(sessionForWrite.TurnLedger) > 0 {
			sessionForWrite.TurnLedger[len(sessionForWrite.TurnLedger)-1].ChargedCredits = creditsRequested
		}
		if err := addHostedGenesisSessionWrite(tx, sessionForWrite, session.sessionIsNew, session.expectedVersion, session.expectedStatus); err != nil {
			if hostedGenesisSessionWriteRejectionFrom(err) != nil {
				sessionWriteRejection = err
			}
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
		if rejection := hostedGenesisSessionWriteRejectionFrom(sessionWriteRejection); rejection != nil {
			log.Printf("controlplane: hosted genesis session write rejected agent_hash=%s conversation_hash=%s reason=%s recovery_action=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), rejection.reason, rejection.recoveryAction, rejection)
			return rejection.appTheoryError()
		}
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
	copy.DeclarationCandidate = session.DeclarationCandidate.Clone()
	if session.Failure != nil {
		failure := *session.Failure
		copy.Failure = &failure
	}
	if session.TraceIDs != nil {
		trace := *session.TraceIDs
		copy.TraceIDs = &trace
	}
	if session.VMCheckpoint != nil {
		checkpoint := *session.VMCheckpoint
		copy.VMCheckpoint = &checkpoint
	}
	return &copy
}

func addHostedGenesisSessionWrite(tx core.TransactionBuilder, session *models.HostedGenesisSession, create bool, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if tx == nil || session == nil {
		return fmt.Errorf("hosted genesis session write is invalid")
	}
	if create {
		if err := session.BeforeCreate(); err != nil {
			return newHostedGenesisSessionStateRejection(err)
		}
		tx.Create(session)
		return nil
	}
	if err := session.BeforeUpdate(); err != nil {
		return newHostedGenesisSessionStateRejection(err)
	}
	if err := hostedgenesis.ValidateTransition(expectedStatus, hostedgenesis.Status(session.Status)); err != nil {
		return newHostedGenesisSessionTransitionRejection(err)
	}
	tx.UpdateWithBuilder(session, func(ub core.UpdateBuilder) error {
		ub.Set("InstanceSlug", session.InstanceSlug)
		ub.Set("RegistrationID", session.RegistrationID)
		ub.Set("AgentID", session.AgentID)
		ub.Set("ConversationID", session.ConversationID)
		ub.Set("GSI1PK", session.GSI1PK)
		ub.Set("GSI1SK", session.GSI1SK)
		ub.Set("GSI2PK", session.GSI2PK)
		ub.Set("GSI2SK", session.GSI2SK)
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
		ub.Set("DeclarationCandidate", session.DeclarationCandidate)
		ub.Set("CandidateRevision", session.CandidateRevision)
		ub.Set("CandidateHash", session.CandidateHash)
		ub.Set("CandidatePhase", session.CandidatePhase)
		ub.Set("Failure", session.Failure)
		ub.Set("TraceIDs", session.TraceIDs)
		ub.Set("VMCheckpoint", session.VMCheckpoint)
		ub.Set("RequestID", session.RequestID)
		ub.Set("UpdatedAt", session.UpdatedAt)
		ub.Set("CompletedAt", session.CompletedAt)
		ub.Add("Version", int64(1))
		return nil
	}, tabletheory.IfExists(), tabletheory.AtVersion(expectedVersion), tabletheory.Condition("Status", "=", string(expectedStatus)))
	return nil
}
