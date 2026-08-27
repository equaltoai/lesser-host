// parallel (distinct marker model types); keep them side by side.
//
//nolint:dupl // The C1 gsi3 and C2 gsi4 backfill-gate tests are intentionally
package store

import (
	"context"
	"errors"
	"testing"

	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestSoulMintConversationGSI4PK(t *testing.T) {
	t.Parallel()

	require.Equal(t, "SOUL#AGENT#0xabc", SoulMintConversationGSI4PK(" 0xABC "))
	require.Equal(t, "SOUL#AGENT#0xabc", SoulMintConversationGSI4PK("0xabc"))
	require.Equal(t, "SOUL#AGENT#", SoulMintConversationGSI4PK(""))
}

// TestListSoulAgentMintConversationsByAgent asserts the exact query shape the
// operator list depends on: a single bounded page against the gsi4 index,
// partition-keyed by the agent, ordered by gsi4SK DESC (createdAt DESC), with
// no scan and no unbounded page.
func TestListSoulAgentMintConversationsByAgent_BoundedGSIQuery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(q).Maybe()
	q.On("Index", "gsi4").Return(q).Once()
	q.On("Where", "gsi4PK", "=", "SOUL#AGENT#0xabc").Return(q).Once()
	q.On("OrderBy", "gsi4SK", soulMintConversationGSI4DescOrder).Return(q).Once()
	q.On("Limit", 20).Return(q).Once()
	q.On("All", mock.AnythingOfType("*[]*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*[]*models.SoulAgentMintConversation)
		if !ok {
			t.Fatalf("expected *[]*models.SoulAgentMintConversation, got %T", args.Get(0))
		}
		*dest = []*models.SoulAgentMintConversation{
			{AgentID: "0xabc", ConversationID: "new", Status: models.SoulMintConversationStatusInProgress},
			{AgentID: "0xabc", ConversationID: "old", Status: models.SoulMintConversationStatusFailed},
		}
	}).Once()

	st := New(db)
	items, err := st.ListSoulAgentMintConversationsByAgent(ctx, "0xabc", 20)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "new", items[0].ConversationID)
	require.Equal(t, "old", items[1].ConversationID)
	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestListSoulAgentMintConversationsByAgent_DefensiveLimitAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("default limit when non-positive", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(q).Maybe()
		q.On("Index", "gsi4").Return(q).Once()
		q.On("Where", "gsi4PK", "=", "SOUL#AGENT#0xabc").Return(q).Once()
		q.On("OrderBy", "gsi4SK", soulMintConversationGSI4DescOrder).Return(q).Once()
		q.On("Limit", soulMintConversationGSI4QueryPageSize).Return(q).Once()
		q.On("All", mock.AnythingOfType("*[]*models.SoulAgentMintConversation")).Return(nil).Once()

		st := New(db)
		items, err := st.ListSoulAgentMintConversationsByAgent(ctx, "0xabc", 0)
		require.NoError(t, err)
		require.Empty(t, items)
	})

	t.Run("query error propagates", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(q).Maybe()
		q.On("Index", "gsi4").Return(q).Once()
		q.On("Where", "gsi4PK", "=", "SOUL#AGENT#0xabc").Return(q).Once()
		q.On("OrderBy", "gsi4SK", soulMintConversationGSI4DescOrder).Return(q).Once()
		q.On("Limit", 20).Return(q).Once()
		q.On("All", mock.AnythingOfType("*[]*models.SoulAgentMintConversation")).Return(errors.New("gsi4 boom")).Once()

		st := New(db)
		_, err := st.ListSoulAgentMintConversationsByAgent(ctx, "0xabc", 20)
		require.Error(t, err)
		require.Contains(t, err.Error(), "gsi4 boom")
	})

	t.Run("not found is an empty page", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(q).Maybe()
		q.On("Index", "gsi4").Return(q).Once()
		q.On("Where", "gsi4PK", "=", "SOUL#AGENT#0xabc").Return(q).Once()
		q.On("OrderBy", "gsi4SK", soulMintConversationGSI4DescOrder).Return(q).Once()
		q.On("Limit", 20).Return(q).Once()
		q.On("All", mock.AnythingOfType("*[]*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

		st := New(db)
		items, err := st.ListSoulAgentMintConversationsByAgent(ctx, "0xabc", 20)
		require.NoError(t, err)
		require.Empty(t, items)
	})

	t.Run("invalid inputs fail", func(t *testing.T) {
		t.Parallel()

		st := New(ttmocks.NewMockExtendedDB())
		_, err := st.ListSoulAgentMintConversationsByAgent(context.Background(), "", 10)
		require.Error(t, err)
		require.Contains(t, err.Error(), "agent id is required")

		var nilStore *Store
		_, err = nilStore.ListSoulAgentMintConversationsByAgent(context.Background(), "0xabc", 10)
		require.Error(t, err)
		require.Contains(t, err.Error(), "store not initialized")
	})
}

func TestRequireSoulAgentMintConversationGSI4BackfillComplete(t *testing.T) {
	t.Parallel()

	t.Run("marker present passes", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(q).Maybe()
		q.On("Where", "PK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerPK).Return(q).Once()
		q.On("Where", "SK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerSK).Return(q).Once()
		q.On("First", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(nil).Once()

		st := New(db)
		require.NoError(t, st.RequireSoulAgentMintConversationGSI4BackfillComplete(ctx))
		db.AssertExpectations(t)
		q.AssertExpectations(t)
	})

	t.Run("marker missing fails closed with tool guidance", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDB()
		q := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(q).Maybe()
		q.On("Where", "PK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerPK).Return(q).Once()
		q.On("Where", "SK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerSK).Return(q).Once()
		q.On("First", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(theoryErrors.ErrItemNotFound).Once()

		st := New(db)
		err := st.RequireSoulAgentMintConversationGSI4BackfillComplete(ctx)
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
		db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(q).Maybe()
		q.On("Where", "PK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerPK).Return(q).Once()
		q.On("Where", "SK", "=", models.SoulAgentMintConversationGSI4BackfillMarkerSK).Return(q).Once()
		q.On("First", mock.AnythingOfType("*models.SoulAgentMintConversationGSI4BackfillMarker")).Return(errors.New("dynamo down")).Once()

		st := New(db)
		err := st.RequireSoulAgentMintConversationGSI4BackfillComplete(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read")
	})

	t.Run("nil store fails", func(t *testing.T) {
		t.Parallel()

		var nilStore *Store
		err := nilStore.RequireSoulAgentMintConversationGSI4BackfillComplete(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "store not initialized")
	})
}
