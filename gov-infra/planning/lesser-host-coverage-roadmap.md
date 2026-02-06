# lesser-host: Coverage Roadmap (to 80%) (Rubric v0.1)

Goal: raise and maintain meaningful Go coverage to **≥ 80%** as measured by the canonical verifier command, without
reducing the measurement surface.

This exists as a standalone roadmap because coverage improvements are usually multi-PR efforts that need clear
intermediate milestones, guardrails, and repeatable measurement.

## Prerequisites
- Lint is green (or has a dedicated lint roadmap) so coverage work does not accumulate unreviewed lint debt.
- The coverage verifier is deterministic and uses a stable default threshold (no “lower it to pass” override).

## Current state
Snapshot (2026-02-06):
- Coverage gate (target): generate `gov-infra/evidence/coverage.out` and enforce total ≥ 80%
- Current result: **7.3%** total (FAIL)
- Measurement surface:
  - In-scope: all packages included by `go test ./...` in the root Go module
  - Out-of-scope (explicit): nested Go modules (e.g., `cdk/`) unless added intentionally later

## Progress snapshots
- Baseline (2026-02-06): **7.3%** (from `gov-infra/evidence/coverage-summary.txt`)
- After COV-1 (2026-02-06): **8.6%** (auth hooks + secrets cache tests; `internal/controlplane`, `internal/trust`, `internal/secrets`)
- After COV-2 (2026-02-06): **25.0%** (broad helper floors across `internal/controlplane` + `internal/trust` + AI harness, plus first tests for `internal/domains`, `internal/metrics`, `internal/billing`, `internal/rendering`, `internal/observability`)
- After COV-3: TBD
- After COV-4: TBD
- After COV-5: TBD (≥ 80%, QUA-3 PASS)

## Guardrails (no denominator games)
- Do not exclude additional production code from the coverage denominator to “hit the number”.
- Do not move logic into excluded areas (examples/tests/generated) to claim progress.
- If package/module floors are needed, add explicit target-based verification rather than weakening the global gate.

## How we measure
1) Refresh coverage via the rubric verifier:
   - `bash gov-infra/verifiers/gov-verify-rubric.sh` (QUA-3)
2) Inspect:
   - `gov-infra/evidence/coverage.out`
   - `gov-infra/evidence/QUA-3-output.log`

## Proposed milestones (incremental, reviewable)
Milestones are designed to be **reviewable** and to avoid denominator games. Each milestone should:
- Add tests that exercise real logic (not only panics / trivial getters).
- Prefer deterministic, offline tests (no AWS / network calls).
- Be accompanied by an updated snapshot line above (percentage + notable packages covered).

### COV-1 — Eliminate 0% islands in security-critical paths
**Goal:** ensure the highest-risk auth surfaces are no longer completely untested.

**Acceptance criteria**
- Coverage increases (recorded in snapshots).
- At least one meaningful test exists for each:
  - `internal/controlplane`: operator auth/session + RBAC helpers
  - `internal/trust`: instance key auth hook
  - `internal/secrets`: SSM parameter parsing + caching (offline)

### COV-2 — Broad floor
**Goal:** reduce “untested cliff edges” by ensuring most in-scope packages have non-trivial coverage.

**Acceptance criteria**
- Total coverage ≥ **25%** (recorded).
- No package with ≥ 50 statements remains at **0%** coverage.

### COV-3 — Meaningful safety net
**Goal:** most core packages have real regression protection.

**Acceptance criteria**
- Total coverage ≥ **50%** (recorded).
- `internal/controlplane` and `internal/trust` each reach ≥ **50%**.

### COV-4 — High confidence
**Goal:** coverage is high enough that refactors are realistically safer.

**Acceptance criteria**
- Total coverage ≥ **70%** (recorded).
- `internal/controlplane` and `internal/trust` each reach ≥ **70%**.

### COV-5 — Finish line
**Goal:** meet the rubric gate and keep it green.

**Acceptance criteria**
- Total coverage ≥ **80%** (QUA-3 PASS).
- Coverage verifier stays deterministic (no scope reductions / excludes).

## Workstreams (target the highest-leverage paths first)
Suggested hotspots in this repo:
- `internal/controlplane/**` (authn/authz, bootstrap, billing/budgets)
- `internal/trust/**` (instance auth, previews/renders, SSRF defenses)
- `internal/attestations/**` (keying/signing)
- `internal/secrets/**` (SSM/KMS reads; no accidental logs)

## Helpful commands
```bash
bash gov-infra/verifiers/gov-verify-rubric.sh

go test ./...
# (use the rubric verifier to generate coverage.out deterministically)
```
