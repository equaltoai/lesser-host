package controlplane

import (
	"context"
	"encoding/json"
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
	isNew            bool
	conv             *models.SoulAgentMintConversation
}

func (s *Server) handleSoulMintConversationForRegistrationAsync(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, instanceSlug string) (*apptheory.Response, error) {
	if publishGuardErr := s.ensureMintConversationAgentNotPublished(ctx.Context(), regCtx.agentIDHex); publishGuardErr != nil {
		return nil, publishGuardErr
	}
	req, message, err := requireMintConversationMessage(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateHostedGenesisRequestIDs(&req); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	instanceSlug = firstNonEmpty(instanceSlug, regCtx.inst.Slug)
	reqHash := hostedGenesisRequestHash(regCtx.reg.ID, "", req.Model, message)
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		if replay, replayMsg, replayErr := s.replayHostedGenesisIdempotency(ctx, regCtx, instanceSlug, req, reqHash); replayErr != nil {
			return nil, replayErr
		} else if replay != nil {
			if enqueueErr := s.enqueueHostedGenesisTurn(ctx.Context(), replayMsg); enqueueErr != nil {
				return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue hosted genesis turn"}
			}
			return hostedGenesisConversationJSON(http.StatusAccepted, replay, hostedGenesisProjectionOptions{
				RegistrationID:  regCtx.reg.ID,
				RequestID:       strings.TrimSpace(ctx.RequestID),
				CollapseCreated: true,
				CorrelationID:   req.CorrelationID,
				IdempotencyKey:  req.IdempotencyKey,
				LesserRequestID: req.LesserRequestID,
			})
		}
	}

	session, appErr := s.loadHostedGenesisTurnSession(ctx.Context(), regCtx.agentIDHex, req.ConversationID, req.Model)
	if appErr != nil {
		return nil, appErr
	}
	if session.modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	if _, appErr := s.apiKeyForMintConversationModel(ctx.Context(), session.modelSet); appErr != nil {
		// Validate provider configuration before debiting or enqueueing. The worker
		// reloads the key for the actual LLM call.
		return nil, appErr
	}

	updatedMessages := append(append([]soulMintConversationMessage(nil), session.existingMessages...), soulMintConversationMessage{Role: "user", Content: message})
	messagesJSON, err := json.Marshal(updatedMessages)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to serialize conversation"}
	}
	idem := buildHostedGenesisIdempotency(instanceSlug, regCtx, session, req, reqHash, now, strings.TrimSpace(ctx.RequestID))
	if appErr := s.persistHostedGenesisAcceptedTurn(ctx.Context(), regCtx, session, updatedMessages, string(messagesJSON), idem, firstNonEmpty(req.IdempotencyKey, ctx.RequestID), strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		return nil, appErr
	}

	conv := session.conv
	if conv == nil {
		conv = &models.SoulAgentMintConversation{AgentID: regCtx.agentIDHex, ConversationID: session.conversationID, Model: session.modelSet, CreatedAt: now}
	}
	conv.Messages = string(messagesJSON)
	conv.Status = models.SoulMintConversationStatusInProgress
	conv.LatestTurnID = session.turnID
	conv.RequestID = strings.TrimSpace(ctx.RequestID)
	conv.CorrelationID = strings.TrimSpace(req.CorrelationID)
	conv.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	conv.UpdatedAt = now
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}

	msg := hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           hostedgenesis.StepAssistantTurn,
		RegistrationID: strings.TrimSpace(regCtx.reg.ID),
		InstanceSlug:   strings.TrimSpace(instanceSlug),
		AgentID:        strings.TrimSpace(regCtx.agentIDHex),
		ConversationID: strings.TrimSpace(session.conversationID),
		TurnID:         strings.TrimSpace(session.turnID),
		RequestID:      strings.TrimSpace(ctx.RequestID),
		CorrelationID:  strings.TrimSpace(req.CorrelationID),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	}
	if enqueueErr := s.enqueueHostedGenesisTurn(ctx.Context(), msg); enqueueErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to enqueue hosted genesis turn"}
	}

	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), regCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(regCtx.reg, now))
	previousPromotion := cloneSoulAgentPromotion(promotion)
	promotion = updateSoulAgentPromotionForConversation(promotion, session.conversationID, models.SoulMintConversationStatusInProgress, now)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if shouldEmitSoulPromotionReviewStartedEvent(previousPromotion, promotion, session.conversationID) {
		if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
			EventType:      models.SoulAgentPromotionEventTypeReviewStarted,
			RequestID:      strings.TrimSpace(ctx.RequestID),
			ConversationID: session.conversationID,
			OccurredAt:     now,
		})); appErr != nil {
			return nil, appErr
		}
	}

	return hostedGenesisConversationJSON(http.StatusAccepted, conv, hostedGenesisProjectionOptions{
		RegistrationID:  regCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
		CorrelationID:   req.CorrelationID,
		IdempotencyKey:  req.IdempotencyKey,
		LesserRequestID: req.LesserRequestID,
	})
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

