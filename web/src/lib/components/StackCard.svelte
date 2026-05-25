<!--
@component
StackCard — customer-readable Lesser / Body / MCP stack-state panel.

M1.6 — Project 39 web UI rework. Renders the Lesser + Body + MCP rows
from the new `GET /api/v1/portal/instances/{slug}/stack` endpoint
(M1.11 backend). When body is not installed, surfaces the "Add agentic"
CTA. When MCP is wired against an older body version than is currently
deployed, surfaces a drift warning.

This component is intentionally fault-tolerant: M1.11 (backend) lands
in a follow-up issue, so the stack endpoint may not yet exist when this
UI ships. A 404 / 5xx / network failure renders a neutral "stack
telemetry pending" empty state rather than blocking the page, and an
optional `fallback` prop lets the parent surface basic version info
sourced from the existing Instance response so the page is useful even
before the backend lands.

Posture invariants preserved:
  - Strict-no-inline-CSP safe.
  - Multi-tenant isolation: the new stack endpoint applies its own
    `requirePortalInstanceOwnership` check before any DB read; this
    component just consumes the typed response.
  - Trust-API instance-auth untouched; SEC-9 change-lock not engaged.
  - No on-chain code path changed.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.6
Issue: equaltoai/lesser-host#415
-->

<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type {
		InstanceResponse,
		InstanceStackResponse,
	} from 'src/lib/api/portalInstances';
	import { portalGetInstanceStack } from 'src/lib/api/portalInstances';
	import { Alert, Badge, Button, Spinner, Text } from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';

	interface Props {
		token: string;
		slug: string;
		/**
		 * Optional fallback instance data from the existing
		 * `GET /api/v1/portal/instances/{slug}` response. When the stack
		 * endpoint is unavailable (M1.11 not yet deployed, or 404), the
		 * card uses these fields to render basic version info so the page
		 * is still useful.
		 */
		fallback?: InstanceResponse | null;
	}

	let { token, slug, fallback = null }: Props = $props();

	let loading = $state(false);
	let stack = $state<InstanceStackResponse | null>(null);
	let stackError = $state<string | null>(null);
	let endpointPending = $state(false);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function loadStack() {
		stackError = null;
		endpointPending = false;
		loading = true;
		try {
			stack = await portalGetInstanceStack(token, slug);
		} catch (err) {
			const status = (err as Partial<ApiError>).status;
			// 404 + 501 (Not Implemented) signal the endpoint is not yet
			// deployed. Treat as a graceful pending state, not an error.
			if (status === 404 || status === 501) {
				endpointPending = true;
			} else {
				stackError = formatError(err);
			}
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void loadStack();
	});

	// Component-row data derived from the stack endpoint (preferred) or
	// the fallback instance response (graceful degrade).
	interface Row {
		readonly id: 'lesser' | 'body' | 'mcp';
		readonly label: string;
		readonly version: string;
		readonly deployedAt: string;
		readonly drift: 'ok' | 'stale' | 'wire-stale' | 'unknown' | 'missing';
		readonly cta?: { label: string; intent: 'add-agentic' };
	}

	function driftBadge(drift: Row['drift']): {
		variant: 'outlined' | 'filled';
		color: 'success' | 'warning' | 'gray' | 'error';
		label: string;
	} {
		switch (drift) {
			case 'ok':
				return { variant: 'filled', color: 'success', label: 'up to date' };
			case 'stale':
				return { variant: 'filled', color: 'warning', label: 'stale' };
			case 'wire-stale':
				return { variant: 'filled', color: 'warning', label: 'MCP wire stale' };
			case 'missing':
				return { variant: 'outlined', color: 'gray', label: 'not installed' };
			case 'unknown':
			default:
				return { variant: 'outlined', color: 'gray', label: 'unknown' };
		}
	}

	function normalizeDrift(value: unknown): Row['drift'] {
		if (value === 'ok' || value === 'stale' || value === 'wire-stale' || value === 'unknown') {
			return value;
		}
		return 'unknown';
	}

	const rows = $derived.by<Row[]>(() => {
		if (stack) {
			const lesserVersion = stack.lesser?.current_version || '—';
			const bodyEnabled = stack.body?.enabled ?? false;
			const bodyVersion = stack.body?.current_version || '—';
			const mcpVersion = stack.mcp?.wired_against_body_version || '—';
			return [
				{
					id: 'lesser',
					label: 'Lesser',
					version: lesserVersion,
					deployedAt: stack.lesser?.deployed_at || '—',
					drift: normalizeDrift(stack.lesser?.drift),
				},
				{
					id: 'body',
					label: 'Body',
					version: bodyEnabled ? bodyVersion : '—',
					deployedAt: bodyEnabled ? stack.body?.deployed_at || '—' : '—',
					drift: bodyEnabled ? normalizeDrift(stack.body?.drift) : 'missing',
					cta: bodyEnabled ? undefined : { label: 'Add agentic', intent: 'add-agentic' },
				},
				{
					id: 'mcp',
					label: 'MCP',
					version: mcpVersion,
					deployedAt: stack.mcp?.wired_at || '—',
					drift: normalizeDrift(stack.mcp?.drift),
				},
			];
		}
		// Fallback rendering when the stack endpoint isn't available yet.
		// Surface basic version info from the existing Instance response.
		if (fallback) {
			const hasBody = Boolean(fallback.body_provisioned_at || fallback.lesser_body_version);
			return [
				{
					id: 'lesser',
					label: 'Lesser',
					version: fallback.lesser_version || '—',
					deployedAt: fallback.lesser_update_at || '—',
					drift: 'unknown' as const,
				},
				{
					id: 'body',
					label: 'Body',
					version: hasBody ? fallback.lesser_body_version || '—' : '—',
					deployedAt: hasBody ? fallback.lesser_body_update_at || fallback.body_provisioned_at || '—' : '—',
					drift: hasBody ? ('unknown' as const) : ('missing' as const),
					cta: hasBody ? undefined : { label: 'Add agentic', intent: 'add-agentic' as const },
				},
				{
					id: 'mcp',
					label: 'MCP',
					version: fallback.lesser_body_version || '—',
					deployedAt: fallback.mcp_update_at || fallback.mcp_wired_at || '—',
					drift: 'unknown' as const,
				},
			];
		}
		return [];
	});

	const mcpDriftWarning = $derived.by<string | null>(() => {
		const mcpRow = rows.find((r) => r.id === 'mcp');
		if (!mcpRow || mcpRow.drift !== 'wire-stale') return null;
		const bodyRow = rows.find((r) => r.id === 'body');
		const currentBody = bodyRow?.version || 'current body';
		const wiredAgainst = stack?.mcp?.wired_against_body_version || mcpRow.version;
		return `MCP was wired against body ${wiredAgainst}, but body ${currentBody} is now deployed. Run a "Wire MCP" update to re-bind.`;
	});

	function onAddAgentic() {
		// Trigger an `add-agentic` event the parent can listen to. The
		// concrete provisioning flow stays in the Updates section of
		// InstanceDetail so the existing managed-update orchestration
		// (start-update job with body version) remains the source of truth.
		const detail = { slug };
		window.dispatchEvent(new CustomEvent('host:add-agentic', { detail }));
	}
