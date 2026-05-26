<!--
@component
InstanceCost — Instance Detail Cost & usage tab.

M3.12 — Project 39 cost telemetry wiring. Replaces the "coming soon" empty state
and TODO(M3) static breakdown with real-time cost telemetry from
GET /api/v1/portal/instances/{slug}/cost. Aggregates response entries by service
and renders category totals plus daily/entry details.

Budget + usage summary from M1.13 is preserved. Cost data loads separately so
budget/summary still display even when cost telemetry is not yet available.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Multi-tenant isolation: cost endpoint enforces per-slug ownership server-side
    via requireInstanceAccess.
  - Trust-API instance-auth untouched.
  - No on-chain code path changed.
  - No framework local patches; consumes existing API client modules,
    UI primitives, and shell components through released surfaces.

Source: Issue equaltoai/lesser-host#456
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { BudgetMonthResponse, UsageSummaryResponse, PortalCostResponse, CostDayEntry } from 'src/lib/api/portalUsage';
	import { portalGetBudgetMonth, portalGetUsageSummary, portalGetInstanceCost } from 'src/lib/api/portalUsage';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Button, Card, DefinitionItem, DefinitionList, Heading, Link, Spinner, Text } from 'src/lib/ui';
	import { StatCard, SummaryStrip } from 'src/lib/shell';

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
	 * Format a 0.0–1.0 ratio as a percentage string with one decimal place.
	 * Matches the wire contract of `UsageSummaryResponse.cache_hit_rate`
	 * (see `portalUsage.ts`). Returns "—" for non-finite input.
	 */
	function formatPercent(p: number): string {
		if (!Number.isFinite(p)) return '—';
		return `${Math.round(p * 1000) / 10}%`;
	}

	/**
	 * Compute remaining credits client-side. Uses the server-provided field
	 * when present (M1.12 / PR #506), falling back to `included - used`
	 * floored at zero.
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

	interface ServiceAggregate {
		service: string;
		category: string;
		total: number;
		count: number;
		currency: string;
	}

	/**
	 * Aggregate cost entries across all days by category.
	 * Returns an array of category aggregates sorted by total cost descending.
	 */
	function aggregateByCategory(days: CostDayEntry[]): ServiceAggregate[] {
		const byCategory: Record<string, ServiceAggregate> = {};
		for (const day of days) {
			for (const entry of day.entries) {
				const cat = categoryForService(entry.service);
				const existing = byCategory[cat];
				if (existing) {
					existing.total += entry.cost;
					existing.count++;
				} else {
					byCategory[cat] = { service: entry.service, category: cat, total: entry.cost, count: 1, currency: entry.currency };
				}
			}
		}
		return Object.values(byCategory).sort((a, b) => b.total - a.total);
	}

	/**
	 * Categorize a normalized service name into a user-facing category label.
	 *
	 * Maps Lambda → "Lambda", DynamoDB → "DynamoDB", and
	 * DataTransfer / CloudFront / any data-transfer-like service → "Egress".
	 * All other services pass through unchanged.
	 *
	 * The mapping is case-insensitive on input and returns stable labels
	 * regardless of upstream service-name drift.
	 */
	function categoryForService(service: string): string {
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

	/**
	 * Format a cost value for display. Uses fixed precision for sub-dollar
	 * values and 2 decimal places for larger amounts.
	 */
	function formatCost(n: number): string {
		if (n < 0.01) return `$${n.toFixed(6)}`;
		return `$${n.toFixed(2)}`;
	}

	let loading = $state(false);
	let costLoading = $state(false);
	let errorMessage = $state<string | null>(null);
	let costError = $state<string | null>(null);

	let budget = $state<BudgetMonthResponse | null>(null);
	let summary = $state<UsageSummaryResponse | null>(null);
	let cost = $state<PortalCostResponse | null>(null);

	/**
	 * Month label pinned at the start of each `loadAll()` so the header,
	 * data, and refresh button all reference the same month. Without this
	 * pin, a refresh that straddles UTC midnight on the 1st of the month
	 * would render a header month different from the fetched data.
	 */
	let displayMonth = $state<string>(currentMonthUTC());

	async function loadAll() {
		// Do NOT clear `budget` / `summary` before the await. Clearing
		// here makes the page-level spinner gate (`loading && !budget &&
		// !summary` in the template) win on every manual refresh, which
		// in turn replaces the Refresh button subtree — at which point
		// `Button` is no longer in the DOM and its `loading` prop can
		// never render the inline spinner. Keeping the prior values
		// during the refresh round-trip lets the page-level branch fire
		// only on first load (no prior data) and lets the Refresh
		// button's inline loading affordance render during inflight
		// refresh. On non-401 errors, `errorMessage` wins the template
		// branch and the stale data is hidden by the existing
		// `{:else if errorMessage}` arm; on 401 we navigate to /login
		// before the state matters.
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

		// Cost telemetry loads separately so budget/summary still display
		// when cost data is not yet available. On 401, redirect.
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
</script>

<div class="instance-cost">
	{#if loading && !budget && !summary}
		<div class="instance-cost__loading">
			<Spinner size="md" />
			<Text>Loading cost & usage…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load cost & usage">{errorMessage}</Alert>
	{:else}
		{@const included = budget?.included_credits ?? summary?.included_credits ?? 0}
		{@const used = budget?.used_credits ?? summary?.used_credits ?? 0}
		{@const remaining = computeRemainingCredits(budget, summary)}
		{@const hitRate = summary?.cache_hit_rate ?? null}

		<SummaryStrip label="Cost & usage summary" columns={4} gap="md">
			<StatCard label="Included credits" value={String(included)} status="default" />
			<StatCard label="Used credits" value={String(used)} status="default" />
			<StatCard
				label="Remaining credits"
				value={String(remaining)}
				status={
					used > included
						? 'danger'
						: remaining > 0
							? 'success'
							: used > 0
								? 'warning'
								: 'default'
				}
			/>
			<StatCard
				label="Cache hit rate"
				value={hitRate !== null ? formatPercent(hitRate) : '—'}
				status="info"
			/>
		</SummaryStrip>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Current month ({displayMonth})</Heading>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Included credits" monospace>{String(included)}</DefinitionItem>
				<DefinitionItem label="Used credits" monospace>{String(used)}</DefinitionItem>
				<DefinitionItem label="Remaining credits" monospace>{String(remaining)}</DefinitionItem>
				<DefinitionItem label="Cache hit rate" monospace>
					{hitRate !== null ? formatPercent(hitRate) : '—'}
				</DefinitionItem>
				{#if summary}
					<DefinitionItem label="Total requests" monospace>{String(summary.requests)}</DefinitionItem>
					<DefinitionItem label="Cache hits" monospace>{String(summary.cache_hits)}</DefinitionItem>
					<DefinitionItem label="Cache misses" monospace>{String(summary.cache_misses)}</DefinitionItem>
					<DefinitionItem label="Debited credits" monospace>{String(summary.debited_credits)}</DefinitionItem>
					<DefinitionItem label="Discount credits" monospace>{String(summary.discount_credits)}</DefinitionItem>
				{/if}
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Real-time cost telemetry</Heading>
			{/snippet}

			{#if costLoading && !cost}
				<div class="instance-cost__loading">
					<Spinner size="sm" />
					<Text size="sm">Loading cost telemetry…</Text>
				</div>
			{:else if costError && !cost}
				<Alert variant="warning" title="Cost telemetry unavailable">{costError}</Alert>
			{:else if cost}
				<Text size="sm" color="secondary">
					{cost.from_date} – {cost.to_date} · {cost.count} day(s) · Total: {formatCost(cost.total_cost)} {cost.currency}
				</Text>

				{@const byCategory = aggregateByCategory(cost.days)}
				{#if byCategory.length > 0}
					<div class="instance-cost__service-grid">
						{#each byCategory as agg (agg.category)}
							<div class="instance-cost__service-item">
								<Text size="sm" weight="medium">{agg.category}</Text>
								<div class="instance-cost__service-cost">
									<Text size="sm"><span class="instance-cost__mono">{formatCost(agg.total)}</span></Text>
									<Text size="xs" color="secondary">{agg.count} entry(s)</Text>
								</div>
							</div>
						{/each}
					</div>
				{/if}

				{#if cost.days.length > 0}
					<div class="instance-cost__daily-section">
						<Text size="sm" weight="medium">Daily breakdown</Text>
						<div class="instance-cost__daily-list">
							{#each [...cost.days].reverse() as day (day.date)}
								<div class="instance-cost__daily-row">
									<div class="instance-cost__daily-header">
										<Text size="sm" weight="medium">{day.date}</Text>
										<Text size="sm"><span class="instance-cost__mono">{formatCost(day.day_cost)} {day.currency}</span></Text>
									</div>
									{#if day.entries.length > 0}
										<div class="instance-cost__daily-entries">
											{#each day.entries as entry (entry.service + "-" + entry.date)}
												<div class="instance-cost__daily-entry">
													<Text size="xs" color="secondary">{entry.service}</Text>
													<Text size="xs"><span class="instance-cost__mono">{formatCost(entry.cost)}</span></Text>
												</div>
											{/each}
										</div>
									{/if}
								</div>
							{/each}
						</div>
					</div>
				{/if}
			{:else}
				<Alert variant="info" title="No cost telemetry available">
					<Text size="sm">
						Cost telemetry data for this instance is not yet available.
						Data typically appears within 24–48 hours of instance activity.
					</Text>
				</Alert>
			{/if}
		</Card>

		<div class="instance-cost__actions">
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
	.instance-cost {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.instance-cost__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.instance-cost__service-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.instance-cost__service-item {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-subtle, #d9d9d9);
		border-radius: var(--gr-radius-md, 12px);
		background: var(--gr-color-surface);
	}

	.instance-cost__service-cost {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-0);
	}

	.instance-cost__daily-section {
		margin-top: var(--gr-spacing-scale-4);
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.instance-cost__daily-list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.instance-cost__daily-row {
		padding: var(--gr-spacing-scale-2);
		border: 1px solid var(--gr-color-border-subtle, #d9d9d9);
		border-radius: var(--gr-radius-sm, 8px);
	}

	.instance-cost__daily-header {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.instance-cost__daily-entries {
		margin-top: var(--gr-spacing-scale-1);
		padding-top: var(--gr-spacing-scale-1);
		border-top: 1px solid var(--gr-color-border-subtle, #d9d9d9);
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-0);
	}

	.instance-cost__daily-entry {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}

	.instance-cost__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.instance-cost__mono {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}
</style>
