<!--
@component
StackMatrix — fleet-wide stack version table for the Operator Console.

Project 39 M2.6 (issue #432). Each row shows one managed instance's
lesser version + target, body version + target, MCP wired-against
version + current body, plus per-cell drift indicators and a row-level
drift label.

Aligned to the documented Change 5.3 contract after PR #512 arch
review 4363557132 Blocker 2. The data model is now per-component
nested:

  entry.lesser = { current, target, drift }
  entry.body   = { current, target, drift }
  entry.mcp    = { wired_against, current_body, drift }

Per-row CTAs:
  Update    — link to /operator/instances/{slug}
  Wire MCP  — emits `onAction(slug, 'wire-mcp')` for wire-stale rows;
              rendered only when the parent supplies `onAction`.

Sortable columns: slug, lesser, body, mcp, drift.
Filterable: drift status (all / ok / lesser-stale / body-stale /
            wire-stale / unknown).

Posture preserved:
- Strict-CSP-safe: no inline scripts / styles / third-party origins.
- Multi-tenant isolation: render-only; the parent owns the operator-JWT
  fetch.
- Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.6
-->
<script lang="ts" module>
	import type { OperatorInstanceDriftEntry, RowDriftLabel } from 'src/lib/api/operatorProvisioning';
	import { rowDriftLabel } from 'src/lib/api/operatorProvisioning';

	export type SortColumn = 'slug' | 'lesser' | 'body' | 'mcp' | 'drift';
	export type SortDirection = 'asc' | 'desc';
	export type DriftFilter = 'all' | 'ok' | 'lesser-stale' | 'body-stale' | 'wire-stale' | 'unknown';

	/** Filter entries by row-level drift label. 'all' passes everything through. */
	export function filterByDrift(
		entries: OperatorInstanceDriftEntry[],
		filter: DriftFilter,
	): OperatorInstanceDriftEntry[] {
		if (filter === 'all') return entries;
		return entries.filter((e) => rowDriftLabel(e) === filter);
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
					return e.slug;
				case 'lesser':
					return e.lesser?.current || '';
				case 'body':
					return e.body?.current || '';
				case 'mcp':
					return e.mcp?.wired_against || '';
				case 'drift':
					return rowDriftLabel(e);
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

	function driftBadge(label: RowDriftLabel): {
		color: 'success' | 'warning' | 'gray' | 'error';
		text: string;
	} {
		switch (label) {
			case 'ok':
				return { color: 'success', text: 'OK' };
			case 'wire-stale':
				return { color: 'warning', text: 'Wire-stale' };
			case 'lesser-stale':
				return { color: 'warning', text: 'Lesser-stale' };
			case 'body-stale':
				return { color: 'warning', text: 'Body-stale' };
			case 'unknown':
			default:
				return { color: 'gray', text: 'Unknown' };
		}
	}

	function cellLabel(current: string | undefined, target?: string, drift?: string): string {
		const cur = current || '—';
		if (!target || target === current) return cur;
		const d = (drift || '').toLowerCase();
		if (d === 'stale' || d === 'wire-stale') return `${cur} → ${target}`;
		return cur;
	}

	function mcpLabel(entry: OperatorInstanceDriftEntry): string {
		const wired = entry.mcp?.wired_against || '—';
		const cur = entry.mcp?.current_body;
		if (cur && cur !== wired) return `${wired} (body @ ${cur})`;
		return wired;
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
					{ value: 'lesser-stale', label: 'Lesser-stale' },
					{ value: 'body-stale', label: 'Body-stale' },
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
					{#each sorted as entry (entry.slug)}
						{@const label = rowDriftLabel(entry)}
						{@const badge = driftBadge(label)}
						<tr>
							<th scope="row" class="matrix__slug">
								<Link {...linkProps(`/operator/instances/${entry.slug}`)} variant="default">
									{entry.slug}
								</Link>
							</th>
							<td class="matrix__mono">
								{cellLabel(entry.lesser?.current, entry.lesser?.target, entry.lesser?.drift)}
							</td>
							<td class="matrix__mono">
								{cellLabel(entry.body?.current, entry.body?.target, entry.body?.drift)}
							</td>
							<td class="matrix__mono">{mcpLabel(entry)}</td>
							<td>
								<Badge color={badge.color} variant="filled" size="sm">{badge.text}</Badge>
							</td>
							<td class="matrix__actions">
								<Link {...linkProps(`/operator/instances/${entry.slug}`)} variant="ghost">
									Update
								</Link>
								{#if label === 'wire-stale' && onAction}
									<Button
										variant="outline"
										onclick={() => onAction?.(entry.slug, 'wire-mcp')}
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
		outline: 2px solid var(--ds-border-focus, #f59e0b);
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
