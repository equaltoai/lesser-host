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
});

