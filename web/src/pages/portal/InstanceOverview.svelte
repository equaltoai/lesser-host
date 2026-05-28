<!--
@component
InstanceOverview — Project 42 M6 Instance Detail Overview UI.

Replaces the prior Overview tab's DefinitionList cards with the design's
layout: four metric cards, Stack card (Lesser / Body / MCP wiring),
7-day Activity panel, Souls preview, and right rail (Snapshot / Operate /
Owners). UI-only; consumes existing instance detail + soul-list endpoints.

@license AGPL-3.0-only

Data posture:
- Metric cards (MTD spend, Active users 30d, Posts 24h, Sig failures)
  render honest unavailable states. The M5 fleet-data fields
  (active_users_30d, posts_24h, sig_fails_24h, spark_activity, spark_cost)
  were added to the Fleet list response but are not yet present on the
  instance detail response. See the committed evidence MD for the full
  deviation list.
- Stack card consumes M7 drift fields (lesser_drift, lesser_body_drift,
  mcp_drift, drift_summary) and instance capability toggles.
- Activity panel shows honest "no activity data" state for the same M5
  data gap.
- Souls preview fetches top-5 bound souls via soulListMyAgents using the
  M0.5-fixed domain matching (hosted_base_domain + managed_lesser_domain).
- Right rail panels consume instance identity fields and M7 owner enrichment.
- Raw ISO 8601 timestamps are replaced with human/relative dates.

Posture invariants:
- Strict-no-inline-CSP safe.
- Multi-tenant isolation: all data sources enforce per-owner / per-slug
  ownership server-side.
- Trust-API instance-auth untouched.

Source: agents/arch/project-40-portal-redesign-recovery/source-host-plan-2026-05-27/milestones/07-m6-instance-overview-ui.md
Issue: equaltoai/lesser-host#541
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import type { SoulMineAgentItem } from 'src/lib/api/soul';
	import { soulListMyAgents } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Heading, Link, Spinner, Text } from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';
	import Metric from 'src/lib/components/primitives/Metric.svelte';
	import Sparkline from 'src/lib/components/primitives/Sparkline.svelte';

	interface Props {
		token: string;
		/** Instance data already loaded by the parent. */
		instance: InstanceResponse;
	}

	let { token, instance }: Props = $props();

	// --- Soul fetch for the Souls preview ---
	let soulsLoading = $state(false);
	let soulsError = $state<string | null>(null);
	let allAgents = $state<SoulMineAgentItem[]>([]);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function loadSouls() {
		soulsError = null;
		soulsLoading = true;
		try {
			const res = await soulListMyAgents(token);
			allAgents = res.agents ?? [];
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			soulsError = formatError(err);
		} finally {
			soulsLoading = false;
		}
	}

	const boundAgents = $derived.by<SoulMineAgentItem[]>(() => {
		if (!instance) return [];
		const candidateDomains: Record<string, true> = {};
		const baseDomain = (instance.hosted_base_domain || '').trim().toLowerCase();
		const managedDomain = (instance.managed_lesser_domain || '').trim().toLowerCase();
		if (baseDomain) candidateDomains[baseDomain] = true;
		if (managedDomain) candidateDomains[managedDomain] = true;
		const candidateCount = Object.keys(candidateDomains).length;
		if (candidateCount === 0) return [];
		return allAgents.filter((item) => {
			const domain = (item.agent.domain || '').trim().toLowerCase();
			return Boolean(candidateDomains[domain]);
		});
	});

	const topBoundAgents = $derived(boundAgents.slice(0, 5));

