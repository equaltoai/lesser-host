/**
 * PortalShell M2 shell tests.
 *
 * Covers the required invariants from issue #536:
 *   - Operator button hidden for non-operator session
 *   - Sign-out action present and functional
 *   - Sidebar shows skeleton during async instance loading (not false empty state)
 *   - Command-trigger dispatches `lesserhost:cmd-k-trigger` event
 *   - Notification bell opens popover with placeholder text
 *
 * @license AGPL-3.0-only
 */

import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { mount } from 'svelte';

// ── Hoisted mocks (processed before imports, must use inline vi.fn()) ─

vi.mock('src/lib/api/portalInstances', () => ({
	portalListInstances: vi.fn(),
}));

vi.mock('src/lib/api/portal', () => ({
	getPortalMe: vi.fn(),
}));

vi.mock('src/lib/api/soul', () => ({
	soulListMyAgents: vi.fn(),
}));

// Partial mock: preserve real router but replace navigate with a mock
// so route-selection tests can verify which path was navigated to.
vi.mock('src/lib/router', async () => {
	const actual = await vi.importActual<typeof import('src/lib/router')>('src/lib/router');
	return { ...actual, navigate: vi.fn() };
});

// ── Imports (after hoisted mocks) ─────────────────────────────────

import PortalShell from './PortalShell.svelte';
import { setSession, clearSession } from 'src/lib/session';
import { portalFleetInstances } from 'src/lib/portalFleetState';
import { portalListInstances } from 'src/lib/api/portalInstances';
import { getPortalMe } from 'src/lib/api/portal';
import { soulListMyAgents } from 'src/lib/api/soul';
import { navigate } from 'src/lib/router';
import type { InstanceResponse } from 'src/lib/api/portalInstances';

// ── Typed mock references ─────────────────────────────────────────

const mockListInstances = vi.mocked(portalListInstances);
const mockGetPortalMe = vi.mocked(getPortalMe);
const mockSoulListMyAgents = vi.mocked(soulListMyAgents);
const mockNavigate = vi.mocked(navigate);

// ── Helpers ───────────────────────────────────────────────────────

function customerSession() {
	return {
		tokenType: 'Bearer' as const,
		token: 'test-token',
		expiresAt: new Date(Date.now() + 3600_000).toISOString(),
		username: 'wallet-f7c8c15eefb7a907ceee47aef9c2e9f8b1d3a5c6e7f8a9b0c1d2e3f4a5b6c7d8',
		role: 'customer',
		method: 'wallet',
		walletAddress: '0x4f29a8b3c1d5e7f9a2b4c6d8e0f1a3b5c7d9e2f4',
	};
}

function operatorSession() {
	return {
		tokenType: 'Bearer' as const,
		token: 'test-token-op',
		expiresAt: new Date(Date.now() + 3600_000).toISOString(),
		username: 'admin-user',
		role: 'admin',
		method: 'wallet',
		walletAddress: '0x1234567890abcdef1234567890abcdef12345678',
	};
}

function makeInstance(overrides: Partial<InstanceResponse> = {}): InstanceResponse {
	return {
		slug: 'my-instance',
		status: 'ok',
		hosted_region: 'us-east-1',
		lesser_version: 'v1.0.0',
		translation_enabled: false,
		hosted_previews_enabled: false,
		link_safety_enabled: false,
		renders_enabled: false,
		render_policy: '',
		overage_policy: '',
		moderation_enabled: false,
		moderation_trigger: '',
		moderation_virality_min: 0,
		ai_enabled: false,
		ai_model_set: '',
		ai_batching_mode: '',
		ai_batch_max_items: 0,
		ai_batch_max_total_bytes: 0,
		ai_pricing_multiplier_bps: 0,
		ai_max_inflight_jobs: 0,
		created_at: new Date().toISOString(),
		...overrides,
	};
}

function makeSoulAgent(overrides: Record<string, unknown> = {}) {
	return {
		agent: {
			agent_id: overrides.agent_id as string ?? 'agent-001',
			domain: overrides.domain as string ?? 'example.com',
			local_id: overrides.local_id as string ?? 'hal',
			wallet: '0xabcd',
			status: overrides.status as string ?? 'graduated',
		},
		reputation: undefined,
	};
}

function mountPortalShell(props?: { children?: () => string }) {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const instance = mount(PortalShell, {
		target,
		// eslint-disable-next-line @typescript-eslint/no-explicit-any
		props: { children: (props?.children ?? (() => 'Test content')) } as any,
	});
	return { target, instance };
}

