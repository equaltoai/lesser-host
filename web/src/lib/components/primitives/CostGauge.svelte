<!--
@component
CostGauge — circular SVG budget gauge matching the Project 42 design fixture.

Renders a circular gauge with:
- An SVG ring with background track and a colored arc representing usage
- Threshold-based color: green (OK, ≤69%), amber (warning, 70–89%), red (danger, 90%+)
- Center percentage readout + optional label rendered as SVG text
- `role="meter"` with `aria-valuenow`, `aria-valuemin`, `aria-valuemax`,
  and `aria-valuetext` for screen readers

Strict-CSP safe: no inline styles. SVG presentation attributes
(`stroke-dasharray`, `stroke-dashoffset`, `font-size`) carry dynamic values;
the rotation transform is scoped to the ring group via CSS so center text
stays upright. Status tone is communicated via CSS class + visible text,
never color-only.

Deviation from the design fixture: inline `style` attributes are replaced
with CSS classes and SVG attributes. The percentage font-size scaling
(`size * 0.24`) is preserved via the SVG `font-size` presentation attribute.

@example
```svelte
<CostGauge used={42} budget={100} label="Monthly spend" />
<CostGauge used={95} budget={100} size={96} />
```

@public
-->
<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import { useStableId } from 'src/lib/greater/utils';

	type CostGaugeStatus = 'ok' | 'warning' | 'danger';

	interface Props extends HTMLAttributes<HTMLDivElement> {
		/** Amount used / consumed. @public */
		used: number;

		/** Budget limit. Must be > 0 for a valid gauge. @public */
		budget: number;

		/** Diameter of the gauge in pixels. Defaults to 72. @public */
		size?: number;

		/** Optional label rendered below the percentage. @public */
		label?: string;

		/** Additional CSS classes. @public */
		class?: string;
	}

	let {
		used,
		budget,
		size = 72,
		label,
		class: className = '',
		...restProps
	}: Props = $props();

	const stableId = useStableId('m1-cost-gauge');

	const strokeWidth = 4;
	const radius = $derived((size - 2 * strokeWidth) / 2);
	const circumference = $derived(2 * Math.PI * radius);

	const pct = $derived.by(() => {
		if (!Number.isFinite(budget) || budget <= 0) return 0;
		return Math.min(100, Math.max(0, (used / budget) * 100));
	});

	const offset = $derived.by(() => {
		const clamped = Math.min(100, Math.max(0, pct));
		return circumference - (clamped / 100) * circumference;
	});

	const status = $derived.by<CostGaugeStatus>(() => {
		if (pct >= 90) return 'danger';
		if (pct >= 70) return 'warning';
		return 'ok';
	});

	const displayPct = $derived(Math.round(pct));
	const valueText = $derived(`${displayPct}% of budget`);
	const percentFontSize = $derived(size * 0.24);
	const labelOffset = $derived(size * 0.065);
	const cx = $derived(size / 2);
	const cy = $derived(size / 2);

	const rootClass = $derived(
		['gr-m1-cost-gauge', `gr-m1-cost-gauge--${status}`, className]
			.filter(Boolean)
			.join(' ')
	);

	const hasValidMeter = $derived(Number.isFinite(budget) && budget > 0);
</script>

<div class={rootClass} {...restProps}>
	<svg
		class="gr-m1-cost-gauge__svg"
		width={size}
		height={size}
		viewBox={`0 0 ${size} ${size}`}
		role={hasValidMeter ? 'meter' : 'img'}
		aria-valuemin={hasValidMeter ? 0 : undefined}
		aria-valuemax={hasValidMeter ? budget : undefined}
		aria-valuenow={hasValidMeter ? used : undefined}
		aria-valuetext={valueText}
		aria-labelledby={label ? stableId.value : undefined}
		focusable="false"
	>
		<!-- Ring group: rotated -90deg so the arc starts at 12-o'clock -->
		<g class="gr-m1-cost-gauge__ring-group">
			<circle
				class="gr-m1-cost-gauge__track"
				cx={cx}
				cy={cy}
				r={radius}
				stroke-width={strokeWidth}
			/>
			<circle
				class="gr-m1-cost-gauge__arc"
				cx={cx}
				cy={cy}
				r={radius}
				stroke-width={strokeWidth}
				stroke-dasharray={circumference}
				stroke-dashoffset={offset}
			/>
		</g>

		<!-- Center text: upright (not rotated with rings) -->
		<text
			class="gr-m1-cost-gauge__percent"
			x={cx}
			y={label ? cy - labelOffset : cy}
			text-anchor="middle"
			dominant-baseline="central"
			font-size={percentFontSize}
		>
			{displayPct}%
		</text>
		{#if label}
			<text
				class="gr-m1-cost-gauge__label"
				id={stableId.value}
				x={cx}
				y={cy + labelOffset + percentFontSize * 0.4}
				text-anchor="middle"
				dominant-baseline="hanging"
			>
				{label}
			</text>
		{/if}
	</svg>

	<!-- Screen-reader-only status text for test assertion -->
	<span class="gr-sr-only" data-test="cost-gauge-status">
		{status === 'ok' ? 'Within budget' : status === 'warning' ? 'Approaching limit' : 'Exceeds threshold'}
	</span>
</div>
