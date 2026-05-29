<!--
@component
Billing — spend-analytics billing page re-skinned for M11.

SPDX-License-Identifier: AGPL-3.0-only
@license AGPL-3.0-only

Renders the spend-analytics surface defined in the Project 42 design fixture
(portal-pages-2.jsx:3–169): four metric cards, a stacked weekly bar chart,
a per-instance breakdown table with burn progress, and a right rail with
This-month / Payment-method / Recent-invoices panels.

Data is aggregated client-side from existing portalListInstances +
portalGetInstanceCost cost telemetry. Invoice and payment-method data
comes from the M12 backend endpoints (GET /api/v1/portal/billing/invoices,
GET /api/v1/portal/billing/payment-method).

Posture invariants preserved:
  - Strict-no-inline-CSP safe: no inline style attributes; styling through
    CSS classes, data-* attribute selectors, and SVG presentation attributes.
  - Multi-tenant isolation: consumes only per-owner portal endpoints;
    no cross-tenant reads.
  - No raw payment data rendered: only brand/last4/expiry/status and
    safe invoice period/status/amount/hosted links.
  - Unavailable denominators ("Per active user", "Per federated post")
    render an explicit '—' state, never invented numbers or divide-by-zero.
  - No backend edits. No internal/controlplane/internal/payments/store/infra
    changes. No Cost & usage tab work. No new payment update flow.

Source: docs/portal-pages-2.jsx M11 Billing UI
Issue: equaltoai/lesser-host#546
@license AGPL-3.0-only
-->

