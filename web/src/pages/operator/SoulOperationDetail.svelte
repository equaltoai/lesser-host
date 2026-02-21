<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { SoulOperation, SafeTxPayload } from 'src/lib/api/soul';
	import { getSoulOperation, recordSoulOperationExecution } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import {
		Alert,
		Badge,
		Button,
		Card,
		CopyButton,
		DefinitionItem,
		DefinitionList,
		Heading,
		Spinner,
		Text,
		TextArea,
		TextField,
	} from 'src/lib/ui';

	let { token, id } = $props<{ token: string; id: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let op = $state<SoulOperation | null>(null);

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

	function parseSafePayload(raw?: string): SafeTxPayload | null {
		if (!raw) return null;
		try {
			return JSON.parse(raw) as SafeTxPayload;
		} catch {
			return null;
		}
	}

	const statusBadge = $derived.by(() => badgeForStatus(op?.status ?? ''));
	const safePayload = $derived.by(() => parseSafePayload(op?.safe_payload));

	async function load() {
		errorMessage = null;
		recordError = null;
		op = null;

		loading = true;
		try {
			op = await getSoulOperation(token, id);
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
			op = await recordSoulOperationExecution(token, id, trimmed);
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

<div class="op-soul-op">
	<header class="op-soul-op__header">
		<div class="op-soul-op__title">
			<Heading level={2} size="xl">Soul operation</Heading>
			<Text color="secondary"><span class="op-soul-op__mono">{id}</span></Text>
		</div>
		<div class="op-soul-op__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Button variant="ghost" onclick={() => navigate('/operator/soul')}>Back</Button>
		</div>
	</header>

	{#if loading}
		<div class="op-soul-op__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Soul operation">{errorMessage}</Alert>
	{:else if op}
		{@const current = op as SoulOperation}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<div class="op-soul-op__row">
					<div class="op-soul-op__row-left">
						<Heading level={3} size="lg">Overview</Heading>
						<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">{current.status}</Badge>
					</div>
					<div class="op-soul-op__row-right">
						<CopyButton size="sm" text={current.operation_id} />
					</div>
				</div>
			{/snippet}

			<DefinitionList>
				<DefinitionItem label="Kind" monospace>{current.kind}</DefinitionItem>
				<DefinitionItem label="Status" monospace>{current.status}</DefinitionItem>
				<DefinitionItem label="Agent" monospace>{current.agent_id || '—'}</DefinitionItem>
				<DefinitionItem label="Created" monospace>{current.created_at}</DefinitionItem>
				<DefinitionItem label="Updated" monospace>{current.updated_at}</DefinitionItem>
				<DefinitionItem label="Executed" monospace>{current.executed_at || '—'}</DefinitionItem>
			</DefinitionList>
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Safe payload</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Copy into Safe (or submit via your Safe workflow).</Text>

			{#if safePayload}
				<DefinitionList>
					<DefinitionItem label="Safe" monospace>{safePayload.safe_address}</DefinitionItem>
					<DefinitionItem label="To" monospace>{safePayload.to}</DefinitionItem>
					<DefinitionItem label="Value" monospace>{safePayload.value}</DefinitionItem>
					<DefinitionItem label="Data" monospace>{safePayload.data}</DefinitionItem>
				</DefinitionList>

				<div class="op-soul-op__row">
					<CopyButton size="sm" text={safePayload.safe_address} />
					<CopyButton size="sm" text={safePayload.to} />
					<CopyButton size="sm" text={safePayload.value} />
					<CopyButton size="sm" text={safePayload.data} />
				</div>
			{:else}
				<Alert variant="warning" title="Missing payload">
					<Text size="sm">Operation does not contain a parseable Safe payload.</Text>
				</Alert>
				{#if current.safe_payload}
					<TextArea value={current.safe_payload} readonly rows={6} />
				{/if}
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Reconcile execution</Heading>
			{/snippet}
			<Text size="sm" color="secondary">Record an on-chain execution tx hash to reconcile status.</Text>

			<div class="op-soul-op__form">
				<TextField label="exec_tx_hash" bind:value={execTxHash} placeholder="0x…" />
				<Button variant="solid" onclick={() => void recordExecution()} disabled={recordLoading}>Record</Button>
			</div>

			{#if recordLoading}
				<div class="op-soul-op__loading-inline">
					<Spinner size="sm" />
					<Text size="sm">Recording…</Text>
				</div>
			{/if}
			{#if recordError}
				<Alert variant="error" title="Record failed">{recordError}</Alert>
			{/if}

			{#if current.exec_tx_hash}
				<DefinitionList>
					<DefinitionItem label="Exec tx hash" monospace>{current.exec_tx_hash}</DefinitionItem>
					<DefinitionItem label="Exec block" monospace>{String(current.exec_block_number || 0)}</DefinitionItem>
					<DefinitionItem label="Exec success" monospace>{String(current.exec_success ?? '—')}</DefinitionItem>
				</DefinitionList>
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Receipt</Heading>
			{/snippet}
			<div class="op-soul-op__row">
				<Button variant="outline" onclick={() => (showReceipt = !showReceipt)} disabled={!current.receipt_json}>
					{showReceipt ? 'Hide receipt' : 'Show receipt'}
				</Button>
			</div>
			{#if showReceipt && current.receipt_json}
				<TextArea value={current.receipt_json} readonly rows={10} />
			{/if}
		</Card>

		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Snapshot</Heading>
			{/snippet}
			<div class="op-soul-op__row">
				<Button variant="outline" onclick={() => (showSnapshot = !showSnapshot)} disabled={!current.snapshot_json}>
					{showSnapshot ? 'Hide snapshot' : 'Show snapshot'}
				</Button>
			</div>
			{#if showSnapshot && current.snapshot_json}
				<TextArea value={current.snapshot_json} readonly rows={10} />
			{/if}
		</Card>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No operation response.</Text>
		</Alert>
	{/if}
</div>

<style>
	.op-soul-op {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-soul-op__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-soul-op__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-soul-op__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-soul-op__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-soul-op__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		justify-content: space-between;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-soul-op__row-left {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-soul-op__row-right {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-soul-op__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-soul-op__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-soul-op__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
