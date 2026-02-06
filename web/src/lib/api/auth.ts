import { fetchJson } from './http';

export function authLogout(token: string): Promise<{ ok: boolean }> {
	return fetchJson<{ ok: boolean }>('/api/v1/auth/logout', {
		method: 'POST',
		headers: {
			authorization: `Bearer ${token}`,
		},
	});
}

