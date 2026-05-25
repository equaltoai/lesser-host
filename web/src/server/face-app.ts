/* ============================================================================
 * FaceTheory server entry (host web/)
 *
 * Builds the host SSR FaceApp and exposes the Lambda Function URL streaming
 * handler. M0.4 ports every existing host page onto its own FaceModule via
 * the shell factory in `src/routes/_shell.ts`; the SSR Lambda emits a
 * strict-no-inline-CSP HTML shell + external hydration sidecar URL, and
 * the existing `src/App.svelte` (mounted by `src/client/face-client.ts`)
 * keeps handling per-page routing and rendering. The M0.3 probe at
 * `/_facetheory/probe` is retained as a per-request health signal.
 *
 * Sidecar serving (M0.12 — retires the 216fb96 in-memory pre-router):
 *   - Each shell's hydration payload `{ routeId, route }` is deterministic
 *     per routeId (no per-request data), so the JSON sidecars at
 *     `/_facetheory/data/<routeId>.json` are pre-generated at web build
 *     time (`web/scripts/build-sidecars.mjs`) and uploaded to the
 *     FaceTheory ISR htmlStoreBucket via the CDK BucketDeployment.
 *   - CloudFront's `/_facetheory/data/*` behavior (AppTheorySsrSite-managed
 *     distribution, M0.12 CDK) serves the JSON directly from S3; the
 *     SSR Lambda never sees those requests, which is the canonical
 *     AppTheory/FaceTheory contract for static SSG hydration payloads.
 *   - When host introduces shells whose hydration payload varies per-
 *     request, the canonical createSsrHydrationSidecarStore +
 *     `/_facetheory/ssr-data/<signed-token>` (Lambda-served, HMAC-signed
 *     under TTL) takes over for those routes — that's a future per-route
 *     opt-in, not a host-wide refactor.
 *
 * CSP posture (preserves host's canonical webCsp byte-string):
 *   - Each FaceModule sets `csp: { inlineScripts: false, inlineStyles: false,
 *     rawHead: false }` so FaceTheory validates the rendered HTML is strict-
 *     no-inline, but does *not* attach a CSP header itself. CloudFront's
 *     response-headers policy continues to attach host's canonical webCsp
 *     and safeAppCsp byte-strings (planning trust-and-safety walk,
 *     docs/trust-and-safety-web-ui-rework-2026-05-24.md).
 *
 * Browser-only-code isolation:
 *   - This module imports zero browser-only modules. `src/App.svelte`,
 *     `src/lib/router.ts`, `src/lib/session.ts` (which all reference
 *     `window`/`document`/`sessionStorage` at module init) are imported
 *     only from `src/client/face-client.ts` and `src/main.ts`, both of
 *     which run in browser environments. SSR Lambda safety preserved.
 *
 * Manifest contract:
 *   - The client build (vite.config.ts `client` environment) emits a Vite
 *     manifest at `dist/.vite/manifest.json` listing every client chunk and
 *     its hashed asset path. The SSR build emits this entry as
 *     `dist/server/face-app.mjs`, so the relative path is
 *     `../.vite/manifest.json`. CDK packaging (M0.12) co-locates the
 *     manifest with the Lambda bundle on the same relative layout.
 *   - If the manifest can't be loaded (e.g. ahead of CDK packaging), shells
 *     degrade gracefully to empty `<head>` rather than crashing on init.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.4
 * ========================================================================== */

import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
	createFaceApp,
	createLambdaUrlStreamingHandler,
	externalHydrationForEntry,
	viteAssetsForEntry,
	type FaceCspPolicy,
	type FaceModule,
	type FaceRequest,
	type FaceResponse,
	type ViteManifest,
} from '@theory-cloud/facetheory';

import { allRoutes, createShellFace } from '../routes/index';
import type { FaceTheoryProbePayload } from '../client/face-client';

// ─── Constants ────────────────────────────────────────────────────────────────

const CLIENT_ENTRY = 'src/client/face-client.ts';
const HYDRATION_DATA_URL_BASE = '/_facetheory/data/';
const PROBE_ROUTE = '/_facetheory/probe';
const SHELL_TITLE = 'lesser.host';

const STRICT_CSP_POLICY: FaceCspPolicy = Object.freeze({
	inlineScripts: false,
	inlineStyles: false,
	rawHead: false,
});

// ─── Manifest loading ─────────────────────────────────────────────────────────

