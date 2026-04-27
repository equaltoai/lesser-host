# Roadmap: Soul Comm Mailbox v1 — host-authoritative MCP contract

**Status:** Approved by Aron for implementation after the AppTheory and TableTheory update integration work lands.  
**GitHub Project:** [equaltoai Project 23](https://github.com/orgs/equaltoai/projects/23)  
**Parent issue:** [equaltoai/lesser-host#160](https://github.com/equaltoai/lesser-host/issues/160)  
**Origin:** Body coordination request received by the host steward on 2026-04-25.

## Goal

Ship a bounded, host-authoritative mailbox contract for soul communications so `lesser-body` can expose MCP
communication tools without becoming mailbox authority. Host owns canonical delivery metadata, content, provider/thread
facts, idempotency facts, and mailbox state. Lesser receives notification projections for UX/activity. Body remains the
MCP facade that gates by profile/scope and calls host.

## AppTheory / TableTheory integration gate

This roadmap is intentionally queued **after** the AppTheory and TableTheory update integration work. Do not begin the
Soul Comm Mailbox implementation PRs until that integration is merged and locally verified, because this work introduces
new AppTheory handler surfaces and TableTheory-backed models/indexes.

Before opening the first implementation PR:

- Confirm the AppTheory/TableTheory update branch has merged to `main`.
- Rebase a fresh feature branch from `main`.
- Re-run the local verification baseline, including:
  - `go test ./...`
  - `cd cdk && npm run synth`
  - `bash gov-infra/verifiers/gov-verify-rubric.sh`
- Re-check AppTheory handler/auth idioms and TableTheory model/index idioms against the updated versions.
- If the updated frameworks make the mailbox model awkward, route that finding through `coordinate-framework-feedback`
  rather than patching around it locally.

### Framework incorporation decisions

After the 2026-04-25 framework refresh, host-v2 is using AppTheory `v1.1.0` and TableTheory `v1.7.0`. If a newer
AppTheory/TableTheory cut lands before implementation begins, repeat this review before opening Host 1. The current
incorporation decisions are:

**AppTheory:**

- Use AppTheory's current typed request binding / JSON response idioms for new mailbox HTTP handlers; keep explicit
  slug/agent ownership checks in handler code rather than relying only on coarse route scopes.
- Apply AppTheory request limits, handler timeouts, and log-sanitization helpers around content-bearing endpoints and
  audit events so list/content separation and PII redaction are enforced consistently.
- Use AppTheory job/concurrency controls where they fit mailbox retention, delivery capture, or projection fan-out; do
  not rewrite existing SQS comm-worker flows solely to adopt a new helper.
- Use AppTheory `v1.1.0` EventBridge/DynamoDB Stream workload normalization and observability for scheduled mailbox
  retention sweeps and any future stream/event-derived projection or audit pipeline.
- Do not use AppTheory MCP runtime features to make host an MCP server; `lesser-body` remains the MCP facade over
  host's contract.

**TableTheory:**

- Model bounded content and provider metadata with current TableTheory field idioms, including encrypted content fields or
  content pointers with explicit hashes, and structured JSON/provider metadata fields rather than ad hoc string blobs.
- Use TableTheory `v1.7.0` write policies for mailbox immutability boundaries:
  - immutable audit/event rows are write-once;
  - current-state rows protect identity/provenance/content identity attributes such as `deliveryId`, `messageId`,
    `threadId`, `instanceSlug`, `agentId`, provider identity fields, content hash/pointer, and creation provenance;
  - mutable mailbox state (`read`, `archived`, `deleted`/tombstoned) remains mutable by design and records an immutable
    event for each transition.
- Use transactional current-row update plus immutable event append where practical for read/unread/archive/delete and
  provider-status transitions.
- Add storage tests that prove protected/write-once mutations fail and allowed state transitions remain idempotent.
- Consider the new release-state helpers for managed-update/release-certification state in a later focused pass; do not
  force those helpers onto mailbox state if their release-state semantics make read/archive/delete awkward.

## Boundary decision

Aron approved host-authoritative bounded mailbox storage because comm delivery already belongs to host. Splitting mailbox
truth across host, lesser, and body would create a split-brain model:

- host knows provider, delivery, thread, status, idempotency, and billing facts;
- lesser holds notification approximations;
- body becomes tempted to glue state together or store mailbox truth locally.

The approved boundary is:

- **Host owns canonical comm objects:** `deliveryId`, `messageId`, `threadId`, provider status, inbound/outbound metadata,
  content, read/unread/archive/delete state, and audit state.
- **Lesser receives projections:** notification summaries for UX/activity; lesser is not canonical mailbox state.
- **Body exposes MCP tools:** `email_get`, `email_get_content`, `email_mark_read`, `email_mark_unread`, compatible list/read
  affordances, and future tools over host's contract.
- **Content is bounded:** retention policy, encryption, explicit access audit, no permanent semantic memory role.
- **List/content split:** list endpoints return redacted previews/metadata; full body is available only through explicit
  content/read calls.
- **Read state belongs with mailbox authority:** if host owns message identity and content, host owns read/unread/archive
  state too.

## Non-goals

- Body does not deliver email/SMS locally.
- Body does not store mailbox truth.
- Host does not become permanent semantic memory for comm content.
- No global cross-tenant mailbox search or analytics.
- No raw instance keys, full bodies, email addresses, phone numbers, JWTs, provider secrets, or `LESSER_HOST_INSTANCE_KEY`
  in logs.
- No CSP loosening.
- No on-chain contract changes.
- No bypass of host rate limits, safety controls, or audit.

## Required governance and safety posture

This is an explicitly approved, bounded exception to host's normal “metadata, not tenant content” posture. It must be
made visible in governance artifacts before implementation persists comm bodies.

Required posture:

- Update the ADR/docs before code stores content.
- Update `gov-infra/planning/lesser-host-threat-model.md` for bounded message-body storage.
- Update `gov-infra/planning/lesser-host-controls-matrix.md` and `gov-infra/planning/lesser-host-evidence-plan.md` for
  retention, encryption, access audit, PII redaction, and list/content split evidence.
- Add an additive deterministic verifier where practical, e.g. a SEC/CMP verifier that checks bounded comm mailbox
  controls and ensures list schemas do not expose full body fields.
- Keep instance-auth strict for sensitive mailbox endpoints: bearer token is hashed (`sha256(raw_key)`) and matched to the
  stored hash. Do not extend any legacy plaintext-key fallback to mailbox read/content/state endpoints.
- Treat content fetch and state mutations as audit-worthy events.

## Current-state findings

The current host contract does **not** satisfy the body request yet.

Existing surfaces:

- `POST /api/v1/soul/comm/send` — instance-key outbound send.
- `GET /api/v1/soul/comm/status/{messageId}` — instance-key outbound status.
- `GET /api/v1/soul/agents/{agentId}/comm/activity` — portal/session activity list.
- `GET /api/v1/soul/agents/{agentId}/comm/queue` — portal/session queued inbound list.
- `GET /api/v1/soul/agents/{agentId}/comm/status/{messageId}` — portal/session status.

Gaps:

- No authoritative mailbox list endpoint for body.
- No stable opaque cursor pagination for comm messages.
- No metadata/content split by `deliveryId`.
- No read/unread/archive/delete state model.
- No canonical host-owned content store for delivered inbound/outbound mailbox items.
- Queue only covers deferred inbound and currently returns full body in list payload.
- Portal comm views are not canonical mailbox state.

## Implementation bundles / intended PR grouping

The GitHub Project tracks detailed issues, but implementation should use PR-sized bundles to avoid one PR per issue.
Each issue should still remain small enough to identify acceptance criteria and blockers.

| Bundle | Intended PR scope | Issues |
| --- | --- | --- |
| Host 1 — Policy + governance | ADR, threat/control/evidence docs, gov-infra verifier. Lands before code stores comm bodies. | [#161](https://github.com/equaltoai/lesser-host/issues/161), [#162](https://github.com/equaltoai/lesser-host/issues/162) |
| Host 2 — Storage + capture | TableTheory models, encrypted/lifecycle-bound content storage, inbound and outbound canonical capture. Keep model, CDK, and worker changes as separate commits inside one PR. | [#163](https://github.com/equaltoai/lesser-host/issues/163), [#164](https://github.com/equaltoai/lesser-host/issues/164), [#165](https://github.com/equaltoai/lesser-host/issues/165), [#166](https://github.com/equaltoai/lesser-host/issues/166) |
| Host 3 — Mailbox APIs | Strict hash-only mailbox auth, list/get/content APIs, idempotent state mutations, bounded contactability resolver. | [#167](https://github.com/equaltoai/lesser-host/issues/167), [#168](https://github.com/equaltoai/lesser-host/issues/168), [#169](https://github.com/equaltoai/lesser-host/issues/169), [#171](https://github.com/equaltoai/lesser-host/issues/171) |
| Host 3.5 — Body-native mailbox contract | Stable `messageRef` semantics, exact-agent list filters including bounded metadata/preview `query`, and a canonical reply endpoint so body does not reconstruct mailbox truth. | Body blocker follow-up for [lesser-body#149](https://github.com/equaltoai/lesser-body/issues/149), [lesser-body#150](https://github.com/equaltoai/lesser-body/issues/150) |
| Host 3.6 — Mailbox hardening patch | Invalid cursor requests return bounded client errors, mailbox state mutations preserve canonical current-row keys and require row existence, host-generated self-send `Message-ID` references normalize into one thread, and lab-only ghost/split rows are repaired by an exact instance+agent cleanup utility. | Body lab probe follow-up |
| Host 4 — Portal + migration | Portal alignment and cross-repo migration docs after API semantics are stable. | [#170](https://github.com/equaltoai/lesser-host/issues/170), [#172](https://github.com/equaltoai/lesser-host/issues/172) |
| Body 1 — MCP tools + canary | Body implements MCP facade over host mailbox contract and validates against host lab. | [lesser-body#149](https://github.com/equaltoai/lesser-body/issues/149), [lesser-body#150](https://github.com/equaltoai/lesser-body/issues/150) |
| Lesser 1 — Projection compatibility | Lesser treats host comm notifications as non-authoritative projections and coordinates future summary-only projections. | [lesser#803](https://github.com/equaltoai/lesser/issues/803) |
| Sim 1 — E2E validation | Validate client → body → host mailbox flows end to end with redacted evidence. | [simulacrum#143](https://github.com/equaltoai/simulacrum/issues/143) |

## Host PR details

### Host 1 — Policy + governance

**Issues:** #161, #162  
**Planned commit subjects:**

- `docs(comm): define bounded host mailbox authority`
- `chore(gov): verify comm mailbox retention controls`

**Acceptance:** ADR and gov-infra planning docs make the approved host-content boundary explicit; a deterministic verifier
and evidence path prevent silent drift in retention/encryption/list-redaction controls. The policy docs also capture the
AppTheory/TableTheory incorporation decisions that are binding on the storage/API implementation.

**Validation:**

- `bash gov-infra/verifiers/gov-verify-rubric.sh`

### Host 2 — Storage + capture

**Issues:** #163, #164, #165, #166  
**Planned commit subjects:**

- `feat(comm): add soul mailbox models`
- `feat(cdk): add bounded comm content storage`
- `feat(comm): persist inbound mailbox deliveries`
- `feat(comm): persist outbound mailbox deliveries`

**Acceptance:** Inbound and outbound comms create canonical host mailbox objects with bounded encrypted content storage,
without breaking existing send/status and lesser notification flows. Mailbox audit/event rows are write-once, identity
and provenance attributes on current rows are protected, and tests prove forbidden mutations fail.

**Validation:**

- `gofmt -l .`
- `go test ./...`
- `cd cdk && npm run synth`
- `bash gov-infra/verifiers/gov-verify-rubric.sh`

### Host 3 — Mailbox APIs

**Issues:** #167, #168, #169, #171  
**Planned commit subjects:**

- `fix(comm): require hash-only mailbox instance auth`
- `feat(comm): expose mailbox read endpoints`
- `feat(comm): add mailbox read archive delete state`
- `feat(comm): expose soul contactability resolver`

**Acceptance:** Body can call host with strict instance-key auth to list redacted messages, get metadata, fetch content
explicitly, mutate read/archive/delete state idempotently, and resolve bounded contactability without global enumeration.
State mutations use transactional current-row updates plus immutable event append where practical.

**Validation:**

- `gofmt -l .`
- `go test ./...`
- contract/schema tests
- `bash gov-infra/verifiers/gov-verify-rubric.sh`

### Host 4 — Portal + migration

**Issues:** #170, #172  
**Planned commit subjects:**

- `feat(portal): read comm activity from mailbox state`
- `docs(comm): describe body mailbox migration path`

**Acceptance:** Portal comm views align with canonical mailbox state, and cross-repo docs describe the body/lesser migration
path, backward compatibility, rate limits, auth, audit, and projection semantics.

**Validation:**

- `gofmt -l .`
- `go test ./...`
- if `web/` changes: `cd web && npm run lint && npm run typecheck && npm test`
- `bash gov-infra/verifiers/gov-verify-rubric.sh`

### Host 3.5 — Body-native mailbox contract

**Trigger:** latest body steward blocker review after Host 4 merge.
**Planned commit subjects:**

- `docs(comm): define body-native mailbox contract`
- `feat(comm): filter mailbox list for body consumers`
- `feat(comm): expose mailbox messageRef semantics`
- `feat(comm): add canonical mailbox reply endpoint`
- `docs(comm): finalize body mailbox migration notes`

**Acceptance:** Host returns `messageRef` as the canonical opaque body-facing reference, backed by `deliveryId` in v1.
Mailbox get/content/state/reply endpoints accept `messageRef` and legacy `messageId` only when unambiguous within the
authenticated instance + exact agent. List APIs support exact-agent channel/direction/read/archive/delete/thread filters
and bounded metadata/preview `query`. Reply APIs derive recipient/thread/provider context from host canonical mailbox
state so body remains an MCP facade and not mailbox authority.

**Validation:**

- `gofmt -l .`
- `go test ./...`
- `go vet ./...`
- `cd web && npm run generate:lesser-host-api && npm run verify:lesser-host-contracts`
- `bash gov-infra/verifiers/gov-verify-rubric.sh`

## Cross-repo sequencing

1. Host 1 establishes the allowed content boundary.
2. Host 2 creates canonical storage/capture while preserving existing lesser/body behavior.
3. Host 3 exposes stable APIs for body.
4. Body 1 implements MCP tools against host lab APIs.
5. Lesser 1 keeps or adjusts notification projections without taking mailbox authority.
6. Host 4 aligns portal/migration docs once API behavior is stable.
7. Sim 1 validates the full client → body → host path.

## Stage rollout plan

### Lab

- Deploy: `theory app up --stage lab`
- Soak criteria:
  - inbound email creates canonical mailbox object and projection;
  - outbound send creates canonical mailbox object and remains backward compatible;
  - list endpoint returns redacted preview/metadata only;
  - content endpoint returns full content only under explicit strict-auth request;
  - read/unread/archive/delete are idempotent and append immutable transition events where practical;
  - invalid, revoked, plaintext-fallback, and cross-instance keys reject;
  - retention/encryption controls are visible in CDK/gov-infra evidence;
  - body MCP canary passes against lab.

### Live

- Deploy: `theory app up --stage live`
- Authorization: explicit operator authorization after lab soak.
- Monitoring:
  - control-plane 4xx/5xx;
  - comm-worker errors and SQS depth;
  - SES ingress health;
  - content fetch rates and auth failures;
  - write-once audit/event emission for content reads and state mutations;
  - storage lifecycle/retention evidence;
  - body MCP canary and Sim E2E canary results.

## Rollback posture

- API/worker regression: revert commit and redeploy host; preserve content bucket with lifecycle policies.
- CDK regression: revert infrastructure commit and redeploy; do not delete stateful buckets/tables manually.
- Data model issue: forward-fix with additive migration; avoid destructive deletes.
- Body/lesser migration issue: keep old notification projections and body existing tools until canaries pass.

## Open questions for implementation

- Exact content retention duration for stored message bodies.
- Whether delete means hard delete, tombstone + content purge, or archive-only for v1.
- Exact preview redaction shape for list responses.
- Whether SMS/voice content share the same content store schema or use channel-specific content records.
- Whether outbound email full body should be stored before or after provider accept, and how to represent provider rejection.
- Whether contactability resolver should expose only send eligibility or also display-safe contact labels.
