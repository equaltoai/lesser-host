<!--
@component
InstanceCost — Instance Detail Cost & usage tab (M8 redesign).

Project 42 M8 replaces the prior M3.12 telemetry view with the design:
  - Three header cards: MTD spend vs budget, Compute GB-sec, Egress GB
  - "Where the dollars go" breakdown table with per-service progress bars
  - Budget alarms panel with warning/critical/cap threshold rows

Data posture:
  - Budget + usage summary from existing portalGetBudgetMonth / portalGetUsageSummary.
    The budget API is credit-denominated; this surface labels credits honestly and
    does not fabricate a dollar conversion.
  - Cost telemetry from portalGetInstanceCost provides dollar-denominated totals
    and per-entry metrics. Compute (GB-sec) and egress (GB) are derived from
    entry.metrics[] when unit data exists; otherwise explicit unavailable states
    are rendered.
  - Budget alarm switches are disabled — no persistence endpoint exists yet.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Multi-tenant isolation: cost endpoint enforces per-slug ownership server-side.
  - Trust-API instance-auth untouched.
  - No on-chain code path changed.
  - No framework local patches; consumes existing API client modules,
    UI primitives, and shell components through released surfaces.

Source: Issue equaltoai/lesser-host#542
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { BudgetMonthResponse, UsageSummaryResponse, PortalCostResponse, CostDayEntry, CostAttributionMetric } from 'src/lib/api/portalUsage';
	import { portalGetBudgetMonth, portalGetUsageSummary, portalGetInstanceCost } from 'src/lib/api/portalUsage';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, Heading, Link, Spinner, Text } from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';
	import Metric from 'src/lib/components/primitives/Metric.svelte';
	import Sparkline from 'src/lib/components/primitives/Sparkline.svelte';
	import ProgressBar from 'src/lib/components/primitives/ProgressBar.svelte';
	import CostGauge from 'src/lib/components/primitives/CostGauge.svelte';

	let { token, slug } = $props<{ token: string; slug: string }>();

	function currentMonthUTC(): string {
		return new Date().toISOString().slice(0, 7);
	}

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	/**
	 * Format a dollar amount. Under $0.01 gets micro-precision.
	 */
	function formatDollars(n: number): string {
		if (!Number.isFinite(n)) return '—';
		if (n < 0.01) return `$${n.toFixed(6)}`;
		return `$${n.toFixed(2)}`;
	}

	/**
	 * Format a raw number with commas.
	 */
	function formatNumber(n: number): string {
		if (!Number.isFinite(n)) return '—';
		return n.toLocaleString();
	}

	/**
	 * Format GB-sec or GB value with human-readable abbreviation for
	 * large numbers (e.g. "1.85M GB-sec" rather than "15962000.0 GB-sec").
	 */
	function formatUnit(n: number, unit: string): string {
		if (!Number.isFinite(n)) return '—';
		if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M ${unit}`;
		if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K ${unit}`;
		return `${n.toFixed(1)} ${unit}`;
	}

	/**
	 * Compute remaining credits client-side. Uses the server-provided field
	 * when present, falling back to `included - used` floored at zero.
	 */
	function computeRemainingCredits(
		budget: BudgetMonthResponse | null,
		summary: UsageSummaryResponse | null,
	): number {
		const server = budget?.remaining_credits ?? summary?.remaining_credits;
		if (typeof server === 'number' && Number.isFinite(server)) return server;
		const included = budget?.included_credits ?? summary?.included_credits ?? 0;
		const used = budget?.used_credits ?? summary?.used_credits ?? 0;
		return Math.max(0, included - used);
	}

	/**
	 * Derive total compute GB-sec from cost telemetry metrics.
	 * Searches entry.metrics[] for units matching GB-sec, GB*s, GBs.
	 * Returns null when no matching metric data exists.
	 */
	function deriveComputeGbSec(days: CostDayEntry[]): number | null {
		let total = 0;
		let found = false;
		for (const day of days) {
			for (const entry of day.entries) {
				if (!entry.metrics) continue;
				for (const m of entry.metrics) {
					const unit = (m.unit ?? '').toLowerCase();
					if (unit.includes('gb-sec') || unit.includes('gb*s') || unit.includes('gbs')) {
						if (Number.isFinite(m.value)) {
							total += m.value;
							found = true;
						}
					}
				}
			}
		}
		return found ? total : null;
	}

	/**
	 * Derive total egress GB from cost telemetry metrics.
	 * Searches entry.metrics[] for data-transfer / bytes units.
	 */
	function deriveEgressGb(days: CostDayEntry[]): number | null {
		let total = 0;
		let found = false;
		for (const day of days) {
			for (const entry of day.entries) {
				if (!entry.metrics) continue;
				for (const m of entry.metrics) {
					const unit = (m.unit ?? '').toLowerCase();
					const svc = (m.service ?? '').toLowerCase();
					// Skip compute units (GB-sec / GB*s / GBs) — those are
					// counted by deriveComputeGbSec, not egress.
					if (unit.includes('gb-sec') || unit.includes('gb*s') || unit.includes('gbs')) continue;
					// Match data-transfer or egress-like metrics
					if (
						svc.includes('datatransfer') ||
						svc.includes('data transfer') ||
						svc.includes('egress') ||
						unit.includes('gb') ||
						unit.includes('gigabyte') ||
						unit.includes('byte')
					) {
						if (Number.isFinite(m.value)) {
							// If unit is Bytes, convert to GB
							if (unit.includes('byte') && !unit.includes('giga')) {
								total += m.value / (1024 * 1024 * 1024);
							} else {
								total += m.value;
							}
							found = true;
						}
					}
				}
			}
		}
		return found ? total : null;
	}

	/**
	 * Extract a series of compute GB-sec values per day for the sparkline.
	 */
	function deriveComputeSeries(days: CostDayEntry[]): number[] {
		const series: number[] = [];
		for (const day of days) {
			let dayTotal = 0;
			for (const entry of day.entries) {
				if (!entry.metrics) continue;
				for (const m of entry.metrics) {
					const unit = (m.unit ?? '').toLowerCase();
					if (unit.includes('gb-sec') || unit.includes('gb*s') || unit.includes('gbs')) {
						if (Number.isFinite(m.value)) dayTotal += m.value;
					}
				}
			}
			series.push(dayTotal);
		}
		return series;
	}

	interface ServiceBreakdown {
		service: string;
		category: string;
		totalCost: number;
		pctOfTotal: number;
		currency: string;
	}

	/**
	 * Aggregate cost entries by category for the breakdown table.
	 */
	function buildBreakdown(days: CostDayEntry[], totalCost: number): ServiceBreakdown[] {
		const byCategory: Record<string, { service: string; category: string; total: number; currency: string }> = {};
		for (const day of days) {
			for (const entry of day.entries) {
				const cat = categoryLabel(entry.service);
				const existing = byCategory[cat];
				if (existing) {
					existing.total += entry.cost;
				} else {
					byCategory[cat] = {
						service: entry.service,
						category: cat,
						total: entry.cost,
						currency: entry.currency,
					};
				}
			}
		}
		return Object.values(byCategory)
			.map((v) => ({
				...v,
				totalCost: v.total,
				pctOfTotal: totalCost > 0 ? (v.total / totalCost) * 100 : 0,
			}))
			.sort((a, b) => b.totalCost - a.totalCost);
	}

	function categoryLabel(service: string): string {
		const s = (service ?? '').trim();
		const lower = s.toLowerCase();
		if (lower === 'lambda') return 'Lambda';
		if (lower === 'dynamodb') return 'DynamoDB';
		if (
			lower === 'datatransfer' ||
			lower === 'cloudfront' ||
			lower.includes('data transfer') ||
			lower.includes('egress') ||
			lower.includes('datatransfer')
		) {
			return 'Egress';
		}
		return s || 'Unknown';
	}

	// Reactive state
	let loading = $state(false);
	let costLoading = $state(false);
	let errorMessage = $state<string | null>(null);
	let costError = $state<string | null>(null);

	let budget = $state<BudgetMonthResponse | null>(null);
	let summary = $state<UsageSummaryResponse | null>(null);
	let cost = $state<PortalCostResponse | null>(null);

	let displayMonth = $state<string>(currentMonthUTC());

	async function loadAll() {
		errorMessage = null;
		costError = null;

		const month = currentMonthUTC();
		displayMonth = month;
		loading = true;
		try {
			const [b, s] = await Promise.all([
				portalGetBudgetMonth(token, slug, month),
				portalGetUsageSummary(token, slug, month),
			]);
			budget = b;
			summary = s;
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			errorMessage = formatError(err);
		} finally {
			loading = false;
		}

		costLoading = true;
		try {
			cost = await portalGetInstanceCost(token, slug);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			costError = formatError(err);
		} finally {
			costLoading = false;
		}
	}

	onMount(() => {
		void loadAll();
	});

	// Derived values
	const usedCredits = $derived(budget?.used_credits ?? summary?.used_credits ?? 0);
	const includedCredits = $derived(budget?.included_credits ?? summary?.included_credits ?? 0);
	const remainingCredits = $derived(computeRemainingCredits(budget, summary));
	const creditPct = $derived.by(() => {
		if (includedCredits <= 0) return 0;
		return Math.min(100, Math.round((usedCredits / includedCredits) * 100));
	});

	const totalCostDollars = $derived(cost?.total_cost ?? null);
	const computeGbSec = $derived(cost ? deriveComputeGbSec(cost.days) : null);
	const egressGb = $derived(cost ? deriveEgressGb(cost.days) : null);
	const computeSeries = $derived(cost ? deriveComputeSeries(cost.days) : []);
	const breakdown = $derived(cost && cost.total_cost > 0 ? buildBreakdown(cost.days, cost.total_cost) : []);

	const creditTone = $derived.by(() => {
		if (includedCredits <= 0) return 'info';
		if (usedCredits > includedCredits) return 'error';
		if (creditPct >= 90) return 'warning';
		return 'success';
	});

	// Budget alarm threshold data (static — no persistence yet)
	const alarmThresholds = $derived([
		{ id: 'warning', label: 'Warning', description: 'Notify when usage reaches 70% of monthly budget', pct: 70, enabled: false, disabled: true },
		{ id: 'critical', label: 'Critical', description: 'Notify when usage reaches 90% of monthly budget', pct: 90, enabled: false, disabled: true },
		{ id: 'cap', label: 'Cap', description: 'Hard-stop instance when budget is fully consumed', pct: 100, enabled: false, disabled: true },
	]);
