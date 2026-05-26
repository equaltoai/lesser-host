<!--
@component
PortalShell — greater-components Shell + Sidebar + Topbar + PageFrame
composition for the customer portal's new fleet view.

This is an ADDITIVE component for M0.11:
  - It does NOT replace `PortalLayout.svelte` (kept untouched for the
    legacy `/portal` / `/portal/instances` paths so byte-for-byte
    compatibility is preserved).
  - It is currently wired only by the new `/portal/fleet` route.
  - Subsequent milestones may migrate additional portal sub-views into
    this shell; this PR doesn't speculate.

Behavior:
  - `Shell` provides the root grid with a `<main>` landmark
    (`mainLabel="Customer portal"`).
  - `Sidebar` provides the primary `<nav>` landmark with route-aware
    button states (current path drives the `solid`/`ghost` variant).
  - `Topbar` provides the `<header>` landmark with the brand on the
    start slot and the session user info / logout on the end slot.
  - `PageFrame` wraps the main content with a sensible max-width.
  - `CommandPalette` mounts inside the shell and opens on ⌘K (or ⌃K on
    non-macOS keyboards) via a document-level keydown listener. Commands
    cover the portal route map and any per-instance "Open <slug>"
    entries the caller passes through `instanceCommands`.

Posture:
  - Strict-CSP safe: no inline event handlers; no inline styles; no
    third-party origins. Status communication uses text labels in
    buttons, never color-only.
  - Same-origin enforcement preserved by upstream Shell components
    (sidebar `<nav>`, topbar `<header>`, main `<main>` are emitted as
    DOM nodes, not via innerHTML).
  - The ⌘K listener is wired in onMount and torn down in onDestroy so
    it never leaks across navigations.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.11
Issue: equaltoai/lesser-host#391
-->

