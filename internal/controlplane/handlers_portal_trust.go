package controlplane

import (
	"net/http"

	apptheory "github.com/theory-cloud/apptheory/runtime"
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
	// window. When host-side signature-failure tracking is not yet
	// instrumented, counters are zero.
	Signatures portalTrustSignaturesResponse `json:"signatures"`

	// QueueDepth contains an inbound queue depth time series. When host-side
	// queue-depth telemetry is not yet persisted, the series is empty.
	QueueDepth portalTrustQueueDepthResponse `json:"queue_depth"`

	// TrustScore exposes a computed trust score with documented formula and
	// dimension breakdown. When the backing reputation store does not yet
	// contain per-instance aggregates, the score is returned as a documented
	// placeholder (score = 0, formula = explicit string, dimensions = empty).
	TrustScore portalTrustScoreResponse `json:"trust_score"`

	// Vouches exposes per-peer vouches (peer/strength pairs) with sensitive
	// provenance redacted. When host-side vouch aggregation is not yet
	// instrumented, the list is empty.
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
// dashboard window, scoped to sources owned by the requesting instance.
//
// Data source (planned):
//   - Managed Lesser instances log signature-failure events via the soul
//     agent failure recording API (SoulAgentFailure with
//     FailureType="signature_failure"). Host-side aggregation will sum
//     failures over the window.
//
// Current status: host-side signature-failure aggregation is not yet
// instrumented. Counters are zero and per-source breakdown is empty.
type portalTrustSignaturesResponse struct {
	// WindowHours is the lookback window in hours (default 168 = 7 days).
	WindowHours int `json:"window_hours"`

	// TotalFailures is the total signature-failure count in the window.
	TotalFailures int `json:"total_failures"`

	// BySource is an optional per-source breakdown (max 50 entries).
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

// portalTrustScoreResponse exposes a computed trust score with clear
// semantics, dimensions, and formula documentation.
//
// Formula (documented):
//
//	trust_score = sum(weight_i * dimension_i) / sum(weight_i)
//	Default weights: all dimensions equal weight (1.0).
//	Scoring window: trailing 30 days where data exists.
//
// Dimensions:
//   - operational: derived from instance status, uptime, provision health
//   - attestation: derived from attestation coverage and recency
//   - social: derived from peer endorsements and relationship graph metrics
//   - economic: derived from tip flow and budget health
//   - integrity: derived from failure/recovery ratio and dispute history
//
// Current status: the dimensions are documented but scores are returned as
// zero placeholders. The backing SoulAgentReputation store contains per-agent
// dimension scores (Trust, Social, Economic, Integrity, etc.) but the
// instance→agent index required for per-instance aggregation is not yet
// materialized.
type portalTrustScoreResponse struct {
	// Score is the composite trust score (0.0–100.0). Zero means no score
	// has been computed yet.
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
// Data source (planned):
//   - SoulAgentPeerEndorsement and SoulAgentRelationship records (type
//     endorsement or trust_grant) aggregated per instance.
//
// Current status: host-side vouch aggregation is not yet instrumented. The
// items list is empty.
type portalTrustVouchesResponse struct {
	// Items is a bounded list of vouch entries (max 50).
	Items []portalTrustVouchItem `json:"items"`

	// Count is the total number of vouches available (may exceed Items length).
	Count int `json:"count"`
}

// portalTrustVouchItem is a single vouch entry. Sensitive provenance fields
// (raw signatures, internal PK/SK, account IDs) are never included.
type portalTrustVouchItem struct {
	// Peer is the peer identifier (agent ID or domain).
	Peer string `json:"peer"`

	// Strength is the vouch strength (0.0–1.0).
	Strength float64 `json:"strength"`

	// Type is the vouch relationship type (e.g. endorsement, trust_grant).
	Type string `json:"type,omitempty"`

	// CreatedAt is when the vouch was recorded (ISO 8601 UTC).
	CreatedAt string `json:"created_at,omitempty"`
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
		InstanceSlug: inst.Slug,
		Federation: portalTrustFederationResponse{
			Reachable: 0,
			Warning:   0,
			Severed:   0,
			Peers:     []portalTrustFederationPeerRow{},
		},
		Signatures: portalTrustSignaturesResponse{
			WindowHours:   168, // 7 days
			TotalFailures: 0,
			BySource:      []portalTrustSignaturesSourceRow{},
		},
		QueueDepth: portalTrustQueueDepthResponse{
			Series: []portalTrustQueueDepthPoint{},
		},
		TrustScore: portalTrustScoreResponse{
			Score:   0,
			Formula: "trust_score = sum(weight_i * dimension_i) / sum(weight_i); weights: operational=1.0, attestation=1.0, social=1.0, economic=1.0, integrity=1.0; window: trailing 30 days",
			Dimensions: portalTrustScoreDimensions{
				Operational: 0,
				Attestation: 0,
				Social:      0,
				Economic:    0,
				Integrity:   0,
			},
			Source: "preliminary: per-agent SoulAgentReputation scores exist but per-instance aggregation index is not yet materialized",
		},
		Vouches: portalTrustVouchesResponse{
			Items: []portalTrustVouchItem{},
			Count: 0,
		},
	}

	return apptheory.JSON(http.StatusOK, resp)
}
