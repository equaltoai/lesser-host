package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHostedGenesisPublishedReadAndListProjectionsAreTerminal(t *testing.T) {
	t.Parallel()

	session, conv := publishedHostedGenesisProjectionFixture()
	response := buildHostedGenesisConversationResponseFromSession(session, conv, hostedGenesisProjectionOptions{CollapseCreated: true})
	require.Equal(t, string(hostedgenesis.StatusPublished), response.Conversation.Status)
	require.Equal(t, 1, response.Conversation.PublishedVersion)
	require.NotNil(t, response.Conversation.PublishedAt)
	require.Zero(t, response.Conversation.PollAfterSeconds)
	require.Nil(t, response.Conversation.ProducedDeclarations)
	require.Nil(t, response.Conversation.Failure)
	require.Empty(t, response.Conversation.Messages)

	summary := soulInstanceMintConversationListSummaryFromSession(session)
	require.Equal(t, string(hostedgenesis.StatusPublished), summary.Status)
	require.Equal(t, session.AgentID, summary.AgentID)
	require.Equal(t, 1, summary.PublishedVersion)
	require.NotNil(t, summary.PublishedAt)

	replayReady, reason := hostedGenesisSessionCompletionReplayReady(session)
	require.True(t, replayReady)
	require.Empty(t, reason)
	require.False(t, hostedGenesisSessionNeedsAssistantRecovery(session))
	require.False(t, hostedGenesisSessionRequiresRestart(session))
	require.False(t, hostedGenesisSessionCanRetryAssistantTurn(session))

	payload, err := json.Marshal(response)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(payload)), "produced_declarations")
	require.NotContains(t, strings.ToLower(string(payload)), "recovery")
}

func TestHostedGenesisPublicationReservationMakesPartialReplaySingleVersion(t *testing.T) {
	t.Parallel()

	session, conv := publishedHostedGenesisProjectionFixture()
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	session.Publication = nil
	session.Version = 7
	conv.Status = models.SoulMintConversationStatusDeclarationReady
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	tx := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tx
	tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tx).Once()
	tx.On("Execute").Return(nil).Once()
	issuedAt := time.Date(2026, 7, 21, 12, 4, 0, 0, time.UTC)
	finalizeCtx := mintConversationFinalizeContext{session: session, conv: conv, identity: &models.SoulAgentIdentity{SelfDescriptionVersion: 0}}

	require.Nil(t, s.reserveHostedGenesisPublication(context.Background(), &finalizeCtx, 1, strings.Repeat("c", 64), issuedAt))
	require.Equal(t, int64(8), session.Version)
	require.NotNil(t, session.Publication)
	require.Equal(t, 1, session.Publication.Version)

	// The exact replay is a no-op. It does not reserve a second version or write
	// another transaction after an interruption.
	require.Nil(t, s.reserveHostedGenesisPublication(context.Background(), &finalizeCtx, 1, strings.Repeat("c", 64), issuedAt))
	baseVersion, appErr := hostedGenesisFinalizeBaseVersion(&models.SoulAgentIdentity{SelfDescriptionVersion: 1}, session)
	require.Nil(t, appErr)
	require.Equal(t, 0, baseVersion)

	conflict := s.reserveHostedGenesisPublication(context.Background(), &finalizeCtx, 1, strings.Repeat("d", 64), issuedAt)
	require.NotNil(t, conflict)
	require.Equal(t, appErrCodeConflict, conflict.Code)
	tx.AssertExpectations(t)
}

