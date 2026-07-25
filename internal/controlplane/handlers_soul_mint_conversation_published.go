package controlplane

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// reserveHostedGenesisPublication binds a declaration_ready session to the
// exact registration digest/version before the irreversible publication path
// starts. A retry must reproduce this checkpoint; it cannot silently publish
// different content or advance a second version.
func (s *Server) reserveHostedGenesisPublication(ctx context.Context, finalizeCtx *mintConversationFinalizeContext, version int, registrationSHA256 string, issuedAt time.Time) *apptheory.AppTheoryError {
	if finalizeCtx == nil || finalizeCtx.session == nil {
		return nil
	}
	session := finalizeCtx.session
	if hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusDeclarationReady {
		return newAppTheoryError(appErrCodeConflict, "conversation is not ready for publication")
	}
	checkpoint := &hostedgenesis.PublicationCheckpoint{
		RegistrationID:       session.RegistrationID,
		ConversationID:       session.ConversationID,
		AgentID:              session.AgentID,
		Version:              version,
		RegistrationSHA256:   strings.ToLower(strings.TrimSpace(registrationSHA256)),
		RegistrationIssuedAt: issuedAt.UTC(),
	}
	if err := checkpoint.ValidatePrepared(session.RegistrationID, session.ConversationID, session.AgentID); err != nil {
		return newAppTheoryError(soulMintAppErrCodeInternal, "failed to prepare hosted genesis publication")
	}
	if session.Publication != nil {
		if err := session.Publication.ValidatePrepared(session.RegistrationID, session.ConversationID, session.AgentID); err != nil ||
			session.Publication.Version != checkpoint.Version ||
			!strings.EqualFold(session.Publication.RegistrationSHA256, checkpoint.RegistrationSHA256) ||
			!session.Publication.RegistrationIssuedAt.Equal(checkpoint.RegistrationIssuedAt) {
			return newAppTheoryError(appErrCodeConflict, "hosted genesis publication checkpoint conflict")
		}
		return nil
	}

	next := *session
	next.Publication = checkpoint
	expectedVersion := session.Version
	if err := s.store.UpdateHostedGenesisSession(ctx, &next, expectedVersion, hostedgenesis.StatusDeclarationReady); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return newAppTheoryError(appErrCodeConflict, "hosted genesis session changed; reload and try again")
		}
		return newAppTheoryError(soulMintAppErrCodeInternal, "failed to prepare hosted genesis publication")
	}
	next.Version = expectedVersion + 1
	*session = next
	return nil
}

// persistHostedGenesisPublished atomically moves authoritative session truth
// and its public projection row to the single explicit terminal publication state.
func (s *Server) persistHostedGenesisPublished(ctx context.Context, finalizeCtx *mintConversationFinalizeContext, publication hostedgenesis.PublicationCheckpoint) *apptheory.AppTheoryError {
	session, conversation, alreadyPublished, appErr := validateHostedGenesisPublishedPersistence(finalizeCtx, publication)
	if appErr != nil || alreadyPublished || session == nil {
		return appErr
	}

	nextSession := *session
	nextSession.Status = string(hostedgenesis.StatusPublished)
	nextSession.Publication = &publication
	nextSession.Failure = nil
	nextSession.UpdatedAt = publication.PublishedAt.UTC()

	nextConversation := *conversation
	nextConversation.Status = models.SoulMintConversationStatusPublished
	nextConversation.StatusReason = ""
	nextConversation.RequestID = firstNonEmpty(nextConversation.RequestID, session.RequestID)
	nextConversation.UpdatedAt = publication.PublishedAt.UTC()
	if nextConversation.CompletedAt.IsZero() {
		nextConversation.CompletedAt = publication.PublishedAt.UTC()
	}

	expectedVersion := session.Version
	err := s.store.PublishHostedGenesisSessionAndConversation(ctx, &nextSession, expectedVersion, hostedgenesis.StatusDeclarationReady, &nextConversation)
	if err != nil {
		return s.resolveHostedGenesisPublishedWriteConflict(ctx, finalizeCtx, publication, err)
	}
	nextSession.Version = expectedVersion + 1
	*session = nextSession
	*finalizeCtx.conv = nextConversation
	return nil
}

