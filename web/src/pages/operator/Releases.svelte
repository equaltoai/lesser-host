<!--
@component
Operator Releases — two-channel release timeline (lesser + lesser-body)
plus the fleet Stack Matrix.

Project 39 M2.5 (issue #431) shipped the two-channel timeline scaffold
plus a forward-compat Stack Matrix card whose narrative pointed at the
then-pending M2.9 drift endpoint. PR #513 landed the M2.8 / M2.9 / M2.10
operator backends, so the Stack Matrix is now first-class: M2.11
(issue #437) wires it through to the live `/api/v1/operators/instances/drift`
aggregation and surfaces a per-summary drift strip above the matrix so
the operator can read fleet posture at a glance.

The page is operator-only: every endpoint it consumes is operator-JWT
gated; no tenant content is read.

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins.
- Multi-tenant isolation: aggregation is fleet-level metadata only.
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.5, M2.11
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { OperatorInstancesDriftResult } from 'src/lib/api/operatorProvisioning';
	import { listOperatorInstancesDrift } from 'src/lib/api/operatorProvisioning';
	import type { ListOperatorReleasesResult } from 'src/lib/api/operatorReleases';
	import { channelVersions, listOperatorReleases } from 'src/lib/api/operatorReleases';
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

	const lesserVersions = $derived(
		releases?.kind === 'data' ? channelVersions(releases.data, 'lesser') : [],
	);
	const bodyVersions = $derived(
		releases?.kind === 'data' ? channelVersions(releases.data, 'lesser-body') : [],
	);
	const fleetTotal = $derived(
		releases?.kind === 'data' ? releases.data.fleet_total ?? 0 : 0,
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
				The operator releases aggregation endpoint
				(<span class="op-releases__mono">/api/v1/operators/releases</span>, M2.8) did not
				respond with adoption data for the current request. The Stack Matrix below still
				reads from the M2.9 drift endpoint and renders independently.
			</Text>
		</Alert>
	{:else if releases?.kind === 'data'}
		<div class="op-releases__columns">
			<Card variant="outlined" padding="lg">
				<ReleaseTimeline channelId="lesser" versions={lesserVersions} fleetTotal={fleetTotal} />
			</Card>
			<Card variant="outlined" padding="lg">
				<ReleaseTimeline channelId="lesser-body" versions={bodyVersions} fleetTotal={fleetTotal} />
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
			{@const summary = drift.data.summary}
			<dl
				class="op-releases__summary"
				aria-label="Fleet drift summary"
			>
				<div class="op-releases__summary-cell">
					<dt><Text size="xs" color="secondary">Total</Text></dt>
					<dd class="op-releases__summary-value">{summary?.total ?? drift.data.instances.length}</dd>
				</div>
				<div class="op-releases__summary-cell">
					<dt><Text size="xs" color="secondary">Lesser stale</Text></dt>
					<dd
						class="op-releases__summary-value"
						data-tone={(summary?.lesser_stale ?? 0) > 0 ? 'warn' : 'ok'}
					>{summary?.lesser_stale ?? 0}</dd>
				</div>
				<div class="op-releases__summary-cell">
					<dt><Text size="xs" color="secondary">Body stale</Text></dt>
					<dd
						class="op-releases__summary-value"
						data-tone={(summary?.body_stale ?? 0) > 0 ? 'warn' : 'ok'}
					>{summary?.body_stale ?? 0}</dd>
				</div>
				<div class="op-releases__summary-cell">
					<dt><Text size="xs" color="secondary">MCP wire-stale</Text></dt>
					<dd
						class="op-releases__summary-value"
						data-tone={(summary?.mcp_wire_stale ?? 0) > 0 ? 'warn' : 'ok'}
					>{summary?.mcp_wire_stale ?? 0}</dd>
				</div>
			</dl>
			<StackMatrix entries={drift.data.instances} onAction={handleStackAction} />
		{:else if drift?.kind === 'endpoint-pending'}
			<Text size="sm" color="secondary">
				The operator drift aggregation endpoint
				(<span class="op-releases__mono">/api/v1/operators/instances/drift</span>) did not
				respond with drift data for the current request. Use Refresh to retry.
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

	/*
	 * M2.11 drift summary strip. Reads directly from
	 * `drift.data.summary` (total / lesser_stale / body_stale / mcp_wire_stale)
	 * so the operator can read fleet posture before scanning the matrix.
	 * `data-tone="warn"` is the single dynamic state; rendered as a static
	 * attribute (not an inline `style:` directive) to keep the strict
	 * `style-src 'self'` CSP intact — same pattern as PR #512 M2.7
	 * (SVG adoption bar) but with even less moving parts since it's a
	 * count, not a width.
	 */
	.op-releases__summary {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
		gap: var(--gr-spacing-scale-3);
		margin: 0 0 var(--gr-spacing-scale-4) 0;
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border, currentColor);
		border-radius: 6px;
		background: rgba(255, 255, 255, 0.02);
	}

	.op-releases__summary-cell {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		min-width: 0;
	}

	.op-releases__summary-cell dt {
		margin: 0;
	}

	.op-releases__summary-cell dd {
		margin: 0;
	}

	.op-releases__summary-value {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
		font-size: 1.1rem;
		font-weight: 600;
		color: var(--ds-fg-1, inherit);
	}

	.op-releases__summary-value[data-tone='warn'] {
		color: #f59e0b;
	}

	.op-releases__summary-value[data-tone='ok'] {
		color: #22c55e;
	}

	@media (max-width: 880px) {
		.op-releases__columns {
			grid-template-columns: 1fr;
		}
	}
</style>
