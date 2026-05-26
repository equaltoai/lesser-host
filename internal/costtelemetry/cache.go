package costtelemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// CostTelemetryStore is the minimal store seam for persisting cost
// telemetry cache records. Tests implement this interface with a
// fake; production satisfies it with the real store.Store.
//
// Only the write path is exposed here; M3.11 adds query methods
// through the same store interface.
type CostTelemetryStore interface {
	PutCostTelemetry(ctx context.Context, record *models.CostTelemetry) error
}

// CostTelemetryCache persists reconciled cost data as per-instance
// per-day CostTelemetry records.
//
// The cache is the M3.10 interface seam: tests inject a fake store;
// production injects the real store.Store.
type CostTelemetryCache interface {
	// Write persists a ReconciledCostReport as per-day CostTelemetry
	// records. The report's entries are grouped by date, and each
	// group is written as a single upsertable record keyed by
	// (COST_TELEMETRY#<slug>, <date>).
	//
	// Re-running Write for the same slug and date range is idempotent:
	// each (slug, date) record is upserted, so subsequent runs replace
	// rather than duplicate.
	//
	// Returns the number of records written.
	Write(ctx context.Context, report *ReconciledCostReport) (int, error)
}

// NewCostTelemetryCache constructs a CostTelemetryCache backed by the
// given store.
func NewCostTelemetryCache(store CostTelemetryStore) CostTelemetryCache {
	return &costTelemetryCacheImpl{store: store}
}

type costTelemetryCacheImpl struct {
	store CostTelemetryStore
}

// Write implements CostTelemetryCache.
func (c *costTelemetryCacheImpl) Write(ctx context.Context, report *ReconciledCostReport) (int, error) {
	if report == nil {
		return 0, fmt.Errorf("costtelemetry: report is nil")
	}
	if report.Slug == "" {
		return 0, fmt.Errorf("costtelemetry: report slug is empty")
	}

	// Group entries by date. Each date becomes one CostTelemetry record.
	byDate := groupEntriesByDate(report.Entries)

	// Build and persist one record per date.
	written := 0
	for _, date := range sortedDates(byDate) {
		entries := byDate[date]

		// Compute per-day total from the entries.
		var dayCost float64
		for _, e := range entries {
			dayCost += e.Cost
		}

		entriesJSON, err := json.Marshal(entries)
		if err != nil {
			return written, fmt.Errorf("costtelemetry: marshaling entries for slug %s date %s: %w",
				report.Slug, date, err)
		}

		record := &models.CostTelemetry{
			InstanceSlug: report.Slug,
			Date:         date,
			EntriesJSON:  string(entriesJSON),
			DayCost:      dayCost,
			Currency:     report.Currency,
		}

		if err := c.store.PutCostTelemetry(ctx, record); err != nil {
			return written, fmt.Errorf("costtelemetry: writing cache record for slug %s date %s: %w",
				report.Slug, date, err)
		}
		written++
	}

	return written, nil
}

// groupEntriesByDate splits a flat list of reconciled entries into a map
// keyed by date string (YYYY-MM-DD). Each date bucket preserves the
// original entry order.
func groupEntriesByDate(entries []ReconciledCostEntry) map[string][]ReconciledCostEntry {
	byDate := make(map[string][]ReconciledCostEntry)
	for _, e := range entries {
		if e.Date == "" {
			continue
		}
		byDate[e.Date] = append(byDate[e.Date], e)
	}
	return byDate
}

// sortedDates returns the keys of a date→entries map in ascending order.
func sortedDates(byDate map[string][]ReconciledCostEntry) []string {
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	return dates
}
