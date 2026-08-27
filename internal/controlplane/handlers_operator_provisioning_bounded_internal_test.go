package controlplane

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// newOperatorProvisioningScanMock builds a mock DB whose ProvisionJob model
// routes to one MockQuery pre-wired for the no-slug scan path
// (Where("SK", "=", "JOB")) — the bounded walk under test (issue #1061 part D).
func newOperatorProvisioningScanMock() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", "JOB").Return(qJob).Once()
	return db, qJob
}

func provisionJob(id string, status string, t int64) *models.ProvisionJob {
	return &models.ProvisionJob{ID: id, Status: status, UpdatedAt: time.Unix(t, 0).UTC()}
}

func TestHandleListOperatorProvisionJobs_BoundedSinglePage(t *testing.T) {
	t.Parallel()

	db, qJob := newOperatorProvisioningScanMock()
	// Literal pin: the walk applies Limit(100) on the single page.
	qJob.On("Limit", 100).Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{
			provisionJob("a", models.ProvisionJobStatusQueued, 10),
			provisionJob("b", models.ProvisionJobStatusError, 20),
			provisionJob("c", models.ProvisionJobStatusQueued, 30),
		}
	}).Once()

	s := &Server{store: store.New(db)}
	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{"status": {"queued"}, "limit": {"2"}}

	resp, err := s.handleListOperatorProvisionJobs(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out listOperatorProvisionJobsResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, 2, out.Count)
	require.Len(t, out.Jobs, 2)
	require.Equal(t, "c", out.Jobs[0].ID) // newest first after the in-memory sort
	require.Equal(t, "a", out.Jobs[1].ID)
	qJob.AssertExpectations(t)
	qJob.AssertNotCalled(t, "Scan", mock.Anything)
	qJob.AssertNotCalled(t, "Cursor", mock.Anything)
}

func TestHandleListOperatorProvisionJobs_MultiPageCursorChain(t *testing.T) {
	t.Parallel()

	db, qJob := newOperatorProvisioningScanMock()
	qJob.On("Limit", 100).Return(qJob).Times(2)
	// Literal cursor pin: page one resumes at "prov-ct-1".
	qJob.On("Cursor", "prov-ct-1").Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "prov-ct-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{
			provisionJob("x", models.ProvisionJobStatusQueued, 30),
			provisionJob("y", models.ProvisionJobStatusQueued, 20),
		}
	}).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{provisionJob("z", models.ProvisionJobStatusQueued, 40)}
	}).Once()

	s := &Server{store: store.New(db)}
	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{"limit": {"2"}}

	resp, err := s.handleListOperatorProvisionJobs(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out listOperatorProvisionJobsResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, 2, out.Count)
	require.Len(t, out.Jobs, 2)
	require.Equal(t, "z", out.Jobs[0].ID)
	require.Equal(t, "x", out.Jobs[1].ID)
	qJob.AssertExpectations(t)
}

func TestHandleListOperatorProvisionJobs_ExactPageSizeMultiple(t *testing.T) {
	t.Parallel()

	db, qJob := newOperatorProvisioningScanMock()
	qJob.On("Limit", 100).Return(qJob).Times(2)
	qJob.On("Cursor", "prov-ct-2").Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "prov-ct-2"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		page := make([]*models.ProvisionJob, 0, 100)
		for i := 0; i < 100; i++ {
			page = append(page, provisionJob(fmt.Sprintf("job-%03d", i), models.ProvisionJobStatusQueued, int64(i)))
		}
		*dest = page
	}).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		page := make([]*models.ProvisionJob, 0, 100)
		for i := 100; i < 200; i++ {
			page = append(page, provisionJob(fmt.Sprintf("job-%03d", i), models.ProvisionJobStatusQueued, int64(i)))
		}
		*dest = page
	}).Once()

	s := &Server{store: store.New(db)}
	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{"limit": {"200"}}

	resp, err := s.handleListOperatorProvisionJobs(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out listOperatorProvisionJobsResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, 200, out.Count)
	require.Len(t, out.Jobs, 200)
	require.Equal(t, "job-199", out.Jobs[0].ID) // newest first
	qJob.AssertExpectations(t)
}

func TestHandleListOperatorProvisionJobs_CapExhaustionFailsClosed(t *testing.T) {
	t.Parallel()

	// The walk's cap is 20 pages (page >= 20): exactly twenty pages are read
	// then the handler fails closed with app.internal — never a silently
	// truncated job list. Exact call counts kill the cap-removed and
	// off-by-one mutations.
	db, qJob := newOperatorProvisioningScanMock()
	qJob.On("Limit", 100).Return(qJob).Times(20)
	qJob.On("Cursor", mock.Anything).Return(qJob).Times(19)
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(20)

	s := &Server{store: store.New(db)}
	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{"limit": {"50"}}

	resp, err := s.handleListOperatorProvisionJobs(ctx)
	require.Nil(t, resp)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok, "expected AppTheoryError, got %T: %v", err, err)
	require.Equal(t, "app.internal", appErr.Code)
	qJob.AssertExpectations(t)
	qJob.AssertNotCalled(t, "Scan", mock.Anything)
}

func TestHandleListOperatorProvisionJobs_EmptyTable(t *testing.T) {
	t.Parallel()

	db, qJob := newOperatorProvisioningScanMock()
	qJob.On("Limit", 100).Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = nil
	}).Once()

	s := &Server{store: store.New(db)}
	ctx := operatorCtx()
	ctx.Request.Query = map[string][]string{"limit": {"50"}}

	resp, err := s.handleListOperatorProvisionJobs(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out listOperatorProvisionJobsResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, 0, out.Count)
	require.Empty(t, out.Jobs)
	qJob.AssertExpectations(t)
}

// TestParseLimit_ClampBoundaries pins the endpoint limit clamp used by the
// operator provision-jobs and audit-log handlers (absent/zero/negative/over-max
// all resolve to the documented bounds).
func TestParseLimit_ClampBoundaries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 50},     // absent -> default
		{"0", 1},     // below min -> min
		{"-5", 1},    // negative -> min
		{"999", 200}, // over max -> max
		{"7", 7},     // in range passes through
		{"abc", 50},  // unparsable -> default
	} {
		if got := parseLimit(tc.raw, 50, 1, 200); got != tc.want {
			t.Fatalf("parseLimit(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
