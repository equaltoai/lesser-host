package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// portalTrustDataResponse is the top-level response for the portal trust-data
// endpoint. All five categories are populated even when backing telemetry does
// not yet exist; in those cases the fields carry safe zero/empty/null values
// with documented semantics.
type portalTrustDataResponse struct {
	InstanceSlug string `json:"instance_slug"`

	// Truncated is true when host hit a soul-backed fan-out or row-read cap
	// while building the trust-data response. Counters then reflect bounded
	// work only. The field is omitted for the normal, complete response shape.
	Truncated bool `json:"truncated,omitempty"`

	// Federation contains federation-peer health counters and a bounded set of
	// peer rows from the requested managed Lesser instance's admin federation API.
	Federation portalTrustFederationResponse `json:"federation"`

	// Signatures contains signature-failure counters scoped to the dashboard
	// window. Populated from SoulAgentFailure rows when soul registry is
	// configured and agents are bound to the requesting instance.
	Signatures portalTrustSignaturesResponse `json:"signatures"`

	// QueueDepth contains an inbound queue depth time series. Populated from
	// instance-scoped queue-depth samples; current snapshots are derived from
	// canonical soul-comm mailbox rows scoped by instance slug + bound agent ID.
	QueueDepth portalTrustQueueDepthResponse `json:"queue_depth"`

	// TrustScore exposes a computed trust score with documented formula and
	// dimension breakdown. Populated from SoulAgentReputation rows when soul
	// registry is configured and agents are bound to the instance.
	TrustScore portalTrustScoreResponse `json:"trust_score"`

	// Vouches exposes per-peer vouches (peer/strength pairs) with sensitive
	// provenance redacted. Populated from SoulAgentPeerEndorsement rows when
	// soul registry is configured and agents are bound to the instance.
	Vouches portalTrustVouchesResponse `json:"vouches"`
}

// portalTrustFederationResponse contains federation-peer health data for a
// single managed instance.
//
// Data source: the requested owner-scoped managed Lesser instance's admin
// federation endpoints, reached server-side with its one-time instance key:
//   - /api/v1/admin/federation/statistics (availability/time-range probe)
//   - /api/v1/admin/federation/instances (peer rows and health counters)
//
// If the requested instance has no managed endpoint/key (for example, external
// registrations), the response degrades to zero/empty values without consulting
// any global or cross-tenant data source.
type portalTrustFederationResponse struct {
	// Reachable is the count of federation peers currently reachable.
	Reachable int `json:"reachable"`

	// Warning is the count of peers with degraded reachability.
	Warning int `json:"warning"`

	// Severed is the count of peers whose federation link has been suspended.
	Severed int `json:"severed"`

	// Peers is a bounded list of peer rows (max 50). Each row includes the peer
	// domain, status, and an honest last_seen or last_fetch timestamp.
	Peers []portalTrustFederationPeerRow `json:"peers"`

	// Source names the backing telemetry source for audit/debug display.
	Source string `json:"source"`

	// Truncated is true when host hit its admin API fetch cap before Lesser's
	// pagination was exhausted. Counters then reflect fetched rows only.
	Truncated bool `json:"truncated,omitempty"`
}

// portalTrustFederationPeerRow is a single federation peer row. Sensitive
// fields (hosting account IDs, raw connection credentials) are never included.
type portalTrustFederationPeerRow struct {
	Domain        string `json:"domain"`
	Status        string `json:"status"` // reachable, warning, severed
	LastSeen      string `json:"last_seen,omitempty"`
	LastFetch     string `json:"last_fetch,omitempty"`
	FollowerCount *int   `json:"follower_count,omitempty"`
}

// portalTrustSignaturesResponse contains signature-failure counters and a
// bounded hourly time series over the dashboard window, scoped to agents bound
// to the requesting instance.
//
// Data source: SoulAgentFailure rows (FailureType = "signature_failure")
// queried per resolved agent, filtered to the 24-hour dashboard window.
type portalTrustSignaturesResponse struct {
	// WindowHours is the lookback window in hours (default 24 hours).
	WindowHours int `json:"window_hours"`

	// TotalFailures is the total signature-failure count in the window.
	TotalFailures int `json:"total_failures"`

	// BySource is a bounded per-bound-agent breakdown (max 50 entries).
	BySource []portalTrustSignaturesSourceRow `json:"by_source"`

	// Series is a true hourly time series of signature failures across all
	// scoped agents. Each point is a nonzero UTC hour bucket derived from
	// SoulAgentFailure.Timestamp; raw failure IDs, descriptions, storage keys,
	// account IDs, and agent IDs are not exposed in the point shape.
	Series []portalTrustSignatureSeriesPoint `json:"series"`

	// Truncated is true when host hit a fan-out or row-read cap while reading
	// signature-failure rows. Counts then reflect the bounded rows read.
	Truncated bool `json:"truncated,omitempty"`
}

