package costtelemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// ---------------------------------------------------------------------------
// test constants
// ---------------------------------------------------------------------------

const (
	testCacheSlug     = "demo"
	testCacheDate1    = "2026-05-25"
	testCacheDate2    = "2026-05-26"
	testCacheCurrency = "USD"
)

// ---------------------------------------------------------------------------
// fake CostTelemetryStore for tests
// ---------------------------------------------------------------------------

// fakeCostTelemetryStore records PutCostTelemetry calls in memory.
// It simulates TableTheory's CreateOrUpdate lifecycle by calling
// BeforeCreate on each record, which populates PK, SK, TTL, and
// timestamps as the production store would.
//
// Error injection: failOnCall controls which call number (1-indexed)
// should return an error. Zero means never fail.
type fakeCostTelemetryStore struct {
	records    []*models.CostTelemetry
	failOnCall int // call number that should fail; 0 = never fail
	callCount  int
	err        error // error to return when failing; uses sentinel if nil
	lastRecord *models.CostTelemetry
}

func (s *fakeCostTelemetryStore) PutCostTelemetry(ctx context.Context, record *models.CostTelemetry) error {
	s.callCount++

	// Check for injected failure before recording — this simulates
	// a store that rejects the write (e.g. DynamoDB throttle).
	if s.failOnCall > 0 && s.callCount >= s.failOnCall {
		if s.err != nil {
			return s.err
		}
		return errors.New("fake store error")
	}

	// Simulate TableTheory's CreateOrUpdate lifecycle: run BeforeCreate
	// so PK, SK, TTL, and timestamps are populated as they would be
	// in production. This lets tests assert on key shape, TTL, etc.
	if record != nil {
		if err := record.BeforeCreate(); err != nil {
			return err
		}
		// Shallow copy is safe: all fields are value types or immutable strings.
		cp := *record
		s.records = append(s.records, &cp)
	}
	s.lastRecord = record
	return nil
}

// recordCount returns the number of records written to the store.
func (s *fakeCostTelemetryStore) recordCount() int { return len(s.records) }

