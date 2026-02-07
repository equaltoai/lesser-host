package ai

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/stretchr/testify/require"
)

func TestDefaultAndEffectiveInstanceConfig(t *testing.T) {
	t.Parallel()

	def := DefaultInstanceConfig()
	require.False(t, def.Enabled)
	require.NotEmpty(t, def.ModelSet)
	require.Greater(t, def.BatchMaxItems, int64(0))
	require.Greater(t, def.BatchMaxTotalBytes, int64(0))
	require.Greater(t, def.PricingMultiplierBps, int64(0))
	require.Greater(t, def.MaxInflightJobs, int64(0))

	require.Equal(t, def, EffectiveInstanceConfig(nil))

	t.Run("OverridesValidFields", func(t *testing.T) {
		t.Parallel()

		enabled := true
		bps := int64(12000)
		inflight := int64(42)
		inst := &models.Instance{
			AIEnabled:              &enabled,
			AIModelSet:             " openai:test ",
			AIBatchingMode:         "WORKER",
			AIBatchMaxItems:        2,
			AIBatchMaxTotalBytes:   123,
			AIPricingMultiplierBps: &bps,
			AIMaxInflightJobs:      &inflight,
		}
		got := EffectiveInstanceConfig(inst)
		require.True(t, got.Enabled)
		require.Equal(t, "openai:test", got.ModelSet)
		require.Equal(t, "worker", got.BatchingMode)
		require.Equal(t, int64(2), got.BatchMaxItems)
		require.Equal(t, int64(123), got.BatchMaxTotalBytes)
		require.Equal(t, int64(12000), got.PricingMultiplierBps)
		require.Equal(t, int64(42), got.MaxInflightJobs)
	})

	t.Run("IgnoresInvalidFields", func(t *testing.T) {
		t.Parallel()

		enabled := true
		bps := int64(0)
		inflight := int64(-1)
		inst := &models.Instance{
			AIEnabled:              &enabled,
			AIBatchingMode:         "not_a_mode",
			AIPricingMultiplierBps: &bps,
			AIMaxInflightJobs:      &inflight,
		}
		got := EffectiveInstanceConfig(inst)
		require.True(t, got.Enabled)
		require.Equal(t, def.BatchingMode, got.BatchingMode)
		require.Equal(t, def.PricingMultiplierBps, got.PricingMultiplierBps)
		require.Equal(t, def.MaxInflightJobs, got.MaxInflightJobs)
	})
}
