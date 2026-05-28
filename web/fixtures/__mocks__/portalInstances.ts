/**
 * M2 Shell Fixture — mock portalInstances API.
 *
 * Returns a realistic instance list so the sidebar renders per-instance
 * entries with status dots, slugs, and optional alert badges. This mock
 * is aliased into the `src/lib/api/portalInstances` import path by the
 * fixture Vite config and is NEVER loaded by any customer portal route.
 *
 * @license AGPL-3.0-only
 */

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

function makeInstance(overrides: Partial<InstanceResponse>): InstanceResponse {
	return {
		slug: 'my-instance',
		status: 'ok',
		hosted_region: 'us-east-1',
		lesser_version: 'v2.4.1',
		translation_enabled: false,
		hosted_previews_enabled: false,
		link_safety_enabled: false,
		renders_enabled: false,
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
		created_at: new Date().toISOString(),
		...overrides,
	};
}

/** Fixture instances — realistic mock data for sidebar rendering. */
const FIXTURE_INSTANCES: InstanceResponse[] = [
	makeInstance({ slug: 'my-instance', status: 'ok', hosted_region: 'us-east-1', lesser_version: 'v2.4.1' }),
	makeInstance({ slug: 'staging-env', status: 'warning', hosted_region: 'eu-west-1', lesser_version: 'v2.4.0' }),
	makeInstance({ slug: 'demo-site', status: 'ok', hosted_region: 'ap-southeast-1', lesser_version: 'v2.4.1' }),
	makeInstance({ slug: 'dev-sandbox', status: 'error', hosted_region: 'us-west-2', lesser_version: 'v2.3.9' }),
];

export function portalListInstances(_token?: string): Promise<ListInstancesResponse> {
	void _token;
	return Promise.resolve({ instances: FIXTURE_INSTANCES, count: FIXTURE_INSTANCES.length });
}
