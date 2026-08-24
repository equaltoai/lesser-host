package controlplane

import (
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

	"github.com/equaltoai/lesser-host/internal/costtelemetry"
	"github.com/equaltoai/lesser-host/internal/httpx"
)

// portalCostResponse is the public DTO returned by
// GET /api/v1/portal/instances/{slug}/cost (M3.11).
//
// Safety invariants:
//   - Excludes PK, SK, TTL, account_id, raw EntriesJSON string, raw instance
//     keys, secrets, request bodies, and tenant content.
//   - M0.4 sources daily usage/cost data from the managed Lesser instance via
//     an instance-key-authenticated server-side call; raw instance keys never
//     reach the browser.
type portalCostResponse struct {
	InstanceSlug string               `json:"instance_slug"`
	FromDate     string               `json:"from_date"`
	ToDate       string               `json:"to_date"`
	Days         []portalCostDayEntry `json:"days"`
	Count        int                  `json:"count"`
	TotalCost    float64              `json:"total_cost"`
	Currency     string               `json:"currency"`
}

// portalCostDayEntry is one day of cost telemetry in the M3.11 response.
type portalCostDayEntry struct {
	Date     string                              `json:"date"`
	DayCost  float64                             `json:"day_cost"`
	Currency string                              `json:"currency"`
	Entries  []costtelemetry.ReconciledCostEntry `json:"entries"`
}

// costQueryDefaultDays is the default lookback window when no query params
// are supplied (past 30 days inclusive).
const costQueryDefaultDays = 30

// costQueryMaxDays caps explicit portal cost windows before the request is
// proxied to the managed Lesser instance.
const costQueryMaxDays = 366

// handlePortalGetInstanceCost implements GET /api/v1/portal/instances/{slug}/cost.
//
// Ownership is enforced via requireInstanceAccess before any instance-key
// secret read or managed Lesser HTTP call. Optional query params from and to
// (YYYY-MM-DD) narrow the window; the default is the past 30 days inclusive.
// Invalid dates fail closed.
func (s *Server) handlePortalGetInstanceCost(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	from, to, parseErr := parseCostDateWindow(ctx)
	if parseErr != nil {
		return nil, parseErr
	}

	apiKey, keyErr := s.resolvePortalCostInstanceKey(ctx.Context(), inst)
	if keyErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to resolve instance metrics access")
	}

	metrics, metricsErr := s.fetchManagedInstanceMetrics(ctx.Context(), inst, apiKey, from, to)
	if metricsErr != nil {
		return nil, metricsErr
	}

	resp := buildPortalCostResponseFromLesser(slug, from, to, metrics)
	return apptheory.JSON(http.StatusOK, resp)
}

// parseCostDateWindow extracts and validates the from/to date query params.
// Defaults to the past 30 days inclusive when neither is supplied.
func parseCostDateWindow(ctx *apptheory.Context) (from, to string, appErr *apptheory.AppTheoryError) {
	from = strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "from"))
	to = strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "to"))

	if from == "" && to == "" {
		now := time.Now().UTC()
		return now.AddDate(0, 0, -costQueryDefaultDays).Format("2006-01-02"),
			now.Format("2006-01-02"),
			nil
	}
	if from == "" || to == "" {
		return "", "", newAppTheoryError("app.bad_request", "from and to are required when either is supplied")
	}

	fromTime, err := time.Parse("2006-01-02", from)
	if err != nil {
		return "", "", newAppTheoryError("app.bad_request", "from must be YYYY-MM-DD")
	}
	toTime, err := time.Parse("2006-01-02", to)
	if err != nil {
		return "", "", newAppTheoryError("app.bad_request", "to must be YYYY-MM-DD")
	}
	if fromTime.After(toTime) {
		return "", "", newAppTheoryError("app.bad_request", "from must not be after to")
	}
	if toTime.Sub(fromTime) > time.Duration(costQueryMaxDays)*24*time.Hour {
		return "", "", newAppTheoryError("app.bad_request", "date range must not exceed 366 days")
	}

	return from, to, nil
}

func buildPortalCostResponseFromLesser(slug, from, to string, metrics lesserInstanceMetricsResponse) portalCostResponse {
	days := make([]portalCostDayEntry, 0, len(metrics.Daily))
	total := 0.0
	currency := "USD"

	for _, row := range metrics.Daily {
		date := strings.TrimSpace(row.Date)
		if date == "" {
			continue
		}

		rowCurrency := strings.TrimSpace(row.Currency)
		if rowCurrency == "" {
			rowCurrency = "USD"
		}
		currency = rowCurrency

		dayCost := row.CostDollars
		if dayCost == 0 && row.CostCents != 0 {
			dayCost = float64(row.CostCents) / 100
		}
		total += dayCost

		entries := []costtelemetry.ReconciledCostEntry{
			{
				Date:     date,
				Service:  "Managed Lesser",
				Cost:     dayCost,
				Currency: rowCurrency,
				Metrics: []costtelemetry.ServiceAttribution{
					{Service: "Managed Lesser", MetricName: "Requests", Stat: "Sum", Unit: "Count", Value: float64(row.TotalRequests)},
					{Service: "Managed Lesser", MetricName: "UniqueUsers", Stat: "Maximum", Unit: "Count", Value: float64(row.UniqueUsers)},
					{Service: "DynamoDB", MetricName: "ConsumedReadCapacityUnits", Stat: "Sum", Unit: "Count", Value: float64(row.DynamoDBReads)},
					{Service: "DynamoDB", MetricName: "ConsumedWriteCapacityUnits", Stat: "Sum", Unit: "Count", Value: float64(row.DynamoDBWrites)},
					{Service: "Lambda", MetricName: "Duration", Stat: "Sum", Unit: "Milliseconds", Value: float64(row.LambdaDurationMs)},
				},
			},
		}

		days = append(days, portalCostDayEntry{
			Date:     date,
			DayCost:  dayCost,
			Currency: rowCurrency,
			Entries:  entries,
		})
	}

	return portalCostResponse{
		InstanceSlug: slug,
		FromDate:     from,
		ToDate:       to,
		Days:         days,
		Count:        len(days),
		TotalCost:    total,
		Currency:     currency,
	}
}