func validateHostedGenesisPublishedPersistence(finalizeCtx *mintConversationFinalizeContext, publication hostedgenesis.PublicationCheckpoint) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, bool, *apptheory.AppTheoryError) {
	if finalizeCtx == nil || finalizeCtx.session == nil {
		return nil, nil, false, nil
	}
	if finalizeCtx.conv == nil {
		return nil, nil, false, newAppTheoryError(soulMintAppErrCodeInternal, "failed to persist hosted genesis publication")
	}
	session := finalizeCtx.session
	status := hostedgenesis.NormalizeStatus(session.Status)
	if status == hostedgenesis.StatusPublished {
		if session.Publication != nil && publicationCheckpointsEqual(*session.Publication, publication) {
			return session, finalizeCtx.conv, true, nil
		}
		return nil, nil, false, newAppTheoryError(appErrCodeConflict, "hosted genesis publication checkpoint conflict")
	}
	if status != hostedgenesis.StatusDeclarationReady {
		return nil, nil, false, newAppTheoryError(appErrCodeConflict, "conversation is not ready for publication")
	}
	if err := publication.ValidatePublished(session.RegistrationID, session.ConversationID, session.AgentID); err != nil {
		return nil, nil, false, newAppTheoryError(soulMintAppErrCodeInternal, "failed to persist hosted genesis publication")
	}
	if !hostedGenesisPreparedPublicationMatches(session, publication) {
		return nil, nil, false, newAppTheoryError(appErrCodeConflict, "hosted genesis publication checkpoint conflict")
	}
	return session, finalizeCtx.conv, false, nil
}

func hostedGenesisPreparedPublicationMatches(session *models.HostedGenesisSession, publication hostedgenesis.PublicationCheckpoint) bool {
	if session == nil || session.Publication == nil {
		return true
	}
	return publicationCheckpointsPreparedEqual(*session.Publication, publication)
}

func (s *Server) resolveHostedGenesisPublishedWriteConflict(ctx context.Context, finalizeCtx *mintConversationFinalizeContext, publication hostedgenesis.PublicationCheckpoint, writeErr error) *apptheory.AppTheoryError {
	if !theoryErrors.IsConditionFailed(writeErr) {
		return newAppTheoryError(soulMintAppErrCodeInternal, "failed to persist hosted genesis publication")
	}
	session := finalizeCtx.session
	reloaded, reloadErr := s.store.GetHostedGenesisSession(ctx, session.InstanceSlug, session.ConversationID)
	if reloadErr != nil || reloaded == nil || hostedgenesis.NormalizeStatus(reloaded.Status) != hostedgenesis.StatusPublished ||
		reloaded.Publication == nil || !publicationCheckpointsEqual(*reloaded.Publication, publication) {
		return newAppTheoryError(appErrCodeConflict, "hosted genesis session changed; reload and try again")
	}
	*session = *reloaded
	finalizeCtx.conv.Status = models.SoulMintConversationStatusPublished
	return nil
}

func publicationCheckpointsPreparedEqual(left hostedgenesis.PublicationCheckpoint, right hostedgenesis.PublicationCheckpoint) bool {
	return strings.TrimSpace(left.RegistrationID) == strings.TrimSpace(right.RegistrationID) &&
		strings.TrimSpace(left.ConversationID) == strings.TrimSpace(right.ConversationID) &&
		strings.EqualFold(strings.TrimSpace(left.AgentID), strings.TrimSpace(right.AgentID)) &&
		left.Version == right.Version &&
		strings.EqualFold(strings.TrimSpace(left.RegistrationSHA256), strings.TrimSpace(right.RegistrationSHA256)) &&
		left.RegistrationIssuedAt.Equal(right.RegistrationIssuedAt)
}

func publicationCheckpointsEqual(left hostedgenesis.PublicationCheckpoint, right hostedgenesis.PublicationCheckpoint) bool {
	return publicationCheckpointsPreparedEqual(left, right) && left.PublishedAt.Equal(right.PublishedAt)
}

func hostedGenesisFinalizeBaseVersion(identity *models.SoulAgentIdentity, session *models.HostedGenesisSession) (int, *apptheory.AppTheoryError) {
	if identity == nil {
		return 0, newAppTheoryError(soulMintAppErrCodeInternal, "internal error")
	}
	if session == nil || session.Publication == nil {
		return identity.SelfDescriptionVersion, nil
	}
	if err := session.Publication.ValidatePrepared(session.RegistrationID, session.ConversationID, session.AgentID); err != nil {
		return 0, newAppTheoryError(appErrCodeConflict, "hosted genesis publication checkpoint is invalid")
	}
	base := session.Publication.Version - 1
	if identity.SelfDescriptionVersion != base && identity.SelfDescriptionVersion != session.Publication.Version {
		return 0, newAppTheoryError(appErrCodeConflict, "version conflict; reload and try again")
	}
	return base, nil
}

