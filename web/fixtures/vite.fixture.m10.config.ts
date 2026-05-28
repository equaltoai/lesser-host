/**
 * Vite config for the M10 Instance Souls tab fixture page.
 *
 * Aliases both portalInstances and soul APIs to mock data so
 * InstanceSouls renders the design-aligned Souls table with
 * realistic agent data without a backend.
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
			'src/lib/api/portalInstances': path.resolve(webDir, 'fixtures/__mocks__/portalInstancesM10Souls.ts'),
			'src/lib/api/soul': path.resolve(webDir, 'fixtures/__mocks__/soulM10.ts'),
			src: path.resolve(webDir, 'src'),
			'@equaltoai/greater-components-icons': path.resolve(webDir, 'src/lib/greater/icons'),
			'@equaltoai/greater-components-primitives': path.resolve(webDir, 'src/lib/greater/primitives'),
			'@equaltoai/greater-components-tokens': path.resolve(webDir, 'src/lib/greater/tokens'),
			'@equaltoai/greater-components-utils': path.resolve(webDir, 'src/lib/greater/utils'),
		},
	},
	server: {
		watch: null,
		port: 5212,
		strictPort: true,
	},
	build: {
		outDir: path.resolve(webDir, 'fixtures-dist'),
		rollupOptions: {
			input: path.resolve(webDir, 'fixtures/m10-instance-souls-ui.html'),
		},
	},
});
