<!--
@component
Trust — Project 42 M15 portal trust dashboard at /portal/trust.

SPDX-License-Identifier: AGPL-3.0-only
@license AGPL-3.0-only

Fleet-level trust & federation dashboard consuming the owner-scoped
per-instance trust-data endpoint. Loads all the customer's instances,
fetches trust data for each, and aggregates across the fleet.

 Layout:
   1. Page header with eyebrow "Trust & federation" + title
   2. Four metric cards (SummaryStrip): reachable peers, warnings,
      severed peers, signature failures
   3. Left panel: peer constellation grid (with follower count and
      last_seen / last_fetch timestamps when available) + sparkline
      panels (signature failures, queue depth)
   4. Right rail: trust score gauge + dimensions + vouches list + severed alert

Issue #573 corrections applied:
  - Peer grid renders follower_count when present, honest "followers
    unavailable" when the field is null/omitted.
  - Peer grid renders last_seen (from lesser admin API) or last_fetch
    (host-side fallback timestamp) per peer row.
  - Signature failures panel renders a Sparkline only from the real
    signatures.series[].failures time series; it never derives a chart from
    per-source aggregates.
  - Window hours reflect the backend 24h default (window_hours=24).
  - Queue depth sparkline renders from real queue_depth.series data.
  - Empty states say "no scoped data is present" rather than "not yet
    instrumented".
  - Trust score gauge and vouches list use real scoped data; vouch
    strength is the presence marker (1.0) per the backing model.

Posture invariants preserved:
  - Strict-CSP safe: no inline style attributes or inline scripts.
  - Multi-tenant isolation: only owner-scoped portal endpoints consumed.
  - Backend DTO extension is additive: signatures.series is redacted,
    timestamped, and 24h bounded; queue_depth.series[].depth remains intact.
  - /portal/trust remains inside PortalShell; /portal/trust/attestations/{id}
    still delegates to the public attestation inspector via App.svelte routing.

