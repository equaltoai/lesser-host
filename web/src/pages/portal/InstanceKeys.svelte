<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { CreateInstanceKeyResponse, InstanceKeyListItem } from 'src/lib/api/portalInstances';
	import { portalCreateInstanceKey, portalListInstanceKeys, portalRevokeInstanceKey } from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Spinner, Text } from 'src/lib/ui';
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
		if (!window.confirm(`Revoke key ${keyId}? This immediately invalidates it.`)) return;

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
	<Panel title="Create key" headerLevel={2}>
		<Text size="sm" color="secondary">
			The plaintext key is shown exactly once. Store it securely — host stores only
			<span class="keys__mono">sha256(key)</span>, never the raw key, so it cannot be re-shown.
		</Text>

		<div class="keys__row">
			<Button variant="solid" onclick={() => void createKey()} disabled={createLoading}>
				Create key
			</Button>
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
					Key ID: <span class="keys__mono">{created?.key_id}</span>
				</Text>
				<div class="keys__mono-row">
					<code class="keys__mono">{created?.key}</code>
					<Button variant="outline" onclick={() => void copy(created?.key || '')}>Copy key</Button>
					<Button variant="ghost" onclick={() => (created = null)}>Dismiss</Button>
				</div>
			</Alert>
		{/if}
	</Panel>

	<Panel title="Existing keys" headerLevel={2}>
		{#snippet actions()}
			<Button variant="outline" onclick={() => void loadKeys()} disabled={keysLoading}>Refresh</Button>
		{/snippet}

		<Text size="sm" color="secondary">
			Keys can overlap. Revoke old keys after you’ve updated your instance.
		</Text>

		{#if keysLoading && keys.length === 0}
			<div class="keys__loading-inline">
				<Spinner size="sm" />
				<Text size="sm">Loading…</Text>
			</div>
		{:else if keysError}
			<Alert variant="error" title="Keys">{keysError}</Alert>
		{:else if keys.length === 0}
			<Text size="sm" color="secondary">No keys created yet.</Text>
		{:else}
			<ul class="keys__list">
				{#each keys as k (k.id)}
					<li class="keys__list-item">
						<div class="keys__list-main">
							<Text size="sm">
								Key ID: <span class="keys__mono">{k.id}</span>
							</Text>
							<Text size="sm" color="secondary">Created: {k.created_at}</Text>
							{#if k.revoked_at}
								<Text size="sm" color="secondary">Revoked: {k.revoked_at}</Text>
							{/if}
						</div>
						<div class="keys__list-actions">
							{#if !k.revoked_at}
								<Button
									variant="outline"
									onclick={() => void revokeKey(k.id)}
									disabled={revoking === k.id}
								>
									Revoke
								</Button>
							{/if}
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</Panel>
</div>

<style>
	.keys {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.keys__row {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-4);
		flex-wrap: wrap;
	}

	.keys__loading-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		margin-top: var(--gr-spacing-scale-3);
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

	.keys__list {
		list-style: none;
		padding: 0;
		margin: var(--gr-spacing-scale-4) 0 0 0;
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
	}

	.keys__list-item {
		display: flex;
		justify-content: space-between;
		gap: var(--gr-spacing-scale-4);
		padding: var(--gr-spacing-scale-3);
		border: 1px solid var(--gr-color-border-default);
		border-radius: var(--gr-radius-md);
		flex-wrap: wrap;
	}

	.keys__list-main {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.keys__list-actions {
		display: flex;
		align-items: flex-start;
	}
</style>
