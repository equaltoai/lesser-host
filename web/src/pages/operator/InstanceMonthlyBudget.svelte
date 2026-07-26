<script lang="ts">
	import { type ApiError } from 'src/lib/api/http';
	import type { BudgetMonthResponse } from 'src/lib/api/portalUsage';
	import { operatorSetBudgetMonth, portalGetBudgetMonth } from 'src/lib/api/portalUsage';
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

	let {
		token,
		slug,
		canEdit = false,
	} = $props<{ token: string; slug: string; canEdit?: boolean }>();

	let month = $state(currentMonthUTC());
	let budget = $state<BudgetMonthResponse | null>(null);
	let loading = $state(false);
	let loadError = $state<string | null>(null);

	let editing = $state(false);
	let includedCreditsInput = $state('');
	let pendingIncludedCredits = $state<number | null>(null);
	let saveLoading = $state(false);
	let saveError = $state<string | null>(null);
	let saveSuccess = $state<string | null>(null);

	let loadedKey = '';
	let requestGeneration = 0;

	function currentMonthUTC(): string {
		return new Date().toISOString().slice(0, 7);
	}

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function remainingCredits(value: BudgetMonthResponse | null = budget): number {
		if (!value) return 0;
		return value.remaining_credits ?? Math.max(value.included_credits - value.used_credits, 0);
	}

	function resetEditor() {
		editing = false;
		pendingIncludedCredits = null;
		saveError = null;
	}

	async function handleUnauthorized(err: unknown): Promise<boolean> {
		if ((err as Partial<ApiError>).status !== 401) return false;
		await logout();
		navigate('/login');
		return true;
	}

	async function loadCurrentBudget(targetSlug: string, targetMonth: string) {
		const generation = ++requestGeneration;
		loading = true;
		loadError = null;
		budget = null;
		saveSuccess = null;
		resetEditor();

		try {
			const result = await portalGetBudgetMonth(token, targetSlug, targetMonth);
			if (generation !== requestGeneration) return;
			budget = result;
			includedCreditsInput = String(result.included_credits);
		} catch (err) {
			if (generation !== requestGeneration) return;
			if (await handleUnauthorized(err)) return;
			loadError = formatError(err);
		} finally {
			if (generation === requestGeneration) {
				loading = false;
			}
		}
	}

	function refresh() {
		const targetSlug = slug.trim().toLowerCase();
		const targetMonth = currentMonthUTC();
		if (!targetSlug) return;
		month = targetMonth;
		loadedKey = `${targetSlug}:${targetMonth}`;
		void loadCurrentBudget(targetSlug, targetMonth);
	}

	function beginEdit() {
		if (!canEdit || !budget) return;
		editing = true;
		pendingIncludedCredits = null;
		includedCreditsInput = String(budget.included_credits);
		saveError = null;
		saveSuccess = null;
	}

	function reviewChange() {
		saveError = null;
		pendingIncludedCredits = null;

		const raw = includedCreditsInput.trim();
		if (!/^[0-9]+$/.test(raw)) {
			saveError = 'Included credits must be a non-negative integer.';
			return;
		}
		const parsed = Number(raw);
		if (!Number.isSafeInteger(parsed)) {
			saveError = 'Included credits must be a safe integer.';
			return;
		}
		if (budget && parsed === budget.included_credits) {
			saveError = 'Enter a different included credit total.';
			return;
		}
		pendingIncludedCredits = parsed;
	}

	async function confirmChange() {
		if (!canEdit || !budget || pendingIncludedCredits === null) return;

		const targetSlug = slug.trim().toLowerCase();
		const targetMonth = month;
		const includedCredits = pendingIncludedCredits;
		saveLoading = true;
		saveError = null;
		saveSuccess = null;

		try {
			const result = await operatorSetBudgetMonth(token, targetSlug, targetMonth, includedCredits);
			if (targetSlug !== slug.trim().toLowerCase() || targetMonth !== month) return;
			budget = result;
			includedCreditsInput = String(result.included_credits);
			resetEditor();
			saveSuccess = 'Monthly budget updated.';
		} catch (err) {
			if (await handleUnauthorized(err)) return;
			saveError = formatError(err);
		} finally {
			saveLoading = false;
		}
	}

	$effect(() => {
		const targetSlug = slug.trim().toLowerCase();
		const targetMonth = currentMonthUTC();
		const targetKey = `${targetSlug}:${targetMonth}`;
		if (!targetSlug || targetKey === loadedKey) return;

		loadedKey = targetKey;
		month = targetMonth;
		void loadCurrentBudget(targetSlug, targetMonth);
	});
