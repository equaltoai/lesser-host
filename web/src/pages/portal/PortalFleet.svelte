<!--
@component
PortalFleet — M4 Fleet UI redesign.

Personalised landing page showing customer greeting, smart summary, four
metric cards, an instance card grid (with Cards/Table tabs), and a right
rail with Cost pulse, Right-now/Provisioning, and Heads-up panels.

Consumes the M5 Fleet DTO fields (active_users_30d, posts_24h,
spark_activity, spark_cost, peers, severed) for per-instance display and
metric computation. Fields that are zero-valued (no data yet) are shown
honestly with placeholder copy.

Posture invariants:
  - UI only — no backend changes. All data from existing portal endpoints.
  - Multi-tenant isolation: consumes only per-owner-scoped portal APIs.
  - Strict-CSP safe: no inline styles, no inline event handlers, no
    third-party origins.
  - AGPL-3.0-only.

Source: agents/arch/project-40-portal-redesign-recovery/.../05-m4-fleet-ui.md
Issue: equaltoai/lesser-host#539

@license AGPL-3.0-only
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalCreateInstance, portalListInstances } from 'src/lib/api/portalInstances';
	import { getPortalMe, type PortalMeResponse } from 'src/lib/api/portal';
	import { soulListMyAgents } from 'src/lib/api/soul';
	import type { BudgetMonthResponse } from 'src/lib/api/portalUsage';
	import { portalGetBudgetMonth } from 'src/lib/api/portalUsage';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Button, Heading, Link, Spinner, Text, TextField } from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';
	import FleetCard from 'src/lib/components/FleetCard.svelte';
	import CostGauge from 'src/lib/components/CostGauge.svelte';
	import Sparkline from 'src/lib/components/primitives/Sparkline.svelte';
	import Metric from 'src/lib/components/primitives/Metric.svelte';
		import { portalFleetInstances } from 'src/lib/portalFleetState';
	import { mapInstanceFleetStatus } from 'src/lib/fleetStatus';
	import type { FleetCardMetadataItem } from 'src/lib/greater/host-platform';

	let { token } = $props<{ token: string }>();

	/* ========================================================================
	 * Form validation
	 * ======================================================================*/

	const slugRE = /^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$/;

	/* ========================================================================
	 * State
	 * ======================================================================*/

	let loading = $state(false);
	let error = $state<string | null>(null);
	let portalMe = $state<PortalMeResponse | null>(null);
	let instances = $state<InstanceResponse[]>([]);
	let budgets = $state<Record<string, BudgetMonthResponse>>({});
	let soulCount = $state<number>(0);
	let activeTab = $state<'cards' | 'table'>('cards');

	/* Provisioning CTA */
	let createSlug = $state('');
	let createLoading = $state(false);
	let createError = $state<string | null>(null);

	/* ========================================================================
	 * Derived metrics
	 * ======================================================================*/

	const COST_THRESHOLDS = { warning: 0.75, danger: 0.95 } as const;

	let liveInstances = $derived(
		instances.filter(
			(i) => i.status === 'active' || i.provision_status === 'ok'
		).length
	);

	const mtdSpend = $derived.by(() =>
		Object.values(budgets).reduce((sum, b) => sum + (b.used_credits ?? 0), 0)
	);

	const totalPeers = $derived(
		instances.reduce((sum, i) => sum + (i.peers ?? 0), 0)
	);

	const uniqueRegions = $derived.by(() => {
		const regions = new Set(
			instances.map((i) => i.hosted_region).filter((r): r is string => !!r)
		);
		return regions.size;
	});

	const provisioningInstances = $derived(
		instances.filter(
			(i) =>
				i.provision_status === 'queued' || i.provision_status === 'running'
		)
	);

	const degradedInstances = $derived(
		instances.filter(
			(i) =>
				i.provision_status === 'error' ||
				i.status === 'warning' ||
				i.status === 'degraded' ||
				i.status === 'failed'
		)
	);

	const overBudgetInstances = $derived.by(() =>
		instances.filter((i) => {
			const b = budgets[i.slug];
			if (!b || b.included_credits <= 0) return false;
			return b.used_credits / b.included_credits >= COST_THRESHOLDS.warning;
		})
	);

	const displayName = $derived(
		portalMe?.display_name ?? portalMe?.username ?? undefined
	);

	const smartSummary = $derived.by(() => {
		if (instances.length === 0) {
			return 'Create your first instance to get started with managed hosting.';
		}

		const parts: string[] = [];
		parts.push(`You have ${instances.length} instance${instances.length !== 1 ? 's' : ''}`);
		if (uniqueRegions > 0) {
			parts.push(`across ${uniqueRegions} region${uniqueRegions !== 1 ? 's' : ''}`);
		}
		if (liveInstances > 0) {
			parts.push(`(${liveInstances} live)`);
		}
		if (soulCount > 0) {
			parts.push(`with ${soulCount} soul${soulCount !== 1 ? 's' : ''} deployed`);
		}

		let summary = parts.join(' ') + '.';

		if (degradedInstances.length > 0) {
			summary += ` ${degradedInstances.length} instance${degradedInstances.length !== 1 ? 's' : ''} need${degradedInstances.length === 1 ? 's' : ''} attention.`;
		}
		if (overBudgetInstances.length > 0) {
			summary += ` ${overBudgetInstances.length} approaching or exceeding budget.`;
		}
		if (provisioningInstances.length > 0) {
			summary += ` ${provisioningInstances.length} provisioning.`;
		}

		return summary;
	});

	/* ========================================================================
	 * Helpers
	 * ======================================================================*/

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function currentMonthUTC(): string {
		return new Date().toISOString().slice(0, 7);
	}

	function formatSpend(value: number): string {
		if (value >= 1000) {
			return `${(value / 1000).toFixed(1)}k`;
		}
		return value.toLocaleString();
	}
	function formatCreditValue(value: number, _currency?: string): string {
		return `${formatSpend(value)} cr`;
	}

	function buildCardMetadata(inst: InstanceResponse): FleetCardMetadataItem[] {
		const items: FleetCardMetadataItem[] = [];

		const domain = inst.managed_lesser_domain ?? inst.hosted_base_domain;
		if (domain) items.push({ key: 'Domain', value: domain });
		if (inst.hosted_region) items.push({ key: 'Region', value: inst.hosted_region });

		items.push({ key: 'Active users', value: inst.active_users_30d != null && inst.active_users_30d > 0 ? String(inst.active_users_30d) : '—' });

		items.push({ key: 'Posts 24h', value: inst.posts_24h != null && inst.posts_24h > 0 ? String(inst.posts_24h) : '—' });

		const fedParts: string[] = [];
		if (inst.peers != null && inst.peers > 0) fedParts.push(`${inst.peers} peers`);
		if (inst.severed != null && inst.severed > 0) fedParts.push(`${inst.severed} severed`);
		items.push({ key: 'Federation', value: fedParts.length > 0 ? fedParts.join(', ') : '—' });

		if (inst.lesser_version) items.push({ key: 'Version', value: inst.lesser_version });
		return items;
	}

	function hasSparkData(inst: InstanceResponse): boolean {
		const a = inst.spark_activity;
		const c = inst.spark_cost;
		return (!!a && a.length > 0) || (!!c && c.length > 0);
	}

	function getSparkValues(inst: InstanceResponse): number[] {
		if (inst.spark_cost && inst.spark_cost.length > 0) return inst.spark_cost;
		if (inst.spark_activity && inst.spark_activity.length > 0) return inst.spark_activity;
		return [];
	}

	/* ========================================================================
	 * Data loading
	 * ======================================================================*/

	async function loadFleet() {
		error = null;
		instances = [];
		budgets = {};
		soulCount = 0;
		portalFleetInstances.set([]);

		loading = true;
		try {
			// Load portal/me, instances, and souls in parallel.
			const [instancesRes, soulsRes] = await Promise.all([
				portalListInstances(token),
				// souls are optional — fail-quiet so fleet still renders
				soulListMyAgents(token).catch(() => ({ agents: [], count: 0 })),
			]);

			const list = instancesRes.instances ?? [];
			instances = list;

			portalFleetInstances.set(
				list.map((inst) => ({
					slug: inst.slug,
					hosted_region: inst.hosted_region,
					lesser_version: inst.lesser_version,
				}))
			);

			soulCount = soulsRes.count ?? soulsRes.agents?.length ?? 0;

			// Load portal/me for display name (fail-quiet).
			try {
				portalMe = await getPortalMe(token);
			} catch {
				// display_name is cosmetic; fleet still renders without it.
			}

			// Load budgets in parallel; fail-quiet per instance.
			const month = currentMonthUTC();
			const settlements = await Promise.allSettled(
				list.map((inst) => portalGetBudgetMonth(token, inst.slug, month))
			);
			const map: Record<string, BudgetMonthResponse> = {};
			for (let i = 0; i < settlements.length; i += 1) {
				const result = settlements[i];
				const inst = list[i];
				if (!inst) continue;
				if (result.status === 'fulfilled') {
					map[inst.slug] = result.value;
				}
			}
			budgets = map;
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			error = formatError(err);
		} finally {
			loading = false;
		}
	}

	async function createInstance() {
		createError = null;
		const slug = createSlug.trim().toLowerCase();
		if (!slug) {
			createError = 'Slug is required.';
			return;
		}
		if (!slugRE.test(slug)) {
			createError =
				'Slug must be 1–63 chars, lowercase letters/numbers, and hyphens (cannot start/end with hyphen).';
			return;
		}
		createLoading = true;
		try {
			const inst = await portalCreateInstance(token, slug);
			createSlug = '';
			await loadFleet();
			navigate(`/portal/instances/${inst.slug}`);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			const maybe = err as Partial<ApiError>;
			if (maybe.status === 403 && typeof maybe.message === 'string') {
				const lower = maybe.message.toLowerCase();
				if (lower.includes('approval rejected')) {
					createError =
						'Your account was rejected. Contact support if you believe this is a mistake.';
					return;
				}
				if (lower.includes('approval required')) {
					createError =
						'Your account is pending approval. Instance creation and provisioning are blocked until an admin approves your user.';
					return;
				}
			}
			createError = formatError(err);
		} finally {
			createLoading = false;
		}
	}

	onMount(() => {
		void loadFleet();
	});
