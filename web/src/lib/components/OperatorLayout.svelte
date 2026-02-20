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

<div class="layout layout--operator">
	<nav class="layout__sidebar">
		<div class="layout__brand">
			<Heading level={2} size="xl">Operator Console</Heading>
		</div>
		<div class="layout__nav">
			<Button
				variant={isActive('/operator', true) ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator')}
			>
				Dashboard
			</Button>
			<Button
				variant={isActive('/operator/approvals/domains') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/approvals/domains')}
			>
				Domains
			</Button>
			<Button
				variant={isActive('/operator/approvals/users') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/approvals/users')}
			>
				Users
			</Button>
			<Button
				variant={isActive('/operator/approvals/external-instances') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/approvals/external-instances')}
			>
				External regs
			</Button>
			<Button
				variant={isActive('/operator/provisioning') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/provisioning/jobs')}
			>
				Provisioning
			</Button>
			<Button
				variant={isActive('/operator/instances') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/instances')}
			>
				Instances
			</Button>
			<Button
				variant={isActive('/operator/tip-registry') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/tip-registry')}
			>
				Tip registry
			</Button>
			<Button
				variant={isActive('/operator/audit') ? 'solid' : 'ghost'}
				onclick={() => navigate('/operator/audit')}
			>
				Audit
			</Button>
		</div>

		<div class="layout__footer">
			<Button variant="ghost" onclick={() => navigate('/portal')}>← Return to Portal</Button>
			<Button variant="ghost" onclick={() => navigate('/account')}>Account Settings</Button>
			{#if $session}
				<div class="layout__user">
					<Text size="sm" weight="medium">{$session.username}</Text>
					<Text size="sm" color="secondary">{$session.role}</Text>
				</div>
				<Button variant="outline" onclick={() => void handleLogout()}>Logout</Button>
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
	.layout--operator .layout__sidebar {
		/* Add a subtle tint or border to distinguish operator view if desired */
		border-right: 2px solid var(--gr-color-error-hover);
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
	.layout__footer :global(button) {
		justify-content: flex-start;
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
		min-width: 0;
	}

	@media (max-width: 768px) {
		.layout {
			flex-direction: column;
		}
		.layout__sidebar {
			width: 100%;
			border-right: none;
			border-bottom: 2px solid var(--gr-color-error-hover);
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
