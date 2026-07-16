# Governance-rubric walk — web/ UI rework on FaceTheory v3.3.0 — 2026-05-24

Output of the `maintain-governance-rubric` walk for the 2026-05-24 web/ UI rework scoped-need (`docs/scoped-need-web-ui-rework-2026-05-24.md`, branch `aron/web-ui-rework-planning`). Consolidates the additive verifier asks from the three preceding specialist walks:

- Framework-feedback walk (commit `5579350`) — no rubric asks directly; Signal C (mixed-auth CloudFront composition) underlies SEC-8 below.
- Trust-and-safety walk (commit `24dcd86`) — 5 verifier asks.
- Provisioning walk (commit `e1aa36a`) — 5 verifier asks.

**Total: 10 new verifiers, all additive (tightening), no loosening.** Anti-drift discipline preserved throughout: each verifier produces 0 or full points (no partial credit); evidence is immutable; pack.json bumps document the shape-of-change in commit bodies.

## Current rubric state (anchored)

`gov-infra/pack.json` at version `2026.05.14-m7.2` (packDigest `c326dfb...`). One executable: `gov-infra/verifiers/gov-verify-rubric.sh`. Evidence codes currently in use (per `gov-infra/evidence/`):

- **QUA-1, QUA-2, QUA-3** — quality
- **CON-1, CON-2, CON-3** — contracts
- **SEC-1, SEC-2, SEC-3, SEC-4** — security
- **COM-1 through COM-6** — community / comms
- **CMP-4** — compliance
- **MAI-1, MAI-2, MAI-3, MAI-4** — maintainability
- **DOC-4, DOC-5** — documentation
- **CSR-001 through CSR-006 / CSR-022** — Project-38 Codex Security Remediation series (embedded within `gov-verify-rubric.sh`, with the CSR-022 expanded-forbid family at script line 1904)

The current verifier counts (29 per the gov-rubric report referenced in recent commits like `3ff3d1a` and `3ba0730`: "PASS 29/0/0") imply 29 deterministic checks across the categories listed above.

## Re-classification of verifier identifiers

The preceding specialist walks proposed identifiers like `CSR-022-FT-*` and `PROV-*`. Per the maintain-governance-rubric naming convention (canonical category prefixes), these are re-classified to:

| Walk-proposed name | Re-classified identifier | Category |
| --- | --- | --- |
| CSR-022-FT-CSP-INTEGRITY | **SEC-5** | Security |
| CSR-022-FT-HTML-INLINE-ABSENCE | **SEC-6** | Security |
| CSR-022-FT-OAC-FORM-INTEGRITY | **SEC-7** | Security |
| CSR-022-FT-CLOUDFRONT-COMPOSITION | **SEC-8** | Security |
| CSR-022-FT-AUTH-PRESERVATION | **SEC-9** | Security |
| PROV-RELEASE-VERIFICATION-PRESERVATION | **SEC-10** | Security |
| PROV-STACK-STATE-TENANT-SCOPING | **SEC-11** | Security |
| PROV-OPERATOR-DRIFT-AUTH-GATE | **SEC-12** | Security |
| PROV-WIRE-MCP-ROUTE-OWNERSHIP | **CON-4** | Contracts |
| PROV-WIRE-ALL-IDEMPOTENCY | **MAI-5** | Maintainability |

This keeps the CSR-022 series locked to its Project-38 origin and uses canonical SEC/CON/MAI numbering for ongoing rubric evolution. The CSR-022 family remains in place unchanged.

## Per-verifier 5-dimension walk

