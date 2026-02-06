import { get } from 'svelte/store';

import { authLogout } from 'src/lib/api/auth';
import { clearSession, session } from 'src/lib/session';

export async function logout(): Promise<void> {
	const current = get(session);
	if (current?.token) {
		try {
			await authLogout(current.token);
		} catch {
			// Best-effort: always clear local session state.
		}
	}
	clearSession();
}

