<!--
@component
StackMatrix — fleet-wide stack version table for the Operator Console.

Project 39 M2.6 (issue #432). Each row shows one managed instance's
lesser version, lesser-body version, MCP wired-against version, and a
drift indicator. Per-row CTAs:

  Update    — navigate to the instance's operator detail page (where
              an UpdateJob can be triggered once M2.10 ships).
  Wire MCP  — emit `onAction(slug, 'wire-mcp')` so a parent surface can
              dispatch the M2.10 remediation. The button is rendered
              only for `wire-stale` rows.

Sortable columns:
  slug, lesser, body, mcp, drift
Filterable: drift status (all / ok / wire-stale / unknown).

The component is render-only; data fetching belongs to the parent (the
M2.5 Releases page consumes the same OperatorInstanceDriftEntry list it
sources for the top-of-page banner).

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins.
- Multi-tenant isolation: render-only; the parent owns the operator-JWT
  fetch.
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.6
-->
<script lang="ts" module>
	import type { OperatorInstanceDriftEntry } from 'src/lib/api/operatorProvisioning';

	export type SortColumn = 'slug' | 'lesser' | 'body' | 'mcp' | 'drift';
	export type SortDirection = 'asc' | 'desc';
	export type DriftFilter = 'all' | 'ok' | 'wire-stale' | 'unknown';

	/** Filter entries by drift status. 'all' passes everything through. */
	export function filterByDrift(
		entries: OperatorInstanceDriftEntry[],
		filter: DriftFilter,
	): OperatorInstanceDriftEntry[] {
		if (filter === 'all') return entries;
		return entries.filter((e) => (e.drift_status || 'unknown') === filter);
	}

	/** Stable comparator for the chosen column + direction. */
	export function sortEntries(
		entries: OperatorInstanceDriftEntry[],
		column: SortColumn,
		direction: SortDirection,
	): OperatorInstanceDriftEntry[] {
		const dir = direction === 'asc' ? 1 : -1;
		const key = (e: OperatorInstanceDriftEntry): string => {
			switch (column) {
				case 'slug':
					return e.instance_slug;
				case 'lesser':
					return e.lesser_version || '';
				case 'body':
					return e.body_version || '';
				case 'mcp':
					return e.mcp_wired_against || '';
				case 'drift':
					return e.drift_status || 'unknown';
			}
		};
		// Slice so we don't mutate the parent's array reference.
		return [...entries].sort((a, b) => {
			const ka = key(a);
			const kb = key(b);
			if (ka < kb) return -1 * dir;
			if (ka > kb) return 1 * dir;
			return 0;
		});
	}
</script>

<script lang="ts">
	import { linkProps } from 'src/lib/router';
	import { Badge, Button, Link, Select, Text } from 'src/lib/ui';

	let {
		entries,
		onAction,
	}: {
		entries: OperatorInstanceDriftEntry[];
		onAction?: (slug: string, action: 'wire-mcp') => void;
	} = $props();

	let sortColumn = $state<SortColumn>('slug');
	let sortDirection = $state<SortDirection>('asc');
	let driftFilter = $state<DriftFilter>('all');

	const filtered = $derived(filterByDrift(entries, driftFilter));
	const sorted = $derived(sortEntries(filtered, sortColumn, sortDirection));

	function toggleSort(col: SortColumn) {
		if (sortColumn === col) {
			sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
		} else {
			sortColumn = col;
			sortDirection = 'asc';
		}
	}

	function ariaSortFor(col: SortColumn): 'ascending' | 'descending' | 'none' {
		if (sortColumn !== col) return 'none';
		return sortDirection === 'asc' ? 'ascending' : 'descending';
	}

	function driftBadge(status: string): {
		color: 'success' | 'warning' | 'gray' | 'error';
		label: string;
	} {
		const s = (status || 'unknown').toLowerCase();
		if (s === 'ok') return { color: 'success', label: 'OK' };
		if (s === 'wire-stale') return { color: 'warning', label: 'Wire-stale' };
		if (s === 'unknown') return { color: 'gray', label: 'Unknown' };
		return { color: 'error', label: s };
	}
</script>

