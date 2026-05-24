# Provisioning audit — web/ UI rework on FaceTheory v3.3.0 — 2026-05-24

Output of the `provision-managed-instance` walk for the 2026-05-24 web/ UI rework scoped-need (`docs/scoped-need-web-ui-rework-2026-05-24.md`, branch `aron/web-ui-rework-planning`). Layered on the framework-feedback walk (commit `5579350`) and the trust-and-safety walk (commit `24dcd86`).

Walks four proposed changes the Claude Design handoff introduces:

1. MCP-wiring as a distinct provision-worker job kind (`wire-mcp`)
2. Two-channel release ingestion (lesser + lesser-body) with release-channel adoption ledger
3. Drift detection across the fleet, with one-click "Wire all" remediation
4. Customer-readable stack-state contract for Portal Instance Detail

The headline result: **three of the four "new" backend concepts already exist in host's code today**, with different naming. The actual rework is materially smaller than the design framed it as.

## Anchored current state (concrete evidence)

### Provisioning state machine

`internal/provisionworker/advance_body_mcp.go` already orchestrates a **distinct MCP-wiring deploy-runner step** as part of the initial provisioning flow:

- `provisionStepBodyDeployStart` → `provisionStepBodyDeployWait` → `provisionStepDeployMcpStart` → `provisionStepDeployMcpWait` → `provisionStepDeployMcpDone`
- Each step gets its own `RunID`, polling, retry, timeout (`provisionMaxDeployAge`)
- Distinct failure codes per step: `mcp_deploy_start_failed`, `mcp_deploy_status_failed`, `mcp_deploy_failed`, `mcp_deploy_timeout`
- Failure notes include CodeBuild deep link when available
- Mode string for the deploy runner is `"lesser-mcp"` (distinct from `"lesser-body"`)

`internal/store/models/provision_job.go:44` already tracks `McpWiredAt time.Time` as a first-class field on the provision job.

### Managed-update flow + job-kind expression

`internal/store/models/update_job.go`:

- `BodyOnly bool` (line 66) — update only the body component (skip lesser deploy and re-wire MCP after)
- `MCPOnly bool` (line 67) — update only the MCP wiring (skip both deploys)
- `LesserVersion` (line 61) and `LesserBodyVersion` (lines 64-65, with explicit fallback comment to `MANAGED_LESSER_BODY_DEFAULT_VERSION` or `releases/latest`) — **two independent release channels**
- Per-phase tracking: `DeployStatus`/`DeployRunID`/`DeployRunURL`/`DeployError` (lesser deploy), `BodyStatus`/`BodyRunID`/`BodyRunURL`/`BodyError` (body deploy), `MCPStatus`/`MCPRunID`/`MCPRunURL`/`MCPError` (MCP wiring)
- `ActivePhase` and `FailedPhase` for step-level recovery

`internal/controlplane/server.go:149`:

- `app.Post("/api/v1/portal/instances/{slug}/updates", s.handlePortalCreateInstanceUpdateJob, apptheory.RequireAuth())`
- Auth-gated customer-facing endpoint
- Accepts `createUpdateJobRequest{BodyOnly, MCPOnly, LesserVersion, LesserBodyVersion, RotateInstanceKey}` per the existing tests

`internal/controlplane/handlers_portal_updates_internal_test.go` confirms (with explicit test cases):

- `TestHandlePortalCreateInstanceUpdateJob_BodyOnlyDefaultsLesserBodyVersion` — BodyOnly defaults LesserBodyVersion from `MANAGED_LESSER_BODY_DEFAULT_VERSION`
- `TestHandlePortalCreateInstanceUpdateJob_BodyOnlyRejectsRotateInstanceKey` — instance-key rotation only valid for lesser updates
- `TestHandlePortalCreateInstanceUpdateJob_BodyOnlyRequiresBodyVersionWhenNoDefault` — fail-closed when no version source
- A test references `createUpdateJobRequest{MCPOnly: true}` at line 642 — MCP-only updates are a tested path

So the existing model already supports the design's four "job kinds":

