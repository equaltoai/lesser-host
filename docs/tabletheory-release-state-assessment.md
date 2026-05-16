# TableTheory release-state fit assessment

Date: 2026-05-16

This note evaluates whether TableTheory `pkg/releasestate` helpers from TableTheory `v1.8.3` should be adopted for
`lesser-host` provisioning and managed-update state.

## Executive decision

**Immediate replacement: non-fit.** Do not replace `ProvisionJob` or `UpdateJob` with TableTheory release-state helpers in
the current managed-instance pipeline.

**Future additive ledger: good fit with design work.** The helpers are a strong candidate for a future, additive
release-authority ledger that records verified managed release transitions after host's existing provisioning / managed
update workflow has passed its checksum-verification and receipt-ingest gates.

That means the safe next step is **no code change now**. Any future implementation should be scoped as a separate
provisioning / managed-update change, with schema design, migration plan, recovery behavior, and release-verification
coverage reviewed before it touches the worker path.

## What TableTheory provides

TableTheory's release-state package provides two main capabilities:

- `releasestate.TransitionAppendEvent` / `AddTransitionAppendEvent`
  - update one mutable actual-state row and append one immutable event row in the same DynamoDB transaction
  - optionally enforce an expected integer `version` and increment it during the transition
  - intentionally excludes external side effects such as Lambda alias flips, CodeBuild runs, or deploy runners
- `releasestate.ValidateDeployAuthorityMetadata`
  - validates provenance and confidence metadata before state is accepted as deploy-authoritative
  - rejects low-confidence or conflicting evidence
  - expects a constrained provenance/evidence vocabulary

The companion example models the pattern as:

- a mutable `ReleaseStateActual` row with protected deploy-authority fields
- immutable `ReleaseStateEvent` history rows
- optional outbox rows for side effects that happen outside the DynamoDB transaction

## Current host state model

### ProvisionJob

`internal/store/models/provision_job.go` is an operational workflow record for a one-time managed-instance provisioning
run. It tracks:

- queue/running/terminal status and current implementation step
- AWS account allocation details
- DNS delegation details
- managed lesser/body/soul provision receipts
- attempts and worker lease state
- terminal error code/message
- TTL-based retention

Provisioning is not only a release-state transition. It creates or adopts per-slug AWS account state, delegates DNS,
seeds tenant configuration, invokes CodeBuild, verifies release artifacts, ingests receipts, and updates instance
metadata. Replacing this record with release-state actual/event rows would obscure operational recovery fields that
operators need while a job is still in flight.

### UpdateJob

`internal/store/models/update_job.go` and `internal/provisionworker/update_jobs.go` model a managed-update state machine.
They track:

- a top-level job lifecycle (`queued`, `running`, `ok`, `error`)
- step-level execution (`instance.config`, `deploy.start`, `deploy.wait`, `receipt.ingest`, body/MCP variants,
  `verify`, `done`, `failed`)
- phase-specific status, run IDs, deep links, and error fields for Lesser, body, and MCP updates
- desired configuration snapshots and optional instance-key rotation
- verification signals for translation, trust, tips, and AI
- active-job indexes, processing leases, retry attempts, and stale-marker repair
- transactional updates to both the job and instance markers

This is broader than release-state registry authority. It is a recoverable workflow record and customer/operator-facing
progress surface.

## Fit analysis

### Where release-state fits well

A future additive ledger can strengthen host in places where the question is:

> What verified release/configuration is authoritative for this managed slug and component right now, and what immutable
> evidence got us there?

Potential future actual rows:

- `MANAGED_RELEASE#<slug>#lesser / ACTUAL`
- `MANAGED_RELEASE#<slug>#body / ACTUAL`
- `MANAGED_RELEASE#<slug>#mcp / ACTUAL`

Potential event rows:

- `EVENT#<timestamp>#<updateJobId>#verified`
- `EVENT#<timestamp>#<updateJobId>#activated`
- `EVENT#<timestamp>#<updateJobId>#rolled_back` if rollback support is later introduced

That ledger could be populated only after the existing worker has already completed the relevant gates:

1. release artifact manifest fetched
2. every deployable asset checksum-verified
3. CodeBuild runner reached the expected terminal state
4. receipt ingested from the artifact bucket
5. post-deploy verification completed where applicable

In this shape, release-state rows would be audit/reconciliation evidence, not a bypass around the existing workflow.

### Where release-state does not fit immediately

Release-state helpers are not a drop-in replacement for either current job model:

- `ProvisionJob` includes account/DNS/bootstrap fields that are not release authority.
- `UpdateJob` includes in-flight progress, runner diagnostics, phase errors, leases, retries, stale-marker repair, and
  customer-facing status fields.