// recordByDate returns the first record with the given date, or nil.
func (s *fakeCostTelemetryStore) recordByDate(date string) *models.CostTelemetry {
	for _, r := range s.records {
		if r != nil && r.Date == date {
			return r
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestReport builds a ReconciledCostReport for tests.
func newTestReport(slug string, entries ...ReconciledCostEntry) *ReconciledCostReport {
	if len(entries) == 0 {
		return &ReconciledCostReport{
			Slug:      slug,
			Currency:  testCacheCurrency,
			Entries:   nil,
			TotalCost: 0,
		}
	}
	var total float64
	for _, e := range entries {
		total += e.Cost
	}
	return &ReconciledCostReport{
		Slug:      slug,
		Currency:  testCacheCurrency,
		Entries:   entries,
		TotalCost: total,
	}
}

// entry builds a ReconciledCostEntry.
func entry(date, service string, cost float64) ReconciledCostEntry {
	return ReconciledCostEntry{
		Date:     date,
		Service:  service,
		Cost:     cost,
		Currency: testCacheCurrency,
	}
}

// entryWithMetrics builds a ReconciledCostEntry with CloudWatch metrics.
func entryWithMetrics(date, service string, cost float64, metrics ...ServiceAttribution) ReconciledCostEntry {
	return ReconciledCostEntry{
		Date:     date,
		Service:  service,
		Cost:     cost,
		Currency: testCacheCurrency,
		Metrics:  metrics,
	}
}

// decodeEntries parses EntriesJSON from a CostTelemetry record.
func decodeEntries(t *testing.T, record *models.CostTelemetry) []ReconciledCostEntry {
	t.Helper()
	if record == nil {
		t.Fatal("record is nil")
	}
	var entries []ReconciledCostEntry
	if err := json.Unmarshal([]byte(record.EntriesJSON), &entries); err != nil {
		t.Fatalf("decode entries: %v", err)
	}
	return entries
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestCacheWriteSingleDaySingleService(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.25),
	)

	written, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if written != 1 {
		t.Fatalf("expected 1 record written, got %d", written)
	}
	if store.recordCount() != 1 {
		t.Fatalf("expected 1 store record, got %d", store.recordCount())
	}

	rec := store.records[0]
	if rec.InstanceSlug != testCacheSlug {
		t.Errorf("InstanceSlug = %q, want %q", rec.InstanceSlug, testCacheSlug)
	}
	if rec.Date != testCacheDate1 {
		t.Errorf("Date = %q, want %q", rec.Date, testCacheDate1)
	}
	if rec.DayCost != 1.25 {
		t.Errorf("DayCost = %f, want 1.25", rec.DayCost)
	}
	if rec.Currency != testCacheCurrency {
		t.Errorf("Currency = %q, want %q", rec.Currency, testCacheCurrency)
	}

	entries := decodeEntries(t, rec)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Service != "Lambda" {
		t.Errorf("Service = %q, want Lambda", entries[0].Service)
	}
}

func TestCacheWriteMultiDaySplit(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
		entry(testCacheDate1, "DynamoDB", 2.00),
		entry(testCacheDate2, "Lambda", 3.00),
		entry(testCacheDate2, "S3", 0.50),
	)

	written, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if written != 2 {
		t.Fatalf("expected 2 records written, got %d", written)
	}
	if store.recordCount() != 2 {
		t.Fatalf("expected 2 store records, got %d", store.recordCount())
	}

	// Day 1: 2 entries, cost = 3.00
	d1 := store.recordByDate(testCacheDate1)
	if d1 == nil {
		t.Fatalf("no record for date %q", testCacheDate1)
	}
	if d1.DayCost != 3.00 {
		t.Errorf("Day1 DayCost = %f, want 3.00", d1.DayCost)
	}
	d1Entries := decodeEntries(t, d1)
	if len(d1Entries) != 2 {
		t.Fatalf("Day1: expected 2 entries, got %d", len(d1Entries))
	}

	// Day 2: 2 entries, cost = 3.50
	d2 := store.recordByDate(testCacheDate2)
	if d2 == nil {
		t.Fatalf("no record for date %q", testCacheDate2)
	}
	if d2.DayCost != 3.50 {
		t.Errorf("Day2 DayCost = %f, want 3.50", d2.DayCost)
	}
	d2Entries := decodeEntries(t, d2)
	if len(d2Entries) != 2 {
		t.Fatalf("Day2: expected 2 entries, got %d", len(d2Entries))
	}
}

func TestCacheWritePreservesMetrics(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	metrics := []ServiceAttribution{
		{Service: "Lambda", MetricName: "Invocations", Stat: "Sum", Unit: "Count", Value: 100},
	}
	report := newTestReport(testCacheSlug,
		entryWithMetrics(testCacheDate1, "Lambda", 1.25, metrics...),
	)

	_, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	rec := store.recordByDate(testCacheDate1)
	entries := decodeEntries(t, rec)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].Metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(entries[0].Metrics))
	}
	if entries[0].Metrics[0].MetricName != "Invocations" {
		t.Errorf("MetricName = %q, want Invocations", entries[0].Metrics[0].MetricName)
	}
}

func TestCacheWriteIdempotent(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
		entry(testCacheDate2, "S3", 0.50),
	)

	// First write.
	written1, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if written1 != 2 {
		t.Fatalf("first Write: expected 2 records, got %d", written1)
	}

	// Second write with same data. The fake store accumulates records,
	// so the count should double. In production, CreateOrUpdate upserts
	// the same PK+SK, so the record count stays at 2. Here we verify
	// that the cache writer produces the same number of records and
	// the keys (slug+date) are identical — meaning the production
	// store would upsert rather than insert duplicates.
	written2, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if written2 != 2 {
		t.Fatalf("second Write: expected 2 records, got %d", written2)
	}
	if store.recordCount() != 4 {
		t.Fatalf("fake store accumulated 4 records (expected for test double); "+
			"production CreateOrUpdate would keep 2 (got %d)", store.recordCount())
	}

	// Verify that the second write's records have the same PK+SK as the first.
	for i := 0; i < 2; i++ {
		first := store.records[i]
		second := store.records[i+2]
		if first.PK != second.PK {
			t.Errorf("record %d: PK mismatch across writes: %q vs %q", i, first.PK, second.PK)
		}
		if first.SK != second.SK {
			t.Errorf("record %d: SK mismatch across writes: %q vs %q", i, first.SK, second.SK)
		}
		if first.InstanceSlug != second.InstanceSlug {
			t.Errorf("record %d: InstanceSlug mismatch: %q vs %q", i, first.InstanceSlug, second.InstanceSlug)
		}
		if first.Date != second.Date {
			t.Errorf("record %d: Date mismatch: %q vs %q", i, first.Date, second.Date)
		}
	}
}

