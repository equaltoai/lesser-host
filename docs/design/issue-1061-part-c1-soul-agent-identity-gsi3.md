# Issue #1061 part C1 — SoulAgentIdentity gsi3 status enumeration index

- Status: Implemented (PR pending review; stack deploy + backfill are operator actions)
- Date: 2026-08-27
- Linkage: Refs #1061 (part C1; do not close), Refs #1067 (part C2, mint-conversation GSI — a SEPARATE PR and deploy)
- Part A: #1066 (merged `e87d854`) bounded the request-path reads and stopped at the two
  `SK=IDENTITY` enumeration sites because `SoulAgentIdentity` has no GSI and its PK is
  per-agent (`SOUL#AGENT#<id>`).

## Problem

Two request-adjacent/worker enumeration sites scan the whole state table by
`SK = IDENTITY`:

1. `internal/controlplane/handlers_soul_publish.go` — `listSoulActiveAgentIdentities`
   (soul reputation/validation root publish, operator-invoked).
2. `internal/soulreputationworker/server.go` — `listAgentIdentities`
   (reputation recompute).

`SoulAgentIdentity` items are keyed `PK=SOUL#AGENT#<agentId>`, `SK=IDENTITY`, so there
was no key-bounded way to enumerate them. ADR-0002 §8 forbids table scans; the operator
approved the sanctioned fix: a dedicated GSI.

## GSI slot audit (evidence)

The state table (`lesser-host-<stage>-state`, `cdk/lib/lesser-host-stack.ts:94-115`) has
2 of the default 20 GSIs per table, PAY_PER_REQUEST billing (no capacity planning
needed). The next free slot is **gsi3**.

- **gsi1** (`gsi1PK`/`gsi1SK`, projection ALL) already serves many models, including:
  `OWNER#` (instances, `handlers_soul_mine.go`), `INSTANCE_DOMAINS#` (domains), `SOUL_OP_STATUS#`
  (soul operations), `PROVISION_INSTANCE#` (provisioning), `TIPREG_OP_STATUS#` (tip registry ops),
  `USER_APPROVAL#` (operator approvals), `RENDER_EXPIRES#` (render artifacts), plus comm
  send idempotency and more. Overloading this slot with identity items would entangle a
  high-write model with unrelated partitions and pollute every existing gsi1 query.
- **gsi2** (`gsi2PK`/`gsi2SK`) serves `UPDATE_ACTIVE` (update jobs), comm-mailbox threads,
  and hosted-genesis sessions. Same argument: do not overload.
- **gsi3** is unused and becomes the identity status enumeration index.