// portalTrustSignaturesSourceRow is a per-bound-agent signature-failure count.
// Source is the bound soul agent ID, not a remote federation peer.
type portalTrustSignaturesSourceRow struct {
	Source   string `json:"source"`
	Failures int    `json:"failures"`
}

// portalTrustSignatureSeriesPoint is a single signature-failure time-series
// data point. Timestamp is the UTC hour bucket and Failures is the number of
// signature failures in that bucket.
type portalTrustSignatureSeriesPoint struct {
	Timestamp string `json:"timestamp"` // ISO 8601 UTC hour bucket
	Failures  int    `json:"failures"`
}

// portalTrustQueueDepthResponse contains an inbound queue depth time series
// for the customer-owned instance.
//
// Data source: TrustQueueDepthSample rows scoped to the requested instance slug.
// Current samples are recorded from SoulCommMailboxMessage Count() queries that
// are scoped by instance slug + bound agent ID; no global SQS or cross-tenant
// queue is queried.
type portalTrustQueueDepthResponse struct {
	// WindowHours is the lookback window in hours (default 24 hours).
	WindowHours int `json:"window_hours"`

	// Source names the backing telemetry source.
	Source string `json:"source"`

	// Series contains time-series data points (max 48 points in the 24h window).
	Series []portalTrustQueueDepthPoint `json:"series"`
}

// portalTrustQueueDepthPoint is a single time-series data point.
type portalTrustQueueDepthPoint struct {
	Timestamp string `json:"timestamp"` // ISO 8601 UTC
	Depth     int    `json:"depth"`
}

// portalTrustScoreResponse exposes a computed trust score with documented
// formula and per-dimension breakdown.
//
// Score formula: aggregate trust_score = average(Composite) across all
// bound agents that have a SoulAgentReputation row.
//
// Dimension mapping (SoulAgentReputation field → trust dimension):
//   - operational ← Trust (reflects operational reliability signals)
//   - attestation ← Validation (validation records proxy for attestation)
//   - social      ← Social
//   - economic    ← Economic
//   - integrity   ← Integrity
//
// Each dimension is averaged across all agents with reputation rows.
// When no agent reputation rows exist, score is 0 and dimensions are 0.
type portalTrustScoreResponse struct {
	// Score is the aggregate composite trust score (0.0–100.0).
	Score float64 `json:"score"`

	// Formula is a human-readable description of the scoring formula.
	Formula string `json:"formula"`

	// Dimensions contains individual dimension scores.
	Dimensions portalTrustScoreDimensions `json:"dimensions"`

	// Source describes where the score data originates.
	Source string `json:"source"`
}

// portalTrustScoreDimensions contains the individual dimension scores that
// compose the trust score.
type portalTrustScoreDimensions struct {
	Operational float64 `json:"operational"`
	Attestation float64 `json:"attestation"`
	Social      float64 `json:"social"`
	Economic    float64 `json:"economic"`
	Integrity   float64 `json:"integrity"`
}

// portalTrustVouchesResponse exposes vouches as peer/strength pairs with
// sensitive provenance redacted.
//
// Data source: SoulAgentPeerEndorsement rows queried per resolved agent.
// Each signed endorsement is a vouch; strength defaults to 1.0 per the
// documented default since the model has no numeric strength field.
type portalTrustVouchesResponse struct {
	// Items is a bounded list of vouch entries (max 50).
	Items []portalTrustVouchItem `json:"items"`

	// Count is the total number of vouches available (may exceed Items length).
	Count int `json:"count"`

	// Truncated is true when host hit a fan-out or row-read cap while reading
	// endorsement rows. Count is still computed with Count() for processed
	// agents; Items reflect the bounded rows read.
	Truncated bool `json:"truncated,omitempty"`
}

// portalTrustVouchItem is a single vouch entry. Sensitive provenance fields
// (raw signatures, internal PK/SK, account IDs) are never included.
type portalTrustVouchItem struct {
	// Peer is the peer identifier (endorser agent ID).
	Peer string `json:"peer"`

	// Strength is currently a fixed presence marker (1.0) for endorsements
	// because SoulAgentPeerEndorsement has no numeric strength field.
	Strength float64 `json:"strength"`

	// Type is the vouch relationship type ("endorsement").
	Type string `json:"type,omitempty"`

	// CreatedAt is when the vouch was recorded (ISO 8601 UTC).
	CreatedAt string `json:"created_at,omitempty"`
}

// trustScoreFormula is the documented formula string returned in every response.
const trustScoreFormula = "trust_score = average(Composite) across agents; " +
	"dimensions: operational←Trust, attestation←Validation, social←Social, economic←Economic, integrity←Integrity; " +
	"weights: all equal (1.0); source: soul_agent_reputation"

// trustScoreSource identifies the backing data model.
const trustScoreSource = "lesser-host:soul_agent_reputation"

// trustFederationSource identifies the Lesser admin API used for peer telemetry.
const trustFederationSource = "lesser:/api/v1/admin/federation"

// trustQueueDepthSource identifies the scoped host model used for queue telemetry.
const trustQueueDepthSource = "lesser-host:soul_comm_mailbox_queue"

