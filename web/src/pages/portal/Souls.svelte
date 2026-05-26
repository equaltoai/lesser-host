<!--
@component
Souls — legacy portal soul tools list (secondary surface; canonical
agent-first flow lives in the Simulacrum client served from Lesser).

M1.2 re-skin: renders on the new shell (greater-components `PageFrame` +
`PageTitle` + `Panel`) and consumes DS tokens through the existing
`--gr-*` bridge. Endpoint shape, error handling, and badge logic are
preserved byte-for-byte from the pre-M1 implementation; only the visual
layer changes.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Trust-API instance-auth untouched (soul tools are not instance-key
    surfaces).
  - Multi-tenant isolation: consumes only the per-owner `mine` agent
    endpoint; no cross-tenant reads.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.2
Issue: equaltoai/lesser-host#411
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { SoulMineAgentItem } from 'src/lib/api/soul';
	import { soulListMyAgents } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, CopyButton, Link, Spinner, Text } from 'src/lib/ui';
	import { PageFrame, PageTitle, Panel, Callout } from 'src/lib/shell';

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

	function badgeForStatus(status: string): {
		variant: 'outlined' | 'filled';
		color: 'success' | 'warning' | 'error' | 'gray';
	} {
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

<PageFrame width="default">
	{#snippet header()}
		<PageTitle
			eyebrow="Souls"
			title="Legacy soul tools"
			description="Secondary lesser-host portal surface for soul operations and inspection."
		>
			{#snippet actions()}
				<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
				<Button variant="solid" onclick={() => navigate('/portal/souls/register')}>
					Open legacy registration
				</Button>
			{/snippet}
		</PageTitle>
	{/snippet}

	<Callout tone="warning" title="Agent-first flow lives in Simulacrum">
		Use the Simulacrum agent workspace on your Lesser instance for the canonical request, review,
		approval, and finalize experience. Keep this portal surface for fallback, recovery, or
		operator-guided work.
	</Callout>

	{#if loading}
		<div class="souls__loading">
			<Spinner size="md" />
			<Text>Loading agents…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load /api/v1/soul/agents/mine">{errorMessage}</Alert>
	{:else if agents.length === 0}
		<Panel title="No agents" headerLevel={2}>
			<Text size="sm" color="secondary">No legacy portal agents found yet.</Text>
			<div class="souls__actions-inline">
				<Button variant="solid" onclick={() => navigate('/portal/souls/register')}>
					Open legacy registration
				</Button>
			</div>
		</Panel>
	{:else}
		<Panel title="My agents" headerLevel={2}>
			<ul class="souls__list">
				{#each agents as item (item.agent.agent_id)}
					{@const statusBadge = badgeForStatus(item.agent.status)}
					{@const needsProfile = !item.agent.self_description_version}
					<li class="souls__item">
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
							{#if needsProfile}
								<Text size="sm" color="secondary">Profile status: setup not finished</Text>
							{:else}
								<Text size="sm" color="secondary">
									Profile status: published v{item.agent.self_description_version}
								</Text>
							{/if}
							{#if item.reputation}
								<Text size="sm" color="secondary">
									Reputation:
									<span class="souls__mono">{item.reputation.composite.toFixed(3)}</span>
									(block {item.reputation.block_ref ?? '—'})
								</Text>
							{:else}
								<Text size="sm" color="secondary">Reputation: —</Text>
							{/if}
						</div>
						<div class="souls__item-actions">
							{#if needsProfile}
								<Button
									variant="solid"
									onclick={() => navigate(`/portal/souls/${item.agent.agent_id}/mint`)}
								>
									Open legacy profile step
								</Button>
							{/if}
							<Link {...linkProps(`/portal/souls/${item.agent.agent_id}`)} variant="default">
								Open
							</Link>
							<CopyButton size="sm" text={item.agent.agent_id} />
						</div>
					</li>
				{/each}
			</ul>
		</Panel>
	{/if}
</PageFrame>

<style>
	.souls__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
	}

	.souls__list {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
		list-style: none;
		padding: 0;
		margin: 0;
	}

	.souls__item {
		display: flex;
		gap: var(--ds-space-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
		padding: var(--ds-space-3);
		border: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		border-radius: var(--ds-radius-md, var(--gr-radius-md));
		background: var(--ds-bg-raised, var(--gr-color-surface));
	}

	.souls__item-meta {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1);
	}

	.souls__item-actions {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.souls__actions-inline {
		display: flex;
		gap: var(--ds-space-2);
		margin-top: var(--ds-space-3);
	}

	.souls__row {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.souls__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}
</style>
