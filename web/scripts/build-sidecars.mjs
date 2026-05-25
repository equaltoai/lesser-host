#!/usr/bin/env node
/* ============================================================================
 * Static SSR hydration sidecar generator.
 *
 * Writes one JSON file per shell route to `web/dist/sidecars/<routeId>.json`.
 * CDK uploads that directory to the FaceTheory ISR htmlStoreBucket so the
 * CloudFront `/_facetheory/data/*` behavior (M0.12) serves the JSON
 * directly from S3.
 *
 * Why static (not via createSsrHydrationSidecarStore + signed tokens):
 *   - Host's per-route shell payloads are deterministic per routeId
 *     (`{ routeId, route }`); they carry no per-request data.
 *   - Serving them as static S3 objects is functionally identical to the
 *     in-memory sidecarById pre-router that `face-app.ts` previously kept,
 *     but lifts the work out of Lambda and onto CloudFront's edge cache.
 *   - When host introduces shells whose hydration payload varies per-
 *     request, the canonical createSsrHydrationSidecarStore + signed-
 *     /_facetheory/ssr-data/<token> path takes over for those routes.
 *
 * The route list is the authoritative source — DO NOT hand-author sidecar
 * filenames; they must come from `web/src/routes/index.ts`'s `allRoutes`
 * export so adding a route is still a one-line change in routes/index.ts.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.12
 * ========================================================================== */

import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join, resolve } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(__dirname, '..');
const outDir = join(webRoot, 'dist', 'sidecars');

// We can't `import` web/src/routes/index.ts directly from Node (Svelte +
// TS source). Instead mirror the route table here as the single source of
// build-time truth. If the route list grows, edit BOTH this file and
// web/src/routes/index.ts (eslint catches drift via the M0.16 verifier
// when it lands).
//
// Each route's hydration payload is `{ routeId, route }` exactly as
// `createShellFace` in `web/src/routes/_shell.ts` declares.
const ROUTES = [
	{ routeId: 'home', route: '/' },
	{ routeId: 'login', route: '/login' },
	{ routeId: 'setup', route: '/setup' },
	{ routeId: 'portal', route: '/portal/{rest*}' },
	{ routeId: 'operator', route: '/operator/{rest*}' },
	{ routeId: 'trust', route: '/trust/{rest*}' },
	{ routeId: 'account', route: '/account' },
	{ routeId: 'tip-registry', route: '/tip-registry/{rest*}' },
	{ routeId: 'safe-app', route: '/safe-app/{rest*}' },
];

// M0.3 probe route — kept as deterministic payload (no per-request timestamp
// so the sidecar URL stays cacheable). The HTML response continues to emit
// the per-request timestamp for human eyeballs during lab soak.
const PROBE_SIDECAR = { routeId: 'probe', route: '/_facetheory/probe' };

function writeSidecar(routeId, payload) {
	const target = join(outDir, `${routeId}.json`);
	// Deterministic serialization — keys in insertion order, no trailing
	// newline, single-line for byte-identical deployment artifacts across
	// invocations of this script.
	writeFileSync(target, JSON.stringify(payload), 'utf8');
}

function main() {
	if (!existsSync(outDir)) {
		mkdirSync(outDir, { recursive: true });
	}
	for (const route of ROUTES) {
		writeSidecar(route.routeId, { routeId: route.routeId, route: route.route });
	}
	// Probe shell payload is just `{ route }` per face-app.ts's M0.3
	// payload contract (no routeId).
	writeSidecar(PROBE_SIDECAR.routeId, { route: PROBE_SIDECAR.route });

	const count = ROUTES.length + 1;
	// console.warn is the lint-allowed surface for build-script status output.
	console.warn(`[build-sidecars] wrote ${count} static sidecar files → dist/sidecars/`);
}

main();