</script>

<Card variant="outlined" padding="lg">
	{#snippet header()}
		<div class="monthly-budget__header">
			<div class="monthly-budget__title">
				<Heading level={3} size="lg">Current-month credit budget</Heading>
				<Text size="sm" color="secondary">UTC month <span class="monthly-budget__mono">{month}</span></Text>
			</div>
			<Button variant="outline" onclick={refresh} disabled={loading || saveLoading}>Refresh</Button>
		</div>
	{/snippet}

	{#if loading}
		<div class="monthly-budget__loading">
			<Spinner size="sm" />
			<Text size="sm">Loading current budget…</Text>
		</div>
	{:else if loadError}
		<Alert variant="error" title="Current budget unavailable">{loadError}</Alert>
	{:else if budget}
		<DefinitionList>
			<DefinitionItem label="Included credits" monospace>{String(budget.included_credits)}</DefinitionItem>
			<DefinitionItem label="Used credits" monospace>{String(budget.used_credits)}</DefinitionItem>
			<DefinitionItem label="Remaining" monospace>{String(remainingCredits())}</DefinitionItem>
		</DefinitionList>

		{#if remainingCredits() === 0}
			<Alert variant="warning" title="Budget exhausted">
				No included credits remain for this UTC month. Fail-closed billing guards continue to block new billable work.
			</Alert>
		{/if}

		{#if saveSuccess}
			<Alert variant="success" title="Budget">{saveSuccess}</Alert>
		{/if}

		{#if canEdit}
			{#if !editing}
				<div class="monthly-budget__actions">
					<Button variant="outline" onclick={beginEdit}>Adjust included credits</Button>
				</div>
			{:else}
				<div class="monthly-budget__editor">
					<TextField label="Included credits" bind:value={includedCreditsInput} placeholder="0" />
					<Text size="sm" color="secondary">
						Recorded usage is not changed. The new non-negative total applies immediately to <span class="monthly-budget__mono">{month}</span>.
					</Text>
					<div class="monthly-budget__actions">
						<Button
							variant="solid"
							onclick={reviewChange}
							disabled={saveLoading || pendingIncludedCredits !== null}
						>
							Review change
						</Button>
						<Button variant="outline" onclick={resetEditor} disabled={saveLoading}>Cancel</Button>
					</div>
				</div>

				{#if pendingIncludedCredits !== null}
					<Alert variant="warning" title="Confirm budget change">
						<Text size="sm">
							Change {month} included credits from {String(budget.included_credits)} to {String(pendingIncludedCredits)}.
							Used credits remain unchanged.
						</Text>
						{#if pendingIncludedCredits < budget.used_credits}
							<Text size="sm">
								The new total is below already-used credits, so remaining credits will be zero and billable work will stay blocked.
							</Text>
						{/if}
						<div class="monthly-budget__actions">
							<Button variant="solid" onclick={() => void confirmChange()} disabled={saveLoading}>
								Confirm budget change
							</Button>
							<Button
								variant="outline"
								onclick={() => (pendingIncludedCredits = null)}
								disabled={saveLoading}
							>
								Back
							</Button>
						</div>
					</Alert>
				{/if}

				{#if saveLoading}
					<div class="monthly-budget__loading">
						<Spinner size="sm" />
						<Text size="sm">Saving…</Text>
					</div>
				{/if}
				{#if saveError}
					<Alert variant="error" title="Budget update failed">{saveError}</Alert>
				{/if}
			{/if}
		{:else}
			<Text size="sm" color="secondary">Admin role required to adjust included credits.</Text>
		{/if}
	{/if}
</Card>

<style>
	.monthly-budget__header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--gr-spacing-scale-3);
		flex-wrap: wrap;
	}

	.monthly-budget__title,
	.monthly-budget__editor {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.monthly-budget__editor {
		margin-top: var(--gr-spacing-scale-4);
	}

	.monthly-budget__actions,
	.monthly-budget__loading {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	.monthly-budget__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
