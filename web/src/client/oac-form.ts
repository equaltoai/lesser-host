/* ============================================================================
 * OAC-safe form transport bootstrap (host web/)
 *
 * Idempotent client-side bootstrap that wires
 * `startAwsOacFormTransport()` from `@theory-cloud/facetheory/oac-form` once per
 * document. The transport listens for `submit` events on any
 * `<form data-facetheory-oac-form>` and rewrites the request so:
 *   - The URL-encoded body is hashed (SHA-256) and the digest is set as
 *     `x-amz-content-sha256` — the header CloudFront Lambda URL OAC signing
 *     requires for body-bearing requests.
 *   - The mutating fetch runs with `redirect: 'error'` so a 307/308 open
 *     redirect cannot replay the signed body to another origin.
 *
 * M0.5 stages the transport ahead of any consumer:
 *   - The current FaceModule shells emit no `<form data-facetheory-oac-form>`
 *     (verified by `grep -r data-facetheory-oac-form web/dist`), so the
 *     transport hooks the document's `submit` listener but never intercepts
 *     a real form during M0 lab soak. M0.6+ shell primitives may introduce
 *     the first marked form.
 *   - Bootstrap is idempotent: a module-scoped flag prevents double-start if
 *     `face-client.ts` is somehow loaded twice (e.g. dev HMR replays).
 *
 * Posture invariants:
 *   - `allowedOrigin` defaults to `document.location.origin`, so off-origin
 *     POSTs are rejected by the transport before any signing happens —
 *     consistent with host's multi-tenant per-slug origin isolation.
 *   - `strictCsp: true` matches host's `style-src 'self'; script-src 'self'`
 *     posture; FaceTheory rejects SPA navigation if response HTML would
 *     require inline styles/scripts.
 *   - `markerAttribute` stays at the default `data-facetheory-oac-form`;
 *     SEC-7 verifier (lands later in M0) asserts the marker is exclusively
 *     applied to forms whose action is OAC-signed.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.5
 * ========================================================================== */

import {
	startAwsOacFormTransport,
	type AwsOacFormTransportController,
} from '@theory-cloud/facetheory/oac-form';

let controller: AwsOacFormTransportController | null = null;

/**
 * Start the OAC-safe form transport for the current document. Safe to call
 * multiple times — subsequent calls return the existing controller without
 * re-registering listeners. Returns `null` outside browser environments
 * (e.g. during SSR rendering of `face-client.ts` if it ever gets pulled
 * into a server build, which today it does not).
 */
export function ensureAwsOacFormTransport(): AwsOacFormTransportController | null {
	if (controller) {
		return controller;
	}
	if (typeof window === 'undefined' || typeof document === 'undefined') {
		return null;
	}
	controller = startAwsOacFormTransport({
		// allowedOrigin defaults to document.location.origin; explicit here for
		// audit clarity and to make multi-origin misconfigurations obvious.
		allowedOrigin: document.location.origin,
		// Host's strict no-inline CSP posture; transport rejects SPA-style
		// navigation HTML that would require inline scripts/styles.
		strictCsp: true,
	});
	return controller;
}
