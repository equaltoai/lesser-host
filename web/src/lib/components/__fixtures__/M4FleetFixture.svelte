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
	import Eyebrow from 'src/lib/components/primitives/Eyebrow.svelte';

	import type { FleetCardMetadataItem } from 'src/lib/greater/host-platform';

	const COST_THRESHOLDS = { warning: 0.75, danger: 0.95 } as const;

	/* ========================================================================
	 * Fixture data
	 * ======================================================================*/

	const displayName = 'Aron';

	const timeOfDayGreeting = 'Good afternoon';

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
			peakDailyUsers: 1243,
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
			peakDailyUsers: 312,
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
			peakDailyUsers: 0,
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
			peakDailyUsers: 0,
			posts24h: 0,
			peers: 0,
			severed: 0,
			sparkCost: [],
			sparkActivity: [],
			budgetLimit: 0,
			budgetUsed: 0,
		},
	] as const;

	/* ========================================================================
	 * Fleet cost sparkline — element-wise sum across instances.
	 * ======================================================================*/

	const fleetCostSparkline: number[] = (() => {
		const maxLen = Math.max(0, ...instances.map((i) => i.sparkCost.length));
		if (maxLen === 0) return [];
		const result = new Array(maxLen).fill(0);
		for (const inst of instances) {
			for (let day = 0; day < inst.sparkCost.length; day += 1) {
				result[day] += inst.sparkCost[day];
			}
		}
		return result;
	})();

	const buildCardMetadata = (inst: (typeof instances)[number]): FleetCardMetadataItem[] => {
		const items: FleetCardMetadataItem[] = [];
		items.push({ key: 'Domain', value: inst.domain });
		items.push({ key: 'Region', value: inst.region });
		items.push({
			key: 'Active users',
			value: inst.peakDailyUsers > 0 ? String(inst.peakDailyUsers) : '—',
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
	};

	const totalPeers = instances.reduce((sum, i) => sum + i.peers, 0);
	const instancesWithPeers = instances.filter((i) => i.peers > 0).length;
	const mtdSpend = instances.reduce((sum, i) => sum + i.budgetUsed, 0);
	const totalBudgetCap = instances.reduce((sum, i) => sum + i.budgetLimit, 0);
	const budgetedCount = instances.filter((i) => i.budgetLimit > 0).length;
	const liveInstances = instances.filter((i) => i.status === 'healthy').length;
	const provisioningInstances = instances.filter((i) => i.status === 'provisioning');
	const degradedInstances = instances.filter((i) => i.status === 'warning');
	const overBudgetInstances = instances.filter((i) => {
		if (i.budgetLimit <= 0) return false;
		return i.budgetUsed / i.budgetLimit >= 0.85;
	});

	const budgetPct = totalBudgetCap > 0 ? Math.min(100, Math.round((mtdSpend / totalBudgetCap) * 100)) : 0;

	const projectedEOM: number | undefined = (() => {
		if (fleetCostSparkline.length === 0) return undefined;
		const avgDaily = fleetCostSparkline.reduce((s, v) => s + v, 0) / fleetCostSparkline.length;
		// Project from mid-month: ~15 days remaining
		return mtdSpend + avgDaily * 15;
	})();

	const formatSpend = (value: number): string => {
		if (value >= 1000) return `${(value / 1000).toFixed(1)}k`;
		return value.toLocaleString();
	};

	const formatCreditValue = (value: number, _currency?: string): string => {
		return `${formatSpend(value)} cr`;
	};
</script>

<div class="fleet-page">
	<!-- ── Main content ──────────────────────────────────────────────── -->
	<div class="fleet-main">
		<!-- Greeting header -->
		<header class="fleet-header">
			<div class="fleet-header__text">
				<Eyebrow>CUSTOMER PORTAL</Eyebrow>
				<Heading level={1} size="2xl" class="fleet-greeting">
					{timeOfDayGreeting}, {displayName}.
				</Heading>
				<Text size="sm" color="secondary">{smartSummary}</Text>
			</div>
			<div class="fleet-header__actions">
				<Button variant="solid" disabled={true}>Provision instance</Button>
				<Button variant="outline" disabled={true}>Refresh</Button>
			</div>
		</header>

		<!-- Metric cards -->
		<div class="fleet-metrics">
			<Metric
				label="Live instances"
				value={String(liveInstances)}
				sub={`of ${instances.length} total`}
				tone="accent"
			/>
			<Metric
				label="Souls"
				value="6"
				sub="deployed agents"
				tone="info"
			/>
			<Metric
				label="MTD spend"
				value={`${formatSpend(mtdSpend)} cr`}
				sub={`of ${formatSpend(totalBudgetCap)} cr budget`}
				tone="warning"
			/>
			<Metric
				label="Federation"
				value={String(totalPeers)}
				sub={`across ${instancesWithPeers} instance${instancesWithPeers !== 1 ? 's' : ''}`}
				tone="success"
			/>
		</div>

		<!-- Fleet panel -->
		<Panel title="Fleet" headerLevel={2} variant="default">
			{#snippet actions()}
				<div class="fleet-tabs" role="tablist" aria-label="Fleet view mode">
					<button type="button" class="fleet-tab" role="tab" aria-selected="true">Cards</button>
					<button type="button" class="fleet-tab" role="tab" aria-selected="false">Table</button>
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
		<!-- Cost pulse panel -->
		<Panel title="Cost pulse" headerLevel={3} variant="flat" padding="sm">
			<div class="fleet-rail__eyebrow">
				<Text size="xs" color="secondary">May 2026</Text>
			</div>
			<div class="fleet-rail__pulse">
				<span class="fleet-rail__live-dot" aria-label="Live"></span>
				<Text size="sm">
					<strong>{formatSpend(mtdSpend)} cr</strong>
					{#if totalBudgetCap > 0}
						 of {formatSpend(totalBudgetCap)} cr budget
					{/if}
				</Text>
			</div>
			{#if totalBudgetCap > 0}
				<div class="fleet-rail__budget-bar">
					<div
						class="fleet-rail__budget-fill"
						data-ratio={budgetPct}
						role="progressbar"
						aria-valuenow={budgetPct}
						aria-valuemin="0"
						aria-valuemax="100"
						aria-label={`${budgetPct}% of budget used`}
					></div>
				</div>
				<Text size="xs" color="secondary">
					{budgetPct}% used
					{#if projectedEOM != null}
						 — proj. EOM {formatSpend(projectedEOM)} cr
					{/if}
				</Text>
			{/if}
			<div class="fleet-rail__sparkline">
				<Sparkline
					values={fleetCostSparkline}
					width={200}
					height={48}
					color="var(--ds-warning-500)"
				/>
			</div>
		</Panel>

		<!-- Right-now panel -->
		<Panel title="Right now" headerLevel={3} variant="flat" padding="sm">
			{#if provisioningInstances.length > 0}
				<ul class="fleet-rail__list">
					{#each provisioningInstances as pi (pi.slug)}
						<li class="fleet-rail__list-item">
							<span class="fleet-rail__list-dot" data-status="provisioning" aria-hidden="true"></span>
							<div class="fleet-rail__list-text">
								<Text size="sm"><strong>{pi.slug}</strong></Text>
								<Text size="xs" color="secondary">
									{#if pi.region}{pi.region}{/if}
									— provisioning
									— <Link href={`/portal/instances/${pi.slug}`} variant="default">View details</Link>
								</Text>
							</div>
						</li>
					{/each}
				</ul>
			{:else}
				<Text size="xs" color="secondary">No instances provisioning</Text>
			{/if}
		</Panel>

		<!-- Heads-up panel -->
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

	.fleet-rail__eyebrow {
		margin-bottom: var(--gr-spacing-scale-1, 0.25rem);
	}

	.fleet-rail__pulse {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2, 0.5rem);
		margin-bottom: var(--gr-spacing-scale-2, 0.5rem);
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

	.fleet-rail__budget-bar {
		width: 100%;
		height: 4px;
		border-radius: 2px;
		background: var(--gr-semantic-background-subtle, var(--gr-color-gray-100));
		margin-bottom: var(--gr-spacing-scale-1, 0.25rem);
		overflow: hidden;
	}

	.fleet-rail__budget-fill {
		height: 100%;
		border-radius: 2px;
		background: var(--ds-warning-500, var(--gr-color-amber-500));
	}

	.fleet-rail__budget-fill[data-ratio='0'] { width: 0%; }
	.fleet-rail__budget-fill[data-ratio='1'] { width: 1%; }
	.fleet-rail__budget-fill[data-ratio='2'] { width: 2%; }
	.fleet-rail__budget-fill[data-ratio='3'] { width: 3%; }
	.fleet-rail__budget-fill[data-ratio='4'] { width: 4%; }
	.fleet-rail__budget-fill[data-ratio='5'] { width: 5%; }
	.fleet-rail__budget-fill[data-ratio='6'] { width: 6%; }
	.fleet-rail__budget-fill[data-ratio='7'] { width: 7%; }
	.fleet-rail__budget-fill[data-ratio='8'] { width: 8%; }
	.fleet-rail__budget-fill[data-ratio='9'] { width: 9%; }
	.fleet-rail__budget-fill[data-ratio='10'] { width: 10%; }
	.fleet-rail__budget-fill[data-ratio='11'] { width: 11%; }
	.fleet-rail__budget-fill[data-ratio='12'] { width: 12%; }
	.fleet-rail__budget-fill[data-ratio='13'] { width: 13%; }
	.fleet-rail__budget-fill[data-ratio='14'] { width: 14%; }
	.fleet-rail__budget-fill[data-ratio='15'] { width: 15%; }
	.fleet-rail__budget-fill[data-ratio='16'] { width: 16%; }
	.fleet-rail__budget-fill[data-ratio='17'] { width: 17%; }
	.fleet-rail__budget-fill[data-ratio='18'] { width: 18%; }
	.fleet-rail__budget-fill[data-ratio='19'] { width: 19%; }
	.fleet-rail__budget-fill[data-ratio='20'] { width: 20%; }
	.fleet-rail__budget-fill[data-ratio='21'] { width: 21%; }
	.fleet-rail__budget-fill[data-ratio='22'] { width: 22%; }
	.fleet-rail__budget-fill[data-ratio='23'] { width: 23%; }
	.fleet-rail__budget-fill[data-ratio='24'] { width: 24%; }
	.fleet-rail__budget-fill[data-ratio='25'] { width: 25%; }
	.fleet-rail__budget-fill[data-ratio='26'] { width: 26%; }
	.fleet-rail__budget-fill[data-ratio='27'] { width: 27%; }
	.fleet-rail__budget-fill[data-ratio='28'] { width: 28%; }
	.fleet-rail__budget-fill[data-ratio='29'] { width: 29%; }
	.fleet-rail__budget-fill[data-ratio='30'] { width: 30%; }
	.fleet-rail__budget-fill[data-ratio='31'] { width: 31%; }
	.fleet-rail__budget-fill[data-ratio='32'] { width: 32%; }
	.fleet-rail__budget-fill[data-ratio='33'] { width: 33%; }
	.fleet-rail__budget-fill[data-ratio='34'] { width: 34%; }
	.fleet-rail__budget-fill[data-ratio='35'] { width: 35%; }
	.fleet-rail__budget-fill[data-ratio='36'] { width: 36%; }
	.fleet-rail__budget-fill[data-ratio='37'] { width: 37%; }
	.fleet-rail__budget-fill[data-ratio='38'] { width: 38%; }
	.fleet-rail__budget-fill[data-ratio='39'] { width: 39%; }
	.fleet-rail__budget-fill[data-ratio='40'] { width: 40%; }
	.fleet-rail__budget-fill[data-ratio='41'] { width: 41%; }
	.fleet-rail__budget-fill[data-ratio='42'] { width: 42%; }
	.fleet-rail__budget-fill[data-ratio='43'] { width: 43%; }
	.fleet-rail__budget-fill[data-ratio='44'] { width: 44%; }
	.fleet-rail__budget-fill[data-ratio='45'] { width: 45%; }
	.fleet-rail__budget-fill[data-ratio='46'] { width: 46%; }
	.fleet-rail__budget-fill[data-ratio='47'] { width: 47%; }
	.fleet-rail__budget-fill[data-ratio='48'] { width: 48%; }
	.fleet-rail__budget-fill[data-ratio='49'] { width: 49%; }
	.fleet-rail__budget-fill[data-ratio='50'] { width: 50%; }
	.fleet-rail__budget-fill[data-ratio='51'] { width: 51%; }
	.fleet-rail__budget-fill[data-ratio='52'] { width: 52%; }
	.fleet-rail__budget-fill[data-ratio='53'] { width: 53%; }
	.fleet-rail__budget-fill[data-ratio='54'] { width: 54%; }
	.fleet-rail__budget-fill[data-ratio='55'] { width: 55%; }
	.fleet-rail__budget-fill[data-ratio='56'] { width: 56%; }
	.fleet-rail__budget-fill[data-ratio='57'] { width: 57%; }
	.fleet-rail__budget-fill[data-ratio='58'] { width: 58%; }
	.fleet-rail__budget-fill[data-ratio='59'] { width: 59%; }
	.fleet-rail__budget-fill[data-ratio='60'] { width: 60%; }
	.fleet-rail__budget-fill[data-ratio='61'] { width: 61%; }
	.fleet-rail__budget-fill[data-ratio='62'] { width: 62%; }
	.fleet-rail__budget-fill[data-ratio='63'] { width: 63%; }
	.fleet-rail__budget-fill[data-ratio='64'] { width: 64%; }
	.fleet-rail__budget-fill[data-ratio='65'] { width: 65%; }
	.fleet-rail__budget-fill[data-ratio='66'] { width: 66%; }
	.fleet-rail__budget-fill[data-ratio='67'] { width: 67%; }
	.fleet-rail__budget-fill[data-ratio='68'] { width: 68%; }
	.fleet-rail__budget-fill[data-ratio='69'] { width: 69%; }
	.fleet-rail__budget-fill[data-ratio='70'] { width: 70%; }
	.fleet-rail__budget-fill[data-ratio='71'] { width: 71%; }
	.fleet-rail__budget-fill[data-ratio='72'] { width: 72%; }
	.fleet-rail__budget-fill[data-ratio='73'] { width: 73%; }
	.fleet-rail__budget-fill[data-ratio='74'] { width: 74%; }
	.fleet-rail__budget-fill[data-ratio='75'] { width: 75%; }
	.fleet-rail__budget-fill[data-ratio='76'] { width: 76%; }
	.fleet-rail__budget-fill[data-ratio='77'] { width: 77%; }
	.fleet-rail__budget-fill[data-ratio='78'] { width: 78%; }
	.fleet-rail__budget-fill[data-ratio='79'] { width: 79%; }
	.fleet-rail__budget-fill[data-ratio='80'] { width: 80%; }
	.fleet-rail__budget-fill[data-ratio='81'] { width: 81%; }
	.fleet-rail__budget-fill[data-ratio='82'] { width: 82%; }
	.fleet-rail__budget-fill[data-ratio='83'] { width: 83%; }
	.fleet-rail__budget-fill[data-ratio='84'] { width: 84%; }
	.fleet-rail__budget-fill[data-ratio='85'] { width: 85%; }
	.fleet-rail__budget-fill[data-ratio='86'] { width: 86%; }
	.fleet-rail__budget-fill[data-ratio='87'] { width: 87%; }
	.fleet-rail__budget-fill[data-ratio='88'] { width: 88%; }
	.fleet-rail__budget-fill[data-ratio='89'] { width: 89%; }
	.fleet-rail__budget-fill[data-ratio='90'] { width: 90%; }
	.fleet-rail__budget-fill[data-ratio='91'] { width: 91%; }
	.fleet-rail__budget-fill[data-ratio='92'] { width: 92%; }
	.fleet-rail__budget-fill[data-ratio='93'] { width: 93%; }
	.fleet-rail__budget-fill[data-ratio='94'] { width: 94%; }
	.fleet-rail__budget-fill[data-ratio='95'] { width: 95%; }
	.fleet-rail__budget-fill[data-ratio='96'] { width: 96%; }
	.fleet-rail__budget-fill[data-ratio='97'] { width: 97%; }
	.fleet-rail__budget-fill[data-ratio='98'] { width: 98%; }
	.fleet-rail__budget-fill[data-ratio='99'] { width: 99%; }
	.fleet-rail__budget-fill[data-ratio='100'] { width: 100%; }

	.fleet-rail__sparkline {
		margin-top: var(--gr-spacing-scale-2, 0.5rem);
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
		align-items: flex-start;
		gap: var(--gr-spacing-scale-2, 0.5rem);
	}

	.fleet-rail__list-text {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1, 0.25rem);
	}

	.fleet-rail__list-dot {
		display: inline-block;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
		margin-top: 0.35em;
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