<div class="matrix">
	<header class="matrix__header">
		<div class="matrix__filter">
			<Text size="sm">Filter by drift</Text>
			<Select
				bind:value={driftFilter}
				options={[
					{ value: 'all', label: 'All' },
					{ value: 'ok', label: 'OK' },
					{ value: 'wire-stale', label: 'Wire-stale' },
					{ value: 'unknown', label: 'Unknown' },
				]}
			/>
		</div>
		<Text size="sm" color="secondary">
			{sorted.length} of {entries.length} {entries.length === 1 ? 'instance' : 'instances'}
		</Text>
	</header>

	{#if sorted.length === 0}
		<Text size="sm" color="secondary">No instances match the current filter.</Text>
	{:else}
		<div class="matrix__scroll">
			<table class="matrix__table">
				<thead>
					<tr>
						<th scope="col" aria-sort={ariaSortFor('slug')}>
							<button type="button" class="matrix__sort" onclick={() => toggleSort('slug')}>
								Instance {sortColumn === 'slug' ? (sortDirection === 'asc' ? '▲' : '▼') : ''}
							</button>
						</th>
						<th scope="col" aria-sort={ariaSortFor('lesser')}>
							<button type="button" class="matrix__sort" onclick={() => toggleSort('lesser')}>
								Lesser {sortColumn === 'lesser' ? (sortDirection === 'asc' ? '▲' : '▼') : ''}
							</button>
						</th>
						<th scope="col" aria-sort={ariaSortFor('body')}>
							<button type="button" class="matrix__sort" onclick={() => toggleSort('body')}>
								Body {sortColumn === 'body' ? (sortDirection === 'asc' ? '▲' : '▼') : ''}
							</button>
						</th>
						<th scope="col" aria-sort={ariaSortFor('mcp')}>
							<button type="button" class="matrix__sort" onclick={() => toggleSort('mcp')}>
								MCP wired {sortColumn === 'mcp' ? (sortDirection === 'asc' ? '▲' : '▼') : ''}
							</button>
						</th>
						<th scope="col" aria-sort={ariaSortFor('drift')}>
							<button type="button" class="matrix__sort" onclick={() => toggleSort('drift')}>
								Drift {sortColumn === 'drift' ? (sortDirection === 'asc' ? '▲' : '▼') : ''}
							</button>
						</th>
						<th scope="col" class="matrix__actions-head">Actions</th>
					</tr>
				</thead>
				<tbody>
					{#each sorted as entry (entry.instance_slug)}
						{@const badge = driftBadge(entry.drift_status)}
						<tr>
							<th scope="row" class="matrix__slug">
								<Link {...linkProps(`/operator/instances/${entry.instance_slug}`)} variant="default">
									{entry.instance_slug}
								</Link>
							</th>
							<td class="matrix__mono">{entry.lesser_version || '—'}</td>
							<td class="matrix__mono">{entry.body_version || '—'}</td>
							<td class="matrix__mono">{entry.mcp_wired_against || '—'}</td>
							<td>
								<Badge color={badge.color} variant="filled" size="sm">{badge.label}</Badge>
							</td>
							<td class="matrix__actions">
								<Link
									{...linkProps(`/operator/instances/${entry.instance_slug}`)}
									variant="ghost"
								>
									Update
								</Link>
								{#if (entry.drift_status || '').toLowerCase() === 'wire-stale' && onAction}
									<Button
										variant="outline"
										onclick={() => onAction?.(entry.instance_slug, 'wire-mcp')}
									>
										Wire MCP
									</Button>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<style>
	.matrix {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		min-width: 0;
	}

	.matrix__header {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: flex-end;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.matrix__filter {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		min-width: 200px;
	}

	.matrix__scroll {
		overflow-x: auto;
	}

	.matrix__table {
		width: 100%;
		border-collapse: collapse;
		min-width: 720px;
	}

	.matrix__table th,
	.matrix__table td {
		text-align: left;
		padding: var(--gr-spacing-scale-3);
		border-bottom: 1px solid var(--gr-color-border, currentColor);
		vertical-align: middle;
	}

	.matrix__table thead th {
		font-size: 0.78rem;
		font-weight: 600;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--ds-fg-3, currentColor);
		background: transparent;
	}

	.matrix__sort {
		appearance: none;
		background: none;
		border: 0;
		padding: 0;
		font: inherit;
		color: inherit;
		cursor: pointer;
		text-align: left;
		letter-spacing: inherit;
		text-transform: inherit;
	}
	.matrix__sort:focus-visible {
		outline: 2px solid var(--ds-border-focus, var(--ds-warning-500));
		outline-offset: 2px;
		border-radius: 2px;
	}

	.matrix__slug {
		font-weight: 500;
	}

	.matrix__mono {
		font-family: var(--gr-typography-fontFamily-mono, ui-monospace, monospace);
		font-size: 0.92rem;
	}

	.matrix__actions-head {
		text-align: right;
	}

	.matrix__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		justify-content: flex-end;
		align-items: center;
		flex-wrap: wrap;
	}
</style>
