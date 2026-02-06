<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { AddDomainVerification, DomainResponse } from 'src/lib/api/portalInstances';
	import {
		portalAddInstanceDomain,
		portalDisableInstanceDomain,
		portalDeleteInstanceDomain,
		portalListInstanceDomains,
		portalRotateInstanceDomain,
		portalUpsertDomainVerificationRoute53,
		portalVerifyInstanceDomain,
	} from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
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

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let domains = $state<DomainResponse[]>([]);

	let addDomain = $state('');
	let addLoading = $state(false);
	let addError = $state<string | null>(null);

	let actionLoadingDomain = $state<string | null>(null);
	let actionError = $state<string | null>(null);

	let verificationByDomain = $state<Record<string, AddDomainVerification>>({});

	let copyNotice = $state<string | null>(null);

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
		return domains.find((d) => d.type === 'primary') ?? null;
	}

	function vanityDomains(): DomainResponse[] {
		return domains.filter((d) => d.type !== 'primary');
	}

	function vanityStatusLabel(d: DomainResponse): string {
		if (d.type !== 'vanity') return d.status;
		if (d.status === 'verified') return 'verified (awaiting approval)';
		return d.status;
	}

	async function loadDomains() {
		errorMessage = null;
		domains = [];

		loading = true;
		try {
			const res = await portalListInstanceDomains(token, slug);
			domains = res.domains ?? [];
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

	async function copy(text: string) {
		copyNotice = null;
		try {
			await navigator.clipboard.writeText(text);
			copyNotice = 'Copied to clipboard.';
			window.setTimeout(() => {
				copyNotice = null;
			}, 1500);
		} catch {
			copyNotice = 'Copy failed.';
		}
	}

	async function addVanityDomain() {
		addError = null;

		const raw = addDomain.trim();
		if (!raw) {
			addError = 'Domain is required.';
			return;
		}

		addLoading = true;
		try {
			const res = await portalAddInstanceDomain(token, slug, raw);
			verificationByDomain = { ...verificationByDomain, [res.domain.domain]: res.verification };
			addDomain = '';
			await loadDomains();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			addError = formatError(err);
		} finally {
			addLoading = false;
		}
	}

	async function rotate(d: DomainResponse) {
		actionError = null;
		actionLoadingDomain = d.domain;
		try {
			const res = await portalRotateInstanceDomain(token, slug, d.domain);
			verificationByDomain = { ...verificationByDomain, [res.domain.domain]: res.verification };
			await loadDomains();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actionLoadingDomain = null;
		}
	}

	async function verify(d: DomainResponse) {
		actionError = null;
		actionLoadingDomain = d.domain;
		try {
			await portalVerifyInstanceDomain(token, slug, d.domain);
			await loadDomains();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actionLoadingDomain = null;
		}
	}

	async function route53AssistAndVerify(d: DomainResponse) {
		actionError = null;
		actionLoadingDomain = d.domain;
		try {
			await portalUpsertDomainVerificationRoute53(token, slug, d.domain);
			await portalVerifyInstanceDomain(token, slug, d.domain);
			await loadDomains();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actionLoadingDomain = null;
		}
	}

	async function disable(d: DomainResponse) {
		actionError = null;
		actionLoadingDomain = d.domain;
		try {
			await portalDisableInstanceDomain(token, slug, d.domain);
			await loadDomains();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actionLoadingDomain = null;
		}
	}

	async function remove(d: DomainResponse) {
		actionError = null;

		if (!confirm(`Delete domain "${d.domain}"?`)) {
			return;
		}

		actionLoadingDomain = d.domain;
		try {
			await portalDeleteInstanceDomain(token, slug, d.domain);
			await loadDomains();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			actionError = formatError(err);
		} finally {
			actionLoadingDomain = null;
		}
	}

	onMount(() => {
		void loadDomains();
	});
</script>

<div class="domains">
	<header class="domains__header">
		<div class="domains__title">
			<Heading level={2} size="xl">Domains</Heading>
			<Text color="secondary">Manage vanity domains for <span class="domains__mono">{slug}</span>.</Text>
		</div>
		<div class="domains__actions">
			<Button variant="outline" onclick={() => void loadDomains()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}`)}>Back</Button>
		</div>
	</header>

	{#if loading}
		<div class="domains__loading">
			<Spinner size="md" />
			<Text>Loading domains…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load domains">{errorMessage}</Alert>
	{:else}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Primary domain</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Managed by lesser.host.</Text>
			<DefinitionList>
				<DefinitionItem label="Domain" monospace>{primaryDomain()?.domain || '—'}</DefinitionItem>
				<DefinitionItem label="Status" monospace>{primaryDomain()?.status || '—'}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Add vanity domain</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Verify by adding a DNS TXT record.</Text>

			<div class="domains__form">
				<TextField label="Domain" bind:value={addDomain} placeholder="example.com" />
			</div>
			<div class="domains__row">
				<Button variant="solid" onclick={() => void addVanityDomain()} disabled={addLoading}>
					Add domain
				</Button>
			</div>

			{#if addError}
				<Alert variant="error" title="Add failed">{addError}</Alert>
			{/if}
		</Card>

		{#if actionError}
			<Alert variant="error" title="Domain action failed">{actionError}</Alert>
		{/if}

		{#if copyNotice}
			<Alert variant="info" title="Clipboard">{copyNotice}</Alert>
		{/if}

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Vanity domains</Heading>
			{/snippet}

			{#if vanityDomains().length === 0}
				<Alert variant="info" title="No vanity domains">
					<Text size="sm">Add a vanity domain above.</Text>
				</Alert>
			{:else}
				<div class="domains__list">
					{#each vanityDomains() as d (d.domain)}
						<Card variant="outlined" padding="md">
							<div class="domains__item">
								<div class="domains__item-meta">
									<Text size="sm" weight="medium">{d.domain}</Text>
									<Text size="sm" color="secondary">Status: {vanityStatusLabel(d)}</Text>
									{#if verificationByDomain[d.domain]?.txt_name && verificationByDomain[d.domain]?.txt_value}
										<Text size="sm" color="secondary">TXT name:</Text>
										<div class="domains__mono-row">
											<code class="domains__mono">{verificationByDomain[d.domain].txt_name}</code>
											<Button variant="ghost" onclick={() => void copy(verificationByDomain[d.domain].txt_name || '')}>
												Copy
											</Button>
										</div>
										<Text size="sm" color="secondary">TXT value:</Text>
										<div class="domains__mono-row">
											<code class="domains__mono">{verificationByDomain[d.domain].txt_value}</code>
											<Button variant="ghost" onclick={() => void copy(verificationByDomain[d.domain].txt_value || '')}>
												Copy
											</Button>
										</div>
									{/if}
								</div>

								<div class="domains__item-actions">
									{#if d.status === 'pending'}
										<Button
											variant="outline"
											onclick={() => void route53AssistAndVerify(d)}
											disabled={actionLoadingDomain === d.domain}
										>
											Route53 assist + verify
										</Button>
										<Button
											variant="outline"
											onclick={() => void verify(d)}
											disabled={actionLoadingDomain === d.domain}
										>
											Verify
										</Button>
										<Button
											variant="outline"
											onclick={() => void rotate(d)}
											disabled={actionLoadingDomain === d.domain}
										>
											Rotate token
										</Button>
									{:else if d.status === 'active'}
										<Button
											variant="outline"
											onclick={() => void disable(d)}
											disabled={actionLoadingDomain === d.domain}
										>
											Disable
										</Button>
									{:else if d.status === 'verified'}
										<Button
											variant="outline"
											onclick={() => void disable(d)}
											disabled={actionLoadingDomain === d.domain}
										>
											Disable
										</Button>
									{:else if d.status === 'disabled'}
										<Button
											variant="outline"
											onclick={() => void rotate(d)}
											disabled={actionLoadingDomain === d.domain}
										>
											Re-verify
										</Button>
									{/if}

									<Button
										variant="outline"
										onclick={() => void remove(d)}
										disabled={actionLoadingDomain === d.domain}
									>
										Delete
									</Button>
								</div>
							</div>
						</Card>
					{/each}
				</div>
			{/if}
		</Card>
	{/if}
</div>

<style>
	.domains {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.domains__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.domains__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.domains__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.domains__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.domains__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.domains__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.domains__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.domains__item {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.domains__item-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		min-width: min(520px, 100%);
	}

	.domains__item-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.domains__mono-row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.domains__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		word-break: break-word;
	}
</style>

