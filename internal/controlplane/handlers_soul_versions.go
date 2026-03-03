package controlplane

import (
	"fmt"
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Response types ---

type soulListVersionsResponse struct {
	Version    string                    `json:"version"`
	Versions   []models.SoulAgentVersion `json:"versions"`
	Count      int                       `json:"count"`
	HasMore    bool                      `json:"has_more"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}

// --- Handlers ---

// handleSoulPublicGetVersions returns paginated version history for an agent.
func (s *Server) handleSoulPublicGetVersions(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if !s.cfg.SoulEnabled {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "not found"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	limit := int(envInt64PositiveFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*models.SoulAgentVersion
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentVersion{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "VERSION#").
		OrderBy("SK", "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list versions"}
	}

	out := make([]models.SoulAgentVersion, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}

	nextCursor := ""
	hasMore := false
	if paged != nil {
		nextCursor = strings.TrimSpace(paged.NextCursor)
		hasMore = paged.HasMore
	}

	resp, err := apptheory.JSON(http.StatusOK, soulListVersionsResponse{
		Version:    "1",
		Versions:   out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	setSoulPublicHeaders(resp, "public, max-age=60")
	return resp, nil
}
