<!--
@component
InstanceDetailShell — tabbed shell for the customer Instance Detail
section (Overview / Cost & usage / Config / Domains / Keys / Souls).

M1.5 — Project 39 web UI rework. Renders the page header + a
tablist-shaped tab bar that drives navigation across the six instance
sub-routes. Deep-linked tab state lives in the URL (one path segment per
tab); the active tab is derived from `$currentPath` so back/forward
navigation, deep-links, and reloads all preserve the open tab.

Keyboard navigation follows the WAI-ARIA Tabs pattern:
  - ArrowRight / ArrowLeft — move to next / previous tab and navigate.
  - Home / End — move to first / last tab and navigate.
  - Tab key — moves out of the tablist into the active panel.
  - Active tab is `tabindex=0`; inactive tabs are `tabindex=-1` so the
    tablist roving-tabindex pattern is preserved.

Tab routing uses the in-house `navigate()` so it integrates with the SPA
router; no full-page reloads.

Posture invariants preserved:
  - Strict-no-inline-CSP safe: no inline scripts, no inline styles, no
    third-party origins. Focus management is done with the SPA router +
    a microtask-deferred focus call (no `<script>` injection).
  - Multi-tenant isolation: this shell never reads tenant state; the
    children (tab content) own all per-slug data fetching and the
    portal ownership checks apply at the API layer.
  - Trust-API instance-auth untouched; SEC-9 change-lock not engaged.

Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.5
Issue: equaltoai/lesser-host#414
-->

<script lang="ts">
	import type { Snippet } from 'svelte';

	import { currentPath, linkProps, navigate } from 'src/lib/router';
	import { Link } from 'src/lib/ui';
	import { PageFrame, PageTitle } from 'src/lib/shell';

	type TabKey = 'overview' | 'cost' | 'config' | 'domains' | 'keys' | 'souls';

	interface TabDescriptor {
		readonly key: TabKey;
		readonly label: string;
		readonly segment: string;
	}

	interface Props {
		/** Instance slug; drives the shell's page-title + tab href construction. */
		slug: string;
		/** Tab content panel rendered below the tablist. */
		children: Snippet;
	}

	let { slug, children }: Props = $props();

	const tabs: readonly TabDescriptor[] = [
		{ key: 'overview', label: 'Overview', segment: '' },
		{ key: 'cost', label: 'Cost & usage', segment: '/cost' },
		{ key: 'config', label: 'Config', segment: '/config' },
		{ key: 'domains', label: 'Domains', segment: '/domains' },
		{ key: 'keys', label: 'Keys', segment: '/keys' },
		{ key: 'souls', label: 'Souls', segment: '/souls' },
	] as const;

	const activeKey = $derived.by<TabKey>(() => {
		const path = $currentPath;
		const prefix = `/portal/instances/${slug}`;
		if (path === prefix) return 'overview';
		if (path === `${prefix}/cost` || path.startsWith(`${prefix}/cost/`)) return 'cost';
		if (path === `${prefix}/config` || path.startsWith(`${prefix}/config/`)) return 'config';
		if (path === `${prefix}/domains` || path.startsWith(`${prefix}/domains/`)) return 'domains';
		if (path === `${prefix}/keys` || path.startsWith(`${prefix}/keys/`)) return 'keys';
		if (path === `${prefix}/souls` || path.startsWith(`${prefix}/souls/`)) return 'souls';
		// Legacy budgets / usage paths still render under the Cost & usage tab
		// until M1.13 consolidates them; the shell highlights "Cost & usage"
		// so the user always sees the right tab as active.
		if (path === `${prefix}/budgets` || path.startsWith(`${prefix}/budgets/`)) return 'cost';
		if (path === `${prefix}/usage` || path.startsWith(`${prefix}/usage/`)) return 'cost';
		return 'overview';
	});

	function tabHref(key: TabKey): string {
		const tab = tabs.find((t) => t.key === key);
		return `/portal/instances/${slug}${tab?.segment ?? ''}`;
	}

	function focusTabButton(key: TabKey) {
		// Defer to the next microtask so the active-tab tabindex flip
		// (driven by `activeKey`) has applied before we move focus.
		queueMicrotask(() => {
			const el = document.getElementById(`instance-tab-${key}`);
			el?.focus();
		});
	}

	function onTabKeydown(ev: KeyboardEvent, index: number) {
		let nextIdx: number;
		switch (ev.key) {
			case 'ArrowRight':
				nextIdx = (index + 1) % tabs.length;
				break;
			case 'ArrowLeft':
				nextIdx = (index - 1 + tabs.length) % tabs.length;
				break;
			case 'Home':
				nextIdx = 0;
				break;
			case 'End':
				nextIdx = tabs.length - 1;
				break;
			default:
				return;
		}
		ev.preventDefault();
		const next = tabs[nextIdx];
		if (!next) return;
		navigate(tabHref(next.key));
		focusTabButton(next.key);
	}

	function onTabClick(key: TabKey) {
		navigate(tabHref(key));
	}
