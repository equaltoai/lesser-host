# Issue #1061 part B — Bounded partition reads (no-Limit `.All()` elimination)

- Status: Implemented (PR pending review; no deploy/backfill — reads only)
- Date: 2026-08-27
- Linkage: Refs #1061 (part B; do not close), part A #1066 (merged `e87d854`),
  part C1 #1069 (merged `917fcf4a`), part C2 #1067 (merged)
- Precedents: `listSoulPublicItems`
  (`internal/controlplane/handlers_soul_public_list_helpers.go`) for
  endpoint pagination; the soul-recovery inventory scan
  (`internal/controlplane/handlers_soul_recovery.go`,
  `soulRecoveryMaxScanPages = 20`) for page-capped full-partition walks.

## Problem

ADR-0002 forbids table scans, and part A eliminated the full-table scans. The
remaining unbounded reads in request and event paths were `.All()` queries that
are key-bounded (a partition key and/or SK prefix) but carry **no `Limit`**, so a
single request could read an entire partition. The factory audit enumerated 21
such sites; this delegation eliminates or documents every one of them.

**No new indexes, no key-schema changes, no backfill tooling** — every fix is a
read-shape change inside the allowed files (the listed sites + their tests + docs).

## Disposition model

Each site was classified per the issue exit criteria and fixed as follows:

### (a) ENDPOINT can paginate → clamped Limit + opaque cursor

Response stays backward compatible: `limit` (default 50, max 200) and
`next_cursor` are additive/optional (`omitempty`); `count` reports the page
size. `cursor` is accepted when present. Mirrors `listSoulPublicItems`.

| Site | Handler | Notes |
|---|---|---|
| 2 | `handleListInstanceDomains` (operator, `handlers_domains.go`) | status-free list; `next_cursor` added to `listDomainsResponse` |
| 3 | `handlePortalListInstances` (`handlers_portal_instances.go`) | reuses `listInstancesResponse` (already carries `limit`/`next_cursor`) |
| 4 | `handlePortalListInstanceDomains` (`handlers_portal_instances.go`) | `listDomainsResponse` |
| 20 | `handleListSoulOperations` (`handlers_soul_operations.go`) | `status` param preserved; `limit`/`cursor` added |
| 21 | `handleListTipRegistryOperations` (`handlers_tip_registry.go`) | `status` param preserved; `limit`/`cursor` added |

Wire-contract changes: the five endpoints above accept `limit` (1..200,
default 50) and `cursor`; responses gain `limit` and `next_cursor`
(omitempty). First-party web consumers (portal sidebar/instances, soul
registry, tip registry) keep working: they read the first page and the new
fields are additive. This mirrors the part A precedent for
`GET /api/v1/instances` (no web changes shipped there either).

### (b) INTERNAL CALLER needs the full set → page-capped bounded walk

Every DynamoDB read in the walk is `Limit(pageSize)` + opaque `Cursor`
(`AllPaginated`); the loop resumes via the cursor between pages and fails
**closed** with an explicit error if the walk exceeds `maxPages` pages, so a
request/event can never issue an unbounded read and a full-set caller is never
silently truncated. Page size 100, max 20 pages (2,000 rows) per walk —
mirroring the soul-recovery page cap (20) with the C1 identity enumeration
page size (100).

| Site | Helper | Notes |
|---|---|---|
| 1 | trust `loadAttestationSubjectDomains` (`attestation_subjects.go`) | trust-api ownership check |
| 5 | `listSoulRosterCandidatesForDomains` (`handlers_portal_souls_roster.go`) | per-domain fan-out |
| 6 | `listOwnedInstances` (`handlers_soul_mine.go`) | `OWNER#` gsi1 |
| 7 | `listVerifiedDomainsForInstance` (`handlers_soul_mine.go`) | `INSTANCE_DOMAINS#` gsi1 |
| 8 | `listAgentIDsForDomains` (`handlers_soul_mine.go`) | per-domain fan-out |
| 9 | `getNextSoulAgentVersion` (`handlers_soul_update_registration.go`) | SK `VERSION#<n>` is not zero-padded, so a `Limit(1)` DESC read cannot find the max — the walk is the correct bounded shape |
| 11 | store `ListHostedGenesisSessionsByAgent` (`store/soul_mint_conversation.go`) | gsi2 agent-scoped list; store-level walk |
| 12 | store `ListHostedGenesisMicroVMExecutions` (`store/hosted_genesis_microvm_executions.go`) | PK-bounded walk; keeps the SessionID sort |
| 13 | `listSoulAgentBoundariesNoTruncation` (`handlers_soul_boundaries.go`) | registration publication needs the full boundary list |
| 14 | `findSoulFailureByID` (`handlers_soul_failures.go`) | failureId attribute lookup within the agent partition; read errors behave as before (treated as not-found) |
| 15-19 | `soulreputationworker/server.go` (`listAgentValidationRecords`, `listSoulRelationships`, `listSoulAgentFailures`, `countDeclaredBoundaries`, `addLegacyEndorsers`) | event-path per-identity fan-out; cap exhaustion fails the recompute phase explicitly |

