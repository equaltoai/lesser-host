import { fetchJson, jsonRequest } from './http';

export interface OperatorProvisionJobListItem {
	id: string;
	instance_slug: string;
	status: string;
	step?: string;
	note?: string;
	run_id?: string;
	attempts: number;
	max_attempts?: number;
	error_code?: string;
	error_message?: string;
	request_id?: string;
	has_receipt: boolean;
	created_at: string;
	updated_at: string;
}

export interface OperatorProvisionJobDetail extends OperatorProvisionJobListItem {
	mode?: string;
	plan?: string;
	region?: string;
	stage?: string;
	lesser_version?: string;
	account_request_id?: string;
	account_id?: string;
	account_email?: string;
	parent_hosted_zone_id?: string;
	base_domain?: string;
	child_hosted_zone_id?: string;
	child_name_servers?: string[];
	receipt_json?: string;
}

export interface ListOperatorProvisionJobsResponse {
	jobs: OperatorProvisionJobListItem[];
	count: number;
}

export function listOperatorProvisionJobs(
	token: string,
	input?: { status?: string; instance_slug?: string; limit?: number },
): Promise<ListOperatorProvisionJobsResponse> {
	const qs = new URLSearchParams();
	if (input?.status) qs.set('status', input.status);
	if (input?.instance_slug) qs.set('instance_slug', input.instance_slug);
	if (input?.limit) qs.set('limit', String(input.limit));
	const url = qs.toString() ? `/api/v1/operators/provisioning/jobs?${qs.toString()}` : '/api/v1/operators/provisioning/jobs';

	return fetchJson<ListOperatorProvisionJobsResponse>(url, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function getOperatorProvisionJob(token: string, id: string): Promise<OperatorProvisionJobDetail> {
	return fetchJson<OperatorProvisionJobDetail>(`/api/v1/operators/provisioning/jobs/${encodeURIComponent(id)}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function retryOperatorProvisionJob(token: string, id: string): Promise<OperatorProvisionJobDetail> {
	return fetchJson<OperatorProvisionJobDetail>(`/api/v1/operators/provisioning/jobs/${encodeURIComponent(id)}/retry`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function adoptOperatorProvisionJobAccount(
	token: string,
	id: string,
	input: { account_id: string; account_email?: string; note?: string },
): Promise<OperatorProvisionJobDetail> {
	const req = jsonRequest(input);
	return fetchJson<OperatorProvisionJobDetail>(`/api/v1/operators/provisioning/jobs/${encodeURIComponent(id)}/adopt`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function appendOperatorProvisionJobNote(
	token: string,
	id: string,
	note: string,
): Promise<OperatorProvisionJobDetail> {
	const req = jsonRequest({ note });
	return fetchJson<OperatorProvisionJobDetail>(`/api/v1/operators/provisioning/jobs/${encodeURIComponent(id)}/note`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

/* ──────────────────────────────────────────────────────────────────────────
 * MCP-drift fleet aggregation — M2.9 endpoint client (forward-compatible).
 *
 * The `/api/v1/operators/instances/drift` endpoint lands in M2.9 (issue
 * #437). Until then `listOperatorInstancesDrift()` catches the expected
 * 404 / 501 and surfaces a sentinel result so the M2.3 banner can render
 * "telemetry pending" without blocking the page. Same fault-tolerance
 * pattern as the M1.6 stack endpoint (mem-06120131ee628046, PR #505).
 * ────────────────────────────────────────────────────────────────────── */

export interface OperatorInstanceDriftEntry {
	instance_slug: string;
	lesser_version?: string;
	body_version?: string;
	mcp_wired_against?: string;
	drift_status: 'ok' | 'wire-stale' | 'unknown' | string;
}

export interface ListOperatorInstancesDriftResponse {
	instances: OperatorInstanceDriftEntry[];
	count: number;
	mcp_drift_count: number;
}

export type OperatorInstancesDriftResult =
	| { kind: 'data'; data: ListOperatorInstancesDriftResponse }
	| { kind: 'endpoint-pending'; status: number };

export async function listOperatorInstancesDrift(token: string): Promise<OperatorInstancesDriftResult> {
	try {
		const data = await fetchJson<ListOperatorInstancesDriftResponse>('/api/v1/operators/instances/drift', {
			headers: {
				authorization: `Bearer ${token}`,
			},
		});
		return { kind: 'data', data };
	} catch (err) {
		const status = (err as { status?: number })?.status ?? 0;
		if (status === 404 || status === 501) {
			return { kind: 'endpoint-pending', status };
		}
		throw err;
	}
}
