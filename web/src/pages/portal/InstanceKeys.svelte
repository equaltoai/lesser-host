<script lang="ts">
	import type { ApiError } from 'src/lib/api/http';
	import type { CreateInstanceKeyResponse } from 'src/lib/api/portalInstances';
	import { portalCreateInstanceKey } from 'src/lib/api/portalInstances';
	import { logout } from 'src/lib/auth/logout';
	import { navigate } from 'src/lib/router';
	import { Alert, Button, Card, Heading, Spinner, Text } from 'src/lib/ui';

	let { token, slug } = $props<{ token: string; slug: string }>();

	let createLoading = $state(false);
	let createError = $state<string | null>(null);
	let created = $state<CreateInstanceKeyResponse | null>(null);

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
</script>

<div class="keys">
	<header class="keys__header">
		<div class="keys__title">
			<Heading level={2} size="xl">Instance keys</Heading>
			<Text color="secondary">Create an instance key for <span class="keys__mono">{slug}</span>.</Text>
		</div>
		<div class="keys__actions">
			<Button variant="ghost" onclick={() => navigate(`/portal/instances/${slug}`)}>Back</Button>
		</div>
	</header>

	<Card variant="outlined" padding="lg">
		{#snippet header()}
			<Heading level={3} size="lg">Create key</Heading>
		{/snippet}
		<Text size="sm" color="secondary">
			The plaintext key is shown exactly once. Store it securely.
		</Text>

		<div class="keys__row">
			<Button variant="solid" onclick={() => void createKey()} disabled={createLoading}>Create key</Button>
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
			<Alert variant="warning" title="Copy this key now">
				<Text size="sm">
					Key ID: <span class="keys__mono">{created?.key_id}</span>
				</Text>
				<div class="keys__mono-row">
					<code class="keys__mono">{created?.key}</code>
					<Button variant="outline" onclick={() => void copy(created?.key || '')}>Copy key</Button>
				</div>
			</Alert>
		{/if}
	</Card>
</div>

<style>
	.keys {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
	}

	.keys__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		justify-content: space-between;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.keys__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
	}

	.keys__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
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
</style>
