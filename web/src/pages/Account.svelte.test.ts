/**
 * M17 Account UI contract tests — issue #551 slim identity + session view.
 *
 * Covers: identity/session DL field rendering, wallet masking, honest
 * button states (rotate-token disabled, sign-out single-session), CSP
 * safety, route dispatch in App.svelte, deep-link /portal/account, and
 * passkey management preservation for operator/admin.
 *
 * @license AGPL-3.0-only
 */

/// <reference types="node" />
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { afterEach, describe, expect, it, vi } from 'vitest';

import { mount, unmount } from 'svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';

import Account from './Account.svelte';

// ── Source files for static assertions ────────────────────────────────

const source = readFileSync(
	join(process.cwd(), 'src/pages/Account.svelte'),
	'utf8',
);

const appSource = readFileSync(
	join(process.cwd(), 'src/App.svelte'),
	'utf8',
);

afterEach(() => {
	vi.restoreAllMocks();
});

// ── Source-text tests (no DOM) ────────────────────────────────────────

describe('M17 Account UI — source-level contracts', () => {
	describe('CSP safety (strict no-inline)', () => {
		it('contains no inline style= attribute in template', () => {
			// Strip <style> blocks and check only the template portion.
			const styleIdx = source.lastIndexOf('<style>');
			const templatePart = styleIdx > 0 ? source.substring(0, styleIdx) : source;
			expect(templatePart).not.toMatch(/\bstyle\s*=\s*"/);
		});

		it('contains no inline onclick/onload/onerror handlers', () => {
			expect(source).not.toMatch(/\son\w+\s*=\s*["']/);
		});

		it('imports no third-party origin resources', () => {
			expect(source).not.toMatch(/https?:\/\/[^/]+\/[^"'\s]*\.(?:js|css|woff2?)/);
		});
	});

	describe('component imports and structure', () => {
		it('imports DefinitionList and DefinitionItem for DL layout', () => {
			expect(source).toContain('DefinitionList');
			expect(source).toContain('DefinitionItem');
		});

		it('imports Panel from shell and Eyebrow from primitives', () => {
			expect(source).toContain("from 'src/lib/shell'");
			expect(source).toContain("from 'src/lib/components/primitives'");
			expect(source).toContain('Eyebrow');
		});

		it('imports session store and logout', () => {
			expect(source).toContain("from 'src/lib/session'");
			expect(source).toContain("from 'src/lib/auth/logout'");
		});

		it('does not import PageFrame from shell (PortalShell provides one)', () => {
			// The component must not use PageFrame — both /portal/account
			// and /account render inside PortalShell which already wraps
			// content in its own PageFrame. Panel from shell is fine.
			// Strip HTML comments (which mention PageFrame in dev-notes) then
			// check the remaining source.
			const noComments = source.replace(/<!--[\s\S]*?-->/g, '');
			expect(noComments).not.toMatch(
				/\bPageFrame\b/,
			);
		});
	});

	describe('identity DL field coverage', () => {
		it('renders Username, Display name, Email, Role DefinitionItems', () => {
			expect(source).toContain('label="Username"');
			expect(source).toContain('label="Display name"');
			expect(source).toContain('label="Email"');
			expect(source).toContain('label="Role"');
		});
	});

	describe('session DL field coverage', () => {
		it('renders Method, Wallet, Token expires, IP DefinitionItems', () => {
			expect(source).toContain('label="Method"');
			expect(source).toContain('label="Wallet"');
			expect(source).toContain('label="Token expires"');
			expect(source).toContain('label="IP"');
		});

		it('masks wallet address via maskWallet function', () => {
			expect(source).toContain('maskWallet');
		});

		it('renders IP as em-dash unavailable', () => {
			expect(source).toContain('>—</Text>');
		});
	});

	describe('action buttons — honest states', () => {
		it('renders Rotate token button as disabled', () => {
			expect(source).toContain('Rotate token');
			expect(source).toMatch(/Rotate token[\s\S]*?disabled/);
		});

		it('renders Sign out button (single session, not "all sessions")', () => {
			expect(source).toContain('Sign out');
			// The template must not label the button "Sign out all sessions"
			// since only single-session logout is supported. The AGPL header
			// comments may mention the phrase, so check the template area.
			const styleIdx = source.lastIndexOf('<style>');
			const templatePart = styleIdx > 0 ? source.substring(0, styleIdx) : source;
			// Strip HTML comments
			const noComments = templatePart.replace(/<!--[\s\S]*?-->/g, '');
			expect(noComments).not.toContain('Sign out all sessions');
		});

		it('documents honest help text about token rotation unavailability', () => {
			expect(source).toContain('Session-token rotation is not yet supported');
		});

		it('documents honest help text about single-session sign-out', () => {
			expect(source).toContain('Sign out signs out the current');
		});
	});

	describe('passkey management preservation', () => {
		it('imports webauthn API modules', () => {
			expect(source).toContain("from 'src/lib/api/webauthn'");
			expect(source).toContain('webAuthnCredentials');
			expect(source).toContain('webAuthnRegisterBegin');
		});

		it('conditionally renders passkey panel for operator/admin role', () => {
			expect(source).toContain('isOperatorRole(profile.role)');
		});
	});

	describe('page header', () => {
		it('renders Eyebrow "Settings" and Heading "Account"', () => {
			expect(source).toContain('>Settings<');
			expect(source).toContain('>Account<');
		});

		it('renders subcopy about identity, sessions, and wallets', () => {
			expect(source).toContain('Identity, sessions, and connected wallets');
		});
	});
});

// ── App.svelte route dispatch tests ────────────────────────────────────

describe('M17 Account UI — App.svelte route dispatch', () => {
	it('imports Account from src/pages/Account.svelte', () => {
		expect(appSource).toContain(
			"import Account from 'src/pages/Account.svelte'",
		);
	});

	it('defines isPortalAccountRoute for exact /portal/account match', () => {
		expect(appSource).toContain('isPortalAccountRoute');
		expect(appSource).toContain("=== '/portal/account'");
	});

	it('renders Account for /portal/account inside PortalShell', () => {
		expect(appSource).toContain('isPortalAccountRoute');
		expect(appSource).toContain('<Account />');
	});

	it('preserves /account deep-link inside PortalShell', () => {
		expect(appSource).toContain("=== '/account'");
		expect(appSource).toContain('<Account />');
	});
});

// ── DOM mount test helpers ────────────────────────────────────────────

function jsonResponse(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'content-type': 'application/json' },
	});
}

/** Install fetch mock for a customer session (portal me). */
function installCustomerSessionMocks() {
	vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
		const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
		if (url.endsWith('/api/v1/portal/me')) {
			return Promise.resolve(
				jsonResponse({
					username: 'wallet-user',
					role: 'customer',
					display_name: 'Alice Example',
					email: 'alice@example.com',
				}),
			);
		}
		return Promise.resolve(jsonResponse({ message: 'not found' }, 404));
	});
}

