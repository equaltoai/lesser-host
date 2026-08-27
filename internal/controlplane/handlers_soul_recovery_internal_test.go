package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	recoveryTestSlug         = "inst1"
	recoveryTestDomain       = "theory.greater.website"
	recoveryTestLocalID      = "silas-vane"
	recoveryTestRegistration = "reg-recovery-1"
	recoveryTestConversation = "conv-recovery-1"
	recoveryTestOtherSlug    = "other"
)

type recoveryTestQueries struct {
	key          *ttmocks.MockQuery
	identity     *ttmocks.MockQuery
	domain       *ttmocks.MockQuery
	instance     *ttmocks.MockQuery
	promotion    *ttmocks.MockQuery
	hosted       *ttmocks.MockQuery
	conversation *ttmocks.MockQuery
	version      *ttmocks.MockQuery
	audit        *ttmocks.MockQuery
	domainAgent  *ttmocks.MockQuery
}

type recoveryPackStore struct {
	objects map[string][]byte
	errors  map[string]error
}

func (f *recoveryPackStore) PutObject(context.Context, string, []byte, string, string) error {
	return errors.New("unexpected recovery write")
}

func (f *recoveryPackStore) GetObject(_ context.Context, key string, _ int64) ([]byte, string, string, error) {
	if err := f.errors[key]; err != nil {
		return nil, "", "", err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, "", "", &types.NoSuchKey{}
	}
	return append([]byte(nil), body...), soulMintInstanceReadJSONContentType, "", nil
}

func TestSoulRecoveryArtifactClassifications(t *testing.T) {
	t.Parallel()

	first := strings.Repeat("a", 64)
	second := strings.Repeat("b", 64)
	published := soulRecoveryArtifactState{
		Versions: []soulRecoveryVersion{
			{VersionNumber: 1, RegistrationSHA256: first, ChecksumVerified: true},
			{VersionNumber: 2, RegistrationSHA256: second, PreviousRegistrationSHA256: first, ChecksumVerified: true},
		},
		CurrentPresent: true,
		CurrentSHA256:  second,
	}
	got, appErr := classifySoulRecoveryArtifacts(2, published)
	require.Nil(t, appErr)
	require.Equal(t, soulRecoveryClassificationPublished, got)

	got, appErr = classifySoulRecoveryArtifacts(1, soulRecoveryArtifactState{})
	require.Nil(t, appErr)
	require.Equal(t, soulRecoveryClassificationLegacy, got)
	_, appErr = classifySoulRecoveryArtifacts(0, soulRecoveryArtifactState{})
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)

	conflicts := []soulRecoveryArtifactState{
		{CurrentPresent: true, CurrentSHA256: first},
		{Versions: published.Versions},
		{Versions: published.Versions, CurrentPresent: true, CurrentSHA256: first},
		{Versions: []soulRecoveryVersion{{VersionNumber: 1, RegistrationSHA256: first, PreviousRegistrationSHA256: second, ChecksumVerified: true}}, CurrentPresent: true, CurrentSHA256: first},
	}
	for _, state := range conflicts {
		_, appErr = classifySoulRecoveryArtifacts(max(1, len(state.Versions)), state)
		require.NotNil(t, appErr)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
	}
}

func TestSelectSoulRecoverySessionEnforcesBindingsAndAmbiguity(t *testing.T) {
	t.Parallel()
	identity, promotion, session := recoveryIdentityPromotionSession(1)

	selected, appErr := selectSoulRecoverySession(recoveryTestSlug, identity.AgentID, identity, promotion, []*models.HostedGenesisSession{session})
	require.Nil(t, appErr)
	require.Same(t, session, selected)

	crossTenant := *session
	crossTenant.InstanceSlug = recoveryTestOtherSlug
	_, appErr = selectSoulRecoverySession(recoveryTestSlug, identity.AgentID, identity, promotion, []*models.HostedGenesisSession{&crossTenant})
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeBoundaryViolation, appErr.Code)

	duplicate := *session
	_, appErr = selectSoulRecoverySession(recoveryTestSlug, identity.AgentID, identity, promotion, []*models.HostedGenesisSession{session, &duplicate})
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)

	invalidStatus := *session
	invalidStatus.Status = models.SoulMintConversationStatusInProgress
	_, appErr = selectSoulRecoverySession(recoveryTestSlug, identity.AgentID, identity, promotion, []*models.HostedGenesisSession{&invalidStatus})
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)

	_, appErr = selectSoulRecoverySession(recoveryTestSlug, identity.AgentID, identity, promotion, nil)
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
}