func TestHostedGenesisRepeatedFinalizeReturnsDurableTerminalResponse(t *testing.T) {
	t.Parallel()

	session, conv := publishedHostedGenesisProjectionFixture()
	identity := &models.SoulAgentIdentity{
		AgentID:                session.AgentID,
		AuthorityModel:         models.SoulAuthorityModelInstanceTrust,
		AnchorState:            models.SoulAnchorStateHostedOffchain,
		Status:                 models.SoulAgentStatusActive,
		LifecycleStatus:        models.SoulAgentStatusActive,
		SelfDescriptionVersion: 1,
	}
	s := &Server{}
	resp, err := s.hostedGenesisPublishedFinalizeResponse(mintConversationFinalizeContext{
		identity:       identity,
		session:        session,
		conv:           conv,
		agentIDHex:     session.AgentID,
		conversationID: session.ConversationID,
	})
	require.NoError(t, err)
	var out soulMintConversationFinalizeResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, string(hostedgenesis.StatusPublished), out.Status)
	require.Equal(t, session.RegistrationID, out.RegistrationID)
	require.Equal(t, session.ConversationID, out.ConversationID)
	require.Equal(t, 1, out.PublishedVersion)
	require.NotEmpty(t, out.PublishedAt)
}

func TestHostedGenesisPublishedTransitionLeavesFailureTerminalSemanticsUnchanged(t *testing.T) {
	t.Parallel()

	session, _ := publishedHostedGenesisProjectionFixture()
	session.Status = string(hostedgenesis.StatusFailed)
	session.Publication = nil
	session.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeOperatorActionRequired,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeOperatorActionRequired),
		Retryable: false,
		Recovery: hostedgenesis.Recovery{
			Action: hostedgenesis.RecoveryActionOperatorAction,
			Reason: string(hostedgenesis.FailureCodeOperatorActionRequired),
		},
	}
	response := buildHostedGenesisConversationResponseFromSession(session, nil, hostedGenesisProjectionOptions{CollapseCreated: true})
	require.Equal(t, string(hostedgenesis.StatusFailed), response.Conversation.Status)
	require.NotNil(t, response.Conversation.Failure)
	require.Zero(t, response.Conversation.PublishedVersion)
}

func TestHostedGenesisPublishedPersistenceGuardsRejectDrift(t *testing.T) {
	t.Parallel()

	session, conv := publishedHostedGenesisProjectionFixture()
	publication := *session.Publication
	require.Nil(t, (&Server{}).reserveHostedGenesisPublication(context.Background(), nil, 1, publication.RegistrationSHA256, publication.RegistrationIssuedAt))
	require.Nil(t, (&Server{}).persistHostedGenesisPublished(context.Background(), nil, publication))
	_, err := (&Server{}).hostedGenesisPublishedFinalizeResponse(mintConversationFinalizeContext{session: session})
	require.Error(t, err)
	appErr := (&Server{}).reserveHostedGenesisPublication(context.Background(), &mintConversationFinalizeContext{session: session}, 1, publication.RegistrationSHA256, publication.RegistrationIssuedAt)
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)
	invalidReservation := *session
	invalidReservation.Status = string(hostedgenesis.StatusDeclarationReady)
	invalidReservation.Publication = nil
	appErr = (&Server{}).reserveHostedGenesisPublication(context.Background(), &mintConversationFinalizeContext{session: &invalidReservation}, 0, "invalid", time.Time{})
	require.NotNil(t, appErr)
	require.Equal(t, soulMintAppErrCodeInternal, appErr.Code)

	_, _, _, appErr = validateHostedGenesisPublishedPersistence(&mintConversationFinalizeContext{session: session}, publication)
	require.NotNil(t, appErr)
	require.Equal(t, soulMintAppErrCodeInternal, appErr.Code)
	_, _, alreadyPublished, appErr := validateHostedGenesisPublishedPersistence(&mintConversationFinalizeContext{session: session, conv: conv}, publication)
	require.Nil(t, appErr)
	require.True(t, alreadyPublished)

	drifted := publication
	drifted.RegistrationSHA256 = strings.Repeat("c", 64)
	_, _, _, appErr = validateHostedGenesisPublishedPersistence(&mintConversationFinalizeContext{session: session, conv: conv}, drifted)
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)

	ready := *session
	ready.Status = string(hostedgenesis.StatusDeclarationReady)
	ready.Publication = &hostedgenesis.PublicationCheckpoint{
		RegistrationID:       publication.RegistrationID,
		ConversationID:       publication.ConversationID,
		AgentID:              publication.AgentID,
		Version:              publication.Version,
		RegistrationSHA256:   publication.RegistrationSHA256,
		RegistrationIssuedAt: publication.RegistrationIssuedAt,
	}
	invalid := publication
	invalid.PublishedAt = time.Time{}
	_, _, _, appErr = validateHostedGenesisPublishedPersistence(&mintConversationFinalizeContext{session: &ready, conv: conv}, invalid)
	require.NotNil(t, appErr)
	require.Equal(t, soulMintAppErrCodeInternal, appErr.Code)

	ready.Publication.RegistrationSHA256 = strings.Repeat("d", 64)
	_, _, _, appErr = validateHostedGenesisPublishedPersistence(&mintConversationFinalizeContext{session: &ready, conv: conv}, publication)
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)

	ready.Status = string(hostedgenesis.StatusFailed)
	_, _, _, appErr = validateHostedGenesisPublishedPersistence(&mintConversationFinalizeContext{session: &ready, conv: conv}, publication)
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)
}