func TestCacheWriteIdempotentSameSlugSameDatesNoDuplicates(t *testing.T) {
	t.Parallel()

	// This test verifies that when writing the same slug+date twice,
	// only one PK+SK pair is produced per date. The fake store lets
	// us inspect what keys the cache writer would send to the real
	// store, confirming that CreateOrUpdate would upsert rather than
	// insert duplicates.
	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
	)

	// Write twice.
	if _, err := cache.Write(ctx, report); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := cache.Write(ctx, report); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	// Both writes should produce records with the same PK+SK for the same date.
	// Verify there are exactly 2 records (one per write) and they have the same key.
	if store.recordCount() != 2 {
		t.Fatalf("expected 2 records total, got %d", store.recordCount())
	}

	r1 := store.records[0]
	r2 := store.records[1]
	if r1.PK != r2.PK || r1.SK != r2.SK {
		t.Errorf("PK/SK differ across writes: (%q, %q) vs (%q, %q)",
			r1.PK, r1.SK, r2.PK, r2.SK)
	}
}

func TestCacheWriteTenantScoping(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	slugA := "tenant-alpha"
	slugB := "tenant-beta"

	reportA := newTestReport(slugA, entry(testCacheDate1, "Lambda", 1.00))
	reportB := newTestReport(slugB, entry(testCacheDate1, "Lambda", 2.00))

	if _, err := cache.Write(ctx, reportA); err != nil {
		t.Fatalf("Write A: %v", err)
	}
	if _, err := cache.Write(ctx, reportB); err != nil {
		t.Fatalf("Write B: %v", err)
	}

	if store.recordCount() != 2 {
		t.Fatalf("expected 2 records, got %d", store.recordCount())
	}

	for _, rec := range store.records {
		if !strings.Contains(rec.PK, rec.InstanceSlug) {
			t.Errorf("PK %q does not contain InstanceSlug %q", rec.PK, rec.InstanceSlug)
		}

		// Verify tenant A and tenant B records are in different partitions.
		otherSlug := slugA
		if rec.InstanceSlug == slugA {
			otherSlug = slugB
		}
		if strings.Contains(rec.PK, otherSlug) {
			t.Errorf("PK %q contains other tenant slug %q", rec.PK, otherSlug)
		}
	}
}

func TestCacheWriteTTLSet(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
	)

	before := time.Now().UTC()
	_, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	after := time.Now().UTC()

	rec := store.recordByDate(testCacheDate1)
	if rec == nil {
		t.Fatalf("no record written")
	}

	// TTL must be non-zero.
	if rec.TTL == 0 {
		t.Fatalf("TTL is zero; must be set to a retention-policy value")
	}

	// ExpiresAt must be set.
	if rec.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt is zero; must be set")
	}

	// ExpiresAt should be ~90 days from now.
	expectedMin := before.Add(models.CostTelemetryRetentionDays * 24 * time.Hour)
	expectedMax := after.Add(models.CostTelemetryRetentionDays * 24 * time.Hour)
	if rec.ExpiresAt.Before(expectedMin) || rec.ExpiresAt.After(expectedMax) {
		t.Errorf("ExpiresAt = %s, want between %s and %s",
			rec.ExpiresAt.Format(time.RFC3339),
			expectedMin.Format(time.RFC3339),
			expectedMax.Format(time.RFC3339))
	}

	// TTL should equal ExpiresAt.Unix().
	if rec.TTL != rec.ExpiresAt.Unix() {
		t.Errorf("TTL = %d, want ExpiresAt.Unix() = %d", rec.TTL, rec.ExpiresAt.Unix())
	}

	// Verify TTL matches the retention constant: ExpiresAt should be
	// CreatedAt + retention days. TTL is a Unix timestamp, not a duration.
	expectedExpiresAt := rec.CreatedAt.Add(models.CostTelemetryRetentionDays * 24 * time.Hour)
	diff := rec.ExpiresAt.Sub(expectedExpiresAt)
	if diff < 0 {
		diff = -diff
	}
	if diff > 2*time.Second {
		t.Errorf("ExpiresAt = %s, want ~%s (CreatedAt + %d days); diff = %s",
			rec.ExpiresAt.Format(time.RFC3339),
			expectedExpiresAt.Format(time.RFC3339),
			models.CostTelemetryRetentionDays,
			diff)
	}
}

func TestCacheWriteEmptyEntries(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug) // no entries

	written, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if written != 0 {
		t.Errorf("expected 0 records written for empty report, got %d", written)
	}
	if store.recordCount() != 0 {
		t.Errorf("expected 0 store records, got %d", store.recordCount())
	}
}

