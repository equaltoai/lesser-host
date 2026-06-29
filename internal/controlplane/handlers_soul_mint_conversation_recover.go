package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) handleSoulInstanceRecoverMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	started := time.Now().UTC()
	convCtx, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	decodeMintConversationFields(convCtx.conv)
	if appErr := rejectOversizeSoulMintInstanceConversation(convCtx.conv); appErr != nil {
		return nil, appErr
	}

	if !hostedGenesisSessionNeedsAssistantRecovery(convCtx.session) {
		resp, err := hostedGenesisConversationJSONFromSession(http.StatusOK, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{
			RegistrationID:  convCtx.reg.ID,
			RequestID:       strings.TrimSpace(ctx.RequestID),
			CollapseCreated: true,
		})
		if err != nil {
			return nil, err
		}
		s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, "noop", resp.Status, len(resp.Body), started)
		return resp, nil
	}

	turnSession, acceptedMessages, buildErr := hostedGenesisRecoveryTurnSession(convCtx)
	if buildErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(buildErr)
	}
	apiKey, apiKeyErr := s.apiKeyForMintConversationModel(ctx.Context(), turnSession.modelSet)
	if apiKeyErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(apiKeyErr)
	}

	progressedSession, progressedConv, status, progressErr := s.progressHostedGenesisAcceptedTurn(
		ctx.Context(),
		mintConversationRegistrationContext{reg: convCtx.reg, inst: convCtx.inst, agentIDHex: convCtx.agentIDHex},
		turnSession,
		convCtx.conv,
		acceptedMessages,
		apiKey,
		strings.TrimSpace(ctx.RequestID),
	)
	if progressErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(progressErr)
	}
	resp, err := hostedGenesisConversationJSONFromSession(status, progressedSession, progressedConv, hostedGenesisProjectionOptions{
		RegistrationID:  convCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if err != nil {
		return nil, err
	}
	s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, "success", resp.Status, len(resp.Body), started)
	return resp, nil
}

func hostedGenesisSessionNeedsAssistantRecovery(session *models.HostedGenesisSession) bool {
	if session == nil {
		return false
	}
	return hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusInProgress &&
		strings.TrimSpace(session.AssistantCheckpointRef) == ""
}

func hostedGenesisRecoveryTurnSession(convCtx soulInstanceBootstrapConversationContext) (hostedGenesisTurnSession, []soulMintConversationMessage, *apptheory.AppError) {
	if convCtx.session == nil || convCtx.conv == nil {
		return hostedGenesisTurnSession{}, nil, &apptheory.AppError{Code: soulMintAppErrCodeNotFound, Message: "conversation not found"}
	}
	if !hostedGenesisConversationMatchesSession(convCtx.session, convCtx.conv) ||
		!strings.EqualFold(strings.TrimSpace(convCtx.session.RegistrationID), strings.TrimSpace(convCtx.reg.ID)) {
		return hostedGenesisTurnSession{}, nil, &apptheory.AppError{Code: appErrCodeForbidden, Message: "conversation is outside the registration boundary"}
	}
	messages, appErr := hostedGenesisRecoveryMessages(convCtx.conv)
	if appErr != nil {
		return hostedGenesisTurnSession{}, nil, appErr
	}
	turnID := firstNonEmpty(convCtx.session.LatestTurnID, convCtx.conv.LatestTurnID)
	if turnID == "" && len(convCtx.session.TurnLedger) > 0 {
		turnID = convCtx.session.TurnLedger[len(convCtx.session.TurnLedger)-1].Normalize().TurnID
	}
	if turnID == "" {
		return hostedGenesisTurnSession{}, nil, &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conversation cannot recover without an accepted turn"}
	}
	modelSet := firstNonEmpty(convCtx.session.Model, convCtx.conv.Model, defaultSoulMintConversationModel)
	session := hostedGenesisTurnSession{
		conversationID:     strings.TrimSpace(convCtx.session.ConversationID),
		turnID:             strings.TrimSpace(turnID),
		modelSet:           strings.TrimSpace(modelSet),
		existingMessages:   append([]soulMintConversationMessage(nil), messages...),
		existingUsage:      convCtx.conv.Usage,
		expectedStatus:     hostedgenesis.StatusInProgress,
		expectedVersion:    convCtx.session.Version,
		progressVersion:    convCtx.session.Version,
		hasProgressVersion: true,
		conv:               convCtx.conv,
		session:            convCtx.session,
	}
	return session, messages, nil
}

func hostedGenesisRecoveryMessages(conv *models.SoulAgentMintConversation) ([]soulMintConversationMessage, *apptheory.AppError) {
	if conv == nil {
		return nil, &apptheory.AppError{Code: soulMintAppErrCodeNotFound, Message: "conversation not found"}
	}
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conv.Messages))
	if raw == "" {
		return nil, &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conversation cannot recover without stored messages"}
	}
	var messages []soulMintConversationMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil || len(messages) == 0 {
		return nil, &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conversation cannot recover without stored messages"}
	}
	for i := range messages {
		messages[i].Role = strings.ToLower(strings.TrimSpace(messages[i].Role))
		messages[i].Content = strings.TrimSpace(messages[i].Content)
	}
	return messages, nil
}
