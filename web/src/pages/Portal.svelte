<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { PortalMeResponse } from 'src/lib/api/portal';
	import { getPortalMe } from 'src/lib/api/portal';
	import { logout } from 'src/lib/auth/logout';
	import { currentPath, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { Alert, Button, Card, Container, Heading, Spinner, Text } from 'src/lib/ui';

	import Billing from 'src/pages/portal/Billing.svelte';
	import InstanceConfig from 'src/pages/portal/InstanceConfig.svelte';
	import InstanceDetail from 'src/pages/portal/InstanceDetail.svelte';
	import InstanceBudgets from 'src/pages/portal/InstanceBudgets.svelte';
	import InstanceDomains from 'src/pages/portal/InstanceDomains.svelte';
	import InstanceKeys from 'src/pages/portal/InstanceKeys.svelte';
	import InstanceUsage from 'src/pages/portal/InstanceUsage.svelte';
	import Instances from 'src/pages/portal/Instances.svelte';

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let me = $state<PortalMeResponse | null>(null);

	type PortalRoute =
		| { kind: 'instances' }
		| { kind: 'instance'; slug: string }
		| { kind: 'instanceConfig'; slug: string }
		| { kind: 'instanceBudgets'; slug: string }
		| { kind: 'instanceUsage'; slug: string }
		| { kind: 'instanceDomains'; slug: string }
		| { kind: 'instanceKeys'; slug: string }
		| { kind: 'billing' }
		| { kind: 'notFound' };

	const portalRoute = $derived.by<PortalRoute>(() => {
		const path = $currentPath;
		if (!path.startsWith('/portal')) return { kind: 'instances' };

		const rest = path.slice('/portal'.length);
		const parts = rest.split('/').filter(Boolean);

		if (parts.length === 0) return { kind: 'instances' };
		if (parts[0] === 'instances') {
			if (parts[1]) {
				if (parts.length === 2) return { kind: 'instance', slug: parts[1] };
				if (parts[2] === 'config') return { kind: 'instanceConfig', slug: parts[1] };
				if (parts[2] === 'budgets') return { kind: 'instanceBudgets', slug: parts[1] };
				if (parts[2] === 'usage') return { kind: 'instanceUsage', slug: parts[1] };
				if (parts[2] === 'domains') return { kind: 'instanceDomains', slug: parts[1] };
				if (parts[2] === 'keys') return { kind: 'instanceKeys', slug: parts[1] };
				return { kind: 'notFound' };
			}
			return { kind: 'instances' };
		}
		if (parts[0] === 'billing') {
			if (parts.length === 1) return { kind: 'billing' };
			return { kind: 'notFound' };
		}

		return { kind: 'notFound' };
	});

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
		if (current.role === 'admin' || current.role === 'operator') {
			navigate('/operator');
			return;
		}

		loading = true;
		try {
			me = await getPortalMe(current.token);
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

	async function handleLogout() {
		await logout();
		navigate('/login');
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
				<Button variant="ghost" onclick={() => navigate('/portal/billing')}>Billing</Button>
				<Button variant="ghost" onclick={() => navigate('/account')}>Account</Button>
				<Button
					variant="ghost"
					onclick={() => void handleLogout()}
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
					<Heading level={2} size="xl">Account</Heading>
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

			{#if !$session}
				<Alert variant="warning" title="Signed out">
					<Text size="sm">Sign in to continue.</Text>
					<div class="portal__actions-inline">
						<Button variant="outline" onclick={() => navigate('/login')}>Sign in</Button>
					</div>
				</Alert>
			{:else if portalRoute.kind === 'instances'}
				<Instances token={$session.token} />
			{:else if portalRoute.kind === 'instance'}
				<InstanceDetail token={$session.token} slug={portalRoute.slug} />
			{:else if portalRoute.kind === 'instanceConfig'}
				<InstanceConfig token={$session.token} slug={portalRoute.slug} />
			{:else if portalRoute.kind === 'instanceBudgets'}
				<InstanceBudgets token={$session.token} slug={portalRoute.slug} />
			{:else if portalRoute.kind === 'instanceUsage'}
				<InstanceUsage token={$session.token} slug={portalRoute.slug} />
			{:else if portalRoute.kind === 'instanceDomains'}
				<InstanceDomains token={$session.token} slug={portalRoute.slug} />
			{:else if portalRoute.kind === 'instanceKeys'}
				<InstanceKeys token={$session.token} slug={portalRoute.slug} />
			{:else if portalRoute.kind === 'billing'}
				<Billing token={$session.token} />
			{:else}
				<Alert variant="warning" title="Not found">
					<Text size="sm">Unknown portal path.</Text>
				</Alert>
			{/if}
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
