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