func hostedGenesisFinalizeIssuedAt(session *models.HostedGenesisSession, fallback time.Time) time.Time {
	if session != nil && session.Publication != nil && !session.Publication.RegistrationIssuedAt.IsZero() {
		return session.Publication.RegistrationIssuedAt.UTC()
	}
	return fallback.UTC()
}

func hostedGenesisFinalizeAlreadyPublishedWithoutReservation(identity *models.SoulAgentIdentity, session *models.HostedGenesisSession) bool {
	return identity != nil && identity.SelfDescriptionVersion > 0 && (session == nil || session.Publication == nil)
}

func validateHostedGenesisFinalizeReservation(session *models.HostedGenesisSession, baseVersion int, expectedVersion int, issuedAt time.Time) *apptheory.AppTheoryError {
	if session == nil || session.Publication == nil {
		return nil
	}
	if expectedVersion != baseVersion {
		return newAppTheoryError(appErrCodeConflict, "version conflict; reload and try again")
	}
	if !issuedAt.Equal(session.Publication.RegistrationIssuedAt) {
		return newAppTheoryError(appErrCodeConflict, "hosted genesis publication checkpoint conflict")
	}
	return nil
}

// convergeHostedGenesisPublished repairs finalized-but-stale prototype rows
// only from exact graduated promotion and version-history evidence. It never
// guesses from an active identity alone, so another conversation cannot be
// promoted across a tenant or registration boundary.
func (s *Server) convergeHostedGenesisPublished(ctx context.Context, session *models.HostedGenesisSession, conversation *models.SoulAgentMintConversation, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion) (bool, *apptheory.AppTheoryError) {
	if session == nil {
		return false, nil
	}
	if hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusPublished {
		if !hostedGenesisPublishedSessionValid(session) {
			return false, newAppTheoryError(soulMintAppErrCodeInternal, "hosted genesis publication truth is invalid")
		}
		return true, nil
	}
	if !hostedGenesisPublishedConvergenceCandidate(session, identity, promotion) {
		return false, nil
	}
	versionRecord, found, appErr := s.loadHostedGenesisPublishedVersionEvidence(ctx, session, identity)
	if appErr != nil || !found {
		return false, appErr
	}
	if appErr := s.ensureSoulAgentRegistrationPublishedIdentityActive(ctx, identity, promotion.GraduatedAt.UTC()); appErr != nil {
		return false, appErr
	}
	publication := hostedGenesisPublicationCheckpointFromEvidence(session, identity, promotion, versionRecord)
	finalizeCtx := mintConversationFinalizeContext{identity: identity, conv: conversation, session: session, agentIDHex: session.AgentID, conversationID: session.ConversationID}
	if appErr := s.persistHostedGenesisPublished(ctx, &finalizeCtx, publication); appErr != nil {
		return false, appErr
	}
	return true, nil
}

func hostedGenesisPublishedConvergenceCandidate(session *models.HostedGenesisSession, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion) bool {
	return hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusDeclarationReady &&
		identity != nil && identity.SelfDescriptionVersion > 0 &&
		hostedGenesisPromotionConfirmsPublication(session, identity, promotion)
}

func (s *Server) loadHostedGenesisPublishedVersionEvidence(ctx context.Context, session *models.HostedGenesisSession, identity *models.SoulAgentIdentity) (*models.SoulAgentVersion, bool, *apptheory.AppTheoryError) {
	versionRecord, err := s.getSoulAgentVersionRecord(ctx, session.AgentID, identity.SelfDescriptionVersion)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, newAppTheoryError(soulMintAppErrCodeInternal, "failed to read hosted genesis publication evidence")
	}
	if versionRecord == nil || !strings.EqualFold(versionRecord.AgentID, session.AgentID) || versionRecord.VersionNumber != identity.SelfDescriptionVersion ||
		strings.TrimSpace(versionRecord.RegistrationSHA256) == "" || versionRecord.CreatedAt.IsZero() {
		return nil, false, newAppTheoryError(soulMintAppErrCodeInternal, "hosted genesis publication evidence is invalid")
	}
	return versionRecord, true, nil
}

