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
		OperatorInstancesDriftResult,
		OperatorProvisionJobListItem,
	} from 'src/lib/api/operatorProvisioning';
	import {
		listOperatorInstancesDrift,
		listOperatorProvisionJobs,
		retryOperatorProvisionJob,
	} from 'src/lib/api/operatorProvisioning';
	import JobKindBadge from 'src/lib/components/JobKindBadge.svelte';
	import { deriveProvisionJobKind } from 'src/lib/components/jobKind';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Link, Select, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let statusFilter = $state('queued');
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let data = $state<ListOperatorProvisionJobsResponse | null>(null);

	let actingId = $state<string | null>(null);
	let actionError = $state<string | null>(null);

	// MCP-drift banner state — sourced from the M2.9 aggregation endpoint.
	// `null` while not yet loaded; `endpoint-pending` if the M2.9 backend
	// is not yet present; `data` once telemetry is available.
	let drift = $state<OperatorInstancesDriftResult | null>(null);
	let driftError = $state<string | null>(null);

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
		data = null;
		// Keep `drift` across reloads so the banner doesn't flicker on filter
		// changes; only clear if a fresh load fails or the endpoint flips
		// from `data` → `endpoint-pending` (which is a meaningful change).

		loading = true;
		try {
			// Drift aggregation is intentionally tolerated; if the M2.9 endpoint
			// is unavailable or the call itself errors, the provisioning list
			// must still render. Errors are surfaced inline via `driftError`.
			const driftPromise = listOperatorInstancesDrift(token).catch((err) => {
				driftError = formatError(err);
				return null;
			});

			const jobsPromise = listOperatorProvisionJobs(token, {
				status: statusFilter === 'all' ? 'all' : statusFilter,
				limit: 100,
			});

			const [driftRes, jobs] = await Promise.all([driftPromise, jobsPromise]);
			drift = driftRes;
			data = jobs;
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

	async function retry(job: OperatorProvisionJobListItem) {
		actionError = null;
		actingId = job.id;
		try {
			await retryOperatorProvisionJob(token, job.id);
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
		{@const count = drift.data.mcp_drift_count ?? 0}
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
	{:else if data && data.jobs.length === 0}
		<Alert variant="info" title="No jobs">
			<Text size="sm">No jobs found for this filter.</Text>
		</Alert>
	{:else if data}
		{#if actionError}
			<Alert variant="error" title="Action failed">{actionError}</Alert>
		{/if}

		<div class="op-provisioning__list">
			{#each data.jobs as job (job.id)}
				<Card variant="outlined" padding="lg">
					{#snippet header()}
						<div class="op-provisioning__row">
							<div class="op-provisioning__row-left">
								<Heading level={3} size="lg"><span class="op-provisioning__mono">{job.id}</span></Heading>
								<!--
									Project 39 M2.3: per-row Kind badge sits next to the status
									badge so operators can scan the queue by deploy class.
									ProvisionJobs are always 'provision'; UpdateJob feed light
									up once the operator UpdateJob endpoint exists (post-M2.10).
								-->
								<JobKindBadge kind={deriveProvisionJobKind()} size="sm" />
								<Badge
									variant={badgeForStatus(job.status).variant}
									color={badgeForStatus(job.status).color}
									size="sm"
								>
									{job.status}
								</Badge>
							</div>
							<div class="op-provisioning__row-right">
								<CopyButton size="sm" text={job.id} />
							</div>
						</div>
					{/snippet}

					<div class="op-provisioning__meta">
						<Text size="sm" color="secondary">
							instance <span class="op-provisioning__mono">{job.instance_slug}</span>
							{#if job.step}
								· step <span class="op-provisioning__mono">{job.step}</span>
							{/if}
						</Text>
						<Text size="sm" color="secondary">
							updated <span class="op-provisioning__mono">{job.updated_at}</span>
							· attempts <span class="op-provisioning__mono">{String(job.attempts)}</span>/{String(job.max_attempts || 0)}
						</Text>
						{#if job.run_id}
							<Text size="sm" color="secondary">
								run <span class="op-provisioning__mono">{job.run_id}</span>
							</Text>
						{/if}
						{#if job.request_id}
							<Text size="sm" color="secondary">
								request <span class="op-provisioning__mono">{job.request_id}</span>
							</Text>
						{/if}
						{#if job.error_code || job.error_message}
							<Text size="sm" color="secondary">
								<span class="op-provisioning__mono">{job.error_code || 'error'}</span> {job.error_message || ''}
							</Text>
						{/if}
					</div>

					<div class="op-provisioning__row">
						<Link {...linkProps(`/operator/provisioning/jobs/${job.id}`)} variant="default">View</Link>
						<Link {...linkProps(`/operator/instances/${job.instance_slug}`)} variant="ghost">Open instance</Link>
						{#if job.status === 'error'}
							<Button variant="solid" onclick={() => void retry(job)} disabled={actingId === job.id}>Retry</Button>
						{/if}
						{#if actingId === job.id}
							<div class="op-provisioning__loading-inline">
								<Spinner size="sm" />
								<Text size="sm">Working…</Text>
							</div>
						{/if}
					</div>
				</Card>
			{/each}
		</div>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No response from provisioning endpoints.</Text>
		</Alert>
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
