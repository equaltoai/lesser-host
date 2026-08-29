package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type testListItem struct {
	ID        string
	CreatedAt time.Time
}

func TestClampListLimit(t *testing.T) {
	t.Parallel()

	require.Equal(t, 50, clampListLimit(0))
	require.Equal(t, 50, clampListLimit(-1))
	require.Equal(t, 200, clampListLimit(201))
	require.Equal(t, 2, clampListLimit(2))
}

func TestSortByCreatedAtDesc_SortsAndHandlesNil(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	// nil at i (items[1]) should not sort before non-nil.
	itemsA := []*testListItem{{ID: "a", CreatedAt: now}, nil}
	sortByCreatedAtDesc(itemsA, func(it *testListItem) time.Time { return it.CreatedAt })
	require.NotNil(t, itemsA[0])
	require.Nil(t, itemsA[1])

	// nil at j (items[0]) should sort after non-nil.
	itemsB := []*testListItem{nil, {ID: "b", CreatedAt: now}}
	sortByCreatedAtDesc(itemsB, func(it *testListItem) time.Time { return it.CreatedAt })
	require.NotNil(t, itemsB[0])
	require.Nil(t, itemsB[1])

	// Non-nil comparison sorts by CreatedAt desc.
	itemsC := []*testListItem{
		{ID: "older", CreatedAt: now.Add(-time.Minute)},
		{ID: "newer", CreatedAt: now},
	}
	sortByCreatedAtDesc(itemsC, func(it *testListItem) time.Time { return it.CreatedAt })
	require.Equal(t, "newer", itemsC[0].ID)
	require.Equal(t, "older", itemsC[1].ID)
}

func TestListByInstanceGSI1_ErrorsAndSorts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("nil_store_errors", func(t *testing.T) {
		t.Parallel()

		_, err := listByInstanceGSI1[testListItem](nil, ctx, "slug", 10, &struct{}{}, "X#%s", func(it *testListItem) time.Time {
			return it.CreatedAt
		})
		require.Error(t, err)
	})

	t.Run("empty_slug_errors", func(t *testing.T) {
		t.Parallel()

		st := New(ttmocks.NewMockExtendedDBStrict())
		_, err := listByInstanceGSI1[testListItem](st, ctx, " ", 10, &struct{}{}, "X#%s", func(it *testListItem) time.Time {
			return it.CreatedAt
		})
		require.Error(t, err)
	})

	t.Run("query_error_returns_error", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		q.On("Index", mock.Anything).Return(q)
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q)
		q.On("OrderBy", "gsi1SK", "DESC").Return(q)
		q.On("Limit", mock.Anything).Return(q)
		q.On("All", mock.Anything).Return(errors.New("boom")).Once()

		st := New(db)
		_, err := listByInstanceGSI1[testListItem](st, ctx, "slug", 10, &struct{}{}, "X#%s", func(it *testListItem) time.Time {
			return it.CreatedAt
		})
		require.Error(t, err)
	})

	t.Run("sorts_and_clamps_limit", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDBStrict()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.Anything).Return(q)

		now := time.Unix(100, 0).UTC()

		q.On("Index", "gsi1").Return(q).Once()
		q.On("Where", "gsi1PK", "=", "X#slug").Return(q).Once()
		q.On("OrderBy", "gsi1SK", "DESC").Return(q).Once()
		q.On("Limit", 200).Return(q).Once()
		q.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			destAny := args.Get(0)
			dest, ok := destAny.(*[]*testListItem)
			if !ok {
				t.Fatalf("expected *[]*testListItem, got %T", destAny)
			}
			*dest = []*testListItem{
				{ID: "a", CreatedAt: now.Add(-time.Minute)},
				nil,
				{ID: "b", CreatedAt: now},
			}
		}).Once()

		st := New(db)
		items, err := listByInstanceGSI1[testListItem](st, ctx, " SLUG ", 999, &struct{}{}, "X#%s", func(it *testListItem) time.Time {
			return it.CreatedAt
		})
		require.NoError(t, err)
		require.Len(t, items, 3)
		require.Equal(t, "b", items[0].ID)
		require.Equal(t, "a", items[1].ID)
		require.Nil(t, items[2])
	})
}

