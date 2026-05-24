/* ============================================================================
 * Host route table (FaceTheory FaceModules)
 *
 * One `ShellFaceOptions` per existing host page; `face-app.ts` builds the
 * `FaceModule[]` via `createShellFace(opts, deps)` after loading the Vite
 * manifest once at module init. Adding a new route family is one new file
 * here plus one line in the `allRoutes` export.
 *
 * Route patterns follow FaceTheory's `Router` syntax:
 *   - static segments:     `/account`
 *   - trailing proxy:      `/portal/{rest*}`  (matches `/portal` and any
 *                                              sub-path)
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.4
 * ========================================================================== */

import type { ShellFaceOptions } from './_shell';

import { accountRoute } from './account';
import { homeRoute } from './home';
import { loginRoute } from './login';
import { operatorRoute } from './operator';
import { portalRoute } from './portal';
import { safeAppRoute } from './safe-app';
import { setupRoute } from './setup';
import { tipRegistryRoute } from './tip-registry';
import { trustRoute } from './trust';

export const allRoutes: readonly ShellFaceOptions[] = Object.freeze([
	homeRoute,
	loginRoute,
	setupRoute,
	portalRoute,
	operatorRoute,
	trustRoute,
	accountRoute,
	tipRegistryRoute,
	safeAppRoute,
]);

export type { ShellFaceOptions, ShellHydrationPayload } from './_shell';
export { createShellFace } from './_shell';