Source: design fixture portal-pages-2.jsx:1–249 (FederationView / Trust)
Issue: equaltoai/lesser-host#550, #573
@license AGPL-3.0-only
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { InstanceResponse } from 'src/lib/api/portalInstances';
	import { portalListInstances } from 'src/lib/api/portalInstances';
	import type {
		PortalTrustDataResponse,
		TrustFederationPeerRow,
		TrustSignatureSeriesPoint,
		TrustSignaturesSourceRow,
		TrustVouchItem,
	} from 'src/lib/api/portalTrust';
	import { portalGetTrustData } from 'src/lib/api/portalTrust';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Heading, Spinner, Text } from 'src/lib/ui';
	import { StatCard, SummaryStrip } from 'src/lib/shell';
	import { Eyebrow, Sparkline } from 'src/lib/components/primitives';

	interface Props {
		token: string;
	}

	let { token }: Props = $props();

	// ── State ──────────────────────────────────────────────────────────

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let instances = $state<InstanceResponse[]>([]);
	let trustData = $state<PortalTrustDataResponse[]>([]);

	// ── Derived aggregates ────────────────────────────────────────────

	interface AggFederation {
		reachable: number;
		warning: number;
		severed: number;
		peers: TrustFederationPeerRow[];
	}

	interface AggSignatures {
		windowHours: number;
		totalFailures: number;
		bySource: TrustSignaturesSourceRow[];
		series: TrustSignatureSeriesPoint[];
		seriesFailureValues: number[];
	}

	interface AggQueueDepth {
		seriesDepthValues: number[];
	}

	interface AggTrustScore {
		score: number;
		formula: string;
		dimensions: {
			operational: number;
			attestation: number;
			social: number;
			economic: number;
			integrity: number;
		};
		source: string;
	}

	interface AggVouches {
		items: TrustVouchItem[];
		count: number;
	}

	const federation = $derived.by<AggFederation>(() => {
		let reachable = 0;
		let warning = 0;
		let severed = 0;
		const peerMap: Record<string, TrustFederationPeerRow> = {};
		for (const td of trustData) {
			reachable += td.federation.reachable;
			warning += td.federation.warning;
			severed += td.federation.severed;
			for (const p of td.federation.peers) {
				if (!(p.domain in peerMap)) {
					peerMap[p.domain] = p;
				}
			}
		}
		return {
			reachable,
			warning,
			severed,
			peers: Object.values(peerMap),
		};
	});

	const signatures = $derived.by<AggSignatures>(() => {
		let totalFailures = 0;
		const windowHours =
			trustData.length > 0 ? trustData[0].signatures.window_hours : 24;
		const sourceMap: Record<string, number> = {};
		const seriesMap: Record<string, number> = {};
		for (const td of trustData) {
			totalFailures += td.signatures.total_failures;
			for (const s of td.signatures.by_source) {
				sourceMap[s.source] = (sourceMap[s.source] ?? 0) + s.failures;
			}
			for (const point of td.signatures.series ?? []) {
				seriesMap[point.timestamp] =
					(seriesMap[point.timestamp] ?? 0) + point.failures;
			}
		}
		const bySource: TrustSignaturesSourceRow[] = [];
		for (const source of Object.keys(sourceMap)) {
			bySource.push({ source, failures: sourceMap[source] });
		}
		bySource.sort((a, b) => b.failures - a.failures);
		const series = Object.keys(seriesMap)
			.sort()
			.map((timestamp) => ({
				timestamp,
				failures: seriesMap[timestamp],
			}));
		return {
			windowHours,
			totalFailures,
			bySource,
			series,
			seriesFailureValues: series.map((point) => point.failures),
		};
	});

	const queueDepth = $derived.by<AggQueueDepth>(() => {
		const values: number[] = [];
		for (const td of trustData) {
			for (const pt of td.queue_depth.series) {
				values.push(pt.depth);
			}
		}
		return { seriesDepthValues: values };
	});

	const trustScore = $derived.by<AggTrustScore>(() => {
		const count = trustData.length;
		if (count === 0) {
			return {
				score: 0,
				formula: '',
				dimensions: { operational: 0, attestation: 0, social: 0, economic: 0, integrity: 0 },
				source: '',
			};
		}
		let sumScore = 0;
		let sumOp = 0;
		let sumAtt = 0;
		let sumSoc = 0;
		let sumEco = 0;
		let sumInt = 0;
		for (const td of trustData) {
			sumScore += td.trust_score.score;
			sumOp += td.trust_score.dimensions.operational;
			sumAtt += td.trust_score.dimensions.attestation;
			sumSoc += td.trust_score.dimensions.social;
			sumEco += td.trust_score.dimensions.economic;
			sumInt += td.trust_score.dimensions.integrity;
		}
		return {
			score: Math.round((sumScore / count) * 10) / 10,
			formula: trustData[0].trust_score.formula,
			dimensions: {
				operational: Math.round((sumOp / count) * 10) / 10,
				attestation: Math.round((sumAtt / count) * 10) / 10,
				social: Math.round((sumSoc / count) * 10) / 10,
				economic: Math.round((sumEco / count) * 10) / 10,
				integrity: Math.round((sumInt / count) * 10) / 10,
			},
			source: trustData[0].trust_score.source,
		};
	});

	const vouches = $derived.by<AggVouches>(() => {
		const itemMap: Record<string, TrustVouchItem> = {};
		let count = 0;
		for (const td of trustData) {
			count += td.vouches.count;
			for (const vi of td.vouches.items) {
				if (!(vi.peer in itemMap)) {
					itemMap[vi.peer] = vi;
				}
			}
		}
		return { items: Object.values(itemMap), count };
	});

	const totalPeers = $derived(
		federation.reachable + federation.warning + federation.severed,
	);

	const headingTitle = $derived(
		`${totalPeers} peer${totalPeers !== 1 ? 's' : ''}, ${federation.severed} severed.`,
	);

	const sigFailuresLabel = $derived(`Sig failures ${signatures.windowHours}h`);

	// ── Trust score gauge helpers ──────────────────────────────────────

	const gaugeSize = 160;
	const gaugeStrokeWidth = 10;
	const gaugeRadius = $derived((gaugeSize - 2 * gaugeStrokeWidth) / 2);
	const gaugeCircumference = $derived(2 * Math.PI * gaugeRadius);
	const gaugeCx = $derived(gaugeSize / 2);
	const gaugeCy = $derived(gaugeSize / 2);

	const scorePct = $derived(
		Math.min(100, Math.max(0, trustScore.score)),
	);

	const gaugeOffset = $derived.by(() => {
		const clamped = Math.min(100, Math.max(0, scorePct));
		return gaugeCircumference - (clamped / 100) * gaugeCircumference;
	});

	const scoreDisplay = $derived(
		trustData.length === 0 ? '—' : String(trustScore.score),
	);

	const gaugeStatus = $derived.by<'ok' | 'warning' | 'danger'>(() => {
		if (scorePct >= 70) return 'ok';
		if (scorePct >= 40) return 'warning';
		return 'danger';
	});

	const gaugeLabel = $derived(
		trustData.length === 0
			? 'No data'
			: gaugeStatus === 'ok'
				? 'Healthy'
				: gaugeStatus === 'warning'
					? 'Moderate'
					: 'Low',
	);

	// ── Helpers ────────────────────────────────────────────────────────

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	function peerStatusClass(status: string): string {
		switch (status) {
			case 'reachable':
				return 'trust-peer-grid__dot--reachable';
			case 'warning':
				return 'trust-peer-grid__dot--warning';
			case 'severed':
				return 'trust-peer-grid__dot--severed';
			default:
				return 'trust-peer-grid__dot--unknown';
		}
	}

	// ── Data loading ───────────────────────────────────────────────────

	async function loadAll() {
		errorMessage = null;
		instances = [];
		trustData = [];
		loading = true;
		try {
			const list = await portalListInstances(token);
			instances = list.instances;
			const results: PortalTrustDataResponse[] = [];
			for (const inst of list.instances) {
				try {
					const td = await portalGetTrustData(token, inst.slug);
					results.push(td);
				} catch (err) {
					// Per-instance trust data load failure is non-fatal;
					// the dashboard renders with whatever was loaded.
					console.warn(
						`Trust data load failed for ${inst.slug}:`,
						formatError(err),
					);
				}
			}
			trustData = results;
		} catch (err) {
			const message = formatError(err);
			errorMessage = message;
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
			}
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void loadAll();
	});
