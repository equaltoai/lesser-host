<!--
@component
InstanceCost — Instance Detail Cost & usage tab.

M1.13 — Project 39 web UI rework. Replaces the coming-soon placeholder on
the Cost & usage tab with real data: current-month budget (included, used,
remaining credits) and cache hit rate, both sourced from the existing
portal budget + usage-summary endpoints.

Per-Lambda/Dynamo/egress real-time cost telemetry is deferred to M3; this
tab surfaces a "Real-time cost telemetry coming soon" empty state for that
breakdown. Legacy `/portal/instances/{slug}/budgets` and `/usage` pages
remain functional for drill-down.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Multi-tenant isolation: budget/usage endpoints enforce per-slug
    ownership server-side via `requireInstanceAccess`.
  - Trust-API instance-auth untouched; SEC-9 change-lock not engaged.
  - No on-chain code path changed.
  - No framework local patches; consumes existing API client modules,
    UI primitives, and shell components through released surfaces.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.13
Issue: equaltoai/lesser-host#422
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { BudgetMonthResponse, UsageSummaryResponse } from 'src/lib/api/portalUsage';
	import { portalGetBudgetMonth, portalGetUsageSummary } from 'src/lib/api/portalUsage';
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

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let budget = $state<BudgetMonthResponse | null>(null);
	let summary = $state<UsageSummaryResponse | null>(null);

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

			<Alert variant="info" title="Real-time cost telemetry coming soon">
				<Text size="sm">
					Per-Lambda, per-Dynamo-table, and per-egress cost breakdown with
					real-time telemetry is scheduled for M3 alongside the cost-telemetry
					firehose. The current tab surfaces budget and aggregate usage through
					the existing portal budget and usage-summary endpoints.
				</Text>
			</Alert>

			<!-- TODO(M3): replace this static breakdown with live per-Lambda /
			     per-Dynamo / per-egress telemetry once the cost-telemetry firehose
			     lands. Tracking: M3 cost-telemetry milestone. -->
			<div class="instance-cost__coming-soon-grid">
				<div class="instance-cost__coming-soon-item">
					<Text size="sm" weight="medium">Lambda</Text>
					<Text size="sm" color="secondary">Invocation count / duration / cost per function — pending M3 telemetry pipeline.</Text>
				</div>
				<div class="instance-cost__coming-soon-item">
					<Text size="sm" weight="medium">DynamoDB</Text>
					<Text size="sm" color="secondary">RCU/WCU consumption / storage per table — pending M3 telemetry pipeline.</Text>
				</div>
				<div class="instance-cost__coming-soon-item">
					<Text size="sm" weight="medium">Egress</Text>
					<Text size="sm" color="secondary">Data transfer by destination / protocol — pending M3 telemetry pipeline.</Text>
				</div>
			</div>
		</Card>

		<div class="instance-cost__actions">
			<Button
				variant="outline"
				onclick={() => void loadAll()}
				loading={loading}
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

	.instance-cost__coming-soon-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
		gap: var(--gr-spacing-scale-4);
		margin-top: var(--gr-spacing-scale-4);
	}

	.instance-cost__coming-soon-item {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-subtle, #d9d9d9);
		border-radius: var(--gr-radius-md, 12px);
		background: var(--gr-color-surface);
	}

	.instance-cost__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}
</style>
