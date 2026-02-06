import { fetchJson, jsonRequest } from './http';

export interface BudgetMonthResponse {
	instance_slug: string;
	month: string;
	included_credits: number;
	used_credits: number;
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
	cache_hit_rate: number;
	list_credits: number;
	requested_credits: number;
	debited_credits: number;
	discount_credits: number;
	included_credits?: number;
	used_credits?: number;
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