const (
	trustFederationStatusReachable = "reachable"
	trustFederationStatusWarning   = "warning"
	trustFederationStatusSevered   = "severed"
)

// trustSignaturesWindowHours is the dashboard lookback window (24 hours).
const trustSignaturesWindowHours = 24

// trustQueueDepthWindowHours is the queue-depth dashboard lookback window (24 hours).
const trustQueueDepthWindowHours = 24

// trustVouchesMaxItems caps the number of vouch items returned.
const trustVouchesMaxItems = 50

// trustDataMaxAgentFanout bounds soul-backed per-agent query fan-out for a
// single trust-data request. The response Truncated flags expose cap hits.
const trustDataMaxAgentFanout = 50

// trustSignaturesMaxRowsPerAgent is a defensive cap for already-window-bounded
// SoulAgentFailure rows read per agent.
const trustSignaturesMaxRowsPerAgent = 500

// trustSignaturesMaxRowsPerRequest bounds total signature rows read across all
// processed agents in a single request.
const trustSignaturesMaxRowsPerRequest = 2000

// trustVouchesMaxRowsPerAgent caps endorsement rows loaded per agent before the
// response-level 50-item truncation. Count uses Count(); Items use bounded rows.
const trustVouchesMaxRowsPerAgent = trustVouchesMaxItems + 25

// trustVouchesMaxRowsPerRequest bounds total endorsement rows loaded across all
// processed agents in a single request.
const trustVouchesMaxRowsPerRequest = 500

// trustSignaturesMaxSources caps the number of by-source rows returned.
const trustSignaturesMaxSources = 50

// trustFederationPeerRowsMax caps peer rows returned to the browser.
const trustFederationPeerRowsMax = 50

// trustFederationFetchLimit is the page size used against Lesser's admin API.
const trustFederationFetchLimit = 100

// trustFederationFetchCap bounds host-side federation pages consumed per request.
const trustFederationFetchCap = 500

// trustFederationDegradedTrustThreshold maps low non-zero Lesser trust scores to warnings.
const trustFederationDegradedTrustThreshold = 50.0

// trustFederationStaleSeenHours maps stale peers to warning status.
const trustFederationStaleSeenHours = 30 * 24

// trustQueueDepthMaxSeriesPoints caps queue-depth points returned to the browser.
const trustQueueDepthMaxSeriesPoints = 48

// signatureFailureType is the normalized SoulAgentFailure.FailureType value
// for signature failures. The model normalises FailureType to lowercase.
const signatureFailureType = "signature_failure"

// vouchDefaultStrength is the documented default vouch presence marker (1.0)
// when the backing model has no numeric strength field. M15 should render
// vouches as a list/count rather than a comparative strength bar until a
// numeric strength source exists.
const vouchDefaultStrength = 1.0

type lesserFederationInstanceInfo struct {
	Domain        string  `json:"domain"`
	IsSilenced    bool    `json:"is_silenced"`
	IsSuspended   bool    `json:"is_suspended"`
	FirstSeen     string  `json:"first_seen"`
	LastSeen      string  `json:"last_seen"`
	ActiveUsers   int64   `json:"active_users"`
	TotalMessages int64   `json:"total_messages"`
	TrustScore    float64 `json:"trust_score"`
	Software      string  `json:"software"`
	Version       string  `json:"version"`
}

type lesserFederationInstancesPage struct {
	Instances  []lesserFederationInstanceInfo `json:"instances"`
	NextCursor *string                        `json:"next_cursor"`
}

func (p *lesserFederationInstancesPage) UnmarshalJSON(data []byte) error {
	var arr []lesserFederationInstanceInfo
	if err := json.Unmarshal(data, &arr); err == nil {
		p.Instances = arr
		p.NextCursor = nil
		return nil
	}
	type pageAlias lesserFederationInstancesPage
	var obj pageAlias
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	p.Instances = obj.Instances
	p.NextCursor = obj.NextCursor
	return nil
}

