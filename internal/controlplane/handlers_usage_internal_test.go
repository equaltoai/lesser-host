package controlplane

import (
	"errors"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type usageTestDB struct {
	db     *ttmocks.MockExtendedDB
	qInst  *ttmocks.MockQuery
	qUsage *ttmocks.MockQuery
}

func newUsageTestDB() usageTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qUsage := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qUsage).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qUsage} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	return usageTestDB{
		db:     db,
		qInst:  qInst,
		qUsage: qUsage,
	}
}

func TestHandleListInstanceUsage_ValidatesInput(t *testing.T) {
	t.Parallel()

	tdb := newUsageTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	if _, err := s.handleListInstanceUsage(ctx); err == nil {
		t.Fatalf("expected bad_request for missing slug/month")
	}

	ctx.Params = map[string]string{"slug": "demo"}
	if _, err := s.handleListInstanceUsage(ctx); err == nil {
		t.Fatalf("expected bad_request for missing month")
	}

	ctx.Params = map[string]string{"slug": "demo", "month": "bad"}
	if _, err := s.handleListInstanceUsage(ctx); err == nil {
		t.Fatalf("expected bad_request for invalid month")
	}
}

func TestHandleListInstanceUsage_InstanceNotFound(t *testing.T) {
	t.Parallel()

	tdb := newUsageTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-02"}}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	if _, err := s.handleListInstanceUsage(ctx); err == nil {
		t.Fatalf("expected not_found")
	}
}

func TestHandleListInstanceUsage_ListErrorAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newUsageTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo"}
	}).Maybe()

	boom := errors.New("boom")
	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.UsageLedgerEntry")).Return(boom).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-02"}}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	if _, err := s.handleListInstanceUsage(ctx); err == nil {
		t.Fatalf("expected internal error")
	}

	tdb.qUsage.On("All", mock.AnythingOfType("*[]*models.UsageLedgerEntry")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.UsageLedgerEntry)
		*dest = []*models.UsageLedgerEntry{{ID: "e1"}, {ID: "e2"}}
	}).Once()

	resp, err := s.handleListInstanceUsage(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected response: %#v err=%v", resp, err)
	}
}
