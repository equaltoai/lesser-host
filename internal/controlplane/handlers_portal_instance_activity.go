package controlplane

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

// portalInstanceActivityResponse is the public DTO returned by
// GET /api/v1/portal/instances/{slug}/activity (M11 corrective).
//
// Safety invariants:
//   - Ownership enforced via requireInstanceAccess before any instance-key
//     secret read or managed Lesser HTTP call.
//   - Raw instance keys never reach the browser.
//   - Only the current-month statuses total is returned (no raw upstream payload dump).
type portalInstanceActivityResponse struct {
	InstanceSlug string `json:"instance_slug"`
	Statuses     int64  `json:"statuses"`
	Weeks        int    `json:"weeks"`
	Month        string `json:"month"`
}

// lesserActivityEntry is a single week row from Lesser's
// GET /api/v1/instance/activity response.
//
// Mastodon convention: all numeric fields are strings.
type lesserActivityEntry struct {
	Week          string `json:"week"`
	Statuses      string `json:"statuses"`
	Logins        string `json:"logins"`
	Registrations string `json:"registrations"`
}

// handlePortalGetInstanceActivity implements
// GET /api/v1/portal/instances/{slug}/activity.
//
// Ownership is enforced via requireInstanceAccess before any instance-key
// resolution or HTTP call to the managed Lesser instance. The handler
// calls Lesser's GET /api/v1/instance/activity, filters weekly entries
// to the current month, and returns the summed statuses count.
//
// This bridge exists because the owner's fleet billing dashboard needs a
// post/status denominator for the "Per federated post" metric, and the
// browser cannot safely call Lesser directly with an instance key.
func (s *Server) handlePortalGetInstanceActivity(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	apiKey, keyErr := s.resolvePortalCostInstanceKey(ctx.Context(), inst)
	if keyErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to resolve instance metrics access"}
	}

	baseURL, urlErr := s.resolvePortalCostMetricsBaseURL(inst)
	if urlErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to resolve instance activity endpoint"}
	}

	endpoint := fmt.Sprintf("%s/api/v1/instance/activity", strings.TrimRight(baseURL, "/"))

	req, reqErr := http.NewRequestWithContext(ctx.Context(), http.MethodGet, endpoint, nil)
	if reqErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create instance activity request"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	client := s.portalManagedHTTPClient()

	resp, respErr := client.Do(req) //nolint:gosec // URL is derived from managed instance metadata, not from browser input.
	if respErr != nil {
		return nil, &apptheory.AppError{Code: "app.upstream_unavailable", Message: "failed to reach instance activity"}
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, &apptheory.AppError{Code: "app.upstream_error", Message: "failed to fetch instance activity"}
	}

	var entries []lesserActivityEntry
	if decErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&entries); decErr != nil {
		return nil, &apptheory.AppError{Code: "app.upstream_error", Message: "failed to decode instance activity"}
	}

	now := time.Now().UTC()
	currentMonth := now.Format("2006-01")

	var totalStatuses int64
	var weeksInMonth int

	for _, e := range entries {
		weekTS, parseErr := strconv.ParseInt(strings.TrimSpace(e.Week), 10, 64)
		if parseErr != nil {
			continue
		}
		weekTime := time.Unix(weekTS, 0).UTC()
		if weekTime.Format("2006-01") != currentMonth {
			continue
		}

		sVal, sErr := strconv.ParseInt(strings.TrimSpace(e.Statuses), 10, 64)
		if sErr != nil {
			continue
		}
		totalStatuses += sVal
		weeksInMonth++
	}

	return apptheory.JSON(http.StatusOK, portalInstanceActivityResponse{
		InstanceSlug: slug,
		Statuses:     totalStatuses,
		Weeks:        weeksInMonth,
		Month:        currentMonth,
	})
}

// compile-time guard: Server implements the handler.
var _ = (*Server).handlePortalGetInstanceActivity
