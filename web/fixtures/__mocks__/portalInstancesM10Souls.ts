/**
 * M10 Souls Fixture — mock portalInstances API.
 *
 * Returns a deterministic instance response so InstanceSouls can resolve
 * the instance's domain and filter souls.
 *
 * @license AGPL-3.0-only
 */

export interface InstanceResponse {
	slug: string;
	owner: string;
	status: string;
	provision_status: string;
	hosted_account_id?: string;
	hosted_region?: string;
	hosted_base_domain: string;
	managed_lesser_domain?: string;
	lesser_version?: string;
	lesser_body_version?: string;
	body_enabled?: boolean;
	soul_enabled?: boolean;
	soul_anchor_state?: string;
	soul_anchor_at?: string;
	lesser_drift?: string;
	lesser_body_drift?: string;
	mcp_drift?: string;
	drift_summary?: string;
	mcp_url?: string;
	created_at: string;
	updated_at: string;
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
	lesser_host_base_url?: string;
	lesser_host_attestations_url?: string;
	owner_handle?: string;
	owner_role?: string;
	owner_avatar_hash?: string;
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
	soul_anchor_state: 'anchored',
	soul_anchor_at: new Date(Date.now() - 3 * 24 * 3600 * 1000).toISOString(),
	lesser_drift: 'ok',
	lesser_body_drift: 'ok',
	mcp_drift: 'ok',
	created_at: new Date(Date.now() - 180 * 24 * 3600 * 1000).toISOString(),
	updated_at: new Date(Date.now() - 4 * 24 * 3600 * 1000).toISOString(),
	translation_enabled: false,
	hosted_previews_enabled: true,
	link_safety_enabled: true,
	renders_enabled: true,
	render_policy: '',
	overage_policy: '',
	moderation_enabled: false,
	moderation_trigger: '',
	moderation_virality_min: 0,
	ai_enabled: false,
	ai_model_set: '',
	ai_batching_mode: '',
	ai_batch_max_items: 0,
	ai_batch_max_total_bytes: 0,
	ai_pricing_multiplier_bps: 0,
	ai_max_inflight_jobs: 0,
	lesser_host_base_url: 'https://simulacrum.greater.website',
	lesser_host_attestations_url: 'https://lesser.host',
	owner_handle: 'Aron',
	owner_role: 'operator',
	owner_avatar_hash: '',
};

export async function portalGetInstance(_token: string, _slug: string): Promise<InstanceResponse> {
	void _token;
	void _slug;
	return FIXTURE_INSTANCE;
}

export async function portalListInstances(_token: string): Promise<{ instances: InstanceResponse[] }> {
	void _token;
	return { instances: [FIXTURE_INSTANCE] };
}
