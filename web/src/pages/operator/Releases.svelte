<!--
@component
Operator Releases — two-channel release timeline (lesser + lesser-body).

Project 39 M2.5 (issue #431). Renders two side-by-side columns sourcing
adoption-aware release history from the M2.8 endpoint
`/api/v1/operators/releases`. When the endpoint is not yet present,
falls back to a "telemetry pending" placeholder so the page is
navigable from day one (same pattern as the M2.3 drift banner and the
M1.6 stack endpoint).

The page is operator-only: the calling endpoint is operator-JWT gated;
no tenant content is read.

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins.
- Multi-tenant isolation: aggregation is fleet-level metadata only.
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.5
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListOperatorReleasesResult } from 'src/lib/api/operatorReleases';
	import { channelEntries, listOperatorReleases } from 'src/lib/api/operatorReleases';
	import ReleaseTimeline from 'src/lib/components/ReleaseTimeline.svelte';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, Heading, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let releases = $state<ListOperatorReleasesResult | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function load() {
		errorMessage = null;
		releases = null;

		loading = true;
		try {
			releases = await listOperatorReleases(token);
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
		void load();
	});

	const lesserEntries = $derived(
		releases?.kind === 'data' ? channelEntries(releases.data, 'lesser') : [],
	);
	const bodyEntries = $derived(
		releases?.kind === 'data' ? channelEntries(releases.data, 'lesser-body') : [],
	);
</script>

<div class="op-releases">
	<header class="op-releases__header">
		<div class="op-releases__title">
			<Heading level={2} size="xl">Releases</Heading>
			<Text color="secondary">
				Two-channel release timeline with per-version fleet adoption.
			</Text>
		</div>
		<div class="op-releases__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	{#if loading}
		<div class="op-releases__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Operator releases">{errorMessage}</Alert>
	{:else if releases?.kind === 'endpoint-pending'}
		<Alert variant="info" title="Release telemetry pending">
			<Text size="sm">
				Awaiting the operator releases aggregation endpoint
				(<span class="op-releases__mono">/api/v1/operators/releases</span>, M2.8). The page
				layout ships now so the M2.6 Stack Matrix + M2.11 Wire-all CTA have a stable home.
			</Text>
		</Alert>
	{:else if releases?.kind === 'data'}
		<div class="op-releases__columns">
			<Card variant="outlined" padding="lg">
				<ReleaseTimeline channel="lesser" entries={lesserEntries} />
			</Card>
			<Card variant="outlined" padding="lg">
				<ReleaseTimeline channel="lesser-body" entries={bodyEntries} />
			</Card>
		</div>
	{/if}
</div>

<style>
	.op-releases {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-releases__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.op-releases__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-releases__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-releases__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-releases__columns {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		gap: var(--gr-spacing-scale-4);
	}

	.op-releases__mono {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
	}

	@media (max-width: 880px) {
		.op-releases__columns {
			grid-template-columns: 1fr;
		}
	}
</style>
