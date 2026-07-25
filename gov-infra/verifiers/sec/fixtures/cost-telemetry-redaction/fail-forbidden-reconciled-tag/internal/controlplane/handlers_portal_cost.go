//go:build ignore

package controlplane

import (
	"net/http"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/costtelemetry"
)

type portalCostResponse struct {
	InstanceSlug string               `json:"instance_slug"`
	Days         []portalCostDayEntry `json:"days"`
	Count        int                  `json:"count"`
}

type portalCostDayEntry struct {
	Date     string                              `json:"date"`
	DayCost  float64                             `json:"day_cost"`
	Currency string                              `json:"currency"`
	Entries  []costtelemetry.ReconciledCostEntry `json:"entries"`
}

func (s *Server) handlePortalGetInstanceCost(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	apiKey, keyErr := s.resolvePortalCostInstanceKey(ctx.Context(), inst)
	if keyErr != nil {
		return nil, keyErr
	}
	metrics, metricsErr := s.fetchManagedInstanceMetrics(ctx.Context(), inst, apiKey, "2026-05-25", "2026-05-26")
	if metricsErr != nil {
		return nil, metricsErr
	}
	_ = metrics
	return apptheory.JSON(http.StatusOK, portalCostResponse{InstanceSlug: inst.Slug})
}
