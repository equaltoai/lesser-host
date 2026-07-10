package controlplane

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

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
		return nil, newAppTheoryError("app.not_found", "not found")
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}

	limit := soulVersionsPageLimit(ctx)
	cursor := strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "cursor"))
	versions, hasMore, nextCursor, appErr := s.loadSoulAgentVersions(ctx, agentIDHex, cursor, limit)
	if appErr != nil {
		return nil, appErr
	}

	resp, err := apptheory.JSON(http.StatusOK, soulListVersionsResponse{
		Version:    "1",
		Versions:   versions,
		Count:      len(versions),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	s.setSoulPublicHeaders(ctx, resp, "public, max-age=60")
	return resp, nil
}

func soulVersionsPageLimit(ctx *apptheory.Context) int {
	return envIntPositiveClampedFromString(httpx.FirstQueryValue(ctx.Request.Query, "limit"), 50, 200)
}

const soulVersionsCursorPrefix = "version:"

func (s *Server) loadSoulAgentVersions(ctx *apptheory.Context, agentIDHex string, cursor string, limit int) ([]models.SoulAgentVersion, bool, string, *apptheory.AppTheoryError) {
	if limit <= 0 {
		limit = 50
	}

	var identity models.SoulAgentIdentity
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentIdentity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "=", "IDENTITY").
		First(&identity); theoryErrors.IsNotFound(err) {
		return []models.SoulAgentVersion{}, false, "", nil
	} else if err != nil {
		return nil, false, "", newAppTheoryError("app.internal", "failed to list versions")
	}

	startVersion, appErr := soulVersionsStartVersion(cursor, identity.SelfDescriptionVersion)
	if appErr != nil {
		return nil, false, "", appErr
	}
	if startVersion <= 0 {
		return []models.SoulAgentVersion{}, false, "", nil
	}

	versions := make([]models.SoulAgentVersion, 0, limit)
	nextVersion := 0
	scanned := 0
	for version := startVersion; version >= 1 && len(versions) < limit && scanned < limit; version-- {
		scanned++
		nextVersion = version - 1
		var item models.SoulAgentVersion
		if err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.SoulAgentVersion{}).
			Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
			Where("SK", "=", fmt.Sprintf("VERSION#%d", version)).
			First(&item); theoryErrors.IsNotFound(err) {
			continue
		} else if err != nil {
			return nil, false, "", newAppTheoryError("app.internal", "failed to list versions")
		}
		versions = append(versions, item)
	}

	if nextVersion >= 1 {
		return versions, true, soulVersionsCursor(nextVersion), nil
	}
	return versions, false, "", nil
}

func soulVersionsStartVersion(cursor string, latestVersion int) (int, *apptheory.AppTheoryError) {
	if latestVersion <= 0 {
		return 0, nil
	}
	raw := strings.TrimSpace(cursor)
	if raw == "" {
		return latestVersion, nil
	}
	raw = strings.TrimPrefix(raw, soulVersionsCursorPrefix)
	raw = strings.TrimPrefix(raw, "VERSION#")
	version, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || version <= 0 {
		return 0, newAppTheoryError("app.bad_request", "invalid cursor")
	}
	if version > latestVersion {
		version = latestVersion
	}
	return version, nil
}

func soulVersionsCursor(nextVersion int) string {
	return fmt.Sprintf("%s%d", soulVersionsCursorPrefix, nextVersion)
}
