/* ============================================================================
 * FaceTheory client entry (host web/)
 *
 * Bootstrap module loaded by FaceTheory's strict-no-inline-CSP hydration path.
 * The SSR Lambda emits a `<script type="module" src=".../face-client-*.js">`
 * head tag (via `viteAssetsForEntry`) plus a same-origin JSON hydration
 * sidecar URL pointer (via `externalHydrationForEntry`); FaceTheory's
 * `readFaceHydrationDataUrl(doc)` reads that URL from the
 * `#__FACETHEORY_DATA_URL__` link tag, and this module fetches it before
 * mounting `App.svelte`. `readFaceHydrationData(doc)` is checked first
 * for inline payloads (today's shells never emit inline, but the dual
 * read keeps the client compatible with both modes for SSG / ISR /
 * future inline-hydration routes).
 *
 * M0.4 — per-route shell hydration. Reads the `ShellHydrationPayload`
 * (`{ routeId, route }`) the SSR Lambda wrote to the sidecar via the
 * `/_facetheory/data/<routeId>.json` pre-router in `face-app.ts`, exposes
 * it on `window.__FACETHEORY_SHELL__` for evidence/observability, then
 * mounts the existing `src/App.svelte` into `<div id="app">` exactly as
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
 * The M0.3 `/_facetheory/probe` route still emits the legacy
 * `FaceTheoryProbePayload` shape; we keep reading both payload shapes so the
 * probe + the real routes coexist during lab soak.
 *
 * Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.4
 * ========================================================================== */

import { mount } from 'svelte';

import { readFaceHydrationData, readFaceHydrationDataUrl } from '@theory-cloud/facetheory';

import App from '../App.svelte';
import 'src/lib/styles/greater/tokens.css';
import 'src/lib/styles/greater/primitives.css';
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

/** Fetch the external hydration sidecar URL. Strict same-origin + `redirect:
 *  'error'` so a 307/308 open redirect cannot relocate the request to a
 *  cross-origin endpoint and exfiltrate any future credentials/cookies that
 *  the sidecar handler might consume. Returns `null` on any non-2xx or
 *  parse failure — the mount path runs unconditionally so a missing sidecar
 *  never blocks the user-visible app. */
async function fetchSidecarPayload(rawUrl: string): Promise<FaceTheoryHydrationPayload | null> {
	let url: URL;
	try {
		url = new URL(rawUrl, document.location.origin);
	} catch {
		return null;
	}
	if (url.origin !== document.location.origin) {
		// Cross-origin sidecars are refused regardless of CORS; the SSR
		// Lambda only ever emits same-origin URLs, so a cross-origin URL
		// here would be a marker-tampering attempt.
		return null;
	}
	try {
		const res = await fetch(url, {
			credentials: 'same-origin',
			redirect: 'error',
			headers: { accept: 'application/json' },
		});
		if (!res.ok) {
			return null;
		}
		return (await res.json()) as FaceTheoryHydrationPayload;
	} catch {
		return null;
	}
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
	// 1. Try inline hydration first (FaceInlineHydration; not used by today's
	//    shells but kept for forward compatibility with future SSG/ISR routes).
	const inlineData = readFaceHydrationData<FaceTheoryHydrationPayload>();
	if (inlineData) {
		stashHydrationPayload(inlineData);
	} else {
		// 2. Otherwise look for the external sidecar URL the SSR shell wrote
		//    into `<link id="__FACETHEORY_DATA_URL__" rel="facetheory-hydration">`
		//    and fetch it. Fire-and-forget — the mount runs independently.
		const sidecarUrl = readFaceHydrationDataUrl();
		if (sidecarUrl) {
			void fetchSidecarPayload(sidecarUrl).then(stashHydrationPayload);
		}
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