// TestAllPartitionItemsBounded_ResumesViaCursor verifies the shared bounded
// partition walk (issue #1061 part B) binds every page, resumes via the opaque
// cursor, and never issues a Scan.
func TestAllPartitionItemsBounded_ResumesViaCursor(t *testing.T) {
	t.Parallel()

	q := new(ttmocks.MockQuery)
	appliedLimits := []int{}
	q.On("Limit", 100).Return(q).Times(2).Run(func(args mock.Arguments) {
		if n, ok := args.Get(0).(int); ok {
			appliedLimits = append(appliedLimits, n)
		}
	})
	q.On("Cursor", "after-1").Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = []*models.HostedGenesisSession{{ConversationID: "c1"}}
	}).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = []*models.HostedGenesisSession{{ConversationID: "c2"}}
	}).Once()

	items, err := allPartitionItemsBounded[models.HostedGenesisSession](q, storePartitionWalkPageSize, storePartitionWalkMaxPages)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, []int{100, 100}, appliedLimits)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestAllPartitionItemsBounded_ExceedsPageCapFailsClosed verifies the bounded
// walk refuses to silently truncate a partition past the page cap.
func TestAllPartitionItemsBounded_ExceedsPageCapFailsClosed(t *testing.T) {
	t.Parallel()

	// The cap check is `page >= maxPages`: with maxPages=2 the walk reads
	// exactly two pages (Limit x2, Cursor x1, AllPaginated x2) and then errors,
	// never a third page. Pinning the fixed call counts makes the off-by-one
	// mutation (`page > maxPages`) fail: it would issue a third read.
	q := new(ttmocks.MockQuery)
	q.On("Limit", mock.Anything).Return(q).Times(2)
	q.On("Cursor", mock.Anything).Return(q).Times(1)
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(2)

	_, err := allPartitionItemsBounded[models.HostedGenesisSession](q, storePartitionWalkPageSize, 2)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exceeded 2 pages"), "expected page-cap error, got %v", err)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestListHostedGenesisSessionsByAgent_BoundedWalk verifies the store method
// (issue #1061 part B, site 11) issues page-capped reads with cursor resume
// and never a Scan.
func TestListHostedGenesisSessionsByAgent_BoundedWalk(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", context.Background()).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisSession")).Return(q).Once()
	q.On("Index", "gsi2").Return(q).Once()
	q.On("Where", "gsi2PK", "=", models.HostedGenesisSessionAgentGSI2PK("inst", "agent1")).Return(q).Once()
	q.On("Limit", 100).Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = []*models.HostedGenesisSession{{ConversationID: "c1", AgentID: "agent1"}}
	}).Once()

	db.On("WithContext", context.Background()).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisSession")).Return(q).Once()
	q.On("Limit", 100).Return(q).Once()
	q.On("Cursor", "after-1").Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisSession")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisSession](t, args, 0)
		*dest = []*models.HostedGenesisSession{{ConversationID: "c2", AgentID: "agent1"}}
	}).Once()

	st := New(db)
	items, err := st.ListHostedGenesisSessionsByAgent(context.Background(), "inst", "agent1")
	require.NoError(t, err)
	require.Len(t, items, 2)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestListHostedGenesisMicroVMExecutions_BoundedWalk verifies the store method
// (issue #1061 part B, site 12) issues page-capped reads with cursor resume
// and never a Scan.
func TestListHostedGenesisMicroVMExecutions_BoundedWalk(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", context.Background()).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.HostedGenesisMicroVMExecution")).Return(q).Once()
	q.On("Where", "PK", "=", models.HostedGenesisMicroVMExecutionPK("inst", "ns")).Return(q).Once()
	q.On("Limit", 100).Return(q).Times(2)
	q.On("Cursor", "after-1").Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisMicroVMExecution")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisMicroVMExecution](t, args, 0)
		*dest = []*models.HostedGenesisMicroVMExecution{{SessionID: "s2"}}
	}).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.HostedGenesisMicroVMExecution")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.HostedGenesisMicroVMExecution](t, args, 0)
		*dest = []*models.HostedGenesisMicroVMExecution{{SessionID: "s1"}}
	}).Once()

	st := New(db)
	items, err := st.ListHostedGenesisMicroVMExecutions(context.Background(), " inst ", " ns ")
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "s1", items[0].SessionID) // sorted by SessionID
	require.Equal(t, "s2", items[1].SessionID)
	q.AssertExpectations(t)
	q.AssertNotCalled(t, "Scan", mock.Anything)
}
