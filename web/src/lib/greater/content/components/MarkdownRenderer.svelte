<!--
MarkdownRenderer component - Renders markdown content safely with syntax highlighting support.
Uses the unified ecosystem (remark + rehype) for ESM-compatible markdown processing.

@component
@example
```svelte
<MarkdownRenderer content="# Hello\n\nThis is **markdown**." />
```
-->
<script lang="ts">
	import { renderMarkdownToHtml } from '../markdown';

	interface Props {
		/**
		 * Markdown content to render.
		 */
		content: string;

		/**
		 * Enable syntax highlighting for code blocks (uses styling class).
		 * @default true
		 */
		enableCodeHighlight?: boolean;

		/**
		 * Enable clickable links.
		 * @default true
		 */
		enableLinks?: boolean;

		/**
		 * Open links in new tab.
		 * @default true
		 */
		openLinksInNewTab?: boolean;

		/**
		 * Additional CSS classes.
		 */
		class?: string;

		/**
		 * Callback when rendering is complete.
		 */
		onRenderComplete?: () => void;

		/**
		 * Callback on error.
		 */
		onError?: (error: Error) => void;
	}

	let {
		content,
		// enableCodeHighlight = true, // Handled by CSS
		enableLinks = true,
		openLinksInNewTab = true,
		class: className = '',
		onRenderComplete,
		onError,
		...restProps
	}: Props = $props();

	const renderedHtml = $derived.by(() => {
		return renderMarkdownToHtml(content, {
			enableLinks,
			openLinksInNewTab,
			onError: (error) => {
				if (onError && error instanceof Error) onError(error);
			},
		});
	});

	$effect(() => {
		if (renderedHtml && onRenderComplete) {
			onRenderComplete();
		}
	});
</script>

<div class="gr-markdown {className}" {...restProps}>
	<!-- eslint-disable-next-line svelte/no-at-html-tags -->
	{@html renderedHtml}
</div>
