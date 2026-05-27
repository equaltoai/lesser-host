<!--
@component
M1PrimitivesFixture — isolated visual fixture rendering all M1 foundation
primitives for design-fidelity evidence capture.

Renders Eyebrow, Metric (tone + delta variants), CostGauge (0/50/75/95),
Sparkline (with/without fill), and ProgressBar (tone variants) in a
standalone layout. Not mounted by any customer portal route — this file
exists solely for local visual review and headless PNG capture.

Strict-CSP safe: no inline styles, no inline event handlers.
No data fetching, global state, or route assumptions.

@license AGPL-3.0-only
@public
-->
<script lang="ts">
	import Eyebrow from '../Eyebrow.svelte';
	import Metric from '../Metric.svelte';
	import CostGauge from '../CostGauge.svelte';
	import Sparkline from '../Sparkline.svelte';
	import ProgressBar from '../ProgressBar.svelte';
</script>

<div class="m1-fixture">
	<h1 class="m1-fixture__title">M1 Primitives — Visual Fixture</h1>
	<p class="m1-fixture__desc">
		Isolated rendering of all Project 42 M1 foundation primitives
		for design-fidelity evidence capture. Not a customer-facing surface.
	</p>

	<!-- ─── Eyebrow ──────────────────────────────────────────────── -->
	<section class="m1-fixture__section">
		<h2 class="m1-fixture__section-title">Eyebrow</h2>
		<div class="m1-fixture__grid">
			<div class="m1-fixture__item">
				<Eyebrow>Fleet overview</Eyebrow>
			</div>
			<div class="m1-fixture__item">
				<Eyebrow>Billing summary</Eyebrow>
			</div>
			<div class="m1-fixture__item">
				<Eyebrow>Resource usage</Eyebrow>
			</div>
		</div>
	</section>

	<!-- ─── Metric ───────────────────────────────────────────────── -->
	<section class="m1-fixture__section">
		<h2 class="m1-fixture__section-title">Metric — Tone &amp; Delta Variants</h2>
		<div class="m1-fixture__grid m1-fixture__grid--metrics">
			<div class="m1-fixture__item">
				<Metric label="Active users" value="1,243" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Revenue" value="$4,200" delta="+12%" deltaDir="up" tone="success" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Errors" value="3" delta="-40%" deltaDir="down" tone="error" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Latency" value="250ms" tone="warning" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Uptime" value="99.9%" tone="success" sub="Last 30 days" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Storage" value="42 GB" tone="info" sub="of 100 GB allocated" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Total cost" value="$9.4k" tone="accent" />
			</div>
			<div class="m1-fixture__item">
				<Metric label="Requests" value="2.1M" delta="Unchanged" />
			</div>
		</div>
	</section>

	<!-- ─── CostGauge ────────────────────────────────────────────── -->
	<section class="m1-fixture__section">
		<h2 class="m1-fixture__section-title">CostGauge — 0% / 50% / 75% / 95%</h2>
		<div class="m1-fixture__grid m1-fixture__grid--gauges">
			<div class="m1-fixture__item m1-fixture__item--center">
				<CostGauge used={0} budget={100} label="Monthly" />
				<span class="m1-fixture__label">0% (OK)</span>
			</div>
			<div class="m1-fixture__item m1-fixture__item--center">
				<CostGauge used={50} budget={100} label="Monthly" />
				<span class="m1-fixture__label">50% (OK)</span>
			</div>
			<div class="m1-fixture__item m1-fixture__item--center">
				<CostGauge used={75} budget={100} label="Monthly" />
				<span class="m1-fixture__label">75% (Warning)</span>
			</div>
			<div class="m1-fixture__item m1-fixture__item--center">
				<CostGauge used={95} budget={100} label="Monthly" />
				<span class="m1-fixture__label">95% (Danger)</span>
			</div>
		</div>
	</section>

	<!-- ─── Sparkline ────────────────────────────────────────────── -->
	<section class="m1-fixture__section">
		<h2 class="m1-fixture__section-title">Sparkline — With &amp; Without Fill</h2>
		<div class="m1-fixture__grid m1-fixture__grid--sparklines">
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">With fill (default)</span>
				<Sparkline values={[3, 8, 5, 12, 9, 14, 10, 11, 7, 13]} />
			</div>
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">Without fill</span>
				<Sparkline values={[100, 200, 150, 300, 250, 400, 350]} color="var(--ds-warning-500)" fill={false} />
			</div>
		</div>
	</section>

	<!-- ─── ProgressBar ──────────────────────────────────────────── -->
	<section class="m1-fixture__section">
		<h2 class="m1-fixture__section-title">ProgressBar — Tone Variants</h2>
		<div class="m1-fixture__grid m1-fixture__grid--bars">
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">Default (no tone)</span>
				<ProgressBar value={42} />
			</div>
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">Success</span>
				<ProgressBar value={30} tone="success" />
			</div>
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">Warning</span>
				<ProgressBar value={75} tone="warning" />
			</div>
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">Error</span>
				<ProgressBar value={92} tone="error" />
			</div>
			<div class="m1-fixture__item">
				<span class="m1-fixture__label">Accent</span>
				<ProgressBar value={60} tone="accent" />
			</div>
		</div>
	</section>
