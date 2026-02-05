import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig, loadEnv } from 'vite';

// https://vite.dev/config/
const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');

	const controlPlaneOrigin = env.LESSER_HOST_CONTROL_PLANE_ORIGIN || 'http://localhost:8787';
	const trustOrigin = env.LESSER_HOST_TRUST_ORIGIN || controlPlaneOrigin;

	return {
		plugins: [svelte()],
		resolve: {
			alias: {
				src: path.resolve(__dirname, './src'),
			},
		},
		server: {
			proxy: {
				'/api': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/auth': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/status': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/bootstrap': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/admin': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/finalize': { target: controlPlaneOrigin, changeOrigin: true, secure: false },

				'/.well-known': { target: trustOrigin, changeOrigin: true, secure: false },
				'/attestations': { target: trustOrigin, changeOrigin: true, secure: false },
			},
		},
	};
});
