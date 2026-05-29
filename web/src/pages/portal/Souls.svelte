<!--
@component
Souls — fleet souls roster at /portal/souls (M13 re-skin).

SPDX-License-Identifier: AGPL-3.0-only
@license AGPL-3.0-only

Replaces the legacy "secondary lesser-host portal surface" framing with the
Project 42 design-spec roster: filterable table (All / Graduated / In review)
with columns for Soul, Instance, Stage, Anchor, Model, and tip-event totals,
plus a right rail showing roster-status counts and soul-minting guidance.

Data source (owner-scoped, read-only):
  - soulListPortalRoster(token) → PortalSoulRosterItem[] via
    GET /api/v1/portal/souls/roster
  - Server-side roster bridge joins lesser-host soul registry rows with
    Lesser GET /api/v1/agents/{username} metadata for model/version.

Stage derivation maps real API fields onto the design's stage vocabulary:
  graduated  = self_description_version > 0 (profile published)
  in_review  = status active but no self_description yet
  requested  = status pending
  on_hold    = status suspended or self_suspended

Documented deviations from the design fixture (see evidence MD):
  - Tips are represented by the persisted all-time tip-event count. The
    current contracts do not expose a current-month dollars aggregate, so the
    table labels the real period/source instead of fabricating a May value.
  - Simulacrum guidance card: no safe same-origin URL exists; renders
    explanatory copy with a disabled action rather than a dead route.
  - Tabs are single-filter (not multi-select) since the real data model
    maps to the design stages through a derived stage function.

Posture invariants preserved:
  - Strict-no-inline-CSP safe: no inline style attributes; styling through
    CSS classes and CSS custom properties only.
  - Multi-tenant isolation: consumes only the per-owner portal roster endpoint;
    the server enforces owner scope before resolving instance/Lesser metadata.
  - Raw instance keys never reach the browser. No soul-request flow.

