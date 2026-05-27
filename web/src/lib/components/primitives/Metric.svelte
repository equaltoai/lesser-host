<!--
@component
Metric — metric tile primitive matching the Project 42 design fixture.

@license AGPL-3.0-only

Displays a label, value, optional subtext, optional delta with direction
indicator, optional icon, and tone/accent support. Consumes `.metric`,
`.metric__label`, `.metric__value`, `.metric__delta`, `.metric__delta--up`,
`.metric__delta--down` CSS classes from the app stylesheet.

Strict-CSP safe: no inline styles, no inline event handlers.
Tone is expressed via CSS classes mapped from keyword props, not arbitrary
color values (CSP-driven deviation from the design fixture's `accent` prop).

@example
```svelte
<Metric label="Active users" value="1,243" />
<Metric label="Revenue" value="$4,200" delta="+12%" deltaDir="up" tone="success" />
```

@public
-->
<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import type { Snippet } from 'svelte';

	/** Visual tone for the metric value. Maps to CSS class `metric__value--<tone>`. */
	type MetricTone = 'success' | 'warning' | 'error' | 'info' | 'accent';

	interface Props extends HTMLAttributes<HTMLDivElement> {
		/** Required visible label (e.g. "Active users"). Rendered as `.metric__label`. @public */
		label: string;

		/** Required metric value (e.g. "1,243"). Rendered as `.metric__value`. @public */
		value: string;

		/** Optional subtext rendered below the value. @public */
		sub?: string;

		/** Optional delta string (e.g. "+12%", "-3"). @public */
		delta?: string;

		/** Direction for the delta arrow indicator. @public */
		deltaDir?: 'up' | 'down';

		/** Optional icon rendered alongside the label. @public */
		icon?: Snippet;

		/** Visual tone for the metric value. CSP-safe alternative to inline accent color. @public */
		tone?: MetricTone;

		/** Additional CSS classes. @public */
		class?: string;
	}

	let {
		label,
		value,
		sub,
		delta,
		deltaDir,
		icon,
		tone,
		class: className = '',
		...restProps
	}: Props = $props();

	const rootClass = $derived(
		['metric', className].filter(Boolean).join(' ')
	);

	const valueClass = $derived(
		['metric__value', tone ? `metric__value--${tone}` : ''].filter(Boolean).join(' ')
	);

	const deltaClass = $derived.by(() => {
		const dirs: Record<string, string> = {
			up: 'metric__delta--up',
			down: 'metric__delta--down',
		};
		return ['metric__delta', deltaDir ? dirs[deltaDir] ?? '' : '']
			.filter(Boolean)
			.join(' ');
	});
</script>

<div class={rootClass} {...restProps}>
	<div class="row row--between">
		<span class="metric__label">{label}</span>
		{#if icon}
			<span class="metric__icon">{@render icon()}</span>
		{/if}
	</div>
	<div class={valueClass}>{value}</div>
	{#if sub}
		<div class="metric__delta metric__delta--sub">{sub}</div>
	{/if}
	{#if delta}
		<div class={deltaClass}>
			{#if deltaDir === 'up'}
				<span class="metric__delta-arrow" aria-hidden="true">↗&#32;</span>
			{:else if deltaDir === 'down'}
				<span class="metric__delta-arrow" aria-hidden="true">↘&#32;</span>
			{/if}
			{delta}
		</div>
	{/if}
</div>
