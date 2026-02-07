package controlplane

import (
	"encoding/json"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type adminInstanceTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qDomain *ttmocks.MockQuery
	qKey    *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newAdminInstanceTestDB() adminInstanceTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qKey := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qDomain, qKey, qBudget, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Filter", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return adminInstanceTestDB{
		db:      db,
		qInst:   qInst,
		qDomain: qDomain,
		qKey:    qKey,
		qBudget: qBudget,
		qAudit:  qAudit,
	}
}

func adminCtx() *apptheory.Context {
	ctx := &apptheory.Context{AuthIdentity: "admin", RequestID: "r1"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	return ctx
}

func TestHandleCreateInstance_AndListInstances(t *testing.T) {
	t.Parallel()

	tdb := newAdminInstanceTestDB()
	s := &Server{cfg: config.Config{TipEnabled: false}, store: store.New(tdb.db)}

	body, _ := json.Marshal(createInstanceRequest{Slug: "demo", Owner: "alice"})
	ctx := adminCtx()
	ctx.Request.Body = body

	resp, err := s.handleCreateInstance(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d", resp.Status)
	}

	tdb.qInst.On("Scan", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.Instance)
		*dest = []*models.Instance{{Slug: "demo"}}
	}).Once()

	resp, err = s.handleListInstances(adminCtx())
	if err != nil || resp.Status != 200 {
		t.Fatalf("list instances: resp=%#v err=%v", resp, err)
	}
}

func TestHandleCreateInstanceKey_AndUpdateConfig_AndSetBudget(t *testing.T) {
	t.Parallel()

	tdb := newAdminInstanceTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	// Instance exists (called multiple times across the handlers below).
	instCall := 0
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		instCall++
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		// The update handler reloads the instance after persisting config.
		if instCall == 3 {
			dest.RenderPolicy = renderPolicyAlways
		}
	}).Times(5)

	tdb.qKey.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo"}
	resp, err := s.handleCreateInstanceKey(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d", resp.Status)
	}

	// Update config (render_policy).
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"slug": "demo"}
	ctx2.Request.Body = []byte(`{"render_policy":"always"}`)

	resp, err = s.handleUpdateInstanceConfig(ctx2)
	if err != nil {
		t.Fatalf("update config err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	// Budget month set (preserve used credits when missing).
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx3 := adminCtx()
	ctx3.Params = map[string]string{"slug": "demo", "month": "2026-02"}
	ctx3.Request.Body, _ = json.Marshal(setBudgetMonthRequest{IncludedCredits: 100})
	resp, err = s.handleSetInstanceBudgetMonth(ctx3)
	if err != nil {
		t.Fatalf("set budget err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	// Existing record preserves UsedCredits.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{UsedCredits: 50}
	}).Once()
	tdb.qBudget.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx4 := adminCtx()
	ctx4.Params = map[string]string{"slug": "demo", "month": "2026-02"}
	ctx4.Request.Body, _ = json.Marshal(setBudgetMonthRequest{IncludedCredits: 200})
	resp, err = s.handleSetInstanceBudgetMonth(ctx4)
	if err != nil {
		t.Fatalf("set budget (existing) err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out budgetMonthResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.UsedCredits != 50 {
		t.Fatalf("expected used credits preserved, got %#v", out)
	}

	// Time sanity (avoid zero).
	if out.UpdatedAt.IsZero() {
		t.Fatalf("expected UpdatedAt set")
	}
}