</script>

<PageFrame width="default">
	{#snippet header()}
		<PageTitle
			eyebrow="Instance"
			title={slug}
			description="Managed lesser.host instance — release state, configuration, domains, and access keys."
		>
			{#snippet actions()}
				<Link {...linkProps('/portal/fleet')} variant="ghost">Fleet</Link>
				<Link {...linkProps('/portal')} variant="ghost">Back</Link>
			{/snippet}
		</PageTitle>
	{/snippet}

	<div
		class="instance-tabs"
		role="tablist"
		aria-label="Instance sections"
		aria-orientation="horizontal"
	>
		{#each tabs as tab, idx (tab.key)}
			{@const isActive = activeKey === tab.key}
			<button
				type="button"
				id={`instance-tab-${tab.key}`}
				class="instance-tabs__tab"
				class:instance-tabs__tab--active={isActive}
				role="tab"
				aria-selected={isActive}
				aria-controls={`instance-panel-${activeKey}`}
				tabindex={isActive ? 0 : -1}
				onclick={() => onTabClick(tab.key)}
				onkeydown={(ev) => onTabKeydown(ev, idx)}
			>
				{tab.label}
			</button>
		{/each}
	</div>

	<div
		role="tabpanel"
		id={`instance-panel-${activeKey}`}
		aria-labelledby={`instance-tab-${activeKey}`}
		class="instance-panel"
		tabindex="0"
	>
		{@render children()}
	</div>
</PageFrame>

<style>
	.instance-tabs {
		display: flex;
		flex-wrap: wrap;
		gap: var(--ds-space-1, 0.25rem);
		border-bottom: 1px solid var(--ds-border-default, var(--gr-color-border-default));
		margin-bottom: var(--ds-space-4);
	}

	.instance-tabs__tab {
		appearance: none;
		border: 0;
		background: transparent;
		cursor: pointer;
		padding: var(--ds-space-2) var(--ds-space-4);
		font: inherit;
		color: var(--ds-fg-2, var(--gr-color-foreground-secondary));
		border-bottom: 2px solid transparent;
		transition:
			color var(--ds-duration-fast, 140ms) var(--ds-ease-standard, ease),
			border-color var(--ds-duration-fast, 140ms) var(--ds-ease-standard, ease);
		border-radius: var(--ds-radius-sm, 0.375rem) var(--ds-radius-sm, 0.375rem) 0 0;
		white-space: nowrap;
	}

	.instance-tabs__tab:hover {
		color: var(--ds-fg-1, var(--gr-color-foreground));
	}

	.instance-tabs__tab:focus-visible {
		outline: 2px solid var(--ds-border-focus, var(--gr-color-primary));
		outline-offset: 2px;
	}

	.instance-tabs__tab--active {
		color: var(--ds-fg-1, var(--gr-color-foreground));
		border-bottom-color: var(--ds-action-primary, var(--gr-color-primary));
		font-weight: var(--ds-weight-semibold, 600);
	}

	.instance-panel {
		display: flex;
		flex-direction: column;
		gap: var(--ds-space-6);
	}

	.instance-panel:focus-visible {
		outline: none;
	}
</style>
