# Managed update recovery contract

This document defines the supported recovery path for failed **release-driven managed updates** in `lesser-host`.

It covers the current `POST /api/v1/portal/instances/{slug}/updates` and `GET /api/v1/portal/instances/{slug}/updates`
workflow for:

- Lesser updates
- `lesser-body` updates
- MCP-only rewiring updates

The goal is simple: operators and portal surfaces should recover managed updates by using the normal API contract, not by
editing DynamoDB rows by hand.

## Recovery invariants

- A failed `UpdateJob` remains durable history. It is not rewritten back to `queued` or `running`.
- A retry creates a **new job id**. The prior failed job remains available for audit and diagnosis.
- Instance markers (`updateStatus`, `lesserUpdateStatus`, `lesserBodyUpdateStatus`, `mcpUpdateStatus`) only block new
  work while they are `queued` or `running`.
- Terminal failures preserve operator-visible state:
  - `status=error`
  - `step=failed`
  - `note`
  - `error_code`
  - `error_message`
  - `failed_phase`
  - phase-specific status and error fields such as `deploy_status`, `body_status`, `mcp_status`, `deploy_error`,
    `body_error`, and `mcp_error`
- When a CodeBuild run exists, the job preserves the deep link fields that operators need for diagnosis:
  - `run_url`
  - `deploy_run_url`
  - `body_run_url`
  - `mcp_run_url`

## Where operators read recovery state

The canonical operator-facing surface is the update-job history endpoint:

- `GET /api/v1/portal/instances/{slug}/updates`

Each job response includes the fields needed to diagnose and safely retry:

- lifecycle: `status`, `step`, `note`, `active_phase`, `failed_phase`
- failure details: `error_code`, `error_message`
- deep links: `run_url`, `deploy_run_url`, `body_run_url`, `mcp_run_url`
- phase detail: `deploy_status`, `deploy_error`, `body_status`, `body_error`, `mcp_status`, `mcp_error`
- deploy intent: `lesser_version`, `lesser_body_version`, `body_only`, `mcp_only`, `rotate_instance_key`

Portal and operator UIs should surface those fields directly instead of inferring recovery state from raw DynamoDB
records.

## Body update failure classes

For `body_only` update jobs, portal and operator surfaces should treat `error_code` as the canonical classifier instead
of trying to infer failure type from free-form text.

- `body_release_preflight_failed`
  - Meaning: the requested `lesser-body` release was rejected before a CodeBuild runner started. Typical causes are
    missing release assets, checksum coverage gaps, or manifest-contract mismatches.
  - Expected evidence:
    - `failed_phase=body`
    - `body_status=failed`
    - `body_error` and `error_message` begin with `lesser-body release preflight failed:`
    - `run_id`, `run_url`, and `body_run_url` are usually empty because no runner was launched
  - Recovery: publish or select a release whose assets satisfy the managed consumer contract, then submit a fresh
    `POST /api/v1/portal/instances/{slug}/updates` request.

- `body_deploy_failed`
  - Meaning: the `RUN_MODE=lesser-body` CodeBuild runner started and reached a terminal failure state.
  - Expected evidence:
    - `failed_phase=body`
    - `body_status=failed`
    - `body_error` and `error_message` preserve the best available sanitized helper or CloudFormation failure detail;
      template-validation failures should remain visible instead of collapsing to only `exit status 1`
    - `body_run_url` and `run_url` preserve the CodeBuild deep link when one was observed
  - Recovery: inspect the preserved CodeBuild link, fix the release or configuration problem, and submit a new update
    job. No DynamoDB edits are required.

- `body_receipt_load_failed`
  - Meaning: the body phase reached receipt ingest, but `lesser-host` could not load the expected
    `managed/updates/<slug>/<jobId>/body-state.json` receipt after exhausting retries.
  - Expected evidence:
    - `failed_phase=body`
    - `body_status=failed`
    - `body_error` and `error_message` begin with `failed to load lesser-body receipt:`
    - `body_run_url` and `run_url` may still be present because the runner itself can have completed successfully before
      receipt ingestion failed
  - Recovery: restore the missing or malformed receipt condition, then submit a fresh retry through the normal portal
    update API.

These distinctions are intentional:

- preflight rejection means the release contract was blocked before AWS-side execution
- deploy failure means the runner itself failed and CodeBuild evidence should exist
- receipt-ingest failure means the runner may have succeeded, but durable deploy evidence could not be loaded afterward

## Canonical retry workflow

1. Inspect the most recent update job with `GET /api/v1/portal/instances/{slug}/updates`.
2. If the latest job is `queued` or `running`, do not create a second job.
3. If the latest job is `error`, fix the underlying release/config issue and submit a fresh update request with:
   - `POST /api/v1/portal/instances/{slug}/updates`
4. Treat the returned job as a new workflow instance with its own `id`, `request_id`, timestamps, and run links.

Retry safety rules:

- It is safe to retry after terminal release preflight failures such as release-manifest or asset-contract mismatches.
- It is safe to retry after terminal CodeBuild runner failures once the underlying Lesser, `lesser-body`, or
  configuration issue is corrected.
- It is safe to retry after a failed Lesser update, `lesser-body` update, or MCP-only update because each retry creates a
  new `UpdateJob` and does not mutate the previous failed history row.

## What happens to orphaned active state

`lesser-host` now repairs common “stuck running” situations without manual table edits:

- Listing update history nudges active jobs that have lost their queue wakeup.
- A scheduled update sweep reprocesses active jobs from the `gsi2` active-job index.
- If an instance marker still says `queued` or `running` but there is no matching active job, the control plane repairs
  that stale marker back to `error`.
- If a CodeBuild runner disappears before the worker records a terminal result, the update worker now reconciles that as a
  terminal error instead of leaving the job permanently in `deploy.wait`.
- The same sweep path reconciles terminal `body.deploy.wait` failures and preserves the best available CodeBuild deep
  link in `run_url` and `body_run_url`.

The supported recovery path is therefore:

- use `GET .../updates` to observe and nudge
- use `POST .../updates` to start a new retry once the prior job is terminal
- do not hand-edit DynamoDB job rows or instance markers

## Receipt contract during recovery

Managed update receipts remain at canonical S3 keys under the artifact bucket:

- `managed/updates/<slug>/<jobId>/state.json`
- `managed/updates/<slug>/<jobId>/body-state.json`
- `managed/updates/<slug>/<jobId>/mcp-state.json`

Receipt expectations:

- The Lesser receipt exists only if the Lesser phase reached receipt upload.
- The `lesser-body` and MCP receipts exist only if those optional phases were enabled and reached receipt upload.
- A terminal failure that happens before a phase writes its receipt will still preserve job-level failure state and deep
  links, even if the corresponding receipt file does not exist.

Operator tooling should surface deep links from the job response first, and treat receipt lookup as phase-dependent
follow-on evidence rather than assuming every failed job has a full receipt set.
