# lesser-host: Lint Green Roadmap (Rubric v0.1.2)

Goal: get to a green lint/static-analysis pass across **Go + TypeScript + CDK** using the repo’s strict lint configuration,
**without** weakening thresholds or adding blanket exclusions.

This exists as a standalone roadmap because lint issues often require large, mechanical change sets that should be kept
reviewable and should not block unrelated remediation work (coverage/security/etc).

## Why this is a dedicated roadmap
- A failing linter blocks claiming CON-* and often blocks later work (coverage work tends to generate lint debt).
- “Green by dilution” (disabling rules, widening excludes) is not an acceptable solution.

## Baseline (start of remediation)
Snapshot (2026-02-06):
- Primary command (full rubric entrypoint): `bash gov-infra/verifiers/gov-verify-rubric.sh` (CON-2)
- Go lint command (target): `golangci-lint run --timeout=5m` (requires pinning a specific golangci-lint version)
- Web lint/typecheck commands:
  - `cd web && npm ci && npm run lint && npm run typecheck`
- CDK build command:
  - `cd cdk && npm ci && npm run build`

Current status:
- **Go lint is BLOCKED** until a specific `golangci-lint` version is pinned (see COM-2).
- Web + CDK lint/build status: unknown until run locally/CI (should be enforced by CON-2).

## Progress snapshots
- Baseline (2026-02-06): Go lint BLOCKED (pin missing)
- After LINT-1: TBD (record date + summary)
- After LINT-2: TBD (record date + summary)

## Guardrails (no “green by dilution”)
- Do not add blanket excludes (directory-wide or linter-wide) unless the scope is demonstrably out-of-signal.
- Prefer line-scoped suppressions with justification over disablements.
- Keep tool versions pinned (no `latest`) and verify config schema validity where supported.
- Keep formatter checks enabled so “fixes” don’t drift into style churn.

## Milestones (small, reviewable change sets)

### LINT-0 — Pin the Go linter toolchain (unblock determinism)
Goal: make Go lint deterministic.

Acceptance criteria:
- Pick and pin an exact `golangci-lint` version.
- Ensure CI uses that pinned version (no floating versions).
- Update `gov-infra/verifiers/gov-verify-rubric.sh` pins so CON-2/COM-3/SEC-1 are no longer BLOCKED.

### LINT-1 — Hygiene and mechanical fixes
Focus: reduce noise fast with low behavior risk.

Examples:
- Auto-fix formatting/imports.
- Fix typos/lint directives.
- Remove/replace stale suppressions.

Done when:
- Lint issue count drops meaningfully without changing linter policy.

### LINT-2 — Low-risk rule families (API-safe)
Focus: rules that are typically mechanical.

Examples:
- Unused parameter renames to `_` / `_unused`.
- Simplify repetitive patterns flagged by the linter.

Done when:
- The dominant “mechanical” linter families are cleared.

### LINT-3 — Correctness and error handling
Focus: stop ignoring errors and restore durable invariants.

Done when:
- Ignored-error findings are eliminated or narrowly justified.

### LINT-4 — Refactors for duplication and complexity
Focus: highest behavior risk; do last.

Done when:
- Lint is green (0 issues) under strict config.

## Helpful commands
```bash
# Full rubric (captures evidence logs)
bash gov-infra/verifiers/gov-verify-rubric.sh

# Web
cd web && npm ci && npm run lint && npm run typecheck

# CDK
cd cdk && npm ci && npm run build

# Contracts (compile gate)
cd contracts && npm ci && npm test

# Go format
cd -
```