### SEC-5 — Web CSP byte-string integrity

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/web-csp-integrity.sh` (new file invoked by `gov-verify-rubric.sh`).

**Claim**: The `webCsp` and `safeAppCsp` arrays in `cdk/lib/lesser-host-stack.ts` (currently lines 1186-1211) contain only allowed directives:

- Each directive is in `{default-src, base-uri, object-src, frame-ancestors, form-action, img-src, font-src, style-src, script-src, connect-src, manifest-src}`
- `script-src` value is exactly `'self'` (no `'unsafe-inline'`, no `'unsafe-eval'`, no `nonce-`, no `sha256-` script-hash, no third-party origin)
- `style-src` value is exactly `'self'` (same restrictions)
- `connect-src` value is exactly `'self'` (same restrictions)
- `default-src` is exactly `'none'`
- `safeAppCsp.frame-ancestors` matches the allowlist `'self' https://safe.global https://*.safe.global` (the only governance-authorized exception; tightened via this verifier)

**Pass/Fail**: Full points if all directives match the allowlist; zero if any directive deviates.

**Input surface**: in-repo (`cdk/lib/lesser-host-stack.ts`). Reads via shell regex parsing. Deterministic.

**Evidence**: `gov-infra/evidence/SEC-5-output.log` — directive-by-directive validation per CSP variant, with commit SHA and verifier-run timestamp.

**No partial credit**: a deviation in any single directive is a fail.

### SEC-6 — Built HTML inline absence

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/web-html-inline-absence.sh`.

**Claim**: After `cd web && npm run build`, every file matching `web/dist/**/*.html` satisfies:

- No `<script>` element with inline body text (only `<script src="...">` or `<script type="module" src="...">`)
- No `<style>` element with inline body text
- No inline event handler attributes (`onclick=`, `onload=`, `onerror=`, `onmouseover=`, etc. — full HTML event-handler attribute list)
- No `data:` script URLs in `<script src="data:...">`
- All `<script src="...">` and `<link rel="stylesheet" href="...">` URLs are same-origin (begin with `/` or are relative paths; not `http://` or `https://`)

**Pass/Fail**: Full points if all HTML files satisfy all constraints; zero on the first violation (with file + offending element in evidence).

**Input surface**: in-repo build output. Deterministic given a reproducible build.

**Evidence**: `gov-infra/evidence/SEC-6-output.log` — list of HTML files scanned, constraint pass per file, any violations enumerated.

