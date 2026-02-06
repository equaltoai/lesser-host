import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
	plugins: [svelte()],
	resolve: {
		alias: {
			src: path.resolve(__dirname, './src'),
		},
	},
	test: {
		environment: 'jsdom',
		include: ['src/**/*.test.ts'],
	},
});
