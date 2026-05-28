package controlplane

import (
	"context"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fleetSparkDays is the number of daily buckets returned for sparkline fields.
const fleetSparkDays = 7

// fleetMetricsWindowDays is the lookback window for the managed Lesser metrics
// fetch used to compute active_users_30d and the 7-day sparklines.
const fleetMetricsWindowDays = 30

// fleetEnrichFromManagedMetrics populates Fleet data fields on the given
// instanceResponse by calling the managed Lesser instance metrics endpoint.
// It uses the existing resolvePortalCostInstanceKey + fetchManagedInstanceMetrics
// path that has been available since M0.4/#529.
//
// Semantics:
//   - active_users_30d = max daily UniqueUsers over the 30-day window.
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

	apiKey, keyErr := s.resolvePortalCostInstanceKey(ctx, inst)
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

	// Compute active_users_30d as max daily unique users across the window.
	var maxUsers int64
	for _, row := range metrics.Daily {
		if row.UniqueUsers > maxUsers {
			maxUsers = row.UniqueUsers
		}
	}
	resp.ActiveUsers30d = maxUsers

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