| Design label | Existing model expression |
| --- | --- |
| `provision` | `ProvisionJob` (initial-creation workflow) |
| `update-lesser` | `UpdateJob{BodyOnly: false, MCPOnly: false}` |
| `update-body` | `UpdateJob{BodyOnly: true}` |
| `wire-mcp` | `UpdateJob{MCPOnly: true}` |

The "new" job-kind enum is a **UI-level derivation**, not a backend storage change.

### Two-channel release-compatibility contracts

`internal/provisionworker/release_compatibility.go`:

- `ManagedLesserCompatibilityContract` schema v1, minimum `v1.2.6`
- `ReleaseManifestAsset`, `ReleaseManifestSchema`, `MinimumReceiptSchema`, `DeployArtifactsSchema`, `LambdaBundle.{Path, ManifestPath, ManifestKind, ManifestSchemaVersion}`

`internal/provisionworker/release_compatibility_lesser_body.go`:

- `ManagedLesserBodyCompatibilityContract` schema v1, minimum `v0.2.3`
- Explicit `Checksums ManagedReleaseChecksumsContract` (with `Path` and `Algorithm` — SHA256)
- `LambdaZip`, `DeployScript`, `SupportedStages`, `DeployTemplates`, `AuxiliaryAssets`

`internal/provisionworker/release_preflight.go` + `release_preflight_lesser_body.go` are the corresponding preflight checks per channel. `scripts/managed-release-certification/main.go` is the certification Go CLI. `scripts/managed-release-readiness/` is the readiness checker.

**Two-channel checksum verification is fully implemented**. The rework does not introduce this — it surfaces what's already there.

### Wire-mcp lesser/body coordination — RESOLVED

`docs/managed-instance-provisioning.md:275`:

```sh
curl -sSfL -X POST "https://api.<stageDomain>/mcp/<actor>" \
  ...
```

The MCP-wiring deploy runner authenticates to the tenant's lesser instance and calls `POST /mcp/{actor}` to register body's MCP endpoints. The endpoint contract is **lesser-owned and stable**.

**Verdict: wire-mcp is entirely host-internal.** No lesser-side or body-side coordination is required for this rework. (If the `POST /mcp/{actor}` payload shape ever evolves, the lesser steward would be consulted as standard cross-repo coordination — but no change is implied by the rework.)

## Dimension 1: Per-slug AWS account setup

### Proposed change

**None directly.** The web/ UI rework does not modify per-slug AWS account creation, IAM roles, SSM seeding, or Secrets Manager seeding.

### Audit assertions

