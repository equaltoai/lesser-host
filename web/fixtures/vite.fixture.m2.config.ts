/**
 * Vite config for the M2 shell fixture page.
 *
 * Minimal config that serves ONLY `fixtures/m2-shell.html` and aliases
 * the portal API modules to mock implementations so PortalShell renders
 * with realistic instance + portalMe data without a backend.
 *
 * File watching is disabled so this works in headless environments
 * where inotify watchers are exhausted.
 *
 * NOT used by the main web build or any customer portal route.
 *
 * @license AGPL-3.0-only
 */
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(__dirname, '..');

export default defineConfig({
	root: webDir,
	plugins: [svelte()],
	resolve: {
		alias: {
			// Mock API modules — MUST precede the general `src` alias so
			// Vite matches these specific prefixes before the broad catch-all.
			// PortalShell's loadSidebarData() calls these on mount; the mocks
			// return realistic fixture data so the sidebar renders per-instance
			// entries and the user chip shows display_name.
			'src/lib/api/portalInstances': path.resolve(webDir, 'fixtures/__mocks__/portalInstances.ts'),
			'src/lib/api/portal': path.resolve(webDir, 'fixtures/__mocks__/portal.ts'),
			src: path.resolve(webDir, 'src'),
			'@equaltoai/greater-components-icons': path.resolve(webDir, 'src/lib/greater/icons'),
			'@equaltoai/greater-components-primitives': path.resolve(webDir, 'src/lib/greater/primitives'),
			'@equaltoai/greater-components-tokens': path.resolve(webDir, 'src/lib/greater/tokens'),
			'@equaltoai/greater-components-utils': path.resolve(webDir, 'src/lib/greater/utils'),
		},
	},
	server: {
		watch: null,
		port: 5200,
		strictPort: true,
	},
	build: {
		outDir: path.resolve(webDir, 'fixtures-dist'),
		rollupOptions: {
			input: path.resolve(webDir, 'fixtures/m2-shell.html'),
		},
	},
});