const FACE_APP_DIR = dirname(fileURLToPath(import.meta.url));
const MANIFEST_PATH = resolve(FACE_APP_DIR, '..', '.vite', 'manifest.json');

function loadManifest(): ViteManifest {
	try {
		return JSON.parse(readFileSync(MANIFEST_PATH, 'utf8')) as ViteManifest;
	} catch (err) {
		console.warn(
			'[face-app] client manifest not found at',
			MANIFEST_PATH,
			'— rendering shells without Vite asset tags',
			err instanceof Error ? err.message : String(err),
		);
		return {};
	}
}

const manifest = loadManifest();

// ─── Probe FaceModule (M0.3 carryover; per-request health/SSR pipeline check) ─
//
// The probe sidecar payload deliberately omits the per-request timestamp so
// the static sidecar pre-router below can serve `/_facetheory/data/probe.json`
// without a per-request re-render. The HTML response still includes the
// timestamp for human eyeballs during lab soak.

const probeFace: FaceModule = {
	route: PROBE_ROUTE,
	mode: 'ssr',
	load: async (): Promise<FaceTheoryProbePayload> => ({
		route: PROBE_ROUTE,
		timestamp: new Date().toISOString(),
	}),
	render: async (_ctx, data) => {
		const hasManifestEntry = Object.hasOwn(manifest, CLIENT_ENTRY);
		const assets = hasManifestEntry
			? viteAssetsForEntry(manifest, CLIENT_ENTRY, { includeAssets: true })
			: { bootstrapModule: '', headTags: [] };
		const hydration = hasManifestEntry
			? externalHydrationForEntry(manifest, CLIENT_ENTRY, data, {
					dataUrl: `${HYDRATION_DATA_URL_BASE}probe.json`,
				})
			: undefined;

		return {
			csp: STRICT_CSP_POLICY,
			headTags: assets.headTags,
			hydration,
			html:
				'<main data-facetheory-view>' +
				'<h1>FaceTheory probe</h1>' +
				'<p>SSR + strict-no-inline-CSP + JSON-sidecar hydration OK.</p>' +
				'</main>',
		};
	},
};

// ─── Route FaceModules (one per host page, all sharing the shell factory) ────

const routeFaces: FaceModule[] = allRoutes.map((opts) =>
	createShellFace(opts, {
		manifest,
		clientEntry: CLIENT_ENTRY,
		hydrationDataUrlBase: HYDRATION_DATA_URL_BASE,
		title: SHELL_TITLE,
	}),
);

// ─── FaceApp ──────────────────────────────────────────────────────────────────

export const app = createFaceApp({ faces: [probeFace, ...routeFaces] });

/**
 * Request handler the Lambda streaming wrapper drives. The 216fb96
 * `/_facetheory/data/*.json` in-memory pre-router has been retired in
 * M0.12 (commit 8bf29e8): CloudFront now routes `/_facetheory/data/*`
 * directly to S3 via the AppTheorySsrSite-managed distribution + the
 * BucketDeployment of `web/scripts/build-sidecars.mjs` output to the
 * htmlStoreBucket. The SSR Lambda no longer sees sidecar requests, so
 * the handler is just `app.handle` (FaceApp request dispatch).
 *
 * Exported alongside `app` so unit tests + the M0.16 web build-time
 * verifier can exercise the FaceApp without Lambda streaming.
 */
export const requestHandler: { handle: (req: FaceRequest) => Promise<FaceResponse> } = {
	handle: (req: FaceRequest): Promise<FaceResponse> => app.handle(req),
};

// ─── Lambda handler ───────────────────────────────────────────────────────────

/**
 * Lazy Lambda handler. `createLambdaUrlStreamingHandler` reaches for
 * `globalThis.awslambda` (an AWS-Lambda-runtime-only global). Constructing
 * eagerly outside Lambda would throw at module load, so we trap that here:
 * inside Lambda the eager construction wins, outside Lambda a stub throws
 * only on invocation (you should not invoke this outside Lambda anyway).
 */
function createSafeHandler(): ReturnType<typeof createLambdaUrlStreamingHandler> {
	try {
		return createLambdaUrlStreamingHandler({ app: requestHandler });
	} catch (err) {
		const message =
			'face-app handler invoked without globalThis.awslambda; this entry is for AWS Lambda runtime only';
		console.warn('[face-app]', message, err instanceof Error ? err.message : String(err));
		return (async () => {
			throw new Error(message);
		}) as ReturnType<typeof createLambdaUrlStreamingHandler>;
	}
}

export const handler = createSafeHandler();
