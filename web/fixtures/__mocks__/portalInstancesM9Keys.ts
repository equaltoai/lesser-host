/**
 * M9 Keys Fixture — mock portalInstances API.
 *
 * Returns deterministic key-list data so the InstanceKeys component
 * renders the design-aligned Keys table without a backend.
 *
 * @license AGPL-3.0-only
 */

export interface InstanceKeyListItem {
	id: string;
	created_at: string;
	last_used_at?: string;
	revoked_at?: string;
}

export interface ListInstanceKeysResponse {
	keys: InstanceKeyListItem[];
	count: number;
}

export interface CreateInstanceKeyResponse {
	instance_slug: string;
	key: string;
	key_id: string;
}

export interface RevokeInstanceKeyResponse {
	instance_slug: string;
	key_id: string;
	revoked: boolean;
}

const FIXTURE_KEYS: InstanceKeyListItem[] = [
	{
		id: 'sk_live_4f29e1c4a3df7b10',
		created_at: new Date('2025-09-14').toISOString(),
		last_used_at: new Date(Date.now() - 4 * 60 * 1000).toISOString(),
		revoked_at: undefined,
	},
	{
		id: 'sk_live_2b71f9c2a3df8810',
		created_at: new Date('2025-12-02').toISOString(),
		last_used_at: new Date(Date.now() - 24 * 3600 * 1000).toISOString(),
		revoked_at: undefined,
	},
	{
		id: 'sk_pkey_8810f9c2a3df4f29',
		created_at: new Date('2025-09-14').toISOString(),
		last_used_at: new Date(Date.now() - 2 * 60 * 1000).toISOString(),
		revoked_at: undefined,
	},
	{
		id: 'sk_live_a1b2c3d4e5f67890',
		created_at: new Date('2025-06-01').toISOString(),
		last_used_at: undefined,
		revoked_at: new Date('2025-11-15').toISOString(),
	},
];

export function portalListInstanceKeys(
	_token?: string,
	_slug?: string,
	_limit?: number,
): Promise<ListInstanceKeysResponse> {
	void _token;
	void _slug;
	void _limit;
	return Promise.resolve({ keys: [...FIXTURE_KEYS], count: FIXTURE_KEYS.length });
}

export function portalCreateInstanceKey(
	_token?: string,
	_slug?: string,
): Promise<CreateInstanceKeyResponse> {
	void _token;
	void _slug;
	return Promise.resolve({
		instance_slug: 'simulacrum',
		key: 'sk_live_newly_created_key_do_not_share',
		key_id: 'sk_live_newkey_9f2c8810',
	});
}

export function portalRevokeInstanceKey(
	_token?: string,
	_slug?: string,
	_keyId?: string,
): Promise<RevokeInstanceKeyResponse> {
	void _token;
	void _slug;
	void _keyId;
	return Promise.resolve({ instance_slug: 'simulacrum', key_id: _keyId || '', revoked: true });
}

/**
 * Normalize key timestamps — zero-dates become undefined (matching the
 * real portalInstances.ts normalizeInstanceKeysResponse).
 */
export function normalizeInstanceKeysResponse(
	res: ListInstanceKeysResponse,
): ListInstanceKeysResponse {
	return {
		...res,
		keys: (res.keys ?? []).map((key) => {
			const { last_used_at: rawLast, revoked_at: rawRev, ...rest } = key;
			const lastUsed =
				rawLast && !rawLast.startsWith('0001-01-01') ? rawLast : undefined;
			const revoked =
				rawRev && !rawRev.startsWith('0001-01-01') ? rawRev : undefined;
			return {
				...rest,
				...(lastUsed ? { last_used_at: lastUsed } : {}),
				...(revoked ? { revoked_at: revoked } : {}),
			};
		}),
	};
}
