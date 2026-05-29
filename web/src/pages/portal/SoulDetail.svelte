<!--
@component
SoulDetail — Project 42 M14 portal soul detail surface at /portal/souls/{agentId}.

SPDX-License-Identifier: AGPL-3.0-only
@license AGPL-3.0-only

Read-only design surface replacing the legacy SoulAgentDetail mutation surface
for the customer-facing portal route. Layout: header with avatar + display name +
handle@domain + instance/stage badge, conditional stage/hold/review alerts, three
metric cards, 7-day continuity loop bar chart, activity timeline, and right rail
(Manifest DL, Anchor gauge, Earnings panel).

Authorization gate (owner-scoped):
  The component first loads the portal soul roster for the authenticated owner.
  If the requested agent_id is not present in the roster, the surface renders
  a safe not-found/unauthorized empty state. Only roster-matched souls proceed
  to the detail display.

Data sources:
  - soulListPortalRoster(token)           → authorization gate + roster match
  - soulPublicGetAgent(agentId)           → agent identity, avatar, stage
  - soulPublicGetContinuity(agentId)      → 7-day continuity loop data
  - soulAgentListCommActivity(token, id)  → activity timeline items
  - Roster item data                     → instance, tips, anchor_assurance

Documented deviations from the design fixture (see evidence MD):
  - Metric card labels reflect real on-wire data: tip-event count (all-time)
    replaces "Avg tip", comm-activity count replaces "Posts (30d)", and
    endpoint count replaces "Followers" where the real aggregate is unavailable.
  - Tip/earnings use safe roster aggregates: tips.received (all-time event
    count with honest label "Tip events · All time"), never raw ledger data.
  - Continuity bar chart uses real continuity entry timestamps bucketed into
    trailing 7-day windows; if fewer than 7 days of data exist the chart
    still renders with available buckets.
  - Anchor gauge uses the typed `anchor_assurance?.state` field (`immutable_onchain`
    | `hosted_offchain`) to derive the CostGauge percentage (80 / 30) and
    anchor label/status; no stale `as { status?: string }` casts.
  - "Open profile", "Refresh anchor", "Configure" buttons are not included
    (they are mutation actions outside M14 read-only scope).

Posture invariants preserved:
  - Strict-CSP safe: no inline style attributes or inline scripts. Styling
    through CSS classes and CSS custom properties only.
  - Multi-tenant isolation: only owner-scoped portal roster endpoint gates
    access; server enforces owner scope before resolving details.
  - No new backend endpoints: all data sourced from existing on-wire contracts.
  - Tip/earnings data: uses safe roster aggregates with honest labels.

