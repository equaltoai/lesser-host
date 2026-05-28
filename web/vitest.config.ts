import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
	plugins: [svelte()],
	resolve: {
		conditions: ['browser'],
		alias: {
			src: path.resolve(__dirname, './src'),
			'@equaltoai/greater-components-icons': path.resolve(__dirname, './src/lib/greater/icons'),
			'@equaltoai/greater-components-primitives': path.resolve(__dirname, './src/lib/greater/primitives'),
			'@equaltoai/greater-components-tokens': path.resolve(__dirname, './src/lib/greater/tokens'),
			'@equaltoai/greater-components-utils': path.resolve(__dirname, './src/lib/greater/utils'),
		},
	},
	test: {
		environment: 'jsdom',
		include: ['src/**/*.test.ts'],
	},
});
