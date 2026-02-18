package models

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpdateJob_BeforeCreate_SetsDefaultsAndKeys(t *testing.T) {
	t.Parallel()

	j := &UpdateJob{
		ID:           " job1 ",
		InstanceSlug: " SLUG ",
	}
	require.NoError(t, j.BeforeCreate())

	require.Equal(t, "job1", j.ID)
	require.Equal(t, "slug", j.InstanceSlug)
	require.Equal(t, UpdateJobStatusQueued, j.Status)
	require.Equal(t, SKJob, j.SK)
	require.Equal(t, "UPDATE_JOB#job1", j.PK)
	require.False(t, j.CreatedAt.IsZero())
	require.False(t, j.UpdatedAt.IsZero())
	require.False(t, j.ExpiresAt.IsZero())
	require.Equal(t, j.ExpiresAt.Unix(), j.TTL)
	require.Equal(t, int64(10), j.MaxAttempts)

	require.Equal(t, "UPDATE_INSTANCE#slug", j.GSI1PK)
	require.True(t, strings.HasSuffix(j.GSI1SK, "#job1"))
}

func TestUpdateJob_UpdateKeys_EmptyInstanceSlugClearsGSI(t *testing.T) {
	t.Parallel()

	j := &UpdateJob{
		ID:           "job1",
		InstanceSlug: "   ",
	}
	require.NoError(t, j.UpdateKeys())
	require.Equal(t, "", j.GSI1PK)
	require.Equal(t, "", j.GSI1SK)
	require.Equal(t, int64(10), j.MaxAttempts)
	require.Equal(t, SKJob, j.SK)
}

func TestUpdateJob_BeforeUpdate_RefreshesTTLAndGSI(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(24 * time.Hour)

	j := &UpdateJob{
		ID:           "job1",
		InstanceSlug: "slug",
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
		MaxAttempts:  1,
	}
	require.NoError(t, j.UpdateKeys())
	before := j.UpdatedAt

	require.NoError(t, j.BeforeUpdate())
	require.False(t, j.UpdatedAt.IsZero())
	require.False(t, j.UpdatedAt.Before(before))
	require.Equal(t, expiresAt.Unix(), j.TTL)

	require.Equal(t, "UPDATE_INSTANCE#slug", j.GSI1PK)
	require.True(t, strings.Contains(j.GSI1SK, createdAt.Format(time.RFC3339Nano)))
}

func TestUpdateJob_TableNameAndKeyGetters(t *testing.T) {
	t.Parallel()

	require.Equal(t, MainTableName(), (UpdateJob{}).TableName())

	j := &UpdateJob{PK: "pk", SK: "sk"}
	require.Equal(t, "pk", j.GetPK())
	require.Equal(t, "sk", j.GetSK())
}

func TestUpdateJob_updateGSI1_NilReceiverNoPanic(t *testing.T) {
	t.Parallel()

	var j *UpdateJob
	j.updateGSI1()
}
