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

export interface AddDomainVerification {
	method: string;
	txt_name?: string;
	txt_value?: string;
}

export interface AddDomainResponse {
	domain: DomainResponse;
	verification: AddDomainVerification;
}

export interface VerifyDomainResponse {
	domain: DomainResponse;
}

export interface DeleteDomainResponse {
	deleted: boolean;
	domain: string;
}

export interface CreateInstanceKeyResponse {
	instance_slug: string;
	key: string;
	key_id: string;
}

export interface Route53AssistResponse {
	ok: boolean;
	hosted_zone_id?: string;
	record_name: string;
	record_value: string;
}

export interface UpdateInstanceConfigRequest {
	hosted_previews_enabled?: boolean;
	link_safety_enabled?: boolean;
	renders_enabled?: boolean;
	render_policy?: string;
	overage_policy?: string;
	moderation_enabled?: boolean;
	moderation_trigger?: string;
	moderation_virality_min?: number;
	ai_enabled?: boolean;
	ai_model_set?: string;
	ai_batching_mode?: string;
	ai_batch_max_items?: number;
	ai_batch_max_total_bytes?: number;
	ai_pricing_multiplier_bps?: number;
	ai_max_inflight_jobs?: number;
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

export function portalUpdateInstanceConfig(
	token: string,
	slug: string,
	input: UpdateInstanceConfigRequest,
): Promise<InstanceResponse> {
	const req = jsonRequest(input);
	return fetchJson<InstanceResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/config`, {
		method: 'PUT',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
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

export function portalAddInstanceDomain(token: string, slug: string, domain: string): Promise<AddDomainResponse> {
	const req = jsonRequest({ domain });
	return fetchJson<AddDomainResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function portalVerifyInstanceDomain(
	token: string,
	slug: string,
	domain: string,
): Promise<VerifyDomainResponse> {
	return fetchJson<VerifyDomainResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains/${encodeURIComponent(domain)}/verify`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

export function portalRotateInstanceDomain(
	token: string,
	slug: string,
	domain: string,
): Promise<AddDomainResponse> {
	return fetchJson<AddDomainResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains/${encodeURIComponent(domain)}/rotate`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

export function portalDisableInstanceDomain(
	token: string,
	slug: string,
	domain: string,
): Promise<VerifyDomainResponse> {
	return fetchJson<VerifyDomainResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains/${encodeURIComponent(domain)}/disable`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

export function portalDeleteInstanceDomain(
	token: string,
	slug: string,
	domain: string,
): Promise<DeleteDomainResponse> {
	return fetchJson<DeleteDomainResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains/${encodeURIComponent(domain)}`,
		{
			method: 'DELETE',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

export function portalUpsertDomainVerificationRoute53(
	token: string,
	slug: string,
	domain: string,
): Promise<Route53AssistResponse> {
	return fetchJson<Route53AssistResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/domains/${encodeURIComponent(domain)}/dns/route53`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
}

export function portalCreateInstanceKey(token: string, slug: string): Promise<CreateInstanceKeyResponse> {
	return fetchJson<CreateInstanceKeyResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/keys`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}