func TestHandleSoulInstanceRecoveryAgentLegacyDeclarationsOnly(t *testing.T) {
	t.Parallel()
	server, queries, identity, promotion, session, declarationBytes := newRecoveryTestServer(t, 1)
	stubRecoveryCommonReads(t, queries, identity, promotion, session, declarationBytes)
	queries.version.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)
	resp, err := server.handleSoulInstanceRecoveryAgent(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var detail soulRecoveryAgentDetail
	require.NoError(t, json.Unmarshal(resp.Body, &detail))
	require.Equal(t, soulRecoveryClassificationLegacy, detail.Classification)
	require.Equal(t, "sha256:"+recoverySHA256(declarationBytes), detail.MigrationReadSHA256)
	require.JSONEq(t, string(declarationBytes), string(detail.Declarations))
	require.Empty(t, detail.Versions)
	require.Nil(t, detail.PublishedRegistration)
	require.NotContains(t, string(resp.Body), `"messages"`)
}

func TestHandleSoulInstanceRecoveryAgentVerifiesCompleteVersionChain(t *testing.T) {
	t.Parallel()
	server, queries, identity, promotion, session, declarationBytes := newRecoveryTestServer(t, 2)
	stubRecoveryCommonReads(t, queries, identity, promotion, session, declarationBytes)

	versionBodies := [][]byte{[]byte(`{"version":"1"}`), []byte(`{"version":"2"}`)}
	versionHashes := []string{recoverySHA256(versionBodies[0]), recoverySHA256(versionBodies[1])}
	versionCall := 0
	queries.version.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
		versionCall++
		dest := testutil.RequireMockArg[*models.SoulAgentVersion](t, args, 0)
		version := versionCall
		previous := ""
		if version > 1 {
			previous = versionHashes[version-2]
		}
		*dest = models.SoulAgentVersion{
			AgentID:                    identity.AgentID,
			VersionNumber:              version,
			RegistrationURI:            fmt.Sprintf("s3://bucket/%s", soulRegistrationVersionedS3Key(identity.AgentID, version)),
			RegistrationSHA256:         versionHashes[version-1],
			PreviousRegistrationSHA256: previous,
			CreatedAt:                  time.Date(2026, 8, version, 1, 2, 3, 0, time.UTC),
		}
	}).Twice()
	pack, ok := server.soulPacks.(*recoveryPackStore)
	require.True(t, ok)
	for i, body := range versionBodies {
		pack.objects[soulRegistrationVersionedS3Key(identity.AgentID, i+1)] = body
	}
	pack.objects[soulRegistrationS3Key(identity.AgentID)] = versionBodies[1]

	ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)
	resp, err := server.handleSoulInstanceRecoveryAgent(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var detail soulRecoveryAgentDetail
	require.NoError(t, json.Unmarshal(resp.Body, &detail))
	require.Equal(t, soulRecoveryClassificationPublished, detail.Classification)
	require.Len(t, detail.Versions, 2)
	require.True(t, detail.Versions[0].ChecksumVerified)
	require.Equal(t, versionHashes[0], detail.Versions[1].PreviousRegistrationSHA256)
	require.NotNil(t, detail.PublishedRegistration)
	require.True(t, detail.PublishedRegistration.CurrentChecksumVerified)
}

