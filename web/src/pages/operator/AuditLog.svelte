<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { ListOperatorAuditLogResponse } from 'src/lib/api/operatorAudit';
	import { listOperatorAuditLog } from 'src/lib/api/operatorAudit';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, CopyButton, Heading, Spinner, Text, TextField } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let data = $state<ListOperatorAuditLogResponse | null>(null);

	let actor = $state('');
	let action = $state('');
	let target = $state('');
	let requestId = $state('');
	let since = $state('');
	let until = $state('');

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function load() {
		errorMessage = null;
		data = null;

		loading = true;
		try {
			data = await listOperatorAuditLog(token, {
				actor: actor.trim() || undefined,
				action: action.trim() || undefined,
				target: target.trim() || undefined,
				request_id: requestId.trim() || undefined,
				since: since.trim() || undefined,
				until: until.trim() || undefined,
				limit: 100,
			});
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
		void load();
	});
</script>

<div class="op-audit">
	<header class="op-audit__header">
		<div class="op-audit__title">
			<Heading level={2} size="xl">Audit log</Heading>
			<Text color="secondary">Search operator actions.</Text>
		</div>
		<div class="op-audit__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Filters</Heading>
		{/snippet}
		<Text size="sm" color="secondary">Times are RFC3339 (UTC recommended).</Text>
		<div class="op-audit__filters">
			<TextField label="Actor" bind:value={actor} placeholder="wallet-…" />
			<TextField label="Action" bind:value={action} placeholder="portal.instance.config.update" />
			<TextField label="Target" bind:value={target} placeholder="instance:slug" />
			<TextField label="Request id" bind:value={requestId} placeholder="req-…" />
			<TextField label="Since" bind:value={since} placeholder="2026-01-01T00:00:00Z" />
			<TextField label="Until" bind:value={until} placeholder="2026-01-31T23:59:59Z" />
		</div>
		<div class="op-audit__row">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Search</Button>
		</div>
	</Card>

	{#if loading}
		<div class="op-audit__loading">
			<Spinner size="md" />
			<Text>Loading…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Audit log">{errorMessage}</Alert>
	{:else if data && data.entries.length === 0}
		<Alert variant="info" title="No results">
			<Text size="sm">No audit entries matched.</Text>
		</Alert>
	{:else if data}
		<Card variant="outlined" padding="lg">
			{#snippet header()}
				<Heading level={3} size="lg">Results</Heading>
			{/snippet}
			<div class="op-audit__list">
				{#each data.entries as entry (entry.id)}
					<div class="op-audit__list-row">
						<div class="op-audit__list-main">
							<Text size="sm">
								<span class="op-audit__mono">{entry.action}</span> · <span class="op-audit__mono">{entry.target}</span>
							</Text>
							<Text size="sm" color="secondary">
								actor <span class="op-audit__mono">{entry.actor}</span> ·
								<span class="op-audit__mono">{entry.created_at}</span>
							</Text>
							<Text size="sm" color="secondary">
								request <span class="op-audit__mono">{entry.request_id}</span>
							</Text>
						</div>
						<div class="op-audit__list-actions">
							<CopyButton size="sm" text={entry.request_id} />
							<CopyButton size="sm" text={entry.target} />
						</div>
					</div>
				{/each}
			</div>
		</Card>
	{:else}
		<Alert variant="warning" title="No data">
			<Text size="sm">No response from audit endpoint.</Text>
		</Alert>
	{/if}
</div>

<style>
	.op-audit {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.op-audit__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.op-audit__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-audit__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.op-audit__filters {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-audit__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.op-audit__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.op-audit__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		margin-top: var(--gr-spacing-scale-4);
	}

	.op-audit__list-row {
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

	.op-audit__list-main {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.op-audit__list-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.op-audit__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>