- TableTheory release-state expects a dedicated actual/event model shape and integer `version`; host's current
  concurrency control is mostly `UpdatedAt` conditions and explicit instance marker conditions.
- The helper's transaction boundary covers DynamoDB state only. Host's risky operations are external: AWS Organizations,
  Route53 delegation, CodeBuild, S3 receipt ingest, and tenant-side deploys.
- Current UI/API contracts read `ProvisionJob` and `UpdateJob` directly; replacing them would require response-shape,
  recovery-runbook, and portal changes.

## Provisioning / managed-update / release-verification audit

### Proposed change

Docs-only feasibility assessment of TableTheory release-state helpers against host's provisioning and managed-update job
models.

### Pipeline surfaces affected

- Per-slug AWS account setup: **no behavior change**
- DNS delegation: **no behavior change**
- Consumer release verification: **no behavior change**
- CodeBuild runner invocation: **no behavior change**
- Managed-update flow: **no behavior change**
- Recovery docs / planning: **assessment only**

### Per-slug AWS account setup

No AWS Organizations, IAM, SSM, Secrets Manager, tenant account, or idempotency behavior changes.

### DNS delegation

No parent-zone, child-zone, ACM, propagation, or cleanup behavior changes.

### Consumer release verification

No release-manifest, asset-download, checksum-verification, mismatch-abort, release-certification, or readiness-script
behavior changes.

Any future release-state ledger must be downstream of checksum verification, never a substitute for it.

### CodeBuild runner

No buildspec, environment, credential, timeout, artifact, receipt, or failure-handling behavior changes.

### Managed-update flow

No state-machine, phase, rollback, progress, marker, lease, or retry behavior changes.

A future ledger must not replace the current `UpdateJob` recovery surface unless a separate migration preserves:

- phase-specific deep links
- error codes and messages
- failed phase
- active/stale-marker repair behavior
- new-job-on-retry invariant
- portal/operator response fields

### Tenant-isolation impact

- Preserves isolation: **yes**
- Cross-tenant reads/writes: **none**
- Shared credentials: **none**
- Tenant content ingestion into host: **none**

If implemented later, release-state actual rows must be keyed per slug/component and queried only through the existing
owner/operator authorization boundaries.

## Cost and risk accounting

### Benefits of future additive adoption

- immutable event history for managed release authority
- clearer separation between workflow progress (`UpdateJob`) and deploy-authoritative current state
- explicit provenance/confidence validation before an actual row becomes authoritative
- easier reconciliation of "what release is actually active?" after receipts and verification pass
- framework-standard write-once event policy rather than hand-rolled event history

### Costs of direct replacement

- schema migration for existing `ProvisionJob`, `UpdateJob`, and `Instance` marker semantics
- portal/API response changes or compatibility shims
- recovery-runbook rewrite
- worker-state-machine refactor around a helper that intentionally excludes external side effects
- new event/outbox models and retention policy
- revalidation of active-job indexes and retry/stale-marker behavior

### Risks if adopted prematurely

- treating release-state actual rows as proof of deployment before checksum verification or receipt ingest completes
- losing operator-visible CodeBuild links and phase-specific errors
- weakening the current new-job-on-retry recovery invariant
- creating cross-tenant aggregate release-state queries that bypass slug ownership checks
- conflating desired release, in-flight release, and verified active release

## Framework feedback to TableTheory

The helper is directionally useful, but host would likely need a few framework-level clarifications before code adoption:

1. **Evidence vocabulary extension.** Host's deploy authority evidence is checksum-verification and receipt based. The
   current examples cover operator commands, factory manifests, CodePipeline, and submodule pins. Host would need an
   idiomatic vocabulary for GitHub release manifests, SHA256 checksum verification, CodeBuild run receipts, and managed
   update job IDs.
2. **Side-effect/outbox recipe for Go.** The example notes that external side effects are outside the transaction.
   Managed updates need a first-class recipe for combining release-state transitions with CodeBuild/S3 receipt
   reconciliation.
3. **Version-field migration guidance.** Host currently uses timestamp/condition checks on job rows. A future actual-row
   model can use integer `version`, but migration and backfill guidance would reduce adoption risk.

## Recommended next steps

1. Keep current `ProvisionJob` and `UpdateJob` models as the authoritative workflow/progress/recovery records.
2. Do not adopt `releasestate` in the worker path in this milestone.
3. Route the evidence-vocabulary and Go side-effect/outbox needs as framework feedback.
4. If Aron wants code adoption later, scope a separate design for an additive `ManagedReleaseStateActual` /
   `ManagedReleaseStateEvent` ledger populated only after checksum verification, receipt ingest, and post-deploy
   verification succeed.