/** Flush async work triggered by onMount (loadSidebarData). */
async function flushMountAsync() {
	await tick();
	await new Promise((r) => setTimeout(r, 0));
	await tick();
	// Second flush for any chained microtasks
	await new Promise((r) => setTimeout(r, 0));
	await tick();
}

// ── Setup / teardown ──────────────────────────────────────────────

beforeEach(() => {
	mockListInstances.mockReset();
	mockGetPortalMe.mockReset();
	mockSoulListMyAgents.mockReset();
	mockNavigate.mockReset();
	clearSession();
});

// ── Tests ─────────────────────────────────────────────────────────

describe('PortalShell', () => {
	describe('operator button visibility', () => {
		it('hides the operator console button for customer sessions', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const opBtn = target.querySelector('.portal-shell__operator-btn');
			expect(opBtn).toBeNull();
		});

		it('shows the operator console button for admin sessions', async () => {
			setSession(operatorSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'admin-user',
				role: 'admin',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const opBtn = target.querySelector('.portal-shell__operator-btn');
			expect(opBtn).not.toBeNull();
		});

		it('shows the operator console button for operator sessions', async () => {
			setSession({
				...operatorSession(),
				role: 'operator',
			});

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'op-user',
				role: 'operator',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const opBtn = target.querySelector('.portal-shell__operator-btn');
			expect(opBtn).not.toBeNull();
		});
	});

	describe('sign-out', () => {
		it('renders a sign-out button when a session is present', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const logoutBtn = target.querySelector('.portal-shell__logout-btn');
			expect(logoutBtn).not.toBeNull();
			expect(logoutBtn?.getAttribute('aria-label')).toBe('Sign out');
		});

		it('does not render a sign-out button when no session is present', () => {
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockRejectedValue(new Error('no session'));

			const { target } = mountPortalShell();

			const logoutBtn = target.querySelector('.portal-shell__logout-btn');
			expect(logoutBtn).toBeNull();
		});
	});

	describe('sidebar instance loading', () => {
		it('shows skeleton rows while instances are loading', async () => {
			setSession(customerSession());

			// eslint-disable-next-line @typescript-eslint/no-explicit-any
			let resolveInstances!: (value: any) => void;
			mockListInstances.mockReturnValue(
				new Promise((resolve) => {
					resolveInstances = resolve;
				}),
			);
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();

			// Flush microtasks to let loadSidebarData reach the pending
			// portalListInstances call (after getPortalMe resolves).
			await flushMountAsync();

			const skeletons = target.querySelectorAll('.portal-shell__skeleton-row');
			expect(skeletons.length).toBeGreaterThan(0);

			const instanceLinks = target.querySelectorAll('.portal-shell__instance-slug');
			expect(instanceLinks.length).toBe(0);

			resolveInstances({ instances: [], count: 0 });
			await tick();
		});

		it('shows instance entries after loading completes', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({
				instances: [
					makeInstance({ slug: 'alpha', status: 'ok' }),
					makeInstance({ slug: 'beta', status: 'warning' }),
				],
				count: 2,
			});
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
				display_name: 'Alice',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const slugs = target.querySelectorAll('.portal-shell__instance-slug');
			expect(slugs.length).toBe(2);
			expect(slugs[0].textContent?.trim()).toBe('alpha');
			expect(slugs[1].textContent?.trim()).toBe('beta');
		});

		it('shows "No instances yet" when the user has no instances', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const emptyHint = target.querySelector('.portal-shell__empty-hint');
			expect(emptyHint).not.toBeNull();
			expect(emptyHint?.textContent?.trim()).toContain('No instances yet');
		});

		it('does not show empty state text while still loading', async () => {
			setSession(customerSession());

			// eslint-disable-next-line @typescript-eslint/no-explicit-any
			let resolveInstances!: (value: any) => void;
			mockListInstances.mockReturnValue(
				new Promise((resolve) => {
					resolveInstances = resolve;
				}),
			);
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();

			// Flush microtasks to let loadSidebarData reach the pending
			// portalListInstances call (after getPortalMe resolves).
			await flushMountAsync();

			// While loading, no empty-state text should appear
			const emptyHint = target.querySelector('.portal-shell__empty-hint');
			expect(emptyHint).toBeNull();

			// Clean up
			resolveInstances({ instances: [], count: 0 });
			await tick();
		});
	});

	describe('command trigger (⌘K button)', () => {
		it('dispatches lesserhost:cmd-k-trigger custom event on click', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const trigger = target.querySelector('.portal-shell__cmdk-trigger') as HTMLButtonElement;
			expect(trigger).not.toBeNull();

			let eventFired = false;
			const handler = () => {
				eventFired = true;
			};
			window.addEventListener('lesserhost:cmd-k-trigger', handler);

			trigger.click();

			expect(eventFired).toBe(true);

			window.removeEventListener('lesserhost:cmd-k-trigger', handler);
		});

		it('renders the ⌘K trigger button with correct label', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const trigger = target.querySelector('.portal-shell__cmdk-trigger');
			expect(trigger).not.toBeNull();
			expect(trigger?.getAttribute('aria-label')).toContain('⌘K');
			expect(trigger?.textContent).toContain('Search instances');
		});
	});

	describe('notification bell', () => {
		it('opens a popover with placeholder text when clicked', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const bellBtn = target.querySelector('.portal-shell__bell-btn') as HTMLButtonElement;
			expect(bellBtn).not.toBeNull();

			expect(target.querySelector('.portal-shell__bell-popover')).toBeNull();

			bellBtn.click();
			await tick();

			const popover = target.querySelector('.portal-shell__bell-popover');
			expect(popover).not.toBeNull();
			expect(popover?.textContent?.trim()).toContain('Notifications coming soon');
		});

		it('closes the popover on second click', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const bellBtn = target.querySelector('.portal-shell__bell-btn') as HTMLButtonElement;
			bellBtn.click();
			await tick();
			expect(target.querySelector('.portal-shell__bell-popover')).not.toBeNull();

			bellBtn.click();
			await tick();
			expect(target.querySelector('.portal-shell__bell-popover')).toBeNull();
		});
	});

	describe('sidebar grouped nav', () => {
		it('renders four eyebrow sections: Overview, Instances, Agents, Settings', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const eyebrows = target.querySelectorAll('.eyebrow');
			const labels = Array.from(eyebrows).map((el) => el.textContent?.trim());
			expect(labels).toContain('Overview');
			expect(labels).toContain('Instances');
			expect(labels).toContain('Agents');
			expect(labels).toContain('Settings');
		});
	});

	describe('user chip', () => {
		it('shows display_name from portalMe when available', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
				display_name: 'Alice Johnson',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const chip = target.querySelector('.portal-shell__user-chip');
			expect(chip).not.toBeNull();
			expect(chip?.textContent).toContain('Alice Johnson');
		});

		it('falls back to wallet address when display_name is unavailable', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const chip = target.querySelector('.portal-shell__user-chip');
			expect(chip).not.toBeNull();
			expect(chip?.textContent).toContain('customer');
		});
	});

	describe('bell backdrop', () => {
		it('renders a click-away backdrop that closes the popover', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const bellBtn = target.querySelector('.portal-shell__bell-btn') as HTMLButtonElement;
			bellBtn.click();
			await tick();

			expect(target.querySelector('.portal-shell__bell-popover')).not.toBeNull();

			const backdrop = target.querySelector('.portal-shell__bell-backdrop') as HTMLDivElement;
			expect(backdrop).not.toBeNull();
			backdrop.click();
			await tick();

			expect(target.querySelector('.portal-shell__bell-popover')).toBeNull();
		});
	});

	describe('instance status dot mapping', () => {
		it('maps provisioning status to accent, not warning', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({
				instances: [makeInstance({ slug: 'new-box', status: 'provisioning' })],
				count: 1,
			});
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const dot = target.querySelector('.portal-shell__status-dot') as HTMLElement;
			expect(dot).not.toBeNull();
			expect(dot.getAttribute('data-status')).toBe('accent');
		});
	});

	describe('sidebar nav IA ordering', () => {
		it('places Fleet, Cost & billing, Trust under Overview and Account alone under Settings', async () => {
			setSession(customerSession());

			mockListInstances.mockResolvedValue({
				instances: [makeInstance({ slug: 'alpha', status: 'ok' })],
				count: 1,
			});
			mockGetPortalMe.mockResolvedValue({
				username: 'wallet-user',
				role: 'customer',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const nav = target.querySelector('.portal-shell__nav');
			expect(nav).not.toBeNull();
			const allText = nav!.textContent?.replace(/\s+/g, ' ').trim() ?? '';

			const fleetIdx = allText.indexOf('Fleet');
			const billingIdx = allText.indexOf('Cost & billing');
			const trustIdx = allText.indexOf('Trust');
			const instancesEyebrowIdx = allText.indexOf('Instances');
			const settingsEyebrowIdx = allText.indexOf('Settings');
			const accountIdx = allText.indexOf('Account');

			expect(fleetIdx).toBeGreaterThan(-1);
			expect(billingIdx).toBeGreaterThan(fleetIdx);
			expect(trustIdx).toBeGreaterThan(billingIdx);
			expect(instancesEyebrowIdx).toBeGreaterThan(trustIdx);
			expect(settingsEyebrowIdx).toBeGreaterThan(instancesEyebrowIdx);
			expect(accountIdx).toBeGreaterThan(settingsEyebrowIdx);

			const bareBillingMatch = /(?<!&)\bBilling\b/.test(allText);
			expect(bareBillingMatch).toBe(false);
		});
	});

	describe('sidebar footer ordering', () => {
		it('renders operator button before user chip for admin sessions', async () => {
			setSession(operatorSession());

			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({
				username: 'admin-user',
				role: 'admin',
				method: 'wallet',
			});

			const { target } = mountPortalShell();
			await flushMountAsync();

			const footer = target.querySelector('.portal-shell__sidebar-footer');
			expect(footer).not.toBeNull();

			const children = Array.from(footer!.children);
			const opBtnIdx = children.findIndex((c) => c.classList.contains('portal-shell__operator-btn'));
			const userChipIdx = children.findIndex((c) => c.classList.contains('portal-shell__user-chip'));

			expect(opBtnIdx).toBeGreaterThan(-1);
			expect(userChipIdx).toBeGreaterThan(-1);
			expect(opBtnIdx).toBeLessThan(userChipIdx);
		});
	});

	describe('command palette (M3 — ⌘K)', () => {
		/** Open the palette by dispatching Meta+K on the document. */
		function openPalette() {
			document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true, bubbles: true }));
		}

		/** Resolve all data-loading promises + flush Svelte ticks. */
		async function settle() {
			await flushMountAsync();
		}

		it('opens the palette on Meta+K keydown', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			// Palette should be closed initially
			let dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).toBeNull();

			openPalette();
			await tick();

			dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).not.toBeNull();
			// greater-shell uses aria-labelledby (not aria-label); verify it is set
			expect(dialog?.getAttribute('aria-labelledby')).toBeTruthy();
		});

		it('opens the palette on Ctrl+K keydown', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }));
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).not.toBeNull();
		});

		it('opens the palette on lesserhost:cmd-k-trigger event', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			// The trigger button dispatches this event; verify it opens the palette
			window.dispatchEvent(new CustomEvent('lesserhost:cmd-k-trigger', { bubbles: true }));
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).not.toBeNull();
		});

		it('closes the palette on Escape', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			let dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).not.toBeNull();

			// Dispatch Escape on the dialog element
			dialog!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
			await tick();

			dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).toBeNull();
		});

		it('closes the palette on backdrop click (click-outside)', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			let dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).not.toBeNull();

			// Click the backdrop (the dialog root itself is the backdrop container)
			dialog!.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
			await tick();

			dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).toBeNull();
		});

		it('renders four groups: Navigate, Actions, Instances, Souls', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({
				instances: [makeInstance({ slug: 'alpha', status: 'ok' })],
				count: 1,
			});
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({
				agents: [makeSoulAgent({ agent_id: 's1', domain: 'alpha.greater.website', local_id: 'bot' })],
				count: 1,
			});

			// Populate fleet store so Instances group appears in palette
			portalFleetInstances.set([{ slug: "alpha", hosted_region: "us-east-1", lesser_version: "v1.0.0" }]);

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();

			const groups = dialog.querySelectorAll('[role="group"]');
			expect(groups.length).toBe(4);

			const groupLabels = Array.from(groups).map((g) =>
				g.getAttribute('aria-labelledby')
					? target.querySelector('#' + g.getAttribute('aria-labelledby'))?.textContent?.trim()
					: ''
			);
			expect(groupLabels).toContain('Navigate');
			expect(groupLabels).toContain('Actions');
			expect(groupLabels).toContain('Instances');
			expect(groupLabels).toContain('Souls');
		});

		it('renders stubbed actions as disabled items', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();
			const options = dialog.querySelectorAll('[role="option"]');
			const stubs = Array.from(options).filter(
				(opt) => opt.getAttribute('aria-disabled') === 'true'
			);
			expect(stubs.length).toBeGreaterThanOrEqual(2);

			const stubLabels = stubs.map((s) => s.textContent?.trim() ?? '');
			expect(stubLabels.some((l) => l.includes('New instance'))).toBe(true);
			expect(stubLabels.some((l) => l.includes('Refresh data'))).toBe(true);
		});

		it('filters results when typing in the search input', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();

			// All items visible initially
			let options = dialog.querySelectorAll('[role="option"]');
			const initialCount = options.length;
			expect(initialCount).toBeGreaterThan(0);

			// Type a filter that matches the Fleet navigate item
			const input = target.querySelector('[role="combobox"]') as HTMLInputElement;
			expect(input).not.toBeNull();
			input.value = 'Fleet';
			input.dispatchEvent(new Event('input', { bubbles: true }));
			await tick();

			// Fewer items after filtering
			options = dialog.querySelectorAll('[role="option"]');
			expect(options.length).toBeLessThan(initialCount);
			expect(options.length).toBeGreaterThan(0);

			// Only Fleet-matching items should remain
			const visibleLabels = Array.from(options).map((o) => o.textContent?.trim() ?? '');
			expect(visibleLabels.some((l) => l.toLowerCase().includes('fleet'))).toBe(true);
		});

		it('navigates to /portal when selecting Fleet from Navigate group', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();
			// Find the "Go to Fleet" option and click it
			const options = dialog.querySelectorAll('[role="option"]');
			const fleetOption = Array.from(options).find(
				(o) => o.textContent?.includes('Go to Fleet')
			) as HTMLElement | undefined;
			expect(fleetOption).toBeDefined();
			fleetOption!.click();
			await tick();

			expect(mockNavigate).toHaveBeenCalledWith('/portal');
		});

		it('navigates to /portal/souls/register when selecting Request a soul', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();
			const options = dialog.querySelectorAll('[role="option"]');
			const soulReqOption = Array.from(options).find(
				(o) => o.textContent?.includes('Request a soul')
			) as HTMLElement | undefined;
			expect(soulReqOption).toBeDefined();
			soulReqOption!.click();
			await tick();

			expect(mockNavigate).toHaveBeenCalledWith('/portal/souls/register');
		});

		it('closes the palette after selecting an item', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const paletteDialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(paletteDialog).not.toBeNull();
			const options = paletteDialog.querySelectorAll('[role="option"]');
			const fleetOption = Array.from(options).find(
				(o) => o.textContent?.includes('Go to Fleet')
			) as HTMLElement | undefined;
			expect(fleetOption).toBeDefined();
			fleetOption!.click();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]');
			expect(dialog).toBeNull();
		});

		it('renders soul items with agent identity in the Souls group', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({
				agents: [
					makeSoulAgent({ agent_id: 'agent-aaa', domain: 'example.com', local_id: 'helper', status: 'graduated' }),
				],
				count: 1,
			});

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();
			// Find the Souls group
			const groups = dialog.querySelectorAll('[role="group"]');
			const soulsGroup = Array.from(groups).find((g) => {
				const headerId = g.getAttribute('aria-labelledby');
				if (!headerId) return false;
				const header = target.querySelector('#' + headerId);
				return header?.textContent?.trim() === 'Souls';
			});
			expect(soulsGroup).toBeDefined();

			// Soul item should show agent identity
			const soulOptions = soulsGroup!.querySelectorAll('[role="option"]');
			expect(soulOptions.length).toBe(1);
			expect(soulOptions[0].textContent).toContain('helper');
			expect(soulOptions[0].textContent).toContain('example.com');
		});

		it('hides the Souls group when no souls are bound', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();
			// Souls group should be absent
			const groups = dialog.querySelectorAll('[role="group"]');
			const soulsGroup = Array.from(groups).find((g) => {
				const headerId = g.getAttribute('aria-labelledby');
				if (!headerId) return false;
				const header = target.querySelector('#' + headerId);
				return header?.textContent?.trim() === 'Souls';
			});
			expect(soulsGroup).toBeUndefined();
		});

		it('focuses the search input when the palette opens', async () => {
			setSession(customerSession());
			mockListInstances.mockResolvedValue({ instances: [], count: 0 });
			mockGetPortalMe.mockResolvedValue({ username: 'u', role: 'customer', method: 'wallet' });
			mockSoulListMyAgents.mockResolvedValue({ agents: [], count: 0 });

			const { target } = mountPortalShell();
			await settle();

			openPalette();
			await tick();
			// Focus trap defers focus to the next microtask
			await new Promise((r) => setTimeout(r, 0));
			await tick();

			const dialog = target.querySelector('[role="dialog"][aria-modal="true"]')!;
			expect(dialog).not.toBeNull();
			const input = dialog.querySelector('[role="combobox"]') as HTMLInputElement;
			expect(input).not.toBeNull();
			expect(document.activeElement).toBe(input);
		});
	});
});