</script>

<Panel title="Stack" headerLevel={2}>
	{#snippet actions()}
		<Button variant="outline" onclick={() => void loadStack()} disabled={loading}>Refresh</Button>
	{/snippet}

	{#if loading && rows.length === 0}
		<div class="stack-card__loading">
			<Spinner size="sm" />
			<Text size="sm">Loading stack…</Text>
		</div>
	{:else if stackError}
		<Alert variant="error" title="Failed to load stack state">{stackError}</Alert>
	{:else if endpointPending && rows.length === 0}
		<Alert variant="info" title="Stack telemetry pending">
			<Text size="sm">
				The customer-readable stack-state endpoint (M1.11) is not yet deployed in this environment.
				Version metadata for Lesser, Body, and MCP will appear here once the backend ships.
			</Text>
		</Alert>
	{:else if rows.length === 0}
		<Alert variant="info" title="No stack data">
			<Text size="sm">Stack state is not yet available for this instance.</Text>
		</Alert>
	{:else}
		{#if endpointPending}
			<Alert variant="info" title="Stack telemetry (preview)">
				<Text size="sm">
					Showing version info from the existing Instance response while the dedicated
					stack-state endpoint (M1.11) lands. Drift indicators are unavailable until then.
				</Text>
			</Alert>
		{/if}

		{#if mcpDriftWarning}
			<Alert variant="warning" title="MCP drift">
				<Text size="sm">{mcpDriftWarning}</Text>
			</Alert>
		{/if}

		<ul class="stack-card__rows">
			{#each rows as row (row.id)}
				{@const badge = driftBadge(row.drift)}
				<li class="stack-card__row">
					<div class="stack-card__row-main">
						<div class="stack-card__row-heading">
							<Text size="sm" weight="medium">{row.label}</Text>
							<Badge variant={badge.variant} color={badge.color} size="sm">{badge.label}</Badge>
						</div>
						<Text size="sm" color="secondary">
							Version: <span class="stack-card__mono">{row.version}</span>
						</Text>
						<Text size="sm" color="secondary">
							{row.id === 'mcp' ? 'Wired at' : 'Deployed at'}:
							<span class="stack-card__mono">{row.deployedAt}</span>
						</Text>
					</div>
					{#if row.cta && row.cta.intent === 'add-agentic'}
						<div class="stack-card__row-cta">
							<Button variant="solid" onclick={() => onAddAgentic()}>{row.cta.label}</Button>
							<Text size="sm" color="secondary">
								Enable body + MCP via an update from the Updates section below.
							</Text>
						</div>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</Panel>

<style>
	.stack-card__loading {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
	}

	.stack-card__rows {
		list-style: none;
		padding: 0;
		margin: var(--ds-space-3) 0 0 0;
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3);
	}

	.stack-card__row {
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

	.stack-card__row-main {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1);
		min-width: min(280px, 100%);
	}

	.stack-card__row-heading {
		display: flex;
		gap: var(--ds-space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.stack-card__row-cta {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-1);
		align-items: flex-end;
	}

	.stack-card__mono {
		font-family: var(--ds-font-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace);
	}
</style>
