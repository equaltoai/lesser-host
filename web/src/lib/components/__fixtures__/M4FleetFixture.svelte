<!--
@component
M4FleetFixture — isolated visual fixture rendering the M4 Fleet UI surface
with realistic mock data for design-fidelity evidence capture.

Renders the Fleet greeting, four metric cards, three InstanceCard examples
(in Cards view), and the right rail with Cost pulse / Right-now / Heads-up
panels. All data is static fixture data — no API calls.

Not mounted by any customer portal route — this file exists solely for
local visual review and headless PNG capture at 1440×900.

Strict-CSP safe: no inline styles, no inline event handlers.

@license AGPL-3.0-only
@public
-->

<script lang="ts">
	import { Alert, Badge, Button, Heading, Link, Spinner, Text, TextField } from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';
	import FleetCard from 'src/lib/components/FleetCard.svelte';
	import CostGauge from 'src/lib/components/CostGauge.svelte';
	import Sparkline from 'src/lib/components/primitives/Sparkline.svelte';
	import Metric from 'src/lib/components/primitives/Metric.svelte';

	import type { FleetCardMetadataItem } from 'src/lib/greater/host-platform';

	const COST_THRESHOLDS = { warning: 0.75, danger: 0.95 } as const;

	/* ========================================================================
	 * Fixture data
	 * ======================================================================*/

	const displayName = 'Aron';

	const smartSummary =
		'You have 4 instances across 3 regions (3 live) with 6 souls deployed. 1 instance needs attention. 1 approaching or exceeding budget.';

	const sparkActivity1 = [3, 8, 5, 12, 9, 14, 10];
	const sparkActivity2 = [1, 2, 4, 3, 7, 6, 8];
	const sparkActivity3 = [12, 10, 14, 13, 15, 11, 18];
	const sparkCost1 = [42, 38, 55, 48, 62, 51, 47];
	const sparkCost2 = [110, 105, 118, 125, 130, 122, 135];

	const instances = [
		{
			slug: 'my-instance',
			domain: 'my-instance.greater.website',
			region: 'us-east-1',
			version: 'v2.4.1',
			status: 'healthy' as const,
			activeUsers: 1243,
			posts24h: 8901,
			peers: 42,
			severed: 0,
			sparkCost: sparkCost1,
			sparkActivity: sparkActivity1,
			budgetLimit: 500,
			budgetUsed: 210,
		},
		{
			slug: 'staging-env',
			domain: 'staging-env.greater.website',
			region: 'eu-west-1',
			version: 'v2.4.0',
			status: 'warning' as const,
			activeUsers: 312,
			posts24h: 1204,
			peers: 18,
			severed: 2,
			sparkCost: sparkCost2,
			sparkActivity: sparkActivity2,
			budgetLimit: 200,
			budgetUsed: 187,
		},
		{
			slug: 'demo-site',
			domain: 'demo-site.greater.website',
			region: 'ap-southeast-1',
			version: 'v2.4.1',
			status: 'healthy' as const,
			activeUsers: 0,
			posts24h: 0,
			peers: 0,
			severed: 0,
			sparkCost: [],
			sparkActivity: sparkActivity3,
			budgetLimit: 0,
			budgetUsed: 0,
		},
		{
			slug: 'dev-sandbox',
			domain: 'dev-sandbox.greater.website',
			region: 'us-west-2',
			version: 'v2.3.9',
			status: 'provisioning' as const,
			activeUsers: 0,
			posts24h: 0,
			peers: 0,
			severed: 0,
			sparkCost: [],
			sparkActivity: [],
			budgetLimit: 0,
			budgetUsed: 0,
		},
	] as const;

	function buildCardMetadata(inst: (typeof instances)[number]): FleetCardMetadataItem[] {
		const items: FleetCardMetadataItem[] = [];
		items.push({ key: 'Domain', value: inst.domain });
		items.push({ key: 'Region', value: inst.region });
		items.push({
			key: 'Active users',
			value: inst.activeUsers > 0 ? String(inst.activeUsers) : '—',
		});
		items.push({
			key: 'Posts 24h',
			value: inst.posts24h > 0 ? String(inst.posts24h) : '—',
		});
		const fedParts: string[] = [];
		if (inst.peers > 0) fedParts.push(`${inst.peers} peers`);
		if (inst.severed > 0) fedParts.push(`${inst.severed} severed`);
		items.push({ key: 'Federation', value: fedParts.length > 0 ? fedParts.join(', ') : '—' });
		items.push({ key: 'Version', value: inst.version });
		return items;
	}

	const totalPeers = instances.reduce((sum, i) => sum + i.peers, 0);
	const mtdSpend = instances.reduce((sum, i) => sum + i.budgetUsed, 0);
	const liveInstances = instances.filter(
		(i) => i.status === 'healthy'
	).length;
	const provisioningInstances = instances.filter(
		(i) => i.status === 'provisioning'
	);
	const degradedInstances = instances.filter(
		(i) => i.status === 'warning'
	);
	const overBudgetInstances = instances.filter((i) => {
		if (i.budgetLimit <= 0) return false;
		return i.budgetUsed / i.budgetLimit >= 0.85;
	});

	function formatSpend(value: number): string {
		if (value >= 1000) return `${(value / 1000).toFixed(1)}k`;
		return value.toLocaleString();
	}
	function formatCreditValue(value: number, _currency?: string): string {
		return `${formatSpend(value)} cr`;
	}
