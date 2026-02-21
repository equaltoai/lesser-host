<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { SoulMineAgentItem } from 'src/lib/api/soul';
	import { soulListMyAgents } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Badge, Button, Card, CopyButton, Heading, Spinner, Text } from 'src/lib/ui';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let agents = $state<SoulMineAgentItem[]>([]);

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
		if (s === 'active') return { variant: 'filled', color: 'success' };
		if (s === 'pending') return { variant: 'outlined', color: 'warning' };
		if (s === 'suspended') return { variant: 'filled', color: 'error' };
		return { variant: 'outlined', color: 'gray' };
	}

	function shortHex(hex: string, left: number = 8, right: number = 6): string {
		const h = (hex || '').trim();
		if (h.length <= left + right + 2) return h;
		return `${h.slice(0, left)}…${h.slice(-right)}`;
	}

	async function load() {
		errorMessage = null;
		agents = [];

		loading = true;
		try {
			const res = await soulListMyAgents(token);
			agents = res.agents ?? [];
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

<div class="souls">
	<header class="souls__header">
		<div class="souls__title">
			<Heading level={2} size="xl">My Agents</Heading>
			<Text color="secondary">Manage Lesser Soul identities, reputation, and validation.</Text>
		</div>
		<div class="souls__actions">
			<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
			<Button variant="solid" onclick={() => navigate('/portal/souls/register')}>Register agent</Button>
		</div>
	</header>

	{#if loading}
		<div class="souls__loading">
			<Spinner size="md" />
			<Text>Loading agents…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load /api/v1/soul/agents/mine">{errorMessage}</Alert>
	{:else if agents.length === 0}
		<Alert variant="info" title="No agents">
			<Text size="sm">Register your first agent to get started.</Text>
			<div class="souls__actions-inline">
				<Button variant="solid" onclick={() => navigate('/portal/souls/register')}>Register agent</Button>
			</div>
		</Alert>
	{:else}
		<div class="souls__list">
			{#each agents as item (item.agent.agent_id)}
				{@const statusBadge = badgeForStatus(item.agent.status)}
				<Card variant="outlined" padding="md">
					<div class="souls__item">
						<div class="souls__item-meta">
							<div class="souls__row">
								<Text size="sm" weight="medium">{item.agent.domain}/{item.agent.local_id}</Text>
								<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">
									{item.agent.status}
								</Badge>
							</div>
							<Text size="sm" color="secondary">
								Agent ID: <span class="souls__mono">{shortHex(item.agent.agent_id, 14, 10)}</span>
							</Text>
							<Text size="sm" color="secondary">
								Wallet: <span class="souls__mono">{shortHex(item.agent.wallet)}</span>
							</Text>
							{#if item.reputation}
								<Text size="sm" color="secondary">
									Reputation: <span class="souls__mono">{item.reputation.composite.toFixed(3)}</span>
									(block {item.reputation.block_ref ?? '—'})
								</Text>
							{:else}
								<Text size="sm" color="secondary">Reputation: —</Text>
							{/if}
						</div>
						<div class="souls__item-actions">
							<Button variant="outline" onclick={() => navigate(`/portal/souls/${item.agent.agent_id}`)}>
								Open
							</Button>
							<CopyButton size="sm" text={item.agent.agent_id} />
						</div>
					</div>
				</Card>
			{/each}
		</div>
	{/if}
</div>

<style>
	.souls {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.souls__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.souls__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.souls__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.souls__actions-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		margin-top: var(--gr-spacing-scale-3);
	}

	.souls__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.souls__list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.souls__item {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.souls__item-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.souls__item-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
	}

	.souls__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.souls__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>

