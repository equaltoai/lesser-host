<!--
@component
M6InstanceOverviewFixture — isolated visual fixture for the M6 Instance
Overview layout. Renders InstanceOverview with realistic mock data to
exercise all UI states: metric cards (all unavailable), Stack card with
drift, Activity panel (empty), Souls preview (2 mock souls via aliased
soulListMyAgents), right rail (Snapshot/Operate/Owners).

Not mounted by any customer portal route — exists solely for local
visual review and headless PNG capture at 1440×900.

@license AGPL-3.0-only
@public
-->
<script lang="ts">
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import InstanceOverview from 'src/pages/portal/InstanceOverview.svelte';

	const fixtureInstance: InstanceResponse = {
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
		mcp_drift: 'wire-stale',
		drift_summary: 'MCP wiring was configured against body v0.7.0; body is now at v0.8.1. Run an MCP update to reconcile.',
		mcp_url: 'https://mcp.simulacrum.greater.website',
		created_at: new Date(Date.now() - 180 * 24 * 3600 * 1000).toISOString(),
		updated_at: new Date(Date.now() - 5 * 24 * 3600 * 1000).toISOString(),
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

	// Mock token — the fixture Vite config aliases src/lib/api/soul to a
	// fixture mock that returns 2 bound souls, so the Souls preview renders
	// the design state without a backend.
	const fixtureToken = 'fixture-token-m6';
</script>

<div class="fixture-wrapper">
	<InstanceOverview token={fixtureToken} instance={fixtureInstance} />
</div>

<style>
	.fixture-wrapper {
		max-width: 1152px;
		margin: 0 auto;
		padding: var(--ds-space-6, 1.5rem);
	}
</style>