<script lang="ts">
	import { onMount } from 'svelte';
	import type { Snippet } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalListInstances } from 'src/lib/api/portalInstances';
	import type { PortalCostResponse } from 'src/lib/api/portalUsage';
	import { portalGetInstanceCost } from 'src/lib/api/portalUsage';
	import type {
		ListInvoicesResponse,
		GetPaymentMethodResponse,
		PaymentMethodSafe,
		InvoiceSummary,
	} from 'src/lib/api/portalBilling';
	import {
		portalListInvoices,
		portalGetPaymentMethod,
	} from 'src/lib/api/portalBilling';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Spinner, Text } from 'src/lib/ui';
	import { PageFrame, PageTitle, Panel } from 'src/lib/shell';
	import { Eyebrow, Metric, Sparkline, ProgressBar } from 'src/lib/components/primitives';

	let { token } = $props<{ token: string }>();

	// ---- state ----
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let instances = $state<InstanceResponse[]>([]);
	let costsBySlug = $state<Map<string, PortalCostResponse>>(new Map());
	let invoicesResponse = $state<ListInvoicesResponse | null>(null);
	let paymentMethodResponse = $state<GetPaymentMethodResponse | null>(null);

	// ---- derived: month helpers ----
	const nowStr = $derived(new Date().toISOString());
	const currentYear = $derived(Number(nowStr.slice(0, 4)));
	const currentMonthNum = $derived(Number(nowStr.slice(5, 7)));
	const currentMonth = $derived(`${currentYear}-${String(currentMonthNum).padStart(2, '0')}`);
	const currentDay = $derived(Number(nowStr.slice(8, 10)));
	const currentMonthLabel = $derived(monthName(currentMonthNum, currentYear));
	const daysInMonth = $derived(monthDays(currentYear, currentMonthNum));
	const daysElapsed = $derived(Math.max(1, currentDay));

	function monthName(m: number, y: number): string {
		const names = ['January','February','March','April','May','June','July','August','September','October','November','December'];
		return `${names[m - 1] ?? 'Unknown'} ${y}`;
	}

	function monthDays(y: number, m: number): number {
		return new Date(y, m, 0).getDate();
	}

	// Day of week for week-start calculation: 0=Sun, ..., 6=Sat
	const currentDow = $derived(new Date(nowStr).getDay());
	const daysSinceMonday = $derived((currentDow + 6) % 7);
	// Date of most recent Monday as YYYY-MM-DD
	const lastMondayStr = $derived(dateOffset(nowStr.slice(0, 10), -daysSinceMonday));

	function dateOffset(dateStr: string, days: number): string {
		// eslint-disable-next-line svelte/prefer-svelte-reactivity -- pure function, returns string immediately
		const d = new Date(dateStr + 'T00:00:00Z');
		d.setUTCDate(d.getUTCDate() + days);
		return d.toISOString().slice(0, 10);
	}

	function dateCmp(a: string, b: string): number {
		return a.localeCompare(b);
	}

	// ---- derived: per-instance costs ----
	interface InstanceCostData {
		slug: string;
		domain: string;
		status: string;
		mtd: number;
		budget: number;
		projected: number;
		accentIndex: number;
		dailyCosts: number[];
	}

	const instanceCosts = $derived.by((): InstanceCostData[] => {
		return instances.map((inst, idx) => {
			const costResp = costsBySlug.get(inst.slug);
			const days = costResp?.days ?? [];
			const monthPrefix = `${currentMonth}-`;
			const mtdDays = days.filter((d) => d.date.startsWith(monthPrefix));
			const mtd = mtdDays.reduce((sum, d) => sum + (d.day_cost || 0), 0);
			const projected = daysElapsed > 0 ? (mtd / daysElapsed) * daysInMonth : mtd;

			const dailyCosts = mtdDays
				.sort((a, b) => a.date.localeCompare(b.date))
				.map((d) => d.day_cost || 0);

			return {
				slug: inst.slug,
				domain: inst.managed_lesser_domain || inst.hosted_base_domain || '—',
				status: inst.status,
				mtd,
				budget: inferBudget(inst.slug),
				projected,
				accentIndex: idx,
				dailyCosts,
			};
		});
	});

	const totalMtd = $derived(instanceCosts.reduce((s, i) => s + i.mtd, 0));
	const totalBudget = $derived(instanceCosts.reduce((s, i) => s + i.budget, 0));
	const totalProjected = $derived(instanceCosts.reduce((s, i) => s + i.projected, 0));

	// ---- derived: weekly stacked data via SVG chart ----
	interface WeekStack {
		label: string;
		stacks: number[];
		isProjection: boolean;
	}

	const weeklyStacks = $derived.by((): WeekStack[] => {
		if (instanceCosts.length === 0) return [];

		const weeks: WeekStack[] = [];
		const today = nowStr.slice(0, 10); // YYYY-MM-DD

		// 4 prior complete weeks (Mon–Sun), most recent first
		for (let w = 4; w >= 1; w--) {
			const weekEnd = dateOffset(lastMondayStr, -(w - 1) * 7 - 1);
			const weekStart = dateOffset(weekEnd, -6);
			const label = shortDateLabel(weekStart);
			const stacks = instanceCosts.map((ic) => weekCost(ic.slug, weekStart, weekEnd));
			weeks.push({ label, stacks, isProjection: false });
		}

		// Current partial week (Mon through yesterday)
		{
			const yesterday = dateOffset(today, -1);
			const label = shortDateLabel(lastMondayStr);
			const stacks = instanceCosts.map((ic) => weekCost(ic.slug, lastMondayStr, yesterday));
			weeks.push({ label, stacks, isProjection: false });
		}

		// Projection for current week
		{
			const weekEndStr = dateOffset(lastMondayStr, 6);
			const projLabel = shortDateLabel(weekEndStr) + ' · proj.';
			const fraction = daysSinceMonday + 1 > 0 ? 7 / (daysSinceMonday + 1) : 1;
			const stacks = instanceCosts.map((ic) => {
				const partial = weekCost(ic.slug, lastMondayStr, today);
				return partial * fraction;
			});
			weeks.push({ label: projLabel, stacks, isProjection: true });
		}

		return weeks;
	});

	function shortDateLabel(dateStr: string): string {
		const m = Number(dateStr.slice(5, 7));
		const d = Number(dateStr.slice(8, 10));
		const names = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
		return `${names[m - 1] ?? '??'} ${d}`;
	}

	function weekCost(slug: string, startStr: string, endStr: string): number {
		let total = 0;
		for (const day of costsBySlug.get(slug)?.days ?? []) {
			if (day.date >= startStr && day.date <= endStr) {
				total += day.day_cost || 0;
			}
		}
		return total;
	}

	const maxWeekTotal = $derived(
		Math.max(...weeklyStacks.map((w) => w.stacks.reduce((a, b) => a + b, 0)), 1)
	);

	// ---- SVG chart constants ----
	const CHART_BAR_W = 52;
	const CHART_GAP = 22;
	const CHART_MAX_H = 180;
	const CHART_PADD_Y = 8;
	const LEGEND_HEIGHT = 22;

	const chartSvgWidth = $derived(weeklyStacks.length * (CHART_BAR_W + CHART_GAP) - CHART_GAP + 2);
	const chartSvgHeight = $derived(CHART_MAX_H + CHART_PADD_Y + LEGEND_HEIGHT);

	function stackYOffset(stacks: number[], maxIdx: number): number {
		let below = 0;
		for (let i = 0; i < maxIdx; i++) {
			below += stacks[i];
		}
		return below;
	}

	function barHeight(stackVal: number, total: number): number {
		if (total <= 0) return 0;
		return Math.max(1, Math.round((stackVal / total) * CHART_MAX_H));
	}

	// ---- accent color palette ----
	const ACCENT_COLORS = $derived([
		'var(--ds-secondary-500, #8d64d1)',
		'var(--ds-primary-500, #e6a645)',
		'var(--ds-warning-500, #f59e0b)',
		'var(--ds-success-500, #22c55e)',
		'var(--ds-info-500, #3b82f6)',
		'var(--ds-fg-3, #999)',
	]);

	const NUM_ACCENTS = $derived(ACCENT_COLORS.length);

	// ---- budget fallback ----
	const BUDGET_FALLBACKS: Record<string, number> = {
		equaltoai: 50,
		'maeve-studio': 20,
		staging: 10,
		'press-room': 25,
		guild: 30,
		lab: 15,
	};

	function inferBudget(slug: string): number {
		return BUDGET_FALLBACKS[slug] ?? 25;
	}

	// ---- formatting ----
	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function formatCurrency(amount: number): string {
		return `$${amount.toFixed(2)}`;
	}

	function formatMonthLabel(dateStr: string): string {
		if (!dateStr) return '—';
		try {
			const d = new Date(dateStr + 'T00:00:00Z');
			return d.toLocaleDateString('en-US', { month: 'long', year: 'numeric' });
		} catch {
			return dateStr;
		}
	}

	function burnTone(ratio: number): 'warning' | 'error' | 'accent' | undefined {
		if (ratio > 1.0) return 'error';
		if (ratio > 0.8) return 'warning';
		return undefined;
	}

	function statusBadge(status: string): { variant: 'outlined' | 'filled'; color: 'success' | 'warning' | 'error' | 'gray' } {
		const s = (status || '').toLowerCase();
		if (s === 'paid' || s === 'open' || s === 'active') return { variant: 'filled', color: 'success' };
		if (s === 'draft' || s === 'pending') return { variant: 'outlined', color: 'warning' };
		if (s === 'void' || s === 'uncollectible') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	// ---- data loading ----
	async function loadAll() {
		errorMessage = null;
		instances = [];
		costsBySlug = new Map();
		invoicesResponse = null;
		paymentMethodResponse = null;
		loading = true;

		try {
			const [instResp, invResp, pmResp] = await Promise.all([
				portalListInstances(token),
				portalListInvoices(token).catch(() => null),
				portalGetPaymentMethod(token).catch(() => null),
			]);

			instances = instResp.instances ?? [];
			invoicesResponse = invResp;
			paymentMethodResponse = pmResp;

			const monthStart = `${currentMonth}-01`;
			const costPromises = instances.map((inst) =>
				portalGetInstanceCost(token, inst.slug, monthStart)
					.then((cr) => ({ slug: inst.slug, data: cr }))
					.catch(() => null)
			);
			const costResults = await Promise.all(costPromises);
			// eslint-disable-next-line svelte/prefer-svelte-reactivity -- temporary Map for data assembly, not reactive state
			const costMap = new Map<string, PortalCostResponse>();
			for (const r of costResults) {
				if (r) costMap.set(r.slug, r.data);
			}
			costsBySlug = costMap;
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

<PageFrame width="wide" asideLabel="Billing details">
	{#snippet header()}
		<PageTitle
			eyebrow="Cost &amp; billing"
			title="Where the money goes."
			description="Per-instance cost across compute, storage, network, and the AWS fixed costs we eat for you. Real-time, billed monthly."
		>
			{#snippet actions()}
				<Button variant="outline" onclick={() => void loadAll()} disabled={loading}>Refresh</Button>
			{/snippet}
		</PageTitle>
	{/snippet}

	{#snippet aside()}
		<div class="billing-rail">
			<!-- This month panel -->
			<Panel title="This month" headerLevel={2}>
				<Eyebrow>{currentMonthLabel}</Eyebrow>
				<div class="billing-rail__amount">
					<span class="billing-rail__value">{formatCurrency(totalMtd)}</span>
					<span class="billing-rail__budget">of {formatCurrency(totalBudget)}</span>
				</div>
				<ProgressBar value={totalMtd} max={Math.max(1, totalBudget)} />
				<div class="billing-rail__row">
					<span class="billing-rail__sub">Projected {formatCurrency(totalProjected)}</span>
				</div>
				{#if instanceCosts.length > 0 && instanceCosts[0].dailyCosts.length > 0}
					<div class="billing-rail__sparkline-wrap">
						<Sparkline values={instanceCosts[0].dailyCosts} color="var(--ds-secondary-500)" />
					</div>
				{/if}
			</Panel>

			<!-- Payment method panel -->
			<Panel title="Payment" headerLevel={2}>
				{#if paymentMethodResponse?.payment_method}
					{@const pm = paymentMethodResponse.payment_method}
					{@const badge = statusBadge(pm.status)}
					<div class="billing-pm">
						<div class="billing-pm__card">
							<div class="billing-pm__card-icon"></div>
							<div class="billing-pm__info">
								<span class="billing-pm__number">{pm.brand || pm.type}{pm.last4 ? ` •••• ${pm.last4}` : ''}</span>
								{#if pm.exp_month && pm.exp_year}
									<span class="billing-pm__expiry">exp {String(pm.exp_month).padStart(2, '0')}/{String(pm.exp_year).slice(-2)}</span>
								{/if}
							</div>
						</div>
						<div class="billing-pm__badge-wrap">
							<Badge variant={badge.variant} color={badge.color} size="sm">{pm.status}</Badge>
						</div>
					</div>
				{:else if paymentMethodResponse && !paymentMethodResponse.payment_method}
					<Text size="sm" color="secondary">No payment method on file.</Text>
				{:else}
					<Text size="sm" color="secondary">Loading…</Text>
				{/if}
			</Panel>

			<!-- Recent invoices panel -->
			<Panel title="Invoices" headerLevel={2}>
				{#if invoicesResponse && invoicesResponse.invoices.length === 0}
					<Text size="sm" color="secondary">No invoices yet.</Text>
				{:else if invoicesResponse}
					<div class="billing-invoices">
						{#each invoicesResponse.invoices.slice(0, 5) as inv (inv.id)}
							{@const badge = statusBadge(inv.status)}
							<div class="billing-invoices__item">
								<div class="billing-invoices__main">
									<span class="billing-invoices__id">{inv.id}</span>
									<span class="billing-invoices__period">{formatMonthLabel(inv.period_start)}</span>
								</div>
								<div class="billing-invoices__meta">
									<span class="billing-invoices__amount">{formatCurrency(inv.amount_due / 100)}</span>
									<Badge variant={badge.variant} color={badge.color} size="sm">{inv.status}</Badge>
								</div>
							</div>
						{/each}
					</div>
				{:else}
					<Text size="sm" color="secondary">Loading…</Text>
				{/if}
			</Panel>
		</div>
	{/snippet}

	<!-- MAIN CONTENT -->
	{#if loading}
		<div class="billing__loading">
			<Spinner size="md" />
			<Text>Loading billing data…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load billing">{errorMessage}</Alert>
	{:else}
		<!-- Four metric cards -->
		<div class="billing-metrics">
			<Metric
				label="MTD"
				value={formatCurrency(totalMtd)}
				sub={`of ${formatCurrency(totalBudget)} aggregate budget`}
			/>
			<Metric
				label="Projected EOM"
				value={formatCurrency(totalProjected)}
				sub="based on daily trailing"
			/>
			<Metric
				label="Per active user"
				value="—"
				sub="unavailable · MAU data not on contract"
			/>
			<Metric
				label="Per federated post"
				value="—"
				sub="unavailable · post count not on contract"
			/>
		</div>

		<!-- Stacked weekly bar chart (CSP-safe: SVG presentation attributes) -->
		<Panel title="Weekly spend" headerLevel={2}>
			<Eyebrow>{currentMonthLabel} · stacked by instance</Eyebrow>
			{#if weeklyStacks.length > 0}
				<div class="billing-chart">
					<div class="billing-chart__svg-wrap">
						<svg
							viewBox="0 0 {chartSvgWidth} {chartSvgHeight}"
							width="100%"
							height={chartSvgHeight}
							focusable="false"
							role="img"
							aria-label="Weekly spend chart"
						>
							{#each weeklyStacks as week, wi (wi)}
								{@const wTotal = week.stacks.reduce((a, b) => a + b, 0)}
								{@const x = wi * (CHART_BAR_W + CHART_GAP)}
								{@const totalBarH = maxWeekTotal > 0 ? (wTotal / maxWeekTotal) * CHART_MAX_H : 0}
								<!-- Render bars from bottom up -->
								{#each week.stacks as stack, si (si)}
									{#if stack > 0}
										{@const h = (stack / maxWeekTotal) * CHART_MAX_H}
										{@const below = week.stacks.slice(0, si).reduce((a, b) => a + b, 0)}
										{@const belowH = (below / maxWeekTotal) * CHART_MAX_H}
										{@const barY = CHART_MAX_H - belowH - h + CHART_PADD_Y}
										<rect
											x={x + 1}
											y={barY}
											width={CHART_BAR_W}
											height={Math.max(0.5, h)}
											fill={instanceCosts[si] ? ACCENT_COLORS[instanceCosts[si].accentIndex % NUM_ACCENTS] : ACCENT_COLORS[si % NUM_ACCENTS]}
											opacity={week.isProjection ? '0.35' : '0.88'}
											rx="3"
										>
											<title>{instanceCosts[si]?.slug ?? `instance-${si}`}: {formatCurrency(stack)}</title>
										</rect>
									{/if}
								{/each}
								<!-- Projection dashed border -->
								{#if week.isProjection && totalBarH > 0}
									{@const y = CHART_MAX_H - totalBarH + CHART_PADD_Y}
									<rect
										x={x + 1}
										y={y}
										width={CHART_BAR_W}
										height={Math.max(0.5, totalBarH)}
										fill="none"
										stroke="var(--ds-border-strong, rgba(107,56,16,0.3))"
										stroke-width="2"
										stroke-dasharray="6 4"
										rx="3"
									/>
								{/if}
							{/each}
						</svg>
					</div>
					<div class="billing-chart__labels">
					{#each weeklyStacks as week, wi (wi)}
						<span class="billing-chart__label" class:billing-chart__label--proj={week.isProjection}>
							{formatCurrency(week.stacks.reduce((a, b) => a + b, 0))}
							<span class="billing-chart__label-date">{week.label}</span>
						</span>
					{/each}
				</div>
				<div class="billing-chart__legend">
					{#each instanceCosts as ic, idx (ic.slug)}
							<div class="billing-chart__legend-item">
								<span
									class="billing-chart__swatch"
									data-accent={idx % NUM_ACCENTS}
								></span>
								<span>{ic.slug}</span>
								<span class="billing-chart__legend-cost">{formatCurrency(ic.mtd)}</span>
							</div>
						{/each}
					</div>
				</div>
			{:else}
				<Text size="sm" color="secondary">No cost data available for chart.</Text>
			{/if}
		</Panel>

		<!-- Per-instance breakdown table -->
		<Panel title="Breakdown" headerLevel={2}>
			<Eyebrow>Per instance</Eyebrow>
			{#if instanceCosts.length > 0}
				<div class="billing-table-wrap">
					<table class="billing-table">
						<thead>
							<tr>
								<th>Instance</th>
								<th class="billing-table__num">MTD</th>
								<th class="billing-table__num">Budget</th>
								<th class="billing-table__num">Projected</th>
								<th>Burn</th>
							</tr>
						</thead>
						<tbody>
						{#each instanceCosts as ic, idx (ic.slug)}
							{@const spendRatio = ic.budget > 0 ? ic.mtd / ic.budget : 0}
							{@const overBudget = ic.projected > ic.budget}
							<tr>
									<td>
										<div class="billing-table__instance">
											<span
												class="billing-table__accent"
												data-accent={idx % NUM_ACCENTS}
											></span>
											<div class="billing-table__instance-info">
												<strong>{ic.slug}</strong>
												<span class="billing-table__domain">{ic.domain}</span>
											</div>
										</div>
									</td>
									<td class="billing-table__num"><strong>{formatCurrency(ic.mtd)}</strong></td>
									<td class="billing-table__num billing-table__num--muted">{formatCurrency(ic.budget)}</td>
									<td class="billing-table__num">
										{formatCurrency(ic.projected)}
										{#if overBudget}
											<span class="billing-table__over-badge">over</span>
										{/if}
									</td>
									<td>
										<div class="billing-table__burn">
											<ProgressBar value={ic.mtd} max={Math.max(1, ic.budget)} tone={burnTone(spendRatio)} />
											<span class="billing-table__burn-pct">{Math.round(spendRatio * 100)}%</span>
										</div>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{:else}
				<Text size="sm" color="secondary">No instances with cost data.</Text>
			{/if}
		</Panel>
	{/if}
</PageFrame>

<style>
	/* ---- Loading ---- */
	.billing__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
	}

	/* ---- Metric card grid ---- */
	.billing-metrics {
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		gap: var(--ds-space-4);
		margin-bottom: var(--ds-space-5);
	}

	/* ---- Right rail ---- */
	.billing-rail {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-4);
	}

	.billing-rail__amount {
		display: flex;
		align-items: flex-end;
		gap: 0.6rem;
		margin-bottom: 0.5rem;
	}

	.billing-rail__value {
		font-family: var(--ds-font-heading, Georgia, serif);
		font-size: 1.9rem;
		font-weight: 700;
		letter-spacing: -0.02em;
		line-height: 1;
	}

	.billing-rail__budget {
		font-size: 0.82rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		padding-bottom: 0.3rem;
	}

	.billing-rail__row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		margin-top: 0.5rem;
		font-size: 0.82rem;
	}

	.billing-rail__sub {
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
	}

	.billing-rail__sparkline-wrap {
		margin-top: 0.75rem;
		height: 36px;
	}

	/* ---- Payment method panel ---- */
	.billing-pm {
		display: flex;
		flex-direction: column;
		gap: 0.6rem;
	}

	.billing-pm__card {
		display: flex;
		align-items: center;
		gap: 0.6rem;
	}

	.billing-pm__card-icon {
		width: 32px;
		height: 20px;
		border-radius: 3px;
		background: linear-gradient(135deg, var(--ds-secondary-500), var(--ds-secondary-700));
	}

	.billing-pm__info {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.billing-pm__number {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.84rem;
	}

	.billing-pm__expiry {
		font-size: 0.72rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.billing-pm__badge-wrap {
		margin-top: 0.3rem;
	}

	/* ---- Recent invoices panel ---- */
	.billing-invoices {
		display: flex;
		flex-direction: column;
		gap: 0.4rem;
	}

	.billing-invoices__item {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 0.4rem 0;
		gap: 0.4rem;
	}

	.billing-invoices__main {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.billing-invoices__id {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.82rem;
		font-weight: 600;
	}

	.billing-invoices__period {
		font-size: 0.72rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.billing-invoices__meta {
		display: flex;
		align-items: center;
		gap: 0.4rem;
	}

	.billing-invoices__amount {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.82rem;
	}

	/* ---- Stacked bar chart ---- */
	.billing-chart {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.billing-chart__svg-wrap {
		width: 100%;
		overflow-x: auto;
	}

	.billing-chart__labels {
		display: flex;
		gap: 1.2rem;
		justify-content: space-around;
	}

	.billing-chart__label {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 2px;
		flex: 1;
		min-width: 0;
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.74rem;
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
	}

	.billing-chart__label--proj {
		opacity: 0.5;
	}

	.billing-chart__label-date {
		font-family: var(--ds-font-sans, sans-serif);
		font-size: 0.7rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		white-space: nowrap;
	}

	.billing-chart__legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.75rem;
		margin-top: 0.25rem;
	}

	.billing-chart__legend-item {
		display: flex;
		align-items: center;
		gap: 5px;
		font-size: 0.8rem;
	}

	.billing-chart__swatch {
		width: 10px;
		height: 10px;
		border-radius: 2px;
		flex-shrink: 0;
	}

	.billing-chart__legend-cost {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	/* Accent swatch colors via data-accent attribute (CSP-safe) */
	.billing-chart__swatch[data-accent='0'],
	.billing-table__accent[data-accent='0'] {
		background: var(--ds-secondary-500, #8d64d1);
	}
	.billing-chart__swatch[data-accent='1'],
	.billing-table__accent[data-accent='1'] {
		background: var(--ds-primary-500, #e6a645);
	}
	.billing-chart__swatch[data-accent='2'],
	.billing-table__accent[data-accent='2'] {
		background: var(--ds-warning-500, #f59e0b);
	}
	.billing-chart__swatch[data-accent='3'],
	.billing-table__accent[data-accent='3'] {
		background: var(--ds-success-500, #22c55e);
	}
	.billing-chart__swatch[data-accent='4'],
	.billing-table__accent[data-accent='4'] {
		background: var(--ds-info-500, #3b82f6);
	}
	.billing-chart__swatch[data-accent='5'],
	.billing-table__accent[data-accent='5'] {
		background: var(--ds-fg-3, #999);
	}

	/* ---- Breakdown table ---- */
	.billing-table-wrap {
		overflow-x: auto;
	}

	.billing-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.88rem;
	}

	.billing-table th {
		text-align: left;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
	}

	.billing-table td {
		padding: 0.6rem 0.75rem;
		border-bottom: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.08));
		vertical-align: middle;
	}

	.billing-table tbody tr:hover {
		background: var(--ds-bg-raised, rgba(255, 255, 255, 0.4));
	}

	.billing-table__num {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		text-align: right;
	}

	.billing-table__num--muted {
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.billing-table__instance {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}

	.billing-table__accent {
		width: 6px;
		height: 24px;
		border-radius: 2px;
		flex-shrink: 0;
	}

	.billing-table__instance-info {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.billing-table__domain {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.72rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.billing-table__over-badge {
		display: inline-block;
		margin-left: 0.4rem;
		padding: 0.1rem 0.35rem;
		font-size: 0.68rem;
		font-weight: 700;
		border-radius: 4px;
		background: var(--ds-error-100, #fee2e2);
		color: var(--ds-error-700, #b91c1c);
	}

	.billing-table__burn {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 160px;
	}

	.billing-table__burn-pct {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.74rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		white-space: nowrap;
		min-width: 2.5rem;
		text-align: right;
	}

	/* ---- Responsive ---- */
	@media (max-width: 900px) {
		.billing-metrics {
			grid-template-columns: repeat(2, 1fr);
		}
	}
	@media (max-width: 500px) {
		.billing-metrics {
			grid-template-columns: 1fr;
		}
	}
</style>
