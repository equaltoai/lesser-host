<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListOperatorProvisionJobsResponse, OperatorProvisionJobListItem } from 'src/lib/api/operatorProvisioning';
	import { listOperatorProvisionJobs, retryOperatorProvisionJob } from 'src/lib/api/operatorProvisioning';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Select, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let statusFilter = $state('queued');
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let data = $state<ListOperatorProvisionJobsResponse | null>(null);

	let actingId = $state<string | null>(null);
	let actionError = $state<string | null>(null);

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
		data = null;

		loading = true;
		try {
			data = await listOperatorProvisionJobs(token, { status: statusFilter === 'all' ? 'all' : statusFilter, limit: 100 });
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
						<Button variant="outline" onclick={() => navigate(`/operator/provisioning/jobs/${job.id}`)}>View</Button>
						<Button variant="ghost" onclick={() => navigate(`/operator/instances/${job.instance_slug}`)}>Open instance</Button>
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