</script>

<!-- =========================================================================
     PAGE LAYOUT
     ========================================================================= -->

<div class="fleet-page">
	<!-- ── Main content ──────────────────────────────────────────────── -->

	<div class="fleet-main">
		<!-- Greeting header -->
		<header class="fleet-header">
			<div class="fleet-header__text">
				<Heading level={1} size="2xl">
					{#if displayName}
						Welcome back{displayName ? `, ${displayName}` : ''}
					{:else if !loading}
						Fleet
					{:else}
						Loading…
					{/if}
				</Heading>
				{#if loading}
					<Text size="sm" color="secondary">Loading your fleet…</Text>
				{:else if !error}
					<Text size="sm" color="secondary">{smartSummary}</Text>
				{/if}
			</div>
			<div class="fleet-header__actions">
				<Button variant="outline" onclick={() => void loadFleet()} disabled={loading}>
					Refresh
				</Button>
			</div>
		</header>

		<!-- Error state (full-page, replaces metrics + fleet) -->
		{#if error}
			<Alert variant="error" title="Failed to load fleet">{error}</Alert>
		{:else}
			<!-- Metric cards -->
			<div class="fleet-metrics">
				<Metric label="Live instances" value={String(liveInstances)} tone="accent" />
				<Metric label="Souls" value={String(soulCount)} tone="info" />
				<Metric label="MTD spend" value={`${formatSpend(mtdSpend)} cr`} tone="warning" />
				<Metric label="Federation" value={String(totalPeers)} tone="success" />
			</div>

			<!-- Fleet panel -->
			<Panel title="Fleet" headerLevel={2} variant="default">
				{#snippet actions()}
					<div class="fleet-tabs" role="tablist" aria-label="Fleet view mode">
						<button
							type="button"
							class="fleet-tab"
							role="tab"
							aria-selected={activeTab === 'cards'}
							onclick={() => (activeTab = 'cards')}
						>
							Cards
						</button>
						<button
							type="button"
							class="fleet-tab"
							role="tab"
							aria-selected={activeTab === 'table'}
							onclick={() => (activeTab = 'table')}
						>
							Table
						</button>
					</div>
				{/snippet}

				{#if loading}
					<div class="fleet-loading">
						<Spinner size="md" />
						<Text>Loading instances…</Text>
					</div>
				{:else if instances.length === 0}
					<!-- Zero-instance state -->
					<div class="fleet-empty">
						<Text size="sm" color="secondary">
							No instances yet. Create your first instance to start managing your fleet.
						</Text>
					</div>
				{:else if activeTab === 'cards'}
					<!-- Cards view -->
					<div class="fleet-cards" role="list" aria-label="Instance fleet">
						{#each instances as inst (inst.slug)}
							{@const budget = budgets[inst.slug]}
							{@const sparkVals = getSparkValues(inst)}
							<div class="fleet-cards__item" role="listitem">
								<FleetCard
									name={inst.slug}
									slug={inst.managed_lesser_domain ?? inst.hosted_base_domain}
									region={inst.hosted_region}
									version={inst.lesser_version}
									status={mapInstanceFleetStatus(inst)}
									metadata={buildCardMetadata(inst)}
									variant="elevated"
								>
									{#snippet cost()}
										{#if budget && budget.included_credits > 0}
											<CostGauge
												current={budget.used_credits}
												limit={budget.included_credits}
												formatValue={formatCreditValue}
												thresholds={COST_THRESHOLDS}
												label="Budget"
											/>
										{:else}
											<div class="fleet-no-budget">
												<Text size="xs" color="secondary">No budget set</Text>
												<Link
													{...linkProps(`/portal/instances/${inst.slug}`)}
													variant="default"
												>
													Set budget
												</Link>
											</div>
										{/if}
									{/snippet}
									{#snippet activity()}
										{#if sparkVals.length > 0}
											<Sparkline
												values={sparkVals}
												width={160}
												height={40}
												color="var(--ds-secondary-500)"
											/>
										{:else}
											<Text size="xs" color="secondary">No activity data yet</Text>
										{/if}
									{/snippet}
									{#snippet actions()}
										<Link {...linkProps(`/portal/instances/${inst.slug}`)} variant="default">
											Open
										</Link>
									{/snippet}
								</FleetCard>
							</div>
						{/each}

						<!-- New-instance CTA card -->
						<div class="fleet-cards__item fleet-cta-card" role="listitem">
							<div class="fleet-cta-card__inner">
								<Heading level={3} size="base">New instance</Heading>
								<Text size="sm" color="secondary">
									Reserve a slug to start provisioning managed hosting.
								</Text>
								<div class="fleet-cta-card__row">
									<TextField
										label="Slug"
										bind:value={createSlug}
										placeholder="my-instance"
									/>
									<Button
										variant="solid"
										onclick={() => void createInstance()}
										disabled={createLoading}
									>
										Create
									</Button>
								</div>
								{#if createError}
									<Alert variant="error" title="Create failed">{createError}</Alert>
								{/if}
							</div>
						</div>
					</div>
				{:else}
					<!-- Table view (accessible summary) -->
					<div class="fleet-table-wrap" role="region" aria-label="Instance fleet table">
						<table class="fleet-table">
							<thead>
								<tr>
									<th scope="col">Instance</th>
									<th scope="col">Status</th>
									<th scope="col">Region</th>
									<th scope="col">Version</th>
									<th scope="col">Spend</th>
									<th scope="col">Peers</th>
									<th scope="col"><span class="sr-only">Actions</span></th>
								</tr>
							</thead>
							<tbody>
								{#each instances as inst (inst.slug)}
									{@const budget = budgets[inst.slug]}
									{@const status = mapInstanceFleetStatus(inst)}
									<tr>
										<td>
											<Link {...linkProps(`/portal/instances/${inst.slug}`)} variant="default">
												{inst.slug}
											</Link>
										</td>
										<td>
											<span class="fleet-table__status" data-status={status}>
												{status}
											</span>
										</td>
										<td>{inst.hosted_region ?? '—'}</td>
										<td>{inst.lesser_version ?? '—'}</td>
										<td>
											{#if budget && budget.included_credits > 0}
												{formatSpend(budget.used_credits)} / {formatSpend(budget.included_credits)} cr
											{:else}
												—
											{/if}
										</td>
										<td>{inst.peers != null ? String(inst.peers) : '—'}</td>
										<td>
											<Link {...linkProps(`/portal/instances/${inst.slug}`)} variant="default">
												Open
											</Link>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</Panel>
		{/if}
	</div>

	<!-- ── Right rail ────────────────────────────────────────────────── -->

	<aside class="fleet-rail" aria-label="Fleet overview">
		<!-- Cost pulse panel -->
		<Panel title="Cost pulse" headerLevel={3} variant="flat" padding="sm">
			{#if !loading && instances.length > 0}
				<div class="fleet-rail__pulse">
					<span class="fleet-rail__live-dot" aria-label="Live"></span>
					<Text size="sm"><strong>{formatSpend(mtdSpend)} cr</strong> spent MTD</Text>
				</div>
				{#if instances.some((i) => (i.spark_cost?.length ?? 0) > 0)}
					<Sparkline
						values={instances
							.flatMap((i) => i.spark_cost ?? [])
							.slice(0, 30)}
						width={200}
						height={48}
						color="var(--ds-warning-500)"
					/>
				{:else}
					<Text size="xs" color="secondary">No cost-telemetry data yet</Text>
				{/if}
			{:else if loading}
				<Spinner size="sm" />
			{:else}
				<Text size="xs" color="secondary">Create an instance to see cost pulse</Text>
			{/if}
		</Panel>

		<!-- Right-now / Provisioning panel -->
		<Panel title="Right now" headerLevel={3} variant="flat" padding="sm">
			{#if loading}
				<Spinner size="sm" />
			{:else if provisioningInstances.length > 0}
				<ul class="fleet-rail__list">
					{#each provisioningInstances as pi (pi.slug)}
						<li class="fleet-rail__list-item">
							<span class="fleet-rail__list-dot" data-status="provisioning" aria-hidden="true"></span>
							<Text size="sm">
								<strong>{pi.slug}</strong> — Provisioning
							</Text>
						</li>
					{/each}
				</ul>
			{:else if instances.length === 0}
				<Text size="xs" color="secondary">No instances yet</Text>
			{:else}
				<Text size="xs" color="secondary">No instances provisioning</Text>
			{/if}
		</Panel>

		<!-- Heads-up / Alerts panel -->
		<Panel title="Heads up" headerLevel={3} variant="flat" padding="sm">
			{#if loading}
				<Spinner size="sm" />
			{:else if degradedInstances.length > 0 || overBudgetInstances.length > 0}
				<ul class="fleet-rail__list">
					{#each degradedInstances as di (di.slug)}
						<li class="fleet-rail__list-item">
							<span class="fleet-rail__list-dot" data-status="degraded" aria-hidden="true"></span>
							<Text size="sm">
								<strong>{di.slug}</strong> — needs attention
							</Text>
						</li>
					{/each}
					{#each overBudgetInstances as obi (obi.slug)}
						{@const b = budgets[obi.slug]}
						{#if !degradedInstances.some((d) => d.slug === obi.slug)}
							<li class="fleet-rail__list-item">
								<span class="fleet-rail__list-dot" data-status="warning" aria-hidden="true"></span>
								<Text size="sm">
									<strong>{obi.slug}</strong> — {b
										? `${Math.round((b.used_credits / b.included_credits) * 100)}% of budget`
										: 'over budget'}
								</Text>
							</li>
						{/if}
					{/each}
				</ul>
			{:else if instances.length === 0}
				<Text size="xs" color="secondary">No instances yet</Text>
			{:else}
				<Text size="xs" color="secondary">All clear</Text>
			{/if}
		</Panel>
	</aside>
</div>

<!-- =========================================================================
     STYLES
     ========================================================================= -->

<style>
	/* ── Page layout ────────────────────────────────────────────────── */

	.fleet-page {
		display: grid;
		grid-template-columns: 1fr 260px;
		gap: var(--gr-spacing-scale-6, 1.5rem);
		align-items: start;
		padding: var(--gr-spacing-scale-6, 1.5rem) 0;
	}

	.fleet-main {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6, 1.5rem);
		min-width: 0;
	}

	.fleet-rail {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4, 1rem);
		position: sticky;
		top: var(--gr-spacing-scale-6, 1.5rem);
	}

	/* ── Greeting header ────────────────────────────────────────────── */

	.fleet-header {
		display: flex;
		gap: var(--gr-spacing-scale-4, 1rem);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.fleet-header__text {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2, 0.5rem);
		max-width: 60ch;
	}

	.fleet-header__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2, 0.5rem);
		align-items: center;
		flex-shrink: 0;
	}

	/* ── Metric cards ───────────────────────────────────────────────── */

	.fleet-metrics {
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		gap: var(--gr-spacing-scale-4, 1rem);
	}

	/* ── Fleet tabs ─────────────────────────────────────────────────── */

	.fleet-tabs {
		display: flex;
		gap: 0;
		border-bottom: 1px solid var(--gr-semantic-border-default, var(--gr-color-gray-200));
	}

	.fleet-tab {
		background: none;
		border: none;
		border-bottom: 2px solid transparent;
		padding: var(--gr-spacing-scale-2, 0.5rem) var(--gr-spacing-scale-3, 0.75rem);
		font-family: inherit;
		font-size: var(--gr-typography-fontSize-sm, 0.875rem);
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		cursor: pointer;
		transition: color 0.15s, border-color 0.15s;
	}

	.fleet-tab:hover {
		color: var(--gr-semantic-text-primary, var(--gr-color-gray-900));
	}

	.fleet-tab[aria-selected='true'] {
		color: var(--gr-semantic-text-primary, var(--gr-color-gray-900));
		border-bottom-color: var(--ds-secondary-500, var(--gr-semantic-border-selected, var(--gr-color-purple-500)));
	}

	/* ── Cards grid ─────────────────────────────────────────────────── */

	.fleet-cards {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(380px, 100%), 1fr));
		gap: var(--gr-spacing-scale-4, 1rem);
	}

	.fleet-cards__item {
		display: contents;
	}

	/* ── New-instance CTA card ──────────────────────────────────────── */

	.fleet-cta-card {
		display: block;
	}

	.fleet-cta-card__inner {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3, 0.75rem);
		padding: var(--gr-spacing-scale-4, 1rem);
		border: 1px dashed var(--gr-semantic-border-default, var(--gr-color-gray-300));
		border-radius: var(--gr-radii-md, 0.5rem);
		background: var(--gr-semantic-background-surface, var(--gr-color-base-white));
	}

	.fleet-cta-card__row {
		display: flex;
		gap: var(--gr-spacing-scale-2, 0.5rem);
		align-items: flex-end;
		flex-wrap: wrap;
	}

	/* ── Empty / loading ────────────────────────────────────────────── */

	.fleet-loading {
		display: flex;
		gap: var(--gr-spacing-scale-3, 0.75rem);
		align-items: center;
		padding: var(--gr-spacing-scale-4, 1rem) 0;
	}

	.fleet-empty {
		padding: var(--gr-spacing-scale-4, 1rem) 0;
	}

	/* ── No-budget action path ──────────────────────────────────────── */

	.fleet-no-budget {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--gr-spacing-scale-1, 0.25rem);
	}

	/* ── Table view ─────────────────────────────────────────────────── */

	.fleet-table-wrap {
		overflow-x: auto;
	}

	.fleet-table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--gr-typography-fontSize-sm, 0.875rem);
	}

	.fleet-table th {
		text-align: left;
		padding: var(--gr-spacing-scale-2, 0.5rem) var(--gr-spacing-scale-3, 0.75rem);
		border-bottom: 2px solid var(--gr-semantic-border-default, var(--gr-color-gray-200));
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		font-weight: var(--gr-typography-fontWeight-semibold, 600);
		white-space: nowrap;
	}

	.fleet-table td {
		padding: var(--gr-spacing-scale-2, 0.5rem) var(--gr-spacing-scale-3, 0.75rem);
		border-bottom: 1px solid var(--gr-semantic-border-subtle, var(--gr-color-gray-100));
		white-space: nowrap;
	}

	.fleet-table tbody tr:hover {
		background: var(--gr-semantic-background-hover, var(--gr-color-gray-50));
	}

	/* CSP-safe status dot in table cells */
	.fleet-table__status {
		display: inline-flex;
		align-items: center;
		gap: var(--gr-spacing-scale-1, 0.25rem);
	}

	.fleet-table__status::before {
		content: '';
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--gr-color-gray-400);
		flex-shrink: 0;
	}

	.fleet-table__status[data-status='healthy']::before {
		background: var(--ds-success-500, var(--gr-color-green-500));
	}

	.fleet-table__status[data-status='provisioning']::before {
		background: var(--ds-info-400, var(--gr-color-blue-400));
	}

	.fleet-table__status[data-status='warning']::before {
		background: var(--ds-warning-500, var(--gr-color-amber-500));
	}

	.fleet-table__status[data-status='degraded']::before {
		background: var(--ds-error-500, var(--gr-color-red-500));
	}

	.fleet-table__status[data-status='offline']::before {
		background: var(--gr-color-gray-400);
	}

	.fleet-table__status[data-status='unknown']::before {
		background: var(--gr-color-gray-300);
	}

	/* ── Right rail panels ──────────────────────────────────────────── */

	.fleet-rail__pulse {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2, 0.5rem);
		margin-bottom: var(--gr-spacing-scale-3, 0.75rem);
	}

	/* CSP-safe live dot (CSS-only, no inline style) */
	.fleet-rail__live-dot {
		display: inline-block;
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--ds-success-500, var(--gr-color-green-500));
		flex-shrink: 0;
		animation: fleet-pulse 2s ease-in-out infinite;
	}

	@keyframes fleet-pulse {
		0%, 100% { opacity: 1; }
		50% { opacity: 0.4; }
	}

	/* CSP-safe status dots for right-rail list items */
	.fleet-rail__list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2, 0.5rem);
	}

	.fleet-rail__list-item {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2, 0.5rem);
	}

	.fleet-rail__list-dot {
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
		background: var(--gr-color-gray-400);
	}

	.fleet-rail__list-dot[data-status='provisioning'] {
		background: var(--ds-info-400, var(--gr-color-blue-400));
	}

	.fleet-rail__list-dot[data-status='degraded'] {
		background: var(--ds-error-500, var(--gr-color-red-500));
	}

	.fleet-rail__list-dot[data-status='warning'] {
		background: var(--ds-warning-500, var(--gr-color-amber-500));
	}

	/* ── Screen reader only ─────────────────────────────────────────── */

	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
		border: 0;
	}

	/* ── Responsive: collapse metric grid below 720px ───────────────── */

	@media (max-width: 960px) {
		.fleet-page {
			grid-template-columns: 1fr;
		}

		.fleet-rail {
			position: static;
		}

		.fleet-metrics {
			grid-template-columns: repeat(2, 1fr);
		}
	}

	@media (max-width: 520px) {
		.fleet-metrics {
			grid-template-columns: 1fr;
		}

		.fleet-cards {
			grid-template-columns: 1fr;
		}
	}
</style>
