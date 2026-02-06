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
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, DefinitionItem, DefinitionList, Heading, Select, Spinner, Text, TextField } from 'src/lib/ui';

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
						<Button variant="outline" onclick={() => navigate(`/operator/tip-registry/operations/${res.operation.id}`)}>
							View operation
						</Button>
						<CopyButton size="sm" text={res.operation.id} />
					</div>

					{#if res.safe_tx}
						<DefinitionList>
							<DefinitionItem label="Safe" monospace>{res.safe_tx.safe_address}</DefinitionItem>
							<DefinitionItem label="To" monospace>{res.safe_tx.to}</DefinitionItem>
							<DefinitionItem label="Value" monospace>{res.safe_tx.value}</DefinitionItem>
							<DefinitionItem label="Data" monospace>{res.safe_tx.data}</DefinitionItem>
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
							<Button variant="outline" onclick={() => navigate(`/operator/tip-registry/operations/${op.id}`)}>View</Button>
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

	.op-tip__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
