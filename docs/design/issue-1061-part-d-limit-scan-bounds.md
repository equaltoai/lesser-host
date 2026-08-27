# Issue #1061 part D — Limit-scan correctness + webauthn credential read

- Status: Implemented (PR to staging; no deploy — reads only)
- Date: 2026-08-27
- Linkage: Refs #1061 (part D; do not close), part A #1066 (merged),
  part B #1071 (merged), part C1 #1073 (re-land of #1069), part C2 #1075
  (re-land of #1070, issue #1067)
- Precedents: part B bounded walk `collectPartitionAll`
  (`internal/controlplane/bounded_query.go`, page 100 / max 20 pages = 2,000
  rows, fail closed) and `workerPartitionAll`
  (`internal/soulreputationworker/bounded_query.go`); the soul-recovery
  page-capped scan (`internal/controlplane/handlers_soul_recovery.go`,
  `soulRecoveryMaxScanPages = 20`); the part A bounded paginated scan
  (`handleListInstances`, `internal/controlplane/handlers_instances.go:453`).

## Problem

Part A eliminated the no-Limit full-table scans and part B eliminated the
no-Limit `.All()` partition reads. Five sites remained that carry a **`Limit` on
a DynamoDB `Scan`/`Query` but still paginate unboundedly**: DynamoDB `Limit`
caps the items *evaluated per page*, and TableTheory's `.All()` loops every page
via the cursor until the table/partition is exhausted — so "`Limit(200)` +
`.All()`" reads as many pages as the table has, with no upper bound on pages
read. ADR-0002 (`docs/adr/0002-canonical-identifiers-and-signatures.md:24,134-148`)
requires every read path to carry a page cap or a key/index; a `Limit` on a
`Scan` does not satisfy this.

Sites (exactly the five audited):

| # | Site | Shape |
|---|---|---|
| 1 | `handlers_operator_releases.go` `listActiveInstances` | filter-scan `SK=METADATA`, `Limit(500)` |
| 2 | `handlers_operator_provisioning.go` `handleListOperatorProvisionJobs` (no-slug path) | filter-scan `SK=JOB`, `Limit(200)` |
| 3 | `handlers_operator_audit.go` `listOperatorAuditLogEntries` (no-target path) | filter-scan `SK BEGINS_WITH EVENT#`, `Limit(200)` |
| 4 | `internal/provisionworker/server.go` `listProvisionSweepJobs` | filter-scan `SK=JOB`, `Limit(provisionSweepLimit=200)` |
| 5 | `handlers_webauthn.go` `listUserWebAuthnCredentials` | keyed `PK=USER#<username>` + `SK BEGINS_WITH WEBAUTHN_CRED#`, `.All()`, **no `Limit`** |

**No new indexes, no `UpdateTable`, no key-schema changes, no backfill** — every
fix is a read-shape change inside the five production files (plus tests).

## Disposition model

Every site is repaired with the wave's sanctioned bounded-walk shape: each read
is `Limit(pageSize)` resumed via the opaque cursor (`AllPaginated`); the loop
fails **closed** with an explicit error once it exceeds `maxPages` pages, so a
request/event can never issue an unbounded read and a caller is never silently
truncated.

### Site 1 — `listActiveInstances` (operator releases/drift/remediate)

- Repair: `collectPartitionAll[models.Instance](q, 100, 5)` — 5 pages of 100 =
  **500 evaluated items**, matching the site's documented `Limit(500)`.
- Ordering/answer set: the scan resumes in scan order; the same
  `SK=METADATA` filter applies per page, so the returned set is identical to
  the previous full scan for fleets ≤ 500. Beyond 500 the read fails closed
  (`app.internal`, "failed to list instances") — never a truncated fleet.
- Sentinel audit: `listActiveInstances` has three callers —
  `handleOperatorReleases` (`handlers_operator_releases.go:52`),
  `handleOperatorFleetDrift` (`handlers_operator_drift.go:23`),
  `handleOperatorRemediateMCPDrift` (`handlers_operator_remediate_mcp.go:47`).
  All three propagate the `*apptheory.AppTheoryError` immediately
  (`if appErr != nil { return nil, appErr }`); there is no
  swallow/log-and-continue that could hide the cap-exhaustion error. No
  sentinel split needed.

### Site 2 — `handleListOperatorProvisionJobs` (no-slug path)

- Repair: `collectPartitionAll[models.ProvisionJob](q, 100, 20)` — **2,000
  evaluated items** per request (the part B documented walk bound; the nominal
  `Limit(200)` is the per-page value, not a result cap). In-memory
  status filter + `UpdatedAt DESC` sort + `limit` truncation are unchanged.
- Answer set: identical to the previous behavior for tables ≤ 2,000 job rows;
  beyond that the handler fails closed (`app.internal`, "failed to list
  provisioning jobs").
- Sentinel audit: the error is returned straight out of
  `handleListOperatorProvisionJobs` (`if err != nil { return nil, ... }`); no
  swallow.
- Adjacent (out of scope, observed): the `instance_slug` path
  (`handlers_operator_provisioning.go:395-401`) is a **keyed gsi1 query**
  (`gsi1PK=PROVISION_INSTANCE#<slug>`) that still uses `Limit(200).All()` —
  per-partition reads bounded by the partition (per-slug job count) but not
  page-capped. Not part of the five-site audit; flagged as a follow-up
  candidate to convert to a bounded walk the same way.

### Site 3 — `listOperatorAuditLogEntries` (no-target path)

- Repair: `collectPartitionAll[models.AuditLogEntry](q, 100, 20)` — **2,000
  evaluated items** per request. The target-scoped keyed path
  (`PK=AUDIT#<target>`, `Limit(200).All()`) is **unchanged** (out of the
  five-site scope — it is a keyed query; see follow-ups).
- Answer set: identical for audit logs ≤ 2,000 `EVENT#` rows; beyond that the
  read fails closed (`app.internal`, "failed to list audit log") — the operator
  endpoint errors instead of silently omitting older entries.
- Sentinel audit: `handleListOperatorAuditLog` propagates the
  `*apptheory.AppTheoryError` (`if appErr != nil { return nil, appErr }`); no
  swallow.

### Site 4 — `listProvisionSweepJobs` (provision worker, event path)

- Repair: a provisionworker-local page-capped walk — `Limit(100)` per page,
  cursor resume, **`provisionSweepWalkMaxPages = 20` (2,000 evaluated items)**,
  fail closed with
  `"bounded provision sweep walk exceeded 20 pages of 100 items each"`.
  Mirrors `workerPartitionAll` (part B) so the event path never scans the
  table on a schedule. `provisionSweepLimit = 200` is removed (it capped items
  per page only and never bounded the sweep).
- Answer set: identical for tables ≤ 2,000 `SK=JOB` rows; beyond that the
  sweep run fails closed and the event handler returns the error (the sweep
  retries on the next scheduled run rather than silently processing a prefix).
- Sentinel audit: `processActiveProvisionSweep` returns `nil, err` on the list
  failure; `handleUpdateSweep` returns `(updateResult, provisionErr)` —
  the event path surfaces the error (Lambda event → retry), no
  log-and-continue swallow. The `sqs == nil` short-circuit (return a skipped
  result without reading) is unchanged.

### Site 5 — `listUserWebAuthnCredentials` (webauthn)

- Repair: **clamp + fail closed**, with a written structural bound (both
  dispositions recorded because the adversary standard demands each test kill):
  `collectPartitionAll[models.WebAuthnCredential](q, maxWebAuthnCredentials+1, 1)`
  — exactly **one page of `Limit(11)` evaluated items** (11 = the per-user
  maximum 10 + 1 sentinel). `Limit` is always applied; the read is one page
  with no cursor loop.
- **Written provably-small proof (structural bound):** credential creation is
  guarded in `completeWebAuthnRegistration`
  (`handlers_webauthn.go:248`: `len(creds) >= maxWebAuthnCredentials` → refuse),
  and the only writer of `WEBAUTHN_CRED#` rows is
  `handleWebAuthnRegisterFinish` (single request path, one credential per
  request). Deletion (`handleWebAuthnDeleteCredential`) only shrinks the
  partition. Therefore a user's `USER#<username>` / `WEBAUTHN_CRED#` partition
  holds **≤ 10 rows** in the steady state; a concurrent-registration race can
  transiently exceed 10, which the `Limit(11)` page detects via `HasMore` and
  fails closed on. A legitimate partition (≤ 10 rows) always terminates on the
  single page with `HasMore=false`; an 11th+ row fails the read instead of
  silently truncating the credential set.
- Callers: `handleWebAuthnRegisterBegin`, `handleWebAuthnRegisterFinish`,
  `handleWebAuthnLoginBegin`, `buildWebAuthnUser` (login finish), and
  `requirePrimaryAdminPasskey` (`handlers_setup.go:852`). All map the error to
  `app.internal` and return it; no swallow.

## Failure shape (chosen + documented)

- Bounded walks: exceeding `maxPages` returns the helper's explicit error
  (`bounded partition walk exceeded N pages of M items each`); handlers map it
  to `app.internal`, the provision sweep propagates it into the event handler
  (fail closed, never a silently truncated result).
- A DynamoDB error on any page maps to the same `app.internal` response as
  before (unchanged behavior).

## Tests

Per-site coverage (all in `internal/controlplane/*_bounded_internal_test.go`
and `internal/provisionworker/server_test.go`): empty table/partition, single
page, multi-page with cursor chain, exact page-size multiple (2 × 100), cap
exhaustion → error (not truncation), and the endpoint `limit` clamp boundaries
(absent / 0 / negative / over-max — `TestParseLimit_ClampBoundaries`, literal
pins). Every test pins literals (page size `100`, webauthn `11`, cursor strings
like `"releases-ct-1"` / `"prov-ct-1"` / `"audit-ct-1"` / `"sweep-ct-1"`), never
the constants under test; mock cursor sequences assert exact call counts with
`AssertExpectations`, and `AssertNotCalled(t, "Scan", ...)` forbids regression
to the unbounded full-table scan. Existing stubs across
`handlers_operator_endpoints_internal_test.go`,
`handlers_operator_provisioning_internal_test.go`,
`handlers_operator_audit_internal_test.go`, the three webauthn test files, the
setup tests, and `provisionworker/server_test.go` were migrated from `.All()` to
`Limit` + `AllPaginated` with `filterMockQueryCalls` (testify
first-registered-match-wins lesson from part B).

Mutation self-checks (one per site, all verified killed): reverting each site's
read to the original `.Limit(n).All()` shape fails that site's bounded test
(`AllPaginated` unexpected); removing/off-by-one-ing the page-cap comparison
fails the cap-exhaustion tests (a sixth/nineteenth call is unexpected, or
`AssertExpectations` reports unconsumed stubs).

## Out of scope / follow-ups

- The `instance_slug` gsi1 path in `handleListOperatorProvisionJobs` and the
  target-scoped `PK=AUDIT#<target>` path in `listOperatorAuditLogEntries` are
  keyed `Limit(n).All()` queries — **not** filter-scans, not in the five-site
  audit. They remain per-partition-bounded in reads but are not page-capped;
  flagged as follow-up candidates for the same bounded-walk conversion.
- No cdk, no model key-schema changes, no backfill tool, no deploy.