Source: design fixture portal-pages-2.jsx:171–249 (PortalSouls)
Issue: equaltoai/lesser-host#547
@license AGPL-3.0-only
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { PortalSoulRosterItem, SoulAgentIdentity } from 'src/lib/api/soul';
	import { soulListPortalRoster } from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Avatar, Badge, Button, DefinitionItem, DefinitionList, Spinner, Tabs, Text } from 'src/lib/ui';
	import { PageFrame, PageTitle, Panel } from 'src/lib/shell';

	let { token } = $props<{ token: string }>();

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let souls = $state<PortalSoulRosterItem[]>([]);

	// ── Derived data ──────────────────────────────────────────────────────

	interface SoulRow {
		item: PortalSoulRosterItem;
		stage: string;
		anchorLabel: string;
		anchorTone: 'success' | 'warning' | 'error' | undefined;
	}

	function deriveStage(agent: SoulAgentIdentity): string {
		const sdv = agent.self_description_version;
		if (sdv != null && sdv > 0) return 'graduated';
		const s = (agent.status || '').toLowerCase();
		if (s === 'active') return 'in_review';
		if (s === 'pending') return 'requested';
		if (s === 'suspended' || s === 'self_suspended') return 'on_hold';
		return s || 'unknown';
	}

	function deriveAnchor(
		agent: SoulAgentIdentity,
	): { label: string; tone: 'success' | 'warning' | 'error' | undefined } {
		const state = (agent.anchor_assurance?.state || '').toLowerCase();
		if (state === 'immutable_onchain') return { label: 'fresh', tone: 'success' };
		if (state === 'hosted_offchain') return { label: 'pending', tone: 'warning' };
		// Fallback: derive from mint status
		if (agent.minted_at) return { label: 'fresh', tone: 'success' };
		return { label: 'pending', tone: 'warning' };
	}

	function stageSortOrder(stage: string): number {
		switch (stage) {
			case 'graduated':
				return 0;
			case 'in_review':
				return 1;
			case 'requested':
				return 2;
			case 'on_hold':
				return 3;
			default:
				return 4;
		}
	}

	const rows = $derived<SoulRow[]>(
		souls
			.map((item) => {
				const stage = deriveStage(item.agent);
				const anchor = deriveAnchor(item.agent);
				return { item, stage, anchorLabel: anchor.label, anchorTone: anchor.tone };
			})
			.sort((a, b) => {
				const so = stageSortOrder(a.stage) - stageSortOrder(b.stage);
				if (so !== 0) return so;
				const instanceSort = (a.item.instance.slug || '').localeCompare(b.item.instance.slug || '');
				if (instanceSort !== 0) return instanceSort;
				return a.item.agent.domain.localeCompare(b.item.agent.domain);
			}),
	);

	const stageCounts = $derived.by(() => {
		const counts: Record<string, number> = { total: rows.length };
		for (const row of rows) {
			counts[row.stage] = (counts[row.stage] || 0) + 1;
		}
		return counts;
	});

	// ── Tab filtering ─────────────────────────────────────────────────────

	let activeFilter = $state<string>('all');

	const filterTabs = $derived([
		{ id: 'all' as const, label: `All (${rows.length})` },
		{ id: 'graduated' as const, label: `Graduated (${stageCounts.graduated ?? 0})` },
		{ id: 'in_review' as const, label: `In review (${stageCounts.in_review ?? 0})` },
	]);

	function handleTabChange(tabId: string) {
		activeFilter = tabId;
	}

	const filteredRows = $derived(
		activeFilter === 'all' ? rows : rows.filter((r) => r.stage === activeFilter),
	);

	// ── Stage badge ───────────────────────────────────────────────────────

	function stageBadge(stage: string): {
		variant: 'outlined' | 'filled';
		color: 'success' | 'warning' | 'error' | 'gray';
	} {
		switch (stage) {
			case 'graduated':
				return { variant: 'filled', color: 'success' };
			case 'in_review':
				return { variant: 'outlined', color: 'warning' };
			case 'on_hold':
				return { variant: 'filled', color: 'error' };
			default:
				return { variant: 'outlined', color: 'gray' };
		}
	}

	function stageLabel(stage: string): string {
		switch (stage) {
			case 'in_review':
				return 'In review';
			case 'on_hold':
				return 'On hold';
			default:
				return stage.charAt(0).toUpperCase() + stage.slice(1);
		}
	}

	// ── Tips display ──────────────────────────────────────────────────────

	function formatTips(item: PortalSoulRosterItem): string {
		if (item.tips && typeof item.tips.received === 'number') {
			return new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 }).format(item.tips.received);
		}
		return '0';
	}

	function modelLabel(item: PortalSoulRosterItem): string {
		const version = item.lesser_agent?.agent_version?.trim();
		if (version) return version;

		const agentType = item.lesser_agent?.agent_type?.trim();
		if (agentType) return agentType;

		const status = item.lesser_agent?.status;
		if (status === 'not_found') return 'Not found';
		if (status === 'not_configured') return 'Not configured';
		return 'Unavailable';
	}

	function instanceLabel(item: PortalSoulRosterItem): string {
		return item.instance.slug || item.agent.domain;
	}

	// ── Format helpers ────────────────────────────────────────────────────

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	// ── Load ──────────────────────────────────────────────────────────────

	async function load() {
		errorMessage = null;

		loading = true;
		try {
			const res = await soulListPortalRoster(token);
			souls = res.souls ?? [];
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

<PageFrame width="wide" asideLabel="Soul roster details">
	{#snippet header()}
		<PageTitle
			eyebrow="Agents · Souls"
			title="Soul roster."
			description="Soul-bound AI agents tied to your instances. Each soul is one continuity-loop process &mdash; its anchor is refreshed continually so context survives restarts."
		>
			{#snippet actions()}
				<Button variant="solid" onclick={() => navigate('/portal/souls/register')}>
					+ Request a soul
				</Button>
			{/snippet}
		</PageTitle>
	{/snippet}

	{#snippet aside()}
		<div class="souls-rail">
			<!-- Roster status -->
			<Panel title="Roster" headerLevel={2}>
				<DefinitionList>
					<DefinitionItem label="Total">{stageCounts.total ?? 0}</DefinitionItem>
					<DefinitionItem label="Graduated">{stageCounts.graduated ?? 0}</DefinitionItem>
					<DefinitionItem label="In review">{stageCounts.in_review ?? 0}</DefinitionItem>
					<DefinitionItem label="Requested">{stageCounts.requested ?? 0}</DefinitionItem>
					<DefinitionItem label="On hold">{stageCounts.on_hold ?? 0}</DefinitionItem>
				</DefinitionList>
			</Panel>

			<!-- Soul minting guidance -->
			<Panel title="Soul minting" headerLevel={2}>
				<div class="souls-rail__minting">
					<Text size="sm" color="secondary">
						The canonical soul creation flow is designed for the Simulacrum agent workspace. This
						roster is your operator-facing view of soul-bound agents across all instances.
					</Text>
					<div class="souls-rail__minting-action">
						<Button variant="outline" size="sm" disabled={true}>
							Open Simulacrum
						</Button>
					</div>
					<Text size="xs" color="secondary">
						Navigate to your Lesser instance to access the agent workspace.
					</Text>
				</div>
			</Panel>
		</div>
	{/snippet}

	<!-- MAIN CONTENT -->
	{#if loading}
		<div class="souls__loading">
			<Spinner size="md" />
			<Text>Loading souls&hellip;</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load souls roster">{errorMessage}</Alert>
	{:else if souls.length === 0}
		<Panel title="No souls" headerLevel={2}>
			<div class="souls__empty">
				<Text size="sm" color="secondary">No soul-bound agents found for your instances.</Text>
				<div class="souls__empty-actions">
					<Button variant="solid" onclick={() => navigate('/portal/souls/register')}>
						+ Request a soul
					</Button>
				</div>
			</div>
		</Panel>
	{:else}
		<Panel title="Roster" headerLevel={2}>
			<section class="souls__tabs-nav">
				<Tabs tabs={filterTabs} activeTab={activeFilter} onTabChange={handleTabChange} variant="underline" />
			</section>

			{#if filteredRows.length === 0}
				<div class="souls__empty">
					<Text size="sm" color="secondary">No souls match the selected filter.</Text>
				</div>
			{:else}
				<div class="souls-table-wrap">
					<table class="souls-table">
						<thead>
							<tr>
								<th>Soul</th>
								<th>Instance</th>
								<th>Stage</th>
								<th>Anchor</th>
								<th>Model</th>
								<th class="souls-table__num">Tip events &middot; All time</th>
								<th></th>
							</tr>
						</thead>
						<tbody>
							{#each filteredRows as row (row.item.agent.agent_id)}
								{@const sb = stageBadge(row.stage)}
								<tr
									class="souls-table__row-clickable"
									onclick={() => navigate(`/portal/souls/${row.item.agent.agent_id}`)}
									onkeydown={(e) => {
										if (e.key === 'Enter' || e.key === ' ') {
											e.preventDefault();
											navigate(`/portal/souls/${row.item.agent.agent_id}`);
										}
									}}
									role="link"
									tabindex="0"
									aria-label={`View ${row.item.agent.local_id} details`}
								>
									<td>
										<div class="souls-table__soul">
											<Avatar
												name={row.item.agent.local_id}
												src={row.item.agent.avatar?.image}
												size="sm"
											/>
											<div class="souls-table__soul-info">
												<strong>{row.item.agent.local_id}</strong>
												<span class="souls-table__handle"
													>{row.item.agent.domain}/{row.item.agent.local_id}</span
												>
											</div>
										</div>
									</td>
									<td>
										<div class="souls-table__instance">
											<strong>{instanceLabel(row.item)}</strong>
											<span class="souls-table__handle">{row.item.instance.domain || row.item.agent.domain}</span>
										</div>
									</td>
									<td>
										<Badge variant={sb.variant} color={sb.color} size="sm">
											{stageLabel(row.stage)}
										</Badge>
									</td>
									<td>
										<Badge
											variant={row.anchorTone === 'error'
												? 'filled'
												: 'outlined'}
											color={row.anchorTone ?? 'gray'}
											size="sm"
										>
											{row.anchorLabel}
										</Badge>
									</td>
									<td class:souls-table__muted={row.item.lesser_agent?.status !== 'loaded'} class="souls-table__mono">
										{modelLabel(row.item)}
									</td>
									<td class="souls-table__mono">{formatTips(row.item)}</td>
									<td class="souls-table__chevron">
										<svg
											width="15"
											height="15"
											viewBox="0 0 15 15"
											fill="none"
											focusable="false"
											aria-hidden="true"
										>
											<path
												d="M5.5 3L10.5 7.5L5.5 12"
												stroke="var(--ds-fg-3, rgba(51, 33, 22, 0.58))"
												stroke-width="1.5"
												stroke-linecap="round"
												stroke-linejoin="round"
											/>
										</svg>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</Panel>
	{/if}
</PageFrame>

<style>
	/* ---- Loading ---- */
	.souls__loading {
		display: flex;
		gap: var(--ds-space-3);
		align-items: center;
	}

	/* ---- Empty ---- */
	.souls__empty {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
	}

	.souls__empty-actions {
		display: flex;
		gap: var(--ds-space-2);
	}

	/* ---- Tabs navigation ---- */
	.souls__tabs-nav {
		margin-bottom: var(--ds-space-4);
	}

	/* ---- Right rail ---- */
	.souls-rail {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-4);
	}

	.souls-rail__minting {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
	}

	.souls-rail__minting-action {
		display: flex;
		gap: var(--ds-space-2);
	}

	/* ---- Table ---- */
	.souls-table-wrap {
		overflow-x: auto;
	}

	.souls-table {
		width: 100%;
		border-collapse: collapse;
		font-size: 0.88rem;
	}

	.souls-table th {
		text-align: left;
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		white-space: nowrap;
	}

	.souls-table td {
		padding: 0.6rem 0.75rem;
		border-bottom: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.08));
		vertical-align: middle;
	}

	.souls-table__row-clickable {
		cursor: pointer;
	}

	.souls-table__row-clickable:hover {
		background: var(--ds-bg-raised, rgba(255, 255, 255, 0.4));
	}

	.souls-table__row-clickable:focus-visible {
		outline: 2px solid var(--ds-primary-500, #e6a645);
		outline-offset: -2px;
	}

	.souls-table__num {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		text-align: right;
	}

	.souls-table__soul {
		display: flex;
		align-items: center;
		gap: 0.7rem;
	}

	.souls-table__soul-info {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.souls-table__instance {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.souls-table__handle {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.78rem;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.souls-table__mono {
		font-family: var(--ds-font-mono, ui-monospace, monospace);
	}

	.souls-table__muted {
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.souls-table__chevron {
		width: 40px;
		text-align: right;
		vertical-align: middle;
	}

	.souls-table__chevron svg {
		display: inline-block;
		vertical-align: middle;
	}
</style>
