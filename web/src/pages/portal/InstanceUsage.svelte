<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListUsageResponse, UsageSummaryResponse } from 'src/lib/api/portalUsage';
	import { portalGetUsageSummary, portalListUsage } from 'src/lib/api/portalUsage';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, DefinitionItem, DefinitionList, Heading, Spinner, Text, TextField } from 'src/lib/ui';

	let { token, slug } = $props<{ token: string; slug: string }>();

	function currentMonthUTC(): string {
		return new Date().toISOString().slice(0, 7);
	}

	function normalizeMonth(input: string): string | null {
		const trimmed = input.trim();
		if (!/^[0-9]{4}-[0-9]{2}$/.test(trimmed)) return null;
		const mm = Number.parseInt(trimmed.slice(5, 7), 10);
		if (!Number.isFinite(mm) || mm < 1 || mm > 12) return null;
		return trimmed;
	}

	let month = $state<string>(currentMonthUTC());

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);

	let summary = $state<UsageSummaryResponse | null>(null);
	let ledger = $state<ListUsageResponse | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function formatPercent(p: number): string {
		if (!Number.isFinite(p)) return '—';
		return `${Math.round(p * 1000) / 10}%`;
	}

	async function loadAll() {
		errorMessage = null;
		summary = null;
		ledger = null;

		const normalizedMonth = normalizeMonth(month);
		if (!normalizedMonth) {
			errorMessage = 'Invalid month. Expected YYYY-MM.';
			return;
		}

		loading = true;
		try {
			const [s, l] = await Promise.all([
				portalGetUsageSummary(token, slug, normalizedMonth),
				portalListUsage(token, slug, normalizedMonth),
			]);
			summary = s;
			ledger = l;
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

	onMount(() => {
		void loadAll();
	});
</script>

<div class="usage">
	<header class="usage__header">
		<div class="usage__title">
			<Heading level={2} size="xl">Usage</Heading>
			<Text color="secondary">
				View credit usage for <span class="usage__mono">{slug}</span>.
			</Text>
		</div>
		<div class="usage__actions">
			<Button variant="outline" onclick={() => void loadAll()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/budgets`)}>Budgets</Button>
			<Button variant="ghost" onclick={() => navigate('/portal/billing')}>Billing</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}`)}>Back</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Month</Heading>
		{/snippet}

		<div class="usage__form">
			<TextField label="Month" bind:value={month} placeholder="YYYY-MM" />
		</div>
		<div class="usage__row">
			<Button variant="outline" onclick={() => void loadAll()} disabled={loading}>Load</Button>
		</div>
	</Card>

	{#if loading}
		<div class="usage__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load usage">{errorMessage}</Alert>
	{:else}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Summary</Heading>
			{/snippet}
			<Text size="sm" color="secondary">
				<span class="usage__mono">list_credits</span> is pre-discount, <span class="usage__mono">requested_credits</span> is after discount.
			</Text>

			<DefinitionList>
				<DefinitionItem label="Requests" monospace>{String(summary?.requests ?? 0)}</DefinitionItem>
				<DefinitionItem label="Cache hit rate" monospace>{summary ? formatPercent(summary.cache_hit_rate) : '—'}</DefinitionItem>
				<DefinitionItem label="List credits" monospace>{summary ? String(summary.list_credits) : '—'}</DefinitionItem>
				<DefinitionItem label="Requested credits" monospace>{summary ? String(summary.requested_credits) : '—'}</DefinitionItem>
				<DefinitionItem label="Discount credits" monospace>{summary ? String(summary.discount_credits) : '—'}</DefinitionItem>
				<DefinitionItem label="Debited credits" monospace>{summary ? String(summary.debited_credits) : '—'}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Ledger</Heading>
			{/snippet}
			<Text size="sm" color="secondary">
				Per-request credit ledger. Cached requests should debit fewer credits.
			</Text>

			{#if ledger && ledger.entries.length === 0}
				<Alert variant="info" title="No usage">
					<Text size="sm">No usage entries for this month.</Text>
				</Alert>
			{:else if ledger}
				<div class="usage__entries">
					{#each ledger.entries.slice(0, 100) as entry (entry.id)}
						<div class="usage__entry">
							<div class="usage__entry-main">
								<Text size="sm">
									<span class="usage__mono">{entry.module}</span>
									{#if entry.cached}
										<Badge variant="outlined" color="info" size="sm">cached</Badge>
									{:else}
										<Badge variant="outlined" color="gray" size="sm">live</Badge>
									{/if}
								</Text>
								<Text size="sm" color="secondary">
									debited <span class="usage__mono">{String(entry.debited_credits)}</span>
									· requested <span class="usage__mono">{String(entry.requested_credits)}</span>
									{#if entry.list_credits != null}
										· list <span class="usage__mono">{String(entry.list_credits)}</span>
									{/if}
								</Text>
							</div>
							<div class="usage__entry-meta">
								<Text size="sm" color="secondary">
									<span class="usage__mono">{entry.created_at}</span>
								</Text>
								{#if entry.request_id}
									<Text size="sm" color="secondary">
										req <span class="usage__mono">{entry.request_id}</span>
									</Text>
								{/if}
							</div>
						</div>
					{/each}
				</div>
				{#if ledger.entries.length > 100}
					<Text size="sm" color="secondary">Showing first 100 entries.</Text>
				{/if}
			{:else}
				<Alert variant="warning" title="No data">
					<Text size="sm">No response from usage endpoints.</Text>
				</Alert>
			{/if}
		</Card>
	{/if}
</div>

<style>
	.usage {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.usage__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.usage__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.usage__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.usage__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.usage__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.usage__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.usage__entries {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.usage__entry {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		flex-wrap: wrap;
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-subtle);
		border-radius: var(--gr-radius-md);
		background: var(--gr-color-surface);
	}

	.usage__entry-main {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.usage__entry-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		align-items: flex-end;
	}

	.usage__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
