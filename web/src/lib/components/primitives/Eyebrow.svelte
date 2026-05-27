<!--
@component
Eyebrow — uppercase tracked label primitive matching the Project 42 design fixture.

Renders as a `<p>` element with the `.eyebrow` CSS class that consumes
`--ds-eyebrow-size`, `--ds-eyebrow-weight`, `--ds-eyebrow-track`,
`--ds-fg-3`, and `--ds-font-sans` tokens.

Strict-CSP safe: no inline styles, no inline event handlers.
Matches the design fixture's default `as="p"` rendering.

@example
```svelte
<Eyebrow>Fleet</Eyebrow>
<Eyebrow class="my-extra">Billing overview</Eyebrow>
```

@public
-->
<script lang="ts">
	import type { HTMLAttributes } from 'svelte/elements';
	import type { Snippet } from 'svelte';

	interface Props extends HTMLAttributes<HTMLParagraphElement> {
		/** Additional CSS classes. @public */
		class?: string;

		/** Content snippet. @public */
		children: Snippet;
	}

	let { class: className = '', children, ...restProps }: Props = $props();

	const rootClass = $derived(['eyebrow', className].filter(Boolean).join(' '));
</script>

<p class={rootClass} {...restProps}>
	{@render children()}
</p>
