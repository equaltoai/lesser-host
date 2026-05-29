/**
 * Vite config for the M13 Souls fixture page.
 *
 * Minimal config that serves ONLY `fixtures/m13-souls.html`. The entry
 * point intercepts `window.fetch` to mock all API endpoints, so no backend
 * proxy or API server is needed.
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
			src: path.resolve(webDir, 'src'),
			'@equaltoai/greater-components-icons': path.resolve(webDir, 'src/lib/greater/icons'),
			'@equaltoai/greater-components-primitives': path.resolve(webDir, 'src/lib/greater/primitives'),
			'@equaltoai/greater-components-tokens': path.resolve(webDir, 'src/lib/greater/tokens'),
			'@equaltoai/greater-components-utils': path.resolve(webDir, 'src/lib/greater/utils'),
		},
	},
	server: {
		watch: null,
		port: 5205,
		strictPort: true,
	},
	build: {
		outDir: path.resolve(webDir, 'fixtures-dist'),
		rollupOptions: {
			input: path.resolve(webDir, 'fixtures/m13-souls.html'),
		},
	},
});