const lesserDriftBadge = $derived(driftBadge(instance.lesser_drift));
const bodyDriftBadge = $derived(driftBadge(instance.lesser_body_drift));
const mcpDriftBadge = $derived(driftBadge(instance.mcp_drift));

	onMount(() => {
		void loadSouls();
	});

	// --- Helpers ---

	function relativeTime(raw: string | undefined): string {
		if (!raw) return '—';
		// Guard against Go zero-time sentinel: "0001-01-01T00:00:00Z"
		if (raw.startsWith('0001-01-01')) return '—';
		const d = new Date(raw);
		if (isNaN(d.getTime())) return '—';
		const now = Date.now();
		const diffMs = now - d.getTime();
		const diffSec = Math.floor(diffMs / 1000);
		if (diffSec < 60) return 'just now';
		const diffMin = Math.floor(diffSec / 60);
		if (diffMin < 60) return `${diffMin}m ago`;
		const diffHr = Math.floor(diffMin / 60);
		if (diffHr < 24) return `${diffHr}h ago`;
		const diffDay = Math.floor(diffHr / 24);
		if (diffDay < 30) return `${diffDay}d ago`;
		if (diffDay < 365) {
			const months = Math.floor(diffDay / 30);
			return `${months}mo ago`;
		}
		const years = Math.floor(diffDay / 365);
		return `${years}y ago`;
	}

	function humanDate(raw: string | undefined): string {
		if (!raw) return '—';
		if (raw.startsWith('0001-01-01')) return '—';
		const d = new Date(raw);
		if (isNaN(d.getTime())) return '—';
		return d.toLocaleDateString(undefined, {
			year: 'numeric',
			month: 'short',
			day: 'numeric',
		});
	}

	function soulAnchorTime(): string {
		const raw = instance.soul_anchor_at;
		if (!raw) return '—';
		if (raw.startsWith('0001-01-01')) return '—';
		const d = new Date(raw);
		if (isNaN(d.getTime())) return '—';
		return relativeTime(raw);
	}

	function soulAnchorLabel(): string {
		const state = (instance.soul_anchor_state || '').trim();
		if (!state) return '—';
		return state;
	}

	function driftBadge(drift: string | undefined): { label: string; color: 'success' | 'warning' | 'error' | 'gray' } {
		const d = (drift || '').trim() || 'unknown';
		switch (d) {
			case 'ok':
				return { label: 'Current', color: 'success' };
			case 'stale':
				return { label: 'Stale', color: 'warning' };
			case 'wire-stale':
				return { label: 'Wire stale', color: 'warning' };
			default:
				return { label: 'Unknown', color: 'gray' };
		}
	}

	function shortHex(hex: string, left: number = 8, right: number = 6): string {
		const h = (hex || '').trim();
		if (h.length <= left + right + 2) return h;
		return `${h.slice(0, left)}…${h.slice(-right)}`;
	}

	function shortId(id: string): string {
		return shortHex(id, 6, 4);
	}

	function domainDisplay(): string {
		const managed = (instance.managed_lesser_domain || '').trim();
		if (managed) return managed;
		const base = (instance.hosted_base_domain || '').trim();
		return base || '—';
	}
</script>

