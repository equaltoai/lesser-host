package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// portaltTrustDataResponse is the top-level response for the portal trust-data
// endpoint. All five categories are populated even when backing telemetry does
// not yet exist; in those cases the fields carry safe zero/empty/null values
// with documented semantics.
type portalTrustDataResponse struct {
	InstanceSlug string `json:"instance_slug"`

	// Federation contains federation-peer health counters and (optionally)
	// a bounded set of peer rows. When host-side federation telemetry is not
	// yet instrumented, counters are zero and the peers list is empty.
	Federation portalTrustFederationResponse `json:"federation"`

	// Signatures contains signature-failure counters scoped to the dashboard
	// window. Populated from SoulAgentFailure rows when soul registry is
	// configured and agents are bound to the requesting instance.
	Signatures portalTrustSignaturesResponse `json:"signatures"`

	// QueueDepth contains an inbound queue depth time series. When host-side
	// queue-depth telemetry is not yet persisted, the series is empty.
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
// Data source (planned):
//   - Managed Lesser instances expose federation health via
//     /api/v1/admin/federation/*. Host-side aggregation (worker or
//     polling-based) will persist roll-up counters and bounded peer rows.
//
// Current status: host-side federation telemetry is not yet instrumented.
// Counters are zero and the peers list is empty.
type portalTrustFederationResponse struct {
	// Reachable is the count of federation peers currently reachable.
	Reachable int `json:"reachable"`

	// Warning is the count of peers with degraded reachability.
	Warning int `json:"warning"`

	// Severed is the count of peers whose federation link has been severed.
	Severed int `json:"severed"`

	// Peers is an optional bounded list of peer rows (max 50). Each row
	// includes the peer domain and its status.
	Peers []portalTrustFederationPeerRow `json:"peers"`
}

// portalTrustFederationPeerRow is a single federation peer row. Sensitive
// fields (hosting account IDs, raw connection credentials) are never included.
type portalTrustFederationPeerRow struct {
	Domain string `json:"domain"`
	Status string `json:"status"` // reachable, warning, severed
}

// portalTrustSignaturesResponse contains signature-failure counters over the
// dashboard window, scoped to agents bound to the requesting instance.
//
// Data source: SoulAgentFailure rows (FailureType = "signature_failure")
// queried per resolved agent, filtered to the 168-hour dashboard window.
type portalTrustSignaturesResponse struct {
	// WindowHours is the lookback window in hours (default 168 = 7 days).
	WindowHours int `json:"window_hours"`

	// TotalFailures is the total signature-failure count in the window.
	TotalFailures int `json:"total_failures"`

	// BySource is a bounded per-agent breakdown (max 50 entries).
	BySource []portalTrustSignaturesSourceRow `json:"by_source"`
}

// portalTrustSignaturesSourceRow is a per-source signature-failure count.
type portalTrustSignaturesSourceRow struct {
	Source   string `json:"source"`
	Failures int    `json:"failures"`
}

// portalTrustQueueDepthResponse contains an inbound queue depth time series
// for the customer-owned instance(s).
//
// Data source (planned):
//   - SQS queue depth metrics (ApproximateNumberOfMessages) collected
//     periodically and stored in a host-side time-series model.
//
// Current status: host-side queue-depth time series is not yet persisted.
// The series is empty.
type portalTrustQueueDepthResponse struct {
	// Series contains time-series data points (max 168 hourly points = 7 days).
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
// Each dimension is averaged across agents that have non-zero values.
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
}

// portalTrustVouchItem is a single vouch entry. Sensitive provenance fields
// (raw signatures, internal PK/SK, account IDs) are never included.
type portalTrustVouchItem struct {
	// Peer is the peer identifier (endorser agent ID).
	Peer string `json:"peer"`

	// Strength is the vouch strength (0.0–1.0). Default 1.0 for endorsements
	// since SoulAgentPeerEndorsement has no numeric strength field.
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

// trustSignaturesWindowHours is the dashboard lookback window (7 days).
const trustSignaturesWindowHours = 168

// trustVouchesMaxItems caps the number of vouch items returned.
const trustVouchesMaxItems = 50

// trustSignaturesMaxSources caps the number of by-source rows returned.
const trustSignaturesMaxSources = 50

// signatureFailureType is the normalized SoulAgentFailure.FailureType value
// for signature failures. The model normalises FailureType to lowercase.
const signatureFailureType = "signature_failure"

// vouchDefaultStrength is the documented default vouch strength (1.0) when the
// backing model has no numeric strength field.
const vouchDefaultStrength = 1.0

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
		InstanceSlug: inst.Slug,
		Federation: portalTrustFederationResponse{
			Reachable: 0,
			Warning:   0,
			Severed:   0,
			Peers:     []portalTrustFederationPeerRow{},
		},
		Signatures: portalTrustSignaturesResponse{
			WindowHours:   trustSignaturesWindowHours,
			TotalFailures: 0,
			BySource:      []portalTrustSignaturesSourceRow{},
		},
		QueueDepth: portalTrustQueueDepthResponse{
			Series: []portalTrustQueueDepthPoint{},
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

	// Populate soul-backed data when the soul registry is configured.
	if s.cfg.SoulEnabled {
		agentIDs := s.resolveAgentIDsForInstance(ctx, inst)
		if len(agentIDs) > 0 {
			resp.Signatures = s.loadTrustSignatures(ctx.Context(), agentIDs)
			resp.TrustScore = s.loadTrustScore(ctx.Context(), agentIDs)
			resp.Vouches = s.loadTrustVouches(ctx.Context(), agentIDs)
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

// loadTrustSignatures queries SoulAgentFailure rows for the given agent IDs,
// filters to signature_failure types within the 168-hour window, and returns
// total and per-agent breakdown counts.
func (s *Server) loadTrustSignatures(ctx context.Context, agentIDs []string) portalTrustSignaturesResponse {
	windowStart := time.Now().UTC().Add(-trustSignaturesWindowHours * time.Hour)

	resp := portalTrustSignaturesResponse{
		WindowHours: trustSignaturesWindowHours,
	}

	bySource := make(map[string]int) // agentID → failure count
	totalFailures := 0

	for _, agentID := range agentIDs {
		pk := fmt.Sprintf("SOUL#AGENT#%s", agentID)
		var failures []*models.SoulAgentFailure
		err := s.store.DB.WithContext(ctx).
			Model(&models.SoulAgentFailure{}).
			Where("PK", "=", pk).
			Where("SK", "begins_with", "FAILURE#").
			All(&failures)
		if err != nil && !theoryErrors.IsNotFound(err) {
			continue // degrade gracefully on read errors
		}

		for _, f := range failures {
			if f == nil {
				continue
			}
			// Only count signature failures within the window.
			if strings.ToLower(strings.TrimSpace(f.FailureType)) != signatureFailureType {
				continue
			}
			if f.Timestamp.Before(windowStart) {
				continue
			}
			totalFailures++
			bySource[agentID]++
		}
	}

	resp.TotalFailures = totalFailures
	resp.BySource = sortedSourceRows(bySource, trustSignaturesMaxSources)
	return resp
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
// reflects total available endorsements even when items are bounded.
func (s *Server) loadTrustVouches(ctx context.Context, agentIDs []string) portalTrustVouchesResponse {
	resp := portalTrustVouchesResponse{}

	type vouchEntry struct {
		peer      string
		createdAt time.Time
	}
	var all []vouchEntry

	for _, agentID := range agentIDs {
		pk := fmt.Sprintf("SOUL#AGENT#%s", agentID)
		var endorsements []*models.SoulAgentPeerEndorsement
		err := s.store.DB.WithContext(ctx).
			Model(&models.SoulAgentPeerEndorsement{}).
			Where("PK", "=", pk).
			Where("SK", "begins_with", "ENDORSEMENT#").
			All(&endorsements)
		if err != nil && !theoryErrors.IsNotFound(err) {
			continue // degrade on read error
		}

		for _, e := range endorsements {
			if e == nil {
				continue
			}
			all = append(all, vouchEntry{
				peer:      strings.TrimSpace(e.EndorserAgentID),
				createdAt: e.CreatedAt,
			})
		}
	}

	resp.Count = len(all)

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
