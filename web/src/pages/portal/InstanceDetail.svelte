<script lang="ts">
	import { onDestroy, onMount } from 'svelte';

	import { type ApiError, safeHref } from 'src/lib/api/http';
	import type { DomainResponse, InstanceResponse, ProvisionJobResponse, UpdateJobResponse } from 'src/lib/api/portalInstances';
	import {
		portalCreateUpdateJob,
		portalGetInstance,
		portalGetProvisioning,
		portalListUpdateJobs,
		portalListInstanceDomains,
		portalProvisionConsentChallenge,
		portalStartProvisioning,
	} from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { pollUntil } from 'src/lib/polling';
	import { navigate } from 'src/lib/router';
	import { getEthereumProvider, personalSign, requestAccounts } from 'src/lib/wallet/ethereum';
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

	let updatesLoading = $state(false);
	let updatesError = $state<string | null>(null);
	let updateJobs = $state<UpdateJobResponse[]>([]);
	let updatesPolling = $state(false);
	let updateCreating = $state(false);
	let updateLesserVersion = $state('');
	let updateLesserBodyVersion = $state('');

	let provisionRegion = $state('');
	let provisionLesserVersion = $state('');
	let provisionAdminUsername = $state('');

	let pollController: AbortController | null = null;
	let updatesPollController: AbortController | null = null;

	const slugRE = /^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$/;

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

	function formatPhaseState(status?: string, err?: string): string {
		const raw = (status || '').trim();
		if (!raw) return '—';
		if (raw === 'failed' && err?.trim()) return `failed: ${err.trim()}`;
		return raw;
	}

	function abortPolling() {
		if (pollController) {
			pollController.abort();
			pollController = null;
		}
		polling = false;
	}

	function abortUpdatesPolling() {
		if (updatesPollController) {
			updatesPollController.abort();
			updatesPollController = null;
		}
		updatesPolling = false;
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

	async function loadUpdates() {
		updatesError = null;
		updateJobs = [];

		updatesLoading = true;
		try {
			const res = await portalListUpdateJobs(token, slug, 50);
			updateJobs = res.jobs ?? [];
		} catch (err) {
			const maybe = err as Partial<ApiError>;
			if (maybe.status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			updatesError = formatError(err);
		} finally {
			updatesLoading = false;
		}
	}

	function latestUpdateJob(): UpdateJobResponse | null {
		return updateJobs.length > 0 ? updateJobs[0] : null;
	}

	function isUpdateTerminal(job: UpdateJobResponse | null): boolean {
		if (!job) return true;
		return job.status === 'ok' || job.status === 'error';
	}

	async function pollUpdateJob(jobId: string) {
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
					const res = await portalListUpdateJobs(token, slug, 50);
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
							updateJobs = [
								job,
								...updateJobs.slice(0, idx),
								...updateJobs.slice(idx + 1),
							];
						}
					},
				},
			);
			void loadInstance();
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

	async function startUpdateJob(options?: {
		lesserVersion?: string;
		lesserBodyVersion?: string;
		rotateInstanceKey?: boolean;
		bodyOnly?: boolean;
		mcpOnly?: boolean;
	}) {
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

			const job = await portalCreateUpdateJob(token, slug, input);
			updateJobs = [job, ...updateJobs.filter((j) => j.id !== job.id)];
			void pollUpdateJob(job.id);
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

	function updateInProgress(): boolean {
		const j = latestUpdateJob();
		if (!j) return false;
		return j.status === 'queued' || j.status === 'running';
	}

	function trustHealthLabel(): string {
		const job = latestUpdateJob();
		if (!job) return 'unverified';
		if (job.verify_trust_ok === true) return 'ok';
		if (job.verify_trust_ok === false) return `fail${job.verify_trust_err ? `: ${job.verify_trust_err}` : ''}`;
		return 'unverified';
	}

	function translationHealthLabel(): string {
		const job = latestUpdateJob();
		if (!job) return 'unverified';
		if (job.verify_translation_ok === true) return 'ok';
		if (job.verify_translation_ok === false) {
			return `fail${job.verify_translation_err ? `: ${job.verify_translation_err}` : ''}`;
		}
		return 'unverified';
	}

	function tipsHealthLabel(): string {
		const job = latestUpdateJob();
		if (!job) return 'unverified';
		if (job.verify_tips_ok === true) return 'ok';
		if (job.verify_tips_ok === false) return `fail${job.verify_tips_err ? `: ${job.verify_tips_err}` : ''}`;
		return 'unverified';
	}

	function aiHealthLabel(): string {
		const job = latestUpdateJob();
		if (!job) return 'unverified';
		if (job.verify_ai_ok === true) return 'ok';
		if (job.verify_ai_ok === false) return `fail${job.verify_ai_err ? `: ${job.verify_ai_err}` : ''}`;
		return 'unverified';
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
		const adminUsernameRaw = provisionAdminUsername.trim().toLowerCase();
		const adminUsername = adminUsernameRaw || slug.trim().toLowerCase();
		if (!slugRE.test(adminUsername)) {
			provisioningError =
				'Admin username must be 1–63 chars, lowercase letters/numbers, and hyphens (cannot start/end with hyphen).';
			return;
		}

		const provider = getEthereumProvider();
		if (!provider) {
			provisioningError = 'No wallet detected. Install or enable a wallet extension to sign the consent message.';
			return;
		}

		provisioningLoading = true;
		try {
			const challenge = await portalProvisionConsentChallenge(token, slug, adminUsername);

			const expected = (challenge.wallet?.address || '').trim();
			if (!expected) {
				provisioningError = 'Consent challenge did not include a wallet address.';
				return;
			}

			const accounts = await requestAccounts(provider);
			const normalized = accounts.map((a) => a.toLowerCase());
			if (!normalized.includes(expected.toLowerCase())) {
				provisioningError = `Connected wallet does not match your portal wallet (${expected}).`;
				return;
			}

			const signature = await personalSign(provider, challenge.wallet.message, expected);

			provisioningJob = await portalStartProvisioning(token, slug, {
				region: region || undefined,
				lesser_version: lesserVersion || undefined,
				admin_username: challenge.admin_username,
				consent_challenge_id: challenge.wallet.id,
				consent_message: challenge.wallet.message,
				consent_signature: signature,
			});
			void pollProvisioning();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			const maybe = err as Partial<ApiError>;
			if (maybe.status === 403 && typeof maybe.message === 'string' && maybe.message.includes('approval')) {
				provisioningError =
					'Your account is pending approval. Provisioning is blocked until an admin approves your user.';
				return;
			}
			if (typeof maybe.message === 'string' && maybe.message.includes('reserved')) {
				provisioningError =
					'This wallet address is reserved and cannot be used for managed instance provisioning.';
				return;
			}
			provisioningError = formatError(err);
		} finally {
			provisioningLoading = false;
		}
	}

	async function refreshAll() {
		abortPolling();
		abortUpdatesPolling();
		await Promise.all([loadInstance(), loadDomains(), loadProvisioning(), loadUpdates()]);
		void pollProvisioning();
		const latest = latestUpdateJob();
		if (latest && !isUpdateTerminal(latest)) {
			void pollUpdateJob(latest.id);
		}
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

	$effect(() => {
		const normalized = slug.trim().toLowerCase();
		if (!normalized) return;
		if (!provisionAdminUsername.trim()) {
			provisionAdminUsername = normalized;
		}
	});

	onDestroy(() => {
		abortPolling();
		abortUpdatesPolling();
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
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/config`)}>Config</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/budgets`)}>Budgets</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/usage`)}>Usage</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/domains`)}>Domains</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/keys`)}>Keys</Button>
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
				<DefinitionItem label="Current Lesser version" monospace>{instance.lesser_version || '—'}</DefinitionItem>
				<DefinitionItem label="Current lesser-body version" monospace>{instance.lesser_body_version || '—'}</DefinitionItem>
				<DefinitionItem label="Body provisioned" monospace>{instance.body_provisioned_at || '—'}</DefinitionItem>
				<DefinitionItem label="MCP wired" monospace>{instance.mcp_wired_at || '—'}</DefinitionItem>
			</DefinitionList>

			{#if domainsError}
				<Alert variant="error" title="Failed to load domains">{domainsError}</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Integration health</Heading>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Lesser host base url" monospace>{instance.lesser_host_base_url || '—'}</DefinitionItem>
				<DefinitionItem label="Attestations url" monospace>{instance.lesser_host_attestations_url || '—'}</DefinitionItem>
				<DefinitionItem label="Verify trust" monospace>{trustHealthLabel()}</DefinitionItem>
				<DefinitionItem label="Verify translation" monospace>{translationHealthLabel()}</DefinitionItem>
				<DefinitionItem label="Verify tips" monospace>{tipsHealthLabel()}</DefinitionItem>
				<DefinitionItem label="Verify AI" monospace>{aiHealthLabel()}</DefinitionItem>
			</DefinitionList>

			{#if trustHealthLabel() !== 'ok'}
				<Alert variant="info" title="Trust not verified yet">
					<Text size="sm">Run an update to apply config and verify trust wiring.</Text>
				</Alert>
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
					<DefinitionItem label="Admin username" monospace>{provisioningJob.admin_username || '—'}</DefinitionItem>
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

					<Alert variant="info" title="Restart provisioning">
						<Text size="sm">Update the inputs (optional) and retry provisioning.</Text>
					</Alert>

					<div class="instance-detail__form">
						<TextField label="Region (optional)" bind:value={provisionRegion} placeholder="us-east-1" />
						<TextField label="Lesser version (optional)" bind:value={provisionLesserVersion} placeholder="vX.Y.Z" />
					</div>

					<div class="instance-detail__row">
						<Button variant="solid" onclick={() => void startProvisioning()} disabled={provisioningLoading}>
							Restart provisioning
						</Button>
					</div>
				{/if}

				{#if provisioningJob.status === 'ok'}
					<Alert variant="success" title="Provisioning complete">
						<Text size="sm">Next: open your instance and complete passkey-only setup.</Text>
						{#if provisioningJob.base_domain}
							<div class="instance-detail__row">
								<Button
									variant="outline"
									onclick={() => {
										const baseDomain = provisioningJob?.base_domain;
										if (!baseDomain) return;
										window.open(`https://${baseDomain}`, '_blank', 'noopener,noreferrer');
									}}
								>
									Open instance
								</Button>
							</div>
						{/if}
					</Alert>
				{/if}
			{:else}
				<Alert variant="info" title="Not started">
					<Text size="sm">Start managed provisioning to allocate infrastructure for this instance.</Text>
				</Alert>

				<div class="instance-detail__form">
					<TextField label="Admin username" bind:value={provisionAdminUsername} placeholder={slug} />
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

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Updates</Heading>
			{/snippet}

			{#if updatesLoading && updateJobs.length === 0}
				<div class="instance-detail__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Loading updates…</Text>
				</div>
			{:else if updatesError}
				<Alert variant="error" title="Update jobs">{updatesError}</Alert>
			{/if}

			{@const managed = Boolean(instance.hosted_account_id && instance.hosted_region && instance.hosted_base_domain)}

			{#if !managed}
				<Alert variant="info" title="Managed updates unavailable">
					<Text size="sm">Update jobs and managed key rotation are only available for managed provisioned instances.</Text>
				</Alert>
			{/if}

			<div class="instance-detail__row">
				<Button
					variant="solid"
					onclick={() => void startUpdateJob()}
					disabled={updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed}
				>
					Apply configuration
				</Button>
				<Text size="sm" color="secondary">
					Re-runs <span class="instance-detail__mono">lesser up</span> only to apply stored trust/translation/tips/AI config.
				</Text>
			</div>

			<div class="instance-detail__row">
				<Button
					variant="outline"
					onclick={() => void startUpdateJob({ rotateInstanceKey: true })}
					disabled={updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed}
				>
					Rotate instance key
				</Button>
				<Text size="sm" color="secondary">
					Writes a new key to the managed secret and re-runs <span class="instance-detail__mono">lesser up</span>. Old keys stay
					valid until revoked.
				</Text>
			</div>

			<div class="instance-detail__form">
				<TextField label="Update Lesser version" bind:value={updateLesserVersion} placeholder="vX.Y.Z or latest" />
			</div>
			<div class="instance-detail__row">
				<Button
					variant="outline"
					onclick={() => void startUpdateJob({ lesserVersion: updateLesserVersion })}
					disabled={
						updateCreating ||
						updatesPolling ||
						updatesLoading ||
						updateInProgress() ||
						!updateLesserVersion.trim() ||
						!managed
					}
				>
					Start version update
				</Button>
				<Text size="sm" color="secondary">
					Updates <span class="instance-detail__mono">lesser</span> only at the requested Lesser release.
				</Text>
			</div>

			<div class="instance-detail__form">
				<TextField
					label="Update Lesser Body version"
					bind:value={updateLesserBodyVersion}
					placeholder="vX.Y.Z, latest, or blank for configured default"
				/>
			</div>
			<div class="instance-detail__row">
				<Button
					variant="outline"
					onclick={() => void startUpdateJob({ lesserBodyVersion: updateLesserBodyVersion, bodyOnly: true })}
					disabled={updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed}
				>
					Update lesser-body only
				</Button>
				<Text size="sm" color="secondary">
					Updates <span class="instance-detail__mono">lesser-body</span> only. MCP wiring is separate.
				</Text>
			</div>

			<div class="instance-detail__row">
				<Button
					variant="outline"
					onclick={() => void startUpdateJob({ mcpOnly: true })}
					disabled={updateCreating || updatesPolling || updatesLoading || updateInProgress() || !managed}
				>
					Update MCP only
				</Button>
				<Text size="sm" color="secondary">
					Re-runs only the <span class="instance-detail__mono">/mcp</span> wiring step against the currently deployed instance and lesser-body runtime.
				</Text>
			</div>

			{#if updatesPolling && updateInProgress()}
				<div class="instance-detail__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Updating…</Text>
				</div>
			{/if}

			{#if latestUpdateJob()}
				{@const job = latestUpdateJob()}
				<DefinitionList>
					<DefinitionItem label="Status" monospace>{job?.status}</DefinitionItem>
					<DefinitionItem label="Step" monospace>{formatStep(job?.step)}</DefinitionItem>
					<DefinitionItem label="Updated" monospace>{job?.updated_at}</DefinitionItem>
					<DefinitionItem label="Lesser version" monospace>{job?.lesser_version || '—'}</DefinitionItem>
					<DefinitionItem label="Lesser Body version" monospace>{job?.lesser_body_version || '—'}</DefinitionItem>
					<DefinitionItem label="Body-only" monospace>{job?.body_only ? 'yes' : 'no'}</DefinitionItem>
					<DefinitionItem label="MCP-only" monospace>{job?.mcp_only ? 'yes' : 'no'}</DefinitionItem>
					<DefinitionItem label="Active phase" monospace>{job?.active_phase || '—'}</DefinitionItem>
					<DefinitionItem label="Failed phase" monospace>{job?.failed_phase || '—'}</DefinitionItem>
						<DefinitionItem label="Run id" monospace>{job?.run_id || '—'}</DefinitionItem>
						<DefinitionItem label="Run url" monospace>
							{@const runUrl = safeHref(job?.run_url)}
							{#if runUrl}
								<a href={runUrl} target="_blank" rel="noopener noreferrer">Open logs</a>
							{:else}
								—
							{/if}
						</DefinitionItem>
					<DefinitionItem label="Deploy phase" monospace>{formatPhaseState(job?.deploy_status, job?.deploy_error)}</DefinitionItem>
					<DefinitionItem label="Deploy logs" monospace>
						{@const deployRunUrl = safeHref(job?.deploy_run_url)}
						{#if deployRunUrl}
							<a href={deployRunUrl} target="_blank" rel="noopener noreferrer">Open deploy logs</a>
						{:else}
							—
						{/if}
					</DefinitionItem>
					<DefinitionItem label="Body phase" monospace>{formatPhaseState(job?.body_status, job?.body_error)}</DefinitionItem>
					<DefinitionItem label="Body logs" monospace>
						{@const bodyRunUrl = safeHref(job?.body_run_url)}
						{#if bodyRunUrl}
							<a href={bodyRunUrl} target="_blank" rel="noopener noreferrer">Open body logs</a>
						{:else}
							—
						{/if}
					</DefinitionItem>
					<DefinitionItem label="MCP phase" monospace>{formatPhaseState(job?.mcp_status, job?.mcp_error)}</DefinitionItem>
					<DefinitionItem label="MCP logs" monospace>
						{@const mcpRunUrl = safeHref(job?.mcp_run_url)}
						{#if mcpRunUrl}
							<a href={mcpRunUrl} target="_blank" rel="noopener noreferrer">Open MCP logs</a>
						{:else}
							—
						{/if}
					</DefinitionItem>
					<DefinitionItem label="Verify translation" monospace>
						{#if job?.verify_translation_ok === true}
							ok
						{:else if job?.verify_translation_ok === false}
							fail{job.verify_translation_err ? `: ${job.verify_translation_err}` : ''}
						{:else}
							—
						{/if}
					</DefinitionItem>
					<DefinitionItem label="Verify trust" monospace>
						{#if job?.verify_trust_ok === true}
							ok
						{:else if job?.verify_trust_ok === false}
							fail{job.verify_trust_err ? `: ${job.verify_trust_err}` : ''}
						{:else}
							—
						{/if}
					</DefinitionItem>
					<DefinitionItem label="Verify tips" monospace>
						{#if job?.verify_tips_ok === true}
							ok
						{:else if job?.verify_tips_ok === false}
							fail{job.verify_tips_err ? `: ${job.verify_tips_err}` : ''}
						{:else}
							—
						{/if}
					</DefinitionItem>
					<DefinitionItem label="Verify AI" monospace>
						{#if job?.verify_ai_ok === true}
							ok
						{:else if job?.verify_ai_ok === false}
							fail{job.verify_ai_err ? `: ${job.verify_ai_err}` : ''}
						{:else}
							—
						{/if}
					</DefinitionItem>
				</DefinitionList>

				{#if job?.status === 'error'}
					<Alert variant="error" title="Update failed">
						<Text size="sm">
							Error: <span class="instance-detail__mono">{job.error_code || 'unknown'}</span>
						</Text>
						{#if job.error_message}
							<Text size="sm">{job.error_message}</Text>
						{/if}
						{#if job.note}
							<Text size="sm" color="secondary">{job.note}</Text>
						{/if}
						<Text size="sm" color="secondary">
							Contact support with job id <span class="instance-detail__mono">{job.id}</span>
							{#if job.request_id}
								and request id <span class="instance-detail__mono">{job.request_id}</span>.
							{/if}
						</Text>
					</Alert>
				{/if}
			{:else}
				<Alert variant="info" title="No update jobs">
					<Text size="sm">No updates have been run yet.</Text>
				</Alert>
			{/if}

			{#if updateJobs.length > 1}
				<div class="instance-detail__row">
					<Text size="sm" color="secondary">Recent update jobs:</Text>
				</div>
				<ul class="instance-detail__list">
					{#each updateJobs.slice(0, 10) as j (j.id)}
						<li class="instance-detail__list-item">
							<span class="instance-detail__mono">{j.id}</span> — {j.status} ({formatStep(j.step)})
						</li>
					{/each}
				</ul>
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

	.instance-detail__list {
		margin: var(--gr-spacing-scale-3) 0 0 0;
		padding-left: var(--gr-spacing-scale-5);
	}

	.instance-detail__list-item {
		margin: var(--gr-spacing-scale-1) 0;
	}
</style>