func TestHandleSoulInstanceRecoveryAgentFailsClosedOnArtifactConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stubRecord func(t *testing.T, server *Server, queries recoveryTestQueries, identity *models.SoulAgentIdentity)
	}{
		{
			name: "checksum mismatch",
			stubRecord: func(t *testing.T, server *Server, queries recoveryTestQueries, identity *models.SoulAgentIdentity) {
				t.Helper()
				queries.version.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(nil).Run(func(args mock.Arguments) {
					dest := testutil.RequireMockArg[*models.SoulAgentVersion](t, args, 0)
					*dest = models.SoulAgentVersion{
						AgentID: identity.AgentID, VersionNumber: 1,
						RegistrationURI:    fmt.Sprintf("s3://bucket/%s", soulRegistrationVersionedS3Key(identity.AgentID, 1)),
						RegistrationSHA256: strings.Repeat("a", 64), CreatedAt: time.Now().UTC(),
					}
				}).Once()
				pack, ok := server.soulPacks.(*recoveryPackStore)
				require.True(t, ok)
				pack.objects[soulRegistrationVersionedS3Key(identity.AgentID, 1)] = []byte(`{"different":true}`)
			},
		},
		{
			name: "orphan versioned object",
			stubRecord: func(t *testing.T, server *Server, queries recoveryTestQueries, identity *models.SoulAgentIdentity) {
				t.Helper()
				queries.version.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()
				pack, ok := server.soulPacks.(*recoveryPackStore)
				require.True(t, ok)
				pack.objects[soulRegistrationVersionedS3Key(identity.AgentID, 1)] = []byte(`{"orphan":true}`)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server, queries, identity, promotion, session, declarationBytes := newRecoveryTestServer(t, 1)
			stubRecoveryCommonReads(t, queries, identity, promotion, session, declarationBytes)
			tt.stubRecord(t, server, queries, identity)

			resp, err := server.handleSoulInstanceRecoveryAgent(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
			require.Nil(t, resp)
			var appErr *apptheory.AppTheoryError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
		})
	}
}

func TestHandleSoulInstanceRecoveryAgentRejectsInvalidAndRevokedKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		stub func(t *testing.T, query *ttmocks.MockQuery)
	}{
		{
			name: "invalid",
			stub: func(t *testing.T, query *ttmocks.MockQuery) {
				t.Helper()
				query.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()
			},
		},
		{
			name: "revoked",
			stub: func(t *testing.T, query *ttmocks.MockQuery) {
				t.Helper()
				query.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
					dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
					*dest = models.InstanceKey{
						ID: sha256HexTrimmed(mintConversationInstanceReadTestRawKey), InstanceSlug: recoveryTestSlug,
						CreatedAt: time.Now().Add(-time.Hour), RevokedAt: time.Now().Add(-time.Minute),
					}
				}).Once()
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
			tc.stub(t, queries.key)

			resp, err := server.handleSoulInstanceRecoveryAgent(newMintConversationInstanceReadContext(identity.AgentID, "", nil))
			require.Nil(t, resp)
			var appErr *apptheory.AppTheoryError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, soulRecoveryCodeUnauthorized, appErr.Code)
		})
	}
}

func TestHandleSoulInstanceRecoveryAgentsIsContentFree(t *testing.T) {
	t.Parallel()
	server, queries, identity, promotion, session, declarationBytes := newRecoveryTestServer(t, 1)
	stubRecoveryCommonReads(t, queries, identity, promotion, session, declarationBytes)
	queries.version.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()
	queries.instance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: recoveryTestSlug, Status: models.InstanceStatusActive, HostedBaseDomain: recoveryTestDomain}
	}).Once()
	queries.domain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: recoveryTestDomain, InstanceSlug: recoveryTestSlug, Status: models.DomainStatusVerified}}
	}).Once()
	queries.domainAgent.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulDomainAgentIndex")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: recoveryTestDomain, LocalID: recoveryTestLocalID, AgentID: identity.AgentID}}
	}).Once()

	ctx := newMintConversationInstanceReadContext("", "", map[string][]string{"limit": {"20"}})
	resp, err := server.handleSoulInstanceRecoveryAgents(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)
	require.NotContains(t, string(resp.Body), `"declarations"`)
	require.NotContains(t, string(resp.Body), `"selfDescription"`)

	var out soulRecoveryAgentListResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Len(t, out.Agents, 1)
	require.Equal(t, soulRecoveryClassificationLegacy, out.Agents[0].Classification)
}

