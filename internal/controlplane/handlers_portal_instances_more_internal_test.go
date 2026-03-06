package controlplane

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type portalHandlerDB struct {
	db        *ttmocks.MockExtendedDB
	qInstance *ttmocks.MockQuery
	qBudget   *ttmocks.MockQuery
	qAudit    *ttmocks.MockQuery
	qJob      *ttmocks.MockQuery
}

func newPortalHandlerDB() portalHandlerDB {
	db := ttmocks.NewMockExtendedDB()
	qInstance := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInstance, qBudget, qAudit, qJob} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}
	qAudit.On("Create").Return(nil).Maybe()

	return portalHandlerDB{
		db:        db,
		qInstance: qInstance,
		qBudget:   qBudget,
		qAudit:    qAudit,
		qJob:      qJob,
	}
}

func requirePortalAppErrorCode(t *testing.T, err error, code string) {
	t.Helper()

	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok, "expected *apptheory.AppError, got %T", err)
	require.Equal(t, code, appErr.Code)
}

func stubPortalOwnedInstance(t *testing.T, q *ttmocks.MockQuery, slug, owner string) {
	t.Helper()

	q.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: slug, Owner: owner, Status: models.InstanceStatusActive}
	}).Once()
}

func TestRequirePortalCreateInstancePrereqs_Branches(t *testing.T) {
	t.Parallel()

	t.Run("requires authentication", func(t *testing.T) {
		appErr := (&Server{}).requirePortalCreateInstancePrereqs(&apptheory.Context{})
		require.Equal(t, "app.unauthorized", appErr.Code)
	})

	t.Run("requires store", func(t *testing.T) {
		appErr := (&Server{}).requirePortalCreateInstancePrereqs(&apptheory.Context{AuthIdentity: "alice"})
		require.Equal(t, "app.internal", appErr.Code)
	})

	t.Run("requires approval", func(t *testing.T) {
		tdb := newPortalTestDB()
		tdb.stubUser.Approved = false
		tdb.stubUser.ApprovalStatus = models.UserApprovalStatusPending

		appErr := (&Server{store: store.New(tdb.db)}).requirePortalCreateInstancePrereqs(&apptheory.Context{AuthIdentity: "alice"})
		require.Equal(t, "app.forbidden", appErr.Code)
	})
}

func TestMaybeReturnExistingPortalInstance_Branches(t *testing.T) {
	t.Parallel()

	t.Run("operator can reuse existing instance", func(t *testing.T) {
		tdb := newPortalTestDB()
		srv := &Server{store: store.New(tdb.db)}
		ctx := &apptheory.Context{}
		ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "someone-else", Status: models.InstanceStatusActive}
		}).Once()

		inst, ok, appErr := srv.maybeReturnExistingPortalInstance(ctx, "demo", "alice")
		require.True(t, ok)
		require.Nil(t, appErr)
		require.Equal(t, "demo", inst.Slug)
	})

	t.Run("owner mismatch conflicts", func(t *testing.T) {
		tdb := newPortalTestDB()
		srv := &Server{store: store.New(tdb.db)}

		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "bob", Status: models.InstanceStatusActive}
		}).Once()

		inst, ok, appErr := srv.maybeReturnExistingPortalInstance(&apptheory.Context{}, "demo", "alice")
		require.Nil(t, inst)
		require.False(t, ok)
		require.Equal(t, "app.conflict", appErr.Code)
	})

	t.Run("lookup errors become internal", func(t *testing.T) {
		tdb := newPortalTestDB()
		srv := &Server{store: store.New(tdb.db)}
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(errors.New("boom")).Once()

		inst, ok, appErr := srv.maybeReturnExistingPortalInstance(&apptheory.Context{}, "demo", "alice")
		require.Nil(t, inst)
		require.False(t, ok)
		require.Equal(t, "app.internal", appErr.Code)
	})
}

func TestCreatePortalInstanceTx_ErrorBranches(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	inst, appErr := buildPortalInstanceDefaults("demo", "alice", now)
	require.Nil(t, appErr)
	primaryDomain := (&Server{}).buildPortalPrimaryDomain("demo", now)
	ctx := &apptheory.Context{RequestID: "req"}

	t.Run("nil server is internal", func(t *testing.T) {
		appErr := (*Server)(nil).createPortalInstanceTx(ctx, inst, primaryDomain, "alice", now)
		require.Equal(t, "app.internal", appErr.Code)
	})

	t.Run("tip registry config errors bubble up", func(t *testing.T) {
		tdb := newPortalTestDB()
		srv := &Server{
			cfg:   config.Config{TipEnabled: true},
			store: store.New(tdb.db),
		}

		appErr := srv.createPortalInstanceTx(ctx, inst, primaryDomain, "alice", now)
		require.Equal(t, "app.conflict", appErr.Code)
	})

	t.Run("condition failures map to conflict", func(t *testing.T) {
		db := &ttmocks.MockExtendedDB{}
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()
		srv := &Server{store: store.New(db)}

		appErr := srv.createPortalInstanceTx(ctx, inst, primaryDomain, "alice", now)
		require.Equal(t, "app.conflict", appErr.Code)
	})

	t.Run("generic transact failures map to internal", func(t *testing.T) {
		db := &ttmocks.MockExtendedDB{}
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New("boom")).Once()
		srv := &Server{store: store.New(db)}

		appErr := srv.createPortalInstanceTx(ctx, inst, primaryDomain, "alice", now)
		require.Equal(t, "app.internal", appErr.Code)
	})
}

