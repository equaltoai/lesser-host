/* ============================================================================
 * Shell FaceModule factory (host web/)
 *
 * Every existing host page is represented by a FaceModule that emits a thin
 * SSR shell (just a `<main data-facetheory-view>` mount-point) plus the
 * hashed `face-client.ts` `<link rel="modulepreload">` head tags and the
 * external hydration JSON sidecar at `/_facetheory/data/<routeId>.json`.
 * The actual page rendering still happens client-side: `face-client.ts`
 * dynamically mounts the existing `src/App.svelte` (whose in-house router at
 * `src/lib/router.ts` keeps driving the per-page logic). This split keeps
 * the SSR Lambda free of browser-only code (no `window`, `document`,
 * `sessionStorage`) while still satisfying the M0.4 acceptance "every
 * existing page renders via a FaceModule".
 *
 * Strict no-inline CSP is preserved:
 *   - `csp: { inlineScripts: false, inlineStyles: false, rawHead: false }`
 *     tells FaceTheory to validate the rendered HTML is strict-no-inline.
 *   - No `content-security-policy` header is attached; CloudFront's
 *     response-headers policy continues to attach host's canonical webCsp
 *     byte-string as the only CSP source of truth.
 *   - Head tags come from `viteAssetsForEntry` (external <script>/<link>
 *     tags pointing to hashed assets), never inline.
 *   - Hydration data lives in an external JSON sidecar via
 *     `externalHydrationForEntry` — never inlined.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.4
 * ========================================================================== */

import {
	externalHydrationForEntry,
	viteAssetsForEntry,
	type FaceCspPolicy,
	type FaceModule,
	type ViteManifest,
} from '@theory-cloud/facetheory';

/** Discriminator written into the hydration sidecar so the client knows which
 *  route family was matched on the server. The client mounts `App.svelte`
 *  unconditionally (its own router picks the page); the discriminator exists
 *  for future per-route shell adoption (M0.6+) and for evidence/audit. */
export interface ShellHydrationPayload {
	readonly routeId: string;
	readonly route: string;
	readonly path: string;
}

export interface ShellFaceOptions {
	/** FaceTheory route pattern; supports static segments, `{name}` params,
	 *  and trailing `{name*}` / `{name+}` proxy patterns per `Router`.add(). */
	readonly route: string;
	/** Stable discriminator name written into the hydration sidecar. */
	readonly routeId: string;
}

export interface ShellFaceFactoryDeps {
	readonly manifest: ViteManifest;
	readonly clientEntry: string;
	readonly hydrationDataUrlBase: string;
	readonly title?: string;
}

const STRICT_CSP_POLICY: FaceCspPolicy = Object.freeze({
	inlineScripts: false,
	inlineStyles: false,
	rawHead: false,
});

/**
 * Returns a `FaceModule` that:
 *   1. Loads a tiny route-metadata payload on the server.
 *   2. Renders a strict-no-inline-CSP HTML shell with manifest-resolved
 *      asset head tags + external hydration sidecar.
 *
 * The factory takes the manifest + client entry as construction-time deps
 * so the shell renders are pure functions of the request path (the manifest
 * is loaded once at module init in `face-app.ts`).
 */
export function createShellFace(
	options: ShellFaceOptions,
	deps: ShellFaceFactoryDeps,
): FaceModule {
	const { route, routeId } = options;
	const { manifest, clientEntry, hydrationDataUrlBase, title } = deps;
	const hydrationDataUrl = `${hydrationDataUrlBase}${routeId}.json`;
	const hasManifestEntry = Object.hasOwn(manifest, clientEntry);

	return {
		route,
		mode: 'ssr',
		load: async (ctx): Promise<ShellHydrationPayload> => ({
			routeId,
			route,
			path: ctx.request.path,
		}),
		render: async (_ctx, data) => {
			const assets = hasManifestEntry
				? viteAssetsForEntry(manifest, clientEntry, { includeAssets: true })
				: { bootstrapModule: '', headTags: [] };
			const hydration = hasManifestEntry
				? externalHydrationForEntry(manifest, clientEntry, data, { dataUrl: hydrationDataUrl })
				: undefined;

			const titleTag = title
				? [{ type: 'title' as const, text: title }]
				: [];

			return {
				csp: STRICT_CSP_POLICY,
				headTags: [...titleTag, ...assets.headTags],
				hydration,
				// The mount point matches the legacy `index.html` `<div id="app">`
				// so `App.svelte`'s `mount({ target: document.getElementById('app') })`
				// keeps working with no client-side change.
				html: '<div id="app" data-facetheory-view></div>',
			};
		},
	};
}
