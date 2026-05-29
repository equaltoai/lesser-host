<script lang="ts">
	// FaceTheory emits CSS discovered from the rendered App surface into the
	// SSR HTML. Keep Project 39 global styles on this graph (not only
	// `main.ts`) so lab receives the Agent Genesis theme before hydration.
	import 'src/lib/styles/greater/tokens.css';
	import 'src/lib/tokens';
	import 'src/lib/styles/greater/primitives.css';
	import 'src/lib/styles/greater/shell.css';
	import 'src/lib/styles/greater/host-platform.css';
	import 'src/lib/styles/m1-primitives.css';
	import 'src/lib/tokens/operator-chrome.css';
	import './app.css';

	import { onMount } from 'svelte';
	import { get } from 'svelte/store';

	import { IdProvider } from 'src/lib/greater/utils';
	import { consumeSafeAppTarget, currentPath, isSafeAppPath, navigate } from 'src/lib/router';

	import PortalLayout from 'src/lib/components/PortalLayout.svelte';
	import PortalShell from 'src/lib/components/PortalShell.svelte';
	import OperatorLayout from 'src/lib/components/OperatorLayout.svelte';

	import Account from 'src/pages/Account.svelte';
	import Home from 'src/pages/Home.svelte';
	import Login from 'src/pages/Login.svelte';
	import NotFound from 'src/pages/NotFound.svelte';
	import Operator from 'src/pages/Operator.svelte';
	import Portal from 'src/pages/Portal.svelte';
	import PortalFleet from 'src/pages/portal/PortalFleet.svelte';
	import { session } from 'src/lib/session';
	import Setup from 'src/pages/Setup.svelte';
	import TipRegistryRegister from 'src/pages/TipRegistryRegister.svelte';
	import PortalTrust from 'src/pages/portal/Trust.svelte';
	import Trust from 'src/pages/Trust.svelte';

	let isOperatorRoute = $derived($currentPath === '/operator' || $currentPath.startsWith('/operator/'));
	// Project 39 customer surfaces render inside the shell. /portal is the
	// canonical fleet view; /portal/fleet remains a compatibility alias.
	// Project 42 M0.1 (#526): /portal/trust and /portal/account are now
	// canonical paths for Trust + Account inside portal chrome; /trust and
	// /account remain backward-compat aliases so existing bookmarks /
	// deep-links continue to work without redirect (and the public
	// attestation inspector subroute under /trust/attestations/* keeps
	// resolving via the same Trust component).
	let isPortalShellRoute = $derived(
		$currentPath === '/portal' ||
			$currentPath === '/portal/fleet' ||
			$currentPath.startsWith('/portal/fleet/') ||
			$currentPath.startsWith('/portal/') ||
			$currentPath === '/trust' ||
			$currentPath.startsWith('/trust/') ||
			$currentPath === '/account' ||
			$currentPath === '/tip-registry' ||
			$currentPath === '/tip-registry/register'
	);
	let isPortalRoute = $derived(
		!isPortalShellRoute && (
			$currentPath === '/' ||
			$currentPath === '/trust' ||
			$currentPath === '/account'
		)
	);
	// Project 42 M0.1: helpers for /portal/trust + /portal/account dispatch.
	// Project 42 M15 (#550): /portal/trust (exact) routes to the new M15
	// Trust dashboard; /portal/trust/attestations/* routes to the existing
	// public attestation inspector so attestation lookup is preserved inside
	// portal chrome.
	let isPortalTrustDashboardRoute = $derived(
		$currentPath === '/portal/trust'
	);
	let isPortalTrustAttestationRoute = $derived(
		$currentPath.startsWith('/portal/trust/attestations')
	);
	let isPortalTrustRoute = $derived(
		isPortalTrustDashboardRoute || isPortalTrustAttestationRoute
	);
	let isPortalAccountRoute = $derived($currentPath === '/portal/account');

	onMount(() => {
		if (!isSafeAppPath() || get(currentPath) !== '/') return;
		const target = consumeSafeAppTarget();
		if (target && target !== '/') {
			navigate(target);
		}
	});
</script>

<IdProvider>
	{#if isOperatorRoute}
		<OperatorLayout>
			<Operator />
		</OperatorLayout>
	{:else if isPortalShellRoute}
		<PortalShell>
			{#if $currentPath === '/portal' || $currentPath === '/portal/fleet' || $currentPath.startsWith('/portal/fleet/')}
				<PortalFleet token={$session?.token ?? ''} />
			{:else if isPortalTrustRoute}
				{#if isPortalTrustDashboardRoute}
					<PortalTrust token={$session?.token ?? ''} />
				{:else}
					<Trust />
				{/if}
			{:else if isPortalAccountRoute}
				<Account />
			{:else if $currentPath.startsWith('/portal/')}
				<Portal />
			{:else if $currentPath === '/trust' || $currentPath.startsWith('/trust/')}
				<Trust />
			{:else if $currentPath === '/account'}
				<Account />
			{:else if $currentPath === '/tip-registry' || $currentPath === '/tip-registry/register'}
				<TipRegistryRegister />
			{/if}
		</PortalShell>
	{:else if isPortalRoute}
		<PortalLayout>
			{#if $currentPath === '/'}
				<Home />
			{:else if $currentPath === '/portal' || $currentPath.startsWith('/portal/')}
				<Portal />
			{:else if $currentPath === '/trust' || $currentPath.startsWith('/trust/')}
				<Trust />
			{:else if $currentPath === '/account'}
				<Account />
			{:else if $currentPath === '/tip-registry' || $currentPath === '/tip-registry/register'}
				<TipRegistryRegister />
			{/if}
		</PortalLayout>
	{:else if $currentPath === '/login'}
		<Login />
	{:else if $currentPath === '/setup'}
		<Setup />
	{:else}
		<PortalLayout>
			<NotFound />
		</PortalLayout>
	{/if}
</IdProvider>
