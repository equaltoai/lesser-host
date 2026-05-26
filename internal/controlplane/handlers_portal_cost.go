package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/costtelemetry"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// portalCostResponse is the public DTO returned by
// GET /api/v1/portal/instances/{slug}/cost (M3.11).
//
// Safety invariants:
//   - Excludes PK, SK, TTL, account_id, raw EntriesJSON string, raw instance
//     keys, secrets, request bodies, and tenant content.
//   - Each day's entries are decoded from the store's cached JSON into
//     ReconciledCostEntry structs, which carry no account_id or internal fields.
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

// costQueryMaxLimit caps the number of records returned.
const costQueryMaxLimit = 200

// handlePortalGetInstanceCost implements GET /api/v1/portal/instances/{slug}/cost.
//
// Ownership is enforced via requireInstanceAccess before any cost telemetry
// read. Optional query params from and to (YYYY-MM-DD) narrow the window;
// the default is the past 30 days inclusive. Invalid dates fail closed.
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

	records, storeErr := s.store.ListCostTelemetryByInstance(ctx.Context(), slug, from, to, costQueryMaxLimit)
	if storeErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list cost telemetry"}
	}

	resp := buildPortalCostResponse(slug, from, to, records)
	return apptheory.JSON(http.StatusOK, resp)
}

// parseCostDateWindow extracts and validates the from/to date query params.
// Defaults to the past 30 days inclusive when neither is supplied.
func parseCostDateWindow(ctx *apptheory.Context) (from, to string, appErr *apptheory.AppError) {
	from = strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "from"))
	to = strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "to"))

	if from == "" && to == "" {
		now := time.Now().UTC()
		return now.AddDate(0, 0, -costQueryDefaultDays).Format("2006-01-02"),
			now.Format("2006-01-02"),
			nil
	}
	if from == "" || to == "" {
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "from and to are required when either is supplied"}
	}

	fromTime, err := time.Parse("2006-01-02", from)
	if err != nil {
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "from must be YYYY-MM-DD"}
	}
	toTime, err := time.Parse("2006-01-02", to)
	if err != nil {
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "to must be YYYY-MM-DD"}
	}
	if fromTime.After(toTime) {
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "from must not be after to"}
	}

	return from, to, nil
}

// buildPortalCostResponse decodes EntriesJSON from each CostTelemetry record
// and assembles the public response DTO. Internal fields (PK, SK, TTL,
// account_id, raw JSON string) are excluded by construction.
func buildPortalCostResponse(slug, from, to string, records []*models.CostTelemetry) portalCostResponse {
	var total float64
	days := make([]portalCostDayEntry, 0, len(records))
	currency := "USD"

	for _, rec := range records {
		if rec == nil {
			continue
		}

		var entries []costtelemetry.ReconciledCostEntry
		if rec.EntriesJSON != "" {
			if unmarshalErr := json.Unmarshal([]byte(rec.EntriesJSON), &entries); unmarshalErr != nil {
				continue
			}
		}
		if entries == nil {
			entries = []costtelemetry.ReconciledCostEntry{}
		}

		total += rec.DayCost
		if rec.Currency != "" {
			currency = rec.Currency
		}

		days = append(days, portalCostDayEntry{
			Date:     rec.Date,
			DayCost:  rec.DayCost,
			Currency: rec.Currency,
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