func hostedGenesisPublicationCheckpointFromEvidence(session *models.HostedGenesisSession, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion, versionRecord *models.SoulAgentVersion) hostedgenesis.PublicationCheckpoint {
	issuedAt := versionRecord.CreatedAt.UTC()
	if session.Publication != nil && !session.Publication.RegistrationIssuedAt.IsZero() {
		issuedAt = session.Publication.RegistrationIssuedAt.UTC()
	}
	return hostedgenesis.PublicationCheckpoint{
		RegistrationID:       session.RegistrationID,
		ConversationID:       session.ConversationID,
		AgentID:              session.AgentID,
		Version:              identity.SelfDescriptionVersion,
		RegistrationSHA256:   strings.ToLower(strings.TrimSpace(versionRecord.RegistrationSHA256)),
		RegistrationIssuedAt: issuedAt,
		PublishedAt:          promotion.GraduatedAt.UTC(),
	}
}

func hostedGenesisPromotionConfirmsPublication(session *models.HostedGenesisSession, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion) bool {
	return session != nil && identity != nil && promotion != nil &&
		strings.EqualFold(strings.TrimSpace(promotion.AgentID), strings.TrimSpace(session.AgentID)) &&
		strings.TrimSpace(promotion.RegistrationID) == strings.TrimSpace(session.RegistrationID) &&
		strings.TrimSpace(promotion.LatestConversationID) == strings.TrimSpace(session.ConversationID) &&
		hostedGenesisPromotionConversationPublished(promotion.LatestConversationStatus) &&
		promotion.PublishedVersion == identity.SelfDescriptionVersion &&
		strings.EqualFold(strings.TrimSpace(promotion.Stage), models.SoulAgentPromotionStageGraduated) &&
		strings.EqualFold(strings.TrimSpace(promotion.ReviewStatus), models.SoulAgentPromotionReviewStatusPublished) &&
		strings.EqualFold(strings.TrimSpace(promotion.ReadinessStatus), models.SoulAgentPromotionReadinessGraduated) &&
		!promotion.GraduatedAt.IsZero()
}

func hostedGenesisPromotionConversationPublished(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == models.SoulMintConversationStatusCompleted || status == models.SoulMintConversationStatusPublished
}

func (s *Server) loadSoulAgentPromotionForPublishedConvergence(ctx context.Context, agentID string) (*models.SoulAgentPromotion, *apptheory.AppTheoryError) {
	promotion, err := s.getSoulAgentPromotion(ctx, agentID)
	if err == nil {
		return promotion, nil
	}
	if errors.Is(err, theoryErrors.ErrItemNotFound) || theoryErrors.IsNotFound(err) {
		return nil, nil
	}
	return nil, newAppTheoryError(soulMintAppErrCodeInternal, "failed to read hosted genesis publication evidence")
}

func (s *Server) hostedGenesisPublishedFinalizeResponse(finalizeCtx mintConversationFinalizeContext) (*apptheory.Response, error) {
	if finalizeCtx.identity == nil || !hostedGenesisPublishedSessionValid(finalizeCtx.session) {
		return nil, newAppTheoryError(soulMintAppErrCodeInternal, "hosted genesis publication truth is invalid")
	}
	publicationCheckpoint := finalizeCtx.session.Publication
	publication := s.buildMintConversationPublicationEvidence(
		finalizeCtx.agentIDHex,
		publicationCheckpoint.Version,
		finalizeCtx.identity.AuthorityModel,
		finalizeCtx.identity.AnchorState,
		publicationCheckpoint.PublishedAt,
	)
	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeResponse{
		Version:          "1",
		Status:           string(hostedgenesis.StatusPublished),
		RegistrationID:   finalizeCtx.session.RegistrationID,
		ConversationID:   finalizeCtx.session.ConversationID,
		AgentID:          finalizeCtx.agentIDHex,
		Agent:            *finalizeCtx.identity,
		PublishedVersion: publicationCheckpoint.Version,
		PublishedAt:      publicationCheckpoint.PublishedAt.UTC().Format(time.RFC3339Nano),
		Publication:      publication,
		Promotion:        nil,
	})
}