</script>

<div class="trust">
	<!-- ── Header ──────────────────────────────────────────────────── -->
	<header class="trust__header">
		<div class="trust__header-text">
			<Eyebrow>Trust &amp; federation</Eyebrow>
			<Heading level={1} size="2xl">{headingTitle}</Heading>
			<Text size="sm" color="secondary">
				Fleet-wide federation health, signature reliability, and trust posture
				across your managed instances. Data is scoped to your owned instances
				and reflects available telemetry.
			</Text>
		</div>
	</header>

	{#if loading}
		<div class="trust__loading">
			<Spinner size="md" />
			<Text>Loading trust data…</Text>
		</div>
	{:else if errorMessage}
		<Alert variant="error" title="Failed to load trust data">{errorMessage}</Alert>
	{:else}
		<!-- ── Metric cards ─────────────────────────────────────────── -->
		<SummaryStrip label="Trust summary" columns={4} gap="md">
			<StatCard
				label="Reachable peers"
				value={String(federation.reachable)}
				status={federation.reachable > 0 ? 'success' : 'default'}
			/>
			<StatCard
				label="Warnings"
				value={String(federation.warning)}
				status={federation.warning > 0 ? 'warning' : 'default'}
			/>
			<StatCard
				label="Severed peers"
				value={String(federation.severed)}
				status={federation.severed > 0 ? 'danger' : 'default'}
			/>
			<StatCard
				label={sigFailuresLabel}
				value={String(signatures.totalFailures)}
				status={
					signatures.totalFailures > 0 ? 'warning' : 'success'
				}
			/>
		</SummaryStrip>

		<!-- ── Main layout ──────────────────────────────────────────── -->
		<div class="trust__main">
			<!-- ── Left panel ───────────────────────────────────────── -->
			<div class="trust__left">
				<!-- Peer constellation grid -->
				<section class="trust__panel" aria-label="Federation peer constellation">
					<Heading level={2} size="lg">Peer constellation</Heading>
					{#if federation.peers.length === 0}
						<div class="trust__empty-state">
							<Text size="sm" color="secondary">
								No scoped federation peer data is present.
								When federation peers are detected across your
								managed instances, they will appear here as
								constellation squares with reachability status.
							</Text>
						</div>
					{:else}
						<div
							class="trust-peer-grid"
							role="list"
							aria-label="Federation peers"
						>
							{#each federation.peers as peer (peer.domain)}
								<div class="trust-peer-grid__item" role="listitem">
									<div class="trust-peer-grid__main">
										<span
											class="trust-peer-grid__dot {peerStatusClass(peer.status)}"
											aria-hidden="true"
										></span>
										<span class="trust-peer-grid__domain">{peer.domain}</span>
										<span class="trust-peer-grid__status">{peer.status}</span>
									</div>
									<div class="trust-peer-grid__meta">
										{#if peer.follower_count != null}
											<span class="trust-peer-grid__followers">
												{peer.follower_count.toLocaleString()} follower{peer.follower_count !== 1 ? 's' : ''}
											</span>
										{:else}
											<span class="trust-peer-grid__followers trust-peer-grid__followers--unavailable">
												followers unavailable
											</span>
										{/if}
										{#if peer.last_seen}
											<span class="trust-peer-grid__timestamp">
												Seen: {peer.last_seen.slice(0, 10)}
											</span>
										{:else if peer.last_fetch}
											<span class="trust-peer-grid__timestamp">
												Fetch: {peer.last_fetch.slice(0, 10)}
											</span>
										{/if}
									</div>
								</div>
							{/each}
						</div>
					{/if}
				</section>

				<!-- Signature failures sparkline panel -->
				<section class="trust__panel" aria-label="HTTP signature failures">
					<Heading level={2} size="lg">Signature failures</Heading>
					<Text size="sm" color="secondary">
						Last {signatures.windowHours}h · {signatures.totalFailures} total
						failures across {signatures.bySource.length} source
						{signatures.bySource.length !== 1 ? 's' : ''}.
					</Text>
					{#if signatures.seriesFailureValues.length === 0}
						<div class="trust__empty-state">
							<Text size="sm" color="secondary">
								{#if signatures.totalFailures === 0}
									No signature failures recorded in the dashboard window.
								{:else}
									Signature failures were counted, but no timestamped series
									points are present in this response.
								{/if}
							</Text>
						</div>
					{:else}
						<Text size="sm" color="secondary">
							{signatures.series.length} hourly bucket
							{signatures.series.length !== 1 ? 's' : ''}.
							Max failures: {Math.max(...signatures.seriesFailureValues)}.
						</Text>
						<div class="trust__sparkline-container">
							<Sparkline
								values={signatures.seriesFailureValues}
								width={200}
								height={48}
								color="var(--ds-warning-500)"
							/>
						</div>
					{/if}
					{#if signatures.bySource.length > 0}
						<div class="trust__source-list" role="list" aria-label="Failures by source">
							{#each signatures.bySource as src (src.source)}
								<div class="trust__source-row" role="listitem">
									<span class="trust__source-name">{src.source}</span>
									<span class="trust__source-count">{src.failures}</span>
								</div>
							{/each}
						</div>
					{/if}
				</section>

				<!-- Queue depth sparkline panel -->
				<section class="trust__panel" aria-label="Inbound queue depth">
					<Heading level={2} size="lg">Queue depth</Heading>
					{#if queueDepth.seriesDepthValues.length === 0}
						<div class="trust__empty-state">
							<Text size="sm" color="secondary">
								No inbound queue depth data is present.
							</Text>
						</div>
					{:else}
						<Text size="sm" color="secondary">
							{queueDepth.seriesDepthValues.length} data point
							{queueDepth.seriesDepthValues.length !== 1 ? 's' : ''}.
							Max depth: {Math.max(...queueDepth.seriesDepthValues)}.
						</Text>
						<div class="trust__sparkline-container">
							<Sparkline
								values={queueDepth.seriesDepthValues}
								width={200}
								height={48}
								color="var(--ds-secondary-500)"
							/>
						</div>
					{/if}
				</section>
			</div>

			<!-- ── Right rail ────────────────────────────────────────── -->
			<div class="trust__right">
				<!-- Trust score gauge -->
				<section class="trust__panel" aria-label="Trust score">
					<Heading level={2} size="lg">Trust score</Heading>
					<div class="trust__gauge-wrap">
						<svg
							class="trust-gauge trust-gauge--{gaugeStatus}"
							width={gaugeSize}
							height={gaugeSize}
							viewBox="0 0 {gaugeSize} {gaugeSize}"
							role="meter"
							aria-valuemin={0}
							aria-valuemax={100}
							aria-valuenow={trustScore.score}
							aria-valuetext="{trustScore.score} out of 100"
							focusable="false"
						>
							<!-- Ring group rotated -90deg so arc starts at 12 o'clock -->
							<g class="trust-gauge__ring-group">
								<circle
									class="trust-gauge__track"
									cx={gaugeCx}
									cy={gaugeCy}
									r={gaugeRadius}
									stroke-width={gaugeStrokeWidth}
								/>
								<circle
									class="trust-gauge__arc"
									cx={gaugeCx}
									cy={gaugeCy}
									r={gaugeRadius}
									stroke-width={gaugeStrokeWidth}
									stroke-dasharray={gaugeCircumference}
									stroke-dashoffset={gaugeOffset}
								/>
							</g>
							<!-- Center text -->
							<text
								class="trust-gauge__score"
								x={gaugeCx}
								y={gaugeCy}
								text-anchor="middle"
								dominant-baseline="central"
								font-size={gaugeSize * 0.22}
							>
								{scoreDisplay}
							</text>
							<text
								class="trust-gauge__label"
								x={gaugeCx}
								y={gaugeCy + gaugeSize * 0.14}
								text-anchor="middle"
								dominant-baseline="hanging"
							>
								{gaugeLabel}
							</text>
						</svg>
					</div>

					<!-- Dimension breakdown -->
					<div class="trust__dimensions">
						<div class="trust__dimension-row">
							<span class="trust__dimension-label">Operational</span>
							{@render ProgressBarSimple({ value: trustScore.dimensions.operational, max: 100 })}
						</div>
						<div class="trust__dimension-row">
							<span class="trust__dimension-label">Attestation</span>
							{@render ProgressBarSimple({ value: trustScore.dimensions.attestation, max: 100 })}
						</div>
						<div class="trust__dimension-row">
							<span class="trust__dimension-label">Social</span>
							{@render ProgressBarSimple({ value: trustScore.dimensions.social, max: 100 })}
						</div>
						<div class="trust__dimension-row">
							<span class="trust__dimension-label">Economic</span>
							{@render ProgressBarSimple({ value: trustScore.dimensions.economic, max: 100 })}
						</div>
						<div class="trust__dimension-row">
							<span class="trust__dimension-label">Integrity</span>
							{@render ProgressBarSimple({ value: trustScore.dimensions.integrity, max: 100 })}
						</div>
					</div>

					<Text size="xs" color="secondary">{trustScore.source}</Text>
				</section>

				<!-- Vouches -->
				<section class="trust__panel" aria-label="Vouches from peers">
					<Heading level={2} size="lg">
						Vouches from peers
					</Heading>
					<Text size="sm" color="secondary">
						{vouches.count} endorsement
						{vouches.count !== 1 ? 's' : ''}
					</Text>
					{#if vouches.items.length === 0}
						<div class="trust__empty-state">
							<Text size="sm" color="secondary">
								No peer endorsements recorded.
							</Text>
						</div>
					{:else}
						<div class="trust__vouch-list" role="list" aria-label="Peer vouches">
							{#each vouches.items as vouch (vouch.peer)}
								<div class="trust__vouch-item" role="listitem">
									<span class="trust__vouch-peer">{vouch.peer}</span>
									{#if vouch.created_at}
										<span class="trust__vouch-date">{vouch.created_at.slice(0, 10)}</span>
									{/if}
								</div>
							{/each}
						</div>
					{/if}
				</section>

				<!-- Severed alert -->
				{#if federation.severed > 0}
					<Alert variant="warning" title="Severed peers detected">
						<Text size="sm">
							{federation.severed} peer
							{federation.severed !== 1 ? 's have' : ' has'} been
							severed. Review federation health in your instance
							dashboards.
						</Text>
					</Alert>
				{/if}
			</div>
		</div>
	{/if}
</div>

<!--
ProgressBarSimple — minimal inline progress bar for trust-score dimensions.
Strict-CSP safe: no inline styles; uses CSS classes only.
-->
{#snippet ProgressBarSimple(props: { value: number; max: number })}
	{@const pct = props.max > 0 ? Math.min(100, Math.max(0, (props.value / props.max) * 100)) : 0}
	{@const barStatus = pct >= 70 ? 'ok' : pct >= 40 ? 'warning' : 'danger'}
	<!-- CSP-safe: SVG rect with presentation attribute width for dynamic fill,
	     no inline style attribute. -->
	<div
		class="trust-progress trust-progress--{barStatus}"
		role="progressbar"
		aria-valuenow={props.value}
		aria-valuemin={0}
		aria-valuemax={props.max}
		aria-label="{props.value} / {props.max}"
	>
		<svg
			class="trust-progress__svg"
			viewBox="0 0 100 6"
			preserveAspectRatio="none"
			aria-hidden="true"
			focusable="false"
		>
			<rect
				class="trust-progress__fill"
				x="0"
				y="0"
				width={pct}
				height="6"
				rx="3"
				ry="3"
			/>
		</svg>
	</div>
{/snippet}

<style>
	.trust {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-6) 0;
	}

	.trust__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.trust__header-text {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
		max-width: 72ch;
	}

	.trust__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	/* ── Main two-column layout ──────────────────────────────────────── */

	.trust__main {
		display: grid;
		grid-template-columns: 1fr 320px;
		gap: var(--gr-spacing-scale-6);
		align-items: start;
	}

	.trust__left {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.trust__right {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		position: sticky;
		top: var(--gr-spacing-scale-6);
	}

	/* ── Panels ──────────────────────────────────────────────────────── */

	.trust__panel {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		padding: var(--gr-spacing-scale-4);
		border: 1px solid var(--gr-semantic-border-default, var(--gr-color-gray-200));
		border-radius: var(--gr-radii-md, 0.375rem);
		background: var(--gr-semantic-background-surface, var(--gr-color-base-white));
	}

	.trust__empty-state {
		padding: var(--gr-spacing-scale-4);
		border: 1px dashed var(--gr-semantic-border-default, var(--gr-color-gray-300));
		border-radius: var(--gr-radii-sm, 0.25rem);
		background: var(--gr-semantic-background-subtle, var(--gr-color-gray-50));
	}

	/* ── Peer constellation grid ─────────────────────────────────────── */

	.trust-peer-grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
		gap: var(--gr-spacing-scale-3);
		list-style: none;
		padding: 0;
		margin: 0;
	}

	.trust-peer-grid__item {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		padding: var(--gr-spacing-scale-2) var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-semantic-border-default, var(--gr-color-gray-200));
		border-radius: var(--gr-radii-sm, 0.25rem);
	}

	.trust-peer-grid__main {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
	}

	.trust-peer-grid__meta {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
		padding-left: calc(8px + var(--gr-spacing-scale-2));
	}

	.trust-peer-grid__dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		flex-shrink: 0;
	}

	.trust-peer-grid__dot--reachable {
		background: var(--ds-success-500, #22c55e);
	}

	.trust-peer-grid__dot--warning {
		background: var(--ds-warning-500, #f59e0b);
	}

	.trust-peer-grid__dot--severed {
		background: var(--ds-error-500, #ef4444);
	}

	.trust-peer-grid__dot--unknown {
		background: var(--ds-secondary-400, #9ca3af);
	}

	.trust-peer-grid__domain {
		font-size: var(--gr-font-size-sm, 0.875rem);
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.trust-peer-grid__status {
		font-size: var(--gr-font-size-xs, 0.75rem);
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		flex-shrink: 0;
	}

	.trust-peer-grid__followers {
		font-size: var(--gr-font-size-xs, 0.75rem);
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		font-variant-numeric: tabular-nums;
	}

	.trust-peer-grid__followers--unavailable {
		color: var(--ds-secondary-400, #9ca3af);
		font-style: italic;
	}

	.trust-peer-grid__timestamp {
		font-size: var(--gr-font-size-xs, 0.75rem);
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
	}

	/* ── Sparkline container ─────────────────────────────────────────── */

	.trust__sparkline-container {
		width: 100%;
		max-width: 400px;
	}

	/* ── Source list (signature failures by source) ──────────────────── */

	.trust__source-list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.trust__source-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
		padding: var(--gr-spacing-scale-1) 0;
	}

	.trust__source-name {
		font-size: var(--gr-font-size-sm, 0.875rem);
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas,
			'Liberation Mono', 'Courier New', monospace;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.trust__source-count {
		font-size: var(--gr-font-size-sm, 0.875rem);
		font-weight: var(--gr-font-weight-semibold, 600);
		font-variant-numeric: tabular-nums;
		flex-shrink: 0;
	}

	/* ── Trust score gauge ───────────────────────────────────────────── */

	.trust__gauge-wrap {
		display: flex;
		justify-content: center;
		padding: var(--gr-spacing-scale-4) 0;
	}

	.trust-gauge__ring-group {
		transform: rotate(-90deg);
		transform-origin: center;
	}

	.trust-gauge__track {
		fill: none;
		stroke: var(--ds-secondary-200, #e5e7eb);
	}

	.trust-gauge__arc {
		fill: none;
		stroke-linecap: round;
		transition: stroke-dashoffset 0.5s ease;
	}

	.trust-gauge--ok .trust-gauge__arc {
		stroke: var(--ds-success-500, #22c55e);
	}

	.trust-gauge--warning .trust-gauge__arc {
		stroke: var(--ds-warning-500, #f59e0b);
	}

	.trust-gauge--danger .trust-gauge__arc {
		stroke: var(--ds-error-500, #ef4444);
	}

	.trust-gauge__score {
		fill: var(--gr-semantic-text-primary, var(--gr-color-gray-900));
		font-weight: var(--gr-font-weight-bold, 700);
	}

	.trust-gauge__label {
		fill: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		font-size: var(--gr-font-size-sm, 0.875rem);
	}

	/* ── Dimensions (progress bars) ──────────────────────────────────── */

	.trust__dimensions {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.trust__dimension-row {
		display: flex;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
	}

	.trust__dimension-label {
		font-size: var(--gr-font-size-xs, 0.75rem);
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		width: 5em;
		flex-shrink: 0;
	}

	.trust-progress {
		flex: 1;
		height: 6px;
		border-radius: 3px;
		background: var(--ds-secondary-200, #e5e7eb);
		overflow: hidden;
	}

	.trust-progress__svg {
		display: block;
		width: 100%;
		height: 100%;
	}

	.trust-progress__fill {
		fill: var(--ds-secondary-400, #9ca3af);
		transition: width 0.3s ease;
	}

	.trust-progress--ok .trust-progress__fill {
		fill: var(--ds-success-500, #22c55e);
	}

	.trust-progress--warning .trust-progress__fill {
		fill: var(--ds-warning-500, #f59e0b);
	}

	.trust-progress--danger .trust-progress__fill {
		fill: var(--ds-error-500, #ef4444);
	}

	/* ── Vouch list ──────────────────────────────────────────────────── */

	.trust__vouch-list {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.trust__vouch-item {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: var(--gr-spacing-scale-2);
		padding: var(--gr-spacing-scale-1) 0;
	}

	.trust__vouch-peer {
		font-size: var(--gr-font-size-sm, 0.875rem);
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas,
			'Liberation Mono', 'Courier New', monospace;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.trust__vouch-date {
		font-size: var(--gr-font-size-xs, 0.75rem);
		color: var(--gr-semantic-text-secondary, var(--gr-color-gray-500));
		flex-shrink: 0;
	}

	/* ── Responsive: single column on narrow viewports ───────────────── */

	@media (max-width: 864px) {
		.trust__main {
			grid-template-columns: 1fr;
		}

		.trust__right {
			position: static;
		}
	}
</style>
