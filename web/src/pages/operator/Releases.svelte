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
	import type { OperatorInstancesDriftResult } from 'src/lib/api/operatorProvisioning';
	import { listOperatorInstancesDrift } from 'src/lib/api/operatorProvisioning';
	import type { ListOperatorReleasesResult } from 'src/lib/api/operatorReleases';
	import { channelEntries, listOperatorReleases } from 'src/lib/api/operatorReleases';
	import ReleaseTimeline from 'src/lib/components/ReleaseTimeline.svelte';
	import StackMatrix from 'src/lib/components/StackMatrix.svelte';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, Heading, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let releases = $state<ListOperatorReleasesResult | null>(null);
	let drift = $state<OperatorInstancesDriftResult | null>(null);
	let wireActionInfo = $state<string | null>(null);

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
		wireActionInfo = null;
		releases = null;
		drift = null;

		loading = true;
		try {
			// Load releases + drift in parallel. Drift powers the Stack Matrix;
			// neither blocks the other.
			const [r, d] = await Promise.all([
				listOperatorReleases(token),
				listOperatorInstancesDrift(token).catch(() => null),
			]);
			releases = r;
			drift = d;
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

	/**
	 * Per-row "Wire MCP" handler emitted by the Stack Matrix. The actual
	 * remediation endpoint is M2.10; until that ships we surface an
	 * info banner explaining the action, the slug, and the next-step
	 * link so the operator can take manual action from the per-slug
	 * detail page.
	 */
	function handleStackAction(slug: string, action: 'wire-mcp') {
		if (action === 'wire-mcp') {
			wireActionInfo = `Wire MCP requested for "${slug}". The fleet remediation endpoint (M2.10) ships in a follow-on commit; use the per-instance detail page for manual remediation in the meantime.`;
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

	{#if wireActionInfo}
		<Alert variant="info" title="Wire MCP requested">
			<Text size="sm">{wireActionInfo}</Text>
		</Alert>
	{/if}

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Stack matrix</Heading>
		{/snippet}
		{#if drift?.kind === 'data'}
			<StackMatrix entries={drift.data.instances} onAction={handleStackAction} />
		{:else if drift?.kind === 'endpoint-pending'}
			<Text size="sm" color="secondary">
				Stack matrix telemetry is pending the M2.9
				<span class="op-releases__mono">/api/v1/operators/instances/drift</span> endpoint.
				The matrix component lights up the moment the backend ships.
			</Text>
		{:else}
			<Text size="sm" color="secondary">No fleet drift data loaded.</Text>
		{/if}
	</Card>
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
