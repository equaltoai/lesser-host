<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { TipRegistryOperation } from 'src/lib/api/tipRegistry';
	import { getTipRegistryOperation, recordTipRegistryOperationExecution } from 'src/lib/api/tipRegistry';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Spinner, Text, TextArea, TextField } from 'src/lib/ui';

	let { token, id } = $props<{ token: string; id: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let op = $state<TipRegistryOperation | null>(null);

	let execTxHash = $state('');
	let recordLoading = $state(false);
	let recordError = $state<string | null>(null);

	let showReceipt = $state(false);
	let showSnapshot = $state(false);

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
		if (s === 'executed') return { variant: 'filled', color: 'success' };
		if (s === 'proposed' || s === 'pending') return { variant: 'outlined', color: 'warning' };
		if (s === 'failed') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	const statusBadge = $derived.by(() => badgeForStatus(op?.status ?? ''));

	async function load() {
		errorMessage = null;
		recordError = null;
		op = null;

		loading = true;
		try {
			op = await getTipRegistryOperation(token, id);
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

	async function recordExecution() {
		recordError = null;
		const trimmed = execTxHash.trim();
		if (!trimmed) {
			recordError = 'exec_tx_hash is required.';
			return;
		}
		recordLoading = true;
		try {
			op = await recordTipRegistryOperationExecution(token, id, trimmed);
			execTxHash = '';
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			recordError = formatError(err);
		} finally {
			recordLoading = false;
		}
	}

	onMount(() => {
		void load();
	});
</script>

<div class="op-tip-op">
	<header class="op-tip-op__header">
		<div class="op-tip-op__title">
			<Heading level={2} size="xl">Tip registry operation</Heading>
			<Text color="secondary"><span class="op-tip-op__mono">{id}</span></Text>
		</div>
		<div class="op-tip-op__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/tip-registry')}>Back</Button>
		</div>
	</header>

	{#if loading}
		<div class="op-tip-op__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Tip registry operation">{errorMessage}</Alert>
	{:else if op}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<div class="op-tip-op__row">
					<div class="op-tip-op__row-left">
						<Heading level={3} size="lg">Overview</Heading>
						<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">{op?.status ?? '—'}</Badge>
					</div>
					<div class="op-tip-op__row-right">
						<CopyButton size="sm" text={op?.id ?? ''} />
					</div>
				</div>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Kind" monospace>{op.kind}</DefinitionItem>
				<DefinitionItem label="Status" monospace>{op.status}</DefinitionItem>
				<DefinitionItem label="Chain id" monospace>{String(op.chain_id)}</DefinitionItem>
				<DefinitionItem label="Contract" monospace>{op.contract_address}</DefinitionItem>
				<DefinitionItem label="Tx mode" monospace>{op.tx_mode || '—'}</DefinitionItem>
				<DefinitionItem label="Safe" monospace>{op.safe_address || '—'}</DefinitionItem>
				<DefinitionItem label="Domain" monospace>{op.domain_normalized || '—'}</DefinitionItem>
				<DefinitionItem label="Token" monospace>{op.token_address || '—'}</DefinitionItem>
				<DefinitionItem label="Created" monospace>{op.created_at}</DefinitionItem>
				<DefinitionItem label="Updated" monospace>{op.updated_at}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Safe payload</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Copy into Safe (or submit via your Safe workflow).</Text>

			<DefinitionList>
				<DefinitionItem label="To" monospace>{op.tx_to || '—'}</DefinitionItem>
				<DefinitionItem label="Value" monospace>{op.tx_value || '—'}</DefinitionItem>
				<DefinitionItem label="Data" monospace>{op.tx_data || '—'}</DefinitionItem>
				<DefinitionItem label="Safe tx hash" monospace>{op.safe_tx_hash || '—'}</DefinitionItem>
			</DefinitionList>

			<div class="op-tip-op__row">
				<CopyButton size="sm" text={op.tx_to || ''} />
				<CopyButton size="sm" text={op.tx_value || ''} />
				<CopyButton size="sm" text={op.tx_data || ''} />
			</div>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Reconcile execution</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Record an on-chain execution tx hash to reconcile status.</Text>

			<div class="op-tip-op__form">
				<TextField label="exec_tx_hash" bind:value={execTxHash} placeholder="0x…" />
				<Button variant="solid" onclick={() => void recordExecution()} disabled={recordLoading}>Record</Button>
			</div>

			{#if recordLoading}
				<div class="op-tip-op__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Recording…</Text>
				</div>
			{/if}
			{#if recordError}
				<Alert variant="error" title="Record failed">{recordError}</Alert>
			{/if}

			{#if op.exec_tx_hash}
				<DefinitionList>
					<DefinitionItem label="Exec tx hash" monospace>{op.exec_tx_hash}</DefinitionItem>
					<DefinitionItem label="Exec block" monospace>{String(op.exec_block_number || 0)}</DefinitionItem>
					<DefinitionItem label="Exec success" monospace>{String(op.exec_success ?? '—')}</DefinitionItem>
				</DefinitionList>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Receipt</Heading>
			{/snippet}
			<div class="op-tip-op__row">
				<Button variant="outline" onclick={() => (showReceipt = !showReceipt)} disabled={!op.receipt_json}>
					{showReceipt ? 'Hide receipt' : 'Show receipt'}
				</Button>
			</div>
			{#if showReceipt && op.receipt_json}
				<TextArea value={op.receipt_json} readonly rows={10} />
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Snapshot</Heading>
			{/snippet}
			<div class="op-tip-op__row">
				<Button variant="outline" onclick={() => (showSnapshot = !showSnapshot)} disabled={!op.snapshot_json}>
					{showSnapshot ? 'Hide snapshot' : 'Show snapshot'}
				</Button>
			</div>
			{#if showSnapshot && op.snapshot_json}
				<TextArea value={op.snapshot_json} readonly rows={10} />
			{/if}
		</Card>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No operation response.</Text>
		</Alert>
	{/if}
</div>

<style>
	.op-tip-op {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-tip-op__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-tip-op__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-tip-op__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-tip-op__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-tip-op__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-tip-op__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-tip-op__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-tip-op__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-tip-op__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-tip-op__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
