package controlplane

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fleetSparkDays is the number of daily buckets returned for sparkline fields.
const fleetSparkDays = 7

// fleetMetricsWindowDays is the lookback window for the managed Lesser metrics
// fetch used to compute peak_daily_users_30d and the 7-day sparklines.
const fleetMetricsWindowDays = 30

// fleetEnrichMaxConcurrency is the maximum number of concurrent managed-instance
// metrics HTTP calls allowed during list enrichment. Additional instances wait
// for a semaphore slot.
const fleetEnrichMaxConcurrency = 4

// fleetEnrichPerInstanceTimeout is the deadline for resolving the instance key
// and fetching managed metrics for a single instance. A slow/unreachable
// instance must not hold up the rest of the list.
const fleetEnrichPerInstanceTimeout = 5 * time.Second

// fleetEnrichAggregateBudget is the total wall-clock time the enrichment phase
// may consume across all instances. Once exhausted, remaining instances are
// returned with Fleet fields at zero values (honest contract — zero = "data
// not available").
const fleetEnrichAggregateBudget = 20 * time.Second

// fleetEnrichFromManagedMetrics populates Fleet data fields on the given
// instanceResponse by calling the managed Lesser instance metrics endpoint.
// It uses the cache-aware resolveInstanceKeyCached + fetchManagedInstanceMetrics
// path.
//
// Semantics:
//   - peak_daily_users_30d = max daily UniqueUsers over the 30-day window.
//     Unique users is a per-day count; using max avoids double-counting users
//     who are active on multiple days. This is the least-misleading single-value
//     summary available from the daily metrics endpoint.
//   - spark_activity = TotalRequests per day for the last 7 days, oldest→newest.
//     Missing days are zero.
//   - spark_cost = CostDollars per day for the last 7 days, oldest→newest.
//     Missing days are zero.
//
// posts_24h, sig_fails_24h, peers, and severed are not yet counterized on the
// managed Lesser side and remain zero.
//
// Failure posture: if key resolution or the managed metrics HTTP call fails for
// any reason, all Fleet fields stay at their zero values. The list endpoint
// must not 500 because one instance's metrics are unavailable.
func (s *Server) fleetEnrichFromManagedMetrics(ctx context.Context, inst *models.Instance, resp *instanceResponse) {
	if s == nil || inst == nil || resp == nil {
		return
	}

	slug := strings.TrimSpace(resp.Slug)
	if slug == "" {
		return
	}

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -fleetMetricsWindowDays).Format("2006-01-02")
	to := now.Format("2006-01-02")

	apiKey, keyErr := s.resolveInstanceKeyCached(ctx, inst)
	if keyErr != nil {
		return
	}

	metrics, metricsErr := s.fetchManagedInstanceMetrics(ctx, inst, apiKey, from, to)
	if metricsErr != nil {
		return
	}

	if len(metrics.Daily) == 0 {
		return
	}

	// Compute peak_daily_users_30d as max daily unique users across the window.
	var maxUsers int64
	for _, row := range metrics.Daily {
		if row.UniqueUsers > maxUsers {
			maxUsers = row.UniqueUsers
		}
	}
	resp.PeakDailyUsers30d = maxUsers

	// Compute 7-day sparklines (oldest→newest, missing days as zero).
	dateIndex := make(map[string]int, len(metrics.Daily))
	for i, row := range metrics.Daily {
		dateIndex[strings.TrimSpace(row.Date)] = i
	}

	sparkActivity := make([]int64, fleetSparkDays)
	sparkCost := make([]float64, fleetSparkDays)
	for i := 0; i < fleetSparkDays; i++ {
		date := now.AddDate(0, 0, -(fleetSparkDays - 1 - i)).Format("2006-01-02")
		if idx, ok := dateIndex[date]; ok {
			row := metrics.Daily[idx]
			sparkActivity[i] = row.TotalRequests
			sparkCost[i] = row.CostDollars
			if sparkCost[i] == 0 && row.CostCents != 0 {
				sparkCost[i] = float64(row.CostCents) / 100
			}
		}
	}
	resp.SparkActivity = sparkActivity
	resp.SparkCost = sparkCost
}

// fleetEnrichInstancesConcurrently enriches a slice of instance responses with
// Fleet data from managed Lesser instances. Enrichment is bounded:
//
//   - Concurrency is capped at fleetEnrichMaxConcurrency.
//   - Each instance gets at most fleetEnrichPerInstanceTimeout.
//   - The entire enrichment phase has an aggregate budget of
//     fleetEnrichAggregateBudget.
//
// If enrichment fails or times out for an instance, that instance's Fleet
// fields stay at zero values. The caller's list response is never failed
// because of enrichment problems.
func (s *Server) fleetEnrichInstancesConcurrently(ctx context.Context, items []*models.Instance, responses []instanceResponse) {
	if s == nil || len(items) == 0 || len(responses) == 0 {
		return
	}
	if len(items) != len(responses) {
		return
	}

	// Create a context with the aggregate enrichment budget.
	enrichCtx, cancel := context.WithTimeout(ctx, fleetEnrichAggregateBudget)
	defer cancel()

	sem := make(chan struct{}, fleetEnrichMaxConcurrency)
	var wg sync.WaitGroup

	for i := range items {
		// Check if the aggregate budget is already exhausted.
		if err := enrichCtx.Err(); err != nil {
			break
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			// Acquire semaphore or bail if the aggregate budget expires first.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-enrichCtx.Done():
				return
			}

			// Create a per-instance deadline context.
			instCtx, instCancel := context.WithTimeout(enrichCtx, fleetEnrichPerInstanceTimeout)
			defer instCancel()

			inst := items[idx]
			resp := &responses[idx]

			s.fleetEnrichFromManagedMetrics(instCtx, inst, resp)
		}(i)
	}

	wg.Wait()
}
