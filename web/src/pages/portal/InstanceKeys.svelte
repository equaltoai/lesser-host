<!--
@component
InstanceKeys — the Keys tab on Instance Detail for managed instances.

Project 42 M9 (#543): re-skinned with a table layout (Token ID, Scopes,
Created, Last used, Revoke). Token IDs are masked (prefix + "..." + suffix)
with a safe copy button. Raw keys are never re-exposed — they are only shown
once at creation time, consistent with host's sha256(key) storage posture.
Scopes are shown as "—" because scope metadata is not available on the
current InstanceKeyListItem DTO.

Posture invariants:
  - Strict-no-inline-CSP safe: no inline scripts, no inline styles, no
    third-party origins.
  - Trust-API instance-auth: raw keys are never returned on re-read, never
    stored, and never logged. The masked token IDs shown in the table are
    non-secret identifiers; the copy button copies the full ID (safe).
  - Multi-tenant isolation: all data fetches use instance-scoped endpoints.

Source: issue #543, design fixture portal-pages.jsx:451–476
-->
<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { CreateInstanceKeyResponse, InstanceKeyListItem } from 'src/lib/api/portalInstances';
	import { portalCreateInstanceKey, portalListInstanceKeys, portalRevokeInstanceKey } from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import {
		Alert,
		Button,
		CopyButton,
		Spinner,
		Text,
	} from 'src/lib/ui';
	import { Panel } from 'src/lib/shell';

	let { token, slug } = $props<{ token: string; slug: string }>();

	let createLoading = $state(false);
	let createError = $state<string | null>(null);
	let created = $state<CreateInstanceKeyResponse | null>(null);

	let keysLoading = $state(false);
	let keysError = $state<string | null>(null);
	let keys = $state<InstanceKeyListItem[]>([]);

	let revoking = $state<string | null>(null);

	let copyNotice = $state<string | null>(null);

	/**
	 * Mask a token ID for display. Shows "sk_XXXX...XXXX" format:
	 * first 8 characters + "..." + last 4 characters.
	 * Falls back to full ID if it's too short to mask.
	 */
	function maskKeyId(id: string): string {
		const trimmed = (id || '').trim();
		if (!trimmed) return '—';
		if (trimmed.length <= 14) return trimmed;
		return `${trimmed.slice(0, 8)}...${trimmed.slice(-4)}`;
	}

	function activeCount(): number {
		return keys.filter((k) => !k.revoked_at).length;
	}

	function formatTime(raw: string | undefined): string {
		if (!raw || raw.trim() === '') return '—';
		const trimmed = raw.trim();
		if (trimmed.startsWith('0001-01-01T00:00:00')) return '—';
		try {
			const d = new Date(trimmed);
			if (isNaN(d.getTime())) return trimmed;
			return d.toLocaleDateString('en-US', {
				year: 'numeric',
				month: 'short',
				day: 'numeric',
			});
		} catch {
			return trimmed;
		}
	}

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function copy(text: string) {
		copyNotice = null;
		try {
			await navigator.clipboard.writeText(text);
			copyNotice = 'Copied to clipboard.';
			window.setTimeout(() => {
				copyNotice = null;
			}, 1500);
		} catch {
			copyNotice = 'Copy failed.';
		}
	}

	async function createKey() {
		createError = null;
		created = null;

		createLoading = true;
		try {
			created = await portalCreateInstanceKey(token, slug);
			void loadKeys();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			createError = formatError(err);
		} finally {
			createLoading = false;
		}
	}

	async function loadKeys() {
		keysError = null;
		keysLoading = true;
		try {
			const res = await portalListInstanceKeys(token, slug, 50);
			keys = res.keys ?? [];
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			keysError = formatError(err);
		} finally {
			keysLoading = false;
		}
	}

	async function revokeKey(keyId: string) {
		if (!keyId.trim()) return;
		if (!window.confirm(`Revoke key ${maskKeyId(keyId)}? This immediately invalidates it.`)) return;

		revoking = keyId;
		try {
			await portalRevokeInstanceKey(token, slug, keyId);
			void loadKeys();
		} catch (err) {
			if ((err as Partial<ApiError>).status === 401) {
				await logout();
				navigate('/login');
				return;
			}
			keysError = formatError(err);
		} finally {
			revoking = null;
		}
	}

	onMount(() => {
		void loadKeys();
	});
</script>

