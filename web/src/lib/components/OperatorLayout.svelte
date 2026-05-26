<script lang="ts">
	import type { Snippet } from 'svelte';
	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { logout } from 'src/lib/auth/logout';
	import { Button, Heading, Link, Text } from 'src/lib/ui';

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

<!--
	Project 39 M2.1 (issue #427): the wrapper carries both `layout--operator`
	(legacy class used by the original sidebar tint) and `shell--operator`
	(canonical design-handoff class) so the operator-chrome token overrides
	in `src/lib/tokens/operator-chrome.css` apply uniformly. The data-shell
	attribute is a stable hook for future analytics and gov-rubric checks.
-->
<div class="layout layout--operator shell--operator" data-shell="operator">
	<nav class="layout__sidebar">
		<div class="layout__brand">
			<Heading level={2} size="xl">Operator Console</Heading>
			<Text size="sm" color="secondary">Managed-hosting control plane</Text>
		</div>
		<div class="layout__nav">
			<Link
				{...linkProps('/operator')}
				variant="ghost"
				aria-current={isActive('/operator', true) ? 'page' : undefined}
			>
				Dashboard
			</Link>
			<Link
				{...linkProps('/operator/approvals/domains')}
				variant="ghost"
				aria-current={isActive('/operator/approvals/domains') ? 'page' : undefined}
			>
				Domains
			</Link>
			<Link
				{...linkProps('/operator/approvals/users')}
				variant="ghost"
				aria-current={isActive('/operator/approvals/users') ? 'page' : undefined}
			>
				Users
			</Link>
			<Link
				{...linkProps('/operator/approvals/external-instances')}
				variant="ghost"
				aria-current={isActive('/operator/approvals/external-instances') ? 'page' : undefined}
			>
				External regs
			</Link>
			<Link
				{...linkProps('/operator/provisioning/jobs')}
				variant="ghost"
				aria-current={isActive('/operator/provisioning') ? 'page' : undefined}
			>
				Provisioning
			</Link>
			<Link
				{...linkProps('/operator/releases')}
				variant="ghost"
				aria-current={isActive('/operator/releases') ? 'page' : undefined}
			>
				Releases
			</Link>
			<Link
				{...linkProps('/operator/instances')}
				variant="ghost"
				aria-current={isActive('/operator/instances') ? 'page' : undefined}
			>
				Instances
			</Link>
			<Link
				{...linkProps('/operator/tip-registry')}
				variant="ghost"
				aria-current={isActive('/operator/tip-registry') ? 'page' : undefined}
			>
				Tip registry
			</Link>
			<Link
				{...linkProps('/operator/soul')}
				variant="ghost"
				aria-current={isActive('/operator/soul') ? 'page' : undefined}
			>
				Souls
			</Link>
			<Link
				{...linkProps('/operator/audit')}
				variant="ghost"
				aria-current={isActive('/operator/audit') ? 'page' : undefined}
			>
				Audit
			</Link>
		</div>

		<div class="layout__footer">
			<Link {...linkProps('/portal')} variant="ghost">← Return to Portal</Link>
			<Link {...linkProps('/account')} variant="ghost">Account Settings</Link>
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
	/*
	 * The operator-specific sidebar seam is owned by operator-chrome.css so
	 * the amber tint reads against the warm-charcoal page gradient. The
	 * legacy `--gr-color-error-hover` red is replaced — see M2.1 design
	 * handoff (docs/design/web-ui-rework-2026-05-24/project/assets/app.css).
	 */
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
			/* Operator-specific bottom-edge seam owned by operator-chrome.css */
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
