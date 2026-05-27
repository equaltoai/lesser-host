<!--
@component
Sparkline — tiny SVG line chart matching the Project 42 design fixture.

@license AGPL-3.0-only

Renders an inline SVG sparkline with an optional area fill below the line.
The path is computed deterministically from the data series — no random or
time-dependent values, so SSR and client renders produce identical output
(no hydration mismatch).

Strict-CSP safe: no inline styles. SVG presentation attributes
(`fill`, `stroke`, `stroke-width`, `fill-opacity`) carry styling; the
`preserveAspectRatio="none"` attribute enables responsive width via the
`.sparkline-svg` CSS class.

SSR safety: the component renders nothing when `values` is empty (matches
the design fixture's `return null` guard). Path computation is pure math
with `.toFixed(1)` for deterministic string output.

@example
```svelte
<Sparkline values={[3, 8, 5, 12, 9, 14, 10]} />
<Sparkline values={[100, 200, 150]} color="var(--ds-warning-500)" fill={false} />
```

@public
-->
<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';

	interface Props extends HTMLAttributes<HTMLDivElement> {
		/** Data series. Empty array renders nothing. @public */
		values: number[];

		/** SVG viewBox width. Defaults to 120. @public */
		width?: number;

		/** SVG viewBox height. Defaults to 32. @public */
		height?: number;

		/** Stroke / fill color. Accepts CSS variable references (e.g. `var(--ds-secondary-500)`). Defaults to secondary. @public */
		color?: string;

		/** When true (default), renders a filled area below the line. @public */
		fill?: boolean;

		/** Additional CSS classes on the wrapper. @public */
		class?: string;
	}

	let {
		values,
		width = 120,
		height = 32,
		color = 'var(--ds-secondary-500)',
		fill: showFill = true,
		class: className = '',
		...restProps
	}: Props = $props();

	const hasData = $derived(values && values.length > 0);

	// Paths are derived from the design fixture's deterministic algorithm.
	// Using .toFixed(1) ensures identical SSR/client output.
	const pathD = $derived.by(() => {
		if (!hasData) return '';
		const data = values;
		const min = Math.min(...data);
		const max = Math.max(...data);
		const range = max - min || 1;
		const step = width / (data.length - 1 || 1);
		const pts = data.map((v, i) => {
			const x = (i * step).toFixed(1);
			const y = (height - ((v - min) / range) * (height - 4) - 2).toFixed(1);
			return [x, y] as const;
		});
		const d = pts
			.map((p, i) => `${i === 0 ? 'M' : 'L'}${p[0]},${p[1]}`)
			.join(' ');
		return d;
	});

	const areaD = $derived.by(() => {
		if (!hasData) return '';
		return `${pathD} L${width},${height} L0,${height} Z`;
	});

	const rootClass = $derived(
		['gr-m1-sparkline', className].filter(Boolean).join(' ')
	);
</script>

{#if hasData}
	<div class={rootClass} {...restProps}>
		<svg
			class="sparkline-svg"
			viewBox={`0 0 ${width} ${height}`}
			preserveAspectRatio="none"
			height={height}
			focusable="false"
			role="img"
			aria-label="Sparkline"
		>
			{#if showFill}
				<path
					d={areaD}
					fill={color}
					fill-opacity="0.12"
				/>
			{/if}
			<path
				d={pathD}
				fill="none"
				stroke={color}
				stroke-width="1.6"
				stroke-linecap="round"
				stroke-linejoin="round"
			/>
		</svg>
	</div>
{/if}
