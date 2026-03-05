package controlplane

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentVersion{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "VERSION#").
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list versions"}
	}

	versions := make([]models.SoulAgentVersion, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		versions = append(versions, *item)
	}

	sort.Slice(versions, func(i, j int) bool {
		if versions[i].VersionNumber == versions[j].VersionNumber {
			return versions[i].CreatedAt.After(versions[j].CreatedAt)
		}
		return versions[i].VersionNumber > versions[j].VersionNumber
	})

	afterVersion := 0
	if cursor != "" {
		if v, parseErr := strconv.Atoi(cursor); parseErr == nil && v > 0 {
			afterVersion = v
		}
	}

	start := 0
	if afterVersion > 0 {
		start = len(versions)
		for i := range versions {
			if versions[i].VersionNumber < afterVersion {
				start = i
				break
			}
		}
	}
	if start > len(versions) {
		start = len(versions)
	}

	end := start + limit
	if end > len(versions) {
		end = len(versions)
	}
	out := versions[start:end]

	hasMore := end < len(versions)
	nextCursor := ""
	if hasMore && len(out) > 0 {
		nextCursor = strconv.Itoa(out[len(out)-1].VersionNumber)
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
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}
