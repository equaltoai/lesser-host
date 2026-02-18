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

func TestStore_UpdateJobsQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var st *Store
	_, err := st.GetUpdateJob(ctx, "j1")
	require.Error(t, err)
	require.Error(t, st.PutUpdateJob(ctx, &models.UpdateJob{ID: "j1"}))

	db := ttmocks.NewMockExtendedDBStrict()
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.Anything).Return(qJob)

	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("Index", mock.Anything).Return(qJob).Maybe()
	qJob.On("Limit", mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{ID: "j1"}
	}).Maybe()
	qJob.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "old", CreatedAt: time.Unix(1, 0).UTC()},
			{ID: "new", CreatedAt: time.Unix(2, 0).UTC()},
		}
	}).Maybe()
	qJob.On("CreateOrUpdate").Return(nil).Maybe()

	st = New(db)

	_, err = st.GetUpdateJob(ctx, " ")
	require.Error(t, err)

	job, err := st.GetUpdateJob(ctx, "j1")
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "j1", job.ID)

	require.Error(t, st.PutUpdateJob(ctx, nil))
	require.NoError(t, st.PutUpdateJob(ctx, &models.UpdateJob{ID: "j1"}))

	jobs, err := st.ListUpdateJobsByInstance(ctx, " Slug ", 2)
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Equal(t, "new", jobs[0].ID)
	require.Equal(t, "old", jobs[1].ID)
}