<script lang="ts">
	import { onMount } from 'svelte';
	import type { Snippet } from 'svelte';

	import {
		Shell,
		Sidebar,
		Topbar,
		PageFrame,
		CommandPalette,
		type CommandPaletteItem,
		type CommandPaletteGroup,
	} from 'src/lib/shell';
	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { logout } from 'src/lib/auth/logout';
	import { Button, Heading, Link, Text } from 'src/lib/ui';
	import { portalFleetInstances, clearPortalFleetState } from 'src/lib/portalFleetState';

	interface Props {
		/** Page content rendered inside the shell's PageFrame. */
		children: Snippet;
		/**
		 * Optional per-instance "Open <slug>" command palette entries
		 * passed in directly. When omitted (the typical case), the shell
		 * derives instance entries from the shared `portalFleetInstances`
		 * store, which `PortalFleet` populates on every successful load.
		 * Explicit passing here is the escape hatch for tests / hosts
		 * that want to override the store-derived list.
		 */
		instanceCommands?: CommandPaletteItem[];
	}

	let { children, instanceCommands }: Props = $props();

	// Auto-derive per-instance commands from the shared fleet store when
	// the caller hasn't passed an explicit list. Each store entry becomes
	// an `instance.<slug>` command with the slug + region + version
	// surfaced as the description so fuzzy-search matches "my-instance",
	// "us-east-1", and "v1.4.12" alike.
	const derivedInstanceCommands = $derived<CommandPaletteItem[]>(
		instanceCommands ??
			$portalFleetInstances.map((inst) => {
				const descriptionParts = [];
				if (inst.hosted_region) descriptionParts.push(inst.hosted_region);
				if (inst.lesser_version) descriptionParts.push(inst.lesser_version);
				const description = descriptionParts.join(' • ') || undefined;
				return {
					id: `instance.${inst.slug}`,
					label: `Open ${inst.slug}`,
					description: description ?? 'Managed instance',
					keywords: [
						inst.slug,
						...(inst.hosted_region ? [inst.hosted_region] : []),
						...(inst.lesser_version ? [inst.lesser_version] : []),
						'instance',
					],
				};
			})
	);

	let paletteOpen = $state(false);
	let paletteQuery = $state('');

	function isActive(path: string, exact: boolean = false): boolean {
		if (exact) return $currentPath === path;
		return $currentPath.startsWith(path);
	}

	function isPortalFleetActive(): boolean {
		return isActive('/portal/fleet');
	}

	function isLegacyPortalActive(): boolean {
		// Matches /portal and /portal/instances + nested paths, but NOT
		// /portal/fleet, /portal/souls, or /portal/billing — which each
		// get their own button state below.
		return (
			isActive('/portal') &&
			!isActive('/portal/fleet') &&
			!isActive('/portal/souls') &&
			!isActive('/portal/billing')
		);
	}

	async function handleLogout() {
		// Drop the fleet snapshot before the next user signs in — an old
		// user's slugs must never leak into a new user's palette.
		clearPortalFleetState();
		await logout();
		navigate('/login');
	}

	const navigationGroup = $derived<CommandPaletteGroup>({
		id: 'nav',
		label: 'Navigate',
		items: [
			{
				id: 'nav.fleet',
				label: 'Go to Fleet',
				description: 'Customer fleet view (new shell)',
				keywords: ['portal', 'instances', 'overview'],
				shortcut: 'F',
			},
			{
				id: 'nav.legacy-instances',
				label: 'Go to Instances (legacy)',
				description: 'Existing /portal/instances list',
				keywords: ['portal', 'list', 'old'],
			},
			{
				id: 'nav.souls',
				label: 'Go to Souls',
				description: 'Soul registry (fallback)',
				keywords: ['agent', 'registry', 'identity'],
			},
			{
				id: 'nav.trust',
				label: 'Go to Trust',
				description: 'Trust attestations',
				keywords: ['attestation', 'evidence'],
			},
			{
				id: 'nav.account',
				label: 'Go to Account',
				description: 'Account & session',
				keywords: ['user', 'profile'],
			},
			{
				id: 'nav.billing',
				label: 'Go to Billing',
				description: 'Credits & invoices',
				keywords: ['credits', 'invoice', 'payment'],
			},
			...($session && ($session.role === 'admin' || $session.role === 'operator')
				? [
						{
							id: 'nav.operator',
							label: 'Go to Operator Console',
							description: 'Admin / operator surface',
							keywords: ['admin', 'operator', 'console'],
						},
				  ]
				: []),
		],
	});

	const actionsGroup = $derived<CommandPaletteGroup>({
		id: 'actions',
		label: 'Actions',
		items: $session
			? [
					{
						id: 'action.logout',
						label: 'Log out',
						description: 'Sign out of the portal',
						keywords: ['signout', 'leave'],
					},
			  ]
			: [
					{
						id: 'action.login',
						label: 'Sign in',
						description: 'Open the login page',
						keywords: ['signin', 'session'],
					},
			  ],
	});

	const instancesGroup = $derived<CommandPaletteGroup | null>(
		derivedInstanceCommands.length > 0
			? {
					id: 'instances',
					label: 'Instances',
					items: derivedInstanceCommands,
			  }
			: null
	);

	const paletteGroups = $derived<CommandPaletteGroup[]>(
		instancesGroup
			? [navigationGroup, instancesGroup, actionsGroup]
			: [navigationGroup, actionsGroup]
	);

	function handlePaletteSelect(item: CommandPaletteItem) {
		paletteOpen = false;
		paletteQuery = '';
		switch (item.id) {
			case 'nav.fleet':
				navigate('/portal/fleet');
				return;
			case 'nav.legacy-instances':
				navigate('/portal/instances');
				return;
			case 'nav.souls':
				navigate('/portal/souls');
				return;
			case 'nav.trust':
				navigate('/trust');
				return;
			case 'nav.account':
				navigate('/account');
				return;
			case 'nav.billing':
				navigate('/portal/billing');
				return;
			case 'nav.operator':
				navigate('/operator');
				return;
			case 'action.logout':
				void handleLogout();
				return;
			case 'action.login':
				navigate('/login');
				return;
		}
		// Per-instance commands carry their slug in the id: `instance.<slug>`
		if (item.id.startsWith('instance.')) {
			const slug = item.id.slice('instance.'.length);
			if (slug) navigate(`/portal/instances/${slug}`);
		}
	}

	function isPaletteHotkey(ev: KeyboardEvent): boolean {
		// ⌘K on macOS, Ctrl+K elsewhere. Match the upstream CommandPalette
		// component's typical activation contract.
		const key = ev.key.toLowerCase();
		if (key !== 'k') return false;
		return ev.metaKey || ev.ctrlKey;
	}

	onMount(() => {
		function onKeydown(ev: KeyboardEvent) {
			if (!isPaletteHotkey(ev)) return;
			// Avoid intercepting browser-native cmd+K (e.g. address-bar focus)
			// only when no input is focused; here we *do* want to take it.
			ev.preventDefault();
			paletteOpen = !paletteOpen;
		}
		document.addEventListener('keydown', onKeydown);
		return () => document.removeEventListener('keydown', onKeydown);
	});