func TestHandleSoulInstanceRecoveryAgentsSkipsInactiveEntryAndContinues(t *testing.T) {
	t.Parallel()
	server, queries, active, promotion, session, declarationBytes := newRecoveryTestServer(t, 1)
	pending := *active
	pending.AgentID = "0x" + strings.Repeat("1", 64)
	pending.LocalID = "juniper-sol"
	pending.Status = models.SoulAgentStatusPending
	pending.LifecycleStatus = models.SoulAgentStatusPending
	pending.SelfDescriptionVersion = 0

	stubRecoveryActiveKey(t, queries.key)
	queries.instance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: recoveryTestSlug, Status: models.InstanceStatusActive, HostedBaseDomain: recoveryTestDomain}
	}).Once()
	queries.domain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: recoveryTestDomain, InstanceSlug: recoveryTestSlug, Status: models.DomainStatusVerified}}
	}).Once()

	queries.domainAgent.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulDomainAgentIndex")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-pending"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: recoveryTestDomain, LocalID: pending.LocalID, AgentID: pending.AgentID}}
	}).Once()
	queries.domainAgent.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulDomainAgentIndex")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: recoveryTestDomain, LocalID: active.LocalID, AgentID: active.AgentID}}
	}).Once()

	identityRead := 0
	queries.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		identityRead++
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if identityRead == 1 {
			*dest = pending
			return
		}
		*dest = *active
	}).Twice()
	queries.domain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: recoveryTestDomain, InstanceSlug: recoveryTestSlug, Status: models.DomainStatusVerified}
	}).Twice()
	queries.promotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = *promotion
	}).Once()
	queries.hosted.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = []*models.HostedGenesisSession{session}
	}).Once()
	queries.conversation.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		*dest = models.SoulAgentMintConversation{
			AgentID: active.AgentID, ConversationID: recoveryTestConversation, Status: models.SoulMintConversationStatusCompleted,
			ProducedDeclarations: models.EncodeSoulMintConversationBlob(string(declarationBytes)), CompletedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		}
	}).Once()
	queries.version.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()

	cursor := encodeSoulRecoveryInventoryCursor(soulRecoveryInventoryCursor{DomainIndex: 0, Inner: "after-iris"})
	ctx := newMintConversationInstanceReadContext("", "", map[string][]string{"limit": {"1"}, "cursor": {cursor}})
	resp, err := server.handleSoulInstanceRecoveryAgents(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var out soulRecoveryAgentListResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Len(t, out.Agents, 1)
	require.Equal(t, active.AgentID, out.Agents[0].AgentID)
	require.False(t, out.HasMore)
	require.Empty(t, out.NextCursor)
}

func TestSoulRecoveryInventoryPreservesActiveIntegrityConflict(t *testing.T) {
	t.Parallel()
	server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
	queries.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = *identity
	}).Once()
	queries.domain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: recoveryTestDomain, InstanceSlug: recoveryTestSlug, Status: models.DomainStatusVerified}
	}).Once()
	queries.promotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(theoryErrors.ErrItemNotFound).Once()

	scan := &soulRecoveryInventoryScan{out: []soulRecoveryAgentSummary{}, seen: map[string]struct{}{}}
	appErr := server.appendSoulRecoveryInventoryItems(
		newMintConversationInstanceReadContext("", "", nil),
		recoveryTestSlug,
		recoveryTestDomain,
		[]*models.SoulDomainAgentIndex{{Domain: recoveryTestDomain, LocalID: identity.LocalID, AgentID: identity.AgentID}},
		scan,
	)
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
}

func TestSoulRecoveryInventoryIndexBindingMatchesIdentity(t *testing.T) {
	t.Parallel()
	identity, _, _ := recoveryIdentityPromotionSession(1)
	item := &models.SoulDomainAgentIndex{Domain: identity.Domain, LocalID: identity.LocalID, AgentID: identity.AgentID}
	require.True(t, soulRecoveryInventoryIndexMatchesIdentity(item, identity))

	wrongLocalID := *item
	wrongLocalID.LocalID = "other-agent"
	require.False(t, soulRecoveryInventoryIndexMatchesIdentity(&wrongLocalID, identity))

	wrongDomain := *item
	wrongDomain.Domain = "other.example"
	require.False(t, soulRecoveryInventoryIndexMatchesIdentity(&wrongDomain, identity))

	wrongAgent := *item
	wrongAgent.AgentID = "0x" + strings.Repeat("2", 64)
	require.False(t, soulRecoveryInventoryIndexMatchesIdentity(&wrongAgent, identity))
	require.False(t, soulRecoveryInventoryIndexMatchesIdentity(nil, identity))
	require.False(t, soulRecoveryInventoryIndexMatchesIdentity(item, nil))
}

