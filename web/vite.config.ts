import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig, loadEnv } from 'vite';

// ============================================================================
// Vite config — host web/
//
// Two build environments (Vite 8 environments API):
//
//   client → CSR SPA (existing pages, unchanged byte-shape) + Vite manifest
//            so FaceTheory's `viteAssetsForEntry` / `externalHydrationForEntry`
//            can resolve hashed client chunks at SSR render time. The new
//            `src/client/face-client.ts` is added as an additional entry so
//            it appears in the manifest as the FaceTheory hydration bootstrap.
//
//   ssr    → SSR Lambda bundle from `src/server/face-app.ts` emitted to
//            `dist/server/face-app.mjs`. The Lambda entry is M0.12's CDK
//            wire-up; for now `npm run build` just produces the artifact.
//
// `builder.buildApp` ensures one `npm run build` invocation walks both
// environments. The dev server still serves the existing CSR SPA via
// `index.html → /src/main.ts`; M0.4 ports each existing page onto a
// Svelte FaceModule.
//
// Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M0.3
// ============================================================================

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');

	const controlPlaneOrigin = env.LESSER_HOST_CONTROL_PLANE_ORIGIN || 'http://localhost:8787';
	const trustOrigin = env.LESSER_HOST_TRUST_ORIGIN || controlPlaneOrigin;

	const greaterAliases = {
		'@equaltoai/greater-components-icons': path.resolve(__dirname, './src/lib/greater/icons'),
		'@equaltoai/greater-components-primitives': path.resolve(__dirname, './src/lib/greater/primitives'),
		'@equaltoai/greater-components-tokens': path.resolve(__dirname, './src/lib/greater/tokens'),
		'@equaltoai/greater-components-utils': path.resolve(__dirname, './src/lib/greater/utils'),
	};

	return {
		plugins: [svelte()],
		resolve: {
			alias: {
				src: path.resolve(__dirname, './src'),
				...greaterAliases,
			},
		},
		server: {
			proxy: {
				'/api': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/auth': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/status': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/bootstrap': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/webauthn': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/admin': { target: controlPlaneOrigin, changeOrigin: true, secure: false },
				'/setup/finalize': { target: controlPlaneOrigin, changeOrigin: true, secure: false },

				'/.well-known': { target: trustOrigin, changeOrigin: true, secure: false },
				'/attestations': { target: trustOrigin, changeOrigin: true, secure: false },
			},
		},
		environments: {
			client: {
				build: {
					outDir: 'dist',
					manifest: true,
					rollupOptions: {
						input: {
							app: path.resolve(__dirname, 'index.html'),
							'face-client': path.resolve(__dirname, 'src/client/face-client.ts'),
						},
					},
				},
			},
			ssr: {
				build: {
					ssr: true,
					outDir: 'dist/server',
					emitAssets: false,
					rollupOptions: {
						input: {
							'face-app': path.resolve(__dirname, 'src/server/face-app.ts'),
						},
						output: {
							format: 'esm',
							entryFileNames: '[name].mjs',
							chunkFileNames: '[name].mjs',
						},
					},
				},
			},
		},
		builder: {
			async buildApp(builder) {
				// Client first so the manifest is on disk before any future
				// SSR test that wants to load it; the two environments are
				// otherwise independent.
				await builder.build(builder.environments.client);
				await builder.build(builder.environments.ssr);
			},
		},
	};
});
