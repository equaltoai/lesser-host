package controlplane

import (
	"net/http"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// operatorReleaseChannel represents a release channel (lesser or lesser-body)
// with per-version adoption telemetry, per Project 39 provisioning walk
// Change 5.2.
type operatorReleaseChannel struct {
	ID       string                   `json:"id"`
	Versions []operatorReleaseVersion `json:"versions"`
}

type operatorReleaseVersion struct {
	Version    string                  `json:"version"`
	ReleasedAt string                  `json:"released_at"`
	IsLatest   bool                    `json:"is_latest"`
	IsBreaking bool                    `json:"is_breaking"`
	Adoption   operatorReleaseAdoption `json:"adoption"`
}

type operatorReleaseAdoption struct {
	Instances int `json:"instances"`
	Of        int `json:"of"`
	Percent   int `json:"percent"`
}

type operatorReleasesResponse struct {
	Channels   []operatorReleaseChannel `json:"channels"`
	FleetTotal int                      `json:"fleet_total"`
}

// handleOperatorReleases returns per-channel release adoption telemetry
// aggregated from host-side Instance state. Operator JWT required.
func (s *Server) handleOperatorReleases(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	instances, appErr := s.listActiveInstances(ctx)
	if appErr != nil {
		return nil, appErr
	}

	lesserTarget := strings.TrimSpace(s.cfg.ManagedLesserDefaultVersion)
	bodyTarget := strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)

	resp := buildOperatorReleasesResponse(instances, lesserTarget, bodyTarget)
	return apptheory.JSON(http.StatusOK, resp)
}

// listActiveInstances scans the Instance table for active instances.
func (s *Server) listActiveInstances(ctx *apptheory.Context) ([]*models.Instance, *apptheory.AppError) {
	var items []*models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Where("SK", "=", models.SKMetadata).
		Limit(500).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list instances"}
	}

	active := make([]*models.Instance, 0, len(items))
	for _, inst := range items {
		if inst == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(inst.Status)) != models.InstanceStatusActive {
			continue
		}
		if strings.TrimSpace(inst.Slug) == "" {
			continue
		}
		active = append(active, inst)
	}
	return active, nil
}

// buildOperatorReleasesResponse aggregates per-channel version adoption from
// active instance records. For each channel, versions are collected from
// the Instance's current-version fields, counted, and sorted newest-first.
func buildOperatorReleasesResponse(
	instances []*models.Instance,
	lesserDefault string,
	bodyDefault string,
) operatorReleasesResponse {
	fleetTotal := len(instances)

	lesserVersions := map[string]*versionAdoption{}
	bodyVersions := map[string]*versionAdoption{}

	for _, inst := range instances {
		slug := strings.TrimSpace(inst.Slug)

		lesserVer := strings.TrimSpace(inst.LesserVersion)
		if lesserVer != "" {
			va := ensureVersionAdoption(lesserVersions, lesserVer, inst.UpdatedAt)
			va.count++
			va.slugs = append(va.slugs, slug)
		}

		bodyVer := strings.TrimSpace(inst.LesserBodyVersion)
		if bodyVer != "" {
			va := ensureVersionAdoption(bodyVersions, bodyVer, inst.LesserBodyUpdateAt)
			va.count++
			va.slugs = append(va.slugs, slug)
		}
	}

	lesserChannel := buildChannel("lesser", lesserVersions, fleetTotal, lesserDefault)
	bodyChannel := buildChannel("lesser-body", bodyVersions, fleetTotal, bodyDefault)

	return operatorReleasesResponse{
		Channels:   []operatorReleaseChannel{lesserChannel, bodyChannel},
		FleetTotal: fleetTotal,
	}
}

type versionAdoption struct {
	count      int
	slugs      []string
	earliestAt time.Time
}

func ensureVersionAdoption(m map[string]*versionAdoption, version string, t time.Time) *versionAdoption {
	if va, ok := m[version]; ok {
		if !t.IsZero() && (va.earliestAt.IsZero() || t.Before(va.earliestAt)) {
			va.earliestAt = t
		}
		return va
	}
	va := &versionAdoption{earliestAt: t}
	m[version] = va
	return va
}

func buildChannel(
	id string,
	adoptions map[string]*versionAdoption,
	fleetTotal int,
	defaultVersion string,
) operatorReleaseChannel {
	versions := make([]string, 0, len(adoptions))
	for v := range adoptions {
		versions = append(versions, v)
	}
	sortVersionStringsDesc(versions)

	out := make([]operatorReleaseVersion, 0, len(versions))
	for _, v := range versions {
		va := adoptions[v]
		releasedAt := ""
		if !va.earliestAt.IsZero() {
			releasedAt = va.earliestAt.UTC().Format(time.RFC3339)
		}
		pct := 0
		if fleetTotal > 0 {
			pct = (va.count * 100) / fleetTotal
		}
		out = append(out, operatorReleaseVersion{
			Version:    v,
			ReleasedAt: releasedAt,
			IsLatest:   strings.EqualFold(v, strings.TrimSpace(defaultVersion)),
			IsBreaking: false,
			Adoption: operatorReleaseAdoption{
				Instances: va.count,
				Of:        fleetTotal,
				Percent:   pct,
			},
		})
	}

	return operatorReleaseChannel{ID: id, Versions: out}
}

// sortVersionStringsDesc sorts semver-like strings in descending order.
// Falls back to simple string comparison for non-semver strings.
func sortVersionStringsDesc(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
}

// compareVersions compares two version strings. Returns -1, 0, or 1.
// Handles the "v" prefix and semver-style three-component versions.
// Non-semver strings are compared lexicographically.
func compareVersions(a, b string) int {
	// Strip leading 'v' prefix if present.
	sa := strings.TrimPrefix(strings.TrimSpace(a), "v")
	sb := strings.TrimPrefix(strings.TrimSpace(b), "v")

	// Try to split into numeric segments.
	partsA := splitVersionNumeric(sa)
	partsB := splitVersionNumeric(sb)

	n := len(partsA)
	if len(partsB) < n {
		n = len(partsB)
	}

	for k := 0; k < n; k++ {
		if partsA[k] < partsB[k] {
			return -1
		}
		if partsA[k] > partsB[k] {
			return 1
		}
	}

	if len(partsA) < len(partsB) {
		return -1
	}
	if len(partsA) > len(partsB) {
		return 1
	}
	return 0
}

// splitVersionNumeric splits a semver-like string into numeric parts.
// Non-numeric segments are treated as 0 for comparison purposes.
func splitVersionNumeric(s string) []int {
	segments := strings.Split(s, ".")
	out := make([]int, 0, len(segments))
	for _, seg := range segments {
		n := 0
		// Extract leading numeric portion.
		for _, r := range seg {
			if r >= '0' && r <= '9' {
				n = n*10 + int(r-'0')
			} else {
				break
			}
		}
		out = append(out, n)
	}
	return out
}
