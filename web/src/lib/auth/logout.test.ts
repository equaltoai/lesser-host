import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('logout', () => {
	beforeEach(() => {
		sessionStorage.clear();
		vi.resetModules();
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('clears the local session even if the API call fails', async () => {
		const { session, setSession } = await import('src/lib/session');
		const { logout } = await import('src/lib/auth/logout');

		setSession({
			tokenType: 'Bearer',
			token: 'token',
			expiresAt: new Date(Date.now() + 60_000).toISOString(),
			username: 'alice',
			role: 'customer',
		});

		expect(get(session)).not.toBeNull();

		vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')));

		await logout();

		expect(get(session)).toBeNull();
	});

	it('clears the local session before the API call resolves', async () => {
		const { session, setSession } = await import('src/lib/session');
		const { logout } = await import('src/lib/auth/logout');

		setSession({
			tokenType: 'Bearer',
			token: 'token',
			expiresAt: new Date(Date.now() + 60_000).toISOString(),
			username: 'alice',
			role: 'customer',
		});

		let resolveLogout: ((value: Response) => void) | undefined;
		vi.stubGlobal(
			'fetch',
			vi.fn(
				() =>
					new Promise<Response>((resolve) => {
						resolveLogout = resolve;
					}),
			),
		);

		await logout();

		expect(get(session)).toBeNull();
		expect(resolveLogout).toBeTypeOf('function');

		resolveLogout?.(
			new Response(JSON.stringify({ ok: true }), {
				status: 200,
				headers: { 'content-type': 'application/json' },
			}),
		);
	});
});
