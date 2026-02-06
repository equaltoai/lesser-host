<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { BudgetMonthResponse, ListBudgetsResponse } from 'src/lib/api/portalUsage';
	import { portalListBudgets } from 'src/lib/api/portalUsage';
	import type { DomainResponse, InstanceResponse, ProvisionJobResponse } from 'src/lib/api/portalInstances';
	import {
		portalGetInstance,
		portalGetProvisioning,
		portalListInstanceDomains,
	} from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
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

	async function loadAll(targetSlug: string) {
		errorMessage = null;
		instance = null;
		domains = [];
		budgets = null;
		provisioning = null;

		loading = true;
		try {
			const [inst, dom, bud] = await Promise.all([
				portalGetInstance(token, targetSlug),
				portalListInstanceDomains(token, targetSlug),
				portalListBudgets(token, targetSlug),
			]);
			instance = inst;
			domains = dom.domains ?? [];
			budgets = bud;

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
</style>
