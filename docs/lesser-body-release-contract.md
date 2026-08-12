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
- every required schema-2 `auxiliary_assets[].path` asset, when the deploy manifest declares auxiliary assets
  - for instance-plane enabled Body templates, the Body build artifact `dist/lesser-body-instance.zip` is represented as
    a required schema-2 auxiliary asset (or an equivalent AppTheory/CDK-managed auxiliary release asset) whose template
    reference points at the instance Lambda `InstanceMcpHandler04CF663E`

The managed runner verifies the checksum coverage for:

- `lesser-body-release.json`
- `lesser-body-deploy.json`
- `lesser-body.zip`
- `deploy-lesser-body-from-release.sh`
- `lesser-body-managed-<stage>.template.json`
- every declared schema-2 auxiliary asset path
  - this includes the instance-plane Lambda artifact when the selected managed template contains the instance plane

## Required release-manifest fields

`lesser-body-release.json` must satisfy the managed compatibility contract and publish:

- `name = "lesser-body"`
- `version = <requested tag>`
- `git_sha = <non-empty commit sha>`
- `artifacts.checksums.path = "checksums.txt"`
- `artifacts.checksums.algorithm = "sha256"`
- `artifacts.deploy_manifest.path = "lesser-body-deploy.json"`
- `artifacts.deploy_manifest.schema = 1` for legacy releases, or `2` for managed auxiliary assets
- `artifacts.lambda_zip.path = "lesser-body.zip"`
- `artifacts.deploy_script.path = "deploy-lesser-body-from-release.sh"`
- `artifacts.deploy_templates.<stage>.path = "lesser-body-managed-<stage>.template.json"`
- `artifacts.deploy_templates.<stage>.format = "cloudformation-json"`
- `deploy.schema = 1` for legacy releases, or `2` for managed auxiliary assets
- `deploy.manifest_path = "lesser-body-deploy.json"`
- `deploy.template_selection = "by_stage"`
- `deploy.source_checkout_required = false`
- `deploy.npm_install_required = false`
- for schema 2, `deploy.required_capabilities` must include `managed_auxiliary_assets_v1`

The managed runner currently normalizes the requested managed stage to `dev`, `staging`, or `live` and expects template
metadata for the selected stage.

## Managed runner execution model

The managed `lesser-body` consumer path is `RUN_MODE=lesser-body`.

The runner:

1. downloads the published `lesser-body` release assets into a clean release directory
2. verifies the release manifest, deploy manifest path, stage template path, required capabilities, and checksum coverage
3. for schema-2 releases, downloads every declared auxiliary asset, verifies its checksum and byte size, uploads it to the managed release artifact prefix, and validates that the selected template references only the primary Body code key or declared auxiliary code-key parameters
4. runs `deploy-lesser-body-from-release.sh --no-execute-changeset` against the managed instance account to certify the
   published stage template through the real CloudFormation consumer path
5. executes `deploy-lesser-body-from-release.sh` for the actual managed body deploy

Both helper invocations receive `LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN` set to the Host-ensured soul-binding
integration secret ARN — the same exact ARN passed to Lesser as `soul_binding_integration_key_arn` — so the Ptah
caller bearer and the Lesser receiver credential are always the one Host-managed secret. Release helper versions that
predate this variable simply ignore it. No operator step exists to create or match these secrets (see
`docs/managed-instance-provisioning.md#soul-binding-integration-secret-host-owned-automation`).

The runner then:
6. reads the instance-scoped SSM export `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`
7. writes the managed receipt and uploads it back to the host artifacts bucket

The managed runner does not require a `lesser-body` source checkout or `npm install` in the happy path.


## Schema 2 auxiliary assets

Schema-2 `lesser-body` deploy manifests are supported for AppTheory/CDK file assets that must be staged alongside the
primary Body Lambda zip. Schema 2 is additive: schema-1 / older single-asset Body releases remain supported.

Host supports exactly this required capability today:

- `managed_auxiliary_assets_v1`

For schema-2 releases, `lesser-body-deploy.json` must declare `required_capabilities: ["managed_auxiliary_assets_v1"]`
and every required auxiliary asset under `auxiliary_assets[]`. Host treats `auxiliary_assets[].s3_key` as
**prefix-relative**: the runner uploads the release asset to
`s3://<assetBucket>/<assetPrefix>/<s3_key>`, where Host's managed asset prefix is currently
`managed/lesser-body/<slug>/<stage>/<tag>`. Do not publish absolute S3 keys in `s3_key`.

Each auxiliary asset entry must include:

- `id`
- `path`
- `sha256`
- `bytes`
- `required`
- `s3_key`
- `template_parameter`
- `template_references[]`

Host fails closed before deploy if:

- a required capability is unsupported or omitted
- a declared auxiliary asset path is missing from `checksums.txt`
- an auxiliary asset download is missing, checksum-mismatched, or byte-size mismatched
- a required auxiliary asset lacks a reference for the selected stage template
- the selected template references an auxiliary Lambda code-key parameter that is not declared by `auxiliary_assets[]`
- any managed Body Lambda `Code.S3Bucket` / `Code.S3Key` uses a literal, `Fn::Sub`, CDK bootstrap bucket
  (`cdk-hnb659fds-*`), or any non-Host-managed bucket/key instead of `Ref: LesserBodyCodeBucketName` plus either
  `Ref: LesserBodyCodeObjectKey` or a declared auxiliary asset parameter
