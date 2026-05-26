<!--
@component
InstanceSouls — Souls tab content for the Instance Detail shell.

M1.10 — Project 39 web UI rework. Lists the soul agents bound to a
specific managed instance by client-side filtering the per-owner
`soulListMyAgents` response on `agent.domain == instance.hosted_base_domain`.
The portal already exposes the user's full agent list and the
per-instance summary via existing endpoints; this view composes them
to present an instance-scoped projection.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Multi-tenant isolation: both source endpoints (`/api/v1/soul/agents/mine`
    and `/api/v1/portal/instances/{slug}`) enforce per-owner / per-slug
    ownership server-side; filtering happens client-side over data the
    user is already authorized to see.
  - Trust-API instance-auth untouched (this is a customer-side soul
    listing; no instance-key path is touched). SEC-9 change-lock not
    engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.10
Issue: equaltoai/lesser-host#419
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalGetInstance } from 'src/lib/api/portalInstances';
	import type { SoulMineAgentItem } from 'src/lib/api/soul';
	import { soulListMyAgents } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import { Alert, Badge, Button, CopyButton, Link, Spinner, Text } from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';

	interface Props {
		token: string;
		slug: string;
	}

	let { token, slug }: Props = $props();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let instance = $state<InstanceResponse | null>(null);
	let allAgents = $state<SoulMineAgentItem[]>([]);

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
		instance = null;
		allAgents = [];

		loading = true;
		try {
			const [inst, agents] = await Promise.all([
				portalGetInstance(token, slug),
				soulListMyAgents(token),
			]);
			instance = inst;
			allAgents = agents.agents ?? [];
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

	const boundAgents = $derived.by<SoulMineAgentItem[]>(() => {
		if (!instance) return [];
		const targetDomain = (instance.hosted_base_domain || '').trim().toLowerCase();
		if (!targetDomain) return [];
		return allAgents.filter(
			(item) => (item.agent.domain || '').trim().toLowerCase() === targetDomain,
		);
	});

	onMount(() => {
		void load();
	});
</script>

<Panel title="Souls bound to this instance" headerLevel={2}>
	{#snippet actions()}
		<Button variant="outline" onclick={() => void load()} disabled={loading}>Refresh</Button>
	{/snippet}

	<Text size="sm" color="secondary">
		Soul agents whose domain matches this instance's managed base domain. The canonical agent-first
		soul lifecycle (request, review, approval, finalize) lives in the Simulacrum agent workspace
		served from the instance.
	</Text>

	{#if loading && boundAgents.length === 0}
		<div class="instance-souls__loading">
			<Spinner size="md" />
			<Text>Loading souls…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load souls">{errorMessage}</Alert>
	{:else if !instance?.hosted_base_domain}
		<Alert variant="info" title="Instance not provisioned">
			<Text size="sm">
				This instance has no managed base domain yet. Souls bind to an instance once provisioning
				completes and the managed domain is delegated.
			</Text>
		</Alert>
	{:else if boundAgents.length === 0}
		<Alert variant="info" title="No souls bound to this instance">
			<Text size="sm">
				You have no soul agents registered under
				<span class="instance-souls__mono">{instance.hosted_base_domain}</span>
				yet. Use the Simulacrum agent workspace on the instance to request and finalize a new
				agent.
			</Text>
			<div class="instance-souls__actions-inline">
				<Link {...linkProps('/portal/souls')} variant="default">
					Open legacy portal souls list
				</Link>
			</div>
		</Alert>
	{:else}
		<ul class="instance-souls__list">
			{#each boundAgents as item (item.agent.agent_id)}
				{@const statusBadge = badgeForStatus(item.agent.status)}
				{@const needsProfile = !item.agent.self_description_version}
				<li class="instance-souls__item">
					<div class="instance-souls__item-meta">
						<div class="instance-souls__row">
							<Text size="sm" weight="medium">
								{item.agent.domain}/{item.agent.local_id}
							</Text>
							<Badge variant={statusBadge.variant} color={statusBadge.color} size="sm">
								{item.agent.status}
							</Badge>
						</div>
						<Text size="sm" color="secondary">
							Agent ID:
							<span class="instance-souls__mono">{shortHex(item.agent.agent_id, 14, 10)}</span>
						</Text>
						<Text size="sm" color="secondary">
							Wallet: <span class="instance-souls__mono">{shortHex(item.agent.wallet)}</span>
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
								<span class="instance-souls__mono">{item.reputation.composite.toFixed(3)}</span>
								(block {item.reputation.block_ref ?? '—'})
							</Text>
						{/if}
					</div>
					<div class="instance-souls__item-actions">
						<Button
							variant="outline"
							onclick={() => navigate(`/portal/souls/${item.agent.agent_id}`)}
						>
							Open
						</Button>
						<CopyButton size="sm" text={item.agent.agent_id} />
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</Panel>

<style>
	.instance-souls__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
	}

	.instance-souls__list {
		list-style: none;
		padding: 0;
		margin: var(--ds-space-3) 0 0 0;
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
	}

	.instance-souls__item {
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

	.instance-souls__item-meta {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1);
		min-width: min(360px, 100%);
	}

	.instance-souls__item-actions {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.instance-souls__row {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.instance-souls__actions-inline {
		display: flex;
		gap: var(--ds-space-2);
		margin-top: var(--ds-space-3);
	}

	.instance-souls__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}
</style>
