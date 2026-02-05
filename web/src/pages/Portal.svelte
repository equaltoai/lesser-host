<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { PortalMeResponse } from 'src/lib/api/portal';
	import { getPortalMe } from 'src/lib/api/portal';
	import { navigate } from 'src/lib/router';
	import { clearSession, session } from 'src/lib/session';
	import { Alert, Button, Card, Container, Heading, Spinner, Text } from 'src/lib/ui';

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let me = $state<PortalMeResponse | null>(null);

	function formatError(err: unknown): string {
		if (!err) return 'unknown error';
		const maybe = err as Partial<ApiError>;
		if (typeof maybe.message === 'string' && typeof maybe.status === 'number') {
			return `${maybe.message} (HTTP ${maybe.status}${maybe.code ? `, ${maybe.code}` : ''})`;
		}
		if (err instanceof Error) return err.message;
		return String(err);
	}

	async function loadMe() {
		errorMessage = null;
		me = null;

		const current = $session;
		if (!current) {
			navigate('/login');
			return;
		}

		loading = true;
		try {
			me = await getPortalMe(current.token);
		} catch (err) {
			const message = formatError(err);
			errorMessage = message;
			if ((err as Partial<ApiError>).status === 401) {
				clearSession();
			}
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		void loadMe();
	});
</script>

<Container size="lg" gutter="lg">
	<div class="portal">
		<header class="portal__header">
			<div class="portal__title">
				<Heading level={1}>Portal</Heading>
				<Text color="secondary">Self-serve customer portal.</Text>
			</div>
			<div class="portal__actions">
				<Button variant="outline" onclick={() => void loadMe()} disabled={loading}>Refresh</Button>
				<Button variant="ghost" onclick={() => navigate('/account')}>Account</Button>
				<Button
					variant="ghost"
					onclick={() => {
						clearSession();
						navigate('/login');
					}}
				>
					Logout
				</Button>
			</div>
		</header>

		{#if loading}
			<div class="portal__loading">
				<Spinner size="md" />
				<Text>Loading…</Text>
			</div>
		{:else if errorMessage}
			<Alert variant="error" title="Failed to load /api/v1/portal/me">{errorMessage}</Alert>
		{:else if me}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Who am I?</Heading>
				{/snippet}
				<div class="portal__me">
					<Text size="sm">
						Username: <span class="portal__mono">{me.username}</span>
					</Text>
					<Text size="sm">
						Role: <span class="portal__mono">{me.role}</span>
					</Text>
					<Text size="sm">
						Method: <span class="portal__mono">{me.method || '—'}</span>
					</Text>
					<Text size="sm">
						Email: <span class="portal__mono">{me.email || '—'}</span>
					</Text>
				</div>
			</Card>
		{:else}
			<Alert variant="warning" title="No session">
				<Text size="sm">You are signed out.</Text>
				<div class="portal__actions-inline">
					<Button variant="outline" onclick={() => navigate('/login')}>Sign in</Button>
				</div>
			</Alert>
		{/if}

		{#if $session}
			<Card variant="outlined" padding="lg">
				{#snippet header()}
					<Heading level={2} size="xl">Session</Heading>
				{/snippet}
				<Text size="sm">
					Expires: <span class="portal__mono">{$session.expiresAt}</span>
				</Text>
			</Card>
		{/if}
	</div>
</Container>

<style>
	.portal {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-6);
		padding: var(--gr-spacing-scale-12) 0;
	}

	.portal__header {
		display: flex;
		gap: var(--gr-spacing-scale-4);
		align-items: flex-start;
		justify-content: space-between;
		flex-wrap: wrap;
	}

	.portal__title {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.portal__actions {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.portal__loading {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
	}

	.portal__me {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-2);
	}

	.portal__actions-inline {
		display: flex;
		gap: var(--gr-spacing-scale-2);
		margin-top: var(--gr-spacing-scale-3);
	}

	.portal__mono {
		font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New',
			monospace;
	}
</style>

