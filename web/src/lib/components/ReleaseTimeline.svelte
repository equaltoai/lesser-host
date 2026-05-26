<!--
@component
ReleaseTimeline — vertical timeline of releases for a single channel.

Project 39 M2.5 (issue #431) introduced the file as a scaffold; M2.7
(issue #433) fleshes it out with the full version-card content:

  - version string (monospace, prominent)
  - released-at timestamp
  - "Latest" badge when `is_latest === true`
  - "Breaking" badge when `is_breaking === true`
  - adoption bar (0..100%) rendered from `adoption_pct`
  - optional summary blurb

Each version is a list item on a vertical-rail timeline; connector
lines join consecutive cards so the timeline reads as a continuous
sequence. The latest release is visually emphasized (filled indicator
+ amber accent) so operators can locate the cutting edge at a glance.

The adoption bar is rendered as an inline SVG `<rect>` whose width is
an SVG attribute — not a CSS inline style — so it does NOT violate the
strict `style-src 'self'` CSP that blocks `style=""` attributes. Bar
fill colour follows the canonical operator-chrome status palette
(success ≥80%, warning 40-80%, error <40%).

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles, no third-party origins.
- Multi-tenant isolation: render-only; parent fetches operator-scope
  release-aggregation data and passes it in.
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.5, M2.7
-->
<script lang="ts" module>
	/**
	 * Map an adoption percentage to a tone band so the bar fill colour
	 * tracks fleet maturity at a glance. The bands match the rest of
	 * the operator-chrome status palette (success ≥80%, warning 40-80%,
	 * error <40%).
	 */
	export type AdoptionBand = 'high' | 'mid' | 'low';
	export function adoptionBand(pct: number | undefined): AdoptionBand {
		const v = typeof pct === 'number' && Number.isFinite(pct) ? pct : 0;
		if (v >= 80) return 'high';
		if (v >= 40) return 'mid';
		return 'low';
	}

	/** Clamp adoption to [0, 100] for the width calculation. */
	export function clampAdoption(pct: number | undefined): number {
		if (typeof pct !== 'number' || !Number.isFinite(pct)) return 0;
		if (pct < 0) return 0;
		if (pct > 100) return 100;
		return pct;
	}
</script>

<script lang="ts">
	import type { ReleaseEntry, ReleaseChannel } from 'src/lib/api/operatorReleases';
	import { Badge, Text } from 'src/lib/ui';

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
		<Text size="sm" color="secondary">
			{entries.length} {entries.length === 1 ? 'release' : 'releases'}
		</Text>
	</header>

	{#if entries.length === 0}
		<Text size="sm" color="secondary">No releases on record for this channel.</Text>
	{:else}
		<ol class="release-timeline__list">
			{#each entries as entry (entry.version)}
				{@const pct = clampAdoption(entry.adoption_pct)}
				{@const band = adoptionBand(entry.adoption_pct)}
				{@const hasAdoption = typeof entry.adoption_pct === 'number'}
				<li
					class="release-timeline__item"
					class:release-timeline__item--latest={entry.is_latest}
					data-latest={entry.is_latest ? 'true' : 'false'}
				>
					<span class="release-timeline__indicator" aria-hidden="true"></span>
					<div class="release-timeline__card">
						<div class="release-timeline__row">
							<span class="release-timeline__version">{entry.version}</span>
							{#if entry.is_latest}
								<Badge color="warning" variant="filled" size="sm">Latest</Badge>
							{/if}
							{#if entry.is_breaking}
								<Badge color="error" variant="filled" size="sm">Breaking</Badge>
							{/if}
						</div>
						{#if entry.released_at}
							<Text size="sm" color="secondary">Released {entry.released_at}</Text>
						{/if}
						{#if entry.summary}
							<Text size="sm">{entry.summary}</Text>
						{/if}
						{#if hasAdoption}
							<div
								class="release-timeline__adoption"
								role="progressbar"
								aria-valuemin="0"
								aria-valuemax="100"
								aria-valuenow={pct}
								aria-label="{entry.version} adoption"
							>
								<!--
									SVG bar (not CSS-styled-width) so the dynamic fill
									width is an SVG attribute rather than an inline
									style="" attribute. Inline style attributes would
									violate the strict `style-src 'self'` CSP.
								-->
								<svg
									class="release-timeline__bar"
									viewBox="0 0 100 4"
									preserveAspectRatio="none"
									aria-hidden="true"
									focusable="false"
								>
									<rect class="release-timeline__bar-track" x="0" y="0" width="100" height="4" rx="2" />
									<rect
										class="release-timeline__bar-fill release-timeline__bar-fill--{band}"
										x="0"
										y="0"
										width={pct}
										height="4"
										rx="2"
									/>
								</svg>
								<Text size="sm" color="secondary">{Math.round(pct)}% adoption</Text>
							</div>
						{/if}
					</div>
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
		justify-content: space-between;
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
		gap: var(--gr-spacing-scale-4);
	}

	.release-timeline__item {
		display: grid;
		grid-template-columns: 1.25rem 1fr;
		gap: var(--gr-spacing-scale-3);
		align-items: start;
		position: relative;
		padding-left: 0;
	}

	/* Connector line between consecutive cards. */
	.release-timeline__item:not(:last-child)::before {
		content: '';
		position: absolute;
		left: 0.6rem;
		top: 1.4rem;
		bottom: -1rem;
		width: 1px;
		background: var(--gr-color-border, currentColor);
		opacity: 0.35;
	}

	.release-timeline__indicator {
		width: 1rem;
		height: 1rem;
		border-radius: 999px;
		border: 2px solid var(--gr-color-border, currentColor);
		background: transparent;
		margin-top: 0.2rem;
	}

	.release-timeline__item--latest .release-timeline__indicator {
		background: var(--ds-warning-500);
		border-color: var(--ds-warning-500);
		box-shadow: 0 0 0 4px color-mix(in srgb, var(--ds-warning-500) 28%, transparent);
	}

	.release-timeline__card {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		padding-bottom: var(--gr-spacing-scale-2);
		min-width: 0;
	}

	.release-timeline__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.release-timeline__version {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
		font-size: 1rem;
		font-weight: 600;
		color: var(--ds-fg-1, inherit);
	}

	.release-timeline__item--latest .release-timeline__version {
		color: var(--ds-warning-500);
	}

	.release-timeline__adoption {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.release-timeline__bar {
		display: block;
		width: 100%;
		height: 0.5rem;
	}

	.release-timeline__bar-track {
		fill: var(--ds-bg-raised, rgba(255, 255, 255, 0.1));
	}

	.release-timeline__bar-fill--high {
		fill: var(--ds-success-500);
	}
	.release-timeline__bar-fill--mid {
		fill: var(--ds-warning-500);
	}
	.release-timeline__bar-fill--low {
		fill: var(--ds-error-500);
	}
</style>
