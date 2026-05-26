<!--
@component
Operator Soul registry — re-skinned for the M2.1 dark warm-charcoal chrome.

Project 39 M3.5 (issue #449). Re-skins the Safe-mediated soul-registry
governance surface at `/operator/soul-registry`. Publishes (reputation root
/ validation root) and lists open governance operations by status. Adds a
top-of-page `SummaryStrip` with the active status-filter bucket count and
per-field `CopyButton` on the Safe-payload preview so an operator handing
off to Safe for signing can paste each field individually.

Posture preserved (and elevated because soul-registry mutations touch
the soul-registry namespace contract that lesser-soul publishes — every
mutation here is, by host's discipline, a Safe-ready governance event):
- Strict-CSP-safe: no inline scripts / styles / third-party origins; the
  Safe-app `frame-ancestors` exception lives in `safeAppCsp` and is
  unchanged.
- Safe-ready governance preserved: Open-in-Safe handoff continues to
  stage a short-lived operator session via `stageSafeAppSessionHandoff`
  and to build the Safe-app launch URL via `buildSafeWalletAppUrl` —
  no signer-bypass paths added, no new Safe contract calls introduced.
- On-chain integrity untouched: no contract changes, no mint-signer
  surface, no off-chain reconciliation contract change.
- Multi-tenant isolation: operator-scope only; soul-registry mutations
  govern the registry namespace, not per-tenant data.
- Trust-API instance-auth untouched; SEC-9 / SEC-12 change-locks not
  engaged.

Behavior preserved:
- Same `listSoulOperations` + `publishSoulReputationRoot` +
  `publishSoulValidationRoot` + `soulPublicGetConfig` endpoints.
- Same Safe-launch handoff with `stageSafeAppSessionHandoff` +
  `buildSafeWalletAppUrl`, same chain-id resolution, same clipboard
  fallback when popup blockers fire.
- Same operator-JWT requirement; same 401 → logout / login navigation
  guard; same status-filter contract (pending / proposed / executed /
  failed).
- No new operator-JWT routes; no new write endpoints; no new Safe
  contract calls.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M3.5
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListSoulOperationsResponse, PublishRootResponse, SoulOperation } from 'src/lib/api/soul';
	import { listSoulOperations, publishSoulReputationRoot, publishSoulValidationRoot, soulPublicGetConfig } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate, safeAppRootUrl, stageSafeAppTarget } from 'src/lib/router';
	import { session, stageSafeAppSessionHandoff } from 'src/lib/session';
	import { StatCard, SummaryStrip } from 'src/lib/shell';
	import type { StatCardStatus } from 'src/lib/shell';
	import { Alert, Badge, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Link, Select, Spinner, Text } from 'src/lib/ui';
	import { buildSafeWalletAppUrl } from 'src/lib/wallet/safeApp';

	let { token } = $props<{ token: string }>();

	let statusFilter = $state('pending');
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let ops = $state<ListSoulOperationsResponse | null>(null);

	let createError = $state<string | null>(null);
	let createResult = $state<PublishRootResponse | null>(null);
	let creating = $state(false);
	let safeChainId = $state<number | null>(null);
	let safeLaunchError = $state<string | null>(null);
	let safeLaunchNotice = $state<string | null>(null);

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

	/**
	 * Mirror TipRegistry.bucketStatus(): pending-bucket non-zero → amber
	 * (operator attention for sign-off); failed-bucket non-zero → danger;
	 * proposed / executed are informational.
	 */
	function bucketStatus(filter: string, count: number | null): StatCardStatus {
		if (count == null) return 'default';
		if (filter === 'pending' && count > 0) return 'warning';
		if (filter === 'failed' && count > 0) return 'danger';
		return 'default';
	}

	function parseSafePayload(op: SoulOperation): { safe_address: string; to: string; value: string; data: string } | null {
		if (!op.safe_payload) return null;
		try {
			const parsed = JSON.parse(op.safe_payload) as { safe_address?: string; to: string; value: string; data: string };
			return parsed.safe_address?.trim()
				? { safe_address: parsed.safe_address, to: parsed.to, value: parsed.value, data: parsed.data }
				: null;
		} catch {
			return null;
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

	async function openOperationInSafe(op: SoulOperation) {
		safeLaunchError = null;
		safeLaunchNotice = null;

		const safePayload = parseSafePayload(op);
		if (!safePayload) {
			safeLaunchError = 'This operation does not contain a Safe payload.';
			return;
		}

		const currentSession = get(session);
		if (!currentSession) {
			safeLaunchError = 'No operator session is available to hand off into Safe.';
			return;
		}
		if (!stageSafeAppSessionHandoff(currentSession)) {
			safeLaunchError = 'Failed to stage a short-lived operator session for Safe.';
			return;
		}
		stageSafeAppTarget(`/operator/soul/operations/${op.operation_id}`);

		const url =
			safeChainId != null
				? buildSafeWalletAppUrl({
						appUrl: safeAppRootUrl(),
						safeAddress: safePayload.safe_address,
						chainId: safeChainId,
					})
				: null;
		if (!url) {
			safeLaunchError = 'Could not build a Safe Wallet launch URL for this chain yet.';
			return;
		}

		try {
			await navigator.clipboard.writeText(url);
		} catch {
			// Clipboard is just a convenience here.
		}

		const popup = window.open(url, '_blank', 'noopener,noreferrer');
		if (popup) {
			safeLaunchNotice = 'Safe Wallet was opened in a new tab for the selected operation.';
		} else {
			safeLaunchNotice = `Safe Wallet launch URL copied. Open ${url} if a new tab did not appear.`;
		}
	}

	async function load() {
		errorMessage = null;
		ops = null;

		loading = true;
		try {
			ops = await listSoulOperations(token, statusFilter);
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

	async function publishReputation() {
		createError = null;
		createResult = null;
		creating = true;
		try {
			createResult = await publishSoulReputationRoot(token);
			await load();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			createError = formatError(err);
		} finally {
			creating = false;
		}
	}

	async function publishValidation() {
		createError = null;
		createResult = null;
		creating = true;
		try {
			createResult = await publishSoulValidationRoot(token);
			await load();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			createError = formatError(err);
		} finally {
			creating = false;
		}
	}

	const bucketCount = $derived<number | null>(ops ? ops.operations.length : null);

	onMount(() => {
		void loadConfig();
		void load();
	});
</script>

<div class="op-soul">
	<header class="op-soul__header">
		<div class="op-soul__title">
			<Heading level={2} size="xl">Soul registry</Heading>
			<Text color="secondary">Safe-mediated operations for publishes and governed registry changes.</Text>
		</div>
		<div class="op-soul__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	<SummaryStrip label="Operations in current bucket" columns={1} gap="md">
		<StatCard
			label={`Operations · status ${statusFilter}`}
			value={String(bucketCount ?? 0)}
			status={bucketStatus(statusFilter, bucketCount)}
		/>
	</SummaryStrip>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Create operations</Heading>
		{/snippet}

		<Text size="sm" color="secondary">Publish the current Merkle roots for off-chain verifiable snapshots.</Text>

		{#if createError}
			<Alert variant="error" title="Create failed">{createError}</Alert>
		{/if}
		{#if safeLaunchNotice}
			<Alert variant="info" title="Safe launch">{safeLaunchNotice}</Alert>
		{/if}
		{#if safeLaunchError}
			<Alert variant="error" title="Safe launch">{safeLaunchError}</Alert>
		{/if}

		<div class="op-soul__create">
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={4} size="lg">Publish reputation root</Heading>
				{/snippet}
				<Text size="sm" color="secondary">Build Merkle root from current reputation records and create a Safe operation.</Text>
				<Button variant="solid" onclick={() => void publishReputation()} disabled={creating}>Publish</Button>
			</Card>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={4} size="lg">Publish validation root</Heading>
				{/snippet}
				<Text size="sm" color="secondary">Build Merkle root from validation summaries and create a Safe operation.</Text>
				<Button variant="solid" onclick={() => void publishValidation()} disabled={creating}>Publish</Button>
			</Card>
		</div>

		{#if creating}
			<div class="op-soul__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Working…</Text>
			</div>
		{/if}

	{#if createResult}
		{@const res = createResult as PublishRootResponse}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={4} size="lg">Result</Heading>
			{/snippet}

			<Text size="sm" color="secondary">
				Operation <span class="op-soul__mono">{res.operation.operation_id}</span>
			</Text>
			<div class="op-soul__row">
				<Link {...linkProps(`/operator/soul/operations/${res.operation.operation_id}`)} variant="default">
					View operation
				</Link>
				<CopyButton size="sm" text={res.operation.operation_id} />
			</div>

			{#if res.safe_tx}
				<!--
					Safe-payload preview keeps each field's copy-to-clipboard
					adjacent to its value so an operator handing off to Safe
					for signing can paste each field individually. Mirrors the
					M3.4 Tip-registry preview. tx-hash reconciliation lives on
					the per-operation detail surface.
				-->
				<DefinitionList>
					<DefinitionItem label="Safe" monospace>
						<span class="op-soul__safe-row">
							<span>{res.safe_tx.safe_address}</span>
							<CopyButton size="sm" text={res.safe_tx.safe_address} />
						</span>
					</DefinitionItem>
					<DefinitionItem label="To" monospace>
						<span class="op-soul__safe-row">
							<span>{res.safe_tx.to}</span>
							<CopyButton size="sm" text={res.safe_tx.to} />
						</span>
					</DefinitionItem>
					<DefinitionItem label="Value" monospace>
						<span class="op-soul__safe-row">
							<span>{res.safe_tx.value}</span>
							<CopyButton size="sm" text={res.safe_tx.value} />
						</span>
					</DefinitionItem>
					<DefinitionItem label="Data" monospace>
						<span class="op-soul__safe-row">
							<span>{res.safe_tx.data}</span>
							<CopyButton size="sm" text={res.safe_tx.data} />
						</span>
					</DefinitionItem>
				</DefinitionList>
			{/if}
		</Card>
	{/if}
	</Card>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Operations</Heading>
		{/snippet}

		<div class="op-soul__filters">
			<div class="op-soul__field">
				<Text size="sm">Status</Text>
				<Select
					bind:value={statusFilter}
					options={[
						{ value: 'pending', label: 'pending' },
						{ value: 'proposed', label: 'proposed' },
						{ value: 'executed', label: 'executed' },
						{ value: 'failed', label: 'failed' },
					]}
				/>
			</div>
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Apply</Button>
		</div>

		{#if loading}
			<div class="op-soul__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Loading…</Text>
			</div>
		{:else if errorMessage}
			<Alert variant="error" title="Failed to load /api/v1/soul/operations">{errorMessage}</Alert>
		{:else if ops && ops.operations.length === 0}
			<Alert variant="info" title="No operations">
				<Text size="sm">No operations found for status {statusFilter}.</Text>
			</Alert>
		{:else if ops}
			<div class="op-soul__list">
				{#each ops.operations as op (op.operation_id)}
					{@const statusBadge = badgeForStatus(op.status)}
					{@const safePayload = parseSafePayload(op)}
					<Card variant="outlined" padding="md">
						<div class="op-soul__item">
							<div class="op-soul__item-meta">
								<div class="op-soul__row">
									<Text size="sm" weight="medium">{op.kind}</Text>
									<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">{op.status}</Badge>
								</div>
								<Text size="sm" color="secondary">Operation: <span class="op-soul__mono">{op.operation_id}</span></Text>
								{#if op.agent_id}
									<Text size="sm" color="secondary">Agent: <span class="op-soul__mono">{op.agent_id}</span></Text>
								{/if}
								{#if safePayload}
									<Text size="sm" color="secondary">To: <span class="op-soul__mono">{safePayload.to}</span></Text>
								{/if}
								<Text size="sm" color="secondary">Created: {op.created_at}</Text>
							</div>
							<div class="op-soul__item-actions">
								{#if safePayload}
									<Button variant="solid" onclick={() => void openOperationInSafe(op)} disabled={safeChainId == null}>
										Open in Safe
									</Button>
								{/if}
								<Link {...linkProps(`/operator/soul/operations/${op.operation_id}`)} variant="default">
									Open
								</Link>
								<CopyButton size="sm" text={op.operation_id} />
							</div>
						</div>
					</Card>
				{/each}
			</div>
		{/if}
	</Card>
</div>

<style>
	.op-soul {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-soul__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-soul__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.op-soul__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-soul__create {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-soul__filters {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: flex-end;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-soul__field {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-soul__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-soul__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-3);
	}

	.op-soul__item {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-soul__item-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-soul__item-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-soul__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	/* Inline Safe-payload row — see TipRegistry.svelte (M3.4) for rationale. */
	.op-soul__safe-row {
		display: inline-flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
		word-break: break-all;
	}

	/* Canonical mono token chain — see UserApprovals.svelte (M3.1) for rationale. */
	.op-soul__mono {
		font-family:
			var(--gr-typography-fontFamily-mono),
			ui-monospace,
			SFMono-Regular,
			Menlo,
			Monaco,
			Consolas,
			'Liberation Mono',
			'Courier New',
			monospace;
	}
</style>