<div class="overview">
	<!-- ================================================================
	Main content area
	================================================================ -->
	<div class="overview__main">
		<!-- Metric cards -->
		<div class="overview__metrics">
			<Metric
				label="MTD spend"
				value="—"
				sub="Not available"
				tone="info"
			/>
			<Metric
				label="Active users"
				value="—"
				sub="30-day peak daily"
				tone="info"
			/>
			<Metric
				label="Posts"
				value="—"
				sub="Last 24 hours"
				tone="info"
			/>
			<Metric
				label="Sig failures"
				value="—"
				sub="Last 24 hours"
				tone="info"
			/>
		</div>

		<!-- Stack card -->
		<Panel title="Stack" headerLevel={2}>
			<div class="overview__stack">
				<!-- Lesser row -->
				<div class="overview__stack-row">
					<div class="overview__stack-label">Lesser</div>
					<div class="overview__stack-version">
						<Text size="sm">
							{instance.lesser_version || '—'}
						</Text>
					</div>
					<div class="overview__stack-drift">
						<Badge variant="outlined" color={lesserDriftBadge.color} size="sm">
							{lesserDriftBadge.label}
						</Badge>
					</div>
					<div class="overview__stack-action">
						<Link {...linkProps(`/portal/instances/${instance.slug}/config`)} variant="default">
							Manage
						</Link>
					</div>
				</div>

				<!-- Body row -->
				<div class="overview__stack-row">
					<div class="overview__stack-label">Body</div>
					<div class="overview__stack-version">
						{#if instance.body_enabled && instance.lesser_body_version}
							<Text size="sm">{instance.lesser_body_version}</Text>
						{:else if instance.body_enabled}
							<Text size="sm" color="secondary">Enabled</Text>
						{:else}
							<Text size="sm" color="secondary">Not installed</Text>
						{/if}
					</div>
					<div class="overview__stack-drift">
						<Badge variant="outlined" color={bodyDriftBadge.color} size="sm">
							{bodyDriftBadge.label}
						</Badge>
					</div>
					<div class="overview__stack-action">
						{#if instance.body_enabled}
							<Link {...linkProps(`/portal/instances/${instance.slug}/config`)} variant="default">
								Manage
							</Link>
						{:else}
							<Button
								variant="outline"
								size="sm"
								disabled
								title="Agentic-capability enablement is gated on operator approval"
							>
								Add agentic
							</Button>
						{/if}
					</div>
				</div>

				<!-- MCP wiring row -->
				<div class="overview__stack-row">
					<div class="overview__stack-label">MCP wiring</div>
					<div class="overview__stack-version">
						{#if instance.mcp_url}
							<Text size="sm" color="secondary">Wired</Text>
						{:else}
							<Text size="sm" color="secondary">Not wired</Text>
						{/if}
					</div>
					<div class="overview__stack-drift">
						<Badge variant="outlined" color={mcpDriftBadge.color} size="sm">
							{mcpDriftBadge.label}
						</Badge>
					</div>
					<div class="overview__stack-action">
						<Button
							variant="outline"
							size="sm"
							disabled
							title="MCP rewiring requires operator approval"
						>
							Re-wire
						</Button>
					</div>
				</div>
			</div>

			{#if instance.drift_summary && instance.drift_summary !== 'ok'}
				<Alert variant="warning" title="Drift detected">
					<Text size="sm">{instance.drift_summary}</Text>
				</Alert>
			{/if}
		</Panel>

		<!-- Activity panel -->
		<Panel title="Activity" headerLevel={2}>
			<div class="overview__activity">
				<div class="overview__activity-chart">
					<div class="overview__activity-chart-label">
						<Text size="xs" color="secondary">Posts · 7d</Text>
					</div>
					<Sparkline values={[]} color="var(--ds-secondary-500)" />
					<Text size="xs" color="secondary">No activity data available</Text>
				</div>
				<div class="overview__activity-chart">
					<div class="overview__activity-chart-label">
						<Text size="xs" color="secondary">Spend · 7d</Text>
					</div>
					<Sparkline values={[]} color="var(--ds-accent-500, var(--ds-secondary-500))" />
					<Text size="xs" color="secondary">No activity data available</Text>
				</div>
			</div>
		</Panel>

		<!-- Souls preview -->
		<Panel title="Souls" headerLevel={2}>
			{#snippet actions()}
				<Link {...linkProps(`/portal/instances/${instance.slug}/souls`)} variant="default">
					View all
				</Link>
			{/snippet}

			{#if soulsLoading && boundAgents.length === 0}
				<div class="overview__loading">
					<Spinner size="sm" />
					<Text size="sm">Loading souls…</Text>
				</div>
			{:else if soulsError}
				<Alert variant="error" title="Failed to load souls">{soulsError}</Alert>
			{:else if !instance.hosted_base_domain}
				<Alert variant="info" title="Instance not provisioned">
					<Text size="sm">
						Souls bind to an instance once provisioning completes and the managed domain is delegated.
					</Text>
				</Alert>
			{:else if topBoundAgents.length === 0}
				<Text size="sm" color="secondary">
					No soul agents bound to this instance yet.
					<Link {...linkProps('/portal/souls')} variant="default">Request a soul</Link>
				</Text>
			{:else}
				<ul class="overview__souls-list">
					{#each topBoundAgents as item (item.agent.agent_id)}
						<li class="overview__souls-item">
							<div class="overview__souls-item-info">
								<Text size="sm" weight="medium">
									{item.agent.local_id || shortId(item.agent.agent_id)}
								</Text>
								<Text size="xs" color="secondary">
									{item.agent.domain}/{item.agent.local_id}
								</Text>
							</div>
							<div class="overview__souls-item-status">
								<Badge
									variant="outlined"
									color={(item.agent.status || '').toLowerCase() === 'active' ? 'success' : 'gray'}
									size="sm"
								>
									{item.agent.status || '—'}
								</Badge>
							</div>
						</li>
					{/each}
				</ul>
				{#if boundAgents.length > 5}
					<Text size="xs" color="secondary">
						+{boundAgents.length - 5} more bound souls not shown.
						<Link {...linkProps(`/portal/instances/${instance.slug}/souls`)} variant="default">View all</Link>
					</Text>
				{/if}
			{/if}
		</Panel>
	</div>

	<!-- ================================================================
	Right rail
	================================================================ -->
	<div class="overview__rail">
		<!-- Snapshot panel -->
		<Panel title="Snapshot" headerLevel={3}>
			<dl class="overview__dl">
				<div class="overview__dl-row">
					<dt class="overview__dl-term">Instance ID</dt>
					<dd class="overview__dl-def">
						<Text size="xs" color="secondary">{instance.hosted_account_id ? shortId(instance.hosted_account_id) : '—'}</Text>
					</dd>
				</div>
				<div class="overview__dl-row">
					<dt class="overview__dl-term">Region</dt>
					<dd class="overview__dl-def">
						<Text size="xs" color="secondary">{instance.hosted_region || '—'}</Text>
					</dd>
				</div>
				<div class="overview__dl-row">
					<dt class="overview__dl-term">Created</dt>
					<dd class="overview__dl-def">
						<Text size="xs" color="secondary">{humanDate(instance.created_at)}</Text>
					</dd>
				</div>
				<div class="overview__dl-row">
					<dt class="overview__dl-term">Domain</dt>
					<dd class="overview__dl-def">
						<Text size="xs" color="secondary">{domainDisplay()}</Text>
					</dd>
				</div>
				<div class="overview__dl-row">
					<dt class="overview__dl-term">Anchor</dt>
					<dd class="overview__dl-def">
						{#if instance.soul_anchor_state}
							<Text size="xs" color="secondary">{soulAnchorLabel()} · {soulAnchorTime()}</Text>
						{:else}
							<Text size="xs" color="secondary">—</Text>
						{/if}
					</dd>
				</div>
			</dl>
		</Panel>

		<!-- Operate panel -->
		<Panel title="Operate" headerLevel={3}>
			<div class="overview__operate">
				<Button
					variant="outline"
					size="sm"
					disabled
					title="Anchor refresh requires operator approval"
				>
					Refresh anchor
				</Button>
				<Button
					variant="outline"
					size="sm"
					disabled
					title="Configuration export is not yet available"
				>
					Export config
				</Button>
				<Link
					{...linkProps(`/portal/instances/${instance.slug}/config`)}
					variant="default"
				>
					Open config…
				</Link>
			</div>
		</Panel>

		<!-- Owners panel -->
		<Panel title="Owners" headerLevel={3}>
			{#if instance.owner_handle}
				<div class="overview__owner">
					<div class="overview__owner-avatar" aria-hidden="true">
						<Text size="sm" weight="medium">{instance.owner_handle.charAt(0).toUpperCase()}</Text>
					</div>
					<div class="overview__owner-info">
						<Text size="sm" weight="medium">{instance.owner_handle}</Text>
						<Text size="xs" color="secondary">{instance.owner_role || 'Owner'}</Text>
					</div>
				</div>
			{:else if instance.owner}
				<div class="overview__owner">
					<div class="overview__owner-avatar" aria-hidden="true">
						<Text size="sm" weight="medium">?</Text>
					</div>
					<div class="overview__owner-info">
						<Text size="sm" weight="medium">{shortHex(instance.owner, 6, 4)}</Text>
						<Text size="xs" color="secondary">Owner</Text>
					</div>
				</div>
			{:else}
				<Text size="sm" color="secondary">No owner information available.</Text>
			{/if}
		</Panel>
	</div>
</div>

<style>
	.overview {
		display: grid;
		grid-template-columns: 1fr 280px;
		gap: var(--ds-space-6, 1.5rem);
		align-items: start;
	}

	.overview__main {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-5, 1.25rem);
		min-width: 0;
	}

	.overview__metrics {
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		gap: var(--ds-space-4, 1rem);
	}

	/* --- Stack card rows --- */
	.overview__stack {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3, 0.75rem);
	}

	.overview__stack-row {
		display: grid;
		grid-template-columns: 80px 1fr auto auto;
		gap: var(--ds-space-3, 0.75rem);
		align-items: center;
		padding: var(--ds-space-2, 0.5rem) 0;
		border-bottom: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
	}

	.overview__stack-row:last-child {
		border-bottom: none;
	}

	.overview__stack-label {
		font-size: var(--ds-font-size-sm, 0.875rem);
		font-weight: var(--ds-weight-semibold, 600);
		color: var(--ds-fg-1, var(--gr-color-foreground));
	}

	.overview__stack-version {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.overview__stack-drift {
		display: flex;
		align-items: center;
	}

	.overview__stack-action {
		display: flex;
		align-items: center;
	}

	/* --- Activity panel --- */
	.overview__activity {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: var(--ds-space-4, 1rem);
	}

	.overview__activity-chart {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2, 0.5rem);
	}

	.overview__activity-chart-label {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	/* --- Souls preview --- */
	.overview__souls-list {
		list-style: none;
		padding: 0;
		margin: 0;
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2, 0.5rem);
	}

	.overview__souls-item {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: var(--ds-space-2, 0.5rem);
		border: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		border-radius: var(--ds-radius-sm, 0.375rem);
		background: var(--ds-bg-raised, var(--gr-color-surface));
	}

	.overview__souls-item-info {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-0, 0);
		min-width: 0;
	}

	.overview__souls-item-status {
		flex-shrink: 0;
	}

	/* --- Right rail --- */
	.overview__rail {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-5, 1.25rem);
		position: sticky;
		top: var(--ds-space-4, 1rem);
	}

	/* --- DL for Snapshot --- */
	.overview__dl {
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2, 0.5rem);
	}

	.overview__dl-row {
		display: flex;
		justify-content: space-between;
		align-items: baseline;
	}

	.overview__dl-term {
		font-size: var(--ds-font-size-xs, 0.75rem);
		font-weight: var(--ds-weight-medium, 500);
		color: var(--ds-fg-2, var(--gr-color-foreground-secondary));
		margin: 0;
	}

	.overview__dl-def {
		margin: 0;
		text-align: right;
	}

	/* --- Operate --- */
	.overview__operate {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2, 0.5rem);
	}

	/* --- Owner --- */
	.overview__owner {
		display: flex;
		gap: var(--ds-space-3, 0.75rem);
		align-items: center;
	}

	.overview__owner-avatar {
		width: 32px;
		height: 32px;
		border-radius: 50%;
		background: var(--ds-bg-subtle, var(--gr-color-surface-secondary));
		display: flex;
		align-items: center;
		justify-content: center;
		flex-shrink: 0;
	}

	.overview__owner-info {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-0, 0);
		min-width: 0;
	}

	.overview__loading {
		display: flex;
		gap: var(--ds-space-2, 0.5rem);
		align-items: center;
	}

	/* --- Responsive: single-column below 960px --- */
	@media (max-width: 959px) {
		.overview {
			grid-template-columns: 1fr;
		}

		.overview__rail {
			position: static;
		}

		.overview__metrics {
			grid-template-columns: repeat(2, 1fr);
		}

		.overview__activity {
			grid-template-columns: 1fr;
		}

		.overview__stack-row {
			grid-template-columns: 60px 1fr auto;
		}

		.overview__stack-action {
			display: none;
		}
	}
</style>
