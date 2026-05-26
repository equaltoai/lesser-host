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
	import type {
		OperatorInstancesDriftResult,
		RemediateMCPDriftResponse,
	} from 'src/lib/api/operatorProvisioning';
	import { listOperatorInstancesDrift, remediateMCPDrift } from 'src/lib/api/operatorProvisioning';
	import type { ListOperatorReleasesResult } from 'src/lib/api/operatorReleases';
	import { channelVersions, listOperatorReleases } from 'src/lib/api/operatorReleases';
	import ReleaseTimeline from 'src/lib/components/ReleaseTimeline.svelte';
	import StackMatrix from 'src/lib/components/StackMatrix.svelte';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Button, Card, Heading, Link, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let releases = $state<ListOperatorReleasesResult | null>(null);
	let drift = $state<OperatorInstancesDriftResult | null>(null);
	let wireActionInfo = $state<string | null>(null);

	// M2.12 wire-all state: in-flight flag, last result (kept so idempotent
	// re-clicks reflect 0-created and a non-zero skipped count), and a
	// per-attempt error string. We don't store the job IDs in this scope
	// because the per-job UI lives at /operator/provisioning — the toast
	// links there for follow-up.
	let wireAllPending = $state(false);
	let wireAllResult = $state<RemediateMCPDriftResponse | null>(null);
	let wireAllError = $state<string | null>(null);

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
		// Drop a stale wire-all result so the toast doesn't persist across
		// a manual refresh; the per-attempt error similarly resets.
		wireAllResult = null;
		wireAllError = null;

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
	 * Per-row "Wire MCP" handler emitted by the Stack Matrix. The fleet
	 * remediation endpoint (M2.10) operates fleet-wide via the "Wire all"
	 * CTA above; per-row remediation is delegated to the per-instance
	 * detail page where the operator owns the call site. Inform the
	 * operator instead of silently doing nothing.
	 */
	function handleStackAction(slug: string, action: 'wire-mcp') {
		if (action === 'wire-mcp') {
			wireActionInfo = `Wire MCP requested for "${slug}". Use the "Wire all" button above to remediate every wire-stale instance, or open /operator/instances/${slug} for per-instance control.`;
		}
	}

	/**
	 * M2.12 wire-all CTA. Calls POST /api/v1/operators/instances/remediate-mcp-drift
	 * (M2.10 backend) and stores the per-attempt result so the toast can
	 * reflect both created and skipped counts. The endpoint is idempotent
	 * via GSI2 UPDATE_ACTIVE on the backend — re-clicks land 0-created /
	 * non-zero-skipped, which the toast wording captures explicitly.
	 *
	 * After a successful remediation, we re-fetch drift so the summary
	 * strip + matrix reflect the post-remediation state. (The wire-mcp
	 * UpdateJobs are async; drift recomputation here shows the *queued*
	 * MCP-only jobs are in flight, not their eventual completion.)
	 */
	async function wireAll() {
		wireAllError = null;
		wireAllResult = null;
		wireAllPending = true;
		try {
			wireAllResult = await remediateMCPDrift(token);
			// Re-fetch drift only — releases telemetry is independent of
			// the wire-all action.
			drift = await listOperatorInstancesDrift(token).catch(() => drift);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			wireAllError = formatError(err);
		} finally {
			wireAllPending = false;
		}
	}

	const wireStaleCount = $derived(
		drift?.kind === 'data' ? drift.data.summary?.mcp_wire_stale ?? 0 : 0,
	);

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

	<!--
		M2.12 top-of-page MCP-drift alert + "Wire all" CTA. Mirrors the
		alert on /operator/provisioning, but adds the fleet remediation
		button inline since this page is the canonical home for the
		matrix view that surfaces wire-stale rows. The button is enabled
		only when drift has loaded AND `summary.mcp_wire_stale > 0`.
	-->
	{#if drift?.kind === 'data'}
		{#if wireStaleCount > 0}
			<Alert variant="warning" title="MCP wire drift detected">
				<div class="op-releases__wireall">
					<Text size="sm">
						{wireStaleCount}
						managed {wireStaleCount === 1 ? 'instance is' : 'instances are'} on a wire-stale
						MCP revision. The "Wire all" CTA queues one MCP-only UpdateJob per affected
						slug; idempotent on re-click via the active-jobs guard.
					</Text>
					<div class="op-releases__wireall-actions">
						<Button
							variant="solid"
							onclick={() => void wireAll()}
							disabled={wireAllPending}
						>
							{wireAllPending ? 'Wiring…' : `Wire all (${wireStaleCount})`}
						</Button>
					</div>
				</div>
			</Alert>
		{:else}
			<Alert variant="info" title="Fleet wire-aligned">
				<Text size="sm">All managed instances are wire-aligned with their target MCP revision.</Text>
			</Alert>
		{/if}
	{/if}

	<!--
		Wire-all result + error toasts. We surface both `created` and
		`skipped` so the operator can distinguish first-click remediation
		from a no-op idempotent re-click. The "View jobs" link routes
		to /operator/provisioning where the per-job rows render.
	-->
	{#if wireAllError}
		<Alert variant="error" title="Wire all failed">
			<Text size="sm">{wireAllError}</Text>
		</Alert>
	{:else if wireAllResult}
		{#if wireAllResult.created > 0}
			<Alert variant="success" title="MCP wire-all queued">
				<Text size="sm">
					Queued {wireAllResult.created}
					MCP-only {wireAllResult.created === 1 ? 'job' : 'jobs'}{wireAllResult.skipped > 0
						? `; skipped ${wireAllResult.skipped} already-active`
						: ''}.
					<Link {...linkProps('/operator/provisioning')} variant="default">View jobs ↗</Link>
				</Text>
			</Alert>
		{:else if wireAllResult.skipped > 0}
			<Alert variant="info" title="MCP wire-all idempotent">
				<Text size="sm">
					No new jobs created — {wireAllResult.skipped}
					{wireAllResult.skipped === 1 ? 'slug already has' : 'slugs already have'} an active
					MCP-only UpdateJob in flight. Re-check once those jobs land.
				</Text>
			</Alert>
		{:else}
			<Alert variant="info" title="MCP wire-all no-op">
				<Text size="sm">
					Nothing to do — no instances reported wire-stale at request time.
				</Text>
			</Alert>
		{/if}
	{/if}

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

	/*
	 * M2.12 wire-all CTA layout — stack body text + action buttons
	 * vertically with a small gap so the button has visual room and
	 * doesn't fight the alert title for the same baseline. CSP-safe
	 * (no inline styles).
	 */
	.op-releases__wireall {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.op-releases__wireall-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		flex-wrap: wrap;
	}

	@media (max-width: 880px) {
		.op-releases__columns {
			grid-template-columns: 1fr;
		}
	}
</style>