### SEC-7 — OAC form transport integrity

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/oac-form-integrity.sh`.

**Claim**: After `cd web && npm run build`, every `<form>` element in `web/dist/**/*.html` satisfies:

- If marked with `data-facetheory-oac-form`: the `action` attribute is same-origin (begins with `/`, is empty, or matches the current document origin pattern); the `method` is in `{POST, PUT, PATCH, DELETE}` (no GET on an OAC-marked form); the form is reachable only on FaceTheory-served routes (default-origin paths, not `/api/*` or `/.well-known/*` paths)
- If not marked with `data-facetheory-oac-form`: the form's `action` does not point at an `/api/*`, `/auth/*`, `/setup/*`, `/.well-known/*`, or `/attestations/*` path with a mutating method (those surfaces use bearer-auth JS fetches, not native form POSTs)

Plus a presence check: at least one bundled module imports `startAwsOacFormTransport` from `@theory-cloud/facetheory` (verifier reads built JS bundles via grep for the import string post-bundle).

**Pass/Fail**: Full points if all forms satisfy the constraints; zero on first violation.

**Input surface**: in-repo build output. Deterministic given a reproducible build.

**Evidence**: `gov-infra/evidence/SEC-7-output.log` — form-by-form validation, transport import presence check.

### SEC-8 — CloudFront distribution composition

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/cloudfront-composition.sh`.

**Claim**: After `cd cdk && npm run synth`, the synthesized `cdk.out/lesser-host-<stage>.template.json` for both `lab` and `live` stages contains a CloudFront distribution where:

- Exactly one default origin exists, and it is the SSR Lambda Function URL with `originAccessControl` (Lambda OAC) protection plus `AWS_IAM` authType
- A behavior pattern `/_facetheory/data/*` exists and routes to the S3 sidecar bucket (no Lambda)
- Behaviors for `/api/*`, `/auth/*`, `/setup/*` route to the control-plane-api Lambda Function URL with auth type `NONE` (bearer-auth in handler code; not changed by this rework)
- Behaviors for `/.well-known/*` and `/attestations/*` route to the trust-api Lambda Function URL with auth type `NONE` (same)
- Only the default origin is OAC-protected; no other origin in the distribution carries OAC

**Pass/Fail**: Full points if all behaviors match; zero on missing or mis-shaped behavior.

**Input surface**: synthesized CDK template (in-repo build output). Deterministic.

**Evidence**: `gov-infra/evidence/SEC-8-output.log` — distribution composition per stage, with behavior list + per-behavior origin type.

### SEC-9 — Trust auth + attestation signing preservation

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/trust-auth-preservation.sh`.

**Claim**: Across the planning branch (`aron/web-ui-rework-planning`) and its descendants (each M0-M3 PR branch), `git diff origin/main` shows no changes to:

- `internal/trust/auth_instance.go`
- `internal/trust/auth_instance_test.go`
- `internal/attestations/kms_service.go`
- `internal/attestations/kms_service_internal_test.go`
- `internal/trust/attestations_issue.go` (semantic changes only; cosmetic / dependency bumps allowed if scoped via separate scope-need)

**Pass/Fail**: Full points if no diff in the listed files (or diff is purely whitespace / import-order); zero on any semantic diff.

**Input surface**: `git diff` against `origin/main`. Deterministic per commit.

**Evidence**: `gov-infra/evidence/SEC-9-output.log` — file-by-file diff status with commit SHAs of current branch + origin/main.

**Refusal**: a milestone PR that touches these files must trigger explicit failure and require a separate scope-need + audit-trust-and-safety walk before override.

### SEC-10 — Two-channel release verification preservation

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/release-verification-preservation.sh`.

**Claim**: Across the planning branch's milestone commits, `git diff origin/main` shows no changes to:

- `internal/provisionworker/release_compatibility.go` (including `minimumSupportedManagedLesserReleaseVersion = "v1.2.6"` constant)
- `internal/provisionworker/release_compatibility_lesser_body.go` (including `minimumSupportedManagedLesserBodyReleaseVersion = "v0.2.3"` constant)
- `internal/provisionworker/release_preflight.go`
- `internal/provisionworker/release_preflight_lesser_body.go`
- `scripts/managed-release-certification/main.go`
- `scripts/managed-release-readiness/**/*.go`

**Pass/Fail**: Full points if no diff (or whitespace-only), or if the full locked-file semantic diff exactly matches a
reviewed governance-event fingerprint embedded in the verifier; zero on any other semantic diff. Reviewed fingerprints are
not blanket exceptions: the verifier hashes the entire locked-file diff, so any additional drift changes the fingerprint
and fails SEC-10.

**Reviewed event**: Project 48 M10/H2 (`lesser-host#700` / PR #921) intentionally hardens managed Body instance-plane
release verification by requiring `dist/lesser-body-instance.zip` to be declared as a required auxiliary asset,
checksum-covered, and template-bound before managed rollout. Its reviewed semantic diff fingerprint is
`d25674c5c67269e48c659449de25487efe6fcd3366c8f6329ed68f1cbf48df4e`.

**Input surface**: `git diff` against `origin/main`. Deterministic.

**Evidence**: `gov-infra/evidence/SEC-10-output.log` — file-by-file diff status.

