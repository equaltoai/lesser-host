# Lesser release contract (consumed by lesser.host)

This document defines the Lesser release surface that `lesser-host` consumes for managed deploys.

It is intentionally narrower than Lesser's full repo-local deploy contract: this doc is about the release assets and
receipt expectations that the managed runner depends on.

Managed rollout decisions for those releases are gated separately by `docs/managed-release-certification.md`.

## Supported managed compatibility contract

The canonical machine-readable compatibility boundary that `lesser-host` supports today lives at:

- `docs/spec/lesser-managed-compatibility.json`

This contract is intentionally explicit:

- the earliest Lesser release tag supported for managed updates is `v1.2.6`
- the release must still satisfy the published release-manifest and lambda-bundle shape described there

That file is verified in CI against the same constants the managed release preflight uses, so operators and rollout
automation can check support before starting a managed update.

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
  - downloads and verifies the published release assets
  - materializes the release-matched Lesser checkout from `lesser-release.json.git_sha`
  - ensures the Go toolchain declared by `lesser-release.json.go_version` is available before invoking Lesser
  - runs the published CLI binary from inside that checkout so repo-local CDK and inventory discovery stay aligned with the release
  - runs:

```bash
(cd "$LESSER_CHECKOUT_DIR" && "$LESSER_RELEASE_DIR/lesser" up \
  --app "$APP_SLUG" \
  --base-domain "$BASE_DOMAIN" \
  --aws-profile managed \
  --provisioning-input "$PROVISION_INPUT" \
  --release-dir "$LESSER_RELEASE_DIR")
```

  - when the managed provisioning input includes `consent_message` and `consent_signature`, immediately seeds the
    initial admin from the same verified release CLI:

```bash
(cd "$LESSER_CHECKOUT_DIR" && "$LESSER_RELEASE_DIR/lesser" init-admin \
  --base-domain "$BASE_DOMAIN" \
  --aws-profile managed \
  --provisioning-input "$PROVISION_INPUT")
```

    For Lesser M9 and later, `consent_message` is the exact compact JSON string described in
    `docs/managed-instance-provisioning.md#structured-init-admin-consent-lesser-m9`. The EIP-191 signature is over those
    exact bytes; the managed runner must not trim, pretty-print, or reserialize the string between portal signing and
    `init-admin`.

- `RUN_MODE=lesser-mcp`
  - downloads and verifies the same published release assets
  - materializes the same release-matched Lesser checkout
  - ensures the same release-declared Go toolchain is active
  - runs the published CLI binary from inside that checkout with `--release-dir`

This means the managed runner no longer recompiles Lesser Lambdas in the happy path.

## Lesser M9 consumption gate

host does not treat a merged Lesser PR or a trusted commit as sufficient for managed rollout. A Lesser M9 release that
enforces structured `init-admin` consent is eligible for managed consumption only after:

1. the exact published GitHub Release assets are available;
2. host's managed runner downloads and checksum-verifies those assets through the normal release path;
3. managed-release certification/readiness succeeds for that tag;
4. lab managed provisioning reaches `lesser init-admin` with the structured consent JSON and succeeds; and
5. a canary managed instance is stable before default-version or live rollout.

If any asset checksum, manifest field, schema field, or certification evidence mismatches, provisioning/update approval
must fail closed. Do not bypass checksum verification to consume Lesser M9.

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
