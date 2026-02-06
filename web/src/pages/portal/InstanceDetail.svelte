<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { DomainResponse, InstanceResponse, ProvisionJobResponse } from 'src/lib/api/portalInstances';
	import {
		portalGetInstance,
		portalGetProvisioning,
		portalListInstanceDomains,
		portalStartProvisioning,
	} from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { pollUntil } from 'src/lib/polling';
	import { navigate } from 'src/lib/router';
	import {
		Alert,
		Button,
		Card,
		DefinitionItem,
		DefinitionList,
		Heading,
		Spinner,
		Text,
		TextField,
	} from 'src/lib/ui';

	let { token, slug } = $props<{ token: string; slug: string }>();

	let instanceLoading = $state(false);
	let instanceError = $state<string | null>(null);
	let instance = $state<InstanceResponse | null>(null);

	let domainsLoading = $state(false);
	let domainsError = $state<string | null>(null);
	let domains = $state<DomainResponse[]>([]);

	let provisioningLoading = $state(false);
	let provisioningError = $state<string | null>(null);
	let provisioningJob = $state<ProvisionJobResponse | null>(null);
	let polling = $state(false);

	let provisionRegion = $state('');
	let provisionLesserVersion = $state('');

	let pollController: AbortController | null = null;

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function primaryDomain(): DomainResponse | null {
		for (const d of domains) {
			if (d.type === 'primary') return d;
		}
		return null;
	}

	function isProvisionTerminal(job: ProvisionJobResponse | null): boolean {
		if (!job) return true;
		return job.status === 'ok' || job.status === 'error';
	}

	function formatStep(step?: string): string {
		const raw = (step || '').trim();
		if (!raw) return '—';
		const parts = raw.split(/[_-]+/g).filter(Boolean);
		return parts.map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
	}

	function abortPolling() {
		if (pollController) {
			pollController.abort();
			pollController = null;
		}
		polling = false;
	}

	async function loadInstance() {
		instanceError = null;
		instance = null;

		instanceLoading = true;
		try {
			instance = await portalGetInstance(token, slug);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			instanceError = formatError(err);
		} finally {
			instanceLoading = false;
		}
	}

	async function loadDomains() {
		domainsError = null;
		domains = [];

		domainsLoading = true;
		try {
			const res = await portalListInstanceDomains(token, slug);
			domains = res.domains ?? [];
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			domainsError = formatError(err);
		} finally {
			domainsLoading = false;
		}
	}

	async function loadProvisioning() {
		provisioningError = null;
		provisioningJob = null;

		provisioningLoading = true;
		try {
			provisioningJob = await portalGetProvisioning(token, slug);
		} catch (err) {
			const maybe = err as Partial<ApiError>;
			if (maybe.status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			if (maybe.status === 404) {
				provisioningJob = null;
			} else {
				provisioningError = formatError(err);
			}
		} finally {
			provisioningLoading = false;
		}
	}

	async function pollProvisioning() {
		abortPolling();
		if (isProvisionTerminal(provisioningJob)) {
			return;
		}

		polling = true;
		const controller = new AbortController();
		pollController = controller;

		try {
			await pollUntil(
				() => portalGetProvisioning(token, slug),
				(job) => job.status === 'ok' || job.status === 'error',
				{
					signal: controller.signal,
					backoff: {
						initialDelayMs: 1000,
						maxDelayMs: 15_000,
						factor: 1.6,
					},
					onUpdate: (job) => {
						provisioningJob = job;
					},
				},
			);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			if ((err as Error).name !== 'AbortError') {
				provisioningError = formatError(err);
			}
		} finally {
			polling = false;
			if (pollController === controller) {
				pollController = null;
			}
		}
	}

	async function startProvisioning() {
		provisioningError = null;

		const region = provisionRegion.trim();
		const lesserVersion = provisionLesserVersion.trim();
		const input =
			region || lesserVersion
				? {
						region: region || undefined,
						lesser_version: lesserVersion || undefined,
					}
				: undefined;

		provisioningLoading = true;
		try {
			provisioningJob = await portalStartProvisioning(token, slug, input);
			void pollProvisioning();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			provisioningError = formatError(err);
		} finally {
			provisioningLoading = false;
		}
	}

	async function refreshAll() {
		abortPolling();
		await Promise.all([loadInstance(), loadDomains(), loadProvisioning()]);
		void pollProvisioning();
	}

	onMount(() => {
		void refreshAll();
	});

	$effect(() => {
		const normalized = slug.trim().toLowerCase();
		if (!normalized) return;
		if (normalized === slug) return;
		navigate(`/portal/instances/${normalized}`);
	});

	onDestroy(() => {
		abortPolling();
	});
</script>

<div class="instance-detail">
	<header class="instance-detail__header">
		<div class="instance-detail__title">
			<Heading level={2} size="xl">Instance</Heading>
			<Text color="secondary"><span class="instance-detail__mono">{slug}</span></Text>
		</div>
		<div class="instance-detail__actions">
			<Button variant="outline" onclick={() => void refreshAll()} disabled={instanceLoading || domainsLoading || provisioningLoading}>
				Refresh
			</Button>
			<Button variant="ghost" onclick={() => navigate('/portal')}>Back</Button>
		</div>
	</header>

	{#if instanceLoading}
		<div class="instance-detail__loading">
			<Spinner size="md" />
			<Text>Loading instance…</Text>
		</div>
	{:else if instanceError}
		<Alert variant="error" title="Failed to load instance">{instanceError}</Alert>
	{:else if instance}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Overview</Heading>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Slug" monospace>{instance.slug}</DefinitionItem>
				<DefinitionItem label="Status" monospace>{instance.status}</DefinitionItem>
				<DefinitionItem label="Provision status" monospace>{instance.provision_status || '—'}</DefinitionItem>
				<DefinitionItem label="Provision job id" monospace>{instance.provision_job_id || '—'}</DefinitionItem>
				<DefinitionItem label="Primary domain" monospace>{primaryDomain()?.domain || '—'}</DefinitionItem>
				<DefinitionItem label="Hosted account" monospace>{instance.hosted_account_id || '—'}</DefinitionItem>
				<DefinitionItem label="Hosted region" monospace>{instance.hosted_region || '—'}</DefinitionItem>
			</DefinitionList>

			{#if domainsError}
				<Alert variant="error" title="Failed to load domains">{domainsError}</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Provisioning</Heading>
			{/snippet}

			{#if provisioningLoading && !provisioningJob}
				<div class="instance-detail__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Loading provisioning…</Text>
				</div>
			{:else if provisioningError}
				<Alert variant="error" title="Provisioning error">{provisioningError}</Alert>
			{:else if provisioningJob}
				<DefinitionList>
					<DefinitionItem label="Status" monospace>{provisioningJob.status}</DefinitionItem>
					<DefinitionItem label="Step" monospace>{formatStep(provisioningJob.step)}</DefinitionItem>
					<DefinitionItem label="Updated" monospace>{provisioningJob.updated_at}</DefinitionItem>
					<DefinitionItem label="Run id" monospace>{provisioningJob.run_id || '—'}</DefinitionItem>
					<DefinitionItem label="Base domain" monospace>{provisioningJob.base_domain || '—'}</DefinitionItem>
					<DefinitionItem label="Account id" monospace>{provisioningJob.account_id || '—'}</DefinitionItem>
				</DefinitionList>

				{#if polling && (provisioningJob.status === 'queued' || provisioningJob.status === 'running')}
					<div class="instance-detail__loading-inline">
						<Spinner size="sm" />
						<Text size="sm">Updating…</Text>
					</div>
				{/if}

				{#if provisioningJob.status === 'error'}
					<Alert variant="error" title="Provisioning failed">
						<Text size="sm">
							Error: <span class="instance-detail__mono">{provisioningJob.error_code || 'unknown'}</span>
						</Text>
						{#if provisioningJob.error_message}
							<Text size="sm">{provisioningJob.error_message}</Text>
						{/if}
						{#if provisioningJob.note}
							<Text size="sm" color="secondary">{provisioningJob.note}</Text>
						{/if}
						<Text size="sm" color="secondary">
							Contact support with job id <span class="instance-detail__mono">{provisioningJob.id}</span>
							{#if provisioningJob.request_id}
								and request id <span class="instance-detail__mono">{provisioningJob.request_id}</span>.
							{/if}
						</Text>
					</Alert>
				{/if}
			{:else}
				<Alert variant="info" title="Not started">
					<Text size="sm">Start managed provisioning to allocate infrastructure for this instance.</Text>
				</Alert>

				<div class="instance-detail__form">
					<TextField label="Region (optional)" bind:value={provisionRegion} placeholder="us-east-1" />
					<TextField label="Lesser version (optional)" bind:value={provisionLesserVersion} placeholder="vX.Y.Z" />
				</div>

				<div class="instance-detail__row">
					<Button variant="solid" onclick={() => void startProvisioning()} disabled={provisioningLoading}>
						Start provisioning
					</Button>
				</div>
			{/if}
		</Card>
	{:else}
		<Alert variant="warning" title="No data">No instance response.</Alert>
	{/if}
</div>

<style>
	.instance-detail {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.instance-detail__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.instance-detail__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.instance-detail__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.instance-detail__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.instance-detail__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.instance-detail__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-4);
	}

	.instance-detail__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.instance-detail__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
