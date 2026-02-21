# Managed Soul integration test (M3.4)

Goal: verify a **new managed instance** provisions with `soul_enabled: true`, bootstraps agents, and can run a multi-subtask
`POST /soul/tasks` end-to-end.

This is a **runbook** (safe to follow manually). Do not run it from an automated agent unless explicitly approved.

## Prereqs

- `lesser-host` is deployed for the target stage (`lab` or `live`) with managed provisioning enabled.
- A signed Soul pack version is published for the target Soul stage (`lab` or `live`).
- The central-account SSM pointers exist:
  - `/soul/<stage>/packBucketName`
  - `/soul/<stage>/signingKeyArn`
  - `/soul/<stage>/packVersion`

## 1) Publish a signed Soul pack (central account)

From the `lesser-soul` repo (clean working tree), publish a new version:

```bash
AWS_PROFILE=Lesser ./scripts/soul-pack/publish-pack.sh lab  <version>
AWS_PROFILE=Lesser ./scripts/soul-pack/publish-pack.sh live <version>
```

Notes:
- Packs are immutable. Reuse of a `<version>` should fail.
- `publish-pack.sh` updates `/soul/<stage>/packVersion` to the new version.

## 2) Provision a managed instance with Soul enabled

Create a new instance via the operator UI or API:

- Set `soul_enabled: true`.
- Leave `body_enabled: true` (default) unless you explicitly want to skip MCP provisioning.
- Optional: set `soul_version: <version>` to pin a specific pack version for this instance (otherwise the runner defaults
  to `/soul/<stage>/packVersion`).

## 3) Monitor the provisioning job

In the operator console: **Provisioning Jobs → Job detail**.

Expected step sequence (high level):

- `deploy.*` + `receipt.ingest` (Lesser)
- `body.deploy.*` (lesser-body deploy)
- `deploy.mcp.*` (re-run Lesser stage deploy to attach `POST /mcp`)
- `soul.deploy.*` (Soul CDK deploy from signed pack)
- `soul.init.*` (Soul bootstrap from signed pack)
- `soul.receipt.ingest`

If pack verification fails, the job should fail closed during `soul.deploy.*` or `soul.init.*` (no deploy/bootstrap).

## 4) Verify receipts are present

In the provisioning job detail page:

- **Lesser receipt**: `receipt_json` (from `state.json`)
- **Soul receipt**: `soul_receipt_json` (from `soul-state.json`)

The Soul receipt should include, at minimum:
- deployed `soul_version`
- `soul_table_name`
- queue URLs
- agent usernames + SSM token paths

## 5) Verify agents exist + are verified

On the instance (GraphQL):

- Confirm agents exist:
  - `soul-researcher`
  - `soul-assistant`
  - `soul-curator`
  - `soul-coder`
  - `soul-summarizer`
- In `lab`, bootstrap should auto-verify (exit quarantine). In `live`, verification may be manual unless explicitly opted in.

## 6) Configure inference SSM params (instance account)

Soul runtime reads inference config from instance-account SSM:

- `/soul/<instance-domain>/inference/url` (String)
- `/soul/<instance-domain>/inference/key` (SecureString)

Set these for the new instance before running tasks.

## 7) Run a multi-subtask task

Call the orchestrator through the instance CloudFront distribution:

- `POST https://<instance-domain>/soul/tasks` with `{ "goal": "..." }`
- Poll: `GET https://<instance-domain>/soul/tasks/<id>`
- Optional: `GET https://<instance-domain>/soul/tasks/<id>/stream`

Expected:
- Task reaches `DONE`
- SubTasks include at least `RESEARCHER`, `CUSTOM:coder`, `CUSTOM:summarizer` (Phase 2 planner)

## Troubleshooting

- **Manifest verify failures:** confirm the correct stage (`lab` vs `live`), SSM pointers, and that the pack objects exist
  in the pack bucket. Ensure the CodeBuild runner role has `ssm:GetParameter`, `s3:GetObject`, and `kms:Verify`.
- **Agent quarantine:** in `live`, verify agents manually or run bootstrap with `--auto-verify-agents=true` (explicit opt-in).
