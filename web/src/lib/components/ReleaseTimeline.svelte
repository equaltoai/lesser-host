<!--
@component
ReleaseTimeline — vertical timeline of releases for a single channel.

Project 39 M2.5 (issue #431) introduces the component as a thin
scaffold so the /operator/releases page can render two side-by-side
columns. M2.7 (issue #433) fleshes out the version-card content
(version string, released-at, latest badge, breaking badge, adoption
bar) — both issues touch this file by design.

The scaffold renders:
- channel header
- a flat list of releases with version + released-at
- a placeholder block where M2.7 will land the full version card

Strict-CSP-safe: no inline scripts / styles / third-party origins.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.5, M2.7
-->
<script lang="ts">
	import type { ReleaseEntry, ReleaseChannel } from 'src/lib/api/operatorReleases';
	import { Text } from 'src/lib/ui';

	let {
		channel,
		entries,
	}: {
		channel: ReleaseChannel;
		entries: ReleaseEntry[];
	} = $props();

	const heading = $derived(channel === 'lesser' ? 'lesser' : 'lesser-body');
</script>

<section class="release-timeline" aria-label="{heading} release timeline">
	<header class="release-timeline__header">
		<span class="release-timeline__channel">{heading}</span>
	</header>

	{#if entries.length === 0}
		<Text size="sm" color="secondary">No releases on record for this channel.</Text>
	{:else}
		<ol class="release-timeline__list">
			{#each entries as entry (entry.version)}
				<li class="release-timeline__item">
					<span class="release-timeline__version">{entry.version}</span>
					{#if entry.released_at}
						<Text size="sm" color="secondary">{entry.released_at}</Text>
					{/if}
				</li>
			{/each}
		</ol>
	{/if}
</section>

<style>
	.release-timeline {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		min-width: 0;
	}

	.release-timeline__header {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: baseline;
		padding-bottom: var(--gr-spacing-scale-2);
		border-bottom: 1px solid var(--gr-color-border, currentColor);
	}

	.release-timeline__channel {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
		font-weight: 600;
		font-size: 0.95rem;
		color: var(--ds-fg-1, inherit);
	}

	.release-timeline__list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.release-timeline__item {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.release-timeline__version {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
		font-size: 0.95rem;
		color: var(--ds-fg-1, inherit);
	}
</style>
