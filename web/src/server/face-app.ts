/* ============================================================================
 * FaceTheory server entry (host web/)
 *
 * Builds the host SSR FaceApp and exposes the Lambda Function URL streaming
 * handler. M0.3 lands one minimal probe FaceModule at `/_facetheory/probe`
 * proving the SSR + strict-no-inline-CSP + JSON-sidecar hydration path
 * end-to-end; M0.4 replaces the probe with real Svelte FaceModules ported
 * from the existing `src/pages/*` SPA routes via `createSvelteFace` +
 * `renderSvelte` (from `@theory-cloud/facetheory/svelte`).
 *
 * CSP posture (preserves host's canonical webCsp byte-string):
 *   - Each FaceModule sets `csp: { inlineScripts: false, inlineStyles: false,
 *     rawHead: false }` so FaceTheory validates the rendered HTML is strict-
 *     no-inline, but does *not* attach a CSP header itself. CloudFront's
 *     response-headers policy continues to attach host's canonical webCsp
 *     and safeAppCsp byte-strings (planning trust-and-safety walk,
 *     docs/trust-and-safety-web-ui-rework-2026-05-24.md). FaceTheory's
 *     `buildStrictCspHeader()` is intentionally not invoked here so the
 *     CDN's CSP remains the single source of truth.
 *
 * Manifest contract:
 *   - The client build (vite.config.ts `client` environment) emits a Vite
 *     manifest at `dist/.vite/manifest.json` listing every client chunk and
 *     its hashed asset path. The SSR build emits this entry as
 *     `dist/server/face-app.mjs`, so the relative path is
 *     `../.vite/manifest.json`. CDK packaging (M0.12) co-locates the
 *     manifest with the Lambda bundle on the same relative layout.
 *   - If the manifest can't be loaded (e.g. ahead of CDK packaging), the
 *     probe degrades gracefully to literal HTML without asset tags rather
 *     than crashing on module init.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.3
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
	type ViteManifest,
} from '@theory-cloud/facetheory';

import type { FaceTheoryProbePayload } from '../client/face-client.ts';

// ─── Constants ────────────────────────────────────────────────────────────────

const CLIENT_ENTRY = 'src/client/face-client.ts';
const PROBE_ROUTE = '/_facetheory/probe';
const HYDRATION_DATA_URL = '/_facetheory/data/probe.json';

/**
 * Strict no-inline CSP validation policy. Tells FaceTheory to reject any
 * rendered HTML that would require `'unsafe-inline'` or raw head HTML; the
 * actual `content-security-policy` header is attached by CloudFront, not
 * here, so host's canonical webCsp byte-string remains the only CSP truth.
 */
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
		// Render-time degrade rather than module-init crash. M0.12 ensures
		// the manifest ships with the Lambda bundle in production.
		console.warn(
			'[face-app] client manifest not found at',
			MANIFEST_PATH,
			'— rendering probe without Vite asset tags',
			err instanceof Error ? err.message : String(err),
		);
		return {};
	}
}

const manifest = loadManifest();

// ─── Probe FaceModule ─────────────────────────────────────────────────────────

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
					dataUrl: HYDRATION_DATA_URL,
				})
			: undefined;

		return {
			csp: STRICT_CSP_POLICY,
			headTags: assets.headTags,
			hydration,
			html:
				'<main data-facetheory-view>' +
				'<h1>FaceTheory bootstrap probe</h1>' +
				'<p>SSR + strict-no-inline-CSP + JSON-sidecar hydration ready.</p>' +
				'<p>Real routes ported in M0.4.</p>' +
				'</main>',
		};
	},
};

// ─── App + Lambda handler ─────────────────────────────────────────────────────

export const app = createFaceApp({ faces: [probeFace] });

/**
 * Lazy Lambda handler. `createLambdaUrlStreamingHandler` reaches for
 * `globalThis.awslambda` (an AWS-Lambda-runtime-only global). Constructing
 * eagerly outside Lambda would throw at module load, so we trap that here:
 * inside Lambda the eager construction wins, outside Lambda a stub throws
 * only on invocation (you should not invoke this outside Lambda anyway).
 */
function createSafeHandler(): ReturnType<typeof createLambdaUrlStreamingHandler> {
	try {
		return createLambdaUrlStreamingHandler({ app });
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
