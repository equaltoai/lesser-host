<!--
@component
M2ShellFixture — isolated visual fixture rendering PortalShell with
pre-seeded session and mock API data for design-fidelity evidence capture.

Renders the full PortalShell chrome (sidebar, topbar, user chip, bell
placeholder, ⌘K trigger) with realistic mock instances and portalMe data.
Not mounted by any customer portal route — this file exists solely for
local visual review and headless PNG capture at 1440×900.

Strict-CSP safe: no inline styles, no inline event handlers.

@license AGPL-3.0-only
@public
-->
<script lang="ts">
	import { setSession } from 'src/lib/session';
	import PortalShell from '../PortalShell.svelte';

	// Pre-seed session so PortalShell user chip renders with
	// display_name from the portalMe mock and role subtext.
	// Must run at component init (before PortalShell mounts)
	// so onMount → loadSidebarData sees a valid token.
	setSession({
		tokenType: 'Bearer',
		token: 'fixture-mock-token',
		expiresAt: new Date(Date.now() + 3600_000).toISOString(),
		username: 'alice',
		role: 'customer',
		method: 'wallet',
		walletAddress: '0x4f29a8b3c1d5e7f9a2b4c6d8e0f1a3b5c7d9e2f4',
	});
</script>

<div class="m2-fixture">
	<PortalShell>
		<div class="m2-fixture__content">
			<h1 class="m2-fixture__title">Fleet Overview</h1>
			<p class="m2-fixture__desc">
				This is a fixture content surface rendered inside PortalShell's
				PageFrame. It exists solely for visual evidence capture.
			</p>
			<div class="m2-fixture__card">
				<h2>Welcome back, Alice</h2>
				<p>You have 4 managed instances across 3 regions.</p>
			</div>
			<div class="m2-fixture__card">
				<h2>Recent Activity</h2>
				<p>my-instance — v2.4.1 deployed 2 hours ago (us-east-1)</p>
				<p>staging-env — warning: disk usage at 82% (eu-west-1)</p>
				<p>dev-sandbox — error: provision failed, check logs (us-west-2)</p>
			</div>
		</div>
	</PortalShell>
</div>

<style>
	.m2-fixture {
		min-height: 100vh;
	}

	.m2-fixture__content {
		padding: var(--ds-space-8, 2rem);
		max-width: 56rem;
	}

	.m2-fixture__title {
		font-size: 1.5rem;
		font-weight: 700;
		color: var(--ds-fg-1, #332116);
		margin: 0 0 0.5rem;
	}

	.m2-fixture__desc {
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
		margin: 0 0 2rem;
	}

	.m2-fixture__card {
		background: var(--ds-bg-surface, rgba(255, 251, 245, 1));
		border: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		border-radius: var(--ds-radius-lg, 1rem);
		padding: var(--ds-space-5, 1.25rem);
		margin-bottom: var(--ds-space-4, 1rem);
	}

	.m2-fixture__card h2 {
		font-size: 1rem;
		font-weight: 600;
		color: var(--ds-fg-1, #332116);
		margin: 0 0 0.5rem;
	}

	.m2-fixture__card p {
		font-size: 0.875rem;
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
		margin: 0 0 0.25rem;
	}

	.m2-fixture__card p:last-child {
		margin-bottom: 0;
	}
</style>