func TestHostedGenesisFinalizeReservationGuards(t *testing.T) {
	t.Parallel()

	session, _ := publishedHostedGenesisProjectionFixture()
	publication := *session.Publication
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	session.Publication = &publication

	base, appErr := hostedGenesisFinalizeBaseVersion(nil, session)
	require.Zero(t, base)
	require.NotNil(t, appErr)

	invalid := publication
	invalid.RegistrationSHA256 = "invalid"
	session.Publication = &invalid
	base, appErr = hostedGenesisFinalizeBaseVersion(&models.SoulAgentIdentity{}, session)
	require.Zero(t, base)
	require.NotNil(t, appErr)

	session.Publication = &publication
	base, appErr = hostedGenesisFinalizeBaseVersion(&models.SoulAgentIdentity{SelfDescriptionVersion: 2}, session)
	require.Zero(t, base)
	require.NotNil(t, appErr)

	appErr = validateHostedGenesisFinalizeReservation(session, 0, 1, publication.RegistrationIssuedAt)
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)
	appErr = validateHostedGenesisFinalizeReservation(session, 0, 0, publication.RegistrationIssuedAt.Add(time.Second))
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)
	require.Nil(t, validateHostedGenesisFinalizeReservation(session, 0, 0, publication.RegistrationIssuedAt))
	require.Nil(t, validateHostedGenesisFinalizeReservation(nil, 0, 0, time.Time{}))
	require.Equal(t, publication.RegistrationIssuedAt, hostedGenesisFinalizeIssuedAt(session, time.Now()))
	require.False(t, hostedGenesisFinalizeIssuedAt(nil, publication.PublishedAt).IsZero())
	checkpoint := hostedGenesisPublicationCheckpointFromEvidence(session, &models.SoulAgentIdentity{SelfDescriptionVersion: 1}, &models.SoulAgentPromotion{GraduatedAt: publication.PublishedAt}, &models.SoulAgentVersion{
		RegistrationSHA256: publication.RegistrationSHA256,
		CreatedAt:          publication.RegistrationIssuedAt.Add(-time.Minute),
	})
	require.Equal(t, publication.RegistrationIssuedAt, checkpoint.RegistrationIssuedAt)
}

