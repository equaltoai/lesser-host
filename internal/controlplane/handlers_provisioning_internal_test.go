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

type provisioningTestDB struct {
	db    *ttmocks.MockExtendedDB
	qInst *ttmocks.MockQuery
	qJob  *ttmocks.MockQuery
}

func newProvisioningTestDB() provisioningTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qJob} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	return provisioningTestDB{db: db, qInst: qInst, qJob: qJob}
}

func TestParseStartInstanceProvisionRequest(t *testing.T) {
	t.Parallel()

	if _, err := parseStartInstanceProvisionRequest(nil); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	// Empty body is allowed.
	got, err := parseStartInstanceProvisionRequest(&apptheory.Context{})
	if err != nil || got != (startInstanceProvisionRequest{}) {
		t.Fatalf("unexpected: got=%#v err=%v", got, err)
	}
}

func TestStartAndGetInstanceProvisioning(t *testing.T) {
	t.Parallel()

	tdb := newProvisioningTestDB()
	s := &Server{
		cfg: config.Config{
			ManagedParentDomain:         "lesser.host",
			ManagedDefaultRegion:        "us-east-1",
			ManagedLesserDefaultVersion: "v0.0.0",
		},
		store: store.New(tdb.db),
		// queues intentionally nil (offline tests).
	}

	// Instance exists.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		_ = dest.UpdateKeys()
	}).Once()

	body, _ := json.Marshal(startInstanceProvisionRequest{Region: "us-west-2", LesserVersion: "v1"})
	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo"}
	ctx.Request.Body = body

	resp, err := s.handleStartInstanceProvisioning(ctx)
	if err != nil {
		t.Fatalf("handleStartInstanceProvisioning err: %v", err)
	}
	if resp.Status != 202 {
		t.Fatalf("expected 202, got %d", resp.Status)
	}

	// Existing queued job path: instance has job id + status, job fetch succeeds.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{
			Slug:            "demo",
			Status:          models.InstanceStatusActive,
			ProvisionJobID:   "job1",
			ProvisionStatus:  models.ProvisionJobStatusQueued,
			HostedBaseDomain: "demo.lesser.host",
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "job1", InstanceSlug: "demo", Status: models.ProvisionJobStatusQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()

	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"slug": "demo"}
	resp, err = s.handleStartInstanceProvisioning(ctx2)
	if err != nil || resp.Status != 200 {
		t.Fatalf("expected existing job 200, got resp=%#v err=%v", resp, err)
	}

	// Get provisioning: instance points to job id.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive, ProvisionJobID: "job2"}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "job2", InstanceSlug: "demo", Status: models.ProvisionJobStatusRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()

	ctx3 := adminCtx()
	ctx3.Params = map[string]string{"slug": "demo"}
	resp, err = s.handleGetInstanceProvisioning(ctx3)
	if err != nil || resp.Status != 200 {
		t.Fatalf("expected 200, got resp=%#v err=%v", resp, err)
	}

	// No provisioning job.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive, ProvisionJobID: ""}
		_ = dest.UpdateKeys()
	}).Once()
	ctxNoJob := adminCtx()
	ctxNoJob.Params = map[string]string{"slug": "demo"}
	if _, err := s.handleGetInstanceProvisioning(ctxNoJob); err == nil {
		t.Fatalf("expected error for missing job id")
	}

	// Job missing.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive, ProvisionJobID: "job404"}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()
	ctxMissing := adminCtx()
	ctxMissing.Params = map[string]string{"slug": "demo"}
	if _, err := s.handleGetInstanceProvisioning(ctxMissing); err == nil {
		t.Fatalf("expected not found for missing job")
	}
}
