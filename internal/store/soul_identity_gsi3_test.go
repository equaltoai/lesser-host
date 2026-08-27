// parallel (distinct marker model types); keep them side by side.
//
//nolint:dupl // The C1 gsi3 and C2 gsi4 backfill-gate tests are intentionally
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestSoulIdentityGSI3PK(t *testing.T) {
	t.Parallel()

	require.Equal(t, "IDENTITY#active", SoulIdentityGSI3PK(" Active "))
	require.Equal(t, "IDENTITY#pending", SoulIdentityGSI3PK("pending"))
	require.Equal(t, "IDENTITY#", SoulIdentityGSI3PK(""))
}

func TestListSoulAgentIdentitiesByStatus_SinglePage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(q).Maybe()
	q.On("Index", "gsi3").Return(q).Once()
	q.On("Where", "gsi3PK", "=", "IDENTITY#active").Return(q).Once()
	q.On("Limit", 100).Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentIdentity")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentIdentity{{AgentID: "0xaa", Status: models.SoulAgentStatusActive}}
	}).Once()

	st := New(db)
	items, err := st.ListSoulAgentIdentitiesByStatus(ctx, models.SoulAgentStatusActive, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "0xaa", items[0].AgentID)
	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestListSoulAgentIdentitiesByStatus_PaginatesAllPages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(q).Maybe()
	q.On("Index", "gsi3").Return(q).Maybe()
	q.On("Where", "gsi3PK", "=", "IDENTITY#active").Return(q).Maybe()
	q.On("Limit", 200).Return(q).Maybe()

	page := 0
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentIdentity")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "cur-1"}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentIdentity{{AgentID: "0xaa", Status: models.SoulAgentStatusActive}}
		page++
	}).Once()
	q.On("Cursor", "cur-1").Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentIdentity")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentIdentity)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentIdentity, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentIdentity{{AgentID: "0xbb", Status: models.SoulAgentStatusActive}}
		page++
	}).Once()

	st := New(db)
	items, err := st.ListSoulAgentIdentitiesByStatus(ctx, models.SoulAgentStatusActive, 200)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "0xaa", items[0].AgentID)
	require.Equal(t, "0xbb", items[1].AgentID)
	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestListSoulAgentIdentitiesByStatus_QueryError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(q).Maybe()
	q.On("Index", "gsi3").Return(q).Once()
	q.On("Where", "gsi3PK", "=", "IDENTITY#active").Return(q).Once()
	q.On("Limit", 100).Return(q).Once()
	q.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentIdentity")).Return((*core.PaginatedResult)(nil), errors.New("gsi boom")).Once()

	st := New(db)
	_, err := st.ListSoulAgentIdentitiesByStatus(ctx, models.SoulAgentStatusActive, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gsi boom")
}

func TestListSoulAgentIdentitiesByStatus_InvalidInputs(t *testing.T) {
	t.Parallel()

	st := New(ttmocks.NewMockExtendedDB())
	_, err := st.ListSoulAgentIdentitiesByStatus(context.Background(), "", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "status is required")

	var nilStore *Store
	_, err = nilStore.ListSoulAgentIdentitiesByStatus(context.Background(), models.SoulAgentStatusActive, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store not initialized")
}

func TestRequireSoulAgentIdentityGSI3BackfillComplete(t *testing.T) {
	t.Parallel()

	t.Run("marker present passes", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(q).Maybe()
		q.On("Where", "PK", "=", models.SoulAgentIdentityGSI3BackfillMarkerPK).Return(q).Once()
		q.On("Where", "SK", "=", models.SoulAgentIdentityGSI3BackfillMarkerSK).Return(q).Once()
		q.On("First", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(nil).Once()

		st := New(db)
		require.NoError(t, st.RequireSoulAgentIdentityGSI3BackfillComplete(ctx))
		db.AssertExpectations(t)
		q.AssertExpectations(t)
	})

	t.Run("marker missing fails closed with tool guidance", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(q).Maybe()
		q.On("Where", "PK", "=", models.SoulAgentIdentityGSI3BackfillMarkerPK).Return(q).Once()
		q.On("Where", "SK", "=", models.SoulAgentIdentityGSI3BackfillMarkerSK).Return(q).Once()
		q.On("First", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(theoryErrors.ErrItemNotFound).Once()

		st := New(db)
		err := st.RequireSoulAgentIdentityGSI3BackfillComplete(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "backfill not complete")
		require.Contains(t, err.Error(), "soul-agent-identity-gsi3-backfill")
	})

	t.Run("read error propagates", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(q).Maybe()
		q.On("Where", "PK", "=", models.SoulAgentIdentityGSI3BackfillMarkerPK).Return(q).Once()
		q.On("Where", "SK", "=", models.SoulAgentIdentityGSI3BackfillMarkerSK).Return(q).Once()
		q.On("First", mock.AnythingOfType("*models.SoulAgentIdentityGSI3BackfillMarker")).Return(errors.New("dynamo down")).Once()

		st := New(db)
		err := st.RequireSoulAgentIdentityGSI3BackfillComplete(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read")
	})

	t.Run("nil store fails", func(t *testing.T) {
		t.Parallel()

		var nilStore *Store
		err := nilStore.RequireSoulAgentIdentityGSI3BackfillComplete(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "store not initialized")
	})
}
