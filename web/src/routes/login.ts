/* Login — `/login` (no layout; wallet challenge/response + WebAuthn). */
import type { ShellFaceOptions } from './_shell';

export const loginRoute: ShellFaceOptions = {
	route: '/login',
	routeId: 'login',
};
