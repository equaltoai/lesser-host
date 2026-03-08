<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';

	import { IdProvider } from 'src/lib/greater/utils';
	import { consumeSafeAppTarget, currentPath, isSafeAppPath, navigate } from 'src/lib/router';

	import PortalLayout from 'src/lib/components/PortalLayout.svelte';
	import OperatorLayout from 'src/lib/components/OperatorLayout.svelte';

	import Account from 'src/pages/Account.svelte';
	import Home from 'src/pages/Home.svelte';
	import Login from 'src/pages/Login.svelte';
	import NotFound from 'src/pages/NotFound.svelte';
	import Operator from 'src/pages/Operator.svelte';
	import Portal from 'src/pages/Portal.svelte';
	import Setup from 'src/pages/Setup.svelte';
	import TipRegistryRegister from 'src/pages/TipRegistryRegister.svelte';
	import Trust from 'src/pages/Trust.svelte';

	let isOperatorRoute = $derived($currentPath === '/operator' || $currentPath.startsWith('/operator/'));
	let isPortalRoute = $derived(
		$currentPath === '/' ||
		$currentPath === '/portal' || $currentPath.startsWith('/portal/') ||
		$currentPath === '/trust' || $currentPath.startsWith('/trust/') ||
		$currentPath === '/tip-registry' || $currentPath === '/tip-registry/register' ||
		$currentPath === '/account'
	);

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
