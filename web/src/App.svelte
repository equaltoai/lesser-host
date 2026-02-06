<script lang="ts">
	import { IdProvider } from 'src/lib/greater/utils';
	import { currentPath } from 'src/lib/router';

	import Account from 'src/pages/Account.svelte';
	import Home from 'src/pages/Home.svelte';
	import Login from 'src/pages/Login.svelte';
	import NotFound from 'src/pages/NotFound.svelte';
	import Operator from 'src/pages/Operator.svelte';
	import Portal from 'src/pages/Portal.svelte';
	import Setup from 'src/pages/Setup.svelte';
</script>

<IdProvider>
	{#if $currentPath === '/'}
		<Home />
	{:else if $currentPath === '/login'}
		<Login />
	{:else if $currentPath === '/setup'}
		<Setup />
	{:else if $currentPath === '/portal' || $currentPath.startsWith('/portal/')}
		<Portal />
	{:else if $currentPath === '/operator' || $currentPath.startsWith('/operator/')}
		<Operator />
	{:else if $currentPath === '/account'}
		<Account />
	{:else}
		<NotFound />
	{/if}
</IdProvider>