func TestCacheWriteNilReport(t *testing.T) {
	t.Parallel()

	cache := NewCostTelemetryCache(&fakeCostTelemetryStore{})
	ctx := context.Background()

	_, err := cache.Write(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil report")
	}
}

func TestCacheWriteEmptySlug(t *testing.T) {
	t.Parallel()

	cache := NewCostTelemetryCache(&fakeCostTelemetryStore{})
	ctx := context.Background()

	report := newTestReport("", entry(testCacheDate1, "Lambda", 1.00))

	_, err := cache.Write(ctx, report)
	if err == nil {
		t.Fatalf("expected error for empty slug")
	}
}

func TestCacheWriteStoreError(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{
		failOnCall: 1, // fail on first write
		err:        fmt.Errorf("simulated store error"),
	}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
	)

	written, err := cache.Write(ctx, report)
	if err == nil {
		t.Fatalf("expected error from failing store, got nil")
	}
	if written != 0 {
		t.Errorf("expected 0 records written on store error, got %d", written)
	}
}

func TestCacheWritePartialStoreError(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{
		failOnCall: 2, // succeed once, then fail on second
		err:        fmt.Errorf("simulated store error after first write"),
	}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
		entry(testCacheDate2, "DynamoDB", 2.00),
	)

	written, err := cache.Write(ctx, report)
	if err == nil {
		t.Fatalf("expected error after partial write, got nil")
	}
	if written != 1 {
		t.Errorf("expected 1 record written before error, got %d", written)
	}
}

func TestCacheWritePKShape(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.00),
	)

	_, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	rec := store.recordByDate(testCacheDate1)
	if rec == nil {
		t.Fatalf("no record written")
	}

	// PK must be COST_TELEMETRY#<slug>.
	wantPK := fmt.Sprintf("COST_TELEMETRY#%s", testCacheSlug)
	if rec.PK != wantPK {
		t.Errorf("PK = %q, want %q", rec.PK, wantPK)
	}

	// SK must be the date.
	if rec.SK != testCacheDate1 {
		t.Errorf("SK = %q, want %q", rec.SK, testCacheDate1)
	}
}

func TestCacheWriteSkipsEmptyDateEntry(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry("", "Lambda", 1.00),               // empty date — should be skipped
		entry(testCacheDate1, "DynamoDB", 2.00), // valid date
	)

	written, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if written != 1 {
		t.Fatalf("expected 1 record (empty-date entry skipped), got %d", written)
	}

	rec := store.recordByDate(testCacheDate1)
	if rec == nil {
		t.Fatalf("no record for date %q", testCacheDate1)
	}
	entries := decodeEntries(t, rec)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Service != "DynamoDB" {
		t.Errorf("Service = %q, want DynamoDB", entries[0].Service)
	}
}

func TestCacheWriteDayCostAggregation(t *testing.T) {
	t.Parallel()

	store := &fakeCostTelemetryStore{}
	cache := NewCostTelemetryCache(store)
	ctx := context.Background()

	report := newTestReport(testCacheSlug,
		entry(testCacheDate1, "Lambda", 1.11),
		entry(testCacheDate1, "DynamoDB", 2.22),
		entry(testCacheDate1, "S3", 3.33),
	)

	_, err := cache.Write(ctx, report)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	rec := store.recordByDate(testCacheDate1)
	wantCost := 1.11 + 2.22 + 3.33
	if rec.DayCost != wantCost {
		t.Errorf("DayCost = %f, want %f", rec.DayCost, wantCost)
	}
}

func TestNewCostTelemetryCache_ReturnsInterface(t *testing.T) {
	t.Parallel()

	// Verify the constructor returns a non-nil interface value.
	cache := NewCostTelemetryCache(&fakeCostTelemetryStore{})
	if cache == nil {
		t.Fatalf("NewCostTelemetryCache returned nil")
	}
}

func TestCostTelemetryRetentionDaysIsPositive(t *testing.T) {
	t.Parallel()

	if models.CostTelemetryRetentionDays <= 0 {
		t.Errorf("CostTelemetryRetentionDays = %d, must be positive", models.CostTelemetryRetentionDays)
	}
	if models.CostTelemetryRetentionDays < 30 {
		t.Errorf("CostTelemetryRetentionDays = %d, must be >= 30 to support M3.11 past-30-day queries",
			models.CostTelemetryRetentionDays)
	}
}