func TestSoulRecoveryCursorAndRateLimitClassification(t *testing.T) {
	t.Parallel()
	cursor := soulRecoveryInventoryCursor{DomainIndex: 2, Inner: "opaque"}
	decoded, appErr := decodeSoulRecoveryInventoryCursor(encodeSoulRecoveryInventoryCursor(cursor))
	require.Nil(t, appErr)
	require.Equal(t, cursor, decoded)
	_, appErr = decodeSoulRecoveryInventoryCursor("not-base64")
	require.NotNil(t, appErr)

	require.True(t, isSoulMintConversationInstanceReadPath("/api/v1/soul/instance/recovery/agents"))
	require.True(t, isSoulMintConversationInstanceReadPath("/api/v1/soul/instance/recovery/agents/0xabc"))
	require.Equal(t, soulRecoveryRouteInventory, soulMintConversationInstanceReadRateLimitRouteClass("/api/v1/soul/instance/recovery/agents"))
	require.Equal(t, soulRecoveryRouteDetail, soulMintConversationInstanceReadRateLimitRouteClass("/api/v1/soul/instance/recovery/agents/0xabc"))
}

func TestSoulRecoveryInventoryScanBounds(t *testing.T) {
	t.Parallel()

	server := &Server{}
	ctx := newMintConversationInstanceReadContext("", "", nil)
	scan := &soulRecoveryInventoryScan{
		domainIndex: 0,
		pageCount:   soulRecoveryMaxScanPages,
		out:         []soulRecoveryAgentSummary{},
		seen:        map[string]struct{}{},
	}
	done, hasMore, next, appErr := server.scanSoulRecoveryInventoryPage(ctx, recoveryTestSlug, []string{recoveryTestDomain}, 20, scan)
	require.Nil(t, appErr)
	require.True(t, done)
	require.True(t, hasMore)
	require.NotEmpty(t, next)

	scan.domainIndex = 1
	scan.pageCount = 0
	done, hasMore, next, appErr = server.scanSoulRecoveryInventoryPage(ctx, recoveryTestSlug, []string{recoveryTestDomain}, 20, scan)
	require.Nil(t, appErr)
	require.True(t, done)
	require.False(t, hasMore)
	require.Empty(t, next)

	appErr = server.appendSoulRecoveryInventoryItems(ctx, recoveryTestSlug, recoveryTestDomain, []*models.SoulDomainAgentIndex{{Domain: "other.example", AgentID: soulLifecycleTestAgentIDHex}}, scan)
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
}

func TestSoulRecoveryErrorAndProjectionHelpers(t *testing.T) {
	t.Parallel()

	require.Nil(t, soulRecoveryAccessError(nil))
	internal := soulRecoveryAccessError(apptheory.NewAppTheoryError(soulMintAppErrCodeInternal, "storage failed"))
	require.Equal(t, soulRecoveryCodeInternal, internal.Code)
	require.Equal(t, http.StatusInternalServerError, internal.StatusCode)
	boundary := soulRecoveryAccessError(apptheory.NewAppTheoryError(soulMintAppErrCodeConflict, "domain is not verified"))
	require.Equal(t, soulRecoveryCodeBoundaryViolation, boundary.Code)
	require.Equal(t, http.StatusForbidden, boundary.StatusCode)

	tooLarge := soulRecoveryResponseError(
		apptheory.NewAppTheoryError(soulMintInstanceReadCodeResponseTooLarge, "too large").WithStatusCode(http.StatusRequestEntityTooLarge),
	)
	require.Equal(t, soulRecoveryCodeResponseTooLarge, tooLarge.Code)
	passthrough := apptheory.NewAppTheoryError("custom.error", "custom")
	require.Same(t, passthrough, soulRecoveryResponseError(passthrough))
	require.Equal(t, soulRecoveryCodeInternal, soulRecoveryResponseError(errors.New("marshal failed")).Code)

	require.Equal(t, soulRecoveryAgentSummary{}, soulRecoverySummaryFromDetail(nil))
	require.True(t, soulRecoverySHA256Matches([]byte("payload"), recoverySHA256([]byte("payload"))))
	require.False(t, soulRecoverySHA256Matches([]byte("payload"), "short"))
	require.False(t, soulRecoverySHA256Matches([]byte("payload"), strings.Repeat("a", 64)))

	limit, appErr := parseSoulRecoveryLimit(newMintConversationInstanceReadContext("", "", nil))
	require.Nil(t, appErr)
	require.Equal(t, soulRecoveryDefaultLimit, limit)
	limit, appErr = parseSoulRecoveryLimit(newMintConversationInstanceReadContext("", "", map[string][]string{"limit": {"999"}}))
	require.Nil(t, appErr)
	require.Equal(t, soulRecoveryMaxLimit, limit)
	_, appErr = parseSoulRecoveryLimit(newMintConversationInstanceReadContext("", "", map[string][]string{"limit": {"zero"}}))
	require.NotNil(t, appErr)
	require.Equal(t, soulRecoveryCodeInvalidRequest, appErr.Code)
}

