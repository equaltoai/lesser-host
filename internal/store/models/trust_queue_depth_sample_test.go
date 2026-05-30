package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTrustQueueDepthSampleKeysAndTTL(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 5, 29, 20, 30, 0, 123, time.UTC)
	sample := &TrustQueueDepthSample{
		InstanceSlug: " Demo ",
		Timestamp:    ts,
		Depth:        7,
		Source:       "lesser-host:soul_comm_mailbox_queue",
	}

	require.NoError(t, sample.BeforeCreate())
	require.Equal(t, "demo", sample.InstanceSlug)
	require.Equal(t, "TRUST#QUEUE_DEPTH#INSTANCE#demo", sample.PK)
	require.Equal(t, "SAMPLE#2026-05-29T20:00:00Z", sample.SK)
	require.Equal(t, ts.Add(TrustQueueDepthSampleRetentionDays*24*time.Hour).Unix(), sample.TTL)
	require.Equal(t, sample.PK, sample.GetPK())
	require.Equal(t, sample.SK, sample.GetSK())
}

func TestTrustQueueDepthSampleKeysCoalesceWithinBucket(t *testing.T) {
	t.Parallel()

	first := &TrustQueueDepthSample{
		InstanceSlug: "demo",
		Timestamp:    time.Date(2026, 5, 29, 20, 5, 0, 0, time.UTC),
	}
	second := &TrustQueueDepthSample{
		InstanceSlug: "demo",
		Timestamp:    time.Date(2026, 5, 29, 20, 55, 0, 0, time.UTC),
	}

	require.NoError(t, first.BeforeCreate())
	require.NoError(t, second.BeforeCreate())
	require.Equal(t, first.PK, second.PK)
	require.Equal(t, first.SK, second.SK)
}

func TestTrustQueueDepthSampleRedactionShape(t *testing.T) {
	t.Parallel()

	sample := &TrustQueueDepthSample{InstanceSlug: "demo", Timestamp: time.Now().UTC(), Depth: -5}
	require.NoError(t, sample.BeforeCreate())
	require.Equal(t, 0, sample.Depth)
	require.NotContains(t, sample.PK, "ACCOUNT")
	require.NotContains(t, sample.SK, "SECRET")
}
