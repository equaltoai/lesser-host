import { get } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

describe('session', () => {
	beforeEach(() => {
		sessionStorage.clear();
		vi.resetModules();
	});

	it('records and consumes expiration timestamp when session is expired', async () => {
		const { consumeSessionExpiredAt, session, setSession } = await import('src/lib/session');

		const expiresAt = new Date(0).toISOString();
		setSession({
			tokenType: 'Bearer',
			token: 'token',
			expiresAt,
			username: 'alice',
			role: 'customer',
		});

		expect(get(session)).toBeNull();
		expect(consumeSessionExpiredAt()).toBe(expiresAt);
		expect(consumeSessionExpiredAt()).toBeNull();
	});

	it('clears portal fleet command state when the session is cleared', async () => {
		const { clearSession, setSession } = await import('src/lib/session');
		const { portalFleetInstances } = await import('src/lib/portalFleetState');

		setSession({
			tokenType: 'Bearer',
			token: 'token',
			expiresAt: new Date(Date.now() + 60_000).toISOString(),
			username: 'alice',
			role: 'customer',
		});
		portalFleetInstances.set([{ slug: 'alpha', hosted_region: 'us-east-1', lesser_version: 'v1.0.0' }]);

		expect(get(portalFleetInstances)).toHaveLength(1);

		clearSession();

		expect(get(portalFleetInstances)).toEqual([]);
	});
});
