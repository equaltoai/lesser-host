<!--
@component
ProgressBar — CSP-safe progress bar matching the Project 42 design fixture.

Renders a horizontal bar with a filled portion representing progress.
Tone variants express semantic states (`.bar__fill--warning`,
`.bar__fill--error`, `.bar__fill--success`, `.bar__fill--accent`).
When `tone` is omitted the default gradient fill is used.

Strict-CSP safe: the design fixture's `style="width: X%"` is replaced
with a `data-ratio` integer attribute (0–100) + CSS attribute selectors
in `ProgressBar.css`. No inline styles are emitted.

Consumes `.bar` and `.bar__fill` CSS classes from the app stylesheet,
plus tone variants.

@example
```svelte
<ProgressBar value={42} max={100} />
<ProgressBar value={85} max={100} tone="warning" />
<ProgressBar value={95} max={100} tone="error" />
```

@public
-->
<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';

	/** Available tone variants for the progress bar fill. */
	type ProgressBarTone = 'success' | 'warning' | 'error' | 'accent';

	interface Props extends HTMLAttributes<HTMLDivElement> {
		/** Current value. @public */
		value: number;

		/** Maximum value. Defaults to 100. @public */
		max?: number;

		/**
		 * Visual tone for the fill. When omitted, the default gradient
		 * (`var(--ds-action-gradient)`) is used.
		 * @public
		 */
		tone?: ProgressBarTone;

		/** Additional CSS classes on the wrapper. @public */
		class?: string;

		/** Accessible label for the progress bar. @public */
		label?: string;
	}

	let {
		value,
		max = 100,
		tone,
		class: className = '',
		label,
		...restProps
	}: Props = $props();

	const ratioStep = $derived(() => {
		if (!Number.isFinite(max) || max <= 0) return 0;
		const pct = Math.min(100, Math.max(0, (value / max) * 100));
		return Math.round(pct);
	});

	const fillClass = $derived.by(() => {
		const classes = ['bar__fill'];
		if (tone) {
			classes.push(`bar__fill--${tone}`);
		}
		return classes.join(' ');
	});

	const accessibleLabel = $derived(
		label ?? `Progress: ${ratioStep()}%`
	);
</script>

<div
	class="bar"
	role="progressbar"
	aria-valuenow={value}
	aria-valuemin={0}
	aria-valuemax={max}
	aria-valuetext={accessibleLabel}
	aria-label={!label ? accessibleLabel : undefined}
	aria-labelledby={undefined}
	{...restProps}
>
	<div class={fillClass} data-ratio={ratioStep()}></div>
</div>
