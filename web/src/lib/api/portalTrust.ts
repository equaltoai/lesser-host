/**
 * Portal trust-data API client for the M15 Trust UI dashboard.
 *
 * Consumes the M16 owner-scoped endpoint:
 *   GET /api/v1/portal/instances/{slug}/trust/data
 *
 * @license AGPL-3.0-only
 */

import { fetchJson } from './http';

/** Federation peer status values. */
export type PeerStatus = 'reachable' | 'warning' | 'severed';

/** A single federation peer row (domain + status + redacted timing). */
export interface TrustFederationPeerRow {
	domain: string;
	status: PeerStatus;
	/** Lesser admin federation last_seen timestamp, when provided. */
	last_seen?: string;
	/** Host fetch timestamp when Lesser has no last_seen value. */
	last_fetch?: string;
	/** Nullable/omitted until a real follower-count source exists. */
	follower_count?: number | null;
}

/** Federation health counters and optional peer list. */
export interface TrustFederationResponse {
	reachable: number;
	warning: number;
	severed: number;
	peers: TrustFederationPeerRow[];
	source?: string;
	truncated?: boolean;
}

/** Per-bound-agent signature failure count. */
export interface TrustSignaturesSourceRow {
	/** Bound soul agent ID, not a remote federation peer. */
	source: string;
	failures: number;
}

/** A single signature-failure time-series point. */
export interface TrustSignatureSeriesPoint {
	timestamp: string; // ISO 8601 UTC hour bucket
	failures: number;
}

/** Signature-failure counters scoped to the dashboard window. */
export interface TrustSignaturesResponse {
	window_hours: number;
	total_failures: number;
	by_source: TrustSignaturesSourceRow[];
	series: TrustSignatureSeriesPoint[];
}

/** A single queue depth time-series point. */
export interface TrustQueueDepthPoint {
	timestamp: string; // ISO 8601 UTC
	depth: number;
}

/** Inbound queue depth time series. */
export interface TrustQueueDepthResponse {
	window_hours?: number;
	source?: string;
	series: TrustQueueDepthPoint[];
}

/** Per-dimension trust score breakdown. */
export interface TrustScoreDimensions {
	operational: number;
	attestation: number;
	social: number;
	economic: number;
	integrity: number;
}

/** Computed trust score with formula and dimension breakdown. */
export interface TrustScoreResponse {
	score: number; // 0.0–100.0
	formula: string;
	dimensions: TrustScoreDimensions;
	source: string;
}

/** A single vouch entry (peer endorsement). */
export interface TrustVouchItem {
	/** Endorser agent ID. */
	peer: string;
	/**
	 * Fixed presence marker (1.0). The backing model has no numeric strength
	 * field; M15 renders vouches as list/count, not comparative strength bars.
	 */
	strength: number;
	type?: string; // "endorsement"
	created_at?: string; // ISO 8601 UTC
}

/** Per-peer vouches with count. */
export interface TrustVouchesResponse {
	items: TrustVouchItem[];
	/** Total endorsements (may exceed items length when capped). */
	count: number;
}

/** Top-level trust data response from the M16 endpoint. */
export interface PortalTrustDataResponse {
	instance_slug: string;
	federation: TrustFederationResponse;
	signatures: TrustSignaturesResponse;
	queue_depth: TrustQueueDepthResponse;
	trust_score: TrustScoreResponse;
	vouches: TrustVouchesResponse;
}

/**
 * Fetch per-instance trust data from the M16 owner-scoped endpoint.
 * Requires customer authentication and instance ownership.
 */
export function portalGetTrustData(
	token: string,
	slug: string,
): Promise<PortalTrustDataResponse> {
	return fetchJson<PortalTrustDataResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/trust/data`,
		{ headers: { authorization: `Bearer ${token}` } },
	);
}
