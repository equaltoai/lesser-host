# Runbook: simulacrum client app (dev.simulacrum.greater.website)

This runbook describes how to build and deploy the **full client app** for the `dev.simulacrum.greater.website` instance.
The client app source lives in the separate repo at `/home/aron/ai-workspace/codebases/simulacrum`.

## Scope

- Target instance: `dev.simulacrum.greater.website` (base domain: `simulacrum.greater.website`)
- AWS profile: `Sim`
- Client app path: `/home/aron/ai-workspace/codebases/simulacrum`
- Backend + CLI: `/home/aron/ai-workspace/codebases/lesser`

## Preconditions

- The instance is already deployed (`lesser up`) and the stage buckets + CloudFront distribution exist.
- The client app builds to `dist/` with base path `/l/`.
- `greater-components` is used for UI primitives.

## Build the client app

- `cd /home/aron/ai-workspace/codebases/simulacrum`
- Install dependencies (example): `npm ci`
- Build (example): `npm run build`
- Ensure the build output is `dist/` with `index.html` at the root.
- Ensure the app sets base path `/l/` (Vite `base: '/l/'` or SvelteKit `kit.paths.base = '/l'`).

## Deploy to the instance

Use the Lesser CLI to deploy the client bundle to the stage bucket and invalidate CloudFront.

```bash
cd /home/aron/ai-workspace/codebases/lesser
./lesser client deploy \
  --app simulacrum \
  --base-domain simulacrum.greater.website \
  --aws-profile Sim \
  --stage dev \
  --dist /home/aron/ai-workspace/codebases/simulacrum/dist
```

Notes:
- The CLI reads the receipt from `~/.lesser/<app>/<base-domain>/state.json` by default.
- If that file is not present, pass `--state <path>` to the CLI.

## Verify the deploy

- `GET https://dev.simulacrum.greater.website/` should redirect to `/l/`.
- `GET https://dev.simulacrum.greater.website/l/` returns HTML.
- JS/CSS load from `/l/_assets/...`.
- Deep link refresh works: `GET /l/<route>` returns the SPA.

## Troubleshooting

- If assets load from `/_assets/...`, the base path is misconfigured.
- If `/l/` returns 404, the client bucket is empty or the bucket path mapping is incorrect.
- If the CLI fails, inspect `~/.lesser/<app>/<base-domain>/state.json` and confirm the `ClientBucketName` and `FrontendDistributionId` exist.

## References

- `lesser/docs/guides/CLIENT_APP_GUIDE.md`
- `lesser/docs/lesser-cli.md`
