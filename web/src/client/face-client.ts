/* ============================================================================
 * FaceTheory client entry (host web/)
 *
 * Bootstrap module loaded by FaceTheory's strict-no-inline-CSP hydration path.
 * The SSR Lambda emits a `<script type="module" src=".../face-client-*.js">`
 * head tag (via `viteAssetsForEntry`) plus a JSON sidecar at
 * `/_facetheory/data/<route>.json` (via `externalHydrationForEntry`); the
 * browser fetches the sidecar, then evaluates this module to hydrate the
 * server-rendered DOM.
 *
 * M0.3 — bootstrap only. We read the hydration payload and stash it on
 * `window.__FACETHEORY_PROBE__` so the probe response is visibly hydrated
 * during the M0 lab soak without claiming any real route. M0.4 ports the
 * existing Portal / Operator / Trust / Login / Setup / Account / Home /
 * NotFound / TipRegistry routes onto Svelte FaceModules and replaces this
 * stash with `mount(...)`-style hydration of the real components. M0.5 adds
 * `startAwsOacFormTransport()` here so any marked `<form
 * data-facetheory-oac-form>` rendered in M0.4+ submits with the right
 * `x-amz-content-sha256` signing.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.3
 * ========================================================================== */

import { readFaceHydrationData } from '@theory-cloud/facetheory';

export interface FaceTheoryProbePayload {
	route: string;
	timestamp: string;
}

// Read the hydration payload the SSR Lambda wrote to the sidecar. Returns
// `null` if the document has no FaceTheory hydration marker (e.g. when this
// bundle is loaded by a not-yet-ported legacy page); the probe just no-ops in
// that case until M0.4 ports real routes.
const data = readFaceHydrationData<FaceTheoryProbePayload>();

if (typeof window !== 'undefined' && data) {
	(window as unknown as { __FACETHEORY_PROBE__?: FaceTheoryProbePayload }).__FACETHEORY_PROBE__ = data;
}

export {};
