/* Operator — `/operator` and `/operator/...` (operator console). */
import type { ShellFaceOptions } from './_shell';

export const operatorRoute: ShellFaceOptions = {
	route: '/operator/{rest*}',
	routeId: 'operator',
};
