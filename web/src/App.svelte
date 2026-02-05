<script lang="ts">
	import { IdProvider } from 'src/lib/greater/utils';
	import { currentPath } from 'src/lib/router';

	import Home from 'src/pages/Home.svelte';
	import NotFound from 'src/pages/NotFound.svelte';
	import Setup from 'src/pages/Setup.svelte';
</script>

<IdProvider>
	{#if $currentPath === '/'}
		<Home />
	{:else if $currentPath === '/setup'}
		<Setup />
	{:else}
		<NotFound />
	{/if}
</IdProvider>

