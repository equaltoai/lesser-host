package models

import (
	"fmt"
	"strings"
	"time"
)

// TrustQueueDepthSampleRetentionDays bounds host-side trust dashboard queue
// telemetry. The Trust dashboard only reads the last 24 hours, but samples are
// retained briefly for operational debugging and verifier evidence.
const TrustQueueDepthSampleRetentionDays = 31

// TrustQueueDepthSampleBucketDuration coalesces portal-triggered queue-depth
// snapshots so repeated reads within a bucket share one deterministic item key.
const TrustQueueDepthSampleBucketDuration = time.Hour

// TrustQueueDepthSample stores an instance-scoped queue-depth snapshot for the
// Trust dashboard.
//
// Keys:
//
//	PK: TRUST#QUEUE_DEPTH#INSTANCE#{instanceSlug}
//	SK: SAMPLE#{hour_bucket}
//
// The item intentionally stores only the tenant slug, sample timestamp, count,
// and source label. It does not store account IDs, raw keys, mailbox delivery
// IDs, agent IDs, message IDs, or mailbox content.
type TrustQueueDepthSample struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK  string `theorydb:"pk,attr:PK" json:"-"`
	SK  string `theorydb:"sk,attr:SK" json:"-"`
	TTL int64  `theorydb:"ttl,attr:ttl" json:"-"`

	InstanceSlug string    `theorydb:"attr:instanceSlug" json:"instance_slug"`
	Timestamp    time.Time `theorydb:"attr:timestamp" json:"timestamp"`
	Depth        int       `theorydb:"attr:depth" json:"depth"`
	Source       string    `theorydb:"attr:source" json:"source"`
}

// TableName returns the database table name for TrustQueueDepthSample.
func (TrustQueueDepthSample) TableName() string { return MainTableName() }

// BeforeCreate sets defaults and keys before creating TrustQueueDepthSample.
func (s *TrustQueueDepthSample) BeforeCreate() error {
	if s.Timestamp.IsZero() {
		s.Timestamp = time.Now().UTC()
	}
	if s.Depth < 0 {
		s.Depth = 0
	}
	if err := s.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("instanceSlug", s.InstanceSlug); err != nil {
		return err
	}
	return nil
}

// UpdateKeys normalizes TrustQueueDepthSample and derives storage keys.
func (s *TrustQueueDepthSample) UpdateKeys() error {
	s.InstanceSlug = strings.ToLower(strings.TrimSpace(s.InstanceSlug))
	s.Source = strings.TrimSpace(s.Source)
	s.Timestamp = s.Timestamp.UTC()
	if s.Depth < 0 {
		s.Depth = 0
	}
	bucket := s.Timestamp.Truncate(TrustQueueDepthSampleBucketDuration)
	ts := bucket.Format(time.RFC3339)
	s.PK = TrustQueueDepthSamplePK(s.InstanceSlug)
	s.SK = fmt.Sprintf("SAMPLE#%s", ts)
	s.TTL = s.Timestamp.Add(TrustQueueDepthSampleRetentionDays * 24 * time.Hour).Unix()
	return nil
}

// GetPK returns the partition key for TrustQueueDepthSample.
func (s *TrustQueueDepthSample) GetPK() string { return s.PK }

// GetSK returns the sort key for TrustQueueDepthSample.
func (s *TrustQueueDepthSample) GetSK() string { return s.SK }

// TrustQueueDepthSamplePK returns the instance-scoped queue-depth partition key.
func TrustQueueDepthSamplePK(instanceSlug string) string {
	return fmt.Sprintf("TRUST#QUEUE_DEPTH#INSTANCE#%s", strings.ToLower(strings.TrimSpace(instanceSlug)))
}
