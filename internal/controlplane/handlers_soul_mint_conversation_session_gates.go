package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func hostedGenesisSessionCompletionReplayReady(session *models.HostedGenesisSession) (bool, string) {
	if session == nil {
		return false, soulMintConversationCompleteReasonInvalidState
	}
	switch hostedgenesis.NormalizeStatus(session.Status) {
	case hostedgenesis.StatusPublished:
		if !hostedGenesisPublishedSessionValid(session) {
			return false, soulMintConversationCompleteReasonInvalidState
		}
		return true, ""
	case hostedgenesis.StatusDeclarationReady:
		if err := hostedgenesis.CanPublish(hostedgenesis.PublishGateInput{
			Status:                hostedgenesis.StatusDeclarationReady,
			RegistrationID:        session.RegistrationID,
			ConversationID:        session.ConversationID,
			AgentID:               session.AgentID,
			DeclarationCheckpoint: session.DeclarationCheckpoint,
		}); err != nil {
			return false, soulMintConversationCompleteReasonInvalidDeclarations
		}
		return true, ""
	case hostedgenesis.StatusFailed:
		if session.Failure != nil {
			switch session.Failure.Code {
			case hostedgenesis.FailureCodeMissingProducedDeclarations:
				return false, soulMintConversationCompleteReasonMissingDeclarations
			case hostedgenesis.FailureCodeInvalidProducedDeclarations:
				return false, soulMintConversationCompleteReasonInvalidDeclarations
			}
		}
		return false, soulMintConversationCompleteReasonInvalidState
	case hostedgenesis.StatusCreated,
		hostedgenesis.StatusInProgress,
		hostedgenesis.StatusAssistantTurnReady:
		return false, ""
	default:
		return false, soulMintConversationCompleteReasonInvalidState
	}
}

func hostedGenesisPublishedSessionValid(session *models.HostedGenesisSession) bool {
	if session == nil || hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusPublished {
		return false
	}
	_, err := hostedgenesis.NewConversationProjection(session.ToProjectionInput(), true)
	return err == nil
}

func requireHostedGenesisSessionReadyForFinalize(session *models.HostedGenesisSession, statusMessage string, emptyDeclMessage string) *apptheory.AppTheoryError {
	if session == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	status := hostedgenesis.NormalizeStatus(session.Status)
	if status != hostedgenesis.StatusDeclarationReady {
		if status == hostedgenesis.StatusFailed && session.Failure != nil {
			switch session.Failure.Code {
			case hostedgenesis.FailureCodeMissingProducedDeclarations:
				return newAppTheoryError("app.conflict", emptyDeclMessage)
			case hostedgenesis.FailureCodeInvalidProducedDeclarations:
				return newAppTheoryError("app.conflict", "conversation has invalid produced declarations")
			}
		}
		return newAppTheoryError("app.conflict", statusMessage)
	}
	if session.DeclarationCheckpoint == nil {
		return newAppTheoryError("app.conflict", emptyDeclMessage)
	}
	if err := hostedgenesis.CanPublish(hostedgenesis.PublishGateInput{
		Status:                hostedgenesis.StatusDeclarationReady,
		RegistrationID:        session.RegistrationID,
		ConversationID:        session.ConversationID,
		AgentID:               session.AgentID,
		DeclarationCheckpoint: session.DeclarationCheckpoint,
	}); err != nil {
		return newAppTheoryError("app.conflict", "conversation has invalid produced declarations")
	}
	return nil
}

func requireHostedGenesisFinalizeDeclarationsMatchSession(session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) *apptheory.AppTheoryError {
	if session == nil {
		return nil
	}
	if appErr := requireHostedGenesisSessionReadyForFinalize(session, "conversation is not completed", "conversation has no produced declarations"); appErr != nil {
		return appErr
	}
	if conv == nil {
		return newAppTheoryError("app.conflict", "conversation has no produced declarations")
	}
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conv.ProducedDeclarations))
	if raw == "" {
		return newAppTheoryError("app.conflict", "conversation has no produced declarations")
	}
	sum := sha256.Sum256([]byte(raw))
	if session.DeclarationCheckpoint == nil || !strings.EqualFold(session.DeclarationCheckpoint.DeclarationHash, "sha256:"+hex.EncodeToString(sum[:])) {
		return newAppTheoryError("app.conflict", "conversation has invalid produced declarations")
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(raw); appErr != nil {
		return appErr
	}
	return nil
}
