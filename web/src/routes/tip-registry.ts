/* Tip registry — `/tip-registry` and `/tip-registry/register` (reskin of
 * Safe-ready payload registration; on-chain contracts unchanged). */
import type { ShellFaceOptions } from './_shell';

export const tipRegistryRoute: ShellFaceOptions = {
	route: '/tip-registry/{rest*}',
	routeId: 'tip-registry',
};
