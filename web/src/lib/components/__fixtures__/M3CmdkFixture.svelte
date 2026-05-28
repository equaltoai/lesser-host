<!--
@component
M3CmdkFixture — isolated visual fixture rendering the ⌘K command palette
with realistic mock group data for design-fidelity evidence capture.

Renders the greater-shell CommandPalette component directly (not nested
inside PortalShell) at `open={true}` with all four groups PortalShell
produces: Navigate, Actions, Instances, and Souls. Each group contains
realistic fixture items matching the M3 wire-up in PortalShell.svelte.

Not mounted by any customer portal route — this file exists solely for
local visual review and headless PNG capture at 1440×900.

Strict-CSP safe: no inline styles, no inline event handlers.

@license AGPL-3.0-only
@public
-->
<script lang="ts">
	import { CommandPalette, type CommandPaletteGroup } from 'src/lib/shell';

	// ── Navigate group (mirrors PortalShell.navigationGroup) ──────────

	const navigateGroup: CommandPaletteGroup = {
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
				id: 'nav.instances',
				label: 'Go to Instance list',
				description: 'Detailed instance list',
				keywords: ['portal', 'list', 'instances'],
			},
			{
				id: 'nav.souls',
				label: 'Go to Souls',
				description: 'Soul registry',
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
			{
				id: 'nav.operator',
				label: 'Go to Operator Console',
				description: 'Admin / operator surface',
				keywords: ['admin', 'operator', 'console'],
			},
		],
	};

	// ── Actions group (mirrors PortalShell.actionsGroup) ──────────────

	const actionsGroup: CommandPaletteGroup = {
		id: 'actions',
		label: 'Actions',
		items: [
			{
				id: 'action.logout',
				label: 'Log out',
				description: 'Sign out of the portal',
				keywords: ['signout', 'leave'],
			},
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
	};

	// ── Instances group (mirrors PortalShell.instancesGroup) ──────────

	const instancesGroup: CommandPaletteGroup = {
		id: 'instances',
		label: 'Instances',
		items: [
			{
				id: 'instance.my-instance',
				label: 'Open my-instance',
				description: 'us-east-1 • v2.4.1',
				keywords: ['my-instance', 'us-east-1', 'v2.4.1', 'instance'],
			},
			{
				id: 'instance.staging-env',
				label: 'Open staging-env',
				description: 'eu-west-1 • v2.4.0',
				keywords: ['staging-env', 'eu-west-1', 'v2.4.0', 'instance'],
			},
			{
				id: 'instance.demo-site',
				label: 'Open demo-site',
				description: 'ap-southeast-1 • v2.4.1',
				keywords: ['demo-site', 'ap-southeast-1', 'v2.4.1', 'instance'],
			},
			{
				id: 'instance.dev-sandbox',
				label: 'Open dev-sandbox',
				description: 'us-west-2 • v2.3.9',
				keywords: ['dev-sandbox', 'us-west-2', 'v2.3.9', 'instance'],
			},
		],
	};

	// ── Souls group (mirrors PortalShell.soulsGroup) ──────────────────

	const soulsGroup: CommandPaletteGroup = {
		id: 'souls',
		label: 'Souls',
		items: [
			{
				id: 'soul.agent-001',
				label: 'ghost@soul.lesser.host',
				description: 'active',
				keywords: ['ghost', 'soul', 'agent'],
			},
			{
				id: 'soul.agent-002',
				label: 'scribe@soul.lesser.host',
				description: 'provisioning',
				keywords: ['scribe', 'soul', 'agent'],
			},
			{
				id: 'soul.agent-003',
				label: 'sentinel@soul.lesser.host',
				description: 'active',
				keywords: ['sentinel', 'soul', 'agent'],
			},
		],
	};

	const groups: CommandPaletteGroup[] = [
		navigateGroup,
		actionsGroup,
		instancesGroup,
		soulsGroup,
	];

	/** Stub — items are selected only via keyboard/mouse in the real app. */
	function handleSelect(_item: { id: string }) {
		// no-op in fixture
		void _item;
	}
</script>

<div class="m3-fixture">
	<!--
		The CommandPalette's own CSS provides the fixed overlay backdrop.
		The fixture page background fills the viewport so the semi-transparent
		overlay reads correctly against it.
	-->
	<CommandPalette
		open={true}
		label="Portal command palette"
		placeholder="Search instances, souls, jobs…"
		{groups}
		onselect={handleSelect}
	/>
</div>

<style>
	.m3-fixture {
		min-height: 100vh;
		background: var(--ds-bg-canvas, #fcf7f0);
	}
</style>
