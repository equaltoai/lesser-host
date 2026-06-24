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

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type hostedGenesisTurnSession struct {
	conversationID   string
	turnID           string
	modelSet         string
	existingMessages []soulMintConversationMessage
	existingUsage    models.AIUsage
	sessionIsNew     bool
	expectedStatus   hostedgenesis.Status
	expectedVersion  int64
	replayed         bool
	requestHash      string
	idempotency      *models.SoulMintConversationIdempotency
	conv             *models.SoulAgentMintConversation
	session          *models.HostedGenesisSession
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
	session, appErr := s.loadHostedGenesisTurnSession(ctx.Context(), regCtx, instanceSlug, req, message, strings.TrimSpace(ctx.RequestID), now)
	if appErr != nil {
		return nil, appErr
	}
	if session.modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	if session.replayed {
		return hostedGenesisConversationJSONFromSession(http.StatusAccepted, session.session, session.conv, hostedGenesisProjectionOptions{
			RegistrationID:  regCtx.reg.ID,
			RequestID:       strings.TrimSpace(ctx.RequestID),
			CollapseCreated: true,
			CorrelationID:   req.CorrelationID,
			IdempotencyKey:  req.IdempotencyKey,
			LesserRequestID: req.LesserRequestID,
		})
	}
	if _, appErr := s.apiKeyForMintConversationModel(ctx.Context(), session.modelSet); appErr != nil {
		// Validate provider configuration before debiting or enqueueing. The worker
		// reloads the key for the actual LLM call.
		return nil, appErr
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
	if enqueueErr := s.enqueueHostedGenesisTurn(ctx.Context(), buildHostedGenesisAssistantQueueMessage(regCtx, instanceSlug, session, req, strings.TrimSpace(ctx.RequestID))); enqueueErr != nil {
		log.Printf("controlplane: hosted genesis session accepted without queue delivery agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), enqueueErr)
	}

	if appErr := s.saveHostedGenesisAcceptedPromotion(ctx.Context(), regCtx, session, strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		log.Printf("controlplane: hosted genesis session accepted without promotion update agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), appErr)
	}

	return hostedGenesisConversationJSONFromSession(http.StatusAccepted, session.session, conv, hostedGenesisProjectionOptions{
		RegistrationID:  regCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
		CorrelationID:   req.CorrelationID,
		IdempotencyKey:  req.IdempotencyKey,
		LesserRequestID: req.LesserRequestID,
	})
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

func buildHostedGenesisAssistantQueueMessage(regCtx mintConversationRegistrationContext, instanceSlug string, session hostedGenesisTurnSession, req soulMintConversationRequest, requestID string) hostedgenesis.QueueMessage {
	return hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           hostedgenesis.StepAssistantTurn,
		RegistrationID: strings.TrimSpace(regCtx.reg.ID),
		InstanceSlug:   strings.TrimSpace(instanceSlug),
		AgentID:        strings.TrimSpace(regCtx.agentIDHex),
		ConversationID: strings.TrimSpace(session.conversationID),
		TurnID:         strings.TrimSpace(session.turnID),
		RequestID:      strings.TrimSpace(requestID),
		CorrelationID:  strings.TrimSpace(req.CorrelationID),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	}
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
	if err != nil || conv == nil {
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

func (s *Server) enqueueHostedGenesisTurn(ctx context.Context, msg hostedgenesis.QueueMessage) error {
	if s == nil || s.enqueueHostedGenesisMessage == nil {
		return fmt.Errorf("hosted genesis queue is not configured")
	}
	if strings.TrimSpace(msg.Kind) == "" {
		msg.Kind = hostedgenesis.QueueMessageKind
	}
	if err := s.enqueueHostedGenesisMessage(ctx, msg); err != nil {
		log.Printf("controlplane: enqueue hosted genesis turn failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(msg.AgentID), soulMintInstanceReadAuditHash(msg.ConversationID), err)
		return err
	}
	return nil
}

func (s *Server) startHostedGenesisDeclarationExtraction(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) error {
	if convCtx.session == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	now := time.Now().UTC()
	if hostedgenesis.NormalizeStatus(convCtx.session.Status) != hostedgenesis.StatusDeclarationExtractionPending {
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
	}
	msg := hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           hostedgenesis.StepDeclarationExtraction,
		RegistrationID: strings.TrimSpace(convCtx.reg.ID),
		InstanceSlug:   strings.TrimSpace(convCtx.instanceSlug),
		AgentID:        strings.TrimSpace(convCtx.agentIDHex),
		ConversationID: strings.TrimSpace(convCtx.conversationID),
		TurnID:         strings.TrimSpace(convCtx.session.LatestTurnID),
		RequestID:      strings.TrimSpace(ctx.RequestID),
	}
	if convCtx.session.TraceIDs != nil {
		msg.CorrelationID = strings.TrimSpace(convCtx.session.TraceIDs.CorrelationID)
		msg.IdempotencyKey = strings.TrimSpace(convCtx.session.TraceIDs.IdempotencyKey)
	}
	if err := s.enqueueHostedGenesisTurn(ctx.Context(), msg); err != nil {
		log.Printf("controlplane: hosted genesis declaration extraction marked pending without queue delivery agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
	}
	return nil
}