DynamoDB creates **exactly one GSI per `UpdateTable`** — each GSI is its own stack deploy.
This PR adds exactly ONE GSI (gsi3). Part C2 (#1067) adds the mint-conversation GSI as
**gsi4 in a separate PR and separate deploy**.

## Key shape

```
gsi3PK = IDENTITY#<status>   (e.g. IDENTITY#active, IDENTITY#pending, ...)
gsi3SK = <agentId>           (lowercased hex, same canonical form as PK suffix)
```

- Status goes in the partition key so each status is its own partition:
  - the publish path (active-only) is a single bounded query on `IDENTITY#active`;
  - the reputation worker enumerates all seven statuses with bounded per-status
    queries driven by `models.SoulAgentIdentityStatuses()` (the model constant set, so a
    new status can never silently vanish from the enumeration);
  - items spread across multiple partitions instead of one hot `IDENTITY` partition.
- `gsi3SK = agentId` keeps the sort key stable and unique per status.
- Every status is indexed; identity lifecycle is status transitions, never item deletion.

## Model + write-path coverage

`SoulAgentIdentity` gains `GSI3PK`/`GSI3SK` (tags `index:gsi3,pk`/`index:gsi3,sk`),
maintained by `UpdateKeys()` → `updateGSI3()` (computed from the current `Status`).
TableTheory does **not** invoke the model hooks, so every write site computes keys
explicitly; the GSI attributes are written on **every** identity write:

- **Creates** (whole-item marshal): `ensureSoulPendingAgentIdentity` and
  `ensureSoulHostedInstanceTrustIdentity` (`handlers_soul_registry.go`) already call
  `UpdateKeys()` before `Create()`.
- **Updates** (`Update(fields...)` writes only the named fields, so the GSI fields are
  added to every field list):
  - `handlers_soul_suspension.go` (suspend, reinstate)
  - `handlers_soul_sovereignty.go` (self-suspend, self-reinstate)
  - `handlers_soul_operations.go` (mint receipt, wallet rotation side effect, burn side effect)
  - `soul_policy.go` (policy persistence — now carries the current status into the partial
    update model)
  - `handlers_soul_registry.go` (pending-principal reconciliation — carries current status)
  - `soul_registration_publish_v2.go` (registration activation — recomputes keys after the
    status transition)
  - `handlers_soul_update_registration.go` (capability update)
- **Deletes**: no code path hard-deletes a `SoulAgentIdentity` (lifecycle is status
  transitions through the audited update sites). If a hard delete is ever introduced,
  DynamoDB removes the item from all GSIs automatically; no tombstone is needed.

`updateGSI3()` preserves the existing gsi3 keys when `Status` is absent (partial update
models) instead of corrupting them with an empty status prefix — a belt-and-suspenders
guard on top of the per-site status propagation.

## Consumer changes (bounded GSI queries)

Both enumerations now go through `store.ListSoulAgentIdentitiesByStatus`:
`Index("gsi3")` + `Where("gsi3PK", "=", IDENTITY#<status>)` + `Limit(pageSize)` (default
100, max 200) + opaque `Cursor` loop via `AllPaginated`. Every DynamoDB read is a
key-bounded GSI query with a capped page; the full result set is assembled by looping.
No request-adjacent unbounded scan remains.

- Publish path: single status (`active`) — the previous client-side status filter is kept
  as a defensive no-op check.
- Reputation worker: loops `models.SoulAgentIdentityStatuses()` and concatenates.

## Graceful degradation (failure shape, chosen + documented)

The stack update that creates gsi3 deploys **before** the backfill completes. During that
window the consumers must fail explicitly, never silently return an empty/partial
identity set as if it were complete. Two failure shapes cover the window:

1. **Index absent** (deploy not yet run): the gsi3 query fails with a DynamoDB
   `ResourceNotFoundException`; both consumers propagate it (publish: HTTP 500
   `failed to list agents: ...`; worker: `list_identities` phase failure). Explicit.
2. **Index present but un-backfilled**: a gsi3 query would return zero/partial items with
   no error. To make this distinguishable from a legitimately empty status, the backfill
   tool writes a **completeness marker** (`PK=META#SOULAGENTIDENTITY#GSI3`,
   `SK=BACKFILL`, model `SoulAgentIdentityGSI3BackfillMarker`) ONLY after a complete
   apply pass with zero errors. Both consumers call
   `store.RequireSoulAgentIdentityGSI3BackfillComplete` first and fail closed with an
   explicit error naming the tool (`... backfill not complete: run
   scripts/soul-agent-identity-gsi3-backfill --apply ...`) if the marker is absent.

This is the "documented fallback" in the brief: the marker is the operator's proof of
completion AND the consumer gate. There is no silent-empty path.

## Operator backfill tool

`scripts/soul-agent-identity-gsi3-backfill/` — a first-class Go tool (flags, tests,
README) following the repo's backfill-tool conventions (`soul-backfill-m11-boundary-index`
precedent):

```
go run ./scripts/soul-agent-identity-gsi3-backfill --profile <aws-profile> --stage <lab|live> [--apply]
```

- `--profile` honored; `AWS_PROFILE` env fallback. Table resolves to
  `lesser-host-<stage>-state` (`--table` override available).
- **Dry-run by default**; mutations only under `--apply`.
- Bounded paginated key-only scans (`Limit` + `ProjectionExpression` + `ExclusiveStartKey`),
  base sleep + jitter between pages (`--sleep-ms`, never saturate).
- Conditional `UpdateItem` (`attribute_not_exists(gsi3PK) AND attribute_not_exists(gsi3SK)`)
  that only sets the two GSI attributes and never clobbers concurrent live writes
  (condition failure ⇒ a live write covered the item ⇒ counted already-correct).
- **Resumable**: persists a `LastEvaluatedKey` checkpoint (default
  `soul-agent-identity-gsi3-backfill.<stage>.checkpoint.json`); an interrupted run resumes
  with `--resume` instead of restarting.
- **Preflight**: `DescribeTable` — refuses unless gsi3 exists and is `ACTIVE`.
- Final count report (`scanned/updated/already_correct/errors/marker`) — the operator's
  proof. No credentials or table data in logs (agent IDs appear only in dry-run samples
  and error lines for remediation).
- Part C2 extends this same tool to `SoulAgentMintConversation` by adding a second
  `modelPlan`; the scan/checkpoint/throttle machinery is model-agnostic. Nothing for the
  second model is built in this PR.

## Deploy sequence (operator)

1. Deploy the stack update (this PR) — creates gsi3 (one GSI per deploy).
2. Run the backfill: `go run ./scripts/soul-agent-identity-gsi3-backfill --profile <p> --stage <s> --apply`
   (dry-run first; checkpoint/resume on interruption; marker written on a complete
   error-free pass).
3. The two enumeration sites answer from gsi3 with bounded reads; before step 2 they
   fail closed with the explicit backfill error.

## Non-goals (explicit)

- `SoulAgentMintConversation` GSI, its write paths, and the operator mint-conversation
  list route — part C2 (#1067), separate PR and separate deploy (gsi4).
- Part B (19 partition reads) — separate delegation.
- No deploy/merge/cloud mutation by this PR.
