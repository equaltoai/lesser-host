package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) completeSoulMintConversationForRegistration(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, conv *models.SoulAgentMintConversation, conversationID string) (*apptheory.Response, error) {
	return s.completeSoulMintConversationForRegistrationWithProjection(ctx, regCtx, conv, conversationID, nil)
}

func (s *Server) completeSoulMintConversationForRegistrationWithProjection(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, conv *models.SoulAgentMintConversation, conversationID string, projectionOpts *hostedGenesisProjectionOptions) (*apptheory.Response, error) {
	if projectionOpts == nil {
		return s.completeLegacySoulMintConversationForRegistration(ctx, regCtx, conv, conversationID)
	}
	return s.completeHostedGenesisSoulMintConversationForRegistration(ctx, regCtx, conv, conversationID, *projectionOpts)
}

func (s *Server) completeLegacySoulMintConversationForRegistration(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, conv *models.SoulAgentMintConversation, conversationID string) (*apptheory.Response, error) {
	now := time.Now().UTC()
	if appErr := requireMintConversationDurableAssistantTurn(conv); appErr != nil {
		return nil, appErr
	}
	declarationsJSON, extractUsage, appErr := s.resolveMintConversationCompletion(ctx, regCtx, conv, conversationID, now)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.persistCompletedMintConversation(ctx.Context(), conv, declarationsJSON, extractUsage, now); appErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to complete conversation")
	}
	if appErr := s.saveMintConversationFinalizeReadyPromotion(ctx.Context(), regCtx, conversationID, declarationsJSON, strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, conv)
}

func (s *Server) completeHostedGenesisSoulMintConversationForRegistration(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, conv *models.SoulAgentMintConversation, conversationID string, projectionOpts hostedGenesisProjectionOptions) (*apptheory.Response, error) {
	now := time.Now().UTC()
	session, appErr := s.loadHostedGenesisSessionForCompletion(ctx.Context(), regCtx, conversationID)
	if appErr != nil {
		return nil, appErr
	}
	if readyErr := requireHostedGenesisSessionAssistantReady(session); readyErr != nil {
		return nil, readyErr
	}
	declarationsJSON, extractUsage, appErr := s.resolveMintConversationCompletion(ctx, regCtx, conv, conversationID, now)
	if appErr != nil {
		return nil, appErr
	}
	checkpoint, appErr := hostedGenesisDeclarationCheckpointForCompletion(regCtx, session, conv, declarationsJSON, strings.TrimSpace(ctx.RequestID), now)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.persistCompletedMintConversationWithSession(ctx.Context(), session, conv, declarationsJSON, extractUsage, checkpoint, now); appErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to complete conversation")
	}
	if appErr := s.saveMintConversationFinalizeReadyPromotion(ctx.Context(), regCtx, conversationID, declarationsJSON, strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		return nil, appErr
	}
	projectionOpts.RequestID = firstNonEmpty(projectionOpts.RequestID, strings.TrimSpace(ctx.RequestID))
	return hostedGenesisConversationJSONFromSession(http.StatusOK, session, conv, projectionOpts)
}

func (s *Server) saveMintConversationFinalizeReadyPromotion(ctx context.Context, regCtx mintConversationRegistrationContext, conversationID string, declarationsJSON string, requestID string, now time.Time) *apptheory.AppTheoryError {
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx, regCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(regCtx.reg, now))
	promotion = updateSoulAgentPromotionForConversation(promotion, conversationID, models.SoulMintConversationStatusCompleted, now)
	promotion = updateSoulAgentPromotionReviewDigest(promotion, declarationsJSON)
	if appErr := s.saveSoulAgentPromotion(ctx, promotion); appErr != nil {
		return appErr
	}
	return s.saveSoulAgentPromotionLifecycleEvent(ctx, buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:      models.SoulAgentPromotionEventTypeFinalizeReady,
		RequestID:      strings.TrimSpace(requestID),
		ConversationID: conversationID,
		OccurredAt:     now,
	}))
}

func (s *Server) loadHostedGenesisSessionForCompletion(ctx context.Context, regCtx mintConversationRegistrationContext, conversationID string) (*models.HostedGenesisSession, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	session, err := s.store.GetHostedGenesisSession(ctx, regCtx.inst.Slug, conversationID)
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "conversation not found")
	}
	if !strings.EqualFold(strings.TrimSpace(session.RegistrationID), strings.TrimSpace(regCtx.reg.ID)) ||
		!strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(regCtx.agentIDHex)) {
		return nil, newAppTheoryError("app.forbidden", "conversation is outside the registration boundary")
	}
	return session, nil
}