<div class="keys">
	<Panel title="API keys" headerLevel={2}>
		{#snippet actions()}
			<Button variant="outline" size="sm" onclick={() => void createKey()} disabled={createLoading}>
				Issue key
			</Button>
		{/snippet}

		<div class="keys__eyebrow">
			<Text size="xs" color="secondary">
				{activeCount()} active
			</Text>
		</div>

		<div class="keys__hint">
			<Text size="sm" color="secondary">
				Scoped tokens; rotate routinely. Never share a secret key in chat or logs. Raw keys are shown exactly
				once at creation — host stores only <span class="keys__mono">sha256(key)</span>, never the raw key.
			</Text>
		</div>

		{#if createLoading}
			<div class="keys__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Creating…</Text>
			</div>
		{/if}

		{#if createError}
			<Alert variant="error" title="Create key failed">{createError}</Alert>
		{/if}

		{#if copyNotice}
			<Alert variant="info" title="Clipboard">{copyNotice}</Alert>
		{/if}

		{#if created}
			<Alert variant="warning" title="Copy this key now — it will not be shown again">
				<Text size="sm">
					Key ID: <span class="keys__mono">{created.key_id}</span>
				</Text>
				<div class="keys__mono-row">
					<code class="keys__mono">{created.key}</code>
					<Button variant="outline" onclick={() => void copy(created?.key || '')}>Copy key</Button>
					<Button variant="ghost" onclick={() => (created = null)}>Dismiss</Button>
				</div>
			</Alert>
		{/if}

		{#if keysLoading && keys.length === 0}
			<div class="keys__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Loading…</Text>
			</div>
		{:else if keysError}
			<Alert variant="error" title="Keys">{keysError}</Alert>
		{:else if keys.length === 0}
			<div class="keys__empty">
				<Text size="sm" color="secondary">No keys created yet.</Text>
			</div>
		{:else}
			<div class="keys__table-wrap">
				<table class="keys__table">
					<thead>
						<tr>
							<th>Token</th>
							<th>Scopes</th>
							<th>Created</th>
							<th>Last used</th>
							<th></th>
						</tr>
					</thead>
					<tbody>
						{#each keys as k (k.id)}
							<tr class:keys__row--revoked={Boolean(k.revoked_at)}>
								<td class="keys__cell-token">
									<span class="keys__mono">{maskKeyId(k.id)}</span>
									<CopyButton text={k.id} variant="icon" />
								</td>
								<td class="keys__cell-scopes">
									<Text size="xs" color="secondary" class="keys__mono">—</Text>
								</td>
								<td class="keys__cell-date">
									<Text size="xs" color="secondary" class="keys__mono">{formatTime(k.created_at)}</Text>
								</td>
								<td class="keys__cell-date">
									<Text size="xs" class="keys__mono">{formatTime(k.last_used_at)}</Text>
								</td>
								<td class="keys__cell-action">
									{#if !k.revoked_at}
										<Button
											variant="ghost"
											size="sm"
											onclick={() => void revokeKey(k.id)}
											disabled={revoking === k.id}
										>
											Revoke
										</Button>
									{:else}
										<Text size="xs" color="secondary">Revoked</Text>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</Panel>
</div>

<style>
	.keys {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.keys__eyebrow {
		display: block;
		margin-bottom: var(--ds-space-1);
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}

	.keys__hint {
		margin-bottom: var(--ds-space-4);
	}

	.keys__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
		margin-bottom: var(--gr-spacing-scale-3);
	}

	.keys__empty {
		padding: var(--ds-space-8) 0;
		text-align: center;
	}

	.keys__mono-row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
		margin-top: var(--gr-spacing-scale-2);
	}

	.keys__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
		word-break: break-word;
	}

	.keys__table-wrap {
		overflow-x: auto;
	}

	.keys__table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--ds-font-size-sm, 0.875rem);
	}

	.keys__table th {
		text-align: left;
		font-weight: var(--ds-weight-semibold, 600);
		color: var(--ds-fg-2, var(--gr-color-foreground-secondary));
		padding: var(--ds-space-2) var(--ds-space-3);
		border-bottom: 1px solid var(--gr-color-border-default);
		font-size: var(--ds-font-size-xs, 0.75rem);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}

	.keys__table td {
		padding: var(--ds-space-2) var(--ds-space-3);
		border-bottom: 1px solid var(--gr-color-border-subtle);
		vertical-align: middle;
	}

	.keys__table tbody tr:last-child td {
		border-bottom: none;
	}

	.keys__cell-token {
		display: flex;
		align-items: center;
		gap: var(--ds-space-2);
	}

	.keys__cell-scopes {
		white-space: nowrap;
	}

	.keys__cell-date {
		white-space: nowrap;
	}

	.keys__cell-action {
		width: 1%;
		white-space: nowrap;
		text-align: right;
	}

	.keys__row--revoked td {
		opacity: 0.5;
	}
</style>
