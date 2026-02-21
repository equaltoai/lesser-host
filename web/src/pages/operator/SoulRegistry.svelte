<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListSoulOperationsResponse, PublishRootResponse, SoulOperation } from 'src/lib/api/soul';
	import { listSoulOperations, publishSoulReputationRoot, publishSoulValidationRoot } from 'src/lib/api/soul';
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
		Select,
		Spinner,
		Text,
	} from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let statusFilter = $state('pending');
	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let ops = $state<ListSoulOperationsResponse | null>(null);

	let createError = $state<string | null>(null);
	let createResult = $state<PublishRootResponse | null>(null);
	let creating = $state(false);

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

	function parseSafePayload(op: SoulOperation): { safe_address: string; to: string; value: string; data: string } | null {
		if (!op.safe_payload) return null;
		try {
			return JSON.parse(op.safe_payload) as { safe_address: string; to: string; value: string; data: string };
		} catch {
			return null;
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

	onMount(() => {
		void load();
	});
</script>

<div class="op-soul">
	<header class="op-soul__header">
		<div class="op-soul__title">
			<Heading level={2} size="xl">Soul registry</Heading>
			<Text color="secondary">Safe-first operations (mints, rotations, publishes).</Text>
		</div>
		<div class="op-soul__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Create operations</Heading>
		{/snippet}

		<Text size="sm" color="secondary">Publish the current Merkle roots for off-chain verifiable snapshots.</Text>

		{#if createError}
			<Alert variant="error" title="Create failed">{createError}</Alert>
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
				<Button
					variant="outline"
					onclick={() => navigate(`/operator/soul/operations/${res.operation.operation_id}`)}
				>
					View operation
				</Button>
				<CopyButton size="sm" text={res.operation.operation_id} />
			</div>

			{#if res.safe_tx}
				<DefinitionList>
					<DefinitionItem label="Safe" monospace>{res.safe_tx.safe_address}</DefinitionItem>
					<DefinitionItem label="To" monospace>{res.safe_tx.to}</DefinitionItem>
					<DefinitionItem label="Value" monospace>{res.safe_tx.value}</DefinitionItem>
					<DefinitionItem label="Data" monospace>{res.safe_tx.data}</DefinitionItem>
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
								<Button variant="outline" onclick={() => navigate(`/operator/soul/operations/${op.operation_id}`)}>
									Open
								</Button>
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

	.op-soul__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>
