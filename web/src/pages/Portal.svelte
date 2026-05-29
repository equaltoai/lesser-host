<script lang="ts">
	import { onMount } from 'svelte';

	import type { ApiError } from 'src/lib/api/http';
	import type { PortalMeResponse } from 'src/lib/api/portal';
	import { getPortalMe } from 'src/lib/api/portal';
	import { logout } from 'src/lib/auth/logout';
	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { Alert, Button, Card, Container, Heading, Link, Spinner, Text } from 'src/lib/ui';

	import Billing from 'src/pages/portal/Billing.svelte';
	import InstanceConfig from 'src/pages/portal/InstanceConfig.svelte';
	import InstanceDetail from 'src/pages/portal/InstanceDetail.svelte';
	import InstanceBudgets from 'src/pages/portal/InstanceBudgets.svelte';
	import InstanceDomains from 'src/pages/portal/InstanceDomains.svelte';
	import InstanceKeys from 'src/pages/portal/InstanceKeys.svelte';
	import InstanceUsage from 'src/pages/portal/InstanceUsage.svelte';
	import Instances from 'src/pages/portal/Instances.svelte';
	import SoulDetail from 'src/pages/portal/SoulDetail.svelte';
	import SoulMintConversation from 'src/pages/portal/SoulMintConversation.svelte';
	import SoulRegister from 'src/pages/portal/SoulRegister.svelte';
	import Souls from 'src/pages/portal/Souls.svelte';
	import InstanceCost from 'src/pages/portal/InstanceCost.svelte';
	import InstanceSouls from 'src/pages/portal/InstanceSouls.svelte';
	import InstanceDetailShell from 'src/lib/components/InstanceDetailShell.svelte';

	let loading = $state(false);
	let errorMessage = $state<string | null>(null);
	let me = $state<PortalMeResponse | null>(null);

	type PortalRoute =
		| { kind: 'instances' }
		| { kind: 'instance'; slug: string }
		| { kind: 'instanceCost'; slug: string }
		| { kind: 'instanceConfig'; slug: string }
		| { kind: 'instanceBudgets'; slug: string }
		| { kind: 'instanceUsage'; slug: string }
		| { kind: 'instanceDomains'; slug: string }
		| { kind: 'instanceKeys'; slug: string }
		| { kind: 'instanceSouls'; slug: string }
		| { kind: 'souls' }
		| { kind: 'soulRegister' }
		| { kind: 'soulMint'; agentId: string }
		| { kind: 'soulAgent'; agentId: string }
		| { kind: 'billing' }
		| { kind: 'notFound' };

	// Project 42 M0.2 (#527): the outer page <h1> in this component used to
	// render "Portal Dashboard" for every /portal/* sub-route, which the
	// 2026-05-27 audit caught as wrong for /portal/billing (the h1 read
	// "Portal Dashboard" while the Billing eyebrow + title appeared
	// further down the page). The h1 + description are now derived from
	// portalRoute so each sub-route names itself correctly without copying
	// the inner section's eyebrow. Other sub-routes still default to the
	// existing "Portal Dashboard" copy until their own redesign milestone
	// owns the page.
	const portalRoute = $derived.by<PortalRoute>(() => {
		const path = $currentPath;
		if (!path.startsWith('/portal')) return { kind: 'instances' };

		const rest = path.slice('/portal'.length);
		const parts = rest.split('/').filter(Boolean);

		if (parts.length === 0) return { kind: 'instances' };
		if (parts[0] === 'instances') {
			if (parts[1]) {
				if (parts.length === 2) return { kind: 'instance', slug: parts[1] };
				if (parts[2] === 'overview') return { kind: 'instance', slug: parts[1] };
				if (parts[2] === 'cost') return { kind: 'instanceCost', slug: parts[1] };
				if (parts[2] === 'config') return { kind: 'instanceConfig', slug: parts[1] };
				if (parts[2] === 'budgets') return { kind: 'instanceBudgets', slug: parts[1] };
				if (parts[2] === 'usage') return { kind: 'instanceUsage', slug: parts[1] };
				if (parts[2] === 'domains') return { kind: 'instanceDomains', slug: parts[1] };
				if (parts[2] === 'keys') return { kind: 'instanceKeys', slug: parts[1] };
				if (parts[2] === 'souls') return { kind: 'instanceSouls', slug: parts[1] };
				return { kind: 'notFound' };
			}
			return { kind: 'instances' };
		}
		if (parts[0] === 'billing') {
			if (parts.length === 1) return { kind: 'billing' };
			return { kind: 'notFound' };
		}
		if (parts[0] === 'souls') {
			if (parts.length === 1) return { kind: 'souls' };
			if (parts[1] === 'register') return { kind: 'soulRegister' };
			if (parts[1] && parts[2] === 'mint') return { kind: 'soulMint', agentId: parts[1] };
			if (parts[1]) return { kind: 'soulAgent', agentId: parts[1] };
			return { kind: 'notFound' };
		}

		return { kind: 'notFound' };
	});

	// Project 42 M0.2 (#527): scoped title + description per sub-route.
	// /portal/billing now reads "Billing" (audit P0 fix); other sub-routes
	// continue to render the existing dashboard copy until their owning
	// milestone (M11 Billing UI / M13 Souls UI / M6 Instance Overview UI /
	// etc.) redesigns them.
	const pageTitle = $derived.by((): string => {
		switch (portalRoute.kind) {
			case 'billing':
				return 'Billing';
			default:
				return 'Portal Dashboard';
		}
	});

	const pageDescription = $derived.by((): string => {
		switch (portalRoute.kind) {
			case 'billing':
				return 'Credits, usage, and invoices for the signed-in account.';
			default:
				return 'Self-serve customer dashboard.';
		}
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

	onMount(() => {
		void loadMe();
	});
</script>

<Container size="lg" gutter="lg">
	<div class="portal">
		<header class="portal__header">
			<div class="portal__title">
				<Heading level={1}>{pageTitle}</Heading>
				<Text color="secondary">{pageDescription}</Text>
			</div>
			<div class="portal__actions">
				<Button variant="outline" onclick={() => void loadMe()} disabled={loading}>Refresh</Button>
				<Link {...linkProps('/portal/billing')} variant="ghost">Billing</Link>
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

			{#if portalRoute.kind === 'souls' || portalRoute.kind === 'soulRegister' || portalRoute.kind === 'soulMint' || portalRoute.kind === 'soulAgent'}
				<Alert variant="warning" title="Secondary soul route">
					<Text size="sm">
						The canonical soul creation, review, approval, and finalize flow now lives in the agent-first Simulacrum
						client served from Lesser at <span class="portal__mono">/l/*</span>. These portal soul routes remain
						available as fallback and operator-oriented tools.
					</Text>
				</Alert>
			{/if}

			{#if !$session}
				<Alert variant="warning" title="Signed out">
					<Text size="sm">Sign in to continue.</Text>
					<div class="portal__actions-inline">
						<Link {...linkProps('/login')} variant="default">Sign in</Link>
					</div>
				</Alert>
			{:else if portalRoute.kind === 'instances'}
				<Instances token={$session.token} />
			{:else if portalRoute.kind === 'instance'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceDetail token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceCost'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceCost token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceConfig'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceConfig token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceBudgets'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceBudgets token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceUsage'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceUsage token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceDomains'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceDomains token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceKeys'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceKeys token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'instanceSouls'}
				<InstanceDetailShell slug={portalRoute.slug}>
					<InstanceSouls token={$session.token} slug={portalRoute.slug} />
				</InstanceDetailShell>
			{:else if portalRoute.kind === 'souls'}
				<Souls token={$session.token} />
			{:else if portalRoute.kind === 'soulRegister'}
				<SoulRegister token={$session.token} />
			{:else if portalRoute.kind === 'soulMint'}
				<SoulMintConversation token={$session.token} agentId={portalRoute.agentId} />
			{:else if portalRoute.kind === 'soulAgent'}
				<SoulDetail token={$session.token} agentId={portalRoute.agentId} />
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
					<Link {...linkProps('/login')} variant="default">Sign in</Link>
				</div>
			</Alert>
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
