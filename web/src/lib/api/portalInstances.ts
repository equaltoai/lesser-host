import { fetchJson, jsonRequest } from './http';

export interface InstanceResponse {
	slug: string;
	owner?: string;
	status: string;
	provision_status?: string;
	provision_job_id?: string;
	update_status?: string;
	update_job_id?: string;
	lesser_update_status?: string;
	lesser_update_job_id?: string;
	lesser_update_at?: string;
	lesser_body_update_status?: string;
	lesser_body_update_job_id?: string;
	lesser_body_update_at?: string;
	mcp_update_status?: string;
	mcp_update_job_id?: string;
	mcp_update_at?: string;
	hosted_account_id?: string;
	hosted_region?: string;
	hosted_base_domain?: string;
	managed_lesser_domain?: string;
	hosted_zone_id?: string;
	lesser_version?: string;
	lesser_body_version?: string;
	body_provisioned_at?: string;
	mcp_wired_at?: string;
	lesser_host_base_url?: string;
	lesser_host_attestations_url?: string;
	translation_enabled: boolean;
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
	updated_at?: string;
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
	admin_username?: string;
	consent_message_hash?: string;
	consent_signature?: string;
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

export interface WalletChallengeResponse {
	id: string;
	username?: string;
	address: string;
	chainId: number;
	nonce: string;
	message: string;
	issuedAt: string;
	expiresAt: string;
}

export interface ProvisionConsentChallengeResponse {
	instance_slug: string;
	stage: string;
	admin_username: string;
	wallet: WalletChallengeResponse;
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

export interface InstanceKeyListItem {
	id: string;
	created_at: string;
	last_used_at?: string;
	revoked_at?: string;
}

export interface ListInstanceKeysResponse {
	keys: InstanceKeyListItem[];
	count: number;
}

export interface RevokeInstanceKeyResponse {
	instance_slug: string;
	key_id: string;
	revoked: boolean;
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
	translation_enabled?: boolean;
}

export interface UpdateJobResponse {
	id: string;
	instance_slug: string;
	kind?: string;
	status: string;
	step?: string;
	note?: string;
	run_id?: string;
	run_url?: string;
	account_id?: string;
	account_role_name?: string;
	region?: string;
	base_domain?: string;
	lesser_version?: string;
	lesser_body_version?: string;
	body_only?: boolean;
	mcp_only?: boolean;
	active_phase?: string;
	failed_phase?: string;
	deploy_status?: string;
	deploy_run_id?: string;
	deploy_run_url?: string;
	deploy_error?: string;
	body_status?: string;
	body_run_id?: string;
	body_run_url?: string;
	body_error?: string;
	mcp_status?: string;
	mcp_run_id?: string;
	mcp_run_url?: string;
	mcp_error?: string;
	lesser_host_base_url?: string;
	lesser_host_attestations_url?: string;
	lesser_host_instance_key_secret_arn?: string;
	translation_enabled: boolean;
	rotate_instance_key?: boolean;
	rotated_instance_key_id?: string;
	verify_translation_ok?: boolean;
	verify_trust_ok?: boolean;
	verify_tips_ok?: boolean;
	verify_ai_ok?: boolean;
	verify_translation_err?: string;
	verify_trust_err?: string;
	verify_tips_err?: string;
	verify_ai_err?: string;
	error_code?: string;
	error_message?: string;
	request_id?: string;
	created_at: string;
	updated_at: string;
}

export interface ListUpdateJobsResponse {
	jobs: UpdateJobResponse[];
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
	input: {
		region?: string;
		lesser_version?: string;
		admin_username?: string;
		consent_challenge_id: string;
		consent_message: string;
		consent_signature: string;
	},
): Promise<ProvisionJobResponse> {
	const req = jsonRequest(input);
	return fetchJson<ProvisionJobResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/provision`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function portalProvisionConsentChallenge(
	token: string,
	slug: string,
	adminUsername?: string,
): Promise<ProvisionConsentChallengeResponse> {
	const input = adminUsername ? { admin_username: adminUsername } : {};
	const req = jsonRequest(input);
	return fetchJson<ProvisionConsentChallengeResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/provision/consent/challenge`,
		{
			method: 'POST',
			headers: {
				authorization: `Bearer ${token}`,
				...req.headers,
			},
			body: req.body,
		},
	);
}

export function portalGetProvisioning(token: string, slug: string): Promise<ProvisionJobResponse> {
	return fetchJson<ProvisionJobResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/provision`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalCreateUpdateJob(
	token: string,
	slug: string,
	input?: {
		lesser_version?: string;
		lesser_body_version?: string;
		rotate_instance_key?: boolean;
		body_only?: boolean;
		mcp_only?: boolean;
	},
): Promise<UpdateJobResponse> {
	const req = jsonRequest(input ?? {});
	return fetchJson<UpdateJobResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/updates`, {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
			...req.headers,
		},
		body: req.body,
	});
}

export function portalListUpdateJobs(token: string, slug: string, limit?: number): Promise<ListUpdateJobsResponse> {
	const qs = limit ? `?limit=${encodeURIComponent(String(limit))}` : '';
	return fetchJson<ListUpdateJobsResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/updates${qs}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalListInstanceKeys(token: string, slug: string, limit?: number): Promise<ListInstanceKeysResponse> {
	const qs = limit ? `?limit=${encodeURIComponent(String(limit))}` : '';
	return fetchJson<ListInstanceKeysResponse>(`/api/v1/portal/instances/${encodeURIComponent(slug)}/keys${qs}`, {
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

export function portalRevokeInstanceKey(token: string, slug: string, keyId: string): Promise<RevokeInstanceKeyResponse> {
	return fetchJson<RevokeInstanceKeyResponse>(
		`/api/v1/portal/instances/${encodeURIComponent(slug)}/keys/${encodeURIComponent(keyId)}`,
		{
			method: 'DELETE',
			headers: {
				authorization: `Bearer ${token}`,
			},
		},
	);
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