func TestSoulRecoveryHandlersRejectInvalidReadShapes(t *testing.T) {
	t.Parallel()

	t.Run("detail body", func(t *testing.T) {
		t.Parallel()
		server, _, identity, _, _, _ := newRecoveryTestServer(t, 1)
		ctx := newMintConversationInstanceReadContext(identity.AgentID, "", nil)
		ctx.Request.Body = []byte(`{"not":"allowed"}`)
		resp, err := server.handleSoulInstanceRecoveryAgent(ctx)
		require.Nil(t, resp)
		var appErr *apptheory.AppTheoryError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, soulRecoveryCodeInvalidRequest, appErr.Code)
	})

	t.Run("invalid agent id", func(t *testing.T) {
		t.Parallel()
		server, queries, _, _, _, _ := newRecoveryTestServer(t, 1)
		stubRecoveryActiveKey(t, queries.key)
		resp, err := server.handleSoulInstanceRecoveryAgent(newMintConversationInstanceReadContext("not-an-agent", "", nil))
		require.Nil(t, resp)
		var appErr *apptheory.AppTheoryError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, soulRecoveryCodeInvalidRequest, appErr.Code)
	})

	t.Run("invalid inventory limit", func(t *testing.T) {
		t.Parallel()
		server, queries, _, _, _, _ := newRecoveryTestServer(t, 1)
		stubRecoveryActiveKey(t, queries.key)
		ctx := newMintConversationInstanceReadContext("", "", map[string][]string{"limit": {"zero"}})
		resp, err := server.handleSoulInstanceRecoveryAgents(ctx)
		require.Nil(t, resp)
		var appErr *apptheory.AppTheoryError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, soulRecoveryCodeInvalidRequest, appErr.Code)
	})

	t.Run("invalid inventory cursor", func(t *testing.T) {
		t.Parallel()
		server, queries, _, _, _, _ := newRecoveryTestServer(t, 1)
		stubRecoveryActiveKey(t, queries.key)
		ctx := newMintConversationInstanceReadContext("", "", map[string][]string{"cursor": {"not-base64"}})
		resp, err := server.handleSoulInstanceRecoveryAgents(ctx)
		require.Nil(t, resp)
		var appErr *apptheory.AppTheoryError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, soulRecoveryCodeInvalidRequest, appErr.Code)
	})
}

func TestLoadSoulRecoveryIdentityFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("inactive", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
		queries.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = *identity
			dest.Status = models.SoulAgentStatusPending
			dest.LifecycleStatus = models.SoulAgentStatusPending
		}).Once()
		queries.domain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: recoveryTestDomain, InstanceSlug: recoveryTestSlug, Status: models.DomainStatusVerified}
		}).Once()

		got, _, appErr := server.loadSoulRecoveryIdentity(context.Background(), recoveryTestSlug, identity.AgentID)
		require.Nil(t, got)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
		require.Equal(t, "agent is not active recovery state", appErr.Message)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
		queries.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
		got, _, appErr := server.loadSoulRecoveryIdentity(context.Background(), recoveryTestSlug, identity.AgentID)
		require.Nil(t, got)
		require.Equal(t, soulRecoveryCodeNotFound, appErr.Code)
	})

	t.Run("store failure", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
		queries.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("read failed")).Once()
		got, _, appErr := server.loadSoulRecoveryIdentity(context.Background(), recoveryTestSlug, identity.AgentID)
		require.Nil(t, got)
		require.Equal(t, soulRecoveryCodeInternal, appErr.Code)
	})

	t.Run("identity binding mismatch", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
		queries.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = *identity
			dest.AgentID = strings.Repeat("0", 66)
		}).Once()
		got, _, appErr := server.loadSoulRecoveryIdentity(context.Background(), recoveryTestSlug, identity.AgentID)
		require.Nil(t, got)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
	})
}

