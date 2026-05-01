import { describe, expect, it } from 'vitest';

import { normalizeInstanceKeysResponse, normalizeInstanceKeyTimestamp } from './portalInstances';

describe('instance key timestamp normalization', () => {
	it('treats missing and Go zero timestamps as absent', () => {
		expect(normalizeInstanceKeyTimestamp(undefined)).toBeUndefined();
		expect(normalizeInstanceKeyTimestamp(null)).toBeUndefined();
		expect(normalizeInstanceKeyTimestamp('')).toBeUndefined();
		expect(normalizeInstanceKeyTimestamp('0001-01-01T00:00:00Z')).toBeUndefined();
		expect(normalizeInstanceKeyTimestamp('0001-01-01T00:00:00.000Z')).toBeUndefined();
	});

	it('preserves non-zero timestamps', () => {
		expect(normalizeInstanceKeyTimestamp(' 2026-05-01T12:34:56Z ')).toBe('2026-05-01T12:34:56Z');
	});

	it('removes zero optional timestamps from listed keys', () => {
		const out = normalizeInstanceKeysResponse({
			count: 1,
			keys: [
				{
					id: 'k1',
					created_at: '2026-05-01T12:00:00Z',
					last_used_at: '0001-01-01T00:00:00Z',
					revoked_at: '0001-01-01T00:00:00Z',
				},
			],
		});

		expect(out.keys).toEqual([
			{
				id: 'k1',
				created_at: '2026-05-01T12:00:00Z',
			},
		]);
	});
});