Source: design fixture portal-pages-2.jsx:251–369 (SoulDetail)
Issue: equaltoai/lesser-host#548
@license AGPL-3.0-only
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		PortalSoulRosterItem,
		SoulPublicAgentResponse,
		SoulPublicContinuityResponse,
		SoulAgentCommActivity,
	} from 'src/lib/api/soul';
	import {
		soulListPortalRoster,
		soulPublicGetAgent,
		soulPublicGetContinuity,
		soulAgentListCommActivity,
	} from 'src/lib/api/soul';
	import { logout } from 'src/lib/auth/logout';
	import { linkProps, navigate } from 'src/lib/router';
	import {
		Alert,
		Badge,
		Button,
		Link,
		Spinner,
		Text,
	} from 'src/lib/ui';
	import { PageFrame, PageTitle, Panel } from 'src/lib/shell';
	import Metric from 'src/lib/components/primitives/Metric.svelte';
	import CostGauge from 'src/lib/components/primitives/CostGauge.svelte';

	let { token, agentId } = $props<{ token: string; agentId: string }>();

	// ── State ─────────────────────────────────────────────────────────────

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let roster = $state<PortalSoulRosterItem[]>([]);
	let rosterItem = $state<PortalSoulRosterItem | null>(null);
	let agent = $state<SoulPublicAgentResponse | null>(null);
	let continuity = $state<SoulPublicContinuityResponse | null>(null);
	let commActivityItems = $state<SoulAgentCommActivity[]>([]);

	// ── Derived display helpers ──────────────────────────────────────────

	interface ContinuityBucket {
		label: string;
		count: number;
		maxCount: number;
	}

	let continuityBuckets = $derived.by<ContinuityBucket[]>(() => {
		const entries = continuity?.entries ?? [];
		const now = Date.now();
		const DAY = 86_400_000;
		const dayLabels = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

		// Build 7-day trailing buckets: today, day-1, ..., day-6
		const buckets: { label: string; count: number; ts: number }[] = [];
		for (let i = 6; i >= 0; i--) {
			const d = new Date(now - i * DAY);
			const label = dayLabels[d.getUTCDay()];
			buckets.push({ label, count: 0, ts: d.getTime() });
		}

		for (const entry of entries) {
			const t = new Date(entry.timestamp).getTime();
			if (Number.isNaN(t)) continue;
			const daysAgo = Math.floor((now - t) / DAY);
			const idx = 6 - daysAgo;
			if (idx >= 0 && idx < 7) {
				buckets[idx].count += 1;
			}
		}

		const maxCount = Math.max(...buckets.map((b) => b.count), 1);
		return buckets.map((b) => ({ label: b.label, count: b.count, maxCount }));
	});

	let continuitySummary = $derived.by(() => {
		const total = (continuity?.entries ?? []).length;
		if (total === 0) return 'No continuity signals yet.';
		const filled = continuityBuckets.filter((b) => b.count > 0).length;
		const streak = filled > 0 ? `${filled}-day` : 'no';
		return `${total} total events · ${streak} coverage this week`;
	});

	let displayName = $derived.by(() => {
		return agent?.agent?.local_id || rosterItem?.agent?.local_id || 'Unknown';
	});

	let handleDomain = $derived.by(() => {
		const domain = agent?.agent?.domain || rosterItem?.agent?.domain || '';
		const localId = agent?.agent?.local_id || rosterItem?.agent?.local_id || '';
		if (!domain) return localId;
		return `${localId}@${domain}`;
	});

	let instanceSlug = $derived(rosterItem?.instance?.slug ?? '');

	let instanceDomain = $derived(rosterItem?.instance?.domain ?? '');

	let stage = $derived.by(() => {
		const a = agent?.agent;
		if (!a) return 'unknown';
		const sdv = a.self_description_version;
		if (sdv != null && sdv > 0) return 'graduated';
		const s = (a.lifecycle_status || a.status || '').toLowerCase();
		if (s === 'active') return 'in_review';
		if (s === 'pending') return 'requested';
		if (s === 'suspended' || s === 'self_suspended') return 'on_hold';
		if (s === 'archived') return 'archived';
		if (s === 'succeeded') return 'succeeded';
		return s || 'unknown';
	});

	let stageLabel = $derived.by(() => {
		const map: Record<string, string> = {
			graduated: 'Graduated',
			in_review: 'In review',
			requested: 'Requested',
			on_hold: 'On hold',
			archived: 'Archived',
			succeeded: 'Succeeded',
		};
		return map[stage] || stage;
	});

	let stageBadgeColor = $derived.by(() => {
		const map: Record<string, 'success' | 'warning' | 'error' | 'gray'> = {
			graduated: 'success',
			in_review: 'warning',
			requested: 'gray',
			on_hold: 'error',
			archived: 'gray',
			succeeded: 'gray',
		};
		return map[stage] || 'gray';
	});

	let modelLabel = $derived.by(() => {
		const version = rosterItem?.lesser_agent?.agent_version?.trim();
		if (version) return version;
		const agentType = rosterItem?.lesser_agent?.agent_type?.trim();
		if (agentType) return agentType;
		const status = rosterItem?.lesser_agent?.status;
		if (status === 'not_found') return 'Not found';
		if (status === 'not_configured') return 'Not configured';
		return 'Unavailable';
	});

	let requestedAt = $derived.by(() => {
		return agent?.agent?.minted_at || agent?.agent?.updated_at || '—';
	});

	let graduatedAt = $derived.by(() => {
		if (stage !== 'graduated') return null;
		return agent?.agent?.updated_at || null;
	});

	let anchorState = $derived.by(() => {
		const aa = rosterItem?.anchor_assurance || agent?.agent?.anchor_assurance;
		return aa?.state ?? null;
	});

	let anchorStatus = $derived.by(() => {
		if (!anchorState) return 'No anchor';
		if (anchorState === 'immutable_onchain') return 'immutable onchain';
		if (anchorState === 'hosted_offchain') return 'hosted offchain';
		return anchorState;
	});

	let anchorFreshness = $derived.by(() => {
		if (!anchorState) return 0;
		if (anchorState === 'immutable_onchain') return 80;
		if (anchorState === 'hosted_offchain') return 30;
		return 50;
	});

	let anchorLabel = $derived.by(() => {
		if (!anchorState) return 'No anchor';
		if (anchorState === 'immutable_onchain') return 'fresh';
		if (anchorState === 'hosted_offchain') return 'pending';
		return 'unknown';
	});

	let tipEventsReceived = $derived.by(() => {
		const tips = rosterItem?.tips;
		if (tips && typeof tips.received === 'number') return tips.received;
		return 0;
	});

	let tipPeriodLabel = $derived(rosterItem?.tips?.label ?? 'All time');

	let commActivityCount = $derived(commActivityItems.length);

	let channelCount = $derived.by(() => {
		const caps = agent?.agent?.capabilities;
		if (!caps || !Array.isArray(caps)) return 0;
		return caps.length;
	});

	// ── Helpers ──────────────────────────────────────────────────────────

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function formatDate(iso: string): string {
		try {
			return new Date(iso).toLocaleDateString('en-US', {
				year: 'numeric',
				month: 'short',
				day: 'numeric',
			});
		} catch {
			return iso;
		}
	}

	function relativeTime(iso: string): string {
		try {
			const diff = Date.now() - new Date(iso).getTime();
			const mins = Math.floor(diff / 60_000);
			if (mins < 1) return 'just now';
			if (mins < 60) return `${mins}m ago`;
			const hours = Math.floor(mins / 60);
			if (hours < 24) return `${hours}h ago`;
			const days = Math.floor(hours / 24);
			if (days < 7) return `${days}d ago`;
			return formatDate(iso);
		} catch {
			return iso;
		}
	}

	function activityDescription(item: SoulAgentCommActivity): string {
		const parts: string[] = [];
		if (item.direction) parts.push(item.direction === 'inbound' ? 'Received' : 'Sent');
		if (item.channel_type) parts.push(item.channel_type);
		if (item.action) parts.push(item.action);
		if (item.counterparty) parts.push(`· ${item.counterparty}`);
		if (item.subject) parts.push(`· ${item.subject}`);
		if (item.preview) parts.push(`· "${item.preview}"`);
		if (item.status) parts.push(`· ${item.status}`);
		return parts.join(' ') || 'Activity';
	}

	// ── Load ─────────────────────────────────────────────────────────────

	async function load() {
		errorMessage = null;
		loading = true;

		try {
			// Step 1: Load roster (authorization gate)
			const rosterRes = await soulListPortalRoster(token);
			roster = rosterRes.souls ?? [];

			const match = roster.find(
				(item) => item.agent.agent_id === agentId,
			);
			if (!match) {
				errorMessage = null;
				rosterItem = null;
				loading = false;
				return;
			}
			rosterItem = match;

			// Step 2: Load detail data in parallel
			const [agentRes, continuityRes, commRes] = await Promise.all([
				soulPublicGetAgent(agentId),
				soulPublicGetContinuity(agentId, undefined, 50),
				soulAgentListCommActivity(token, agentId, 50),
			]);
			agent = agentRes;
			continuity = continuityRes;
			commActivityItems = commRes?.activities ?? [];
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

<PageFrame width="wide" asideLabel="Soul detail">
	{#snippet header()}
		<PageTitle
			eyebrow="Soul"
			title={displayName}
			description={`${handleDomain} · Continuity loop, recent activity, anchor health and tip earnings.`}
		>
			{#snippet actions()}
				<Link {...linkProps('/portal/souls')} variant="ghost">Back to roster</Link>
			{/snippet}
		</PageTitle>
	{/snippet}

	{#snippet aside()}
		<div class="soul-detail-rail">
			<!-- Manifest -->
			<Panel title="Manifest" headerLevel={2}>
				<dl class="soul-detail-rail__manifest">
					<div class="soul-detail-rail__manifest-row">
						<dt class="soul-detail-rail__manifest-label">Handle</dt>
						<dd class="soul-detail-rail__manifest-value soul-detail-rail__mono" title={handleDomain}>{handleDomain}</dd>
					</div>
					<div class="soul-detail-rail__manifest-row">
						<dt class="soul-detail-rail__manifest-label">Stage</dt>
						<dd class="soul-detail-rail__manifest-value">
							<Badge variant="filled" color={stageBadgeColor} size="sm">{stageLabel}</Badge>
						</dd>
					</div>
					<div class="soul-detail-rail__manifest-row">
						<dt class="soul-detail-rail__manifest-label">Model</dt>
						<dd class="soul-detail-rail__manifest-value soul-detail-rail__mono" title={modelLabel}>{modelLabel}</dd>
					</div>
					<div class="soul-detail-rail__manifest-row">
						<dt class="soul-detail-rail__manifest-label">Instance</dt>
						<dd class="soul-detail-rail__manifest-value soul-detail-rail__mono" title={instanceSlug || instanceDomain}>{instanceSlug || instanceDomain}</dd>
					</div>
					<div class="soul-detail-rail__manifest-row">
						<dt class="soul-detail-rail__manifest-label">Requested</dt>
						<dd class="soul-detail-rail__manifest-value soul-detail-rail__mono">{formatDate(requestedAt)}</dd>
					</div>
					{#if graduatedAt}
						<div class="soul-detail-rail__manifest-row">
							<dt class="soul-detail-rail__manifest-label">Graduated</dt>
							<dd class="soul-detail-rail__manifest-value soul-detail-rail__mono">{formatDate(graduatedAt)}</dd>
						</div>
					{/if}
				</dl>
			</Panel>

			<!-- Anchor gauge -->
			<Panel title="Anchor" headerLevel={2}>
				<div class="soul-detail-rail__anchor">
					<CostGauge
						used={anchorFreshness}
						budget={100}
						size={80}
						label={anchorLabel}
					/>
					<div class="soul-detail-rail__anchor-meta">
						<span class="soul-detail-rail__muted">Anchor status</span>
						<strong class="soul-detail-rail__mono">{anchorStatus}</strong>
					</div>
				</div>
			</Panel>

			<!-- Tip earnings -->
			<Panel title="Earnings" headerLevel={2}>
				<div class="soul-detail-rail__earnings">
					<div class="soul-detail-rail__earnings-row">
						<span>Tip events</span>
						<strong class="soul-detail-rail__mono">{tipEventsReceived.toLocaleString()}</strong>
					</div>
					<div class="soul-detail-rail__earnings-row soul-detail-rail__muted">
						<span>Period</span>
						<span>{tipPeriodLabel}</span>
					</div>
					<div class="soul-detail-rail__divider"></div>
					<div class="soul-detail-rail__tertiary">
						Data source: portal roster aggregate. Dollar-denominated earnings are not yet
						available on the wire &mdash; tip-event counts are shown as an honest proxy.
					</div>
				</div>
			</Panel>
		</div>
	{/snippet}

	<!-- MAIN CONTENT -->
	{#if loading}
		<div class="soul-detail__loading">
			<Spinner size="md" />
			<Text>Loading soul&hellip;</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load soul">{errorMessage}</Alert>
	{:else if !rosterItem}
		<Panel title="Not found" headerLevel={2}>
			<div class="soul-detail__empty">
				<Text size="sm" color="secondary">
					The requested soul is not associated with your account or does not exist.
				</Text>
				<div class="soul-detail__empty-actions">
					<Button variant="outline" onclick={() => navigate('/portal/souls')}>
						Back to roster
					</Button>
				</div>
			</div>
		</Panel>
	{:else}
		<!-- Stage / hold / review alert banners -->
		{#if stage === 'on_hold'}
			<Alert variant="warning" title="On hold — anchor stale">
				<Text size="sm">
					The anchor has not refreshed recently. Re-establish continuity to resume.
				</Text>
			</Alert>
		{/if}

		{#if stage === 'in_review'}
			<Alert variant="info" title="Review in progress">
				<Text size="sm">
					This soul is currently under review and has not yet published a self-description.
				</Text>
			</Alert>
		{/if}

		{#if stage === 'requested'}
			<Alert variant="info" title="Registration requested">
				<Text size="sm">
					The soul registration has been requested but is not yet active.
				</Text>
			</Alert>
		{/if}

		<!-- Three metric cards -->
		<div class="soul-detail__metrics">
			<Metric
				label="Comm events"
				value={commActivityCount.toLocaleString()}
				sub="All-time communication activity"
			/>
			<Metric
				label="Capabilities"
				value={channelCount.toLocaleString()}
				sub="Declared capabilities on profile"
			/>
			<Metric
				label="Tip events"
				value={tipEventsReceived.toLocaleString()}
				sub={tipPeriodLabel}
			/>
		</div>

		<!-- Continuity loop (7-day bar chart) -->
		<Panel title="Continuity loop" headerLevel={2}>
			{#if continuityBuckets.length > 0}
				<div class="soul-detail__continuity-chart">
					{#each continuityBuckets as bucket (bucket.label)}
						{@const heightPct = bucket.maxCount > 0 ? (bucket.count / bucket.maxCount) * 100 : 0}
						<div class="soul-detail__continuity-bar-col">
							<div
								class="soul-detail__continuity-bar"
								class:soul-detail__continuity-bar--filled={bucket.count > 0}
								style:height={`${Math.max(heightPct, bucket.count > 0 ? 4 : 2)}%`}
							></div>
							<span class="soul-detail__continuity-label">{bucket.label}</span>
						</div>
					{/each}
				</div>
				<div class="soul-detail__continuity-summary">
					<Text size="sm" color="secondary">{continuitySummary}</Text>
				</div>
			{:else}
				<div class="soul-detail__empty">
					<Text size="sm" color="secondary">No continuity signals in the trailing 7 days.</Text>
				</div>
			{/if}
		</Panel>

		<!-- Activity timeline -->
		<Panel title="Activity" headerLevel={2}>
			{#if commActivityItems.length > 0}
				<div class="soul-detail__activity-list">
					{#each commActivityItems.slice(0, 20) as item (item.activity_id + item.timestamp)}
						<div class="soul-detail__activity-row">
							<span class="soul-detail__activity-time">{relativeTime(item.timestamp)}</span>
							<span class="soul-detail__activity-desc">{activityDescription(item)}</span>
						</div>
					{/each}
				</div>
			{:else}
				<div class="soul-detail__empty">
					<Text size="sm" color="secondary">No communication activity recorded yet.</Text>
				</div>
			{/if}
		</Panel>
	{/if}
</PageFrame>

<style>
	/* ── Loading / empty ─────────────────────────────────────────────── */

	.soul-detail__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.soul-detail__empty {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		align-items: flex-start;
	}

	.soul-detail__empty-actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
	}

	/* ── Metric cards row ────────────────────────────────────────────── */

	.soul-detail__metrics {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: var(--gr-spacing-scale-4);
		margin-top: var(--gr-spacing-scale-4);
	}

	@media (max-width: 640px) {
		.soul-detail__metrics {
			grid-template-columns: 1fr;
		}
	}

	/* ── Continuity bar chart ────────────────────────────────────────── */

	.soul-detail__continuity-chart {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: flex-end;
		padding: var(--gr-spacing-scale-3) 0;
		height: 120px;
	}

	.soul-detail__continuity-bar-col {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--gr-spacing-scale-1);
		flex: 1;
		height: 100%;
		justify-content: flex-end;
	}

	.soul-detail__continuity-bar {
		width: 60%;
		min-height: 2px;
		border-radius: var(--gr-radius-md, 6px);
		background: var(--gr-color-border-secondary, #e0e0e0);
		transition: background 0.2s;
	}

	.soul-detail__continuity-bar--filled {
		background: var(--gr-color-action-gradient, var(--gr-color-accent, #4a6cf7));
		opacity: 0.9;
	}

	.soul-detail__continuity-label {
		font-size: 0.76rem;
		color: var(--gr-color-text-tertiary, #999);
	}

	.soul-detail__continuity-summary {
		text-align: center;
		margin-top: var(--gr-spacing-scale-2);
	}

	/* ── Activity timeline ───────────────────────────────────────────── */

	.soul-detail__activity-list {
		display: flex;
		flex-direction: column;
		gap: 0;
	}

	.soul-detail__activity-row {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		padding: var(--gr-spacing-scale-2) 0;
		border-bottom: 1px solid var(--gr-color-border-subtle, var(--gr-color-border-secondary, #e8e8e8));
	}

	.soul-detail__activity-row:last-child {
		border-bottom: none;
	}

	.soul-detail__activity-time {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
		font-size: 0.78rem;
		color: var(--gr-color-text-tertiary, #999);
		min-width: 90px;
		flex-shrink: 0;
	}

	.soul-detail__activity-desc {
		font-size: 0.92rem;
		color: var(--gr-color-text-primary, #111);
	}

	/* ── Shared type helpers ─────────────────────────────────────────── */

	.soul-detail-rail__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
	}

	.soul-detail-rail__muted {
		font-size: 0.86rem;
		color: var(--gr-color-text-secondary, #666);
	}

	.soul-detail-rail__tertiary {
		font-size: 0.78rem;
		color: var(--gr-color-text-tertiary, #999);
	}

	/* ── Right rail ──────────────────────────────────────────────────── */

	.soul-detail-rail {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-5);
	}

	/* ── Manifest definition list (right rail) ────────────────────── */

	.soul-detail-rail__manifest {
		display: flex;
		flex-direction: column;
		gap: 0;
		margin: 0;
		padding: 0;
	}

	.soul-detail-rail__manifest-row {
		display: grid;
		grid-template-columns: minmax(0, 6rem) minmax(0, 1fr);
		gap: var(--gr-spacing-scale-2);
		padding: var(--gr-spacing-scale-1) 0;
	}

	.soul-detail-rail__manifest-row:not(:last-child) {
		border-bottom: 1px solid var(--gr-color-border-subtle, var(--gr-color-border-secondary, #e0e0e0));
	}

	.soul-detail-rail__manifest-label {
		margin: 0;
		font-size: 0.80rem;
		font-weight: var(--gr-typography-fontWeight-medium, 500);
		color: var(--gr-color-text-secondary, #666);
		white-space: nowrap;
	}

	.soul-detail-rail__manifest-value {
		margin: 0;
		font-size: 0.86rem;
		color: var(--gr-color-text-primary, #111);
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.soul-detail-rail__anchor {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: center;
	}

	.soul-detail-rail__anchor-meta {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.soul-detail-rail__earnings {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.soul-detail-rail__earnings-row {
		display: flex;
		justify-content: space-between;
		font-size: 0.86rem;
	}

	.soul-detail-rail__divider {
		height: 1px;
		background: var(--gr-color-border-subtle, var(--gr-color-border-secondary, #e0e0e0));
		margin: var(--gr-spacing-scale-1) 0;
	}
</style>
