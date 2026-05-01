import { get } from 'svelte/store';

import { authLogout } from 'src/lib/api/auth';
import { clearSession, session } from 'src/lib/session';

export async function logout(): Promise<void> {
	const token = get(session)?.token;
	clearSession();
	if (token) {
		void authLogout(token).catch(() => {
			// Best-effort: local session state has already been cleared.
		});
	}
}
