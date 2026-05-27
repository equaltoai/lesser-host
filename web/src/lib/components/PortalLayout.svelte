<script lang="ts">
	import type { Snippet } from 'svelte';
	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { logout } from 'src/lib/auth/logout';
	import { Button, Link, Text } from 'src/lib/ui';
	import BrandLockup from './BrandLockup.svelte';

	let { children }: { children: Snippet } = $props();

	async function handleLogout() {
		await logout();
		navigate('/login');
	}

	function isActive(path: string, exact: boolean = false): boolean {
		if (exact) return $currentPath === path;
		return $currentPath.startsWith(path);
	}

	function isPortalActive(): boolean {
		return isActive('/portal') && !isActive('/portal/souls');
	}
</script>

<div class="layout">
	<nav class="layout__sidebar">
		<div class="layout__brand">
			<BrandLockup edition="Control plane" />
		</div>
		<div class="layout__nav">
			<Link
				{...linkProps('/portal')}
				variant="ghost"
				aria-current={isPortalActive() ? 'page' : undefined}
			>
				Portal
			</Link>
			<Link
				{...linkProps('/portal/souls')}
				variant="ghost"
				aria-current={isActive('/portal/souls') ? 'page' : undefined}
			>
				Souls
			</Link>
			<Link
				{...linkProps('/trust')}
				variant="ghost"
				aria-current={isActive('/trust') ? 'page' : undefined}
			>
				Trust
			</Link>
			<Link
				{...linkProps('/tip-registry/register')}
				variant="ghost"
				aria-current={isActive('/tip-registry/register') ? 'page' : undefined}
			>
				Host registry
			</Link>
			<Link
				{...linkProps('/account')}
				variant="ghost"
				aria-current={isActive('/account', true) ? 'page' : undefined}
			>
				Account
			</Link>

			{#if $session && ($session.role === 'admin' || $session.role === 'operator')}
				<Link {...linkProps('/operator')} variant="ghost">Operator Console</Link>
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
				<Link {...linkProps('/login')} variant="default">Sign in</Link>
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
		background: var(--ds-bg-base, #f8f1e7);
		background-image: var(--ds-page-gradient);
		background-attachment: fixed;
	}
	.layout__sidebar {
		width: 260px;
		display: flex;
		flex-direction: column;
		border-right: 1px solid var(--ds-border-default, var(--gr-semantic-border-default));
		padding: var(--gr-spacing-scale-6) var(--gr-spacing-scale-4);
		gap: var(--gr-spacing-scale-6);
		background: var(--ds-bg-glass, var(--gr-semantic-background-surface));
		-webkit-backdrop-filter: blur(var(--ds-blur-nav, 24px));
		backdrop-filter: blur(var(--ds-blur-nav, 24px));
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
		border-top: 1px solid var(--ds-border-subtle, var(--gr-semantic-border-subtle));
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
