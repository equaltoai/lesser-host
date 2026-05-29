/**
 * M17 Account UI Fixture — entry point.
 *
 * Pre-loads session store with a customer session, mocks the
 * `/api/v1/portal/me` endpoint, then mounts the real Account page
 * component. This entry is ONLY loaded via `web/fixtures/m17-account.html`
 * for headless PNG capture — it is never imported by any customer portal route.
 *
 * Import order mirrors the real app entrypoints so screenshot evidence
 * reflects the actual Account.svelte rendering rather than unstyled layout.
 *
 * @license AGPL-3.0-only
 */

/* Design tokens: Greater base + Agent Genesis bridge */
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/tokens';
import 'src/lib/styles/greater/primitives.css';

/* Greater Shell CSS (Account uses Panel from shell) */
import 'src/lib/styles/greater/shell.css';

/* Greater host-platform CSS */
import 'src/lib/styles/greater/host-platform.css';

/* M1 foundation primitives (Eyebrow, etc.) */
import 'src/lib/styles/m1-primitives.css';

/* Host-specific global resets + skin overrides */
import 'src/app.css';

import { mount } from 'svelte';
import Account from 'src/pages/Account.svelte';

// ── Pre-load session store ────────────────────────────────────────────

import { setSession } from 'src/lib/session';

setSession({
	tokenType: 'bearer',
	token: 'tok_fixture_m17_account',
	expiresAt: new Date(Date.now() + 86400_000).toISOString(),
	username: 'alice-wallet',
	role: 'customer',
	method: 'wallet',
	walletAddress: '0x1234567890abcdef1234567890abcdef12345678',
});

// ── Mock fetch for /api/v1/portal/me ───────────────────────────────────

const originalFetch = window.fetch;
window.fetch = function (input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
	const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;

	if (url.endsWith('/api/v1/portal/me')) {
		return Promise.resolve(
			new Response(
				JSON.stringify({
					username: 'alice-wallet',
					role: 'customer',
					display_name: 'Alice',
					email: 'alice@example.com',
				}),
				{
					status: 200,
					headers: { 'content-type': 'application/json' },
				},
			),
		);
	}

	return originalFetch.call(window, input, init);
} as typeof window.fetch;

// ── Mount ──────────────────────────────────────────────────────────────

const root = document.getElementById('fixture-root');
if (root) {
	mount(Account, { target: root });
}
