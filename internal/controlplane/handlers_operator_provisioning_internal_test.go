package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type operatorProvisioningTestDB struct {
	db     *ttmocks.MockExtendedDB
	qJob   *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

func newOperatorProvisioningTestDB() operatorProvisioningTestDB {
	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qJob, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return operatorProvisioningTestDB{db: db, qJob: qJob, qAudit: qAudit}
}

func operatorCtx() *apptheory.Context {
	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	return ctx
}

func TestOperatorProvisioningHelpers_AdditionalCases(t *testing.T) {
	t.Parallel()

	if got := queryFirst(nil, "k"); got != "" {
		t.Fatalf("expected empty for nil ctx")
	}
	if got := queryFirst(&apptheory.Context{}, "k"); got != "" {
		t.Fatalf("expected empty for missing query")
	}

	qctx := &apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"k": {"v1", "v2"}}}}
	if got := queryFirst(qctx, "k"); got != "v1" {
		t.Fatalf("unexpected queryFirst: %q", got)
	}

	if got := parseLimit("", 50, 1, 200); got != 50 {
		t.Fatalf("unexpected default: %d", got)
	}
	if got := parseLimit("nope", 50, 1, 200); got != 50 {
		t.Fatalf("unexpected invalid: %d", got)
	}
}

func TestHandleListOperatorProvisionJobs_FiltersAndLimits(t *testing.T) {
	t.Parallel()

	tdb := newOperatorProvisioningTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{
		"status": {"queued"},
		"limit":  {"1"},
	}

	tdb.qJob.On("All", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.ProvisionJob)
		*dest = []*models.ProvisionJob{
			{ID: "a", Status: models.ProvisionJobStatusQueued, UpdatedAt: time.Unix(10, 0).UTC()},
			{ID: "b", Status: models.ProvisionJobStatusError, UpdatedAt: time.Unix(20, 0).UTC()},
			{ID: "c", Status: models.ProvisionJobStatusQueued, UpdatedAt: time.Unix(30, 0).UTC()},
		}
	}).Once()

	resp, err := s.handleListOperatorProvisionJobs(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var out listOperatorProvisionJobsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Jobs) != 1 || out.Jobs[0].ID != "c" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestHandleGetOperatorProvisionJob_NotFoundAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newOperatorProvisioningTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := operatorCtx()
	ctx.Params = map[string]string{"id": "missing"}
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleGetOperatorProvisionJob(ctx); err == nil {
		t.Fatalf("expected not found")
	}

	ctx = operatorCtx()
	ctx.Params = map[string]string{"id": "j1"}
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "j1", InstanceSlug: "inst", Status: models.ProvisionJobStatusQueued}
		_ = dest.UpdateKeys()
	}).Once()
	resp, err := s.handleGetOperatorProvisionJob(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestHandleRetryOperatorProvisionJob_QueuedAndErrorBranches(t *testing.T) {
	t.Parallel()

	tdb := newOperatorProvisioningTestDB()
	s := &Server{
		store:  store.New(tdb.db),
		queues: &queueClient{}, // url intentionally empty so enqueue fails fast (ignored)
		cfg:    config.Config{ProvisionQueueURL: "url"},
	}

	// Already OK.
	ctx := operatorCtx()
	ctx.Params = map[string]string{"id": "ok"}
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "ok", InstanceSlug: "inst", Status: models.ProvisionJobStatusOK}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.handleRetryOperatorProvisionJob(ctx); err == nil {
		t.Fatalf("expected conflict for ok job")
	}

	// Queued/non-error status: just records audit and requeues.
	ctx = operatorCtx()
	ctx.Params = map[string]string{"id": "q1"}
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "q1", InstanceSlug: "inst", Status: models.ProvisionJobStatusQueued}
		_ = dest.UpdateKeys()
	}).Twice() // initial + updated
	resp, err := s.handleRetryOperatorProvisionJob(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("queued retry resp=%#v err=%v", resp, err)
	}

	// Error status: uses TransactWrite to reset + also requeues.
	ctx = operatorCtx()
	ctx.Params = map[string]string{"id": "e1"}
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "e1", InstanceSlug: "inst", Status: models.ProvisionJobStatusError, CreatedAt: time.Unix(1, 0).UTC()}
		_ = dest.UpdateKeys()
	}).Twice()
	resp, err = s.handleRetryOperatorProvisionJob(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("error retry resp=%#v err=%v", resp, err)
	}
}

func TestHandleAppendOperatorProvisionJobNote_ValidationsAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newOperatorProvisioningTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := operatorCtx()
	ctx.Params = map[string]string{"id": "j"}
	ctx.Request.Body = []byte(`{"note":" "}`)
	if _, err := s.handleAppendOperatorProvisionJobNote(ctx); err == nil {
		t.Fatalf("expected note required")
	}

	ctx = operatorCtx()
	ctx.Params = map[string]string{"id": "missing"}
	ctx.Request.Body = []byte(`{"note":"x"}`)
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleAppendOperatorProvisionJobNote(ctx); err == nil {
		t.Fatalf("expected not found")
	}

	ctx = operatorCtx()
	ctx.Params = map[string]string{"id": "j1"}
	ctx.Request.Body = []byte(`{"note":"hello"}`)
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "j1", InstanceSlug: "inst", Status: models.ProvisionJobStatusQueued}
		_ = dest.UpdateKeys()
	}).Twice()
	resp, err := s.handleAppendOperatorProvisionJobNote(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}
