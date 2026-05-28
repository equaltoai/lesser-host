/**
 * M9 Configuration Fixture — mock portalInstances API.
 *
 * Returns deterministic InstanceResponse data so the InstanceConfig
 * component renders the design-aligned Configuration tab without a backend.
 *
 * @license AGPL-3.0-only
 */
export interface InstanceResponse {
	slug: string;
	owner?: string;
	status: string;
	provision_status?: string;
	hosted_account_id?: string;
	hosted_region?: string;
	hosted_base_domain?: string;
	managed_lesser_domain?: string;
	lesser_version?: string;
	lesser_body_version?: string;
	body_provisioned_at?: string;
	mcp_wired_at?: string;
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
	soul_enabled?: boolean;
	body_enabled?: boolean;
	lesser_ai_enabled?: boolean;
	lesser_ai_moderation_enabled?: boolean;
	created_at: string;
	updated_at?: string;
}

const FIXTURE_INSTANCE: InstanceResponse = {
	slug: 'simulacrum',
	owner: '0x4f29abc123def4567890abc123def4567890f9c2',
	status: 'ok',
	provision_status: 'ok',
	hosted_account_id: '123456789012',
	hosted_region: 'us-west-2',
	hosted_base_domain: 'simulacrum.greater.website',
	managed_lesser_domain: 'simulacrum.greater.website',
	lesser_version: 'v0.31.2',
	lesser_body_version: 'v0.8.1',
	body_enabled: true,
	soul_enabled: true,
	lesser_ai_moderation_enabled: true,
	translation_enabled: true,
	hosted_previews_enabled: true,
	link_safety_enabled: true,
	renders_enabled: true,
	render_policy: 'suspicious',
	overage_policy: 'block',
	moderation_enabled: true,
	moderation_trigger: 'on_reports',
	moderation_virality_min: 0,
	ai_enabled: false,
	ai_model_set: 'default',
	ai_batching_mode: 'none',
	ai_batch_max_items: 10,
	ai_batch_max_total_bytes: 1048576,
	ai_pricing_multiplier_bps: 10000,
	ai_max_inflight_jobs: 10,
	created_at: new Date(Date.now() - 180 * 24 * 3600 * 1000).toISOString(),
	updated_at: new Date(Date.now() - 5 * 24 * 3600 * 1000).toISOString(),
};

export function portalGetInstance(_token?: string, _slug?: string): Promise<InstanceResponse> {
	void _token;
	void _slug;
	return Promise.resolve({ ...FIXTURE_INSTANCE });
}

export function portalUpdateInstanceConfig(
	_token?: string,
	_slug?: string,
	_input?: Record<string, unknown>,
): Promise<InstanceResponse> {
	void _token;
	void _slug;
	void _input;
	return Promise.resolve({ ...FIXTURE_INSTANCE });
}

/** Defined locally because the real module is aliased away by Vite. */
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