func TestHandlePortalUpdateInstanceConfig_ErrorBranches(t *testing.T) {
	t.Parallel()

	makeCtx := func(body []byte) *apptheory.Context {
		return &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"slug": "demo"},
			Request:      apptheory.Request{Body: body},
			RequestID:    "rid",
		}
	}

	t.Run("requires at least one field", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")

		_, err := srv.handlePortalUpdateInstanceConfig(makeCtx([]byte(`{}`)))
		requirePortalAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("condition failures become not found", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")
		tdb.qInstance.On("Update", mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

		disable := false
		body, _ := json.Marshal(updateInstanceConfigRequest{LinkSafetyEnabled: &disable})
		_, err := srv.handlePortalUpdateInstanceConfig(makeCtx(body))
		requirePortalAppErrorCode(t, err, "app.not_found")
	})

	t.Run("update failures become internal", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")
		tdb.qInstance.On("Update", mock.Anything).Return(errors.New("boom")).Once()

		disable := false
		body, _ := json.Marshal(updateInstanceConfigRequest{LinkSafetyEnabled: &disable})
		_, err := srv.handlePortalUpdateInstanceConfig(makeCtx(body))
		requirePortalAppErrorCode(t, err, "app.internal")
	})

	t.Run("reload failures become internal", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")
		tdb.qInstance.On("Update", mock.Anything).Return(nil).Once()
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(errors.New("boom")).Once()

		disable := false
		body, _ := json.Marshal(updateInstanceConfigRequest{LinkSafetyEnabled: &disable})
		_, err := srv.handlePortalUpdateInstanceConfig(makeCtx(body))
		requirePortalAppErrorCode(t, err, "app.internal")
	})
}

func TestHandlePortalGetInstanceProvisioning_ErrorBranches(t *testing.T) {
	t.Parallel()

	t.Run("missing job id returns not found", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")

		ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
		_, err := srv.handlePortalGetInstanceProvisioning(ctx)
		requirePortalAppErrorCode(t, err, "app.not_found")
	})

	t.Run("missing provision job returns not found", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, ProvisionJobID: "job1"}
		}).Once()
		tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()

		ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
		_, err := srv.handlePortalGetInstanceProvisioning(ctx)
		requirePortalAppErrorCode(t, err, "app.not_found")
	})

	t.Run("provision job load errors become internal", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, ProvisionJobID: "job1"}
		}).Once()
		tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(errors.New("boom")).Once()

		ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
		_, err := srv.handlePortalGetInstanceProvisioning(ctx)
		requirePortalAppErrorCode(t, err, "app.internal")
	})
}

func TestHandlePortalGetInstanceBudgetMonth_ErrorBranches(t *testing.T) {
	t.Parallel()

	makeCtx := func(month string) *apptheory.Context {
		return &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": month}}
	}

	t.Run("month is required", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")

		_, err := srv.handlePortalGetInstanceBudgetMonth(makeCtx(""))
		requirePortalAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("month must parse", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")

		_, err := srv.handlePortalGetInstanceBudgetMonth(makeCtx("2026/01"))
		requirePortalAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("budget lookup errors become internal", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")
		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(errors.New("boom")).Once()

		_, err := srv.handlePortalGetInstanceBudgetMonth(makeCtx("2026-01"))
		requirePortalAppErrorCode(t, err, "app.internal")
	})
}

func TestHandlePortalSetInstanceBudgetMonth_ErrorBranches(t *testing.T) {
	t.Parallel()

	makeCtx := func(body []byte) *apptheory.Context {
		return &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"slug": "demo", "month": "2026-01"},
			Request:      apptheory.Request{Body: body},
			RequestID:    "rid",
		}
	}

	t.Run("included credits must be non negative", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")

		body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: -1})
		_, err := srv.handlePortalSetInstanceBudgetMonth(makeCtx(body))
		requirePortalAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("existing budget lookup errors become internal", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")
		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(errors.New("boom")).Once()

		body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 10})
		_, err := srv.handlePortalSetInstanceBudgetMonth(makeCtx(body))
		requirePortalAppErrorCode(t, err, "app.internal")
	})

	t.Run("create or update errors become internal", func(t *testing.T) {
		tdb := newPortalHandlerDB()
		srv := &Server{store: store.New(tdb.db)}
		stubPortalOwnedInstance(t, tdb.qInstance, "demo", "alice")
		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qBudget.On("CreateOrUpdate").Return(errors.New("boom")).Once()

		body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 10})
		_, err := srv.handlePortalSetInstanceBudgetMonth(makeCtx(body))
		requirePortalAppErrorCode(t, err, "app.internal")
	})
}
