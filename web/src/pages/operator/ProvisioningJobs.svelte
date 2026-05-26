<!--
@component
Operator Provisioning list — adds per-row Kind badges (Project 39 M2.3,
issue #429) and a top-of-page MCP-drift banner derived from the M2.9
`/api/v1/operators/instances/drift` aggregation endpoint. When the M2.9
endpoint is not yet present, the drift client returns an
`endpoint-pending` sentinel and the banner renders a "telemetry pending"
message rather than blocking the page (same fault-tolerance pattern as
the M1.6 stack endpoint).

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins.
- Multi-tenant isolation: every row is gated through the existing
  operator-JWT endpoint; drift aggregation is operator-scope only.
- Trust-API instance-auth untouched; SEC-9 / SEC-10 change-locks not
  engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.3
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		ListOperatorProvisionJobsResponse,
		ListOperatorUpdateJobsResult,
		OperatorInstancesDriftResult,
		OperatorProvisionJobListItem,
		OperatorUpdateJobListItem,
	} from 'src/lib/api/operatorProvisioning';
	import {
		listOperatorInstancesDrift,
		listOperatorProvisionJobs,
		listOperatorUpdateJobs,
		retryOperatorProvisionJob,
	} from 'src/lib/api/operatorProvisioning';
	import JobKindBadge from 'src/lib/components/JobKindBadge.svelte';
	import type { OperatorJobRow } from 'src/lib/components/operatorJobRow';
	import { mergeJobRows } from 'src/lib/components/operatorJobRow';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Link, Select, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let statusFilter = $state('queued');
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let provisionJobs = $state<OperatorProvisionJobListItem[]>([]);
	let updateJobsResult = $state<ListOperatorUpdateJobsResult | null>(null);

	let actingId = $state<string | null>(null);
	let actionError = $state<string | null>(null);

	// MCP-drift banner state — sourced from the M2.9 aggregation endpoint.
	// `null` while not yet loaded; `endpoint-pending` if the M2.9 backend
	// is not yet present; `data` once telemetry is available.
	let drift = $state<OperatorInstancesDriftResult | null>(null);
	let driftError = $state<string | null>(null);

	/**
	 * Unified feed: ProvisionJob rows + UpdateJob rows merged by
	 * `updated_at` desc. Until the operator-scope UpdateJob endpoint
	 * ships, the update half is empty and the page renders only
	 * ProvisionJobs (with an info note above the list explaining the
	 * partial state — see the template).
	 */
	const rows = $derived.by<OperatorJobRow[]>(() => {
		const updates: OperatorUpdateJobListItem[] =
			updateJobsResult?.kind === 'data' ? updateJobsResult.data.jobs : [];
		return mergeJobRows(provisionJobs, updates);
	});

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function badgeForStatus(status: string): { variant: 'outlined' | 'filled'; color: 'success' | 'warning' | 'error' | 'gray' } {
		const s = (status || '').toLowerCase();
		if (s === 'ok') return { variant: 'filled', color: 'success' };
		if (s === 'running' || s === 'queued') return { variant: 'outlined', color: 'warning' };
		if (s === 'error') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	async function load() {
		errorMessage = null;
		actionError = null;
		driftError = null;
		provisionJobs = [];
		updateJobsResult = null;
		// Keep `drift` across reloads so the banner doesn't flicker on filter
		// changes; only clear if a fresh load fails or the endpoint flips
		// from `data` → `endpoint-pending` (which is a meaningful change).

		loading = true;
		try {
			// Drift + UpdateJob aggregation are intentionally tolerated; if
			// either backend endpoint is unavailable or errors, the ProvisionJob
			// list must still render. Errors are surfaced inline (`driftError`
			// for drift; `updateJobsResult.kind === 'endpoint-pending'` for
			// the UpdateJob feed — see the template banner below).
			const driftPromise = listOperatorInstancesDrift(token).catch((err) => {
				driftError = formatError(err);
				return null;
			});

			const updateJobsPromise = listOperatorUpdateJobs(token, {
				status: statusFilter === 'all' ? 'all' : statusFilter,
				limit: 100,
			}).catch(() => null);

			const provisionJobsPromise = listOperatorProvisionJobs(token, {
				status: statusFilter === 'all' ? 'all' : statusFilter,
				limit: 100,
			});

			const [driftRes, updateRes, provisionRes] = await Promise.all([
				driftPromise,
				updateJobsPromise,
				provisionJobsPromise,
			]);
			drift = driftRes;
			updateJobsResult = updateRes;
			provisionJobs = provisionRes.jobs;
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

	async function retry(row: OperatorJobRow) {
		actionError = null;
		// Only ProvisionJobs have an operator retry endpoint today
		// (`POST /api/v1/operators/provisioning/jobs/{id}/retry`). UpdateJob
		// retry is the customer-portal flow and isn't exposed at operator
		// scope yet. The row's `retryable` flag gates the CTA so this guard
		// is defence-in-depth.
		if (row.source !== 'provision' || !row.retryable) {
			actionError = `Retry not supported for ${row.source} jobs at operator scope.`;
			return;
		}
		actingId = row.id;
		try {
			await retryOperatorProvisionJob(token, row.id);
			await load();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actingId = null;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="op-provisioning">
	<header class="op-provisioning__header">
		<div class="op-provisioning__title">
			<Heading level={2} size="xl">Provisioning jobs</Heading>
			<Text color="secondary">Observe and retry managed provisioning.</Text>
		</div>
		<div class="op-provisioning__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	{#if drift?.kind === 'data'}
		{@const count = drift.data.summary?.mcp_wire_stale ?? 0}
		{#if count > 0}
			<Alert variant="warning" title="MCP wire drift detected">
				<Text size="sm">
					{count} managed {count === 1 ? 'instance is' : 'instances are'} on a wire-stale MCP
					revision. Use the Stack Matrix or Wire-all CTA on
					<Link {...linkProps('/operator/releases')} variant="default">/operator/releases</Link>
					to remediate.
				</Text>
			</Alert>
		{:else}
			<Alert variant="info" title="Fleet wire-aligned">
				<Text size="sm">All managed instances are wire-aligned with their target MCP revision.</Text>
			</Alert>
		{/if}
	{:else if drift?.kind === 'endpoint-pending'}
		<Alert variant="info" title="MCP drift telemetry pending">
			<Text size="sm">
				Awaiting the operator drift-aggregation endpoint
				(<span class="op-provisioning__mono">/api/v1/operators/instances/drift</span>, M2.9).
				The provisioning list still renders normally while the backend ships.
			</Text>
		</Alert>
	{:else if driftError}
		<Alert variant="warning" title="MCP drift load failed">
			<Text size="sm">{driftError}</Text>
		</Alert>
	{/if}

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Filters</Heading>
		{/snippet}
		<div class="op-provisioning__filters">
			<div class="op-provisioning__field">
				<Text size="sm">Status</Text>
				<Select
					bind:value={statusFilter}
					options={[
						{ value: 'all', label: 'All' },
						{ value: 'queued', label: 'Queued' },
						{ value: 'running', label: 'Running' },
						{ value: 'error', label: 'Error' },
						{ value: 'ok', label: 'OK' },
					]}
				/>
			</div>
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Apply</Button>
		</div>
	</Card>

	{#if loading}
		<div class="op-provisioning__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Provisioning jobs">{errorMessage}</Alert>
	{:else if rows.length === 0}
		<Alert variant="info" title="No jobs">
			<Text size="sm">No jobs found for this filter.</Text>
		</Alert>
	{:else}
		{#if actionError}
			<Alert variant="error" title="Action failed">{actionError}</Alert>
		{/if}

		{#if updateJobsResult?.kind === 'endpoint-pending'}
			<Alert variant="info" title="UpdateJob fleet feed pending">
				<Text size="sm">
					Awaiting the operator UpdateJob aggregation endpoint
					(<span class="op-provisioning__mono">/api/v1/operators/updates</span>). Provision
					rows render below; update-lesser / update-body / wire-mcp rows surface as soon
					as the backend ships.
				</Text>
			</Alert>
		{/if}

		<div class="op-provisioning__list">
			{#each rows as row (row.id)}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<div class="op-provisioning__row">
							<div class="op-provisioning__row-left">
								<Heading level={3} size="lg"><span class="op-provisioning__mono">{row.id}</span></Heading>
								<!--
									Project 39 M2.3: per-row Kind badge derived per-source.
									ProvisionJobs → 'provision'; UpdateJobs map per kind /
									body_only / mcp_only via deriveUpdateJobKind() inside the
									merge normalizer.
								-->
								<JobKindBadge kind={row.kind} size="sm" />
								<Badge
									variant={badgeForStatus(row.status).variant}
									color={badgeForStatus(row.status).color}
									size="sm"
								>
									{row.status}
								</Badge>
							</div>
							<div class="op-provisioning__row-right">
								<CopyButton size="sm" text={row.id} />
							</div>
						</div>
					{/snippet}

					<div class="op-provisioning__meta">
						<Text size="sm" color="secondary">
							instance <span class="op-provisioning__mono">{row.instance_slug}</span>
							{#if row.step}
								· step <span class="op-provisioning__mono">{row.step}</span>
							{/if}
						</Text>
						<Text size="sm" color="secondary">
							updated <span class="op-provisioning__mono">{row.updated_at}</span>
							{#if typeof row.attempts === 'number'}
								· attempts <span class="op-provisioning__mono">{String(row.attempts)}</span>/{String(row.max_attempts || 0)}
							{/if}
						</Text>
						{#if row.run_id}
							<Text size="sm" color="secondary">
								run <span class="op-provisioning__mono">{row.run_id}</span>
							</Text>
						{/if}
						{#if row.request_id}
							<Text size="sm" color="secondary">
								request <span class="op-provisioning__mono">{row.request_id}</span>
							</Text>
						{/if}
						{#if row.error_code || row.error_message}
							<Text size="sm" color="secondary">
								<span class="op-provisioning__mono">{row.error_code || 'error'}</span> {row.error_message || ''}
							</Text>
						{/if}
					</div>

					<div class="op-provisioning__row">
						<Link {...linkProps(row.detail_path)} variant="default">View</Link>
						<Link {...linkProps(`/operator/instances/${row.instance_slug}`)} variant="ghost">Open instance</Link>
						{#if row.retryable && row.status === 'error'}
							<Button variant="solid" onclick={() => void retry(row)} disabled={actingId === row.id}>Retry</Button>
						{/if}
						{#if actingId === row.id}
							<div class="op-provisioning__loading-inline">
								<Spinner size="sm" />
								<Text size="sm">Working…</Text>
							</div>
						{/if}
					</div>
				</Card>
			{/each}
		</div>
	{/if}
</div>

<style>
	.op-provisioning {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-provisioning__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-provisioning__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-provisioning__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-provisioning__filters {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: flex-end;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.op-provisioning__field {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		min-width: 240px;
	}

	.op-provisioning__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-provisioning__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
	}

	.op-provisioning__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.op-provisioning__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-provisioning__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-provisioning__meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-provisioning__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-provisioning__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
