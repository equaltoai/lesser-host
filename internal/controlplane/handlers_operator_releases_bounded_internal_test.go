package controlplane

import (
	"fmt"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// newListActiveInstancesMock builds a mock DB whose Instance model routes to a
// single MockQuery, with the standard Where/Model plumbing (issue #1061 part D:
// listActiveInstances is the only qInstance read with a Limit).
func newListActiveInstancesMock() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Once()
	return db, qInst
}

func activeInstance(slug string) *models.Instance {
	return &models.Instance{Slug: slug, Status: models.InstanceStatusActive}
}

func TestListActiveInstances_BoundedSinglePage(t *testing.T) {
	t.Parallel()

	db, qInst := newListActiveInstancesMock()
	// Literal pin: the walk applies Limit(100) on the single page (the site's
	// documented cap is 500 = 5 pages of 100; the constant under test is never
	// referenced).
	qInst.On("Limit", 100).Return(qInst).Once()
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			activeInstance("alpha"),
			activeInstance("beta"),
			{Slug: "off", Status: models.InstanceStatusDisabled},
			{Slug: "", Status: models.InstanceStatusActive}, // empty slug filtered out
		}
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listActiveInstances(new(apptheory.Context))
	require.Nil(t, appErr)
	require.Len(t, items, 2)
	require.Equal(t, "alpha", items[0].Slug)
	require.Equal(t, "beta", items[1].Slug)
	qInst.AssertExpectations(t)
	qInst.AssertNotCalled(t, "Scan", mock.Anything)
	qInst.AssertNotCalled(t, "Cursor", mock.Anything)
}

func TestListActiveInstances_MultiPageCursorChain(t *testing.T) {
	t.Parallel()

	db, qInst := newListActiveInstancesMock()
	qInst.On("Limit", 100).Return(qInst).Times(2)
	// Literal cursor pin: page one resumes at "releases-ct-1".
	qInst.On("Cursor", "releases-ct-1").Return(qInst).Once()
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "releases-ct-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{activeInstance("alpha"), activeInstance("beta")}
	}).Once()
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{activeInstance("gamma")}
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listActiveInstances(new(apptheory.Context))
	require.Nil(t, appErr)
	require.Len(t, items, 3)
	require.Equal(t, []string{"alpha", "beta", "gamma"}, []string{items[0].Slug, items[1].Slug, items[2].Slug})
	qInst.AssertExpectations(t)
	qInst.AssertNotCalled(t, "Scan", mock.Anything)
}

func TestListActiveInstances_ExactPageSizeMultiple(t *testing.T) {
	t.Parallel()

	db, qInst := newListActiveInstancesMock()
	qInst.On("Limit", 100).Return(qInst).Times(2)
	qInst.On("Cursor", "releases-ct-2").Return(qInst).Once()
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "releases-ct-2"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		page := make([]*models.Instance, 0, 100)
		for i := 0; i < 100; i++ {
			page = append(page, activeInstance(fmt.Sprintf("inst-%03d", i)))
		}
		*dest = page
	}).Once()
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		page := make([]*models.Instance, 0, 100)
		for i := 100; i < 200; i++ {
			page = append(page, activeInstance(fmt.Sprintf("inst-%03d", i)))
		}
		*dest = page
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listActiveInstances(new(apptheory.Context))
	require.Nil(t, appErr)
	require.Len(t, items, 200)
	qInst.AssertExpectations(t)
}

func TestListActiveInstances_EmptyTable(t *testing.T) {
	t.Parallel()

	db, qInst := newListActiveInstancesMock()
	qInst.On("Limit", 100).Return(qInst).Once()
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = nil
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listActiveInstances(new(apptheory.Context))
	require.Nil(t, appErr)
	require.Empty(t, items)
	qInst.AssertExpectations(t)
}

func TestListActiveInstances_CapExhaustionFailsClosed(t *testing.T) {
	t.Parallel()

	// The walk's cap is 5 pages (page >= 5): exactly five pages are read, then
	// listActiveInstances fails closed with app.internal — never a silently
	// truncated fleet. Pinning the exact call counts kills both the
	// cap-removed mutation (a sixth AllPaginated call is unexpected) and the
	// off-by-one mutation (a fourth-page stop leaves the fifth stub
	// unconsumed, which AssertExpectations reports).
	db, qInst := newListActiveInstancesMock()
	qInst.On("Limit", 100).Return(qInst).Times(5)
	qInst.On("Cursor", mock.Anything).Return(qInst).Times(4)
	qInst.On("AllPaginated", mock.AnythingOfType("*[]*models.Instance")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(5)

	s := &Server{store: store.New(db)}
	items, appErr := s.listActiveInstances(new(apptheory.Context))
	require.Nil(t, items)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)
	qInst.AssertExpectations(t)
	qInst.AssertNotCalled(t, "Scan", mock.Anything)
}
