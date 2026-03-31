# lesser-body release contract (consumed by lesser.host)

This document defines the `lesser-body` release surface that `lesser-host` consumes for managed body deploys.

It is intentionally narrower than the full `lesser-body` producer contract. This doc covers only the release assets,
manifest fields, runner expectations, and receipt provenance that the managed host path depends on.

Managed rollout decisions for those releases are gated separately by `docs/managed-release-certification.md`.

## Supported managed compatibility contract

The canonical machine-readable compatibility boundary that `lesser-host` supports today lives at:

- `docs/spec/lesser-body-managed-compatibility.json`

That file is verified in CI against the same constants the managed body preflight uses, so operators and rollout
automation can check support before starting a managed `lesser-body` update.

## Required lesser-body release assets

Every managed-consumable `lesser-body` release must publish:

- `checksums.txt`
- `lesser-body-release.json`
- `lesser-body-deploy.json`
- `lesser-body.zip`
- `deploy-lesser-body-from-release.sh`
- `lesser-body-managed-<stage>.template.json`

The managed runner verifies the checksum coverage for:

- `lesser-body-release.json`
- `lesser-body-deploy.json`
- `lesser-body.zip`
- `deploy-lesser-body-from-release.sh`
- `lesser-body-managed-<stage>.template.json`

## Required release-manifest fields

`lesser-body-release.json` must satisfy the managed compatibility contract and publish:

- `name = "lesser-body"`
- `version = <requested tag>`
- `git_sha = <non-empty commit sha>`
- `artifacts.checksums.path = "checksums.txt"`
- `artifacts.checksums.algorithm = "sha256"`
- `artifacts.deploy_manifest.path = "lesser-body-deploy.json"`
- `artifacts.deploy_manifest.schema = 1`
- `artifacts.lambda_zip.path = "lesser-body.zip"`
- `artifacts.deploy_script.path = "deploy-lesser-body-from-release.sh"`
- `artifacts.deploy_templates.<stage>.path = "lesser-body-managed-<stage>.template.json"`
- `artifacts.deploy_templates.<stage>.format = "cloudformation-json"`
- `deploy.schema = 1`
- `deploy.manifest_path = "lesser-body-deploy.json"`
- `deploy.template_selection = "by_stage"`
- `deploy.source_checkout_required = false`
- `deploy.npm_install_required = false`

The managed runner currently normalizes the requested managed stage to `dev`, `staging`, or `live` and expects template
metadata for the selected stage.

## Managed runner execution model

The managed `lesser-body` consumer path is `RUN_MODE=lesser-body`.

The runner:

1. downloads the published `lesser-body` release assets into a clean release directory
2. verifies the release manifest, deploy manifest path, stage template path, and checksum coverage
3. runs `deploy-lesser-body-from-release.sh --no-execute-changeset` against the managed instance account to certify the
   published stage template through the real CloudFormation consumer path
4. executes `deploy-lesser-body-from-release.sh` for the actual managed body deploy
5. reads the instance-scoped SSM export `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`
6. writes the managed receipt and uploads it back to the host artifacts bucket

The managed runner does not require a `lesser-body` source checkout or `npm install` in the happy path.

## lesser-body managed receipt contract

Managed body deploys upload the host-enriched receipt to:

- `managed/provisioning/<slug>/<jobId>/body-state.json`
- `managed/updates/<slug>/<jobId>/body-state.json`

The receipt preserves the native `lesser-body` deploy fields and adds host-side artifact provenance:

```json
{
  "managed_deploy_artifacts": {
    "mode": "release",
    "checksums_path": "checksums.txt",
    "release_manifest_path": "lesser-body-release.json",
    "release": {
      "name": "lesser-body",
      "version": "v0.2.3",
      "git_sha": "abc123",
      "source_checkout_required": false,
      "npm_install_required": false
    },
    "deploy_artifact": {
      "kind": "lesser_body_managed_deploy",
      "path": "lesser-body.zip",
      "manifest_path": "lesser-body-deploy.json",
      "script_path": "deploy-lesser-body-from-release.sh",
      "template_path": "lesser-body-managed-dev.template.json"
    }
  }
}
```

This provenance object is the canonical consumer-visible record of which verified `lesser-body` release assets were used
for the managed deploy.

## Follow-on MCP expectation

The `RUN_MODE=lesser-mcp` phase does not consume the `lesser-body` release assets directly. It consumes the deployed body
state indirectly through:

- the body receipt uploaded by `RUN_MODE=lesser-body`
- the instance-scoped SSM export `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`

That separation is intentional: the body deploy contract is release-asset driven, while the MCP follow-on contract is
receipt and export driven.
