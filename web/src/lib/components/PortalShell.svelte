<!--
@component
PortalShell — M2 redesigned customer portal chrome.

Grouped sidebar nav with eyebrow sections (Overview, Instances, Agents,
Settings), per-instance entries with status dots and alert badges,
user chip footer with sign-out, conditional Operator console button,
topbar bell placeholder, and ⌘K command-palette trigger.

Behavior:
  - Sidebar fetches /api/v1/portal/instances and /api/v1/portal/me on mount
    for per-instance entries and display-name surfacing.
  - ⌘K trigger button dispatches `lesserhost:cmd-k-trigger` custom event;
    M3 owns the command palette overlay wiring. The existing CommandPalette
    remains functional via the legacy ⌘K keybinding.
  - Bell icon opens an empty popover stating notifications are not wired yet.
  - Operator console footer button navigates to /operator without prefetching
    operator data; visible only when session.role is admin or operator.
  - Content surfaces keep their existing implementation — only the outer
    chrome changes.

Posture:
  - Strict-CSP safe: no inline event handlers; no inline styles; no
    third-party origins.
  - Multi-tenant isolation preserved: data sourced through existing
    authorized portal APIs scoped to the authenticated owner.
  - AGPL-3.0-only.

Source: docs/project-42-portal-redesign/milestones/03-m2-shell.md
Issue: equaltoai/lesser-host#536

@license AGPL-3.0-only
-->

