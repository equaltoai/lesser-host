# M7 — Instance Overview data prerequisites — Contract Note

Date: 2026-05-28
Branch: `aron/portal-m7-instance-overview-data`
Issue: [#540](https://github.com/equaltoai/lesser-host/issues/540)

## Audit result

The instance detail endpoint `GET /api/v1/portal/instances/{slug}` already returned
`Owner`, `LesserVersion`, `LesserBodyVersion`, `McpWiredAt`, `SoulEnabled`,
`BodyEnabled`, `ProvisionStatus`, `SoulProvisionedAt`, and `BodyProvisionedAt`.
However, owner identity was a bare username (wallet-derived), soul anchor
state/freshness were absent, and per-component drift flags were not in the
detail response (only in the separate `/stack` endpoint).

## Additive fields added to `instanceResponse`

### Owner enrichment (additive; `json:"owner_handle,omitempty"` etc.)

| Field               | Type   | Source                    | Empty semantics                  |
|---------------------|--------|---------------------------|----------------------------------|
| `owner_handle`      | string | `User.DisplayName`        | Falls back to `User.Username`    |
| `owner_role`        | string | `User.Role`               | Empty if User record not found   |
| `owner_avatar_hash` | string | (none)                    | Always empty — no avatar storage |

The lookup is best-effort (graceful fallback). If the instance owner's `User`
profile is not found, `owner_handle` and `owner_role` remain empty. No
account-level membership or cross-tenant identifiers are leaked.

### Soul anchor freshness (additive; derived from `Instance` model)

| Field               | Type   | Source                   | Empty semantics              |
|---------------------|--------|--------------------------|------------------------------|
| `soul_anchor_state` | string | `deriveSoulAnchorState`  | `""` if soul not enabled     |
|                     |        |                          | `"anchored"` if provisioned  |
| `soul_anchor_at`    | *time  | `Instance.SoulProvisionedAt` | `null` (absent from JSON) if not provisioned |

No additional DB query is required — these are derived entirely from the
existing Instance model fields (`SoulEnabled`, `SoulProvisionedAt`).

### Per-component drift (additive; computed from update jobs already loaded)

| Field               | Type   | Source                    | Empty semantics                    |
|---------------------|--------|---------------------------|------------------------------------|
| `lesser_drift`      | string | `computeDriftForKind`     | `"unknown"` if no successful job   |
| `lesser_body_drift` | string | `computeDriftForKind`     | `"unknown"` if no successful job   |
| `mcp_drift`         | string | `computeMCPDriftForDetail`| `"unknown"` if no successful job   |
| `drift_summary`     | string | `deriveDriftSummary`      | `"partial telemetry"` if no jobs   |

Drift values match the dedicated `/stack` endpoint contract:
- `"ok"` — current matches target
- `"stale"` — current is older than target
- `"wire-stale"` — MCP wired against a body version that differs from deployed
- `"unknown"` — no target / no telemetry yet

## Null/empty semantics

All new fields use `omitempty`. When the underlying data is absent:
- Strings default to `""` (empty string, omitted from JSON)
- Times use `*time.Time` pointers — `nil` when not provisioned, absent from JSON via `omitempty`
- `owner_avatar_hash` is intentionally always empty — no avatar storage exists

## Multi-tenant isolation

- Owner enrichment reads the `User` record by the instance's `Owner` username,
  which is the same identity that `requireInstanceAccess` already verified
  against the caller's `AuthIdentity`.
- No cross-instance, cross-owner, or account-membership data is exposed.

## Tests

Go tests cover:
- `TestDeriveSoulAnchorState` — pure function: nil, not-enabled, nil-enabled, enabled-but-empty, enabled-and-provisioned
- `TestHandlePortalGetInstance_OwnerEnrichment` — happy path with rich profile
- `TestHandlePortalGetInstance_OwnerEnrichmentFallbackToUsername` — empty DisplayName → username fallback
- `TestHandlePortalGetInstance_OwnerUserNotFoundGraceful` — User record not found → fields empty
- `TestHandlePortalGetInstance_CrossTenantIsolation` — Alice can't read Bob's instance
- `TestHandlePortalGetInstance_DriftFields` — successful update jobs → drift=ok
- `TestHandlePortalGetInstance_DriftWireStale` — MCP wired against old body
- `TestHandlePortalGetInstance_SoulAnchorFields` — soul disabled (nil pointer) vs. anchored
- `TestHandlePortalGetInstance_SoulAnchorFields_JSONAbsence` — `soul_anchor_at` absent from JSON when not provisioned (guards against Go `time.Time` zero-serialization)
- `TestInstanceResponseDTO_RedactionProof` — no PK/SK/TTL/GSI/account_id/secret leaks; `soul_anchor_at` present when anchored
- `TestEnrichDerivedDrift_NilResponse` — nil safety
- `TestEnrichDerivedDrift_EmptyJobs` — empty jobs → all unknown
- `TestComputeMCPDriftForDetail_*` — nil, non-OK edge cases
- `TestComputeDriftForKind_*` — nil, non-OK edge cases
- `TestEnrichOwnerIdentity_NilArgs` — nil safety

## API compatibility

All changes are additive. No existing field was renamed, removed, or re-typed.
The `instanceResponse` struct gains only `omitempty`-tagged fields. Existing
callers (including M6 UI) can ignore the new fields.

## Governance

- No SEC verifier changes required — no new sensitive boundaries.
- All existing tests pass; gov-infra rubric remains green.
