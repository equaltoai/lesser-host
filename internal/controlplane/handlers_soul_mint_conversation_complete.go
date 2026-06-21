package controlplane

import (
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) completeSoulMintConversationForRegistration(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, conv *models.SoulAgentMintConversation, conversationID string) (*apptheory.Response, error) {
	return s.completeSoulMintConversationForRegistrationWithProjection(ctx, regCtx, conv, conversationID, nil)
}

func (s *Server) completeSoulMintConversationForRegistrationWithProjection(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, conv *models.SoulAgentMintConversation, conversationID string, projectionOpts *hostedGenesisProjectionOptions) (*apptheory.Response, error) {
	now := time.Now().UTC()
	if appErr := requireMintConversationDurableAssistantTurn(conv); appErr != nil {
		return nil, appErr
	}
	declarationsJSON, extractUsage, appErr := s.resolveMintConversationCompletion(ctx, regCtx, conv, conversationID, now)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.persistCompletedMintConversation(ctx.Context(), conv, declarationsJSON, extractUsage, now); appErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to complete conversation"}
	}
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), regCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(regCtx.reg, now))
	promotion = updateSoulAgentPromotionForConversation(promotion, conversationID, models.SoulMintConversationStatusCompleted, now)
	promotion = updateSoulAgentPromotionReviewDigest(promotion, declarationsJSON)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:      models.SoulAgentPromotionEventTypeFinalizeReady,
		RequestID:      strings.TrimSpace(ctx.RequestID),
		ConversationID: conversationID,
		OccurredAt:     now,
	})); appErr != nil {
		return nil, appErr
	}

	if projectionOpts != nil {
		opts := *projectionOpts
		if strings.TrimSpace(opts.RegistrationID) == "" && regCtx.reg != nil {
			opts.RegistrationID = regCtx.reg.ID
		}
		if strings.TrimSpace(opts.RequestID) == "" {
			opts.RequestID = strings.TrimSpace(ctx.RequestID)
		}
		return hostedGenesisConversationJSON(http.StatusOK, conv, opts)
	}
	return apptheory.JSON(http.StatusOK, conv)
}
