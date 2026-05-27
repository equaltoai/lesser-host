<!--
@component
PortalShell — greater-components Shell + Sidebar + Topbar + PageFrame
composition for the customer portal's fleet view.

This shell is now the canonical `/portal` surface. The older
`PortalLayout.svelte` remains only for legacy `/portal/instances` detail
paths that have not yet been migrated into this shell.

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
	import BrandLockup from './BrandLockup.svelte';

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
		return $currentPath === '/portal' || isActive('/portal/fleet');
	}

	function isLegacyPortalActive(): boolean {
		// Matches only the legacy /portal/instances + nested paths.
		return (
			isActive('/portal/instances') &&
			!isActive('/portal/fleet')
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
				description: 'Customer fleet view',
				keywords: ['portal', 'instances', 'overview'],
				shortcut: 'F',
			},
			{
				id: 'nav.legacy-instances',
				label: 'Go to Instance list',
				description: 'Detailed instance list',
				keywords: ['portal', 'list', 'instances'],
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
				navigate('/portal');
				return;
			case 'nav.legacy-instances':
				navigate('/portal/instances');
				return;
			case 'nav.souls':
				navigate('/portal/souls');
				return;
			case 'nav.trust':
				// Project 42 M0.1 (#526): canonical Trust path is now under
				// portal chrome. /trust still resolves (App.svelte routes it
				// to the same Trust component) but the command palette
				// always navigates to the canonical entrypoint.
				navigate('/portal/trust');
				return;
			case 'nav.account':
				navigate('/portal/account');
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

	// Project 42 M0.7 (#532): the sidebar footer used to render
	// `@{session.username}` literally. For wallet-login customers the
	// username is auto-generated as `wallet-<64-hex>` and the audit
	// caught the result rendering as `@wallet-f7c8c15eefb7a907ceee47ae…`
	// — visible, unreadable, and operator-hostile. The full display-name
	// surfacing belongs to M2 (PortalShell redesign), which fetches the
	// `display_name` field returned by `/api/v1/portal/me`. This M0 fix
	// is the bounded interim: detect the wallet-* synthetic-username
	// pattern and substitute a short, recognizable identity built from
	// the session.walletAddress already on the wire.
	const WALLET_USERNAME_PATTERN = /^wallet-[0-9a-f]+$/i;

	function shortWalletAddress(addr: string | undefined): string {
		const a = (addr ?? '').trim();
		if (!a) return '';
		if (a.length <= 14) return a;
		return `${a.slice(0, 6)}…${a.slice(-4)}`;
	}

	const displayHandle = $derived.by((): string => {
		if (!$session) return '';
		const u = ($session.username || '').trim();
		if (!u) return '';
		if (!WALLET_USERNAME_PATTERN.test(u)) {
			return `@${u}`;
		}
		// Synthetic wallet-* username: prefer the truncated wallet address
		// when present, fall back to a short prefix of the synthetic name.
		const short = shortWalletAddress($session.walletAddress);
		if (short) return short;
		const tail = u.slice('wallet-'.length, 'wallet-'.length + 6);
		return tail ? `@wallet-${tail}…` : '@wallet';
	});

	const avatarInitials = $derived.by((): string => {
		if (!$session) return '';
		const u = ($session.username || '').trim();
		if (!u) return '?';
		if (!WALLET_USERNAME_PATTERN.test(u)) {
			return u.slice(0, 2).toUpperCase();
		}
		const addr = ($session.walletAddress || '').trim();
		if (addr.startsWith('0x') && addr.length >= 4) return addr.slice(2, 4).toUpperCase();
		return 'W•';
	});

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
		<Topbar variant="default" sticky>
			{#snippet start()}
				<div class="portal-shell__crumb">
					<Heading level={2} size="lg">Portal</Heading>
					<Text size="sm" color="secondary">Fleet, billing, trust, and soul operations</Text>
				</div>
			{/snippet}
			{#snippet center()}
				<button class="portal-shell__cmdk-trigger" type="button" onclick={() => (paletteOpen = true)}>
					<span aria-hidden="true">⌕</span>
					<span>Search instances, souls, jobs…</span>
					<span class="portal-shell__kbd">⌘K</span>
				</button>
			{/snippet}
			{#snippet end()}
				<div class="portal-shell__topbar-end">
					{#if $session}
						<Text size="sm" weight="medium">{displayHandle}</Text>
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
			{#snippet header()}
				<BrandLockup edition="Customer portal" />
			{/snippet}
			<div class="portal-shell__nav">
				<Link
					{...linkProps('/portal')}
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
					Instance list
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
					{...linkProps('/portal/trust')}
					variant="ghost"
					aria-current={isActive('/portal/trust') || isActive('/trust') ? 'page' : undefined}
				>
					Trust
				</Link>
				<Link
					{...linkProps('/portal/account')}
					variant="ghost"
					aria-current={isActive('/portal/account', true) || isActive('/account', true)
						? 'page'
						: undefined}
				>
					Account
				</Link>
				{#if $session && ($session.role === 'admin' || $session.role === 'operator')}
					<Link {...linkProps('/operator')} variant="ghost">Operator Console</Link>
				{/if}
			</div>
			{#snippet footer()}
				<div class="portal-shell__sidebar-footer">
					<Text size="sm" color="secondary">⌘K for command palette</Text>
					{#if $session}
						<div class="portal-shell__user-chip">
							<span class="portal-shell__avatar" aria-hidden="true">
								{avatarInitials}
							</span>
							<span class="portal-shell__user-copy">
								<Text size="sm" weight="medium">{displayHandle}</Text>
								<Text size="sm" color="secondary">{$session.role} · {$session.method ?? 'session'}</Text>
							</span>
						</div>
					{:else}
						<Link {...linkProps('/login')} variant="default">Sign in</Link>
					{/if}
				</div>
			{/snippet}
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
	.portal-shell__crumb {
		display: flex;
		flex-direction: column;
		justify-content: center;
		min-width: 0;
	}

	.portal-shell__topbar-end {
		display: flex;
		gap: var(--gr-spacing-scale-3);
		align-items: center;
		padding: 0 var(--gr-spacing-scale-3);
	}

	.portal-shell__cmdk-trigger {
		display: flex;
		align-items: center;
		gap: var(--ds-space-2, 0.5rem);
		min-width: min(26rem, 42vw);
		border: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		border-radius: var(--ds-radius-pill, 999px);
		padding: 0.42rem 0.75rem;
		background: var(--ds-bg-raised, rgba(255, 255, 255, 0.72));
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
		font: inherit;
		text-align: left;
		cursor: pointer;
		box-shadow: var(--ds-shadow-xs, 0 1px 2px rgba(73, 34, 10, 0.04));
	}

	.portal-shell__cmdk-trigger:hover {
		color: var(--ds-fg-1, #332116);
		border-color: var(--ds-border-default, rgba(107, 56, 16, 0.16));
	}

	.portal-shell__kbd {
		margin-left: auto;
		font-family: var(--ds-font-mono, ui-monospace, monospace);
		font-size: 0.7rem;
		background: var(--ds-bg-surface, rgba(255, 251, 245, 0.92));
		border: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		padding: 0.05rem 0.32rem;
		border-radius: 5px;
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
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
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3, 0.75rem);
	}

	.portal-shell__user-chip {
		display: flex;
		align-items: center;
		gap: var(--ds-space-3, 0.75rem);
		padding: var(--ds-space-2, 0.5rem);
		border-radius: var(--ds-radius-lg, 1rem);
		background: var(--ds-bg-raised, rgba(255, 255, 255, 0.72));
		border: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		min-width: 0;
	}

	.portal-shell__avatar {
		width: 32px;
		height: 32px;
		border-radius: 999px;
		background: linear-gradient(135deg, var(--ds-secondary-400, #aa84f2), var(--ds-primary-400, #e6a645));
		color: white;
		display: grid;
		place-items: center;
		font-weight: 700;
		font-size: 0.82rem;
		flex: 0 0 auto;
	}

	.portal-shell__user-copy {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}

	@media (max-width: 58rem) {
		.portal-shell__cmdk-trigger {
			display: none;
		}
	}
</style>
