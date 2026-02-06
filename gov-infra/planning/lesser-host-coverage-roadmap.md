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
- Current result: unknown (run `bash gov-infra/verifiers/gov-verify-rubric.sh` to generate evidence)
- Measurement surface:
  - In-scope: all packages included by `go test ./...` in the root Go module
  - Out-of-scope (explicit): nested Go modules (e.g., `cdk/`) unless added intentionally later

## Progress snapshots
- Baseline (2026-02-06): TBD (record % from `gov-infra/evidence/QUA-3-output.log`)
- After COV-1: TBD (record % + changed packages)
- After COV-2: TBD (record % + changed packages)

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
- COV-1: eliminate “0% islands” in security-critical packages (auth, sessions, provisioning, trust)
- COV-2: broad floor (25%+ across in-scope packages)
- COV-3: meaningful safety net (50%+)
- COV-4: high confidence (70%+)
- COV-5: finish line (≥ 80% and the gate is green)

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
