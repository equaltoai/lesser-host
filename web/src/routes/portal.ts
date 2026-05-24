/* Portal — `/portal` and `/portal/...` (customer portal). */
import type { ShellFaceOptions } from './_shell';

export const portalRoute: ShellFaceOptions = {
	route: '/portal/{rest*}',
	routeId: 'portal',
};