- `internal/provisionworker/account_create_branches_internal_test.go` and related account-creation paths remain unchanged across M0-M3.
- The cross-account assume-role trust policy hardening (PR #367, `deploy_runner_trust.go` with `sts:ExternalId = "lesser-host/deploy/<slug>"` per CSR-002) remains in place.
- No new IAM role, no new SSM parameter, no new Secrets Manager entry created or consumed by the UI rework on the host side. The data being displayed in the new UI exists in host's own DynamoDB already.

### Verdict

**No impact.** Tenant-isolation preserved by default; no change.

## Dimension 2: DNS delegation

### Proposed change

**None.** Parent zone `greater.website`, child zone `slug.greater.website`, ACM certificate flows, and NS delegation are unchanged.

### Verdict

**No impact.**

## Dimension 3: Consumer release verification (the supply-chain frontier)

### Proposed change

**Verification path is preserved unchanged.** The rework reads existing release-state telemetry (per-channel `ManagedLesserCompatibilityContract` and `ManagedLesserBodyCompatibilityContract` evaluation outcomes, latest applied versions per slug, and per-version fleet adoption counts) to populate new UI surfaces — release timeline cards, Stack Matrix, drift indicators. **No verification step is added, removed, or relaxed. No checksum is skipped or short-circuited.**

The new UI introduces a **release-channel adoption ledger** for two-channel adoption telemetry. The audit treats this as a **read-side aggregation** over existing state, not a new gate. Two implementation options:

- **Option A — live aggregation query**: aggregate per-instance `LesserVersion` + `LesserBodyVersion` + `McpWiredAt` from existing `Instance`, `ProvisionJob`, `UpdateJob` records via DynamoDB GSI queries. No new write path; no new ledger model. Recommended.
- **Option B — materialized ledger**: a new `ReleaseChannelAdoption` aggregate written by the provision-worker on each successful deploy step. Persistent ledger; faster reads; introduces an additional write path.

Option A is preferred for M2: it avoids a new write path on the supply-chain-critical worker, and the read-volume is operator-scale (small). Option B can replace it later as a perf optimization without changing the UI contract.

### Audit assertions for CI

1. The two release-compatibility contracts (`release_compatibility.go` lines 14-15 `minimumSupportedManagedLesserReleaseVersion = "v1.2.6"` and `release_compatibility_lesser_body.go` lines 13-14 `minimumSupportedManagedLesserBodyReleaseVersion = "v0.2.3"`) are not weakened during M0-M3 except via a separate scope-need.
2. The certification scripts (`scripts/managed-release-certification/main.go`) are not modified during M0-M3 except via a separate scope-need.
3. The readiness scripts (`scripts/managed-release-readiness/*`) are not modified during M0-M3 except via a separate scope-need.
4. A test in `internal/provisionworker/release_preflight_internal_test.go` and `release_preflight_lesser_body` continues to pass with: known-good manifest succeeds; known-bad checksum aborts with no deploy invoked.
5. The release-adoption ledger query (Option A) is read-only and scoped per-tenant in the customer plane (`/api/v1/portal/...`) and operator-scoped in the operator plane (`/api/v1/operators/...`).

### Refusal cases triggered

- Any proposal to fetch lesser/body releases and skip the certification call → **refuse**.
- Any proposal to allow operators to override checksum mismatch and proceed → **refuse**.
- Any proposal to display a release as "ready" in the Stack Matrix without running through the preflight → **refuse**; only certified releases appear in the timeline.

## Dimension 4: CodeBuild runner invocation

### Proposed change

**None.** The wire-mcp CodeBuild runner already exists (mode `"lesser-mcp"`); its build spec, environment, credentials, resource limits, and failure handling are unchanged. The `wire-mcp` UI label resolves to invoking the existing runner for an `UpdateJob{MCPOnly: true}`.

### Audit assertions

- `cdk/lib/provision-runner-buildspec.ts` (per memory: 2026-05-17 fix for JS String.replace shell-literal corruption) and the rendered build spec are not modified by this rework.
- The `provisionStepDeployMcpStart` / `provisionStepDeployMcpWait` orchestration in `advance_body_mcp.go` is unchanged.
- Wire-mcp jobs initiated from the operator "Wire all" CTA produce one `UpdateJob{MCPOnly: true}` per affected slug; the existing runner contract is reused unchanged.

### Verdict

**No CodeBuild runner change.**

## Dimension 5: Managed-update flow and step-level recovery

### Proposed change

Three additive endpoints + one UI-level derivation. **No change to the existing managed-update state machine or recovery paths.**

#### Change 5.1 — Customer-readable stack-state contract (Portal Instance Detail)

A new read-only endpoint, e.g.:

```
GET /api/v1/portal/instances/{slug}/stack
```

Returns:

```json
{
  "slug": "press-room",
  "lesser": {
    "current_version": "v1.4.2",
    "deployed_at": "2026-05-17T14:14:04Z",
    "source_job_id": "..."
  },
  "body": {
    "enabled": true,
    "current_version": "v0.2.6",
    "deployed_at": "2026-05-22T10:00:00Z",
    "source_job_id": "..."
  },
  "mcp": {
    "wired_at": "2026-05-22T10:01:30Z",
    "wired_against_body_version": "v0.2.6",
    "drift": "ok"
  },
  "drift_summary": "ok"
}
```

Data sources:
- `lesser.current_version` = latest successful `UpdateJob.LesserVersion` for the slug, or `ProvisionJob.LesserVersion` if no successful update yet
- `body.current_version` = latest successful `UpdateJob.LesserBodyVersion` for the slug (with `BodyOnly` or full update), or `ProvisionJob.BodyEnabled` + initial body version if no update yet
- `mcp.wired_at` = latest `MCPRunID` success timestamp on `UpdateJob`, or `ProvisionJob.McpWiredAt`
- `mcp.wired_against_body_version` = the `LesserBodyVersion` of the `UpdateJob` whose MCP step succeeded
- `drift` per component: `"ok"` if `current == target`, `"stale"` if `current < target`, `"unknown"` if no target set

Tenant scoping: **strict per-slug ownership check** before any read. Same auth model as today's `/api/v1/portal/instances/{slug}/*` endpoints.

#### Change 5.2 — Release-channel adoption ledger (operator-side)

A new read-only operator endpoint:

```
GET /api/v1/operators/releases
```

Returns per-channel:

```json
{
  "channels": [
    {
      "id": "lesser",
      "versions": [
        {
          "version": "v1.4.2",
          "released_at": "2026-05-15T...",
          "is_latest": true,
          "is_breaking": false,
          "adoption": {
            "instances": 7,
            "of": 12,
            "percent": 58
          }
        },
        ...
      ]
    },
    {
      "id": "lesser-body",
      "versions": [...]
    }
  ],
  "fleet_total": 12
}
```

Auth: operator JWT. Aggregation: read-only over `UpdateJob` + `ProvisionJob` + `Instance`; never reads tenant-side data.

#### Change 5.3 — Fleet drift state (operator-side)

A new read-only operator endpoint:

```
GET /api/v1/operators/instances/drift
```

Returns per-instance:

```json
{
  "instances": [
    {
      "slug": "press-room",
      "lesser": {"current": "v1.4.1", "target": "v1.4.2", "drift": "stale"},
      "body": {"current": "v0.2.5", "target": "v0.2.6", "drift": "stale"},
      "mcp": {"wired_against": "v0.2.5", "current_body": "v0.2.5", "drift": "ok"}
    },
    {
      "slug": "equaltoai",
      "lesser": {"current": "v1.4.2", "target": "v1.4.2", "drift": "ok"},
      "body": {"current": "v0.2.6", "target": "v0.2.6", "drift": "ok"},
      "mcp": {"wired_against": "v0.2.5", "current_body": "v0.2.6", "drift": "wire-stale"}
    }
  ],
  "summary": {
    "total": 12,
    "lesser_stale": 3,
    "body_stale": 2,
    "mcp_wire_stale": 4
  }
}
```

Auth: operator JWT. Target version: `MANAGED_LESSER_DEFAULT_VERSION` and `MANAGED_LESSER_BODY_DEFAULT_VERSION` from config, unless an operator-set fleet target overrides (deferred; out of scope for M2 unless explicitly added).

The `mcp.drift = "wire-stale"` case fires when MCP was last wired against a body version older than the currently-deployed body version. This catches the design's `press-room` scenario (body updated, MCP not yet re-wired).

#### Change 5.4 — One-click "Wire all" fleet remediation

A new operator endpoint:

```
POST /api/v1/operators/instances/remediate-mcp-drift
```

Returns the list of `UpdateJob` IDs created (one per affected slug). Implementation:

1. Read `GET /api/v1/operators/instances/drift` internally.
2. For each instance with `mcp.drift == "wire-stale"`, create an `UpdateJob{InstanceSlug: slug, MCPOnly: true}`.
3. Idempotency: if an active MCP-only UpdateJob already exists for a slug (`GSI2 = UPDATE_ACTIVE`), skip it.
4. Audit: emit one operator-action event per remediation triggered, with slug list.

Tenant boundary: each remediation triggers a **per-slug** `UpdateJob`. No cross-tenant data in the operator-side request body. The aggregated response carries job IDs only.

#### Change 5.5 — UI-derived "kind" label

Both the Portal and Operator UI surface one of four labels for any provisioning/update workload row:

| Backend state | UI label |
| --- | --- |
| `ProvisionJob` (status running or terminal) | `provision` |
| `UpdateJob{BodyOnly: false, MCPOnly: false}` | `update-lesser` |
| `UpdateJob{BodyOnly: true}` | `update-body` |
| `UpdateJob{MCPOnly: true}` | `wire-mcp` |

No backend change; pure UI derivation. The Operator's `/operator/provisioning` list and Portal's instance detail "Recent activity" surface use the same derivation rule.

### Step-level recovery

**Preserved unchanged.** The existing recovery primitives in `internal/provisionworker/state_machine_*` + per-step retry/backoff/timeout/fail-job logic + recovery docs (`docs/managed-update-recovery.md`, `docs/provisioning-recovery-plan.md`, `docs/recovery.md`) remain authoritative.

### Idempotency

- The new GET endpoints are read-only; idempotent by definition.
- The "Wire all" POST endpoint enforces idempotency via the existing `GSI2 = UPDATE_ACTIVE` query (defined on `UpdateJob.updateGSI2()` in `update_job.go:229-247`).
- No proposal to change the existing single-instance update idempotency.

### Verdict

**Audit clean.** Five additive surfaces, all read-only or operator-aggregated, all preserving existing recovery semantics and idempotency.

## Dimension 6: Tenant-isolation preservation

### Customer-plane (Portal) surfaces

- `GET /api/v1/portal/instances/{slug}/stack` — auth-gated; ownership check on `{slug}` before read; returns only that slug's state. No cross-tenant data leakage.
- Portal Instance Detail rendering uses only data scoped to the authenticated customer's instance.

### Operator-plane (Operator Console) surfaces

- `GET /api/v1/operators/releases` — operator JWT required; aggregates across the fleet; no per-instance ownership disclosure beyond what the operator already has access to (they administer all managed instances).
- `GET /api/v1/operators/instances/drift` — operator JWT required; lists slug + version state across the fleet; operator-only.
- `POST /api/v1/operators/instances/remediate-mcp-drift` — operator JWT required; emits one UpdateJob per affected slug; audit logs the operator-action with slug list.

### Cross-tenant boundary assertions

- **No customer-side endpoint reads another customer's data.** Confirmed by tenant-ownership check at handler entry.
- **No tenant content (user data, messages, posts) leaks into host's plane.** Stack state is metadata only: version strings, timestamps, status enums.
- **No shared secret across tenants.** No change to credential handling.
- **No new tenant-side data fetched by host.** Lesser version is what host deployed; host knows it from its own provisioning state. No reach-back into the tenant's lesser instance for version info.
- **Audit logging per operator action.** `POST /api/v1/operators/instances/remediate-mcp-drift` emits a structured audit event with operator identity, timestamp, and the list of slugs affected.

### Verdict

**Isolation preserved absolutely.**

## Consumer impact

- **Existing managed instances**: no migration. The new endpoints are additive; existing endpoints are unchanged.
- **Future provisioning**: no change. New provisioning continues to flow through `ProvisionJob`.
- **Operator portal UX**: gains release timeline, Stack Matrix, drift indicator, "Wire all" CTA.
- **Customer portal UX**: gains Stack card on Instance Detail.
- **Sibling repos**:
  - `lesser`: no change. `POST /mcp/{actor}` contract is stable and lesser-owned; host calls it via the existing wire-mcp runner.
  - `body`: no change. Body deploys through the existing two-channel ingestion.
  - `soul`: no impact.
  - `greater-components`: components requested per the framework-feedback walk (Signal D). Drift / release-timeline / Stack Matrix may be Greater additive requests, or host-bespoke if declined.
  - `sim`: existing instance — surfaced in operator views like any other slug; no contract change.

## Test coverage

### Existing (preserved)

- `internal/provisionworker/release_preflight_internal_test.go` — checksum verification path.
- `internal/provisionworker/release_compatibility_internal_test.go`, `release_compatibility_lesser_body_internal_test.go` — contract schema verification.
- `internal/provisionworker/state_machine_*_test.go` — state machine transitions.
- `internal/controlplane/handlers_portal_updates_internal_test.go` — managed-update endpoint contract.

### Added by the rework (deferred to enumerate-changes)

- Unit test for stack-state aggregation logic (Lesser/Body/MCP component sourcing rules per slug).
- Unit test for drift computation (per-component drift state, including `wire-stale` for MCP).
- Unit test for "Wire all" endpoint idempotency (active MCP-only job present → skip).
- Integration test asserting `POST /api/v1/operators/instances/remediate-mcp-drift` emits one `UpdateJob{MCPOnly: true}` per affected slug, no cross-tenant data.
- Integration test asserting `GET /api/v1/portal/instances/{slug}/stack` fails closed (401/403) for a customer requesting another slug.

## Governance-rubric impact

This walk's additive verifier asks (consolidated in the `maintain-governance-rubric` walk):

6. **PROV-WIRE-MCP-ROUTE-OWNERSHIP**: assert host's MCP wiring runner targets only the canonical lesser-owned `POST /mcp/{actor}` route, not any new tenant-side endpoint. Catches drift if a future change pulls the contract host-side.
7. **PROV-RELEASE-VERIFICATION-PRESERVATION**: assert the two-channel checksum verification paths (`release_preflight*.go` + `scripts/managed-release-certification/main.go`) are not modified across M0-M3 without an explicit governance event.
8. **PROV-STACK-STATE-TENANT-SCOPING**: assert the new `GET /api/v1/portal/instances/{slug}/stack` handler has an explicit ownership check before any DB read; verifier reads handler code.
9. **PROV-OPERATOR-DRIFT-AUTH-GATE**: assert the new `GET /api/v1/operators/instances/drift`, `GET /api/v1/operators/releases`, and `POST /api/v1/operators/instances/remediate-mcp-drift` handlers require operator JWT; verifier reads handler code.
10. **PROV-WIRE-ALL-IDEMPOTENCY**: assert "Wire all" endpoint skips slugs with an active MCP-only `UpdateJob` (GSI2 = UPDATE_ACTIVE); verifier reads handler test fixtures.

## On-chain impact

**None.** No soul-registry, TipSplitter, or on-chain contract is touched. Soul provisioning is unchanged (the `ProvisionJob.SoulEnabled` / `SoulProvisionedAt` fields are read-only for the Stack card).

## AGPL posture

Unchanged. All new endpoints + UI surfaces are host-internal under AGPL-3.0.

## Open question resolution

- **"Does wire-mcp need lesser-side or body-side coordination?"** **No.** Wire-mcp is entirely host-internal. Lesser owns the `POST /mcp/{actor}` contract (per `docs/managed-instance-provisioning.md:275`); host's existing wire-mcp deploy runner already exercises it (per `advance_body_mcp.go:137-151` mode `"lesser-mcp"`). The design's "wire-mcp as first-class job kind" is achieved by surfacing the existing `UpdateJob{MCPOnly: true}` as a labeled UI kind plus adding the operator-side fleet aggregation surfaces.

## Cross-walk interactions

- **Trust-and-safety walk**: the new operator endpoints (`/api/v1/operators/releases`, `.../drift`, `.../remediate-mcp-drift`) live behind the existing operator-JWT auth; no instance-auth / sha256(raw_key) contract change. Confirmed CSP-compatible (no new origins, no inline content). The new customer endpoint (`/api/v1/portal/instances/{slug}/stack`) uses the same wallet/session auth as today's portal endpoints. **Compatible — no trust-and-safety follow-up needed.**
- **Framework-feedback walk Signal D**: the release timeline, Stack Matrix, and drift-indicator UI components are on the additive greater-components request list. Resolution depends on Greater steward triage.
- **Governance-rubric walk (next)**: consolidates the additive verifier asks above (PROV-WIRE-MCP-ROUTE-OWNERSHIP, PROV-RELEASE-VERIFICATION-PRESERVATION, PROV-STACK-STATE-TENANT-SCOPING, PROV-OPERATOR-DRIFT-AUTH-GATE, PROV-WIRE-ALL-IDEMPOTENCY) plus the trust-and-safety walk's five verifier asks.

## Proposed next skill

The audit is **clean for the provisioning dimensions**. Three of the four design-proposed "new" backend concepts (wire-mcp job kind, two-channel release ingestion, MCP-step orchestration) already exist; the actual new work is additive read/aggregation endpoints + one operator-side write endpoint + UI-derived labeling.

Handoff:

- **`maintain-governance-rubric`** runs next (final specialist walk) to consolidate the ten additive verifier asks from this walk + the trust-and-safety walk.
- **`enumerate-changes`** receives the four-walk output.
- **No lesser / body steward coordination needed** for the rework; the wire-mcp contract is stable.
- **No sibling-repo PRs required** in this rework (Greater additive components are a separate decision from the framework walk).