### SEC-11 — Portal stack-state endpoint tenant scoping

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/portal-stack-state-tenant-scoping.sh`.

**Claim**: The handler registered at `GET /api/v1/portal/instances/{slug}/stack` (introduced in M1) performs an explicit ownership check before any DB read. Verifier reads handler code (via Go source inspection or `go vet`-style AST walk) and asserts:

- The handler function name resolves the `{slug}` path parameter
- Before any `s.store.DB...` call, the handler invokes the existing ownership-check helper (e.g. `s.requirePortalInstanceOwnership(ctx, slug)`) used by other `/api/v1/portal/instances/{slug}/*` endpoints
- The handler returns `http.StatusForbidden` (or `http.StatusNotFound` per host convention) on ownership-check failure

**Pass/Fail**: Full points if the ownership check is present + structurally correct; zero if missing or bypassable.

**Input surface**: in-repo Go source. Deterministic per commit.

**Evidence**: `gov-infra/evidence/SEC-11-output.log` — handler code path validation with the matched ownership-check call site.

**Triggered milestone**: SEC-11 activates with M1 (when the stack-state endpoint lands). Before M1 it skips with NOT-APPLICABLE status.

### SEC-12 — Operator drift endpoints auth gate

**Category**: SEC. **Path**: `gov-infra/verifiers/sec/operator-drift-auth-gate.sh`.

**Claim**: Each of the three new operator endpoints (introduced in M2) is registered with the operator-JWT auth requirement:

- `GET /api/v1/operators/releases` — `apptheory.RequireAuth()` + operator-role check
- `GET /api/v1/operators/instances/drift` — same
- `POST /api/v1/operators/instances/remediate-mcp-drift` — same

Verifier reads `internal/controlplane/server.go` route registrations and asserts each route carries the operator-auth middleware chain (same pattern as existing `/api/v1/operators/*` routes).

**Pass/Fail**: Full points if all three routes are registered with operator-auth middleware; zero if any route lacks it.

**Input surface**: in-repo Go source. Deterministic.

**Evidence**: `gov-infra/evidence/SEC-12-output.log` — route-by-route middleware chain check.

**Triggered milestone**: SEC-12 activates with M2.

### CON-4 — Wire-MCP route ownership preservation

**Category**: CON (contracts). **Path**: `gov-infra/verifiers/con/wire-mcp-route-ownership.sh`.

**Claim**: The MCP-wiring deploy runner (mode `"lesser-mcp"` per `internal/provisionworker/advance_body_mcp.go:138-151`) targets only the canonical lesser-owned route `POST /mcp/{actor}` on the tenant's lesser API gateway. Verifier reads the deploy-runner buildspec rendering (`cdk/lib/provision-runner-buildspec.ts` and any inline scripts) and asserts:

- The buildspec includes a `curl ... -X POST "https://api.<stageDomain>/mcp/<actor>"` invocation pattern
- The buildspec does not invoke any host-owned MCP route (any URL containing `lesser.host` or the host's CloudFront distribution domain in the body of an MCP-wiring command)
- The buildspec does not invoke any body-side MCP-wiring endpoint (body's role is to expose MCP tools, not to receive wiring directives)

**Pass/Fail**: Full points if the canonical lesser route is the only MCP-wiring target; zero on drift.

**Input surface**: in-repo TS source + rendered buildspec. Deterministic.

**Evidence**: `gov-infra/evidence/CON-4-output.log` — buildspec inspection with the matched route reference.

### MAI-5 — Wire-all endpoint idempotency

**Category**: MAI (maintainability). **Path**: `gov-infra/verifiers/mai/wire-all-idempotency.sh`.

**Claim**: The handler for `POST /api/v1/operators/instances/remediate-mcp-drift` (M2) enforces idempotency by querying the existing `GSI2 = UPDATE_ACTIVE` index on `UpdateJob` and skipping any slug that already has an active MCP-only update job. Verifier reads handler code and asserts:

- Before emitting an `UpdateJob{MCPOnly: true}` for a given slug, the handler queries `UpdateJob` by GSI2 with `UPDATE_ACTIVE` and filters by `InstanceSlug == slug` and `MCPOnly == true`
- If such a job exists, the handler does not emit a duplicate
- The handler emits an audit event per remediation triggered (`internal/controlplane/audit` or equivalent), with slug list

**Pass/Fail**: Full points if idempotency check + audit emission both present; zero on either missing.

**Input surface**: in-repo Go source. Deterministic.

**Evidence**: `gov-infra/evidence/MAI-5-output.log` — handler code path validation.

**Triggered milestone**: MAI-5 activates with M2.

## Threat-model additions

The following threats are added to `gov-infra/planning/lesser-host-threat-model.md`. Each is mapped to its mitigating controls + verifiers below.

| Threat ID | Threat | Mitigation | Verifier |
| --- | --- | --- | --- |
| T-CSP-001 | Adversary injects content via FaceTheory hydration sidecar, escaping strict CSP | Same-origin JSON sidecar at `/_facetheory/data/*` from S3 only; `script-src 'self'` enforced; no `'unsafe-inline'` / `'unsafe-eval'` / nonce / hash | SEC-5, SEC-6 |
| T-OAC-001 | Adversary attempts replay / CSRF via mutating form, bypassing AWS_IAM signing | `data-facetheory-oac-form` + `startAwsOacFormTransport()` requires same-origin action + `redirect: "error"`; cross-origin actions fail closed in FaceTheory | SEC-7 |
| T-COMP-001 | Adversary adds a non-OAC origin to CloudFront under the same distribution, gaining cookies / credentials inadvertently | CloudFront composition restricted: exactly one OAC default origin (SSR Lambda); existing bearer-auth Lambda origins for `/api/*`, `/auth/*`, `/setup/*`, `/.well-known/*`, `/attestations/*` unchanged | SEC-8 |
| T-AUTH-DRIFT-001 | Rework drift inadvertently changes instance-auth `sha256(raw_key)` semantics or attestation signing | Files in `internal/trust/auth_instance.go` and `internal/attestations/kms_service.go` change-locked; any modification triggers explicit governance event | SEC-9 |
| T-SUPPLY-001 | Rework drift weakens release-verification gate, allowing unverified lesser / body artifacts through | Files in `internal/provisionworker/release_*.go` and `scripts/managed-release-certification/main.go` change-locked | SEC-10 |
| T-TENANT-LEAK-001 | Customer reads another customer's stack state via `/api/v1/portal/instances/{slug}/stack` | Explicit ownership check before DB read; failure returns 403/404 | SEC-11 |
| T-OPER-AUTH-001 | Non-operator triggers fleet drift listing / wire-all remediation | Operator-JWT requirement on three new operator endpoints | SEC-12 |
| T-MCP-ROUTE-001 | MCP-wiring runner inadvertently targets a non-canonical route (host-internal or body-side), enabling drift between MCP wiring and lesser's actual MCP registration | Only `POST /mcp/{actor}` on lesser API gateway is a valid target; verifier reads buildspec | CON-4 |
| T-WIRE-FLOOD-001 | Operator triggers Wire-all repeatedly, creating duplicate MCP-only UpdateJobs per slug, flooding CodeBuild and producing inconsistent fleet state | Idempotency check skips slugs with active MCP-only job (GSI2 = UPDATE_ACTIVE) | MAI-5 |
| T-DRIFT-CROSS-TENANT-001 | Drift computation aggregates tenant content (not just version metadata) and leaks via operator-side response or cache | Drift state computation is metadata-only (version strings, timestamps, status enums); no tenant content read; operator-aggregated views are operator-JWT-gated | SEC-12 + tenant-isolation tests added in enumerated-changes |

## Controls-matrix additions

The following controls are added to `gov-infra/planning/lesser-host-controls-matrix.md`. Each control maps a threat to verifiers + evidence.

| Control ID | Control | Threats addressed | Verifiers | Evidence |
| --- | --- | --- | --- | --- |
| C-CSP-FT | Strict CSP via FaceTheory JSON sidecars | T-CSP-001 | SEC-5, SEC-6 | SEC-5-output.log, SEC-6-output.log |
| C-OAC-MUT | OAC mutating form safety | T-OAC-001 | SEC-7 | SEC-7-output.log |
| C-CDN-COMP | CloudFront composition integrity | T-COMP-001 | SEC-8 | SEC-8-output.log |
| C-AUTH-LOCK | Trust auth + attestation signing change-lock | T-AUTH-DRIFT-001 | SEC-9 | SEC-9-output.log |
| C-SUPPLY-LOCK | Release-verification path change-lock | T-SUPPLY-001 | SEC-10 | SEC-10-output.log |
| C-TENANT-SCOPE | Per-tenant ownership check on new portal endpoint | T-TENANT-LEAK-001 | SEC-11 | SEC-11-output.log |
| C-OPER-AUTH | Operator-JWT on fleet aggregation endpoints | T-OPER-AUTH-001, T-DRIFT-CROSS-TENANT-001 | SEC-12 | SEC-12-output.log |
| C-MCP-ROUTE | Wire-MCP route ownership lock to lesser's `POST /mcp/{actor}` | T-MCP-ROUTE-001 | CON-4 | CON-4-output.log |
| C-WIRE-IDEMP | Wire-all endpoint idempotency via GSI2 = UPDATE_ACTIVE | T-WIRE-FLOOD-001 | MAI-5 | MAI-5-output.log |

## pack.json bump plan

Current: `2026.05.14-m7.2`, packDigest `c326dfb...`.

Proposed bumps, one per milestone (so reviewers see the rubric shape evolve in lockstep with the rework):

| Milestone | pack.json version | Verifiers added | Trigger |
| --- | --- | --- | --- |
| M0 — FaceTheory foundation + design system + shell + Fleet | **`2026.05.24-web.0`** | SEC-5, SEC-6, SEC-7, SEC-8, SEC-9, SEC-10, CON-4 | M0 PR merges. All seven verifiers run from the M0 PR onward. |
| M1 — Portal hero pages + customer-readable update flows | **`2026.05.24-web.1`** | SEC-11 (activates with new stack-state endpoint) | M1 PR merges. |
| M2 — Operator Console + dark chrome + Provisioning evolution | **`2026.05.24-web.2`** | SEC-12, MAI-5 (both activate with new operator endpoints) | M2 PR merges. |
| M3 — Operator Console balance + cost-telemetry firehose | **`2026.05.24-web.3`** (or skip if no new verifiers) | (verifier asks for cost-telemetry redaction TBD by audit-trust-and-safety re-run at M3; deferred to that walk) | M3 PR merges. |

Each bump commit body explains:

- Which verifiers added
- Their categorical classification + claim
- Rationale (which threat they mitigate + which control they implement)
- A line stating "additive (tightening); no loosening; no exception; no skip"

## Anti-drift discipline

Per the skill's anti-drift rules:

- **No partial credit**: every new verifier is 0 or full points. None of the ten produce "warning" status.
- **No silent loosening**: all ten are additive (tightening). The CSR-022 family stays exactly as-is.
- **No exceptions**: no verifier in this set has a per-PR opt-out. If a milestone PR can't satisfy a verifier, the PR doesn't merge and a separate governance event must explicitly evolve the rubric.
- **Evidence immutability**: each verifier emits to `gov-infra/evidence/<code>-output.log` per the existing convention. New evidence files are appended; old ones not overwritten.
- **Reproducibility**: each verifier reads in-repo state (Go source, TS source, built artifacts, synthesized CDK templates, git diff). No external API reads. Verifier-version pinning unchanged from current convention (`PIN_GOVULNCHECK_VERSION = "v1.1.4"` etc.).
- **Pack.json bumps**: each milestone's bump is one commit with explicit body; reviewers see the shape-of-change clearly.

## Failure-mode discipline

Per the skill's failure-mode rules:

- **Real failure**: code is out of spec → fix the code. Example: M1 PR drops the ownership check from `/api/v1/portal/instances/{slug}/stack` → SEC-11 fails → fix the handler. Don't weaken SEC-11.
- **Flaky verifier**: same input produces different output → fix the verifier. Example: SEC-6 fails because the build is non-reproducible → fix the build, not SEC-6.
- **Out-of-date verifier**: world moved, rubric didn't. Example: lesser publishes a new MCP route alongside `POST /mcp/{actor}` → CON-4 needs an explicit governance event to broaden its acceptable target set, not a quiet edit.
- **Wrong verifier**: replace via governance event, not silent edit.

## Refusals triggered (none yet, but explicit guardrails)

- "Skip SEC-5 for the M0 PR because we haven't wired the new CSP yet." → **refuse**. M0 PR must land with the FaceTheory CSP in place. If the CSP isn't ready, M0 isn't ready.
- "Allow SEC-9 to ignore a one-line whitespace change in `auth_instance.go`." → **refuse**. SEC-9's pass condition is precise: no semantic diff. Whitespace-only is acceptable (verifier handles this), but the verifier doesn't get a manual override.
- "Combine SEC-11 and SEC-12 into one verifier." → **refuse without strong rationale**. Narrow specific claims preserve clarity; combining them creates partial-credit temptation.
- "Make MAI-5 produce a warning instead of failing the build for non-critical idempotency drift." → **refuse**. 0 or full points.

## Consumer-of-the-rubric impact

- **CI gating**: every host PR runs gov-verify-rubric.sh. After M0 merges and pack.json bumps to `2026.05.24-web.0`, 36 verifiers run (29 existing + 7 new). After M1 merges: 37. After M2: 39. The CI-gate behavior is "all verifiers pass or PR doesn't merge."
- **Evidence consumers**: external auditors and operators consuming `gov-infra/evidence/` see the new file patterns (SEC-5/6/7/8/9/10/11/12, CON-4, MAI-5 outputs). Documented in COM-* (community/comms) per existing pattern.
- **Managed-instance operators**: no impact on their deployments. The rubric governs host's own development, not tenant-side behavior.

## Cross-walk synthesis

The four walks land here as a single consolidated governance event:

1. **Framework-feedback walk** (5579350, updated 2026-05-24): four signals raised. **Signal A resolved 2026-05-24** Aron-direct (Greater Components owns UI primitives across all equaltoai products; M0.6 un-gated). **Signal B withdrawn 2026-05-24** per Arch's PR #380 review (framework support for tenant-partition-safe ISR confirmed; recategorized as host trust-surface conservatism note documenting the ISR deferral). **Signal C framework issues opened 2026-05-24** at theory-cloud/AppTheory#593 + theory-cloud/FaceTheory#248 (host waits for upstream resolution before M0.12 CDK adoption; Aron is the upstream theory-cloud maintainer); drives SEC-8. **Signal D request sent 2026-05-24** to Greater steward via host_lab email (delivery `delivery-f3c1b7a6f664bb27`); expanded list per Signal A resolution includes shell primitives + hosted-platform components; Aron is the upstream greater-components maintainer.
2. **Trust-and-safety walk** (24dcd86): CSP shape change is delivery-path only (contract preserved); OAC posture under AppTheorySsrSite documented; ISR deferred; auth + attestation signing preservation enforced. Drives SEC-5, SEC-6, SEC-7, SEC-8, SEC-9.
3. **Provisioning walk** (e1aa36a): three of four design-proposed "new" backend concepts already exist; wire-mcp is entirely host-internal; the actual new work is five additive read/aggregation endpoints + one operator-side write endpoint + UI-derived labeling. Drives SEC-10, SEC-11, SEC-12, CON-4, MAI-5.
4. **This governance walk**: ten additive verifiers consolidated, threat-model + controls-matrix entries proposed, pack.json bump plan staged per-milestone.

## Proposed next skill

The four-walk specialist phase is **complete**. The audit is clean (no loosening, no exceptions, no skipped evidence). Handoff to `enumerate-changes` with the full four-walk inputs:

- `docs/scoped-need-web-ui-rework-2026-05-24.md` — the scoped need
- `docs/framework-feedback-facetheory-v3.3.0-2026-05-24.md` — framework-feedback signals
- `docs/trust-and-safety-web-ui-rework-2026-05-24.md` — CSP / OAC / ISR / auth-preservation audit
- `docs/provisioning-web-ui-rework-2026-05-24.md` — wire-mcp / drift / stack-state / release-verification audit
- `docs/governance-rubric-web-ui-rework-2026-05-24.md` — consolidated verifier asks, threat model, controls matrix, pack.json bump plan

`enumerate-changes` produces the flat ordered list of commits required to implement M0–M3, with verifier additions interleaved at the correct milestone boundaries per the pack.json bump plan above.
