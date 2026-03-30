# Lesser release contract (consumed by lesser.host)

This document defines the Lesser release surface that `lesser-host` consumes for managed deploys.

It is intentionally narrower than Lesser's full repo-local deploy contract: this doc is about the release assets and
receipt expectations that the managed runner depends on.

Managed rollout decisions for those releases are gated separately by `docs/managed-release-certification.md`.

## Required Lesser release assets

Every managed-consumable Lesser release must publish:

- CLI binaries:
  - `lesser-linux-amd64`
  - `lesser-linux-arm64`
- Release metadata:
  - `checksums.txt`
  - `lesser-release.json`
- First-phase deploy artifacts:
  - `lesser-lambda-bundle.tar.gz`
  - `lesser-lambda-bundle.json`

The runner verifies all four release-asset files before using them:

- `checksums.txt`
- `lesser-release.json`
- `lesser-lambda-bundle.tar.gz`
- `lesser-lambda-bundle.json`

`lesser-release.json` must describe the published Lambda bundle at:

- `artifacts.deploy_artifacts.lambda_bundle.path = "lesser-lambda-bundle.tar.gz"`
- `artifacts.deploy_artifacts.lambda_bundle.manifest_path = "lesser-lambda-bundle.json"`

## Managed runner execution model

The current managed Lesser path is a two-input deploy:

1. A Lesser checkout and CLI binary for repo-local CDK and `auth-ui` execution.
2. A verified release directory passed into Lesser with `--release-dir`.

The managed runner currently consumes Lesser in two ways:

- `RUN_MODE=lesser`
  - downloads the Lesser checkout and CLI binary
  - downloads and verifies the published release assets
  - runs:

```bash
./lesser up \
  --app "$APP_SLUG" \
  --base-domain "$BASE_DOMAIN" \
  --aws-profile managed \
  --provisioning-input "$PROVISION_INPUT" \
  --release-dir "$LESSER_RELEASE_DIR"
```

- `RUN_MODE=lesser-mcp`
  - downloads and verifies the same published release assets
  - extracts `lesser-lambda-bundle.tar.gz`
  - verifies the extracted `bin/*.zip` files against `lesser-lambda-bundle.json`
  - passes the staged asset root into the direct stage-stack deploy as `lambdaAssetRoot`

This means the managed runner no longer recompiles Lesser Lambdas in the happy path.

## Lesser-managed receipt contract

Lesser still owns the canonical local deploy receipt:

- `~/.lesser/<app>/<base-domain>/state.json`

For managed deploys, `lesser-host` uploads an enriched copy of that receipt to:

- `managed/provisioning/<slug>/<jobId>/state.json`
- `managed/updates/<slug>/<jobId>/state.json`

The enriched managed copy preserves Lesser's native receipt fields and adds:

```json
{
  "managed_deploy_artifacts": {
    "mode": "release",
    "checksums_path": "checksums.txt",
    "release_manifest_path": "lesser-release.json",
    "release": {
      "name": "lesser",
      "version": "v1.2.4",
      "git_sha": "abc123"
    },
    "deploy_artifact": {
      "kind": "lambda_bundle",
      "path": "lesser-lambda-bundle.tar.gz",
      "manifest_path": "lesser-lambda-bundle.json",
      "files": ["bin/api.zip", "bin/graphql.zip"],
      "prepared_at": "2026-03-30T01:00:00Z"
    }
  }
}
```

The added `managed_deploy_artifacts` object is the host-side provenance surface. It must not remove or rename Lesser's
native receipt fields that the control plane already ingests.

## Non-goals

This contract still does not mean the managed runner is fully source-free. Current managed execution still depends on:

- repo-local `infra/cdk/`
- repo-local `auth-ui/`
- deploy-time AWS credentials, hosted-zone discovery, and instance inputs

Those are phase-2 immutable-deploy concerns, not part of the phase-1 release asset contract.