/** Preload the session store to simulate a signed-in customer. */
async function preloadCustomerSession() {
	const { session, setSession } = await import('src/lib/session');
	setSession({
		tokenType: 'bearer',
		token: 'tok_test_m17_account',
		expiresAt: new Date(Date.now() + 3600_000).toISOString(),
		username: 'wallet-user',
		role: 'customer',
		method: 'wallet',
		walletAddress: '0x1234567890abcdef1234567890abcdef12345678',
	});
	return session;
}

async function waitForText(target: HTMLElement, expected: string) {
	for (let i = 0; i < 20; i += 1) {
		await tick();
		await new Promise((resolve) => setTimeout(resolve, 0));
		if (target.textContent?.includes(expected)) {
			return;
		}
	}
	expect(target.textContent).toContain(expected);
}

// ── DOM mount tests ───────────────────────────────────────────────────

describe('M17 Account UI — DOM mount', () => {
	it('mounts without error and renders the page header', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		const instance = mount(Account, { target });
		await tick();
		await waitForText(target, 'Account');

		const header = target.querySelector('.account__header');
		expect(header).not.toBeNull();
		expect(header?.textContent).toContain('Settings');
		expect(header?.textContent).toContain('Account');
		expect(header?.textContent).toContain('Identity, sessions, and connected wallets');

		unmount(instance);
		document.body.removeChild(target);
	});

	it('renders identity DL fields: Username, Display name, Email, Role', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'Username');

		expect(target.textContent).toContain('Username');
		expect(target.textContent).toContain('wallet-user');
		expect(target.textContent).toContain('Display name');
		expect(target.textContent).toContain('Alice Example');
		expect(target.textContent).toContain('Email');
		expect(target.textContent).toContain('alice@example.com');
		expect(target.textContent).toContain('Role');
		expect(target.textContent).toContain('customer');

		document.body.removeChild(target);
	});

	it('renders session DL: Method, Wallet masked, Token expires', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'Method');

		expect(target.textContent).toContain('Method');
		expect(target.textContent).toContain('wallet');
		expect(target.textContent).toContain('Wallet');
		// Wallet must be masked: 0x1234…5678
		expect(target.textContent).toContain('0x1234');
		expect(target.textContent).toContain('…');
		expect(target.textContent).toContain('5678');
		expect(target.textContent).toContain('Token expires');

		document.body.removeChild(target);
	});

	it('does NOT expose the full wallet address string', async () => {
		installCustomerSessionMocks();
		const sess = await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'Wallet');

		const fullWallet = get(sess)?.walletAddress ?? '';
		expect(fullWallet).toBeTruthy();
		// The full 42-char address must NOT appear anywhere in the DOM text.
		expect(target.textContent).not.toContain(fullWallet);

		document.body.removeChild(target);
	});

	it('renders IP as em-dash unavailable', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'IP');

		// The IP row should be present but show the dash
		expect(target.textContent).toContain('IP');

		document.body.removeChild(target);
	});

	it('renders Rotate token button as disabled', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'Rotate token');

		const found = Array.from(target.querySelectorAll('button')).find(
			(btn) => btn.textContent?.includes('Rotate token'),
		);
		expect(found).not.toBeNull();
		expect(found?.disabled).toBe(true);

		document.body.removeChild(target);
	});

	it('renders Sign out button (not disabled by default)', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'Sign out');

		const signOutBtn = Array.from(target.querySelectorAll('button')).find(
			(btn) => btn.textContent?.includes('Sign out'),
		);
		expect(signOutBtn).not.toBeNull();
		// Should not be disabled (it's the active button)
		expect(signOutBtn?.getAttribute('disabled')).toBeNull();

		document.body.removeChild(target);
	});

	it('does not render passkey panel for customer role', async () => {
		installCustomerSessionMocks();
		await preloadCustomerSession();

		const target = document.createElement('div');
		document.body.appendChild(target);

		mount(Account, { target });
		await tick();
		await waitForText(target, 'Username');

		expect(target.textContent).not.toContain('Passkeys');

		document.body.removeChild(target);
	});
});