type lesserFederationStatisticsResponse struct {
	ActiveInstances int `json:"active_instances"`
	TotalUsers      int `json:"total_users"`
	TotalMessages   int `json:"total_messages"`
	TimeRange       struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"time_range"`
}

// handlePortalGetTrustData returns per-instance trust data for the Trust
// dashboard. Requires customer authentication and instance ownership (or
// operator role).
//
//	GET /api/v1/portal/instances/{slug}/trust/data
func (s *Server) handlePortalGetTrustData(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	resp := portalTrustDataResponse{
		InstanceSlug: strings.ToLower(strings.TrimSpace(inst.Slug)),
		Federation: portalTrustFederationResponse{
			Reachable: 0,
			Warning:   0,
			Severed:   0,
			Peers:     []portalTrustFederationPeerRow{},
			Source:    trustFederationSource,
		},
		Signatures: portalTrustSignaturesResponse{
			WindowHours:   trustSignaturesWindowHours,
			TotalFailures: 0,
			BySource:      []portalTrustSignaturesSourceRow{},
			Series:        []portalTrustSignatureSeriesPoint{},
		},
		QueueDepth: portalTrustQueueDepthResponse{
			WindowHours: trustQueueDepthWindowHours,
			Source:      trustQueueDepthSource,
			Series:      []portalTrustQueueDepthPoint{},
		},
		TrustScore: portalTrustScoreResponse{
			Score:      0,
			Formula:    trustScoreFormula,
			Dimensions: portalTrustScoreDimensions{},
			Source:     trustScoreSource,
		},
		Vouches: portalTrustVouchesResponse{
			Items: []portalTrustVouchItem{},
			Count: 0,
		},
	}

	if federation, appErr := s.loadTrustFederation(ctx, inst); appErr == nil {
		resp.Federation = federation
	}

	// Populate soul-backed data when the soul registry is configured.
	if s.cfg.SoulEnabled {
		agentIDs := s.resolveAgentIDsForInstance(ctx, inst)
		agentIDs, agentFanoutTruncated := boundedTrustAgentIDs(agentIDs)
		if len(agentIDs) > 0 {
			resp.Truncated = agentFanoutTruncated
			resp.Signatures = s.loadTrustSignatures(ctx.Context(), agentIDs)
			resp.QueueDepth = s.loadTrustQueueDepth(ctx.Context(), inst, agentIDs)
			resp.TrustScore = s.loadTrustScore(ctx.Context(), agentIDs)
			resp.Vouches = s.loadTrustVouches(ctx.Context(), agentIDs)
			resp.Truncated = resp.Truncated || resp.Signatures.Truncated || resp.Vouches.Truncated
		}
	}

	return apptheory.JSON(http.StatusOK, resp)
}

// resolveAgentIDsForInstance derives the set of agent IDs bound to a single
// instance by resolving its verified/managed domains through the soul domain
// agent index. Returns nil on any error so callers degrade to zero/empty.
func (s *Server) resolveAgentIDsForInstance(ctx *apptheory.Context, inst *models.Instance) []string {
	// Build domain set: verified domains + managed stage domain.
	instances := []*models.Instance{inst}
	domainSet, appErr := s.listDomainsForInstances(ctx.Context(), instances)
	if appErr != nil {
		return nil
	}
	agentIDs, appErr := s.listAgentIDsForDomains(ctx.Context(), domainSet)
	if appErr != nil {
		return nil
	}
	sort.Strings(agentIDs)
	return agentIDs
}

func (s *Server) loadTrustFederation(ctx *apptheory.Context, inst *models.Instance) (portalTrustFederationResponse, *apptheory.AppTheoryError) {
	resp := portalTrustFederationResponse{
		Peers:  []portalTrustFederationPeerRow{},
		Source: trustFederationSource,
	}
	if s == nil || inst == nil {
		return resp, newAppTheoryError("app.internal", "internal error")
	}

	apiKey, keyErr := s.resolvePortalCostInstanceKey(ctx.Context(), inst)
	if keyErr != nil || strings.TrimSpace(apiKey) == "" {
		return resp, newAppTheoryError("app.internal", "failed to resolve instance federation access")
	}
	baseURL, urlErr := s.resolvePortalCostMetricsBaseURL(inst)
	if urlErr != nil {
		return resp, newAppTheoryError("app.internal", "failed to resolve instance federation endpoint")
	}

	client := s.portalManagedHTTPClient()

	// Probe statistics first. The current DTO does not expose the aggregate
	// active-users/messages counters, but this call verifies the managed admin
	// federation surface and future-proofs the same server-side access path.
	_ = s.fetchLesserFederationStatistics(ctx.Context(), client, baseURL, apiKey)

	instances, truncated, appErr := s.fetchLesserFederationInstances(ctx.Context(), client, baseURL, apiKey)
	if appErr != nil {
		return resp, appErr
	}
	return buildTrustFederationResponse(instances, time.Now().UTC(), truncated), nil
}

func (s *Server) fetchLesserFederationStatistics(ctx context.Context, client *http.Client, baseURL string, apiKey string) *apptheory.AppTheoryError {
	endpoint, err := buildManagedInstanceFederationURL(baseURL, "/api/v1/admin/federation/statistics", nil)
	if err != nil {
		return newAppTheoryError("app.internal", "failed to resolve federation statistics endpoint")
	}
	var decoded lesserFederationStatisticsResponse
	return decodeManagedLesserJSON(ctx, client, http.MethodGet, endpoint, apiKey, &decoded)
}

func (s *Server) fetchLesserFederationInstances(ctx context.Context, client *http.Client, baseURL string, apiKey string) ([]lesserFederationInstanceInfo, bool, *apptheory.AppTheoryError) {
	out := make([]lesserFederationInstanceInfo, 0)
	cursor := ""
	truncated := false

	for len(out) < trustFederationFetchCap {
		q := url.Values{}
		q.Set("limit", fmt.Sprintf("%d", trustFederationFetchLimit))
		if strings.TrimSpace(cursor) != "" {
			q.Set("cursor", strings.TrimSpace(cursor))
		}
		endpoint, err := buildManagedInstanceFederationURL(baseURL, "/api/v1/admin/federation/instances", q)
		if err != nil {
			return nil, false, newAppTheoryError("app.internal", "failed to resolve federation instances endpoint")
		}

		var page lesserFederationInstancesPage
		if appErr := decodeManagedLesserJSON(ctx, client, http.MethodGet, endpoint, apiKey, &page); appErr != nil {
			return nil, false, appErr
		}
		out = append(out, page.Instances...)
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" {
			return out, false, nil
		}
		cursor = strings.TrimSpace(*page.NextCursor)
	}
	truncated = strings.TrimSpace(cursor) != ""
	return out, truncated, nil
}

func buildManagedInstanceFederationURL(baseURL string, path string, q url.Values) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("base url is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(baseURL + path)
	if err != nil {
		return "", err
	}
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func decodeManagedLesserJSON(ctx context.Context, client *http.Client, method string, endpoint string, apiKey string, dest any) *apptheory.AppTheoryError {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return newAppTheoryError("app.internal", "failed to create managed Lesser request")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req) //nolint:gosec // endpoint is derived from managed instance metadata or an injected test seam.
	if err != nil {
		return newAppTheoryError("app.upstream_unavailable", "failed to reach managed Lesser")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return newAppTheoryError("app.upstream_error", "managed Lesser request failed")
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(dest); err != nil {
		return newAppTheoryError("app.upstream_error", "failed to decode managed Lesser response")
	}
	return nil
}

func buildTrustFederationResponse(instances []lesserFederationInstanceInfo, fetchedAt time.Time, truncated bool) portalTrustFederationResponse {
	resp := portalTrustFederationResponse{
		Peers:     []portalTrustFederationPeerRow{},
		Source:    trustFederationSource,
		Truncated: truncated,
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now().UTC()
	}

	seen := make(map[string]struct{}, len(instances))
	for _, info := range instances {
		domain := strings.ToLower(strings.TrimSpace(info.Domain))
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}

		status := trustFederationStatus(info, fetchedAt)
		switch status {
		case trustFederationStatusSevered:
			resp.Severed++
		case trustFederationStatusWarning:
			resp.Warning++
		default:
			resp.Reachable++
		}

		if len(resp.Peers) >= trustFederationPeerRowsMax {
			continue
		}
		row := portalTrustFederationPeerRow{
			Domain: domain,
			Status: status,
		}
		lastSeen := strings.TrimSpace(info.LastSeen)
		if lastSeen != "" {
			row.LastSeen = lastSeen
		} else {
			row.LastFetch = fetchedAt.UTC().Format(time.RFC3339)
		}
		resp.Peers = append(resp.Peers, row)
	}

	sort.Slice(resp.Peers, func(i, j int) bool {
		return resp.Peers[i].Domain < resp.Peers[j].Domain
	})
	return resp
}

func trustFederationStatus(info lesserFederationInstanceInfo, now time.Time) string {
	if info.IsSuspended {
		return trustFederationStatusSevered
	}
	if info.IsSilenced {
		return trustFederationStatusWarning
	}
	if info.TrustScore > 0 && info.TrustScore < trustFederationDegradedTrustThreshold {
		return trustFederationStatusWarning
	}
	lastSeen := strings.TrimSpace(info.LastSeen)
	if lastSeen != "" {
		if ts, err := time.Parse(time.RFC3339, lastSeen); err == nil && !ts.IsZero() && ts.Before(now.UTC().Add(-trustFederationStaleSeenHours*time.Hour)) {
			return trustFederationStatusWarning
		}
	}
	return trustFederationStatusReachable
}

// loadTrustQueueDepth records a best-effort, coalesced current queue-depth
// sample and then returns persisted instance-scoped samples in the 24-hour
// dashboard window.
func (s *Server) loadTrustQueueDepth(ctx context.Context, inst *models.Instance, agentIDs []string) portalTrustQueueDepthResponse {
	resp := newPortalTrustQueueDepthResponse()
	if !canLoadTrustQueueDepth(s, inst, agentIDs) {
		return resp
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	now := time.Now().UTC()
	if sample, err := s.recordTrustQueueDepthSnapshot(ctx, slug, agentIDs, now); err == nil && sample != nil {
		resp.Series = []portalTrustQueueDepthPoint{trustQueueDepthPointFromSample(sample)}
	}
	if points, err := s.loadPersistedTrustQueueDepthPoints(ctx, slug, now); err == nil && len(points) > 0 {
		resp.Series = points
	}
	return resp
}

func newPortalTrustQueueDepthResponse() portalTrustQueueDepthResponse {
	return portalTrustQueueDepthResponse{
		WindowHours: trustQueueDepthWindowHours,
		Source:      trustQueueDepthSource,
		Series:      []portalTrustQueueDepthPoint{},
	}
}

func canLoadTrustQueueDepth(s *Server, inst *models.Instance, agentIDs []string) bool {
	return s != nil && s.store != nil && s.store.DB != nil && inst != nil && strings.TrimSpace(inst.Slug) != "" && len(agentIDs) > 0
}

func (s *Server) loadPersistedTrustQueueDepthPoints(ctx context.Context, slug string, now time.Time) ([]portalTrustQueueDepthPoint, error) {
	var samples []*models.TrustQueueDepthSample
	err := s.store.DB.WithContext(ctx).
		Model(&models.TrustQueueDepthSample{}).
		Where("PK", "=", models.TrustQueueDepthSamplePK(slug)).
		Where("SK", "begins_with", "SAMPLE#").
		OrderBy("SK", "DESC").
		Limit(trustQueueDepthMaxSeriesPoints).
		All(&samples)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, err
	}
	return trustQueueDepthPointsFromSamples(samples, slug, now), nil
}

func trustQueueDepthPointsFromSamples(samples []*models.TrustQueueDepthSample, slug string, now time.Time) []portalTrustQueueDepthPoint {
	windowStart := now.Add(-trustQueueDepthWindowHours * time.Hour)
	points := make([]portalTrustQueueDepthPoint, 0, minInt(len(samples), trustQueueDepthMaxSeriesPoints))
	for _, sample := range samples {
		if !trustQueueDepthSampleInScope(sample, slug, windowStart) {
			continue
		}
		points = append(points, trustQueueDepthPointFromSample(sample))
		if len(points) >= trustQueueDepthMaxSeriesPoints {
			break
		}
	}
	sort.Slice(points, func(i, j int) bool {
		return points[i].Timestamp < points[j].Timestamp
	})
	return points
}

func trustQueueDepthSampleInScope(sample *models.TrustQueueDepthSample, slug string, windowStart time.Time) bool {
	return sample != nil && strings.ToLower(strings.TrimSpace(sample.InstanceSlug)) == slug && !sample.Timestamp.Before(windowStart)
}

func (s *Server) recordTrustQueueDepthSnapshot(ctx context.Context, instanceSlug string, agentIDs []string, now time.Time) (*models.TrustQueueDepthSample, error) {
	sample := &models.TrustQueueDepthSample{
		InstanceSlug: strings.ToLower(strings.TrimSpace(instanceSlug)),
		Timestamp:    now,
		Source:       trustQueueDepthSource,
	}
	if err := sample.BeforeCreate(); err != nil {
		return sample, err
	}
	existing, exists, err := s.loadTrustQueueDepthSampleBucket(ctx, sample)
	if err != nil {
		return nil, err
	}
	if exists {
		return existing, nil
	}

	depth := 0
	for _, agentID := range uniqueSortedStrings(agentIDs) {
		if agentID == "" {
			continue
		}
		count, err := s.store.DB.WithContext(ctx).
			Model(&models.SoulCommMailboxMessage{}).
			Where("PK", "=", models.SoulCommMailboxAgentPK(instanceSlug, agentID)).
			Filter("direction", "=", models.SoulCommDirectionInbound).
			Filter("status", "=", models.SoulCommMailboxStatusQueued).
			Filter("deleted", "=", false).
			Count()
		if err != nil && !theoryErrors.IsNotFound(err) {
			continue
		}
		if count > 0 {
			depth += int(count)
		}
	}

	sample.Depth = depth
	if err := s.store.DB.WithContext(ctx).Model(sample).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, nil
		}
		return sample, err
	}
	return sample, nil
}

func (s *Server) loadTrustQueueDepthSampleBucket(ctx context.Context, sample *models.TrustQueueDepthSample) (*models.TrustQueueDepthSample, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil || sample == nil {
		return nil, false, nil
	}
	var existing models.TrustQueueDepthSample
	err := s.store.DB.WithContext(ctx).
		Model(&models.TrustQueueDepthSample{}).
		Where("PK", "=", sample.PK).
		Where("SK", "=", sample.SK).
		First(&existing)
	if err != nil {
		if theoryErrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &existing, true, nil
}

func trustQueueDepthPointFromSample(sample *models.TrustQueueDepthSample) portalTrustQueueDepthPoint {
	if sample == nil {
		return portalTrustQueueDepthPoint{}
	}
	return portalTrustQueueDepthPoint{
		Timestamp: sample.Timestamp.UTC().Format(time.RFC3339),
		Depth:     sample.Depth,
	}
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func boundedTrustAgentIDs(agentIDs []string) ([]string, bool) {
	out := uniqueSortedStrings(agentIDs)
	if len(out) <= trustDataMaxAgentFanout {
		return out, false
	}
	return out[:trustDataMaxAgentFanout], true
}

// loadTrustSignatures queries SoulAgentFailure rows for the given agent IDs,
// filters to signature_failure types within the 24-hour window, and returns
// total and per-agent breakdown counts plus a redacted hourly time series. The
// storage query is constrained by the FAILURE#{RFC3339Nano} sort-key range and
// defensive row caps before the type post-filter runs.
func (s *Server) loadTrustSignatures(ctx context.Context, agentIDs []string) portalTrustSignaturesResponse {
	now := time.Now().UTC()
	windowStart := now.Add(-trustSignaturesWindowHours * time.Hour)
	agentIDs, agentFanoutTruncated := boundedTrustAgentIDs(agentIDs)

	resp := portalTrustSignaturesResponse{
		WindowHours: trustSignaturesWindowHours,
		BySource:    []portalTrustSignaturesSourceRow{},
		Series:      []portalTrustSignatureSeriesPoint{},
		Truncated:   agentFanoutTruncated,
	}

	bySource := make(map[string]int) // agentID → failure count
	byBucket := make(map[time.Time]int)
	totalFailures := 0
	rowsRead := 0

	for _, agentID := range agentIDs {
		remainingRows := trustSignaturesMaxRowsPerRequest - rowsRead
		if remainingRows <= 0 {
			resp.Truncated = true
			break
		}
		limit := minInt(trustSignaturesMaxRowsPerAgent, remainingRows)
		pk := fmt.Sprintf("SOUL#AGENT#%s", agentID)
		var failures []*models.SoulAgentFailure
		err := s.store.DB.WithContext(ctx).
			Model(&models.SoulAgentFailure{}).
			Where("PK", "=", pk).
			Where("SK", "BETWEEN", []any{
				signatureFailureSortKeyLowerBound(windowStart),
				signatureFailureSortKeyUpperBound(now),
			}).
			OrderBy("SK", "ASC").
			Limit(limit).
			All(&failures)
		if err != nil && !theoryErrors.IsNotFound(err) {
			continue // degrade gracefully on read errors
		}
		rowsRead += len(failures)
		if len(failures) >= limit || rowsRead >= trustSignaturesMaxRowsPerRequest {
			resp.Truncated = true
		}

		for _, f := range failures {
			if f == nil {
				continue
			}
			// Only count signature failures within the window.
			if strings.ToLower(strings.TrimSpace(f.FailureType)) != signatureFailureType {
				continue
			}
			if signatureFailureTimestampOutOfWindow(f.Timestamp, windowStart, now) {
				continue
			}
			totalFailures++
			bySource[agentID]++
			bucket := signatureFailureHourBucket(f.Timestamp)
			byBucket[bucket]++
		}
	}

	resp.TotalFailures = totalFailures
	resp.BySource = sortedSourceRows(bySource, trustSignaturesMaxSources)
	resp.Series = sortedSignatureSeries(byBucket)
	return resp
}

func signatureFailureSortKeyLowerBound(ts time.Time) string {
	return fmt.Sprintf("FAILURE#%s", ts.UTC().Format(time.RFC3339Nano))
}

func signatureFailureSortKeyUpperBound(ts time.Time) string {
	return fmt.Sprintf("FAILURE#%s~", ts.UTC().Format(time.RFC3339Nano))
}

func signatureFailureTimestampOutOfWindow(ts time.Time, windowStart time.Time, now time.Time) bool {
	return ts.IsZero() || ts.Before(windowStart) || ts.After(now)
}

func signatureFailureHourBucket(ts time.Time) time.Time {
	return ts.UTC().Truncate(time.Hour)
}

func sortedSignatureSeries(byBucket map[time.Time]int) []portalTrustSignatureSeriesPoint {
	buckets := make([]time.Time, 0, len(byBucket))
	for bucket, failures := range byBucket {
		if failures > 0 {
			buckets = append(buckets, bucket.UTC())
		}
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Before(buckets[j])
	})

	points := make([]portalTrustSignatureSeriesPoint, 0, len(buckets))
	for _, bucket := range buckets {
		failures := byBucket[bucket]
		if failures <= 0 {
			continue
		}
		points = append(points, portalTrustSignatureSeriesPoint{
			Timestamp: bucket.UTC().Format(time.RFC3339),
			Failures:  failures,
		})
	}
	return points
}

// sortedSourceRows returns a bounded, descending-sorted slice of source rows.
func sortedSourceRows(bySource map[string]int, maxRows int) []portalTrustSignaturesSourceRow {
	type entry struct {
		source   string
		failures int
	}
	entries := make([]entry, 0, len(bySource))
	for source, count := range bySource {
		entries = append(entries, entry{source, count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].failures > entries[j].failures ||
			(entries[i].failures == entries[j].failures && entries[i].source < entries[j].source)
	})
	limit := len(entries)
	if limit > maxRows {
		limit = maxRows
	}
	rows := make([]portalTrustSignaturesSourceRow, limit)
	for i := 0; i < limit; i++ {
		rows[i] = portalTrustSignaturesSourceRow{
			Source:   entries[i].source,
			Failures: entries[i].failures,
		}
	}
	return rows
}

// loadTrustScore computes an aggregate trust score from SoulAgentReputation
// rows for the given agent IDs. Score is the average Composite across agents;
// dimensions are the average of each mapped field. When no reputation rows
// exist, returns documented zero/empty semantics.
func (s *Server) loadTrustScore(ctx context.Context, agentIDs []string) portalTrustScoreResponse {
	resp := portalTrustScoreResponse{
		Formula: trustScoreFormula,
		Source:  trustScoreSource,
	}

	var (
		totalComposite   float64
		totalOperational float64 // ← Trust
		totalAttestation float64 // ← Validation
		totalSocial      float64 // ← Social
		totalEconomic    float64 // ← Economic
		totalIntegrity   float64 // ← Integrity
		agentCount       int
	)

	for _, agentID := range agentIDs {
		rep, err := s.getSoulAgentReputation(ctx, agentID)
		if theoryErrors.IsNotFound(err) || rep == nil {
			continue
		}
		if err != nil {
			continue // degrade on read error
		}
		agentCount++
		totalComposite += rep.Composite
		totalOperational += rep.Trust
		totalAttestation += rep.Validation
		totalSocial += rep.Social
		totalEconomic += rep.Economic
		totalIntegrity += rep.Integrity
	}

	if agentCount == 0 {
		return resp
	}

	n := float64(agentCount)
	resp.Score = totalComposite / n
	resp.Dimensions = portalTrustScoreDimensions{
		Operational: totalOperational / n,
		Attestation: totalAttestation / n,
		Social:      totalSocial / n,
		Economic:    totalEconomic / n,
		Integrity:   totalIntegrity / n,
	}

	return resp
}

// loadTrustVouches queries SoulAgentPeerEndorsement rows for the given agent
// IDs, returning bounded peer/strength pairs with provenance redacted. Count
// uses Count() instead of loading all endorsement rows; Items are built from
// bounded per-agent OrderBy(SK DESC)+Limit reads and a request-level row cap.
func (s *Server) loadTrustVouches(ctx context.Context, agentIDs []string) portalTrustVouchesResponse {
	agentIDs, agentFanoutTruncated := boundedTrustAgentIDs(agentIDs)
	resp := portalTrustVouchesResponse{
		Items:     []portalTrustVouchItem{},
		Truncated: agentFanoutTruncated,
	}

	var all []portalTrustVouchEntry
	rowsRead := 0

	for _, agentID := range agentIDs {
		remainingRows := trustVouchesMaxRowsPerRequest - rowsRead
		if remainingRows <= 0 {
			resp.Truncated = true
			break
		}
		limit := minInt(trustVouchesMaxRowsPerAgent, remainingRows)
		pk := fmt.Sprintf("SOUL#AGENT#%s", agentID)
		endorsements, count, truncated, err := s.loadBoundedTrustVouchesForAgent(ctx, pk, limit)
		if err != nil && !theoryErrors.IsNotFound(err) {
			continue // degrade on read error
		}
		rowsRead += len(endorsements)
		resp.Count += count
		if truncated || rowsRead >= trustVouchesMaxRowsPerRequest {
			resp.Truncated = true
		}
		all = appendTrustVouchEntries(all, endorsements)
	}

	// Stable sort by created_at descending.
	sort.Slice(all, func(i, j int) bool {
		return all[i].createdAt.After(all[j].createdAt)
	})

	limit := len(all)
	if limit > trustVouchesMaxItems {
		limit = trustVouchesMaxItems
	}

	items := make([]portalTrustVouchItem, limit)
	for i := 0; i < limit; i++ {
		items[i] = portalTrustVouchItem{
			Peer:      all[i].peer,
			Strength:  vouchDefaultStrength,
			Type:      "endorsement",
			CreatedAt: all[i].createdAt.UTC().Format(time.RFC3339),
		}
	}
	resp.Items = items

	return resp
}

type portalTrustVouchEntry struct {
	peer      string
	createdAt time.Time
}

func (s *Server) loadBoundedTrustVouchesForAgent(ctx context.Context, pk string, limit int) ([]*models.SoulAgentPeerEndorsement, int, bool, error) {
	count, countErr := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentPeerEndorsement{}).
		Where("PK", "=", pk).
		Where("SK", "begins_with", "ENDORSEMENT#").
		Count()

	var endorsements []*models.SoulAgentPeerEndorsement
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentPeerEndorsement{}).
		Where("PK", "=", pk).
		Where("SK", "begins_with", "ENDORSEMENT#").
		OrderBy("SK", "DESC").
		Limit(limit).
		All(&endorsements)
	if err != nil {
		return nil, 0, false, err
	}

	countOut := len(endorsements)
	truncated := len(endorsements) >= limit
	if countErr == nil && count > int64(countOut) {
		countOut = int(count)
		truncated = true
	}
	return endorsements, countOut, truncated, nil
}

func appendTrustVouchEntries(out []portalTrustVouchEntry, endorsements []*models.SoulAgentPeerEndorsement) []portalTrustVouchEntry {
	for _, e := range endorsements {
		if e == nil {
			continue
		}
		out = append(out, portalTrustVouchEntry{
			peer:      strings.TrimSpace(e.EndorserAgentID),
			createdAt: e.CreatedAt,
		})
	}
	return out
}
