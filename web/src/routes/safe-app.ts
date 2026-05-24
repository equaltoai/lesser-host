/* Safe App — `/safe-app` and `/safe-app/...` (the Safe-Apps SDK harness
 * `App.svelte` switches into when loaded via SAFE_APP_BASE_PATH). The
 * client-side `src/lib/router.ts` resolves the staged sub-target after
 * hydration; the FaceModule's job here is just to emit the SSR shell. */
import type { ShellFaceOptions } from './_shell';

export const safeAppRoute: ShellFaceOptions = {
	route: '/safe-app/{rest*}',
	routeId: 'safe-app',
};