### (c) Cross-entity lookup in disguise → flagged needs-index, (b) applied as interim

- **Site 14** `findSoulFailureByID`: the access pattern is
  `failureId`-attribute lookup within `SOUL#AGENT#<id>`; the SK embeds a
  variable-width RFC3339Nano timestamp (`FAILURE#<ts>#<failureId>`), so a
  deterministic point read is not possible today. A `FAILURE#<failureId>` SK
  prefix (or a failureId-indexed key) would make this a point read. **No index
  is built in this PR**; the bounded walk (b) is the interim.
- **Site 9** `getNextSoulAgentVersion`: the access pattern is "latest version"
  over `VERSION#<n>` where `n` is unpadded, so SK ordering cannot find the max
  in one read. A fixed-width version SK would enable a `Limit(1)` DESC read.
  Flagged for a future key-format change; bounded walk (b) is the interim.

### Dispositioned by an earlier part

- **Site 10** `handlers_soul_mint_conversation_instance_read.go`
  (`listSoulAgentMintConversationsWithoutPrivateDecode`) — the `limit` param
  was wired into the query by part A (#1066, commit `4f1841c`); no change
  needed here.

## Shared helpers

- `internal/controlplane/bounded_query.go` — `collectPartitionAll[T]`
  (+ `partitionWalkPageSize = 100`, `partitionWalkMaxPages = 20`)
- `internal/store/list_helpers.go` — `allPartitionItemsBounded[T]`
  (+ `storePartitionWalkPageSize`/`storePartitionWalkMaxPages`)
- `internal/trust/bounded_query.go` — `trustPartitionAll[T]`
- `internal/soulreputationworker/bounded_query.go` — `workerPartitionAll[T]`

All four implement the same page-capped walk and fail closed on cap
exhaustion; endpoint pagination is a single `Limit` + `Cursor` + `AllPaginated`
call per request (no loop), matching `listSoulPublicItems`.

## Failure shape (chosen + documented)

- Endpoint reads: a DynamoDB error maps to the existing `app.internal`
  response (unchanged behavior); no silent empty page.
- Bounded walks: exceeding `maxPages` returns an explicit error
  (`bounded partition walk exceeded N pages of M items each`) — handlers map
  it to `app.internal`, the store methods return it, and the reputation worker
  fails the recompute phase (fail closed, never a silently truncated score).

## Tests

Every touched site has a test proving (i) no `Scan` is issued
(`AssertNotCalled(t, "Scan", ...)`), (ii) the read is bounded (the outgoing
`Limit` value is asserted), and (iii) cursor round-trip (a `HasMore` page is
resumed via `Cursor(...)`; endpoint responses echo `next_cursor`). The shared
helpers additionally prove the page-cap fail-closed path. Existing tests that
stubbed `.All(...)` on these queries were updated to `.AllPaginated(...)` with
`Limit` stubs.

## Out of scope

- The four filter-scan-with-`Limit` sites (`handlers_operator_releases.go`,
  `handlers_operator_provisioning.go`, `handlers_operator_audit.go`,
  `internal/provisionworker/server.go`) are part D — untouched (their
  Limit-vs-pages semantics need a separate TableTheory investigation).
- `handlers_webauthn.go:84` (`listUserWebAuthnCredentials`) is a no-Limit
  `.All()` on `USER#<username>`/`WEBAUTHN_CRED#` that was **not** in the audit's
  21-site list; it is flagged here for a follow-up (per-user partitions are
  small, but the read is technically unbounded) — not changed in this PR to
  keep the diff to the audited surface.
- No cdk, no model key-schema changes, no backfill tool, no deploy.
