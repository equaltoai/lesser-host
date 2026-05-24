/* ============================================================================
 * FaceTheory server entry (host web/)
 *
 * Builds the host SSR FaceApp, wraps it with a `/_facetheory/data/*.json`
 * sidecar pre-router, and exposes the Lambda Function URL streaming
 * handler. M0.4 ports every existing host page onto its own FaceModule via
 * the shell factory in `src/routes/_shell.ts`; the SSR Lambda emits a
 * strict-no-inline-CSP HTML shell + external hydration sidecar URL, and
 * the existing `src/App.svelte` (mounted by `src/client/face-client.ts`)
 * keeps handling per-page routing and rendering. The M0.3 probe at
 * `/_facetheory/probe` is retained as a per-request health signal.
 *
 * Why a sidecar pre-router (FaceTheory v3.3.0 limitation): FaceTheory's
 * runtime always wraps FaceModule responses in `renderHTMLDocument(...)`
 * (see `app.js#toHTTPResponse`), so JSON sidecars cannot be served from a
 * FaceModule. ISR mode exposes `onHydrationSidecar` for persistence, but
 * SSR mode has no equivalent. The host-side pre-router below shells the
 * static `{routeId, route}` payload for `/_facetheory/data/<routeId>.json`
 * before delegating to the FaceApp. Captured as a framework-feedback
 * candidate (see memory note `mem-66a93e3b1c3a1ce1` for the related
 * client-bundle export-map signal).
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

import { allRoutes, createShellFace, type ShellHydrationPayload } from '../routes/index';
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

const PROBE_ROUTE_ID = 'probe';

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

// ─── Sidecar pre-router ───────────────────────────────────────────────────────
//
// Map routeId → the static hydration payload the SSR shells reference via
// `externalHydrationForEntry({ dataUrl: '/_facetheory/data/<routeId>.json' })`.
// FaceTheory's runtime only serves SSG/ISR sidecars natively; for SSR we
// serve the JSON ourselves here so the dataUrl actually resolves.

interface SidecarEntry {
	readonly contentType: string;
	readonly cacheControl: string;
	readonly payload: ShellHydrationPayload | { route: string };
}

const SIDECAR_PATH_PREFIX = HYDRATION_DATA_URL_BASE; // '/_facetheory/data/'
const SIDECAR_PATH_SUFFIX = '.json';
const SIDECAR_CONTENT_TYPE = 'application/json; charset=utf-8';
const SIDECAR_CACHE_CONTROL = 'no-store';

const sidecarById: ReadonlyMap<string, SidecarEntry> = new Map(
	[
		// Real shell routes — static `{ routeId, route }` payload per routeId.
		...allRoutes.map<readonly [string, SidecarEntry]>((r) => [
			r.routeId,
			{
				contentType: SIDECAR_CONTENT_TYPE,
				cacheControl: SIDECAR_CACHE_CONTROL,
				payload: { routeId: r.routeId, route: r.route },
			},
		]),
		// M0.3 probe route — sidecar holds only the route name (the request-
		// time timestamp the probe's render emits is intentionally not in the
		// sidecar so the URL stays deterministic).
		[
			PROBE_ROUTE_ID,
			{
				contentType: SIDECAR_CONTENT_TYPE,
				cacheControl: SIDECAR_CACHE_CONTROL,
				payload: { route: PROBE_ROUTE },
			},
		],
	],
);

function isSidecarPath(path: string): boolean {
	return path.startsWith(SIDECAR_PATH_PREFIX) && path.endsWith(SIDECAR_PATH_SUFFIX);
}

function resolveSidecarRouteId(path: string): string | null {
	const tail = path.slice(SIDECAR_PATH_PREFIX.length, path.length - SIDECAR_PATH_SUFFIX.length);
	// Reject path traversal / nested segments / empty tail — sidecar URLs are
	// single-segment routeIds only.
	if (!tail || tail.includes('/') || tail.includes('..')) {
		return null;
	}
	return tail;
}

function makeJsonResponse(status: number, body: string, headers: Record<string, string>): FaceResponse {
	const normalized: Record<string, string[]> = {};
	for (const [k, v] of Object.entries(headers)) {
		normalized[k.toLowerCase()] = [v];
	}
	return {
		status,
		headers: normalized,
		cookies: [],
		body: new TextEncoder().encode(body),
		isBase64: false,
	};
}

function handleSidecarRequest(req: FaceRequest): FaceResponse {
	if (req.method !== 'GET' && req.method !== 'HEAD') {
		return makeJsonResponse(
			405,
			JSON.stringify({ error: 'method_not_allowed' }),
			{ 'content-type': SIDECAR_CONTENT_TYPE, allow: 'GET, HEAD' },
		);
	}
	const routeId = resolveSidecarRouteId(req.path);
	if (routeId === null) {
		return makeJsonResponse(
			404,
			JSON.stringify({ error: 'not_found' }),
			{ 'content-type': SIDECAR_CONTENT_TYPE },
		);
	}
	const entry = sidecarById.get(routeId);
	if (!entry) {
		return makeJsonResponse(
			404,
			JSON.stringify({ error: 'unknown_route_id', routeId }),
			{ 'content-type': SIDECAR_CONTENT_TYPE },
		);
	}
	return makeJsonResponse(
		200,
		JSON.stringify(entry.payload),
		{
			'content-type': entry.contentType,
			'cache-control': entry.cacheControl,
		},
	);
}

/**
 * Request handler that the Lambda streaming wrapper drives. Pre-routes
 * `/_facetheory/data/*.json` to the static sidecar map, then delegates
 * every other request to the FaceApp. Exported (alongside `app`) so unit
 * tests + the M0.16 web build-time verifier can exercise the full handler
 * tree without spinning up Lambda streaming.
 */
export const requestHandler: { handle: (req: FaceRequest) => Promise<FaceResponse> } = {
	async handle(req: FaceRequest): Promise<FaceResponse> {
		if (isSidecarPath(req.path)) {
			return handleSidecarRequest(req);
		}
		return app.handle(req);
	},
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
