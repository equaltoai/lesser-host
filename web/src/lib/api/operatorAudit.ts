import { fetchJson } from './http';

export interface AuditLogEntry {
	id: string;
	actor: string;
	action: string;
	target: string;
	request_id: string;
	created_at: string;
}

export interface ListOperatorAuditLogResponse {
	entries: AuditLogEntry[];
	count: number;
}

export function listOperatorAuditLog(
	token: string,
	input?: {
		target?: string;
		actor?: string;
		action?: string;
		request_id?: string;
		since?: string;
		until?: string;
		limit?: number;
	},
): Promise<ListOperatorAuditLogResponse> {
	const qs = new URLSearchParams();
	if (input?.target) qs.set('target', input.target);
	if (input?.actor) qs.set('actor', input.actor);
	if (input?.action) qs.set('action', input.action);
	if (input?.request_id) qs.set('request_id', input.request_id);
	if (input?.since) qs.set('since', input.since);
	if (input?.until) qs.set('until', input.until);
	if (input?.limit) qs.set('limit', String(input.limit));

	const url = qs.toString() ? `/api/v1/operators/audit?${qs.toString()}` : '/api/v1/operators/audit';
	return fetchJson<ListOperatorAuditLogResponse>(url, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

