<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import { portalCreateCreditsCheckout } from 'src/lib/api/portalBilling';
	import type { BudgetMonthResponse, ListBudgetsResponse, UsageSummaryResponse } from 'src/lib/api/portalUsage';
	import { portalGetBudgetMonth, portalGetUsageSummary, portalListBudgets, portalSetBudgetMonth } from 'src/lib/api/portalUsage';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, DefinitionItem, DefinitionList, Heading, Spinner, Text, TextField } from 'src/lib/ui';

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

	let budget = $state<BudgetMonthResponse | null>(null);
	let summary = $state<UsageSummaryResponse | null>(null);
	let budgetsList = $state<ListBudgetsResponse | null>(null);

	let includedCreditsInput = $state<string>('');
	let setBudgetLoading = $state(false);
	let setBudgetError = $state<string | null>(null);

	let buyCreditsInput = $state<string>('1000');
	let buyLoading = $state(false);
	let buyError = $state<string | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function loadAll() {
		errorMessage = null;
		budget = null;
		summary = null;
		budgetsList = null;

		const normalizedMonth = normalizeMonth(month);
		if (!normalizedMonth) {
			errorMessage = 'Invalid month. Expected YYYY-MM.';
			return;
		}

		loading = true;
		try {
			const [b, s, bl] = await Promise.all([
				portalGetBudgetMonth(token, slug, normalizedMonth),
				portalGetUsageSummary(token, slug, normalizedMonth),
				portalListBudgets(token, slug),
			]);
			budget = b;
			summary = s;
			budgetsList = bl;
			includedCreditsInput = String(b.included_credits || 0);
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

	async function setBudgetMonth() {
		setBudgetError = null;

		const normalizedMonth = normalizeMonth(month);
		if (!normalizedMonth) {
			setBudgetError = 'Invalid month. Expected YYYY-MM.';
			return;
		}

		const parsed = Number.parseInt(includedCreditsInput.trim(), 10);
		if (!Number.isFinite(parsed) || parsed < 0) {
			setBudgetError = 'Included credits must be a non-negative integer.';
			return;
		}

		setBudgetLoading = true;
		try {
			budget = await portalSetBudgetMonth(token, slug, normalizedMonth, parsed);
			await loadAll();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			setBudgetError = formatError(err);
		} finally {
			setBudgetLoading = false;
		}
	}

	async function buyCredits() {
		buyError = null;

		const normalizedMonth = normalizeMonth(month);
		if (!normalizedMonth) {
			buyError = 'Invalid month. Expected YYYY-MM.';
			return;
		}

		const credits = Number.parseInt(buyCreditsInput.trim(), 10);
		if (!Number.isFinite(credits) || credits <= 0) {
			buyError = 'Credits must be a positive integer.';
			return;
		}

		buyLoading = true;
		try {
			const res = await portalCreateCreditsCheckout(token, { instance_slug: slug, credits, month: normalizedMonth });
			window.location.assign(res.checkout_url);
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			buyError = formatError(err);
		} finally {
			buyLoading = false;
		}
	}

	onMount(() => {
		void loadAll();
	});
</script>

<div class="budgets">
	<header class="budgets__header">
		<div class="budgets__title">
			<Heading level={2} size="xl">Budgets</Heading>
			<Text color="secondary">
				Manage included credits for <span class="budgets__mono">{slug}</span>.
			</Text>
		</div>
		<div class="budgets__actions">
			<Button variant="outline" onclick={() => void loadAll()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}/usage`)}>Usage</Button>
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}`)}>Back</Button>
			<Button variant="ghost" onclick={() => navigate('/portal/billing')}>Billing</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Month</Heading>
		{/snippet}

		<div class="budgets__form">
			<TextField label="Month" bind:value={month} placeholder="YYYY-MM" />
		</div>
		<div class="budgets__row">
			<Button variant="outline" onclick={() => void loadAll()} disabled={loading}>Load</Button>
		</div>
	</Card>

	{#if loading}
		<div class="budgets__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load budget">{errorMessage}</Alert>
	{:else}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Summary</Heading>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Included credits" monospace>{String(budget?.included_credits ?? summary?.included_credits ?? 0)}</DefinitionItem>
				<DefinitionItem label="Used credits" monospace>{String(budget?.used_credits ?? summary?.used_credits ?? 0)}</DefinitionItem>
				<DefinitionItem label="Cache hit rate" monospace>{summary ? String(Math.round(summary.cache_hit_rate * 1000) / 10) + '%' : '—'}</DefinitionItem>
				<DefinitionItem label="Debited credits" monospace>{summary ? String(summary.debited_credits) : '—'}</DefinitionItem>
				<DefinitionItem label="Discount credits" monospace>{summary ? String(summary.discount_credits) : '—'}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Set included credits</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Sets the month’s included credits (must be ≥ used credits).</Text>

			<div class="budgets__form">
				<TextField label="Included credits" bind:value={includedCreditsInput} placeholder="0" />
			</div>
			<div class="budgets__row">
				<Button variant="solid" onclick={() => void setBudgetMonth()} disabled={setBudgetLoading}>Save</Button>
			</div>
			{#if setBudgetLoading}
				<div class="budgets__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Saving…</Text>
				</div>
			{/if}
			{#if setBudgetError}
				<Alert variant="error" title="Set budget failed">{setBudgetError}</Alert>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Buy credits</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Redirect-only checkout (no third-party scripts).</Text>

			<div class="budgets__form">
				<TextField label="Credits" bind:value={buyCreditsInput} placeholder="1000" />
			</div>
			<div class="budgets__row">
				<Button variant="solid" onclick={() => void buyCredits()} disabled={buyLoading}>Checkout</Button>
			</div>
			{#if buyLoading}
				<div class="budgets__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Redirecting…</Text>
				</div>
			{/if}
			{#if buyError}
				<Alert variant="error" title="Checkout failed">{buyError}</Alert>
			{/if}
		</Card>

		{#if budgetsList && budgetsList.budgets.length > 0}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">History</Heading>
				{/snippet}
				<Text size="sm" color="secondary">Stored budget months for this instance.</Text>
				<div class="budgets__history">
					{#each budgetsList.budgets.slice(0, 12) as b (b.month)}
						<div class="budgets__history-row">
							<Text size="sm"><span class="budgets__mono">{b.month}</span></Text>
							<Text size="sm" color="secondary">
								included <span class="budgets__mono">{String(b.included_credits)}</span> · used
								<span class="budgets__mono">{String(b.used_credits)}</span>
							</Text>
						</div>
					{/each}
				</div>
			</Card>
		{/if}
	{/if}
</div>

<style>
	.budgets {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.budgets__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.budgets__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.budgets__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.budgets__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.budgets__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.budgets__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.budgets__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.budgets__history {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		margin-top: var(--gr-spacing-scale-4);
	}

	.budgets__history-row {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.budgets__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
