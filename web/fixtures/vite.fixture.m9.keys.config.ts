/**
 * Vite config for the M9 Keys fixture page.
 *
 * Aliases the portalInstances API to mock data so InstanceKeys renders
 * with realistic key-list data without a backend.
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
			'src/lib/api/portalInstances': path.resolve(webDir, 'fixtures/__mocks__/portalInstancesM9Keys.ts'),
			src: path.resolve(webDir, 'src'),
			'@equaltoai/greater-components-icons': path.resolve(webDir, 'src/lib/greater/icons'),
			'@equaltoai/greater-components-primitives': path.resolve(webDir, 'src/lib/greater/primitives'),
			'@equaltoai/greater-components-tokens': path.resolve(webDir, 'src/lib/greater/tokens'),
			'@equaltoai/greater-components-utils': path.resolve(webDir, 'src/lib/greater/utils'),
		},
	},
	server: {
		watch: null,
		port: 5210,
		strictPort: true,
	},
});
