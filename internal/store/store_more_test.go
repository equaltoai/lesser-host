package store

import (
	"context"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type storeMoreTestDB struct {
	db       *ttmocks.MockExtendedDB
	qPreview *ttmocks.MockQuery
	qLSB     *ttmocks.MockQuery
	qRender  *ttmocks.MockQuery
	qProv    *ttmocks.MockQuery
}

func newStoreMoreTestDB() storeMoreTestDB {
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
		dest := args.Get(0).(*models.LinkPreview)
		dest.ID = "p1"
	})
	qPreview.On("CreateOrUpdate").Return(nil)

	qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.LinkSafetyBasicResult)
		dest.ID = "r1"
	})
	qLSB.On("CreateOrUpdate").Return(nil)

	qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		dest.ID = "ra1"
	})
	qRender.On("CreateOrUpdate").Return(nil)
	qRender.On("Delete").Return(nil)
	qRender.On("All", mock.AnythingOfType("*[]*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.RenderArtifact)
		*dest = []*models.RenderArtifact{{ID: "expired"}}
	})

	qProv.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
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
	tdb := newStoreMoreTestDB()
	st := New(tdb.db)

	if _, err := st.GetLinkPreview(ctx, " "); err == nil {
		t.Fatalf("expected error for empty preview id")
	}
	if err := st.PutLinkPreview(ctx, nil); err == nil {
		t.Fatalf("expected error for nil preview")
	}
	if p, err := st.GetLinkPreview(ctx, "p1"); err != nil || p == nil || p.ID != "p1" {
		t.Fatalf("GetLinkPreview: p=%#v err=%v", p, err)
	}
	if err := st.PutLinkPreview(ctx, &models.LinkPreview{ID: "p1"}); err != nil {
		t.Fatalf("PutLinkPreview: %v", err)
	}

	if _, err := st.GetLinkSafetyBasicResult(ctx, " "); err == nil {
		t.Fatalf("expected error for empty result id")
	}
	if err := st.PutLinkSafetyBasicResult(ctx, nil); err == nil {
		t.Fatalf("expected error for nil result")
	}
	if r, err := st.GetLinkSafetyBasicResult(ctx, "r1"); err != nil || r == nil || r.ID != "r1" {
		t.Fatalf("GetLinkSafetyBasicResult: r=%#v err=%v", r, err)
	}
	if err := st.PutLinkSafetyBasicResult(ctx, &models.LinkSafetyBasicResult{ID: "r1"}); err != nil {
		t.Fatalf("PutLinkSafetyBasicResult: %v", err)
	}

	if _, err := st.GetRenderArtifact(ctx, " "); err == nil {
		t.Fatalf("expected error for empty artifact id")
	}
	if err := st.PutRenderArtifact(ctx, nil); err == nil {
		t.Fatalf("expected error for nil artifact")
	}
	if a, err := st.GetRenderArtifact(ctx, "ra1"); err != nil || a == nil || a.ID != "ra1" {
		t.Fatalf("GetRenderArtifact: a=%#v err=%v", a, err)
	}
	if err := st.PutRenderArtifact(ctx, &models.RenderArtifact{ID: "ra1"}); err != nil {
		t.Fatalf("PutRenderArtifact: %v", err)
	}
	if err := st.DeleteRenderArtifact(ctx, " "); err == nil {
		t.Fatalf("expected error for empty artifact id")
	}
	if err := st.DeleteRenderArtifact(ctx, "ra1"); err != nil {
		t.Fatalf("DeleteRenderArtifact: %v", err)
	}

	items, err := st.ListExpiredRenderArtifacts(ctx, time.Time{}, -1)
	if err != nil || len(items) != 1 || items[0].ID != "expired" {
		t.Fatalf("ListExpiredRenderArtifacts: items=%#v err=%v", items, err)
	}

	if _, err := st.GetProvisionJob(ctx, " "); err == nil {
		t.Fatalf("expected error for empty job id")
	}
	if err := st.PutProvisionJob(ctx, nil); err == nil {
		t.Fatalf("expected error for nil job")
	}
	if j, err := st.GetProvisionJob(ctx, "j1"); err != nil || j == nil || j.ID != "j1" {
		t.Fatalf("GetProvisionJob: j=%#v err=%v", j, err)
	}
	if err := st.PutProvisionJob(ctx, &models.ProvisionJob{ID: "j1"}); err != nil {
		t.Fatalf("PutProvisionJob: %v", err)
	}
}