<script lang="ts">
	import { onMount } from 'svelte';
	import type { Snippet } from 'svelte';
	import { get } from 'svelte/store';

	import {
		Shell,
		Sidebar,
		Topbar,
		PageFrame,
		Breadcrumb,
		CommandPalette,
		type CommandPaletteItem,
		type CommandPaletteGroup,
		type BreadcrumbItem,
	} from 'src/lib/shell';
	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { session } from 'src/lib/session';
	import { logout } from 'src/lib/auth/logout';
	import { Button, Heading, Link, Text } from 'src/lib/ui';
	import { portalFleetInstances, clearPortalFleetState } from 'src/lib/portalFleetState';
	import BrandLockup from './BrandLockup.svelte';
	import Eyebrow from './primitives/Eyebrow.svelte';
	import { portalListInstances, type InstanceResponse } from 'src/lib/api/portalInstances';
	import { getPortalMe, type PortalMeResponse } from 'src/lib/api/portal';
	import { soulListMyAgents, type SoulMineAgentItem } from 'src/lib/api/soul';
	import type { ApiError } from 'src/lib/api/http';

	// Icons
	import BellIcon from 'src/lib/greater/icons/icons/bell.svelte';
	import SearchIcon from 'src/lib/greater/icons/icons/search.svelte';
	import LogOutIcon from 'src/lib/greater/icons/icons/log-out.svelte';
	import ShieldIcon from 'src/lib/greater/icons/icons/shield.svelte';
	import HomeIcon from 'src/lib/greater/icons/icons/home.svelte';
	import UsersIcon from 'src/lib/greater/icons/icons/users.svelte';
	import SettingsIcon from 'src/lib/greater/icons/icons/settings.svelte';
	import AlertCircleIcon from 'src/lib/greater/icons/icons/alert-circle.svelte';
	import CheckCircleIcon from 'src/lib/greater/icons/icons/check-circle.svelte';
	import LoaderIcon from 'src/lib/greater/icons/icons/loader.svelte';
	import DollarSignIcon from 'src/lib/greater/icons/icons/dollar-sign.svelte';

	interface Props {
		/** Page content rendered inside the shell's PageFrame. */
		children: Snippet;
		/**
		 * Optional per-instance "Open <slug>" command palette entries.
		 * When omitted, derived from the shared `portalFleetInstances` store.
		 */
		instanceCommands?: CommandPaletteItem[];
	}

	let { children, instanceCommands }: Props = $props();

	// ── PortalMe state ────────────────────────────────────────────────
	let meLoading = $state(false);
	let me = $state<PortalMeResponse | null>(null);

	// ── Instance list state (for sidebar) ─────────────────────────────
	let instancesLoading = $state(false);
	let instances = $state<InstanceResponse[]>([]);

	// ── Souls state (for command palette) ─────────────────────────────
	let soulsLoading = $state(false);
	let souls = $state<SoulMineAgentItem[]>([]);

	// ── Bell popover ──────────────────────────────────────────────────
	let bellOpen = $state(false);

	// ── Command palette ───────────────────────────────────────────────
	let paletteOpen = $state(false);
	let paletteQuery = $state('');

	// ── Route helpers ─────────────────────────────────────────────────

	function isActive(path: string, exact: boolean = false): boolean {
		if (exact) return $currentPath === path;
		return $currentPath.startsWith(path);
	}

	function isPortalFleetActive(): boolean {
		return $currentPath === '/portal' || isActive('/portal/fleet');
	}

	function isInstanceDetailActive(slug: string): boolean {
		return $currentPath === `/portal/instances/${slug}` ||
			$currentPath.startsWith(`/portal/instances/${slug}/`);
	}

	// ── Breadcrumbs ───────────────────────────────────────────────────

	const breadcrumbs = $derived.by<BreadcrumbItem[]>(() => {
		const path = $currentPath;

		// Build crumbs from the path segments
		if (path === '/portal' || path === '/portal/fleet' || path.startsWith('/portal/fleet/')) {
			return [{ label: 'Portal', href: '/portal', current: true }];
		}

		if (path.startsWith('/portal/instances/')) {
			const rest = path.slice('/portal/instances/'.length);
			const slug = rest.split('/')[0];
			const crumbs: BreadcrumbItem[] = [
				{ label: 'Portal', href: '/portal' },
				{ label: 'Instances', href: '/portal/instances' },
			];
			if (slug) {
				crumbs.push({ label: slug, href: `/portal/instances/${slug}`, current: rest === slug || rest.startsWith(`${slug}/overview`) });
			}
			if (rest.includes('/')) {
				const sub = rest.split('/').slice(1).join(' / ');
				if (sub && !rest.startsWith(`${slug}/overview`)) {
					crumbs.push({ label: sub, current: true });
				}
			}
			return crumbs;
		}

		if (path === '/portal/instances') {
			return [{ label: 'Portal', href: '/portal' }, { label: 'Instances', current: true }];
		}

		// Map single-segment portal routes to breadcrumbs
		const routeCrumbMap: Record<string, string> = {
			'/portal/billing': 'Billing',
			'/portal/souls': 'Souls',
			'/portal/trust': 'Trust',
			'/portal/account': 'Account',
		};

		if (routeCrumbMap[path]) {
			return [{ label: 'Portal', href: '/portal' }, { label: routeCrumbMap[path], current: true }];
		}

		// Nested under /portal/trust/...
		if (path.startsWith('/portal/trust/')) {
			return [{ label: 'Portal', href: '/portal' }, { label: 'Trust', current: true }];
		}

		// Default
		return [{ label: 'Portal', current: true }];
	});

	// ── Display-name / avatar derivation (M2 full surfacing) ──────────

	const WALLET_USERNAME_PATTERN = /^wallet-[0-9a-f]+$/i;

	function shortWalletAddress(addr: string | undefined): string {
		const a = (addr ?? '').trim();
		if (!a) return '';
		if (a.length <= 14) return a;
		return `${a.slice(0, 6)}…${a.slice(-4)}`;
	}

	const displayHandle = $derived.by((): string => {
		// Prefer display_name from /api/v1/portal/me (M2 surfacing).
		if (me?.display_name) return me.display_name;
		if (!$session) return '';

		const u = ($session.username || '').trim();
		if (!u) return '';
		if (!WALLET_USERNAME_PATTERN.test(u)) {
			return `@${u}`;
		}
		const short = shortWalletAddress($session.walletAddress);
		if (short) return short;
		const tail = u.slice('wallet-'.length, 'wallet-'.length + 6);
		return tail ? `@wallet-${tail}…` : '@wallet';
	});

	const avatarInitials = $derived.by((): string => {
		// Use display_name initials when available.
		if (me?.display_name) {
			const trimmed = me.display_name.trim();
			if (trimmed) {
				const parts = trimmed.split(/\s+/);
				if (parts.length >= 2) {
					return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
				}
				return trimmed.slice(0, 2).toUpperCase();
			}
		}
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

	const roleSubtext = $derived.by((): string => {
		if (!$session) return '';
		return `${$session.role} · ${$session.method ?? 'session'}`;
	});

	// ── Instance status mapping ───────────────────────────────────────

	function instanceStatusColor(status: string): string {
		const s = (status ?? '').toLowerCase();
		if (s === 'ok' || s === 'active' || s === 'running' || s === 'provisioned') return 'ok';
		if (s === 'warning') return 'warning';
		if (s === 'error' || s === 'failed') return 'error';
		if (s === 'pending' || s === 'in_progress' || s === 'creating' || s === 'provisioning') return 'accent';
		return 'ok';
	}

	function instanceHasAlert(inst: InstanceResponse): boolean {
		const s = (inst.status ?? '').toLowerCase();
		return s === 'warning' || s === 'error' || s === 'failed';
	}

	// ── Data loading ──────────────────────────────────────────────────

	async function loadSidebarData() {
		const tok = get(session)?.token;
		if (!tok) return;

		// Fetch portalMe for display_name
		meLoading = true;
		try {
			me = await getPortalMe(tok);
		} catch (_err) {
			// Fail-quiet: display_name falls back to session username / wallet address.
		} finally {
			meLoading = false;
		}

		// Fetch instances for sidebar entries
		instancesLoading = true;
		try {
			const res = await portalListInstances(tok);
			instances = res.instances ?? [];
		} catch (_err) {
			// Fail-quiet: sidebar shows empty state.
			instances = [];
		} finally {
			instancesLoading = false;
		}

		// Fetch souls for command palette group
		soulsLoading = true;
		try {
			const soulRes = await soulListMyAgents(tok);
			souls = soulRes.agents ?? [];
		} catch (_err) {
			// Fail-quiet: cmd-k palette omits Souls group.
			souls = [];
		} finally {
			soulsLoading = false;
		}
	}

	onMount(() => {
		void loadSidebarData();

		function onKeydown(ev: KeyboardEvent) {
			if (!isPaletteHotkey(ev)) return;
			ev.preventDefault();
			paletteOpen = !paletteOpen;
		}

		function onCmdKTrigger() {
			paletteOpen = true;
			paletteQuery = '';
		}

		document.addEventListener('keydown', onKeydown);
		window.addEventListener('lesserhost:cmd-k-trigger', onCmdKTrigger);
		return () => {
			document.removeEventListener('keydown', onKeydown);
			window.removeEventListener('lesserhost:cmd-k-trigger', onCmdKTrigger);
		};
	});

	// ── Handlers ──────────────────────────────────────────────────────

	async function handleLogout() {
		clearPortalFleetState();
		await logout();
		navigate('/login');
	}

	function handleBellClick() {
		bellOpen = !bellOpen;
	}

	function closeBell() {
		bellOpen = false;
	}

	/**
	 * Dispatch a custom event for the M3 command palette to consume.
	 * The button is intentionally inert in M2 — clicking it dispatches
	 * `lesserhost:cmd-k-trigger` on the window but does not open any
	 * overlay itself.
	 */
	function handleCmdKTrigger() {
		window.dispatchEvent(new CustomEvent('lesserhost:cmd-k-trigger', { bubbles: true }));
	}

	// ── Command palette (preserved from M0/M1, M3 replaces) ───────────

	const derivedInstanceCommands = $derived.by<CommandPaletteItem[]>(() => {
		if (!$session?.token) return [];
		if (instanceCommands) return instanceCommands;
		return $portalFleetInstances.map((inst) => {
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
		});
	});

	const navigationGroup = $derived<CommandPaletteGroup>({
		id: 'nav',
		label: 'Navigate',
		items: [
			{
				id: 'nav.fleet',
				label: 'Go to Fleet',
				description: 'Customer fleet view',
				keywords: ['portal', 'instances', 'overview'],
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
		items: [
			...($session
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
					]),
			// Stubbed / future actions (M3)
			{
				id: 'action.new-instance',
				label: 'New instance…',
				description: 'Create a new managed instance',
				keywords: ['create', 'provision', 'deploy'],
				disabled: true,
			},
			{
				id: 'action.request-soul',
				label: 'Request a soul…',
				description: 'Register a new soul agent',
				keywords: ['register', 'agent', 'soul'],
			},
			{
				id: 'action.refresh-data',
				label: 'Refresh data',
				description: 'Refresh portal data',
				keywords: ['reload', 'sync'],
				disabled: true,
			},
		],
	});

	const instancesGroup = $derived<CommandPaletteGroup | null>(
		$session?.token && derivedInstanceCommands.length > 0
			? {
					id: 'instances',
					label: 'Instances',
					items: derivedInstanceCommands,
				}
			: null
	);

	const soulsGroup = $derived<CommandPaletteGroup | null>(
		souls.length > 0
			? {
					id: 'souls',
					label: 'Souls',
					items: souls.map((entry) => ({
						id: `soul.${entry.agent.agent_id}`,
						label: `${entry.agent.local_id}@${entry.agent.domain}`,
						description: entry.agent.status || undefined,
						keywords: [entry.agent.local_id, entry.agent.domain, 'soul', 'agent'],
					})),
				}
			: null
	);

	const paletteGroups = $derived<CommandPaletteGroup[]>(
		[navigationGroup, actionsGroup, instancesGroup, soulsGroup].filter(
			(g): g is CommandPaletteGroup => g !== null
		)
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
			case 'action.request-soul':
				navigate('/portal/souls/register');
				return;
		}
		if (item.id.startsWith('instance.')) {
			const slug = item.id.slice('instance.'.length);
			if (slug) navigate(`/portal/instances/${slug}`);
		}
		if (item.id.startsWith('soul.')) {
			const agentId = item.id.slice('soul.'.length);
			if (agentId) navigate(`/portal/souls/${agentId}`);
		}
	}

	function isPaletteHotkey(ev: KeyboardEvent): boolean {
		const key = ev.key.toLowerCase();
		if (key !== 'k') return false;
		return ev.metaKey || ev.ctrlKey;
	}
</script>

<Shell mainLabel="Customer portal" sidebarPlacement="left" sidebarWidth="md">
	{#snippet topbar()}
		<Topbar variant="default" sticky>
			{#snippet start()}
				<div class="portal-shell__crumbs">
					<Breadcrumb items={breadcrumbs} />
				</div>
			{/snippet}
			{#snippet center()}
				<button
					class="portal-shell__cmdk-trigger"
					type="button"
					onclick={handleCmdKTrigger}
					aria-label="Search instances, souls, jobs — ⌘K"
				>
					<span class="portal-shell__cmdk-icon"><SearchIcon /></span>
					<span>Search instances, souls, jobs…</span>
					<span class="portal-shell__kbd">⌘K</span>
				</button>
			{/snippet}
			{#snippet end()}
				<div class="portal-shell__topbar-end">
					<div class="portal-shell__bell-wrapper">
						<button
							class="portal-shell__bell-btn"
							type="button"
							onclick={handleBellClick}
							aria-label="Notifications"
						>
							<BellIcon />
						</button>
						{#if bellOpen}
							<div
								class="portal-shell__bell-popover"
								role="dialog"
								aria-label="Notifications"
								tabindex="-1"
								onclick={(e: MouseEvent) => e.stopPropagation()}
								onkeydown={(e: KeyboardEvent) => { if (e.key === 'Escape') closeBell(); }}
							>
								<div class="portal-shell__bell-popover-body">
									<Text size="sm" color="secondary">Notifications coming soon.</Text>
								</div>
							</div>
						{/if}
					</div>
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
				<!-- ── Overview section ───────────────────────────── -->
				<div class="portal-shell__nav-section">
					<Eyebrow>Overview</Eyebrow>
				</div>
				<Link
					{...linkProps('/portal')}
					variant="ghost"
					aria-current={isPortalFleetActive() ? 'page' : undefined}
				>
					<span class="portal-shell__nav-icon"><HomeIcon /></span>
					Fleet
				</Link>
				<Link
					{...linkProps('/portal/billing')}
					variant="ghost"
					aria-current={isActive('/portal/billing') ? 'page' : undefined}
				>
					<DollarSignIcon class="portal-shell__nav-icon" />
					Cost &amp; billing
				</Link>
				<Link
					{...linkProps('/portal/trust')}
					variant="ghost"
					aria-current={isActive('/portal/trust') || isActive('/trust') ? 'page' : undefined}
				>
					<CheckCircleIcon class="portal-shell__nav-icon" />
					Trust
				</Link>

				<!-- ── Instances section ──────────────────────────── -->
				<div class="portal-shell__nav-section">
					<Eyebrow>Instances</Eyebrow>
				</div>
				{#if instancesLoading}
					{#each [1, 2, 3] as _i (_i)}
						<div class="portal-shell__skeleton-row" aria-hidden="true">
							<span class="portal-shell__skeleton-dot"></span>
							<span class="portal-shell__skeleton-text"></span>
						</div>
					{/each}
				{:else if instances.length === 0}
					<div class="portal-shell__empty-hint"><Text size="sm" color="secondary">
						No instances yet
					</Text></div>
				{:else}
					{#each instances as inst (inst.slug)}
						<Link
							{...linkProps(`/portal/instances/${inst.slug}`)}
							variant="ghost"
							aria-current={isInstanceDetailActive(inst.slug) ? 'page' : undefined}
						>
							<span
								class="portal-shell__status-dot"
								data-status={instanceStatusColor(inst.status)}
								aria-hidden="true"
							></span>
							<span class="portal-shell__instance-slug">{inst.slug}</span>
							{#if instanceHasAlert(inst)}
								<AlertCircleIcon class="portal-shell__alert-icon" aria-label="Alert" />
							{/if}
						</Link>
					{/each}
				{/if}

				<!-- ── Agents section ─────────────────────────────── -->
				<div class="portal-shell__nav-section">
					<Eyebrow>Agents</Eyebrow>
				</div>
				<Link
					{...linkProps('/portal/souls')}
					variant="ghost"
					aria-current={isActive('/portal/souls') ? 'page' : undefined}
				>
					<UsersIcon />
					Souls
				</Link>

				<!-- ── Settings section ───────────────────────────── -->
				<div class="portal-shell__nav-section">
					<Eyebrow>Settings</Eyebrow>
				</div>
				<Link
					{...linkProps('/portal/account')}
					variant="ghost"
					aria-current={isActive('/portal/account', true) || isActive('/account', true) ? 'page' : undefined}
				>
					<SettingsIcon class="portal-shell__nav-icon" />
					Account
				</Link>
			</div>

			{#snippet footer()}
				<div class="portal-shell__sidebar-footer">
					{#if $session}
						{#if $session.role === 'admin' || $session.role === 'operator'}
							<Button
								variant="outline"
								onclick={() => navigate('/operator')}
								class="portal-shell__operator-btn"
							>
								<ShieldIcon class="portal-shell__operator-icon" />
								Operator console
							</Button>
						{/if}
						<div class="portal-shell__user-chip">
							<span class="portal-shell__avatar" aria-hidden="true">
								{avatarInitials}
							</span>
							<span class="portal-shell__user-copy">
								<Text size="sm" weight="medium">{displayHandle}</Text>
								<Text size="sm" color="secondary">{roleSubtext}</Text>
							</span>
							<button
								class="portal-shell__logout-btn"
								type="button"
								onclick={() => void handleLogout()}
								aria-label="Sign out"
							>
								<LogOutIcon />
							</button>
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
	placeholder="Search instances, souls, jobs…"
	groups={paletteGroups}
	onselect={handlePaletteSelect}
/>

<div
	class="portal-shell__bell-backdrop"
	class:portal-shell__bell-backdrop--open={bellOpen}
	onclick={closeBell}
	aria-hidden="true"
></div>

<style>
	/* ── Topbar ─────────────────────────────────────────────────── */
	.portal-shell__crumbs {
		display: flex;
		align-items: center;
		min-width: 0;
		padding: 0 var(--gr-spacing-scale-3, 0.75rem);
	}

	.portal-shell__topbar-end {
		display: flex;
		gap: var(--gr-spacing-scale-3, 0.75rem);
		align-items: center;
		padding: 0 var(--gr-spacing-scale-3, 0.75rem);
	}

	/* ⌘K trigger */
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

	.portal-shell__cmdk-trigger :global(.portal-shell__cmdk-icon) {
		width: 16px;
		height: 16px;
		flex: 0 0 auto;
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

	/* Bell */
	.portal-shell__bell-wrapper {
		position: relative;
	}

	.portal-shell__bell-btn {
		display: grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border: 1px solid transparent;
		border-radius: var(--ds-radius-md, 0.5rem);
		background: transparent;
		color: var(--ds-fg-2, rgba(51, 33, 22, 0.78));
		cursor: pointer;
		padding: 0;
	}

	.portal-shell__bell-btn:hover {
		background: var(--ds-bg-raised, rgba(255, 255, 255, 0.72));
		color: var(--ds-fg-1, #332116);
	}

	.portal-shell__bell-popover {
		position: absolute;
		top: calc(100% + 0.5rem);
		right: 0;
		width: 18rem;
		background: var(--ds-bg-surface, rgba(255, 251, 245, 1));
		border: 1px solid var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		border-radius: var(--ds-radius-lg, 1rem);
		box-shadow: var(--ds-shadow-lg, 0 8px 24px rgba(73, 34, 10, 0.12));
		z-index: 100;
	}

	.portal-shell__bell-popover-body {
		padding: var(--ds-space-4, 1rem);
	}

	.portal-shell__bell-backdrop {
		display: none;
		position: fixed;
		inset: 0;
		z-index: 99;
	}

	.portal-shell__bell-backdrop--open {
		display: block;
	}

	/* ── Sidebar nav ────────────────────────────────────────────── */
	.portal-shell__nav {
		display: flex;
		flex-direction: column;
		flex: 1;
		padding: var(--gr-spacing-scale-3, 0.75rem);
	}

	.portal-shell__nav :global(button) {
		justify-content: flex-start;
	}

	.portal-shell__nav-section {
		padding: var(--ds-space-3, 0.75rem) var(--ds-space-2, 0.5rem) var(--ds-space-1, 0.25rem);
	}

	.portal-shell__nav-section:first-child {
		padding-top: 0;
	}

	.portal-shell__nav :global(.portal-shell__nav-icon) {
		width: 16px;
		height: 16px;
		flex: 0 0 auto;
	}

	.portal-shell__nav :global(.portal-shell__empty-hint) {
		padding: var(--ds-space-1, 0.25rem) var(--ds-space-2, 0.5rem);
	}

	/* Status dots — CSP-safe via data-status attribute */
	.portal-shell__status-dot {
		width: 8px;
		height: 8px;
		border-radius: 999px;
		flex: 0 0 auto;
		background: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
	}

	.portal-shell__status-dot[data-status='ok'] {
		background: var(--ds-success-500, #22c55e);
	}

	.portal-shell__status-dot[data-status='warning'] {
		background: var(--ds-warning-500, #f59e0b);
	}

	.portal-shell__status-dot[data-status='error'] {
		background: var(--ds-error-500, #ef4444);
	}

	.portal-shell__status-dot[data-status='accent'] {
		background: var(--ds-accent-500, #e6a645);
	}

	.portal-shell__instance-slug {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.portal-shell__nav :global(.portal-shell__alert-icon) {
		width: 14px;
		height: 14px;
		flex: 0 0 auto;
		color: var(--ds-warning-500, #f59e0b);
	}

	/* Skeleton rows */
	.portal-shell__skeleton-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding: 0.5rem 0.75rem;
	}

	.portal-shell__skeleton-dot {
		width: 8px;
		height: 8px;
		border-radius: 999px;
		flex: 0 0 auto;
		background: var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		animation: portal-shell-skeleton-pulse 1.5s ease-in-out infinite;
	}

	.portal-shell__skeleton-text {
		height: 0.75rem;
		border-radius: 4px;
		flex: 1;
		background: var(--ds-border-subtle, rgba(107, 56, 16, 0.1));
		animation: portal-shell-skeleton-pulse 1.5s ease-in-out infinite;
	}

	@keyframes portal-shell-skeleton-pulse {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.4;
		}
	}

	/* ── Sidebar footer ─────────────────────────────────────────── */
	.portal-shell__sidebar-footer {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-3, 0.75rem);
	}

	/* User chip */
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
		background: linear-gradient(
			135deg,
			var(--ds-secondary-400, #aa84f2),
			var(--ds-primary-400, #e6a645)
		);
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
		flex: 1;
	}

	.portal-shell__logout-btn {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: 1px solid transparent;
		border-radius: var(--ds-radius-sm, 0.375rem);
		background: transparent;
		color: var(--ds-fg-3, rgba(51, 33, 22, 0.58));
		cursor: pointer;
		padding: 0;
		flex: 0 0 auto;
	}

	.portal-shell__logout-btn:hover {
		color: var(--ds-error-500, #ef4444);
		background: var(--ds-bg-raised, rgba(255, 255, 255, 0.72));
	}

	/* Operator console button */
	.portal-shell__sidebar-footer :global(.portal-shell__operator-btn) {
		width: 100%;
	}

	.portal-shell__sidebar-footer :global(.portal-shell__operator-icon) {
		width: 16px;
		height: 16px;
	}

	/* ── Responsive ─────────────────────────────────────────────── */
	@media (max-width: 58rem) {
		.portal-shell__cmdk-trigger {
			display: none;
		}
	}
</style>