</script>

<div class="fleet-page">
	<!-- ── Main content ──────────────────────────────────────────────── -->
	<div class="fleet-main">
		<!-- Greeting header -->
		<header class="fleet-header">
			<div class="fleet-header__text">
				<Heading level={1} size="2xl">Welcome back, {displayName}</Heading>
				<Text size="sm" color="secondary">{smartSummary}</Text>
			</div>
			<div class="fleet-header__actions">
				<Button variant="outline" disabled={true}>Refresh</Button>
			</div>
		</header>

		<!-- Metric cards -->
		<div class="fleet-metrics">
			<Metric label="Live instances" value={String(liveInstances)} tone="accent" />
			<Metric label="Souls" value="6" tone="info" />
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
						aria-selected="true"
					>
						Cards
					</button>
					<button
						type="button"
						class="fleet-tab"
						role="tab"
						aria-selected="false"
					>
						Table
					</button>
				</div>
			{/snippet}

			<div class="fleet-cards" role="list" aria-label="Instance fleet">
				{#each instances as inst (inst.slug)}
					<div class="fleet-cards__item" role="listitem">
						<FleetCard
							name={inst.slug}
							slug={inst.domain}
							region={inst.region}
							version={inst.version}
							status={inst.status}
							metadata={buildCardMetadata(inst)}
							variant="elevated"
						>
							{#snippet cost()}
								{#if inst.budgetLimit > 0}
									<CostGauge
										current={inst.budgetUsed}
										limit={inst.budgetLimit}
										formatValue={formatCreditValue}
										thresholds={COST_THRESHOLDS}
										label="Budget"
									/>
								{:else}
									<div class="fleet-no-budget">
										<Text size="xs" color="secondary">No budget set</Text>
										<Text size="xs" color="secondary">Set budget →</Text>
									</div>
								{/if}
							{/snippet}
							{#snippet activity()}
								{#if inst.sparkActivity.length > 0}
									<Sparkline
										values={[...inst.sparkActivity]}
										width={160}
										height={40}
										color="var(--ds-secondary-500)"
									/>
								{:else}
									<Text size="xs" color="secondary">No activity data yet</Text>
								{/if}
							{/snippet}
							{#snippet actions()}
								<Link href={`/portal/instances/${inst.slug}`} variant="default">Open</Link>
							{/snippet}
						</FleetCard>
					</div>
				{/each}

				<!-- New-instance CTA card (stub, not interactive in fixture) -->
				<div class="fleet-cards__item fleet-cta-card" role="listitem">
					<div class="fleet-cta-card__inner">
						<Heading level={3} size="base">New instance</Heading>
						<Text size="sm" color="secondary">
							Reserve a slug to start provisioning managed hosting.
						</Text>
						<div class="fleet-cta-card__row">
							<TextField label="Slug" value="my-instance" disabled={true} />
							<Button variant="solid" disabled={true}>Create</Button>
						</div>
					</div>
				</div>
			</div>
		</Panel>
	</div>

	<!-- ── Right rail ────────────────────────────────────────────────── -->
	<aside class="fleet-rail" aria-label="Fleet overview">
		<Panel title="Cost pulse" headerLevel={3} variant="flat" padding="sm">
			<div class="fleet-rail__pulse">
				<span class="fleet-rail__live-dot" aria-label="Live"></span>
				<Text size="sm"><strong>{formatSpend(mtdSpend)} cr</strong> spent MTD</Text>
			</div>
			<Sparkline
				values={[42, 38, 55, 48, 62, 51, 47, 52, 44, 58, 49, 63, 57, 50]}
				width={200}
				height={48}
				color="var(--ds-warning-500)"
			/>
		</Panel>

		<Panel title="Right now" headerLevel={3} variant="flat" padding="sm">
			{#if provisioningInstances.length > 0}
				<ul class="fleet-rail__list">
					{#each provisioningInstances as pi (pi.slug)}
						<li class="fleet-rail__list-item">
							<span class="fleet-rail__list-dot" data-status="provisioning" aria-hidden="true"></span>
							<Text size="sm"><strong>{pi.slug}</strong> — Provisioning</Text>
						</li>
					{/each}
				</ul>
			{:else}
				<Text size="xs" color="secondary">No instances provisioning</Text>
			{/if}
		</Panel>

		<Panel title="Heads up" headerLevel={3} variant="flat" padding="sm">
			{#if degradedInstances.length > 0 || overBudgetInstances.length > 0}
				<ul class="fleet-rail__list">
					{#each degradedInstances as di (di.slug)}
						<li class="fleet-rail__list-item">
							<span class="fleet-rail__list-dot" data-status="degraded" aria-hidden="true"></span>
							<Text size="sm"><strong>{di.slug}</strong> — needs attention</Text>
						</li>
					{/each}
					{#each overBudgetInstances as obi (obi.slug)}
						<li class="fleet-rail__list-item">
							<span class="fleet-rail__list-dot" data-status="warning" aria-hidden="true"></span>
							<Text size="sm">
								<strong>{obi.slug}</strong> — {Math.round((obi.budgetUsed / obi.budgetLimit) * 100)}% of budget
							</Text>
						</li>
					{/each}
				</ul>
			{:else}
				<Text size="xs" color="secondary">All clear</Text>
			{/if}
		</Panel>
	</aside>
</div>

<style>
	/* Replicated from PortalFleet.svelte — fixture must be self-contained */
	.fleet-page {
		display: grid;
		grid-template-columns: 1fr 260px;
		gap: var(--gr-spacing-scale-6, 1.5rem);
		align-items: start;
		padding: var(--gr-spacing-scale-6, 1.5rem);
		max-width: 1200px;
		margin: 0 auto;
		min-height: 100vh;
		background: var(--ds-bg-canvas, var(--gr-color-base-white, #fcf7f0));
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

	.fleet-metrics {
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		gap: var(--gr-spacing-scale-4, 1rem);
	}

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

	.fleet-tab[aria-selected='true'] {
		color: var(--gr-semantic-text-primary, var(--gr-color-gray-900));
		border-bottom-color: var(--ds-secondary-500, var(--gr-semantic-border-selected, var(--gr-color-purple-500)));
	}

	.fleet-cards {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(380px, 100%), 1fr));
		gap: var(--gr-spacing-scale-4, 1rem);
	}

	.fleet-cards__item {
		display: contents;
	}

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

	.fleet-no-budget {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--gr-spacing-scale-1, 0.25rem);
	}

	.fleet-rail__pulse {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2, 0.5rem);
		margin-bottom: var(--gr-spacing-scale-3, 0.75rem);
	}

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
</style>
