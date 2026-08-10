# Soul recovery inventory pagination investigation — 2026-08-09

## Reported symptom

> One Host issue remains: inventory pagination returns Della, then Iris, then fails with 409
> `soul_recovery.integrity_conflict`. This prevents authoritative enumeration of later agents.

Body's bounded reproduction adds that the error message is `agent is not active recovery state`: a default request
fails, while `limit=1` returns Della and Iris before the third page fails. Direct detail reads for Della, Iris, Mags,
Nico, Silas, and Theo all return `200` with the expected recovery classification.

## Dimensions

- Surface: Control plane Soul registry instance recovery API
- Lambda: `control-plane-api`
- Tenant context: authenticated TheoryLive Managed instance; Slug authority remains derived from its active
  `InstanceKey`. No raw key or key hash was read or recorded during this investigation.
- On-chain context: none; the affected path is an off-chain, read-only inventory.
- Release context: not a provisioning, Managed update, or Consumer release verification issue.
- Gov-infra context: no Verifier, Evidence, or Pack change is implicated.
- Recent deploys: the recovery API from PR #1031 was merged to `staging`, promoted, and reported reachable in `live`.

## Specialist elevation check

Soul registry, off-chain only. The corrective boundary is eligible-record selection for a tenant-scoped read. There is
no Solidity, on-chain-reaching Go, Safe-ready payload, Mint-signer, namespace, Trust API, CSP, or authentication change.

## What is definitely true

1. The tenant-owned domain index is ordered by local ID. Its first three entries are Della, Iris, and Juniper.
2. The third entry's identity is tenant/domain/local-ID bound but has both `status` and `lifecycleStatus` equal to
   `pending`, with `selfDescriptionVersion=0`. It is therefore not an eligible active recovery record.
3. The inventory implementation resolves every domain-index entry through the strict detail loader. That loader
   correctly returns `409 soul_recovery.integrity_conflict` for an explicitly requested inactive identity.
4. The inventory skips a missing identity but does not skip an ineligible inactive identity. Consequently a normal
   pending domain-index entry aborts the whole inventory page and strands later valid active records.
5. The domain-index binding itself is valid. This is not evidence of cross-tenant access, credential leakage, an auth
   bypass, or corrupt on-chain state.

## Fix-locus verdict

Fix here in Host. Inventory eligibility and detail-read integrity are distinct contracts: an inactive identity is not
a member of the active recovery inventory, while an explicit detail read for that same identity must continue to fail
closed with `409`. All other binding, declaration, promotion, artifact, and checksum conflicts encountered for an
active inventory candidate must still abort with the existing typed error.

## Hypotheses (ranked)

1. **Confirmed:** the inventory incorrectly treats an expected ineligible pending identity as a fatal integrity
   conflict because it reuses the strict detail loader without an eligibility filter. Evidence: the third ordered
   index entry is pending, and the returned message is emitted only by the detail loader's active-state check.
2. **Rejected:** pagination cursor corruption. Evidence against: the first two one-item pages advance in index order,
   and the third page reaches the expected third entry before returning the loader's lifecycle error.
3. **Rejected:** an active agent has broken recovery evidence. Evidence against: the failing entry is pending with no
   published self-description, while all six named active detail reads succeed.

## Verification step

Add a regression test with a cursor positioned after the two preceding active records, followed by a pending record and
a later eligible active record. Prove the inventory omits the pending record, continues bounded pagination to the later
active record, and still propagates integrity conflicts from an active candidate. Verify domain/local-ID/agent-ID index
bindings before applying the inactive eligibility filter. Retain an explicit-detail assertion that the pending identity
returns `409`.

## Proposed next skill

Implement the already-scoped pagination acceptance criterion as a small Host correction under
`implement-milestone`, with the off-chain Soul-registry audit above and no contract deployment or state repair.
