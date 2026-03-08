<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';

	import type { ApiError } from 'src/lib/api/http';
	import type { SoulOperation, SafeTxPayload } from 'src/lib/api/soul';
	import { getSoulOperation, recordSoulOperationExecution, soulPublicGetConfig } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate, safeAppRootUrl, stageSafeAppTarget } from 'src/lib/router';
	import { session, stageSafeAppSessionHandoff } from 'src/lib/session';
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
	import {
		buildSafeWalletAppUrl,
		clearPendingSafeAppTxHash,
		detectSafeAppContext,
		getSafeAppTransaction,
		loadPendingSafeAppTxHash,
		savePendingSafeAppTxHash,
		submitSafeAppTransaction,
		summarizeSafeAppExecution,
		type SafeAppContext,
		type SafeAppExecutionStatus,
	} from 'src/lib/wallet/safeApp';

	let { token, id } = $props<{ token: string; id: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let op = $state<SoulOperation | null>(null);

	let execTxHash = $state('');
	let recordLoading = $state(false);
	let recordError = $state<string | null>(null);

	let showReceipt = $state(false);
	let showSnapshot = $state(false);

	let safeAppLoading = $state(false);
	let safeAppSubmitting = $state(false);
	let safeAppError = $state<string | null>(null);
	let safeAppNotice = $state<string | null>(null);
	let safeAppContext = $state<SafeAppContext | null>(null);
	let safeAppTxHash = $state('');
	let safeAppStatus = $state<SafeAppExecutionStatus | null>(null);
	let safeChainId = $state<number | null>(null);
	let safeAppPollTimer = 0;

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
			const parsed = JSON.parse(raw) as SafeTxPayload;
			return parsed.safe_address?.trim() ? parsed : null;
		} catch {
			return null;
		}
	}

	const statusBadge = $derived.by(() => badgeForStatus(op?.status ?? ''));
	const safePayload = $derived.by(() => parseSafePayload(op?.safe_payload));
	const safeAppUrl = $derived.by(() => safeAppRootUrl());
	const safeWalletUrl = $derived.by(() => {
		if (!safePayload || !safeChainId) return '';
		return (
			buildSafeWalletAppUrl({
				appUrl: safeAppUrl,
				safeAddress: safePayload.safe_address,
				chainId: safeChainId,
			}) || ''
		);
	});
	const safeAppSafeMatches = $derived.by(() => {
		if (!safeAppContext || !safePayload) return false;
		return safeAppContext.info.safeAddress.toLowerCase() === safePayload.safe_address.toLowerCase();
	});

	function stopSafeAppPolling() {
		if (safeAppPollTimer) {
			window.clearInterval(safeAppPollTimer);
			safeAppPollTimer = 0;
		}
	}

	async function load() {
		errorMessage = null;
		recordError = null;

		loading = true;
		try {
			op = await getSoulOperation(token, id);
			if (op?.exec_tx_hash) {
				clearPendingSafeAppTxHash(id);
				stopSafeAppPolling();
				safeAppTxHash = '';
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

	async function recordExecutionHash(txHash: string, source: 'manual' | 'safe'): Promise<void> {
		recordLoading = true;
		try {
			op = await recordSoulOperationExecution(token, id, txHash);
			execTxHash = '';
			if (source === 'safe') {
				clearPendingSafeAppTxHash(id);
				safeAppNotice = 'Execution was mined and reconciled back into lesser-host.';
				safeAppError = null;
				stopSafeAppPolling();
			}
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			if (source === 'safe') {
				safeAppError = formatError(err);
			} else {
				recordError = formatError(err);
			}
		} finally {
			recordLoading = false;
		}
	}

	async function recordExecution() {
		recordError = null;
		const trimmed = execTxHash.trim();
		if (!trimmed) {
			recordError = 'exec_tx_hash is required.';
			return;
		}
		await recordExecutionHash(trimmed, 'manual');
	}

	async function pollSafeExecution(safeTxHash: string) {
		if (!safeAppContext) return;
		try {
			const details = await getSafeAppTransaction(safeAppContext, safeTxHash);
			safeAppStatus = summarizeSafeAppExecution(details);
			safeAppError = null;
			if (safeAppStatus.txHash && safeAppStatus.txHash !== op?.exec_tx_hash) {
				await recordExecutionHash(safeAppStatus.txHash, 'safe');
			}
		} catch (err) {
			safeAppError = formatError(err);
		}
	}

	function startSafeAppPolling(safeTxHash: string) {
		stopSafeAppPolling();
		void pollSafeExecution(safeTxHash);
		safeAppPollTimer = window.setInterval(() => {
			void pollSafeExecution(safeTxHash);
		}, 5000);
	}

	async function initSafeApp() {
		safeAppLoading = true;
		try {
			safeAppContext = await detectSafeAppContext();
			safeAppTxHash = loadPendingSafeAppTxHash(id);
			if (safeAppContext && safeAppTxHash) {
				startSafeAppPolling(safeAppTxHash);
			}
		} finally {
			safeAppLoading = false;
		}
	}

	async function loadConfig() {
		try {
			const cfg = await soulPublicGetConfig();
			safeChainId = typeof cfg.chain_id === 'number' ? cfg.chain_id : null;
		} catch {
			safeChainId = null;
		}
	}

	function stageSafeAppLaunch(): boolean {
		const currentSession = get(session);
		if (!currentSession) {
			safeAppError = 'No operator session is available to hand off into Safe.';
			return false;
		}
		if (!stageSafeAppSessionHandoff(currentSession)) {
			safeAppError = 'Failed to stage a short-lived operator session for Safe.';
			return false;
		}
		stageSafeAppTarget(`/operator/soul/operations/${id}`);
		return true;
	}

	async function openSafeWallet() {
		safeAppError = null;
		safeAppNotice = null;
		if (!stageSafeAppLaunch()) {
			return;
		}
		const url = safeWalletUrl;
		if (!url) {
			safeAppError = 'Could not build a Safe Wallet launch URL for this chain yet.';
			safeAppNotice = `As a fallback, add lesser-host as a custom app with ${safeAppUrl}.`;
			return;
		}
		try {
			await navigator.clipboard.writeText(url);
		} catch {
			// Clipboard is just a convenience here.
		}
		const popup = window.open(url, '_blank', 'noopener,noreferrer');
		if (popup) {
			safeAppNotice = 'Safe Wallet was opened in a new tab and the launch URL was copied as a fallback.';
		} else {
			safeAppNotice = `Safe Wallet launch URL copied. Open ${url} if a new tab did not appear.`;
		}
	}

	async function copySafeWalletUrl() {
		safeAppError = null;
		safeAppNotice = null;
		if (!stageSafeAppLaunch()) {
			return;
		}
		const url = safeWalletUrl;
		if (!url) {
			safeAppError = 'Could not build a Safe Wallet launch URL for this chain yet.';
			return;
		}
		try {
			await navigator.clipboard.writeText(url);
			safeAppNotice = 'Copied the full Safe Wallet launch URL for this operation.';
		} catch (err) {
			safeAppError = formatError(err);
		}
	}

	async function submitViaSafe() {
		safeAppError = null;
		safeAppNotice = null;
		if (!safeAppContext || !safePayload) {
			safeAppError = 'Open this page inside the matching Safe app first.';
			return;
		}
		if (!safeAppSafeMatches) {
			safeAppError = 'The currently opened Safe does not match this operation.';
			return;
		}
		if (safeAppContext.info.isReadOnly) {
			safeAppError = 'This Safe view is read-only and cannot submit transactions.';
			return;
		}

		safeAppSubmitting = true;
		try {
			const txHash = await submitSafeAppTransaction(safeAppContext, {
				to: safePayload.to,
				value: safePayload.value,
				data: safePayload.data,
			});
			safeAppTxHash = txHash;
			savePendingSafeAppTxHash(id, txHash);
			safeAppNotice = 'Submitted to Safe. Waiting for confirmation/execution status.';
			startSafeAppPolling(txHash);
		} catch (err) {
			safeAppError = formatError(err);
		} finally {
			safeAppSubmitting = false;
		}
	}

	onMount(() => {
		void initSafeApp();
		void loadConfig();
		void load();
		return () => stopSafeAppPolling();
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

		{#if safePayload && !current.exec_tx_hash}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={3} size="lg">Execute With Safe</Heading>
				{/snippet}

				<div class="op-soul-op__stack">
					{#if safeAppLoading}
						<div class="op-soul-op__loading-inline">
							<Spinner size="sm" />
							<Text size="sm">Checking for Safe app context…</Text>
						</div>
					{:else if safeAppContext}
						<DefinitionList>
							<DefinitionItem label="Opened safe" monospace>{safeAppContext.info.safeAddress}</DefinitionItem>
							<DefinitionItem label="Threshold" monospace>{String(safeAppContext.info.threshold)}</DefinitionItem>
							<DefinitionItem label="Read only" monospace>{String(safeAppContext.info.isReadOnly)}</DefinitionItem>
						</DefinitionList>

						{#if !safeAppSafeMatches}
							<Alert variant="warning" title="Wrong Safe">
								This operation targets <span class="op-soul-op__mono">{safePayload.safe_address}</span>, but the current
								Safe app is opened on <span class="op-soul-op__mono">{safeAppContext.info.safeAddress}</span>.
							</Alert>
						{:else if safeAppContext.info.isReadOnly}
							<Alert variant="warning" title="Read-only Safe">
								This Safe context is read-only. Open the writable Safe and try again.
							</Alert>
						{:else}
							<Button variant="solid" onclick={() => void submitViaSafe()} disabled={safeAppSubmitting || recordLoading}>
								{safeAppSubmitting ? 'Submitting to Safe…' : 'Submit via Safe'}
							</Button>
						{/if}
					{:else}
						<Text size="sm" color="secondary">
							Open this operation directly in Safe Wallet. The staged operator session lets the Safe app land on this
							operation without manual navigation.
						</Text>
						<div class="op-soul-op__row">
							<Button variant="solid" onclick={() => void openSafeWallet()} disabled={!safeWalletUrl}>Open in Safe</Button>
							<Button variant="outline" onclick={() => void copySafeWalletUrl()} disabled={!safeWalletUrl}>
								Copy Safe Wallet URL
							</Button>
						</div>
						<Text size="sm" color="secondary">
							{#if safeWalletUrl}
								If Safe asks for the custom app root, use <span class="op-soul-op__mono">{safeAppUrl}</span> once.
							{:else}
								Waiting for chain configuration before building the Safe Wallet launch URL.
							{/if}
						</Text>
					{/if}

					{#if safeAppNotice}
						<Alert variant="info" title="Safe app">{safeAppNotice}</Alert>
					{/if}
					{#if safeAppError}
						<Alert variant="error" title="Safe app">{safeAppError}</Alert>
					{/if}

					{#if safeAppTxHash || safeAppStatus}
						<DefinitionList>
							<DefinitionItem label="Safe tx hash" monospace>{safeAppTxHash || '—'}</DefinitionItem>
							<DefinitionItem label="Safe tx status" monospace>{safeAppStatus?.txStatus || '—'}</DefinitionItem>
							<DefinitionItem label="Execution tx hash" monospace>{safeAppStatus?.txHash || current.exec_tx_hash || '—'}</DefinitionItem>
							<DefinitionItem label="Confirmations" monospace>
								{safeAppStatus?.confirmationsSubmitted != null && safeAppStatus?.confirmationsRequired != null
									? `${safeAppStatus.confirmationsSubmitted}/${safeAppStatus.confirmationsRequired}`
									: '—'}
							</DefinitionItem>
						</DefinitionList>
					{/if}
				</div>
			</Card>
		{/if}

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

	.op-soul-op__stack {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-4);
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
