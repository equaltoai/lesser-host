/**
 * Vite config for the M8 Instance Cost UI fixture page.
 *
 * Minimal config that serves ONLY `fixtures/m8-instance-cost-ui.html` and
 * aliases the portalUsage API module to a mock implementation so the
 * InstanceCost component renders with realistic budget, usage, and cost
 * telemetry data without a backend.
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
			// Mock the portalUsage API module — MUST precede the general `src`
			// alias so Vite matches this specific prefix before the broad catch-all.
			// InstanceCost calls portalGetBudgetMonth, portalGetUsageSummary, and
			// portalGetInstanceCost on mount; the mock returns realistic fixture
			// data so all M8 design surfaces render fully populated.
			'src/lib/api/portalUsage': path.resolve(webDir, 'fixtures/__mocks__/portalUsage.ts'),
			src: path.resolve(webDir, 'src'),
			'@equaltoai/greater-components-icons': path.resolve(webDir, 'src/lib/greater/icons'),
			'@equaltoai/greater-components-primitives': path.resolve(webDir, 'src/lib/greater/primitives'),
			'@equaltoai/greater-components-tokens': path.resolve(webDir, 'src/lib/greater/tokens'),
			'@equaltoai/greater-components-utils': path.resolve(webDir, 'src/lib/greater/utils'),
		},
	},
	server: {
		watch: null,
		port: 5208,
		strictPort: true,
	},
	build: {
		outDir: path.resolve(webDir, 'fixtures-dist'),
		rollupOptions: {
			input: path.resolve(webDir, 'fixtures/m8-instance-cost-ui.html'),
		},
	},
});
