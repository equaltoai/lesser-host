import { fetchJson, jsonRequest } from './http';

export interface InstanceResponse {
	slug: string;
	owner?: string;
	status: string;
	provision_status?: string;
	provision_job_id?: string;
	hosted_account_id?: string;
	hosted_region?: string;
	hosted_base_domain?: string;
	hosted_zone_id?: string;
	hosted_previews_enabled: boolean;
	link_safety_enabled: boolean;
	renders_enabled: boolean;
	render_policy: string;
	overage_policy: string;
	moderation_enabled: boolean;
	moderation_trigger: string;
	moderation_virality_min: number;
	ai_enabled: boolean;
	ai_model_set: string;
	ai_batching_mode: string;
	ai_batch_max_items: number;
	ai_batch_max_total_bytes: number;
	ai_pricing_multiplier_bps: number;
	ai_max_inflight_jobs: number;
	created_at: string;
}

export interface ListInstancesResponse {
	instances: InstanceResponse[];
	count: number;
}

export interface ProvisionJobResponse {
	id: string;
	instance_slug: string;
	status: string;
	step?: string;
	note?: string;
	mode?: string;
	plan?: string;
	region?: string;
	stage?: string;
	lesser_version?: string;
	account_request_id?: string;
	account_id?: string;
	parent_hosted_zone_id?: string;
	base_domain?: string;
	child_hosted_zone_id?: string;
	child_name_servers?: string[];
	run_id?: string;
	error_code?: string;
	error_message?: string;
	request_id?: string;
	created_at: string;
	updated_at: string;
}

export interface DomainResponse {
	domain: string;
	instance_slug: string;
	type: string;
	status: string;
	verification_method?: string;
	verified_at?: string;
	created_at: string;
	updated_at: string;
}

export interface ListDomainsResponse {
	domains: DomainResponse[];
	count: number;
}

export function portalListInstances(token: string): Promise<ListInstancesResponse> {
	return fetchJson<ListInstancesResponse>('/api/v1/portal/instances', {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalCreateInstance(token: string, slug: string): Promise<InstanceResponse> {
	const req = jsonRequest({ slug });
	return fetchJson<InstanceResponse>('/api/v1/portal/instances', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function portalGetInstance(token: string, slug: string): Promise<InstanceResponse> {
	return fetchJson<InstanceResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalStartProvisioning(
	token: string,
	slug: string,
	input?: { region?: string; lesser_version?: string },
): Promise<ProvisionJobResponse> {
	const req = input ? jsonRequest(input) : null;
	return fetchJson<ProvisionJobResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/provision`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...(req?.headers ?? {}),
		},
		body: req?.body,
	});
}

export function portalGetProvisioning(token: string, slug: string): Promise<ProvisionJobResponse> {
	return fetchJson<ProvisionJobResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/provision`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalListInstanceDomains(token: string, slug: string): Promise<ListDomainsResponse> {
	return fetchJson<ListDomainsResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}
