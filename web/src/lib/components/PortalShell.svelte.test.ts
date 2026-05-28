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

// ── Imports (after hoisted mocks) ─────────────────────────────────

import PortalShell from './PortalShell.svelte';
import { setSession, clearSession } from 'src/lib/session';
import { portalListInstances } from 'src/lib/api/portalInstances';
import { getPortalMe } from 'src/lib/api/portal';
import type { InstanceResponse } from 'src/lib/api/portalInstances';

// ── Typed mock references ─────────────────────────────────────────

const mockListInstances = vi.mocked(portalListInstances);
const mockGetPortalMe = vi.mocked(getPortalMe);

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
});
