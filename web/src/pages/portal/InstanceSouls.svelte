<!--
@component
InstanceSouls — Souls tab content for the Instance Detail shell.

Project 42 M10 (#544): re-skins the Souls tab as a design-aligned table
with a "+ Request soul" CTA. The data-fetch correctness fix (M0.5 domain
matching by managed_lesser_domain) is preserved.

Columns: Handle (avatar + local_id + domain), Stage (badge), Model,
Anchor freshness, Tips MTD. Row click navigates to /portal/souls/{agent_id}.

Data-availability notes:
  - Model is not present on SoulAgentIdentity; rendered as "—".
  - Anchor freshness derived from anchor_assurance.state; absent → "—".
  - Tips MTD is rendered from reputation.tips_received (total tips, not MTD);
    this deviation is documented in the evidence MD.
  - The "+ Request soul" CTA is disabled (coming soon) because the
    soul-request workflow (distinct from soul registration) does not exist.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Multi-tenant isolation: both source endpoints enforce per-owner /
    per-slug ownership server-side.
  - Trust-API instance-auth untouched.

Source: project-40-portal-redesign-recovery/milestones/11-m10-instance-souls-ui.md
Issue: equaltoai/lesser-host#544
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
	import { Alert, Avatar, Badge, Button, Link, Spinner, Text } from 'src/lib/ui';
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

	// ── Stage badge mapping ─────────────────────────────────────────────

	interface StageBadgeStyle {
		variant: 'outlined' | 'filled';
		color: 'success' | 'warning' | 'error' | 'info' | 'gray';
		label: string;
	}

	function badgeForStage(lifecycleStatus?: string, status?: string): StageBadgeStyle {
		const s = (lifecycleStatus || status || '').toLowerCase();
		if (s === 'graduated') return { variant: 'filled', color: 'success', label: 'graduated' };
		if (s === 'in_review') return { variant: 'outlined', color: 'info', label: 'in review' };
		if (s === 'requested') return { variant: 'outlined', color: 'gray', label: 'requested' };
		if (s === 'on_hold') return { variant: 'filled', color: 'warning', label: 'on hold' };
		if (s === 'active') return { variant: 'filled', color: 'success', label: 'active' };
		if (s === 'pending') return { variant: 'outlined', color: 'warning', label: 'pending' };
		if (s === 'suspended') return { variant: 'filled', color: 'error', label: 'suspended' };
		return { variant: 'outlined', color: 'gray', label: s || '—' };
	}

	// ── Anchor freshness ────────────────────────────────────────────────

	interface AnchorDisplay {
		label: string;
		dot: boolean;
		color: 'success' | 'warning' | 'error' | 'gray';
	}

	function anchorDisplay(item: SoulMineAgentItem): AnchorDisplay {
		const state = item.agent.anchor_assurance?.state;
		if (state === 'immutable_onchain') return { label: 'fresh', dot: true, color: 'success' };
		if (state === 'hosted_offchain') return { label: 'pending', dot: true, color: 'warning' };
		// No anchor_assurance at all
		return { label: '—', dot: false, color: 'gray' };
	}

	// ── Tips display ────────────────────────────────────────────────────

	function tipsDisplay(item: SoulMineAgentItem): string {
		const tips = item.reputation?.tips_received;
		if (tips == null) return '—';
		return `$${tips.toFixed(2)}`;
	}

	// ── Date formatting ─────────────────────────────────────────────────

	function formatDate(iso: string | undefined): string {
		if (!iso) return '—';
		if (iso.startsWith('0001-01-01T00:00:00')) return '—';
		try {
			const d = new Date(iso);
			if (isNaN(d.getTime())) return '—';
			return d.toLocaleDateString('en-US', {
				month: 'short',
				day: 'numeric',
				year: 'numeric',
			});
		} catch {
			return '—';
		}
	}

	// ── Data loading ────────────────────────────────────────────────────

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

	// M0.5 domain-matching fix: compare agent.domain against both
	// hosted_base_domain and managed_lesser_domain (stage-prefixed) so
	// non-live stage instances match correctly. Preserved from the pre-M10
	// implementation — this is a data-fetch correctness fix, not layout.
	const boundAgents = $derived.by<SoulMineAgentItem[]>(() => {
		if (!instance) return [];
		const candidateDomains: Record<string, true> = {};
		const baseDomain = (instance.hosted_base_domain || '').trim().toLowerCase();
		const managedDomain = (instance.managed_lesser_domain || '').trim().toLowerCase();
		if (baseDomain) candidateDomains[baseDomain] = true;
		if (managedDomain) candidateDomains[managedDomain] = true;
		const candidateCount = Object.keys(candidateDomains).length;
		if (candidateCount === 0) return [];
		return allAgents.filter((item) => {
			const domain = (item.agent.domain || '').trim().toLowerCase();
			return Boolean(candidateDomains[domain]);
		});
	});

	onMount(() => {
		void load();
	});
</script>

<Panel
	title="Souls on this instance"
	headerLevel={2}
	padding="none"
>
	{#snippet actions()}
		<div class="instance-souls__header-actions">
			<Button variant="solid" size="sm" disabled title="Soul request workflow is coming soon. Until then, use the Simulacrum agent workspace on the instance to request and finalize a new agent.">
				+ Request soul
			</Button>
			<Button variant="ghost" size="sm" onclick={() => void load()} disabled={loading}>Refresh</Button>
		</div>
	{/snippet}

	{#if loading && boundAgents.length === 0}
		<div class="instance-souls__loading">
			<Spinner size="md" />
			<Text>Loading souls…</Text>
		</div>
	{:else if errorMessage}
		<div class="instance-souls__padded">
			<Alert variant="error" title="Failed to load souls">{errorMessage}</Alert>
		</div>
	{:else if !instance?.hosted_base_domain}
		<div class="instance-souls__padded">
			<Alert variant="info" title="Instance not provisioned">
				<Text size="sm">
					This instance has no managed base domain yet. Souls bind to an instance once provisioning
					completes and the managed domain is delegated.
				</Text>
			</Alert>
		</div>
	{:else if boundAgents.length === 0}
		<div class="instance-souls__padded">
			<Alert variant="info" title="No souls bound to this instance">
				<Text size="sm">
					You have no soul agents registered under
					<span class="instance-souls__mono">{instance.hosted_base_domain}</span>
					yet. Use the Simulacrum agent workspace on the instance to request and finalize a new
					agent.
				</Text>
				<div class="instance-souls__actions-inline">
					<Link {...linkProps('/portal/souls')} variant="default">
						View all souls
					</Link>
				</div>
			</Alert>
		</div>
	{:else}
		<!-- Design-fidelity: tabular layout per the M10 design fixture (portal-pages.jsx:484-506). -->
		<div class="instance-souls__eyebrow">
			<Text size="xs" color="secondary">
				<span class="instance-souls__eyebrow-text">{boundAgents.length} bound</span>
			</Text>
		</div>
		<table class="instance-souls__table">
			<thead>
				<tr>
					<th>Handle</th>
					<th>Stage</th>
					<th>Model</th>
					<th>Anchor</th>
					<th>Tips (MTD)</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each boundAgents as item (item.agent.agent_id)}
					{@const soulPath = `/portal/souls/${item.agent.agent_id}`}
					{@const stageStyle = badgeForStage(item.agent.lifecycle_status, item.agent.status)}
					{@const anchor = anchorDisplay(item)}
					<tr
						class="instance-souls__row"
						onclick={() => navigate(soulPath)}
						onkeydown={(e) => {
							if (e.key === 'Enter' || e.key === ' ') {
								e.preventDefault();
								navigate(soulPath);
							}
						}}
						tabindex="0"
						role="link"
						aria-label="Open soul {item.agent.local_id || item.agent.agent_id}"
					>
						<td class="instance-souls__cell-handle">
							<div class="instance-souls__handle-row">
								<Avatar
									src={item.agent.avatar?.image}
									name={item.agent.local_id}
									size="sm"
									shape="circle"
								/>
								<div class="instance-souls__handle-text">
									<Text size="sm" weight="semibold">
										{item.agent.local_id || item.agent.agent_id}
									</Text>
									<Text size="xs" color="tertiary">
										<span class="instance-souls__mono">{item.agent.domain}</span>
									</Text>
								</div>
							</div>
						</td>
						<td class="instance-souls__cell-stage">
							<Badge variant={stageStyle.variant} color={stageStyle.color} size="sm">
								{stageStyle.label}
							</Badge>
						</td>
						<td class="instance-souls__cell-model">
							<Text size="sm" color="tertiary">
								<span class="instance-souls__mono">—</span>
							</Text>
						</td>
						<td class="instance-souls__cell-anchor">
							{#if anchor.label === '—'}
								<Text size="sm" color="tertiary">—</Text>
							{:else}
								<Badge variant="dot" color={anchor.color} size="sm">
									{anchor.label}
								</Badge>
							{/if}
						</td>
						<td class="instance-souls__cell-tips">
							<Text size="sm">
								<span class="instance-souls__mono">{tipsDisplay(item)}</span>
							</Text>
						</td>
						<td class="instance-souls__cell-chevron">
							<Link {...linkProps(soulPath)} variant="ghost" aria-label="Open soul {item.agent.local_id || item.agent.agent_id}">
								<svg width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true">
									<path d="M5.5 2.5L10.5 7.5L5.5 12.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
								</svg>
							</Link>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{/if}
</Panel>

<style>
	.instance-souls__header-actions {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
	}

	.instance-souls__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
		padding: var(--ds-space-4);
	}

	.instance-souls__padded {
		padding: var(--ds-space-4);
	}

	.instance-souls__eyebrow-text {
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.instance-souls__eyebrow {
		padding: var(--ds-space-3) var(--ds-space-4) 0 var(--ds-space-4);
	}

	.instance-souls__table {
		width: 100%;
		border-collapse: collapse;
		margin-top: var(--ds-space-2);
	}

	.instance-souls__table th {
		text-align: left;
		padding: var(--ds-space-2) var(--ds-space-4);
		font-size: var(--ds-font-size-xs, 0.75rem);
		font-weight: var(--ds-weight-semibold, 600);
		color: var(--ds-fg-2, var(--gr-color-foreground-secondary));
		text-transform: uppercase;
		letter-spacing: 0.05em;
		border-bottom: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
	}

	.instance-souls__row {
		border-bottom: 1px solid var(--ds-border-subtle, var(--gr-color-border-subtle));
		cursor: pointer;
		transition: background var(--ds-duration-fast, 140ms) var(--ds-ease-standard, ease);
		outline: none;
	}

	.instance-souls__row:hover {
		background: var(--ds-bg-hover, var(--gr-color-surface-hover));
	}

	.instance-souls__row:focus-visible {
		outline: 2px solid var(--ds-border-focus, var(--gr-color-primary));
		outline-offset: -2px;
	}

	.instance-souls__table td {
		padding: var(--ds-space-3) var(--ds-space-4);
		vertical-align: middle;
	}

	.instance-souls__cell-handle {
		min-width: 200px;
	}

	.instance-souls__cell-model,
	.instance-souls__cell-tips {
		white-space: nowrap;
	}

	.instance-souls__cell-chevron {
		width: 48px;
		text-align: center;
		padding: var(--ds-space-3) var(--ds-space-2);
		color: var(--ds-fg-3, var(--gr-color-foreground-tertiary));
	}

	.instance-souls__handle-row {
		display: flex;
		gap: 0.65rem;
		align-items: center;
	}

	.instance-souls__handle-text {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.instance-souls__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}

	.instance-souls__actions-inline {
		display: flex;
		gap: var(--ds-space-2);
		margin-top: var(--ds-space-3);
	}
</style>