</div>

<style>
	/*
	 * M1PrimitivesFixture — fixture-only layout styles.
	 *
	 * These styles are scoped to the fixture page only. They are not
	 * loaded by any customer portal route. No inline styles.
	 *
	 * @license AGPL-3.0-only
	 */

	.m1-fixture {
		max-width: 900px;
		margin: 0 auto;
		padding: 2rem 1.5rem 4rem;
		font-family: var(--ds-font-sans, sans-serif);
		color: var(--ds-fg-1, #fff);
		background: var(--ds-bg-1, #0d0d0d);
	}

	.m1-fixture__title {
		font-family: var(--ds-font-heading, sans-serif);
		font-size: 1.5rem;
		font-weight: 700;
		margin: 0 0 0.25rem;
		color: var(--ds-fg-1, #fff);
	}

	.m1-fixture__desc {
		font-size: 0.875rem;
		color: var(--ds-fg-3, #888);
		margin: 0 0 2rem;
	}

	.m1-fixture__section {
		margin-bottom: 2.5rem;
	}

	.m1-fixture__section-title {
		font-family: var(--ds-font-heading, sans-serif);
		font-size: 1rem;
		font-weight: 600;
		margin: 0 0 1rem;
		padding-bottom: 0.5rem;
		border-bottom: 1px solid var(--ds-border-subtle, #2a2a2a);
	}

	.m1-fixture__grid {
		display: flex;
		flex-wrap: wrap;
		gap: 1rem;
	}

	.m1-fixture__grid--metrics {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
	}

	.m1-fixture__grid--gauges {
		gap: 1.5rem;
	}

	.m1-fixture__grid--sparklines {
		flex-direction: column;
		gap: 1.25rem;
	}

	.m1-fixture__grid--bars {
		flex-direction: column;
		gap: 1rem;
	}

	.m1-fixture__item--center {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.5rem;
	}

	.m1-fixture__label {
		font-size: 0.75rem;
		color: var(--ds-fg-3, #888);
		margin-bottom: 0.375rem;
	}

	/* Eyebrow items: use the panel card style for visual context */
	.m1-fixture__section:first-of-type .m1-fixture__item {
		background: var(--ds-bg-2, #1a1a1a);
		border: 1px solid var(--ds-border-subtle, #2a2a2a);
		border-radius: 8px;
		padding: 1rem;
	}

	/* Metric items: card context matching the design fixture */
	.m1-fixture__grid--metrics .m1-fixture__item {
		background: var(--ds-bg-2, #1a1a1a);
		border: 1px solid var(--ds-border-subtle, #2a2a2a);
		border-radius: 8px;
		padding: 1rem;
	}

	/* ProgressBar items: card context */
	.m1-fixture__grid--bars .m1-fixture__item {
		background: var(--ds-bg-2, #1a1a1a);
		border: 1px solid var(--ds-border-subtle, #2a2a2a);
		border-radius: 8px;
		padding: 1rem;
	}
</style>
