package controlplane

import (
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

// newOperatorAuditScanMock builds a mock DB whose AuditLogEntry model routes to
// one MockQuery pre-wired for the no-target scan path
// (Where("SK", "BEGINS_WITH", "EVENT#")) — the bounded walk under test
// (issue #1061 part D).
func newOperatorAuditScanMock() (*ttmocks.MockExtendedDB, *ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	qAudit := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	qAudit.On("Where", "SK", "BEGINS_WITH", "EVENT#").Return(qAudit).Once()
	return db, qAudit
}

func auditEntry(actor string, action string, createdAt time.Time) *models.AuditLogEntry {
	return &models.AuditLogEntry{Actor: actor, Action: action, CreatedAt: createdAt}
}

func TestListOperatorAuditLogEntries_BoundedSinglePage(t *testing.T) {
	t.Parallel()

	db, qAudit := newOperatorAuditScanMock()
	// Literal pin: the walk applies Limit(100) on the single page.
	qAudit.On("Limit", 100).Return(qAudit).Once()
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		*dest = []*models.AuditLogEntry{
			auditEntry("alice", "x", time.Unix(10, 0).UTC()),
			auditEntry("bob", "y", time.Unix(20, 0).UTC()),
			auditEntry("carol", "x", time.Unix(30, 0).UTC()),
		}
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listOperatorAuditLogEntries(new(apptheory.Context), operatorAuditLogFilters{Limit: 1})
	require.Nil(t, appErr)
	require.Len(t, items, 3) // read side returns the full page; filtering happens downstream
	qAudit.AssertExpectations(t)
	qAudit.AssertNotCalled(t, "Scan", mock.Anything)
	qAudit.AssertNotCalled(t, "Cursor", mock.Anything)
}

func TestListOperatorAuditLogEntries_MultiPageCursorChain(t *testing.T) {
	t.Parallel()

	db, qAudit := newOperatorAuditScanMock()
	qAudit.On("Limit", 100).Return(qAudit).Times(2)
	// Literal cursor pin: page one resumes at "audit-ct-1".
	qAudit.On("Cursor", "audit-ct-1").Return(qAudit).Once()
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "audit-ct-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		*dest = []*models.AuditLogEntry{
			auditEntry("alice", "a", time.Unix(10, 0).UTC()),
			auditEntry("bob", "b", time.Unix(20, 0).UTC()),
		}
	}).Once()
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		*dest = []*models.AuditLogEntry{auditEntry("carol", "c", time.Unix(30, 0).UTC())}
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listOperatorAuditLogEntries(new(apptheory.Context), operatorAuditLogFilters{})
	require.Nil(t, appErr)
	require.Len(t, items, 3)
	qAudit.AssertExpectations(t)
}

func TestListOperatorAuditLogEntries_ExactPageSizeMultiple(t *testing.T) {
	t.Parallel()

	db, qAudit := newOperatorAuditScanMock()
	qAudit.On("Limit", 100).Return(qAudit).Times(2)
	qAudit.On("Cursor", "audit-ct-2").Return(qAudit).Once()
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "audit-ct-2"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		page := make([]*models.AuditLogEntry, 0, 100)
		for i := 0; i < 100; i++ {
			page = append(page, auditEntry(fmt.Sprintf("actor-%03d", i), "x", time.Unix(int64(i), 0).UTC()))
		}
		*dest = page
	}).Once()
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		page := make([]*models.AuditLogEntry, 0, 100)
		for i := 100; i < 200; i++ {
			page = append(page, auditEntry(fmt.Sprintf("actor-%03d", i), "x", time.Unix(int64(i), 0).UTC()))
		}
		*dest = page
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listOperatorAuditLogEntries(new(apptheory.Context), operatorAuditLogFilters{Limit: 200})
	require.Nil(t, appErr)
	require.Len(t, items, 200)
	qAudit.AssertExpectations(t)
}

func TestListOperatorAuditLogEntries_CapExhaustionFailsClosed(t *testing.T) {
	t.Parallel()

	// The walk's cap is 20 pages (page >= 20): exactly twenty pages are read
	// then the read fails closed with app.internal — never a silently
	// truncated audit log. Exact call counts kill the cap-removed and
	// off-by-one mutations.
	db, qAudit := newOperatorAuditScanMock()
	qAudit.On("Limit", 100).Return(qAudit).Times(20)
	qAudit.On("Cursor", mock.Anything).Return(qAudit).Times(19)
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(20)

	s := &Server{store: store.New(db)}
	items, appErr := s.listOperatorAuditLogEntries(new(apptheory.Context), operatorAuditLogFilters{Limit: 50})
	require.Nil(t, items)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)
	qAudit.AssertExpectations(t)
	qAudit.AssertNotCalled(t, "Scan", mock.Anything)
}

func TestListOperatorAuditLogEntries_EmptyTable(t *testing.T) {
	t.Parallel()

	db, qAudit := newOperatorAuditScanMock()
	qAudit.On("Limit", 100).Return(qAudit).Once()
	qAudit.On("AllPaginated", mock.AnythingOfType("*[]*models.AuditLogEntry")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AuditLogEntry](t, args, 0)
		*dest = nil
	}).Once()

	s := &Server{store: store.New(db)}
	items, appErr := s.listOperatorAuditLogEntries(new(apptheory.Context), operatorAuditLogFilters{})
	require.Nil(t, appErr)
	require.Empty(t, items)
	qAudit.AssertExpectations(t)
}

// TestFilterOperatorAuditLogEntries_SortsFiltersAndClamps pins the
// filter/sort/truncate contract that runs after the bounded read.
func TestFilterOperatorAuditLogEntries_SortsFiltersAndClamps(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	entries := []*models.AuditLogEntry{
		auditEntry("alice", "a", now.Add(-time.Hour)),
		auditEntry("bob", "b", now),
		auditEntry("alice", "b", now.Add(time.Hour)),
	}
	out := filterOperatorAuditLogEntries(entries, operatorAuditLogFilters{Actor: "alice", Limit: 1})
	require.Len(t, out, 1)
	require.Equal(t, "alice", out[0].Actor)
	require.Equal(t, "b", out[0].Action) // newest alice entry wins after DESC sort + clamp
}
