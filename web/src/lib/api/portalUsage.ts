import { fetchJson, jsonRequest } from './http';

export interface BudgetMonthResponse {
	instance_slug: string;
	month: string;
	included_credits: number;
	used_credits: number;
	/** Server-computed remaining credits (M1.12 — lands with PR #506). Computed client-side as fallback when absent. */
	remaining_credits?: number;
	updated_at?: string;
}

export interface ListBudgetsResponse {
	budgets: BudgetMonthResponse[];
	count: number;
}

export interface UsageLedgerEntry {
	id: string;
	instance_slug: string;
	month: string;
	module: string;
	target?: string;
	cached: boolean;
	reason?: string;
	request_id?: string;
	requested_credits: number;
	list_credits?: number;
	pricing_multiplier_bps?: number;
	debited_credits: number;
	included_debited_credits: number;
	overage_debited_credits: number;
	billing_type: string;
	actor_uri?: string;
	object_uri?: string;
	content_hash?: string;
	links_hash?: string;
	created_at: string;
}

export interface ListUsageResponse {
	entries: UsageLedgerEntry[];
	count: number;
}

export interface UsageSummaryResponse {
	instance_slug: string;
	month: string;
	requests: number;
	cache_hits: number;
	cache_misses: number;
	/**
	 * Cache hit ratio as a 0.0–1.0 float (NOT a 0–100 percentage).
	 * Server computes `cacheHits / totalRequests`; see
	 * `internal/controlplane/handlers_portal_instances.go` `cacheHitRate`.
	 * Format with `formatPercent` (multiplies by 100) at the call site.
	 */
	cache_hit_rate: number;
	list_credits: number;
	requested_credits: number;
	debited_credits: number;
	discount_credits: number;
	included_credits?: number;
	used_credits?: number;
	/** Server-computed remaining credits (M1.12 — lands with PR #506). Computed client-side as fallback when absent. */
	remaining_credits?: number;
}

export function portalListBudgets(token: string, slug: string): Promise<ListBudgetsResponse> {
	return fetchJson<ListBudgetsResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/budgets`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalGetBudgetMonth(token: string, slug: string, month: string): Promise<BudgetMonthResponse> {
	return fetchJson<BudgetMonthResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/budgets/${month}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalSetBudgetMonth(
	token: string,
	slug: string,
	month: string,
	includedCredits: number,
): Promise<BudgetMonthResponse> {
	const req = jsonRequest({ included_credits: includedCredits });
	return fetchJson<BudgetMonthResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/budgets/${month}`, {
		method: 'PUT',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function portalListUsage(token: string, slug: string, month: string): Promise<ListUsageResponse> {
	return fetchJson<ListUsageResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/usage/${month}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalGetUsageSummary(token: string, slug: string, month: string): Promise<UsageSummaryResponse> {
	return fetchJson<UsageSummaryResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/usage/${month}/summary`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

// ---------------------------------------------------------------------------
// Cost telemetry (M3.11+) — GET /api/v1/portal/instances/{slug}/cost
// ---------------------------------------------------------------------------

export interface CostAttributionMetric {
	service: string;
	metric_name: string;
	stat: string;
	unit: string;
	value: number;
}

export interface CostServiceEntry {
	date: string;
	service: string;
	cost: number;
	currency: string;
	metrics?: CostAttributionMetric[];
}

export interface CostDayEntry {
	date: string;
	day_cost: number;
	currency: string;
	entries: CostServiceEntry[];
}

export interface PortalCostResponse {
	instance_slug: string;
	from_date: string;
	to_date: string;
	days: CostDayEntry[];
	count: number;
	total_cost: number;
	currency: string;
}

// ---------------------------------------------------------------------------
// Instance activity (M11 corrective) — GET /api/v1/portal/instances/{slug}/activity
// ---------------------------------------------------------------------------

export interface PortalInstanceActivityResponse {
	instance_slug: string;
	/** Sum of weekly statuses from /api/v1/instance/activity filtered to current month. */
	statuses: number;
	/** Number of activity weeks that fell within the current month. */
	weeks: number;
	month: string;
}

/**
 * Fetch managed Lesser instance activity (weekly statuses) via the
 * owner-scoped read-only instance-auth bridge. Used as the post/status
 * denominator for the "Per federated post" fleet billing metric.
 */
export function portalGetInstanceActivity(
	token: string,
	slug: string,
): Promise<PortalInstanceActivityResponse> {
	return fetchJson<PortalInstanceActivityResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/activity`,
		{
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

// ---------------------------------------------------------------------------

/**
 * Fetch per-instance real-time cost telemetry for the given date window.
 * Defaults to the past 30 days when from/to are omitted.
 */
export function portalGetInstanceCost(
	token: string,
	slug: string,
	from?: string,
	to?: string,
): Promise<PortalCostResponse> {
	const params = new URLSearchParams();
	if (from) params.set('from', from);
	if (to) params.set('to', to);
	const qs = params.toString();
	return fetchJson<PortalCostResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/cost${qs ? `?${qs}` : ''}`,
		{
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}