func TestSoulRecoveryStorageFailuresAreTyped(t *testing.T) {
	t.Parallel()

	t.Run("promotion missing", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
		queries.promotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(theoryErrors.ErrItemNotFound).Once()
		promotion, appErr := server.loadSoulRecoveryPromotion(context.Background(), identity.AgentID)
		require.Nil(t, promotion)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
	})

	t.Run("promotion store failure", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, _, _, _ := newRecoveryTestServer(t, 1)
		queries.promotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(errors.New("read failed")).Once()
		promotion, appErr := server.loadSoulRecoveryPromotion(context.Background(), identity.AgentID)
		require.Nil(t, promotion)
		require.Equal(t, soulRecoveryCodeInternal, appErr.Code)
	})

	t.Run("declaration store failure", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, promotion, session, _ := newRecoveryTestServer(t, 1)
		queries.hosted.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
			*dest = []*models.HostedGenesisSession{session}
		}).Once()
		queries.conversation.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(errors.New("read failed")).Once()
		got, _, _, appErr := server.loadSoulRecoveryDeclarationSource(context.Background(), recoveryTestSlug, identity.AgentID, identity, promotion)
		require.Nil(t, got)
		require.Equal(t, soulRecoveryCodeInternal, appErr.Code)
	})

	t.Run("declaration source missing", func(t *testing.T) {
		t.Parallel()
		server, queries, identity, promotion, session, _ := newRecoveryTestServer(t, 1)
		queries.hosted.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
			*dest = []*models.HostedGenesisSession{session}
		}).Once()
		queries.conversation.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Once()
		got, _, _, appErr := server.loadSoulRecoveryDeclarationSource(context.Background(), recoveryTestSlug, identity.AgentID, identity, promotion)
		require.Nil(t, got)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
	})

	t.Run("version history bound", func(t *testing.T) {
		t.Parallel()
		server, _, identity, _, _, _ := newRecoveryTestServer(t, soulRecoveryMaxVersions+1)
		state, appErr := server.loadSoulRecoveryArtifacts(context.Background(), identity)
		require.Empty(t, state.Versions)
		require.Equal(t, soulRecoveryCodeResponseTooLarge, appErr.Code)
	})

	t.Run("oversize object", func(t *testing.T) {
		t.Parallel()
		server, _, _, _, _, _ := newRecoveryTestServer(t, 1)
		pack, ok := server.soulPacks.(*recoveryPackStore)
		require.True(t, ok)
		pack.errors["oversize"] = errors.New("object too large")
		_, present, appErr := server.getSoulRecoveryObject(context.Background(), "oversize")
		require.False(t, present)
		require.Equal(t, soulRecoveryCodeResponseTooLarge, appErr.Code)
	})

	t.Run("object store failure", func(t *testing.T) {
		t.Parallel()
		server, _, _, _, _, _ := newRecoveryTestServer(t, 1)
		pack, ok := server.soulPacks.(*recoveryPackStore)
		require.True(t, ok)
		pack.errors["failed"] = errors.New("s3 unavailable")
		_, present, appErr := server.getSoulRecoveryObject(context.Background(), "failed")
		require.False(t, present)
		require.Equal(t, soulRecoveryCodeInternal, appErr.Code)
	})

	t.Run("incomplete promotion binding", func(t *testing.T) {
		t.Parallel()
		appErr := validateSoulRecoveryPromotionIdentity(nil, nil)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
		identity, promotion, _ := recoveryIdentityPromotionSession(1)
		promotion.LocalID = "other-agent"
		appErr = validateSoulRecoveryPromotionIdentity(identity, promotion)
		require.Equal(t, soulRecoveryCodeIntegrityConflict, appErr.Code)
	})

	t.Run("instance store failure", func(t *testing.T) {
		t.Parallel()
		server, queries, _, _, _, _ := newRecoveryTestServer(t, 1)
		queries.instance.On("First", mock.AnythingOfType("*models.Instance")).Return(errors.New("read failed")).Once()
		domains, appErr := server.loadSoulRecoveryDomains(newMintConversationInstanceReadContext("", "", nil), recoveryTestSlug)
		require.Nil(t, domains)
		require.Equal(t, soulRecoveryCodeInternal, appErr.Code)
	})

	t.Run("instance missing", func(t *testing.T) {
		t.Parallel()
		server, queries, _, _, _, _ := newRecoveryTestServer(t, 1)
		queries.instance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
		domains, appErr := server.loadSoulRecoveryDomains(newMintConversationInstanceReadContext("", "", nil), recoveryTestSlug)
		require.Nil(t, domains)
		require.Equal(t, soulRecoveryCodeUnauthorized, appErr.Code)
	})
}