func (s *Server) loadHostedGenesisTurnSession(ctx context.Context, agentIDHex string, requestedConversationID string, requestedModel string) (hostedGenesisTurnSession, *apptheory.AppError) {
	session := hostedGenesisTurnSession{
		conversationID: strings.TrimSpace(requestedConversationID),
		modelSet:       strings.TrimSpace(requestedModel),
	}
	turn, err := newToken(16)
	if err != nil {
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.internal", Message: "failed to create turn id"}
	}
	session.turnID = "turn_" + turn
	if session.conversationID == "" {
		if session.modelSet == "" {
			session.modelSet = defaultSoulMintConversationModel
		}
		token, err := newToken(16)
		if err != nil {
			return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.internal", Message: "failed to create conversation id"}
		}
		session.conversationID = token
		session.isNew = true
		return session, nil
	}

	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", session.conversationID))
	if err != nil {
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	decodeMintConversationFields(conv)
	status := strings.TrimSpace(conv.Status)
	if status != models.SoulMintConversationStatusInProgress && status != models.SoulMintConversationStatusAssistantTurnReady && status != models.SoulMintConversationStatusCreated {
		return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.conflict", Message: "conversation cannot accept a new turn"}
	}

	storedModel := strings.TrimSpace(conv.Model)
	if storedModel != "" {
		if session.modelSet != "" && !strings.EqualFold(storedModel, session.modelSet) {
			return hostedGenesisTurnSession{}, &apptheory.AppError{Code: "app.conflict", Message: "cannot change model for an existing conversation"}
		}
		session.modelSet = storedModel
	}
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &session.existingMessages)
	}
	session.existingUsage = conv.Usage
	session.conv = conv
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

func (s *Server) replayHostedGenesisIdempotency(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, instanceSlug string, req soulMintConversationRequest, reqHash string) (*models.SoulAgentMintConversation, hostedgenesis.QueueMessage, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || strings.TrimSpace(req.IdempotencyKey) == "" {
		return nil, hostedgenesis.QueueMessage{}, nil
	}
	item, err := s.getHostedGenesisIdempotency(ctx.Context(), instanceSlug, regCtx.reg.ID, req.IdempotencyKey)
	if theoryErrors.IsNotFound(err) {
		return nil, hostedgenesis.QueueMessage{}, nil
	}
	if err != nil {
		return nil, hostedgenesis.QueueMessage{}, &apptheory.AppError{Code: "app.internal", Message: "failed to load idempotency key"}
	}
	if strings.TrimSpace(item.RequestHash) != strings.TrimSpace(reqHash) {
		return nil, hostedgenesis.QueueMessage{}, &apptheory.AppError{Code: "app.conflict", Message: "idempotency key already used for a different request"}
	}
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), regCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", item.ConversationID))
	if err != nil {
		return nil, hostedgenesis.QueueMessage{}, &apptheory.AppError{Code: "app.internal", Message: "idempotent conversation is unavailable"}
	}
	decodeMintConversationFields(conv)
	msg := hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           hostedgenesis.StepAssistantTurn,
		RegistrationID: strings.TrimSpace(regCtx.reg.ID),
		InstanceSlug:   strings.TrimSpace(instanceSlug),
		AgentID:        strings.TrimSpace(regCtx.agentIDHex),
		ConversationID: strings.TrimSpace(item.ConversationID),
		TurnID:         strings.TrimSpace(item.TurnID),
		RequestID:      strings.TrimSpace(ctx.RequestID),
		CorrelationID:  firstNonEmpty(req.CorrelationID, item.CorrelationID),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	}
	return conv, msg, nil
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
		if idem != nil {
			tx.Create(idem)
		}
		if session.isNew {
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
	if convCtx.conv == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	now := time.Now().UTC()
	if strings.TrimSpace(convCtx.conv.Status) != models.SoulMintConversationStatusDeclarationExtractionPending {
		creditsDebited, appErr := s.debitSoulMintConversationCredits(
			ctx.Context(),
			convCtx.inst,
			soulMintConversationExtractModule,
			convCtx.conversationID,
			firstNonEmpty(convCtx.conv.IdempotencyKey, ctx.RequestID),
			soulMintConversationExtractBaseCredits,
			now,
			func(tx core.TransactionBuilder, creditsRequested int64) error {
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
				return nil
			},
		)
		if appErr != nil {
			return appErr
		}
		convCtx.conv.ChargedCredits += creditsDebited
		convCtx.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
		convCtx.conv.StatusReason = ""
		convCtx.conv.RequestID = strings.TrimSpace(ctx.RequestID)
		convCtx.conv.UpdatedAt = now
	}
	return s.enqueueHostedGenesisTurn(ctx.Context(), hostedgenesis.QueueMessage{
		Kind:           hostedgenesis.QueueMessageKind,
		Step:           hostedgenesis.StepDeclarationExtraction,
		RegistrationID: strings.TrimSpace(convCtx.reg.ID),
		InstanceSlug:   strings.TrimSpace(convCtx.instanceSlug),
		AgentID:        strings.TrimSpace(convCtx.agentIDHex),
		ConversationID: strings.TrimSpace(convCtx.conversationID),
		TurnID:         strings.TrimSpace(convCtx.conv.LatestTurnID),
		RequestID:      strings.TrimSpace(ctx.RequestID),
		CorrelationID:  strings.TrimSpace(convCtx.conv.CorrelationID),
		IdempotencyKey: strings.TrimSpace(convCtx.conv.IdempotencyKey),
	})
}