func requireHostedGenesisSessionAssistantReady(session *models.HostedGenesisSession) *apptheory.AppTheoryError {
	if session == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusAssistantTurnReady &&
		hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusDeclarationExtractionPending {
		return newAppTheoryError(appErrCodeConflict, "conversation has no completed assistant turn")
	}
	return nil
}

func hostedGenesisDeclarationCheckpointForCompletion(regCtx mintConversationRegistrationContext, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, declarationsJSON string, requestID string, now time.Time) (*hostedgenesis.DeclarationCheckpoint, *apptheory.AppTheoryError) {
	if session == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	raw := strings.TrimSpace(declarationsJSON)
	if raw == "" {
		return nil, newAppTheoryError("app.conflict", "conversation has no produced declarations")
	}
	sum := sha256.Sum256([]byte(raw))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	model := strings.TrimSpace(session.Model)
	if model == "" && conv != nil {
		model = strings.TrimSpace(conv.Model)
	}
	checkpoint := &hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl_" + strings.TrimPrefix(hash, "sha256:")[:16],
		DeclarationHash: hash,
		CheckpointRef:   hostedgenesis.CheckpointRef("declaration", session.ConversationID, strings.TrimPrefix(hash, "sha256:")[:16]),
		ProducedAt:      now.UTC(),
		RegistrationID:  strings.TrimSpace(regCtx.reg.ID),
		ConversationID:  strings.TrimSpace(session.ConversationID),
		AgentID:         strings.ToLower(strings.TrimSpace(regCtx.agentIDHex)),
		MessageCount:    session.MessageCount,
		Model:           model,
		RequestID:       strings.TrimSpace(requestID),
	}
	if checkpoint.MessageCount <= 0 && conv != nil {
		checkpoint.MessageCount = mintConversationMessageCount(conv)
	}
	if checkpoint.RequestID == "" {
		checkpoint.RequestID = strings.TrimSpace(session.RequestID)
	}
	if err := checkpoint.Validate(); err != nil {
		return nil, newAppTheoryError("app.conflict", "conversation has invalid produced declarations")
	}
	return checkpoint, nil
}

func (s *Server) persistCompletedMintConversationWithSession(ctx context.Context, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, declarationsJSON string, extractUsage models.AIUsage, checkpoint *hostedgenesis.DeclarationCheckpoint, now time.Time) *apptheory.AppTheoryError {
	if session == nil || conv == nil || checkpoint == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	expectedVersion := session.Version
	expectedStatus := hostedgenesis.NormalizeStatus(session.Status)
	if extractUsage != (models.AIUsage{}) {
		conv.Usage = addAIUsage(conv.Usage, extractUsage)
	}
	conv.Status = models.SoulMintConversationStatusDeclarationReady
	conv.ProducedDeclarations = declarationsJSON
	conv.CompletedAt = now
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	session.DeclarationCheckpoint = checkpoint
	session.Failure = nil
	session.RequestID = firstNonEmpty(checkpoint.RequestID, session.RequestID)
	session.CompletedAt = now
	session.UpdatedAt = now
	updateConv := &models.SoulAgentMintConversation{
		AgentID:              conv.AgentID,
		ConversationID:       conv.ConversationID,
		Status:               conv.Status,
		ProducedDeclarations: encodeMintConversationBlob(declarationsJSON),
		CompletedAt:          conv.CompletedAt,
		Usage:                conv.Usage,
	}
	_ = updateConv.UpdateKeys()
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		if err := addHostedGenesisSessionWrite(tx, session, false, expectedVersion, expectedStatus); err != nil {
			return err
		}
		tx.UpdateWithBuilder(updateConv, func(ub core.UpdateBuilder) error {
			ub.Set("Status", updateConv.Status)
			ub.Set("ProducedDeclarations", updateConv.ProducedDeclarations)
			ub.Set("CompletedAt", updateConv.CompletedAt)
			ub.Set("Usage", updateConv.Usage)
			return nil
		}, tabletheory.IfExists())
		return nil
	}); err != nil {
		return newAppTheoryError("app.internal", "failed to complete conversation")
	}
	return nil
}
