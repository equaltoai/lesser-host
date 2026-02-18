package store

import (
	"context"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestStore_InstanceKeysQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var st *Store
	_, err := st.GetInstanceKey(ctx, "k1")
	require.Error(t, err)

	db := ttmocks.NewMockExtendedDBStrict()
	qKey := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey)

	qKey.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qKey).Maybe()
	qKey.On("Index", mock.Anything).Return(qKey).Maybe()
	qKey.On("Limit", mock.Anything).Return(qKey).Maybe()
	qKey.On("ConsistentRead").Return(qKey).Maybe()
	qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: "k1"}
	}).Maybe()
	qKey.On("All", mock.AnythingOfType("*[]*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.InstanceKey](t, args, 0)
		*dest = []*models.InstanceKey{
			{ID: "old", CreatedAt: time.Unix(1, 0).UTC()},
			{ID: "new", CreatedAt: time.Unix(2, 0).UTC()},
		}
	}).Maybe()

	st = New(db)

	_, err = st.GetInstanceKey(ctx, " ")
	require.Error(t, err)

	key, err := st.GetInstanceKey(ctx, "k1")
	require.NoError(t, err)
	require.NotNil(t, key)
	require.Equal(t, "k1", key.ID)

	keys, err := st.ListInstanceKeysByInstance(ctx, " Slug ", 2)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, "new", keys[0].ID)
	require.Equal(t, "old", keys[1].ID)
}
