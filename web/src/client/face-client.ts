/* ============================================================================
 * FaceTheory client entry (host web/)
 *
 * Bootstrap module loaded by FaceTheory's strict-no-inline-CSP hydration path.
 * The SSR Lambda emits a `<script type="module" src=".../face-client-*.js">`
 * head tag (via `viteAssetsForEntry`) plus a JSON sidecar at
 * `/_facetheory/data/<routeId>.json` (via `externalHydrationForEntry`); the
 * browser fetches the sidecar, then evaluates this module to hydrate the
 * server-rendered DOM.
 *
 * M0.4 — per-route shell hydration. Reads the `ShellHydrationPayload` (route
 * discriminator + path) the SSR Lambda wrote to the sidecar, exposes it on
 * `window.__FACETHEORY_SHELL__` for evidence/observability, then mounts the
 * existing `src/App.svelte` into `<div id="app">` exactly as `src/main.ts`
 * does today. App.svelte's in-house router at `src/lib/router.ts` keeps
 * driving the per-page logic, so every existing page is rendered through
 * its FaceModule without a per-page rewrite.
 *
 * M0.5 — `startAwsOacFormTransport()` is wired via `ensureAwsOacFormTransport`
 * (see `oac-form.ts`) so any future `<form data-facetheory-oac-form>` rendered
 * by an SSR-route is submitted with the `x-amz-content-sha256` digest that
 * CloudFront Lambda URL OAC signing requires. The current shells emit no such
 * forms; the transport stages the listener for later (SEC-7 verifier covers
 * marker hygiene once it lands later in M0).
 *
 * The M0.3 `/_facetheory/probe` route still emits the legacy
 * `FaceTheoryProbePayload` shape; we keep reading both payload shapes so the
 * probe + the real routes coexist during lab soak.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.4
 * ========================================================================== */

import { mount } from 'svelte';

import { readFaceHydrationData } from '@theory-cloud/facetheory';

import App from '../App.svelte';
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/styles/greater/primitives.css';
import '../app.css';

import { ensureAwsOacFormTransport } from './oac-form';
import type { ShellHydrationPayload } from '../routes/index';

/** Legacy M0.3 probe payload shape. */
export interface FaceTheoryProbePayload {
	route: string;
	timestamp: string;
}

type FaceTheoryHydrationPayload = ShellHydrationPayload | FaceTheoryProbePayload;

declare global {
	interface Window {
		__FACETHEORY_SHELL__?: ShellHydrationPayload;
		__FACETHEORY_PROBE__?: FaceTheoryProbePayload;
	}
}

function isShellPayload(payload: FaceTheoryHydrationPayload | null): payload is ShellHydrationPayload {
	return !!payload && typeof (payload as ShellHydrationPayload).routeId === 'string';
}

function isProbePayload(payload: FaceTheoryHydrationPayload | null): payload is FaceTheoryProbePayload {
	return (
		!!payload &&
		typeof (payload as FaceTheoryProbePayload).timestamp === 'string' &&
		typeof (payload as { routeId?: string }).routeId !== 'string'
	);
}

const data = readFaceHydrationData<FaceTheoryHydrationPayload>();

if (typeof window !== 'undefined') {
	if (isShellPayload(data)) {
		window.__FACETHEORY_SHELL__ = data;
	} else if (isProbePayload(data)) {
		window.__FACETHEORY_PROBE__ = data;
	}

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
