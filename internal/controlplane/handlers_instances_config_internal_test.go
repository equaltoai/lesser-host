package controlplane

import (
	"testing"

	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestBuildInstanceConfigUpdate(t *testing.T) {
	t.Parallel()

	_, _, err := buildInstanceConfigUpdate("slug", updateInstanceConfigRequest{})
	require.Error(t, err)

	rp := testNope
	_, _, err = buildInstanceConfigUpdate("slug", updateInstanceConfigRequest{RenderPolicy: &rp})
	require.Error(t, err)
	var appErr *apptheory.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, appErrCodeBadRequest, appErr.Code)

	op := "allow"
	mt := "virality"
	ms := "openai:test"
	bm := "worker"
	maxItems := int64(2)
	maxBytes := int64(100)
	mult := int64(11000)
	inflight := int64(10)
	enabled := true

	update, fields, err := buildInstanceConfigUpdate("slug", updateInstanceConfigRequest{
		OveragePolicy:          &op,
		ModerationTrigger:      &mt,
		AIModelSet:             &ms,
		AIBatchingMode:         &bm,
		AIBatchMaxItems:        &maxItems,
		AIBatchMaxTotalBytes:   &maxBytes,
		AIPricingMultiplierBps: &mult,
		AIMaxInflightJobs:      &inflight,
		AIEnabled:              &enabled,
	})
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, "slug", update.Slug)
	require.NotEmpty(t, fields)
	require.Equal(t, "allow", update.OveragePolicy)
	require.Equal(t, "virality", update.ModerationTrigger)
	require.Equal(t, "openai:test", update.AIModelSet)
	require.Equal(t, "worker", update.AIBatchingMode)
	require.Equal(t, int64(2), update.AIBatchMaxItems)
	require.Equal(t, int64(100), update.AIBatchMaxTotalBytes)
	require.NotNil(t, update.AIPricingMultiplierBps)
	require.Equal(t, int64(11000), *update.AIPricingMultiplierBps)
	require.NotNil(t, update.AIMaxInflightJobs)
	require.Equal(t, int64(10), *update.AIMaxInflightJobs)
}