func newRecoveryTestServer(t *testing.T, version int) (*Server, recoveryTestQueries, *models.SoulAgentIdentity, *models.SoulAgentPromotion, *models.HostedGenesisSession, []byte) {
	t.Helper()
	typeNames := []string{
		"*models.InstanceKey", "*models.SoulAgentIdentity", "*models.Domain", "*models.Instance", "*models.SoulAgentPromotion",
		"*models.HostedGenesisSession", "*models.SoulAgentMintConversation", "*models.SoulAgentVersion", "*models.AuditLogEntry", "*models.SoulDomainAgentIndex",
	}
	db, q := newTestDBWithModelQueries(typeNames...)
	for _, query := range q {
		query.On("Cursor", mock.Anything).Return(query).Maybe()
	}
	queries := recoveryTestQueries{key: q[0], identity: q[1], domain: q[2], instance: q[3], promotion: q[4], hosted: q[5], conversation: q[6], version: q[7], audit: q[8], domainAgent: q[9]}
	identity, promotion, session := recoveryIdentityPromotionSession(version)
	declarations, err := json.Marshal(testMintConversationDecl())
	require.NoError(t, err)
	server := &Server{
		cfg:       config.Config{SoulEnabled: true, SoulPackBucketName: "bucket", Stage: "live"},
		store:     store.New(db),
		soulPacks: &recoveryPackStore{objects: map[string][]byte{}, errors: map[string]error{}},
	}
	return server, queries, identity, promotion, session, declarations
}

func recoveryIdentityPromotionSession(version int) (*models.SoulAgentIdentity, *models.SoulAgentPromotion, *models.HostedGenesisSession) {
	agentID := soulLifecycleTestAgentIDHex
	identity := &models.SoulAgentIdentity{
		AgentID: agentID, Domain: recoveryTestDomain, LocalID: recoveryTestLocalID,
		Status: models.SoulAgentStatusActive, SelfDescriptionVersion: version,
	}
	promotion := &models.SoulAgentPromotion{
		AgentID: agentID, RegistrationID: recoveryTestRegistration, Domain: recoveryTestDomain, LocalID: recoveryTestLocalID,
		Stage: models.SoulAgentPromotionStageGraduated, ReviewStatus: models.SoulAgentPromotionReviewStatusPublished,
		ReadinessStatus: models.SoulAgentPromotionReadinessGraduated, LatestConversationID: recoveryTestConversation,
		LatestConversationStatus: models.SoulMintConversationStatusCompleted, PublishedVersion: version, GraduatedAt: time.Date(2026, 8, 1, 2, 3, 4, 0, time.UTC),
	}
	session := &models.HostedGenesisSession{
		InstanceSlug: recoveryTestSlug, RegistrationID: recoveryTestRegistration, AgentID: agentID,
		ConversationID: recoveryTestConversation, Status: models.SoulMintConversationStatusCompleted,
		CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	return identity, promotion, session
}

func stubRecoveryCommonReads(t *testing.T, q recoveryTestQueries, identity *models.SoulAgentIdentity, promotion *models.SoulAgentPromotion, session *models.HostedGenesisSession, declarations []byte) {
	t.Helper()
	stubRecoveryActiveKey(t, q.key)
	q.identity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = *identity
	}).Once()
	q.domain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: recoveryTestDomain, InstanceSlug: recoveryTestSlug, Status: models.DomainStatusVerified}
	}).Once()
	q.promotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = *promotion
	}).Once()
	q.hosted.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = []*models.HostedGenesisSession{session}
	}).Once()
	q.conversation.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentMintConversation](t, args, 0)
		*dest = models.SoulAgentMintConversation{
			AgentID: identity.AgentID, ConversationID: recoveryTestConversation, Status: models.SoulMintConversationStatusCompleted,
			ProducedDeclarations: models.EncodeSoulMintConversationBlob(string(declarations)), CompletedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		}
	}).Once()
}

func stubRecoveryActiveKey(t *testing.T, query *ttmocks.MockQuery) {
	t.Helper()
	query.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: sha256HexTrimmed(mintConversationInstanceReadTestRawKey), InstanceSlug: recoveryTestSlug, CreatedAt: time.Now().Add(-time.Hour)}
	}).Once()
}

func recoverySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