func TestHostedGenesisPublishedWriteConflictReloadsExactTerminalTruth(t *testing.T) {
	t.Parallel()

	published, conv := publishedHostedGenesisProjectionFixture()
	publication := *published.Publication
	stale := *published
	stale.Status = string(hostedgenesis.StatusDeclarationReady)
	stale.Publication = &hostedgenesis.PublicationCheckpoint{
		RegistrationID:       publication.RegistrationID,
		ConversationID:       publication.ConversationID,
		AgentID:              publication.AgentID,
		Version:              publication.Version,
		RegistrationSHA256:   publication.RegistrationSHA256,
		RegistrationIssuedAt: publication.RegistrationIssuedAt,
	}
	tdb := newMintConversationTestDB()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		*testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0) = *published
	}).Once()
	s := newMintConversationServer(tdb)
	finalizeCtx := mintConversationFinalizeContext{session: &stale, conv: conv}
	require.Nil(t, s.resolveHostedGenesisPublishedWriteConflict(context.Background(), &finalizeCtx, publication, theoryErrors.ErrConditionFailed))
	require.Equal(t, string(hostedgenesis.StatusPublished), stale.Status)
	require.Equal(t, models.SoulMintConversationStatusPublished, conv.Status)

	appErr := s.resolveHostedGenesisPublishedWriteConflict(context.Background(), &finalizeCtx, publication, errors.New("write failed"))
	require.NotNil(t, appErr)
	require.Equal(t, soulMintAppErrCodeInternal, appErr.Code)

	tdbConflict := newMintConversationTestDB()
	tdbConflict.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(theoryErrors.ErrItemNotFound).Once()
	appErr = newMintConversationServer(tdbConflict).resolveHostedGenesisPublishedWriteConflict(context.Background(), &finalizeCtx, publication, theoryErrors.ErrConditionFailed)
	require.NotNil(t, appErr)
	require.Equal(t, appErrCodeConflict, appErr.Code)
}

func TestHostedGenesisPublishedConvergenceEvidenceFailsClosed(t *testing.T) {
	t.Parallel()

	session, conv := publishedHostedGenesisProjectionFixture()
	identity := &models.SoulAgentIdentity{AgentID: session.AgentID, SelfDescriptionVersion: 1}
	promotion := &models.SoulAgentPromotion{
		AgentID:                  session.AgentID,
		RegistrationID:           session.RegistrationID,
		LatestConversationID:     session.ConversationID,
		LatestConversationStatus: models.SoulMintConversationStatusCompleted,
		PublishedVersion:         1,
		Stage:                    models.SoulAgentPromotionStageGraduated,
		ReviewStatus:             models.SoulAgentPromotionReviewStatusPublished,
		ReadinessStatus:          models.SoulAgentPromotionReadinessGraduated,
		GraduatedAt:              session.Publication.PublishedAt,
	}
	s := &Server{}
	converged, appErr := s.convergeHostedGenesisPublished(context.Background(), nil, conv, identity, promotion)
	require.False(t, converged)
	require.Nil(t, appErr)
	converged, appErr = s.convergeHostedGenesisPublished(context.Background(), session, conv, identity, promotion)
	require.True(t, converged)
	require.Nil(t, appErr)

	invalid := *session
	invalid.Publication = nil
	converged, appErr = s.convergeHostedGenesisPublished(context.Background(), &invalid, conv, identity, promotion)
	require.False(t, converged)
	require.NotNil(t, appErr)

	ready := *session
	ready.Status = string(hostedgenesis.StatusDeclarationReady)
	ready.Publication = nil
	promotion.LatestConversationStatus = models.SoulMintConversationStatusInProgress
	converged, appErr = s.convergeHostedGenesisPublished(context.Background(), &ready, conv, identity, promotion)
	require.False(t, converged)
	require.Nil(t, appErr)
}

