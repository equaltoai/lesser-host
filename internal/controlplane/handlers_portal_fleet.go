package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fleetSparkDays is the number of daily buckets returned for sparkline fields.
const fleetSparkDays = 7

// fleetEnrichSparklines populates SparkActivity and SparkCost on the given
// instance response by querying host-local data stores (CostTelemetry for
// cost, UsageLedgerEntry for activity). Both queries are scoped to the owning
// customer's instance slug — no cross-tenant aggregation is possible.
//
// Failures are silent: if the store is unavailable or data is missing the
// fields remain at their zero values. This is the honest contract — zero
// means "no data available", not "zero cost/activity".
func (s *Server) fleetEnrichSparklines(ctx context.Context, resp *instanceResponse) {
	if s == nil || s.store == nil || s.store.DB == nil || resp == nil {
		return
	}

	slug := strings.TrimSpace(resp.Slug)
	if slug == "" {
		return
	}

	now := time.Now().UTC()
	fromDate := now.AddDate(0, 0, -fleetSparkDays).Format("2006-01-02")
	toDate := now.Format("2006-01-02")

	// Cost sparkline from CostTelemetry (host-local DynamoDB).
	costRecords, err := s.store.ListCostTelemetryByInstance(ctx, slug, fromDate, toDate, fleetSparkDays)
	if err == nil && len(costRecords) > 0 {
		costs := make([]float64, fleetSparkDays)
		// Records are ordered by date descending (most recent first).
		// Build a date-indexed map then fill the 7-day window oldest→newest.
		costByDate := make(map[string]float64, len(costRecords))
		for _, r := range costRecords {
			if r == nil {
				continue
			}
			date := strings.TrimSpace(r.Date)
			if date != "" {
				costByDate[date] = r.DayCost
			}
		}
		for i := 0; i < fleetSparkDays; i++ {
			date := now.AddDate(0, 0, -(fleetSparkDays - 1 - i)).Format("2006-01-02")
			if c, ok := costByDate[date]; ok {
				costs[i] = c
			}
		}
		resp.SparkCost = costs
	}

	// Activity sparkline from UsageLedgerEntry aggregation.
	// Count entries per day for the last 7 days. Because UsageLedgerEntry PK
	// embeds the month, a 7-day window may span up to 2 months (at most 2
	// DynamoDB queries per instance).
	activity, err := fleetAggregateDailyActivity(ctx, s, slug, now)
	if err == nil {
		resp.SparkActivity = activity
	}
}

// fleetAggregateDailyActivity counts UsageLedgerEntry records per day for the
// last fleetSparkDays days. The window may span at most 2 months.
func fleetAggregateDailyActivity(ctx context.Context, s *Server, slug string, now time.Time) ([]int64, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not available")
	}

	counts := make([]int64, fleetSparkDays)
	months := make(map[string]bool)

	for i := 0; i < fleetSparkDays; i++ {
		date := now.AddDate(0, 0, -(fleetSparkDays - 1 - i))
		months[date.Format("2006-01")] = true
	}

	for month := range months {
		pk := fmt.Sprintf("USAGE#%s#%s", slug, month)
		var items []*models.UsageLedgerEntry
		err := s.store.DB.WithContext(ctx).
			Model(&models.UsageLedgerEntry{}).
			Where("PK", "=", pk).
			Limit(2000).
			All(&items)
		if err != nil {
			return nil, err
		}

		for _, e := range items {
			if e == nil {
				continue
			}
			entryDate := e.CreatedAt.UTC().Format("2006-01-02")
			for i := 0; i < fleetSparkDays; i++ {
				bucketDate := now.AddDate(0, 0, -(fleetSparkDays - 1 - i)).Format("2006-01-02")
				if entryDate == bucketDate {
					counts[i]++
					break
				}
			}
		}
	}

	return counts, nil
}