</script>

<Shell mainLabel="Customer portal" sidebarPlacement="left" sidebarWidth="md">
	{#snippet topbar()}
		<Topbar variant="default">
			{#snippet start()}
				<div class="portal-shell__brand">
					<Heading level={2} size="lg">lesser.host</Heading>
				</div>
			{/snippet}
			{#snippet end()}
				<div class="portal-shell__topbar-end">
					{#if $session}
						<Text size="sm" weight="medium">{$session.username}</Text>
						<Text size="sm" color="secondary">{$session.role}</Text>
						<Button variant="outline" onclick={() => void handleLogout()}>Logout</Button>
					{:else}
						<Link {...linkProps('/login')} variant="default">Sign in</Link>
					{/if}
				</div>
			{/snippet}
		</Topbar>
	{/snippet}

	{#snippet sidebar()}
		<Sidebar label="Primary navigation">
			<div class="portal-shell__nav">
				<Link
					{...linkProps('/portal/fleet')}
					variant="ghost"
					aria-current={isPortalFleetActive() ? 'page' : undefined}
				>
					Fleet
				</Link>
				<Link
					{...linkProps('/portal/instances')}
					variant="ghost"
					aria-current={isLegacyPortalActive() ? 'page' : undefined}
				>
					Instances (legacy)
				</Link>
				<Link
					{...linkProps('/portal/souls')}
					variant="ghost"
					aria-current={isActive('/portal/souls') ? 'page' : undefined}
				>
					Souls
				</Link>
				<Link
					{...linkProps('/portal/billing')}
					variant="ghost"
					aria-current={isActive('/portal/billing') ? 'page' : undefined}
				>
					Billing
				</Link>
				<Link
					{...linkProps('/trust')}
					variant="ghost"
					aria-current={isActive('/trust') ? 'page' : undefined}
				>
					Trust
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
			<div class="portal-shell__sidebar-footer">
				<Text size="sm" color="secondary">⌘K for command palette</Text>
			</div>
		</Sidebar>
	{/snippet}

	<PageFrame width="wide">
		{@render children()}
	</PageFrame>
</Shell>

<CommandPalette
	bind:open={paletteOpen}
	bind:query={paletteQuery}
	label="Portal command palette"
	groups={paletteGroups}
	onselect={handlePaletteSelect}
/>

<style>
	.portal-shell__brand {
		display: flex;
		align-items: center;
		padding: 0 var(--gr-spacing-scale-3);
	}

	.portal-shell__topbar-end {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
		padding: 0 var(--gr-spacing-scale-3);
	}

	.portal-shell__nav {
		display: flex;
		flex-direction: column;
		gap: var(--gr-spacing-scale-1);
		flex: 1;
		padding: var(--gr-spacing-scale-3);
	}

	.portal-shell__nav :global(button) {
		justify-content: flex-start;
	}

	.portal-shell__sidebar-footer {
		padding: var(--gr-spacing-scale-3);
		border-top: 1px solid var(--gr-semantic-border-default, var(--gr-color-gray-200));
	}
</style>