func TestLoadHostedGenesisPublishedVersionEvidenceHandlesMissingAndInvalidRecords(t *testing.T) {
	t.Parallel()

	session, _ := publishedHostedGenesisProjectionFixture()
	identity := &models.SoulAgentIdentity{SelfDescriptionVersion: 1}

	for name, firstErr := range map[string]error{
		"missing": theoryErrors.ErrItemNotFound,
		"read":    errors.New("read failed"),
	} {
		t.Run(name, func(t *testing.T) {
			tdb := newMintConversationTestDB()
			qVersion := new(ttmocks.MockQuery)
			tdb.db.On("Model", mock.AnythingOfType("*models.SoulAgentVersion")).Return(qVersion).Once()
			addStandardMockQueryStubs(qVersion)
			qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(firstErr).Once()
			_, found, appErr := newMintConversationServer(tdb).loadHostedGenesisPublishedVersionEvidence(context.Background(), session, identity)
			require.False(t, found)
			if theoryErrors.IsNotFound(firstErr) {
				require.Nil(t, appErr)
			} else {
				require.NotNil(t, appErr)
			}
		})
	}

	tdb := newMintConversationTestDB()
	qVersion := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.SoulAgentVersion")).Return(qVersion).Once()
	addStandardMockQueryStubs(qVersion)
	qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Once()
	_, found, appErr := newMintConversationServer(tdb).loadHostedGenesisPublishedVersionEvidence(context.Background(), session, identity)
	require.False(t, found)
	require.NotNil(t, appErr)

	tdbValid := newMintConversationTestDB()
	qValid := new(ttmocks.MockQuery)
	tdbValid.db.On("Model", mock.AnythingOfType("*models.SoulAgentVersion")).Return(qValid).Once()
	addStandardMockQueryStubs(qValid)
	qValid.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
		*testutil.RequireMockArg[*models.SoulAgentVersion](t, args, 0) = models.SoulAgentVersion{
			AgentID:            session.AgentID,
			VersionNumber:      1,
			RegistrationSHA256: strings.Repeat("b", 64),
			CreatedAt:          session.Publication.RegistrationIssuedAt,
		}
	}).Once()
	record, found, appErr := newMintConversationServer(tdbValid).loadHostedGenesisPublishedVersionEvidence(context.Background(), session, identity)
	require.True(t, found)
	require.Nil(t, appErr)
	require.NotNil(t, record)

	tdbPromotion := newMintConversationTestDB()
	tdbPromotion.qPromotion.ExpectedCalls = nil
	addStandardMockQueryStubs(tdbPromotion.qPromotion)
	tdbPromotion.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(errors.New("read failed")).Once()
	_, appErr = newMintConversationServer(tdbPromotion).loadSoulAgentPromotionForPublishedConvergence(context.Background(), session.AgentID)
	require.NotNil(t, appErr)
}

func publishedHostedGenesisProjectionFixture() (*models.HostedGenesisSession, *models.SoulAgentMintConversation) {
	createdAt := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	publishedAt := createdAt.Add(5 * time.Minute)
	agentID := "0x" + strings.Repeat("2", 64)
	declaration := &hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl-1",
		DeclarationHash: "sha256:" + strings.Repeat("a", 64),
		CheckpointRef:   "checkpoint://hosted-genesis/decl-1",
		ProducedAt:      createdAt.Add(4 * time.Minute),
		RegistrationID:  "reg-1",
		ConversationID:  "conv-1",
		AgentID:         agentID,
		MessageCount:    2,
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
		RequestID:       "req-1",
	}
	session := &models.HostedGenesisSession{
		InstanceSlug:          "demo",
		RegistrationID:        declaration.RegistrationID,
		AgentID:               agentID,
		ConversationID:        declaration.ConversationID,
		Status:                string(hostedgenesis.StatusPublished),
		MessageCount:          declaration.MessageCount,
		DeclarationCheckpoint: declaration,
		Publication: &hostedgenesis.PublicationCheckpoint{
			RegistrationID:       declaration.RegistrationID,
			ConversationID:       declaration.ConversationID,
			AgentID:              agentID,
			Version:              1,
			RegistrationSHA256:   strings.Repeat("b", 64),
			RegistrationIssuedAt: createdAt.Add(4 * time.Minute),
			PublishedAt:          publishedAt,
		},
		RequestID:   "req-1",
		CreatedAt:   createdAt,
		UpdatedAt:   publishedAt,
		CompletedAt: createdAt.Add(4 * time.Minute),
	}
	conv := &models.SoulAgentMintConversation{
		AgentID:        agentID,
		ConversationID: declaration.ConversationID,
		Status:         models.SoulMintConversationStatusPublished,
		CreatedAt:      createdAt,
		UpdatedAt:      publishedAt,
		CompletedAt:    createdAt.Add(4 * time.Minute),
	}
	return session, conv
}
