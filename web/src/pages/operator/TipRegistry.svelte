<!--
@component
Operator Tip registry — re-skinned for the M2.1 dark warm-charcoal chrome.

Project 39 M3.4 (issue #448). The index surface at `/operator/tip-registry`
that gathers Safe-mediated tip-registry mutations (host ensure / set host
active / token allowlist) and lists open operations by status. The dark
chrome treats Safe-payload preview material as first-class evidence: per-row
CopyButton on each Safe payload field so the safe-launch flow round-trips
unbroken from this surface (the Open-operation detail page —
`TipRegistryOperationDetail.svelte` — handles tx-hash reconciliation on
its own re-skin track via operator-detail wiring).

Behavior preserved:
- Same `ensureTipRegistryHost` / `setTipRegistryHostActive` /
  `setTipRegistryTokenAllowed` / `listTipRegistryOperations` endpoints.
- Same Safe-payload data shape — `safe_address` / `to` / `value` / `data`
  copy-to-clipboard is preserved (now per-field for the Safe-payload
  audit-trail; the per-operation `View operation` link still navigates to
  the detail page where tx-hash reconciliation lives).
- Same operator-JWT requirement; same 401 → logout / login navigation
  guard; same idempotent ensure-host no-op surface; same TipSplitter Safe
  contract interaction (this page is presentational; no Safe contract or
  Safe-ready governance mutation is introduced).
- No new operator-JWT routes; no new write endpoints.

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins; the
  Safe-app `frame-ancestors` exception lives in `safeAppCsp` and is
  unchanged.
- Multi-tenant isolation: operator-scope only; tip-registry mutations
  govern the platform-wide allowlist, not per-tenant data.
- Trust-API instance-auth untouched; SEC-9 / SEC-12 change-locks not
  engaged.
- On-chain integrity untouched: Safe-ready payloads continue to flow
  through the existing tip-registry operations contract.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M3.4
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { CreateTipRegistryOperationResponse, EnsureTipRegistryHostNoopResponse, ListTipRegistryOperationsResponse } from 'src/lib/api/tipRegistry';
	import {
		ensureTipRegistryHost,
		listTipRegistryOperations,
		setTipRegistryHostActive,
		setTipRegistryTokenAllowed,
	} from 'src/lib/api/tipRegistry';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { StatCard, SummaryStrip } from 'src/lib/shell';
	import type { StatCardStatus } from 'src/lib/shell';
	import { Alert, Badge, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Link, Select, Spinner, Text, TextField } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let statusFilter = $state('pending');
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let ops = $state<ListTipRegistryOperationsResponse | null>(null);

	let createError = $state<string | null>(null);
	let createResult = $state<CreateTipRegistryOperationResponse | EnsureTipRegistryHostNoopResponse | null>(null);
	let creating = $state(false);

	let ensureDomain = $state('');
	let hostDomain = $state('');
	let hostActive = $state('true');
	let tokenAddress = $state('');
	let tokenAllowed = $state('true');

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
	 * Map the active status-filter result count to a `StatCardStatus` tone
	 * so the top-of-page SummaryStrip telegraphs whether there's a backlog.
	 * Pending-bucket non-zero → amber (operator attention); other buckets
	 * (proposed / executed / failed) stay neutral — they're informational
	 * not actionable from this surface.
	 */
	function bucketStatus(filter: string, count: number | null): StatCardStatus {
		if (count == null) return 'default';
		if (filter === 'pending' && count > 0) return 'warning';
		if (filter === 'failed' && count > 0) return 'danger';
		return 'default';
	}

	async function load() {
		errorMessage = null;
		ops = null;

		loading = true;
		try {
			ops = await listTipRegistryOperations(token, statusFilter);
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

	async function createEnsure() {
		createError = null;
		createResult = null;
		const domain = ensureDomain.trim();
		if (!domain) {
			createError = 'Domain is required.';
			return;
		}
		creating = true;
		try {
			createResult = await ensureTipRegistryHost(token, domain);
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

	async function createHostActive() {
		createError = null;
		createResult = null;
		const domain = hostDomain.trim();
		if (!domain) {
			createError = 'Domain is required.';
			return;
		}
		creating = true;
		try {
			createResult = await setTipRegistryHostActive(token, domain, hostActive === 'true');
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

	async function createTokenAllowed() {
		createError = null;
		createResult = null;
		const addr = tokenAddress.trim();
		if (!addr) {
			createError = 'Token address is required.';
			return;
		}
		creating = true;
		try {
			createResult = await setTipRegistryTokenAllowed(token, addr, tokenAllowed === 'true');
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
		void load();
	});
</script>

<div class="op-tip">
	<header class="op-tip__header">
		<div class="op-tip__title">
			<Heading level={2} size="xl">Tip registry</Heading>
			<Text color="secondary">Safe-first operations and reconciliation.</Text>
		</div>
		<div class="op-tip__actions">
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

		{#if createError}
			<Alert variant="error" title="Create failed">{createError}</Alert>
		{/if}

		<div class="op-tip__create">
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={4} size="lg">Ensure host</Heading>
				{/snippet}
				<Text size="sm" color="secondary">Creates register/update/activate operation if needed.</Text>
				<div class="op-tip__form">
					<TextField label="Domain" bind:value={ensureDomain} placeholder="example.com" />
					<Button variant="solid" onclick={() => void createEnsure()} disabled={creating}>Ensure</Button>
				</div>
			</Card>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={4} size="lg">Set host active</Heading>
				{/snippet}
				<div class="op-tip__form">
					<TextField label="Domain" bind:value={hostDomain} placeholder="example.com" />
					<div class="op-tip__field">
						<Text size="sm">Active</Text>
						<Select bind:value={hostActive} options={[{ value: 'true', label: 'true' }, { value: 'false', label: 'false' }]} />
					</div>
					<Button variant="solid" onclick={() => void createHostActive()} disabled={creating}>Create</Button>
				</div>
			</Card>

			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={4} size="lg">Token allowlist</Heading>
				{/snippet}
				<div class="op-tip__form">
					<TextField label="Token address" bind:value={tokenAddress} placeholder="0x…" />
					<div class="op-tip__field">
						<Text size="sm">Allowed</Text>
						<Select bind:value={tokenAllowed} options={[{ value: 'true', label: 'true' }, { value: 'false', label: 'false' }]} />
					</div>
					<Button variant="solid" onclick={() => void createTokenAllowed()} disabled={creating}>Create</Button>
				</div>
			</Card>
		</div>

		{#if creating}
			<div class="op-tip__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Working…</Text>
			</div>
		{/if}

		{#if createResult}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={4} size="lg">Result</Heading>
				{/snippet}

				{#if (createResult as EnsureTipRegistryHostNoopResponse).noop}
					<Alert variant="info" title="No-op">
						<Text size="sm">Host already matches desired state.</Text>
					</Alert>
				{:else}
					{@const res = createResult as CreateTipRegistryOperationResponse}
					<Text size="sm" color="secondary">
						Operation <span class="op-tip__mono">{res.operation.id}</span>
					</Text>
					<div class="op-tip__row">
						<Link {...linkProps(`/operator/tip-registry/operations/${res.operation.id}`)} variant="default">
							View operation
						</Link>
						<CopyButton size="sm" text={res.operation.id} />
					</div>

					{#if res.safe_tx}
						<!--
							Safe-payload preview keeps each field's copy-to-clipboard
							adjacent to its value so an operator handing off to Safe
							for signing can paste each field individually without
							hunting through a JSON blob. This preserves the M3.4
							"Safe payload copy-to-clipboard preserved" acceptance
							criterion. tx-hash reconciliation continues to live on
							the per-operation detail page.
						-->
						<DefinitionList>
							<DefinitionItem label="Safe" monospace>
								<span class="op-tip__safe-row">
									<span>{res.safe_tx.safe_address}</span>
									<CopyButton size="sm" text={res.safe_tx.safe_address} />
								</span>
							</DefinitionItem>
							<DefinitionItem label="To" monospace>
								<span class="op-tip__safe-row">
									<span>{res.safe_tx.to}</span>
									<CopyButton size="sm" text={res.safe_tx.to} />
								</span>
							</DefinitionItem>
							<DefinitionItem label="Value" monospace>
								<span class="op-tip__safe-row">
									<span>{res.safe_tx.value}</span>
									<CopyButton size="sm" text={res.safe_tx.value} />
								</span>
							</DefinitionItem>
							<DefinitionItem label="Data" monospace>
								<span class="op-tip__safe-row">
									<span>{res.safe_tx.data}</span>
									<CopyButton size="sm" text={res.safe_tx.data} />
								</span>
							</DefinitionItem>
						</DefinitionList>
					{/if}
				{/if}
			</Card>
		{/if}
	</Card>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Operations</Heading>
		{/snippet}
		<div class="op-tip__filters">
			<div class="op-tip__field">
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
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Load</Button>
		</div>

		{#if loading}
			<div class="op-tip__loading">
				<Spinner size="md" />
				<Text>Loading…</Text>
			</div>
		{:else if errorMessage}
			<Alert variant="error" title="Tip registry">{errorMessage}</Alert>
		{:else if ops && ops.operations.length === 0}
			<Alert variant="info" title="No operations">
				<Text size="sm">No operations for this status.</Text>
			</Alert>
		{:else if ops}
			<div class="op-tip__list">
				{#each ops.operations as op (op.id)}
					<div class="op-tip__list-row">
						<div class="op-tip__list-main">
							<Text size="sm">
								{@const b = badgeForStatus(op.status)}
								<Badge variant={b.variant} color={b.color} size="sm">{op.status}</Badge>
								<span class="op-tip__mono">{op.kind}</span>
								<span class="op-tip__mono">{op.id}</span>
							</Text>
							<Text size="sm" color="secondary">
								{op.domain_normalized || op.token_address || '—'} · created <span class="op-tip__mono">{op.created_at}</span>
							</Text>
						</div>
						<div class="op-tip__list-actions">
							<Link {...linkProps(`/operator/tip-registry/operations/${op.id}`)} variant="default">View</Link>
							<CopyButton size="sm" text={op.id} />
						</div>
					</div>
				{/each}
			</div>
		{:else}
			<Alert variant="warning" title="No data">
				<Text size="sm">No response from tip registry endpoints.</Text>
			</Alert>
		{/if}
	</Card>
</div>

<style>
	.op-tip {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-tip__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-tip__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-tip__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-tip__create {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
		gap: var(--gr-spacing-scale-4);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-tip__form {
		display: grid;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-tip__filters {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: flex-end;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.op-tip__field {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		min-width: 220px;
	}

	.op-tip__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
		flex-wrap: wrap;
	}

	.op-tip__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-tip__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-tip__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-tip__list-row {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: center;
		flex-wrap: wrap;
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-subtle);
		border-radius: var(--gr-radius-md);
		background: var(--gr-color-surface);
	}

	.op-tip__list-main {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-tip__list-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	/* Inline Safe-payload row: value + CopyButton sit on the same line so the
	 * field is paste-ready without scrolling. The hash string wraps if the
	 * viewport narrows. */
	.op-tip__safe-row {
		display: inline-flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
		word-break: break-all;
	}

	/* Canonical mono token chain — see UserApprovals.svelte (M3.1) for rationale. */
	.op-tip__mono {
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
