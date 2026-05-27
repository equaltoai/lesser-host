/* ============================================================================
 * FaceTheory client entry (host web/)
 *
 * Bootstrap module loaded by FaceTheory's strict-no-inline-CSP hydration path.
 * The SSR Lambda emits a `<script type="module" src=".../face-client-*.js">`
 * head tag (via `viteAssetsForEntry`) plus a same-origin JSON hydration
 * sidecar URL pointer (via `externalHydrationForEntry`); FaceTheory's
 * canonical `loadFaceHydrationData()` reads any inline payload first, then
 * falls back to fetching the external sidecar referenced by the
 * `#__FACETHEORY_DATA_URL__` link tag.
 *
 * M0.4 — per-route shell hydration. Reads the `ShellHydrationPayload`
 * (`{ routeId, route }`) the SSR Lambda wrote to the sidecar, exposes it on
 * `window.__FACETHEORY_SHELL__` for evidence/observability, then mounts
 * the existing `src/App.svelte` into `<div id="app">` exactly as
 * `src/main.ts` does today. App.svelte's in-house router at
 * `src/lib/router.ts` keeps driving the per-page logic. The mount runs
 * synchronously alongside the fetch — App.svelte does not depend on the
 * hydration payload (shell-only architecture), so a slow sidecar fetch
 * never blocks user-visible rendering.
 *
 * M0.5 — `startAwsOacFormTransport()` is wired via `ensureAwsOacFormTransport`
 * (see `oac-form.ts`) so any future `<form data-facetheory-oac-form>` rendered
 * by an SSR-route is submitted with the `x-amz-content-sha256` digest that
 * CloudFront Lambda URL OAC signing requires.
 *
 * Framework upgrade (this commit) — retire 216fb96 client-side workaround.
 * Earlier, host shipped a bespoke `fetchSidecarPayload` helper that did
 * its own URL construction, same-origin enforcement, and fail-quiet
 * fetch with `redirect: 'error'`, because FaceTheory v3.3.0 did not
 * expose a canonical loader. FaceTheory v3.4.0 ships
 * `loadFaceHydrationData()` (closes equaltoai/lesser-host's framework
 * issue theory-cloud/FaceTheory#250) with built-in `allowedOrigin`
 * enforcement (pre-fetch URL check + post-fetch redirect-chain check)
 * and a `requestInit` passthrough that lets host keep its stricter
 * `redirect: 'error'` posture (the framework default is `follow`).
 * The server-side `/_facetheory/data/*.json` pre-router in
 * `src/server/face-app.ts` stays in place for now — its canonical
 * replacement is the `createSsrHydrationSidecarStore` HMAC-signed,
 * TTL-bound, S3-backed pattern, which is M0.12's responsibility
 * (AppTheorySsrSite + JSON-sidecar S3 behavior).
 *
 * The M0.3 `/_facetheory/probe` route still emits the legacy
 * `FaceTheoryProbePayload` shape; we keep reading both payload shapes so the
 * probe + the real routes coexist during lab soak.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.4
 * ========================================================================== */

import { mount } from 'svelte';

// Use the v3.4.0 client subpath: the top-level `@theory-cloud/facetheory`
// entry re-exports SSR modules that import `node:crypto`/`node:path`/
// `node:fs/promises` — Vite externalizes them with a browser-compat warning
// on every build. The `./client` subpath ships only browser-safe helpers
// and is the framework's intended import for client bundles.
import { loadFaceHydrationData } from '@theory-cloud/facetheory/client';

import App from '../App.svelte';
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/styles/greater/primitives.css';
import 'src/lib/styles/greater/shell.css';
import 'src/lib/styles/greater/host-platform.css';
import 'src/lib/styles/m1-primitives.css';
import '../app.css';

import { ensureAwsOacFormTransport } from './oac-form';
import type { ShellHydrationPayload } from '../routes/index';

/** Legacy M0.3 probe payload shape. */
export interface FaceTheoryProbePayload {
	route: string;
	timestamp?: string;
}

type FaceTheoryHydrationPayload = ShellHydrationPayload | FaceTheoryProbePayload;

declare global {
	interface Window {
		__FACETHEORY_SHELL__?: ShellHydrationPayload;
		__FACETHEORY_PROBE__?: FaceTheoryProbePayload;
	}
}

function isShellPayload(payload: unknown): payload is ShellHydrationPayload {
	return !!payload && typeof (payload as { routeId?: unknown }).routeId === 'string';
}

function isProbePayload(payload: unknown): payload is FaceTheoryProbePayload {
	return (
		!!payload &&
		typeof (payload as { route?: unknown }).route === 'string' &&
		typeof (payload as { routeId?: unknown }).routeId !== 'string'
	);
}

function stashHydrationPayload(payload: unknown): void {
	if (isShellPayload(payload)) {
		window.__FACETHEORY_SHELL__ = payload;
	} else if (isProbePayload(payload)) {
		window.__FACETHEORY_PROBE__ = payload;
	}
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
	// Hydration: inline first (forward-compat with future SSG/ISR routes),
	// then external sidecar fetch via the canonical loader. Posture overrides:
	//   - `allowedOrigin` pinned to `document.location.origin`: the SSR Lambda
	//     only ever emits same-origin sidecar URLs; any cross-origin URL would
	//     indicate marker-tampering. FaceTheory v3.4.0 enforces this both
	//     pre-fetch (URL resolution) and post-fetch (final-URL recheck after
	//     redirects). Explicit here for audit clarity.
	//   - `requestInit.redirect: 'error'`: framework default is `follow` + a
	//     post-fetch cross-origin check; host opts into the stricter "refuse
	//     any redirect at all" posture because the SSR Lambda emits URLs that
	//     are served directly with 2xx, so a 3xx is anomalous and likely
	//     indicates an open-redirect / tampering attempt regardless of where
	//     it points. Defense-in-depth on top of the framework's same-origin
	//     guarantee.
	// Fail-quiet via `.catch`: a missing or invalid sidecar never blocks the
	// user-visible mount — App.svelte does not depend on the hydration payload.
	void loadFaceHydrationData<FaceTheoryHydrationPayload>({
		allowedOrigin: document.location.origin,
		requestInit: { redirect: 'error' },
	})
		.then(stashHydrationPayload)
		.catch(() => {
			/* swallowed: fail-quiet preserves user-visible mount */
		});

	// Stage the OAC-safe form transport once per document. Idempotent; safe
	// to call before any marked `<form data-facetheory-oac-form>` exists in
	// the DOM — the transport listens for `submit` events lazily.
	ensureAwsOacFormTransport();

	// Mount the existing host app into the SSR-emitted `<div id="app">`. The
	// shell HTML contains no Svelte SSR'd content (M0.4 is shell-only); the
	// client mount drives every page exactly as `src/main.ts` does for the
	// legacy CSR entrypoint, so existing pages and `src/lib/router.ts` keep
	// working unchanged. M0.6+ replaces this `mount` with `hydrate` when the
	// shell primitives ship from greater-components.
	const target = document.getElementById('app');
	if (target) {
		mount(App, { target });
	}
}

export {};