</script>

<div class="cost">
	<!-- Loading state -->
	{#if loading && !budget && !summary}
		<div class="cost__loading">
			<Spinner size="md" />
			<Text>Loading cost & usage…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load cost & usage">{errorMessage}</Alert>
	{:else}
		<!-- ================================================================
		Header metric cards
		================================================================ -->
		<div class="cost__metrics">
			<!-- MTD spend vs budget card -->
			<Panel title="MTD spend vs budget" headerLevel={3} padding="lg">
				<div class="cost__metric-card">
					<div class="cost__gauge-row">
						<CostGauge
							used={usedCredits}
							budget={includedCredits > 0 ? includedCredits : 1}
							size={96}
							label="credits"
						/>
						<div class="cost__gauge-detail">
							<Text size="sm" weight="medium">Credits used</Text>
							<Text size="lg" weight="semibold">{formatNumber(usedCredits)}</Text>
							<Text size="xs" color="secondary">of {formatNumber(includedCredits)} included</Text>
							{#if totalCostDollars !== null}
								<div class="cost__dollar-note">
									<Badge variant="outlined" color="info" size="sm">
										≈ {formatDollars(totalCostDollars)} {cost?.currency ?? 'USD'}
									</Badge>
								</div>
							{/if}
						</div>
					</div>
					<div class="cost__progress-row">
						<ProgressBar value={usedCredits} max={includedCredits > 0 ? includedCredits : 1} tone={creditTone === 'error' ? 'error' : creditTone === 'warning' ? 'warning' : 'success'} />
						<div class="cost__progress-labels">
							<Text size="xs" color="secondary">{creditPct}% used</Text>
							<Text size="xs" color="secondary">{formatNumber(remainingCredits)} remaining</Text>
						</div>
					</div>
				</div>
			</Panel>

			<!-- Compute GB-sec card -->
			<Panel title="Compute" headerLevel={3} padding="lg">
				<div class="cost__metric-card">
					{#if costLoading && !cost}
						<div class="cost__loading">
							<Spinner size="sm" />
							<Text size="sm">Loading…</Text>
						</div>
					{:else if computeGbSec !== null}
						<Metric
							label="GB-sec (MTD)"
							value={formatUnit(computeGbSec, 'GB-sec')}
							sub="Compute usage this month"
						/>
					{:else if cost}
						<Metric
							label="GB-sec (MTD)"
							value="—"
							sub="Not available — no GB-sec metrics in cost data"
							tone="info"
						/>
					{:else}
						<Metric
							label="GB-sec (MTD)"
							value="—"
							sub="Cost data not yet loaded"
							tone="info"
						/>
					{/if}
					{#if computeSeries.length > 0}
						<div class="cost__spark-row">
							<Sparkline values={computeSeries} color="var(--ds-secondary-500)" />
						</div>
					{:else if cost && computeGbSec === null}
						<Text size="xs" color="secondary">No compute metrics to chart</Text>
					{/if}
				</div>
			</Panel>

			<!-- Egress GB card -->
			<Panel title="Egress" headerLevel={3} padding="lg">
				<div class="cost__metric-card">
					{#if costLoading && !cost}
						<div class="cost__loading">
							<Spinner size="sm" />
							<Text size="sm">Loading…</Text>
						</div>
					{:else if egressGb !== null}
						<Metric
							label="Data transfer (MTD)"
							value={formatUnit(egressGb, 'GB')}
							sub="Egress this month"
						/>
					{:else if cost}
						<Metric
							label="Data transfer (MTD)"
							value="—"
							sub="Not available — no egress metrics in cost data"
							tone="info"
						/>
					{:else}
						<Metric
							label="Data transfer (MTD)"
							value="—"
							sub="Cost data not yet loaded"
							tone="info"
						/>
					{/if}
					{#if cost && egressGb === null}
						<Text size="xs" color="secondary">No data transfer metrics available</Text>
					{/if}
				</div>
			</Panel>
		</div>

		<!-- ================================================================
		Where the dollars go — service breakdown table
		================================================================ -->
		<Panel title="Where the dollars go" headerLevel={2}>
			{#snippet actions()}
				<Text size="xs" color="secondary">
					{#if cost}
						{cost.from_date} – {cost.to_date}
					{/if}
				</Text>
			{/snippet}

			{#if costLoading && !cost}
				<div class="cost__loading">
					<Spinner size="sm" />
					<Text size="sm">Loading cost data…</Text>
				</div>
			{:else if costError && !cost}
				<Alert variant="warning" title="Cost telemetry unavailable">{costError}</Alert>
			{:else if cost && breakdown.length > 0}
				<div class="cost__table-wrap">
					<div class="cost__table" role="table" aria-label="Cost breakdown by service">
						<div class="cost__table-header" role="row">
							<div class="cost__col-service" role="columnheader">Service</div>
							<div class="cost__col-cost" role="columnheader">Cost</div>
							<div class="cost__col-bar" role="columnheader"></div>
							<div class="cost__col-pct" role="columnheader">% of total</div>
						</div>
						{#each breakdown as row (row.category)}
							<div class="cost__table-row" role="row">
								<div class="cost__col-service" role="cell">
									<Text size="sm" weight="medium">{row.category}</Text>
								</div>
								<div class="cost__col-cost" role="cell">
									<Text size="sm">{formatDollars(row.totalCost)}</Text>
								</div>
								<div class="cost__col-bar" role="cell">
									<ProgressBar
										value={row.totalCost}
										max={cost!.total_cost}
										tone={row.pctOfTotal > 50 ? 'accent' : undefined}
									/>
								</div>
								<div class="cost__col-pct" role="cell">
									<Text size="sm">{Math.round(row.pctOfTotal)}%</Text>
								</div>
							</div>
						{/each}
					</div>
				</div>
				<div class="cost__table-footer">
					<Text size="sm" weight="semibold">
						Total: {formatDollars(cost.total_cost)} {cost.currency}
					</Text>
				</div>
			{:else if cost && breakdown.length === 0}
				<Alert variant="info" title="No cost breakdown available">
					<Text size="sm">
						Cost data is present but no per-service breakdown entries are available for this period.
					</Text>
				</Alert>
			{:else}
				<Alert variant="info" title="No cost telemetry available">
					<Text size="sm">
						Cost telemetry data for this instance is not yet available.
						Data typically appears within 24–48 hours of instance activity.
					</Text>
				</Alert>
			{/if}
		</Panel>

		<!-- ================================================================
		Budget alarms panel
		================================================================ -->
		<Panel title="Budget alarms" headerLevel={2}>
			<div class="cost__alarms">
				<Text size="sm" color="secondary">
					Budget alarms are not yet available for configuration. Threshold controls are shown for planning only.
				</Text>
				<div class="cost__alarm-list">
					{#each alarmThresholds as alarm (alarm.id)}
						<div class="cost__alarm-row">
							<div class="cost__alarm-info">
								<div class="cost__alarm-header">
									<Text size="sm" weight="semibold">{alarm.label}</Text>
									<Badge
										variant="outlined"
										color={alarm.id === 'cap' ? 'error' : alarm.id === 'critical' ? 'warning' : 'gray'}
										size="sm"
									>
										{alarm.pct}%
									</Badge>
								</div>
								<Text size="xs" color="secondary">{alarm.description}</Text>
							</div>
							<div class="cost__alarm-toggle">
								<Button
									variant="outline"
									size="sm"
									disabled={alarm.disabled}
									title="Budget alarm persistence is not yet available. Thresholds are local-only."
								>
									{alarm.enabled ? 'On' : 'Off'}
								</Button>
							</div>
						</div>
					{/each}
				</div>
			</div>
		</Panel>

		<!-- ================================================================
		Monthly credit summary (existing data, re-styled)
		================================================================ -->
		<Panel title="Monthly credit summary" headerLevel={2}>
			<div class="cost__credit-summary">
				<div class="cost__credit-row">
					<Text size="sm">Included credits</Text>
					<Text size="sm" weight="semibold">{formatNumber(includedCredits)}</Text>
				</div>
				<div class="cost__credit-row">
					<Text size="sm">Used credits</Text>
					<Text size="sm" weight="semibold">{formatNumber(usedCredits)}</Text>
				</div>
				<div class="cost__credit-row">
					<Text size="sm">Remaining credits</Text>
					<Text size="sm" weight="semibold" color={remainingCredits > 0 ? 'primary' : 'error'}>{formatNumber(remainingCredits)}</Text>
				</div>
				{#if summary}
					<div class="cost__credit-divider"></div>
					<div class="cost__credit-row">
						<Text size="sm">Total requests</Text>
						<Text size="sm" weight="semibold">{formatNumber(summary.requests)}</Text>
					</div>
					<div class="cost__credit-row">
						<Text size="sm">Cache hits</Text>
						<Text size="sm" weight="semibold">{formatNumber(summary.cache_hits)}</Text>
					</div>
					<div class="cost__credit-row">
						<Text size="sm">Cache misses</Text>
						<Text size="sm" weight="semibold">{formatNumber(summary.cache_misses)}</Text>
					</div>
					<div class="cost__credit-row">
						<Text size="sm">Debited credits</Text>
						<Text size="sm" weight="semibold">{formatNumber(summary.debited_credits)}</Text>
					</div>
				{/if}
			</div>
		</Panel>

		<!-- ================================================================
		Actions
		================================================================ -->
		<div class="cost__actions">
			<Button
				variant="outline"
				onclick={() => void loadAll()}
				loading={loading || costLoading}
				loadingBehavior="prepend"
			>
				Refresh
			</Button>
			<Link {...linkProps(`/portal/instances/${slug}/budgets`)} variant="ghost">Budgets (legacy)</Link>
			<Link {...linkProps(`/portal/instances/${slug}/usage`)} variant="ghost">Usage (legacy)</Link>
		</div>
	{/if}
</div>

<style>
	.cost {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-5, 1.25rem);
	}

	.cost__loading {
		display: flex;
		gap: var(--ds-space-2, 0.5rem);
		align-items: center;
	}

	/* --- Header metric cards --- */
	.cost__metrics {
		display: grid;
		grid-template-columns: 2fr 1fr 1fr;
		gap: var(--ds-space-4, 1rem);
	}

	.cost__metric-card {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3, 0.75rem);
	}

	.cost__gauge-row {
		display: flex;
		gap: var(--ds-space-4, 1rem);
		align-items: center;
	}

	.cost__gauge-detail {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1, 0.25rem);
	}

	.cost__dollar-note {
		margin-top: var(--ds-space-1, 0.25rem);
	}

	.cost__progress-row {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1, 0.25rem);
	}

	.cost__progress-labels {
		display: flex;
		justify-content: space-between;
	}

	.cost__spark-row {
		margin-top: var(--ds-space-1, 0.25rem);
	}

	/* --- Breakdown table --- */
	.cost__table-wrap {
		overflow-x: auto;
	}

	.cost__table {
		display: flex;
		flex-direction: column;
		gap: 0;
		min-width: 480px;
	}

	.cost__table-header {
		display: grid;
		grid-template-columns: 1fr 100px 1fr 80px;
		gap: var(--ds-space-3, 0.75rem);
		align-items: center;
		padding: var(--ds-space-2, 0.5rem) 0;
		border-bottom: 2px solid var(--ds-border-default, var(--gr-color-border));
		font-size: var(--ds-font-size-xs, 0.75rem);
		font-weight: var(--ds-weight-semibold, 600);
		color: var(--ds-fg-2, var(--gr-color-foreground-secondary));
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.cost__table-row {
		display: grid;
		grid-template-columns: 1fr 100px 1fr 80px;
		gap: var(--ds-space-3, 0.75rem);
		align-items: center;
		padding: var(--ds-space-3, 0.75rem) 0;
		border-bottom: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
	}

	.cost__table-row:last-child {
		border-bottom: none;
	}

	.cost__col-service {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.cost__col-cost {
		text-align: right;
	}

	.cost__col-pct {
		text-align: right;
	}

	.cost__table-footer {
		display: flex;
		justify-content: flex-end;
		padding-top: var(--ds-space-3, 0.75rem);
		border-top: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
	}

	/* --- Budget alarms --- */
	.cost__alarms {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-4, 1rem);
	}

	.cost__alarm-list {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.cost__alarm-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: var(--ds-space-3, 0.75rem);
		border: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		border-radius: var(--ds-radius-sm, 0.375rem);
	}

	.cost__alarm-row + .cost__alarm-row {
		border-top: none;
		border-radius: 0;
	}

	.cost__alarm-row:first-child {
		border-bottom-left-radius: 0;
		border-bottom-right-radius: 0;
	}

	.cost__alarm-row:last-child {
		border-top-left-radius: 0;
		border-top-right-radius: 0;
		margin-top: -1px;
	}

	.cost__alarm-row:only-child {
		border-radius: var(--ds-radius-sm, 0.375rem);
	}

	.cost__alarm-info {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1, 0.25rem);
	}

	.cost__alarm-header {
		display: flex;
		gap: var(--ds-space-2, 0.5rem);
		align-items: center;
	}

	.cost__alarm-toggle {
		flex-shrink: 0;
	}

	/* --- Credit summary --- */
	.cost__credit-summary {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-2, 0.5rem);
	}

	.cost__credit-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: var(--ds-space-1, 0.25rem) 0;
	}

	.cost__credit-divider {
		border-top: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		margin: var(--ds-space-1, 0.25rem) 0;
	}

	/* --- Actions --- */
	.cost__actions {
		display: flex;
		gap: var(--ds-space-2, 0.5rem);
		align-items: center;
		flex-wrap: wrap;
	}

	/* --- Responsive --- */
	@media (max-width: 959px) {
		.cost__metrics {
			grid-template-columns: 1fr;
		}

		.cost__gauge-row {
			flex-direction: column;
			align-items: flex-start;
		}

		.cost__table-header,
		.cost__table-row {
			grid-template-columns: 1fr 80px 1fr 60px;
			gap: var(--ds-space-2, 0.5rem);
		}
	}
</style>
