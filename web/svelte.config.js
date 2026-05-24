import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// ============================================================================
// Svelte config — host web/
//
// `vitePreprocess()` is SSR-compatible for Svelte 5: the Svelte 5 compiler
// emits server-rendered HTML through the @sveltejs/vite-plugin-svelte SSR
// path that FaceTheory's `renderSvelte` (from `@theory-cloud/facetheory/svelte`)
// invokes. No additional preprocessing is needed for M0.3 bootstrap; the
// compile-time behavior is unchanged from the pre-rework SPA build. M0.4
// may extend this when the first ported Svelte FaceModule lands.
//
// Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.3
// ============================================================================

/** @type {import("@sveltejs/vite-plugin-svelte").SvelteConfig} */
export default {
	preprocess: vitePreprocess(),
};
