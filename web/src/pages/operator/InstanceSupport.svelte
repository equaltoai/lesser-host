<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	import { type ApiError, safeHref } from 'src/lib/api/http';
	import type { BudgetMonthResponse, ListBudgetsResponse } from 'src/lib/api/portalUsage';
	import { portalListBudgets } from 'src/lib/api/portalUsage';
	import type { DomainResponse, InstanceResponse, ProvisionJobResponse, UpdateJobResponse } from 'src/lib/api/portalInstances';
	import {
		portalCreateUpdateJob,
		portalGetInstance,
		portalGetProvisioning,
		portalListInstanceDomains,
		portalListUpdateJobs,
	} from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { pollUntil } from 'src/lib/polling';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Spinner, Text, TextField } from 'src/lib/ui';

	let { token, slug } = $props<{ token: string; slug?: string }>();

	let slugInput = $state('');

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let instance = $state<InstanceResponse | null>(null);
	let domains = $state<DomainResponse[]>([]);
	let budgets = $state<ListBudgetsResponse | null>(null);
	let provisioning = $state<ProvisionJobResponse | null>(null);

	let updatesLoading = $state(false);
	let updatesError = $state<string | null>(null);
	let updateJobs = $state<UpdateJobResponse[]>([]);
	let updatesPolling = $state(false);
	let updateCreating = $state(false);
	let updateLesserVersion = $state('');
	let updateLesserBodyVersion = $state('');

	let updatesPollController: AbortController | null = null;

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function normalizeSlug(input: string): string | null {
		const trimmed = input.trim().toLowerCase();
		if (!trimmed) return null;
		return trimmed;
	}

	function primaryDomain(): DomainResponse | null {
		for (const d of domains) {
			if (d.type === 'primary') return d;
		}
		return null;
	}

	function sortedBudgets(list: BudgetMonthResponse[]): BudgetMonthResponse[] {
		return [...list].sort((a, b) => b.month.localeCompare(a.month));
	}

	function formatStep(step?: string): string {
		const raw = (step || '').trim();
		if (!raw) return '—';
		const parts = raw.split(/[_-]+/g).filter(Boolean);
		return parts.map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join(' ');
	}

	function formatPhaseState(status?: string, err?: string): string {
		const raw = (status || '').trim();
		if (!raw) return '—';
		if (raw === 'failed' && err?.trim()) return `failed: ${err.trim()}`;
		return raw;
	}

	function managed(): boolean {
		return Boolean(instance?.hosted_account_id && instance?.hosted_region && instance?.hosted_base_domain);
	}

	function isUpdateTerminal(job: UpdateJobResponse | null): boolean {
		if (!job) return true;
		return job.status === 'ok' || job.status === 'error';
	}

	function jobKind(job?: UpdateJobResponse | null): string {
		if (!job) return 'lesser';
		const kind = job.kind?.trim();
		if (kind) return kind;
		if (job.mcp_only) return 'mcp';
		if (job.body_only) return 'lesser-body';
		return 'lesser';
	}

	function jobKindLabel(job?: UpdateJobResponse | null): string {
		switch (jobKind(job)) {
			case 'lesser-body':
				return 'lesser-body';
			case 'mcp':
				return 'MCP';
			default:
				return 'Lesser';
		}
	}

	function latestUpdateJobForKind(kind: string): UpdateJobResponse | null {
		return updateJobs.find((job) => jobKind(job) === kind) ?? null;
	}

	function activeUpdateJobs(): UpdateJobResponse[] {
		return updateJobs.filter((job) => !isUpdateTerminal(job));
	}

	function updateInProgress(): boolean {
		return activeUpdateJobs().length > 0;
	}

	function abortUpdatesPolling() {
		if (updatesPollController) {
			updatesPollController.abort();
			updatesPollController = null;
		}
		updatesPolling = false;
	}

	async function loadUpdates(targetSlug: string) {
		updatesError = null;
		updatesLoading = true;
		try {
			const res = await portalListUpdateJobs(token, targetSlug, 50);
			updateJobs = res.jobs ?? [];
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			updatesError = formatError(err);
		} finally {
			updatesLoading = false;
		}
	}

	async function pollUpdateJob(targetSlug: string, jobId: string) {
		abortUpdatesPolling();

		const current = updateJobs.find((j) => j.id === jobId) ?? null;
		if (current && isUpdateTerminal(current)) {
			return;
		}

		updatesPolling = true;
		const controller = new AbortController();
		updatesPollController = controller;

		try {
			await pollUntil(
				async () => {
					const res = await portalListUpdateJobs(token, targetSlug, 50);
					updateJobs = res.jobs ?? [];
					return (res.jobs ?? []).find((j) => j.id === jobId) ?? null;
				},
				(job) => Boolean(job && isUpdateTerminal(job)),
				{
					signal: controller.signal,
					backoff: {
						initialDelayMs: 1000,
						maxDelayMs: 15_000,
						factor: 1.6,
					},
					onUpdate: (job) => {
						if (!job) return;
						const idx = updateJobs.findIndex((j) => j.id === job.id);
						if (idx >= 0) {
							updateJobs = [job, ...updateJobs.slice(0, idx), ...updateJobs.slice(idx + 1)];
						}
					},
				},
			);
			void loadAll(targetSlug);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			if ((err as Error).name !== 'AbortError') {
				updatesError = formatError(err);
			}
		} finally {
			updatesPolling = false;
			if (updatesPollController === controller) {
				updatesPollController = null;
			}
		}
	}

	async function startUpdateJob(
		targetSlug: string,
		options?: {
			lesserVersion?: string;
			lesserBodyVersion?: string;
			rotateInstanceKey?: boolean;
			bodyOnly?: boolean;
			mcpOnly?: boolean;
		}
	) {
		updatesError = null;
		const version = (options?.lesserVersion || '').trim();
		const bodyVersion = (options?.lesserBodyVersion || '').trim();

		updateCreating = true;
		try {
			const input: {
				lesser_version?: string;
				lesser_body_version?: string;
				rotate_instance_key?: boolean;
				body_only?: boolean;
				mcp_only?: boolean;
			} = {};
			if (version) input.lesser_version = version;
			if (bodyVersion) input.lesser_body_version = bodyVersion;
			if (options?.rotateInstanceKey) input.rotate_instance_key = true;
			if (options?.bodyOnly) input.body_only = true;
			if (options?.mcpOnly) input.mcp_only = true;
			const job = await portalCreateUpdateJob(token, targetSlug, input);
			updateJobs = [job, ...updateJobs.filter((j) => j.id !== job.id)];
			void pollUpdateJob(targetSlug, job.id);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			updatesError = formatError(err);
		} finally {
			updateCreating = false;
		}
	}

	async function loadAll(targetSlug: string) {
		errorMessage = null;
		instance = null;
		domains = [];
		budgets = null;
		provisioning = null;
		updatesError = null;
		updateJobs = [];

		loading = true;
		try {
			const [inst, dom, bud, upd] = await Promise.all([
				portalGetInstance(token, targetSlug),
				portalListInstanceDomains(token, targetSlug),
				portalListBudgets(token, targetSlug),
				portalListUpdateJobs(token, targetSlug, 50),
			]);
			instance = inst;
			domains = dom.domains ?? [];
			budgets = bud;
			updateJobs = upd.jobs ?? [];

			try {
				provisioning = await portalGetProvisioning(token, targetSlug);
			} catch (err) {
				const maybe = err as Partial<ApiError>;
				if (maybe.status === 404) {
					provisioning = null;
				} else {
					throw err;
				}
			}
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

	function openSlug() {
		const normalized = normalizeSlug(slugInput);
		if (!normalized) return;
		navigate(`/operator/instances/${normalized}`);
	}

	onMount(() => {
		const normalized = slug ? normalizeSlug(slug) : null;
		if (normalized) {
			void loadAll(normalized);
		}
	});

	onDestroy(() => {
		abortUpdatesPolling();
	});

	$effect(() => {
		const normalized = slug ? normalizeSlug(slug) : null;
		if (!normalized) return;
		if (slugInput.trim().toLowerCase() !== normalized) {
			slugInput = normalized;
		}
	});
</script>

<div class="op-support">
	<header class="op-support__header">
		<div class="op-support__title">
			<Heading level={2} size="xl">Instance support</Heading>
			<Text color="secondary">Search by slug and view full state.</Text>
		</div>
		<div class="op-support__actions">
			<Button variant="outline" onclick={openSlug} disabled={loading}>Open</Button>
			<Button
				variant="outline"
				onclick={() => slug && void loadAll(slug)}
				disabled={loading || !slug}
			>
				Refresh
			</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Search</Heading>
		{/snippet}
		<div class="op-support__form">
			<TextField label="Slug" bind:value={slugInput} placeholder="your-instance-slug" />
		</div>
	</Card>

	{#if loading}
		<div class="op-support__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Instance support">{errorMessage}</Alert>
	{:else if !slug}
		<Alert variant="info" title="No instance selected">
			<Text size="sm">Enter a slug and click Open.</Text>
		</Alert>
	{:else if instance}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<div class="op-support__row">
					<Heading level={3} size="lg">Overview</Heading>
					<CopyButton size="sm" text={instance?.slug ?? ''} />
				</div>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Slug" monospace>{instance.slug}</DefinitionItem>
				<DefinitionItem label="Owner" monospace>{instance.owner || '—'}</DefinitionItem>
				<DefinitionItem label="Status" monospace>{instance.status}</DefinitionItem>
				<DefinitionItem label="Primary domain" monospace>{primaryDomain()?.domain || '—'}</DefinitionItem>
				<DefinitionItem label="Provision status" monospace>{instance.provision_status || '—'}</DefinitionItem>
				<DefinitionItem label="Provision job id" monospace>{instance.provision_job_id || '—'}</DefinitionItem>
				<DefinitionItem label="Current Lesser version" monospace>{instance.lesser_version || '—'}</DefinitionItem>
				<DefinitionItem label="Current lesser-body version" monospace>{instance.lesser_body_version || '—'}</DefinitionItem>
				<DefinitionItem label="Body provisioned" monospace>{instance.body_provisioned_at || '—'}</DefinitionItem>
				<DefinitionItem label="MCP wired" monospace>{instance.mcp_wired_at || '—'}</DefinitionItem>
				<DefinitionItem label="Lesser update state" monospace>{instance.lesser_update_status || '—'}</DefinitionItem>
				<DefinitionItem label="Lesser update job" monospace>{instance.lesser_update_job_id || '—'}</DefinitionItem>
				<DefinitionItem label="Lesser updated" monospace>{instance.lesser_update_at || '—'}</DefinitionItem>
				<DefinitionItem label="lesser-body update state" monospace>{instance.lesser_body_update_status || '—'}</DefinitionItem>
				<DefinitionItem label="lesser-body update job" monospace>{instance.lesser_body_update_job_id || '—'}</DefinitionItem>
				<DefinitionItem label="lesser-body updated" monospace>{instance.lesser_body_update_at || '—'}</DefinitionItem>
				<DefinitionItem label="MCP update state" monospace>{instance.mcp_update_status || '—'}</DefinitionItem>
				<DefinitionItem label="MCP update job" monospace>{instance.mcp_update_job_id || '—'}</DefinitionItem>
				<DefinitionItem label="MCP updated" monospace>{instance.mcp_update_at || '—'}</DefinitionItem>
				<DefinitionItem label="Instance updated" monospace>{instance.updated_at || '—'}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Config</Heading>
			{/snippet}
			<DefinitionList>
				<DefinitionItem label="Hosted previews" monospace>{String(instance.hosted_previews_enabled)}</DefinitionItem>
				<DefinitionItem label="Link safety" monospace>{String(instance.link_safety_enabled)}</DefinitionItem>
				<DefinitionItem label="Renders" monospace>{String(instance.renders_enabled)}</DefinitionItem>
				<DefinitionItem label="Render policy" monospace>{instance.render_policy}</DefinitionItem>
				<DefinitionItem label="Overage policy" monospace>{instance.overage_policy}</DefinitionItem>
				<DefinitionItem label="Moderation" monospace>{String(instance.moderation_enabled)}</DefinitionItem>
				<DefinitionItem label="Moderation trigger" monospace>{instance.moderation_trigger}</DefinitionItem>
				<DefinitionItem label="Moderation virality min" monospace>{String(instance.moderation_virality_min)}</DefinitionItem>
				<DefinitionItem label="AI" monospace>{String(instance.ai_enabled)}</DefinitionItem>
				<DefinitionItem label="AI model set" monospace>{instance.ai_model_set}</DefinitionItem>
				<DefinitionItem label="AI batching mode" monospace>{instance.ai_batching_mode}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Domains</Heading>
			{/snippet}
			{#if domains.length === 0}
				<Alert variant="info" title="No domains">
					<Text size="sm">No domains returned.</Text>
				</Alert>
			{:else}
				<div class="op-support__list">
					{#each domains as d (d.domain)}
						<div class="op-support__list-row">
							<Text size="sm">
								<span class="op-support__mono">{d.domain}</span> · {d.type} · {d.status}
							</Text>
							<CopyButton size="sm" text={d.domain} />
						</div>
					{/each}
				</div>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Budgets</Heading>
			{/snippet}
			{#if budgets && budgets.budgets.length === 0}
				<Alert variant="info" title="No budgets">
					<Text size="sm">No budget months stored.</Text>
				</Alert>
			{:else if budgets}
				<div class="op-support__list">
					{#each sortedBudgets(budgets.budgets).slice(0, 8) as b (b.month)}
						<div class="op-support__list-row">
							<Text size="sm">
								<span class="op-support__mono">{b.month}</span> · included
								<span class="op-support__mono">{String(b.included_credits)}</span> · used
								<span class="op-support__mono">{String(b.used_credits)}</span>
							</Text>
							<CopyButton size="sm" text={b.month} />
						</div>
					{/each}
				</div>
			{:else}
				<Alert variant="warning" title="No data">
					<Text size="sm">No response from budgets endpoint.</Text>
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Provisioning</Heading>
			{/snippet}
			{#if provisioning}
				<DefinitionList>
					<DefinitionItem label="Status" monospace>{provisioning.status}</DefinitionItem>
					<DefinitionItem label="Step" monospace>{provisioning.step || '—'}</DefinitionItem>
					<DefinitionItem label="Job id" monospace>{provisioning.id}</DefinitionItem>
					<DefinitionItem label="Request id" monospace>{provisioning.request_id || '—'}</DefinitionItem>
					<DefinitionItem label="Run id" monospace>{provisioning.run_id || '—'}</DefinitionItem>
					<DefinitionItem label="Updated" monospace>{provisioning.updated_at}</DefinitionItem>
				</DefinitionList>
				{#if provisioning.status === 'error'}
					<Alert variant="error" title="Provisioning error">
						<Text size="sm">
							<span class="op-support__mono">{provisioning.error_code || 'unknown'}</span>
						</Text>
						{#if provisioning.error_message}
							<Text size="sm">{provisioning.error_message}</Text>
						{/if}
						{#if provisioning.note}
							<Text size="sm" color="secondary">{provisioning.note}</Text>
						{/if}
					</Alert>
				{/if}
			{:else}
				<Alert variant="info" title="Not started">
					<Text size="sm">No provisioning job for this instance.</Text>
				</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Updates</Heading>
			{/snippet}

			{#if updatesLoading && updateJobs.length === 0}
				<div class="op-support__loading">
					<Spinner size="md" />
					<Text>Loading…</Text>
				</div>
			{:else if updatesError}
				<Alert variant="error" title="Update jobs">{updatesError}</Alert>
			{/if}

			{#if !managed()}
				<Alert variant="info" title="Managed updates unavailable">
					<Text size="sm">Update jobs and managed key rotation are only available for managed provisioned instances.</Text>
				</Alert>
			{/if}

			<div class="op-support__row">
				<Button
					variant="solid"
					onclick={() => slug && void startUpdateJob(slug)}
					disabled={!slug || updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed()}
				>
					Apply configuration
				</Button>
				<Text size="sm" color="secondary">
					Re-runs <span class="op-support__mono">lesser up</span> only to apply stored trust/translation/tips/AI config.
				</Text>
			</div>

			<div class="op-support__row">
				<Button
					variant="outline"
					onclick={() => slug && void startUpdateJob(slug, { rotateInstanceKey: true })}
					disabled={!slug || updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed()}
				>
					Rotate instance key
				</Button>
				<Text size="sm" color="secondary">
					Writes a new key to the managed secret and re-runs <span class="op-support__mono">lesser up</span>. Old keys stay valid until revoked.
				</Text>
			</div>

			<div class="op-support__form">
				<TextField label="Update Lesser version" bind:value={updateLesserVersion} placeholder="vX.Y.Z or latest" />
			</div>

			<div class="op-support__actions">
				<Button
					variant="outline"
					onclick={() => slug && void startUpdateJob(slug, { lesserVersion: updateLesserVersion })}
					disabled={
						!slug ||
						updateCreating ||
						updatesPolling ||
						updatesLoading ||
						updateInProgress() ||
						!updateLesserVersion.trim() ||
						!managed()
					}
				>
					Start version update
				</Button>
				<Text size="sm" color="secondary">
					Updates <span class="op-support__mono">lesser</span> only at the requested Lesser release.
				</Text>
			</div>

			<div class="op-support__form">
				<TextField
					label="Update Lesser Body version"
					bind:value={updateLesserBodyVersion}
					placeholder="vX.Y.Z, latest, or blank for configured default"
				/>
			</div>

			<div class="op-support__actions">
				<Button
					variant="outline"
					onclick={() => slug && void startUpdateJob(slug, { lesserBodyVersion: updateLesserBodyVersion, bodyOnly: true })}
					disabled={!slug || updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed()}
				>
					Update lesser-body only
				</Button>
				<Text size="sm" color="secondary">
					Updates <span class="op-support__mono">lesser-body</span> only. MCP wiring is separate.
				</Text>
				<Button
					variant="outline"
					onclick={() => slug && void startUpdateJob(slug, { mcpOnly: true })}
					disabled={!slug || updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed()}
				>
					Update MCP only
				</Button>
				<Text size="sm" color="secondary">
					Re-runs only the <span class="op-support__mono">/mcp</span> wiring step against the current lesser and lesser-body deployment.
				</Text>
				<Button variant="outline" onclick={() => slug && void loadUpdates(slug)} disabled={!slug || updatesLoading}>
					Refresh
				</Button>
			</div>

			{#if updatesPolling && updateInProgress()}
				<div class="op-support__loading">
					<Spinner size="md" />
					<Text>Updating…</Text>
				</div>
			{/if}

			{@const activeJobs = activeUpdateJobs()}
			{#if activeJobs.length > 0}
				<Alert variant="info" title="Active updates">
					{#each activeJobs as job (job.id)}
						<Text size="sm">
							<span class="op-support__mono">{jobKindLabel(job)}</span> ·
							<span class="op-support__mono">{job.id}</span> ·
							{job.note || formatStep(job.step)}
						</Text>
					{/each}
				</Alert>
			{/if}

			{@const lesserJob = latestUpdateJobForKind('lesser')}
			{@const bodyJob = latestUpdateJobForKind('lesser-body')}
			{@const mcpJob = latestUpdateJobForKind('mcp')}
			{#if lesserJob || bodyJob || mcpJob}
				<div class="op-support__update-sections">
					<div class="op-support__update-section">
						<Heading level={4} size="lg">Latest Lesser update</Heading>
						{#if lesserJob}
							<DefinitionList>
								<DefinitionItem label="Job id" monospace>{lesserJob.id}</DefinitionItem>
								<DefinitionItem label="Status" monospace>{lesserJob.status}</DefinitionItem>
								<DefinitionItem label="Step" monospace>{formatStep(lesserJob.step)}</DefinitionItem>
								<DefinitionItem label="Updated" monospace>{lesserJob.updated_at}</DefinitionItem>
								<DefinitionItem label="Lesser version" monospace>{lesserJob.lesser_version || '—'}</DefinitionItem>
								<DefinitionItem label="Active phase" monospace>{lesserJob.active_phase || '—'}</DefinitionItem>
								<DefinitionItem label="Failed phase" monospace>{lesserJob.failed_phase || '—'}</DefinitionItem>
								<DefinitionItem label="Run id" monospace>{lesserJob.run_id || '—'}</DefinitionItem>
								<DefinitionItem label="Run url" monospace>
									{@const lesserRunUrl = safeHref(lesserJob.run_url)}
									{#if lesserRunUrl}
										<a href={lesserRunUrl} target="_blank" rel="noopener noreferrer">Open logs</a>
									{:else}
										—
									{/if}
								</DefinitionItem>
								<DefinitionItem label="Deploy phase" monospace>{formatPhaseState(lesserJob.deploy_status, lesserJob.deploy_error)}</DefinitionItem>
								<DefinitionItem label="Deploy logs" monospace>
									{@const lesserDeployRunUrl = safeHref(lesserJob.deploy_run_url)}
									{#if lesserDeployRunUrl}
										<a href={lesserDeployRunUrl} target="_blank" rel="noopener noreferrer">Open deploy logs</a>
									{:else}
										—
									{/if}
								</DefinitionItem>
							</DefinitionList>
							{#if lesserJob.status === 'error'}
								<Alert variant="error" title="Lesser update failed">
									<Text size="sm">Error: <span class="op-support__mono">{lesserJob.error_code || 'unknown'}</span></Text>
									{#if lesserJob.error_message}
										<Text size="sm">{lesserJob.error_message}</Text>
									{/if}
									{#if lesserJob.note}
										<Text size="sm" color="secondary">{lesserJob.note}</Text>
									{/if}
								</Alert>
							{/if}
						{:else}
							<Alert variant="info" title="No Lesser updates">
								<Text size="sm">No Lesser update jobs have run yet.</Text>
							</Alert>
						{/if}
					</div>

					<div class="op-support__update-section">
						<Heading level={4} size="lg">Latest lesser-body update</Heading>
						{#if bodyJob}
							<DefinitionList>
								<DefinitionItem label="Job id" monospace>{bodyJob.id}</DefinitionItem>
								<DefinitionItem label="Status" monospace>{bodyJob.status}</DefinitionItem>
								<DefinitionItem label="Step" monospace>{formatStep(bodyJob.step)}</DefinitionItem>
								<DefinitionItem label="Updated" monospace>{bodyJob.updated_at}</DefinitionItem>
								<DefinitionItem label="Body version" monospace>{bodyJob.lesser_body_version || '—'}</DefinitionItem>
								<DefinitionItem label="Active phase" monospace>{bodyJob.active_phase || '—'}</DefinitionItem>
								<DefinitionItem label="Failed phase" monospace>{bodyJob.failed_phase || '—'}</DefinitionItem>
								<DefinitionItem label="Run id" monospace>{bodyJob.run_id || '—'}</DefinitionItem>
								<DefinitionItem label="Run url" monospace>
									{@const bodyRunUrl = safeHref(bodyJob.run_url)}
									{#if bodyRunUrl}
										<a href={bodyRunUrl} target="_blank" rel="noopener noreferrer">Open logs</a>
									{:else}
										—
									{/if}
								</DefinitionItem>
								<DefinitionItem label="Body phase" monospace>{formatPhaseState(bodyJob.body_status, bodyJob.body_error)}</DefinitionItem>
								<DefinitionItem label="Body logs" monospace>
									{@const bodyPhaseRunUrl = safeHref(bodyJob.body_run_url)}
									{#if bodyPhaseRunUrl}
										<a href={bodyPhaseRunUrl} target="_blank" rel="noopener noreferrer">Open body logs</a>
									{:else}
										—
									{/if}
								</DefinitionItem>
							</DefinitionList>
							{#if bodyJob.status === 'error'}
								<Alert variant="error" title="lesser-body update failed">
									<Text size="sm">Error: <span class="op-support__mono">{bodyJob.error_code || 'unknown'}</span></Text>
									{#if bodyJob.error_message}
										<Text size="sm">{bodyJob.error_message}</Text>
									{/if}
									{#if bodyJob.note}
										<Text size="sm" color="secondary">{bodyJob.note}</Text>
									{/if}
								</Alert>
							{/if}
						{:else}
							<Alert variant="info" title="No lesser-body updates">
								<Text size="sm">No lesser-body update jobs have run yet.</Text>
							</Alert>
						{/if}
					</div>

					<div class="op-support__update-section">
						<Heading level={4} size="lg">Latest MCP update</Heading>
						{#if mcpJob}
							<DefinitionList>
								<DefinitionItem label="Job id" monospace>{mcpJob.id}</DefinitionItem>
								<DefinitionItem label="Status" monospace>{mcpJob.status}</DefinitionItem>
								<DefinitionItem label="Step" monospace>{formatStep(mcpJob.step)}</DefinitionItem>
								<DefinitionItem label="Updated" monospace>{mcpJob.updated_at}</DefinitionItem>
								<DefinitionItem label="Body version" monospace>{mcpJob.lesser_body_version || '—'}</DefinitionItem>
								<DefinitionItem label="Active phase" monospace>{mcpJob.active_phase || '—'}</DefinitionItem>
								<DefinitionItem label="Failed phase" monospace>{mcpJob.failed_phase || '—'}</DefinitionItem>
								<DefinitionItem label="Run id" monospace>{mcpJob.run_id || '—'}</DefinitionItem>
								<DefinitionItem label="Run url" monospace>
									{@const mcpRunUrl = safeHref(mcpJob.run_url)}
									{#if mcpRunUrl}
										<a href={mcpRunUrl} target="_blank" rel="noopener noreferrer">Open logs</a>
									{:else}
										—
									{/if}
								</DefinitionItem>
								<DefinitionItem label="MCP phase" monospace>{formatPhaseState(mcpJob.mcp_status, mcpJob.mcp_error)}</DefinitionItem>
								<DefinitionItem label="MCP logs" monospace>
									{@const mcpPhaseRunUrl = safeHref(mcpJob.mcp_run_url)}
									{#if mcpPhaseRunUrl}
										<a href={mcpPhaseRunUrl} target="_blank" rel="noopener noreferrer">Open MCP logs</a>
									{:else}
										—
									{/if}
								</DefinitionItem>
							</DefinitionList>
							{#if mcpJob.status === 'error'}
								<Alert variant="error" title="MCP update failed">
									<Text size="sm">Error: <span class="op-support__mono">{mcpJob.error_code || 'unknown'}</span></Text>
									{#if mcpJob.error_message}
										<Text size="sm">{mcpJob.error_message}</Text>
									{/if}
									{#if mcpJob.note}
										<Text size="sm" color="secondary">{mcpJob.note}</Text>
									{/if}
								</Alert>
							{/if}
						{:else}
							<Alert variant="info" title="No MCP updates">
								<Text size="sm">No MCP update jobs have run yet.</Text>
							</Alert>
						{/if}
					</div>
				</div>
			{:else}
				<Alert variant="info" title="No update jobs">
					<Text size="sm">No updates have been run yet.</Text>
				</Alert>
			{/if}

			{#if updateJobs.length > 1}
				<div class="op-support__list">
					{#each updateJobs.slice(0, 10) as j (j.id)}
						<div class="op-support__list-row">
							<Text size="sm">
								<span class="op-support__mono">{jobKindLabel(j)}</span> ·
								<span class="op-support__mono">{j.id}</span> — {j.status} ({formatStep(j.step)})
							</Text>
							<CopyButton size="sm" text={j.id} />
						</div>
					{/each}
				</div>
			{/if}
		</Card>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No instance response.</Text>
		</Alert>
	{/if}
</div>

<style>
	.op-support {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-support__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-support__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-support__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-support__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-support__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-support__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.op-support__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-support__list-row {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		padding: var(--gr-spacing-scale-2);
		border: 1px solid var(--gr-color-border-subtle);
		border-radius: var(--gr-radius-md);
		background: var(--gr-color-surface);
	}

	.op-support__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}

	.op-support__update-sections {
		display: grid;
		gap: var(--gr-spacing-scale-4);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-support__update-section {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		padding: var(--gr-spacing-scale-4);
		border: 1px solid var(--gr-color-border-subtle, #d9d9d9);
		border-radius: var(--gr-radius-md, 12px);
	}
</style>
