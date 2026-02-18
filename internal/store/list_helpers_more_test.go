package store

import (
	"context"
	"errors"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
