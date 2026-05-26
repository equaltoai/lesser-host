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
 * UpdateJob fleet feed — operator-scope listing (forward-compatible).
 *
 * The per-slug portal already exposes UpdateJobs via
 * `/api/v1/portal/instances/{slug}/updates` (see `portalInstances.ts`).
 * The operator-scope equivalent — a flat fleet-wide listing — is not yet
 * shipped. PR #512 arch review 4363557132 Blocker 4 calls for the M2.3
 * provisioning list to surface ProvisionJob + UpdateJob rows side by
 * side; this client is the forward-compat client that consumes the
 * eventual endpoint, with an `endpoint-pending` sentinel so the UI
 * lights up without backend dependency.
 *
 * Target endpoint: GET `/api/v1/operators/updates`.
 * Expected shape: `{ jobs: OperatorUpdateJobListItem[], count: number }`.
 * Operator-JWT required (same auth shape as the ProvisionJob list).
 * ────────────────────────────────────────────────────────────────────── */

export interface OperatorUpdateJobListItem {
	id: string;
	instance_slug: string;
	/** UpdateJob kind: 'lesser' / 'body' / 'mcp' / etc. */
	kind?: string;
	status: string;
	/** Currently active phase (deploy / body / mcp / verify) or empty. */
	active_phase?: string;
	failed_phase?: string;
	/** Body-only / MCP-only flags discriminate kind when `kind` is unset. */
	body_only?: boolean;
	mcp_only?: boolean;
	step?: string;
	attempts?: number;
	max_attempts?: number;
	run_id?: string;
	run_url?: string;
	deploy_run_url?: string;
	body_run_url?: string;
	mcp_run_url?: string;
	error_code?: string;
	error_message?: string;
	request_id?: string;
	created_at: string;
	updated_at: string;
}

export interface ListOperatorUpdateJobsResponse {
	jobs: OperatorUpdateJobListItem[];
	count: number;
}

export type ListOperatorUpdateJobsResult =
	| { kind: 'data'; data: ListOperatorUpdateJobsResponse }
	| { kind: 'endpoint-pending'; status: number };

export async function listOperatorUpdateJobs(
	token: string,
	input?: { status?: string; limit?: number },
): Promise<ListOperatorUpdateJobsResult> {
	const qs = new URLSearchParams();
	if (input?.status) qs.set('status', input.status);
	if (input?.limit) qs.set('limit', String(input.limit));
	const url = qs.toString() ? `/api/v1/operators/updates?${qs.toString()}` : '/api/v1/operators/updates';
	try {
		const data = await fetchJson<ListOperatorUpdateJobsResponse>(url, {
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

/* ──────────────────────────────────────────────────────────────────────────
 * MCP-drift fleet aggregation — M2.9 endpoint client (forward-compatible).
 *
 * Shape matches the documented contract in
 * `docs/provisioning-web-ui-rework-2026-05-24.md` Change 5.3:
 *
 *   GET /api/v1/operators/instances/drift
 *   {
 *     "instances": [
 *       {
 *         "slug": "press-room",
 *         "lesser": { "current": "v1.4.1", "target": "v1.4.2", "drift": "stale" },
 *         "body":   { "current": "v0.2.5", "target": "v0.2.6", "drift": "stale" },
 *         "mcp":    { "wired_against": "v0.2.5", "current_body": "v0.2.5", "drift": "ok" }
 *       }
 *     ],
 *     "summary": { "total": 12, "lesser_stale": 3, "body_stale": 2, "mcp_wire_stale": 4 }
 *   }
 *
 * Until M2.9 (issue #437) lands, `listOperatorInstancesDrift()` catches
 * the expected 404 / 501 and returns an `endpoint-pending` sentinel so
 * pages render gracefully. Aligned to documented contract after PR #512
 * arch review 4363557132 Blocker 2.
 * ────────────────────────────────────────────────────────────────────── */

/** Per-component drift state. 'ok' / 'stale' / 'wire-stale' / 'unknown'. */
export type DriftState = 'ok' | 'stale' | 'wire-stale' | 'unknown' | string;

export interface DriftCellLesser {
	current?: string;
	target?: string;
	drift: DriftState;
}

export interface DriftCellBody {
	current?: string;
	target?: string;
	drift: DriftState;
}

export interface DriftCellMcp {
	wired_against?: string;
	current_body?: string;
	drift: DriftState;
}

export interface OperatorInstanceDriftEntry {
	slug: string;
	lesser: DriftCellLesser;
	body: DriftCellBody;
	mcp: DriftCellMcp;
}

export interface OperatorInstancesDriftSummary {
	total: number;
	lesser_stale: number;
	body_stale: number;
	mcp_wire_stale: number;
}

export interface ListOperatorInstancesDriftResponse {
	instances: OperatorInstanceDriftEntry[];
	summary: OperatorInstancesDriftSummary;
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

/**
 * Derive a single "row-level" drift label from a per-instance entry.
 * Used by the M2.6 Stack Matrix to colour each row + drive the Wire-MCP
 * CTA visibility. Priority: wire-stale > lesser-stale > body-stale > ok.
 */
export type RowDriftLabel = 'ok' | 'lesser-stale' | 'body-stale' | 'wire-stale' | 'unknown';
export function rowDriftLabel(entry: OperatorInstanceDriftEntry): RowDriftLabel {
	if ((entry.mcp?.drift || '').toLowerCase() === 'wire-stale') return 'wire-stale';
	if ((entry.lesser?.drift || '').toLowerCase() === 'stale') return 'lesser-stale';
	if ((entry.body?.drift || '').toLowerCase() === 'stale') return 'body-stale';
	const allOk =
		(entry.lesser?.drift || '').toLowerCase() === 'ok' &&
		(entry.body?.drift || '').toLowerCase() === 'ok' &&
		(entry.mcp?.drift || '').toLowerCase() === 'ok';
	return allOk ? 'ok' : 'unknown';
}

/* ──────────────────────────────────────────────────────────────────────────
 * MCP-drift fleet remediation — M2.10 endpoint client.
 *
 * Shape matches the documented contract in
 * `docs/provisioning-web-ui-rework-2026-05-24.md` Change 5.4 and the
 * shipped backend (`internal/controlplane/handlers_operator_remediate_mcp.go`):
 *
 *   POST /api/v1/operators/instances/remediate-mcp-drift
 *   {
 *     "created_job_ids": ["uj-…", "uj-…"],
 *     "created": 2,
 *     "skipped": 1
 *   }
 *
 * The backend reads fleet drift internally, then creates one MCP-only
 * UpdateJob per slug whose `mcp.drift == "wire-stale"`. Idempotency is
 * enforced via `GSI2 = UPDATE_ACTIVE` lookup — slugs that already have
 * an active MCP-only job are surfaced in `skipped`, not `created`. The
 * operator-JWT requirement matches the rest of the operator surface.
 *
 * No request body is required; the operator-JWT is the sole input.
 * ────────────────────────────────────────────────────────────────────── */

export interface RemediateMCPDriftResponse {
	created_job_ids: string[];
	created: number;
	skipped: number;
}

export function remediateMCPDrift(token: string): Promise<RemediateMCPDriftResponse> {
	return fetchJson<RemediateMCPDriftResponse>('/api/v1/operators/instances/remediate-mcp-drift', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}
