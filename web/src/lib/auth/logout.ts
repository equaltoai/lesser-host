import { get } from 'svelte/store';

import { authLogout } from 'src/lib/api/auth';
import { clearSession, session } from 'src/lib/session';

export async function logout(): Promise<void> {
	const token = get(session)?.token;
	try {
		if (token) {
			await authLogout(token);
		}
	} finally {
		clearSession();
	}
}
