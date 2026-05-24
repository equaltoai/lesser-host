/* Setup — `/setup` and `/setup/...` (first-run bootstrap). The trailing
 * `{rest*}` proxy segment matches both the bare path and any sub-paths. */
import type { ShellFaceOptions } from './_shell';

export const setupRoute: ShellFaceOptions = {
	route: '/setup/{rest*}',
	routeId: 'setup',
};
