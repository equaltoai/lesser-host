package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type storeMoreTestDB struct {
	db       *ttmocks.MockExtendedDB
	qPreview *ttmocks.MockQuery
	qLSB     *ttmocks.MockQuery
	qRender  *ttmocks.MockQuery
	qProv    *ttmocks.MockQuery
}

func newStoreMoreTestDB(t *testing.T) storeMoreTestDB {
	t.Helper()

	db := ttmocks.NewMockExtendedDBStrict()
	qPreview := new(ttmocks.MockQuery)
	qLSB := new(ttmocks.MockQuery)
	qRender := new(ttmocks.MockQuery)
	qProv := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.LinkPreview")).Return(qPreview)
	db.On("Model", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(qLSB)
	db.On("Model", mock.AnythingOfType("*models.RenderArtifact")).Return(qRender)
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qProv)

	for _, q := range []*ttmocks.MockQuery{qPreview, qLSB, qRender, qProv} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("Index", mock.Anything).Return(q)
		q.On("Limit", mock.Anything).Return(q)
		q.On("ConsistentRead").Return(q)
	}

	qPreview.On("First", mock.AnythingOfType("*models.LinkPreview")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkPreview](t, args, 0)
		dest.ID = "p1"
	})
	qPreview.On("CreateOrUpdate").Return(nil)

	qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkSafetyBasicResult](t, args, 0)
		dest.ID = "r1"
	})
	qLSB.On("CreateOrUpdate").Return(nil)

	qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		dest.ID = "ra1"
	})
	qRender.On("CreateOrUpdate").Return(nil)
	qRender.On("Delete").Return(nil)
	qRender.On("All", mock.AnythingOfType("*[]*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.RenderArtifact](t, args, 0)
		*dest = []*models.RenderArtifact{{ID: "expired"}}
	})

	qProv.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		dest.ID = "j1"
	})
	qProv.On("CreateOrUpdate").Return(nil)

	return storeMoreTestDB{
		db:       db,
		qPreview: qPreview,
		qLSB:     qLSB,
		qRender:  qRender,
		qProv:    qProv,
	}
}

func TestStore_MoreQueriesAndValidations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tdb := newStoreMoreTestDB(t)
	st := New(tdb.db)

	_, err := st.GetLinkPreview(ctx, " ")
	require.Error(t, err)
	require.Error(t, st.PutLinkPreview(ctx, nil))
	p, err := st.GetLinkPreview(ctx, "p1")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, "p1", p.ID)
	require.NoError(t, st.PutLinkPreview(ctx, &models.LinkPreview{ID: "p1"}))

	_, err = st.GetLinkSafetyBasicResult(ctx, " ")
	require.Error(t, err)
	require.Error(t, st.PutLinkSafetyBasicResult(ctx, nil))
	r, err := st.GetLinkSafetyBasicResult(ctx, "r1")
	require.NoError(t, err)
	require.NotNil(t, r)
	require.Equal(t, "r1", r.ID)
	require.NoError(t, st.PutLinkSafetyBasicResult(ctx, &models.LinkSafetyBasicResult{ID: "r1"}))

	_, err = st.GetRenderArtifact(ctx, " ")
	require.Error(t, err)
	require.Error(t, st.PutRenderArtifact(ctx, nil))
	a, err := st.GetRenderArtifact(ctx, "ra1")
	require.NoError(t, err)
	require.NotNil(t, a)
	require.Equal(t, "ra1", a.ID)
	require.NoError(t, st.PutRenderArtifact(ctx, &models.RenderArtifact{ID: "ra1"}))
	require.Error(t, st.DeleteRenderArtifact(ctx, " "))
	require.NoError(t, st.DeleteRenderArtifact(ctx, "ra1"))

	items, err := st.ListExpiredRenderArtifacts(ctx, time.Time{}, -1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "expired", items[0].ID)

	_, err = st.GetProvisionJob(ctx, " ")
	require.Error(t, err)
	require.Error(t, st.PutProvisionJob(ctx, nil))
	j, err := st.GetProvisionJob(ctx, "j1")
	require.NoError(t, err)
	require.NotNil(t, j)
	require.Equal(t, "j1", j.ID)
	require.NoError(t, st.PutProvisionJob(ctx, &models.ProvisionJob{ID: "j1"}))
}