- an instance-plane template is partial: if any instance-plane resource is present, Host requires the instance Lambda
  `InstanceMcpHandler04CF663E`, the instance content/registry/grant/session tables, and the additive instance SSM export
  parameters to be present with their stable logical IDs
- the instance Lambda reuses the primary `lesser-body.zip` key; it must use a declared, required auxiliary asset parameter
  so `dist/lesser-body-instance.zip` (or the AppTheory/CDK-managed release asset derived from it) receives independent
  checksum verification and staging

`content_type` is optional. If present, Host preserves it on the S3 upload; Host does not require MIME metadata for
CloudFormation Lambda code assets.

## Instance-plane include / omit behavior

Host does not fork Body templates. It consumes the Body/AppTheory-produced managed templates exactly as release assets:

- When the Body managed template includes the instance plane, Host verifies the instance Lambda artifact through the
  schema-2 auxiliary-asset path above, uploads it under the managed release prefix, and passes the corresponding
  CloudFormation object-key parameter to the release deploy helper.
- When the selected Body managed template omits the instance plane, Host does not require those instance-plane resources
  or the instance Lambda auxiliary asset. This preserves the safe omit path for Lesser deployments whose
  `instancePlaneEnabled`/`BODY_ENABLED` path is false.
- Host's initial Lesser provisioning phase still sets `BODY_ENABLED=false`; the Body and MCP follow-on phases remain the
  point at which Body/instance-plane availability is introduced.

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
      "template_path": "lesser-body-managed-dev.template.json",
      "asset_prefix": "managed/lesser-body/simulacrum/dev/v0.2.3",
      "auxiliary_assets": []
    }
  }
}
```

This provenance object is the canonical consumer-visible record of which verified `lesser-body` release assets were used
for the managed deploy.


## Soul comm mailbox MCP contract

`lesser-body` does not own canonical mailbox state. For soul comm tools it is the MCP facade over host's
instance-authenticated mailbox contract:

- list/get tools call host mailbox metadata endpoints and return redacted previews/state. Host returns `messageRef` as the
  canonical opaque mailbox reference; body MCP parameters may remain named `messageId` for compatibility, but their value
  should be documented as a host mailbox message reference.
- full message bodies are fetched only by explicit content tools
- read/unread/archive/delete tools mutate host's canonical mailbox state
- reply tools call host's canonical mailbox reply endpoint so body does not reconstruct provider reply headers, thread
  roots, or recipients locally
- send tools must treat host-generated message identity as response-only. Host `POST /api/v1/soul/comm/send` does not
  accept caller-supplied outgoing `messageId`; its optional `inReplyTo` is a reply/conversation boundary reference that
  must match prior host/provider state for every recipient. Invalid refs fail closed as HTTP 403
  `comm.boundary_violation` with `error.details.field=inReplyTo`. Body should remove, rename, or prevalidate any
  `email_send.messageId`-style argument rather than forwarding it as Host `inReplyTo`; mailbox replies should use
  `email_reply` / Host's mailbox reply endpoint.
- when a caller acts under a lesser agent share grant, body send tools should pass the real caller's local lesser
  username as Host `actedBy` (`^[a-z0-9_-]{1,30}$`). Host treats `actedBy` as pure attribution — never authorization,
  never resolved against host-side identity — persists it with the send record, echoes it in the send and
  `GET /api/v1/soul/comm/status/{messageId}` responses, fails closed with 400 `comm.invalid_request` +
  `error.details.field=actedBy` on malformed values, and includes it in idempotency-key semantics (same key with a
  different `actedBy` is a `comm.idempotency_conflict`).
- mailbox list filters and bounded `query` are exact-agent host-side filters only; body must not implement global mailbox
  search or store durable query indexes
- mailbox list tools should use host-side `fields` projection (for example
  `messageRef,subject,preview,from,to,createdAt,state,content.available,threadId`) for compact MCP responses; `include_raw`
  defaults false and body should not re-add a duplicated raw upstream message object
- filtered mailbox `hasMore`/`nextCursor` describe matching results. If host returns `partialScan`/`scanHasMore`/
  `scanCursor`, that is explicit broad-scan metadata and must not be surfaced to MCP callers as matching-message
  pagination.
- body must not persist a durable mailbox-content or read-state store of its own

See `docs/soul-comm-mailbox-migration.md` for the migration order, backward-compatibility expectations, rate limits,
auth, audit, and lesser projection semantics.

## Follow-on MCP expectation

The `RUN_MODE=lesser-mcp` phase does not consume the `lesser-body` release assets directly. It consumes the deployed body
state indirectly through:

- the body receipt uploaded by `RUN_MODE=lesser-body`
- the instance-scoped SSM export `/${app}/${stage}/lesser-body/exports/v1/mcp_lambda_arn`

That separation is intentional: the body deploy contract is release-asset driven, while the MCP follow-on contract is
receipt and export driven.
