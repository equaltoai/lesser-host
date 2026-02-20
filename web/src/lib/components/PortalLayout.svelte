<script lang="ts">
	import type { Snippet } from 'svelte';
	import { navigate, currentPath } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { logout } from 'src/lib/auth/logout';
	import { Button, Text, Heading } from 'src/lib/ui';

	let { children }: { children: Snippet } = $props();

	async function handleLogout() {
		await logout();
		navigate('/login');
	}

	function isActive(path: string, exact: boolean = false): boolean {
		if (exact) return $currentPath === path;
		return $currentPath.startsWith(path);
	}
</script>

<div class="layout">
	<nav class="layout__sidebar">
		<div class="layout__brand">
			<Heading level={2} size="xl">lesser.host</Heading>
		</div>
		<div class="layout__nav">
			<Button
				variant={isActive('/portal') ? 'solid' : 'ghost'}
				onclick={() => navigate('/portal')}
			>
				Portal
			</Button>
			<Button
				variant={isActive('/trust') ? 'solid' : 'ghost'}
				onclick={() => navigate('/trust')}
			>
				Trust
			</Button>
			<Button
				variant={isActive('/tip-registry/register') ? 'solid' : 'ghost'}
				onclick={() => navigate('/tip-registry/register')}
			>
				Host registry
			</Button>
			<Button
				variant={isActive('/account', true) ? 'solid' : 'ghost'}
				onclick={() => navigate('/account')}
			>
				Account
			</Button>

			{#if $session && ($session.role === 'admin' || $session.role === 'operator')}
				<Button
					variant="ghost"
					onclick={() => navigate('/operator')}
				>
					Operator Console
				</Button>
			{/if}
		</div>
		<div class="layout__footer">
			{#if $session}
				<div class="layout__user">
					<Text size="sm" weight="medium">{$session.username}</Text>
					<Text size="sm" color="secondary">{$session.role}</Text>
				</div>
				<Button variant="outline" onclick={() => void handleLogout()}>Logout</Button>
			{:else}
				<Button variant="outline" onclick={() => navigate('/login')}>Sign in</Button>
			{/if}
		</div>
	</nav>
	<main class="layout__content">
		{@render children()}
	</main>
</div>

<style>
	.layout {
		display: flex;
		min-height: 100vh;
		background: var(--gr-color-background);
	}
	.layout__sidebar {
		width: 260px;
		display: flex;
		flex-direction: column;
		border-right: 1px solid var(--gr-color-border);
		padding: var(--gr-spacing-scale-6) var(--gr-spacing-scale-4);
		gap: var(--gr-spacing-scale-6);
		background: var(--gr-color-background-surface);
	}
	.layout__brand {
		padding: 0 var(--gr-spacing-scale-2);
		margin-bottom: var(--gr-spacing-scale-2);
	}
	.layout__nav {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		flex: 1;
	}
	.layout__nav :global(button) {
		justify-content: flex-start;
	}
	.layout__footer {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-3);
		padding-top: var(--gr-spacing-scale-4);
		border-top: 1px solid var(--gr-color-border);
	}
	.layout__user {
		display: flex;
		flex-direction: column;
		padding: 0 var(--gr-spacing-scale-2);
	}
	.layout__content {
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0; /* allows children to truncate or shrink */
	}

	@media (max-width: 768px) {
		.layout {
			flex-direction: column;
		}
		.layout__sidebar {
			width: 100%;
			border-right: none;
			border-bottom: 1px solid var(--gr-color-border);
			padding: var(--gr-spacing-scale-4);
		}
		.layout__nav {
			flex-direction: row;
			flex-wrap: wrap;
			gap: var(--gr-spacing-scale-2);
			flex: unset;
		}
	}
</style>
