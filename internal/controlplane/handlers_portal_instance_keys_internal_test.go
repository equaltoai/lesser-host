package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestInstanceKeyListItemFromModel_Nil(t *testing.T) {
	t.Parallel()

	out := instanceKeyListItemFromModel(nil)
	require.Empty(t, out.ID)
}

func TestHandlePortalListInstanceKeys_ReturnsKeys(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qKey := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	addStandardMockQueryStubs(qKey)

	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testPortalInstanceSlugDemo, Owner: "alice"}
	}).Once()

	now := time.Unix(10, 0).UTC()
	qKey.On("All", mock.AnythingOfType("*[]*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.InstanceKey](t, args, 0)
		*dest = []*models.InstanceKey{
			{ID: "k1", InstanceSlug: testPortalInstanceSlugDemo, CreatedAt: now},
		}
	}).Once()

	resp, err := s.handlePortalListInstanceKeys(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var parsed listInstanceKeysResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, 1, parsed.Count)
	require.Len(t, parsed.Keys, 1)
	require.Equal(t, "k1", parsed.Keys[0].ID)
}

func TestHandlePortalRevokeInstanceKey_NotFoundAndAlreadyRevoked(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qKey := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	tdb.db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	addStandardMockQueryStubs(qKey)
	addStandardMockQueryStubs(qAudit)

	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo, "keyId": "k1"}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testPortalInstanceSlugDemo, Owner: "alice"}
	}).Once()

	// Key not found.
	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(theoryErrors.ErrItemNotFound).Once()
	_, err := s.handlePortalRevokeInstanceKey(ctx)
	require.Error(t, err)

	// Already revoked.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testPortalInstanceSlugDemo, Owner: "alice"}
	}).Once()

	revokedAt := time.Unix(10, 0).UTC()
	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1", InstanceSlug: testPortalInstanceSlugDemo, RevokedAt: revokedAt}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err := s.handlePortalRevokeInstanceKey(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)
}

