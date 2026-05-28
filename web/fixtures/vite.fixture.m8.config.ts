/**
 * Vite config for M8 Instance Cost UI fixture.
 *
 * Provides the resolve aliases and environment needed to render
 * the M8InstanceCostFixture in a standalone HTML page. This config
 * is NOT used by any customer portal build.
 *
 * @license AGPL-3.0-only
 */

import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import path from 'node:path';

export default defineConfig({
	plugins: [svelte()],
	resolve: {
		alias: {
			src: path.resolve(__dirname, '../src'),
		},
	},
	root: path.resolve(__dirname),
});
