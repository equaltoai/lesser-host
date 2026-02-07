# lesser-host: 10/10 Roadmap (Rubric v0.1.2)

This roadmap maps milestones directly to rubric IDs with measurable acceptance criteria and verification commands.

## Current scorecard (Rubric v0.1.2)
Generate the scorecard by running:

- `bash gov-infra/verifiers/gov-verify-rubric.sh`
- Then read: `gov-infra/evidence/gov-rubric-report.json`

Snapshot (2026-02-07): verifier reports **PASS** (28/28).

## Evidence commands (canonical)
All evidence is produced by the deterministic verifier:
- `bash gov-infra/verifiers/gov-verify-rubric.sh`

Per-ID evidence logs are written under `gov-infra/evidence/*-output.log`.

## Rubric-to-milestone mapping
Status meanings used in this roadmap:
- **PASS**: verifier exists and should pass in a clean repo state
- **FAIL**: verifier exists and is expected to fail until remediation
- **BLOCKED**: verifier cannot be trusted yet (missing pin/tooling)
- **TBD**: needs first verifier run to confirm

| Rubric ID | Status (current) | Milestone |
| --- | --- | --- |
| CMP-1 | PASS | M0 |
| CMP-2 | PASS | M0 |
| CMP-3 | PASS | M0 |
| DOC-1 | PASS | M0 |
| DOC-2 | PASS | M0 |
| DOC-3 | PASS | M0 |
| DOC-5 | PASS | M0 |
| DOC-4 | PASS | M0 |
| QUA-1 | PASS | M1 |
| QUA-2 | PASS | M1 |
| QUA-3 | PASS | M1.5 |
| CON-1 | PASS | M1 |
| CON-2 | PASS | M1 |
| CON-3 | PASS | M3 |
| COM-1 | PASS | M2 |
| COM-2 | PASS | M2 |
| COM-3 | PASS | M1 |
| COM-4 | PASS | M1.5 |
| COM-5 | PASS | M2 |
| COM-6 | PASS | M2 |
| SEC-1 | PASS | M2 |
| SEC-2 | PASS | M2 |
| SEC-3 | PASS | M2 |
| SEC-4 | PASS | M3 |
| MAI-1 | PASS | M3 |
| MAI-2 | PASS | M0 |
| MAI-3 | PASS | M3 |
| MAI-4 | PASS | M2 |

## Workstream tracking docs (generated)
- Lint remediation: `gov-infra/planning/lesser-host-lint-green-roadmap.md`
- Coverage remediation: `gov-infra/planning/lesser-host-coverage-roadmap.md`

## Milestones (sequenced)
### M0 — Freeze rubric + planning artifacts
**Closes:** CMP-*, DOC-*, MAI-2  
**Goal:** prevent goalpost drift by making the definition of “good” explicit and versioned.

**Acceptance criteria**
- Rubric exists and is versioned.
- Threat model exists and is owned.
- Evidence plan maps rubric IDs → verifiers → artifacts.
- Doc integrity check is green (no template tokens in `gov-infra/`).

### M1 — Make core lint/build loop reproducible
**Closes:** CON-1, CON-2, COM-3  
**Goal:** strict lint/format enforcement with pinned tools; no drift.

Tracking document: `gov-infra/planning/lesser-host-lint-green-roadmap.md`

**Acceptance criteria**
- Formatter clean.
- Go lint green with schema-valid config.
- Web lint/typecheck green.
- CDK build green.
- Tool versions pinned; no blanket excludes.

### M1.5 — Coverage/quality gates
**Closes:** QUA-1, QUA-2, QUA-3, COM-4  
**Goal:** meet coverage floor (≥ 80%) without reducing scope; tests green.

Tracking document: `gov-infra/planning/lesser-host-coverage-roadmap.md`

### M2 — Enforce in CI + supply-chain hardening
**Closes:** COM-1, COM-2, COM-5, COM-6, MAI-4, SEC-1..3  
**Goal:** run the rubric surface in CI with pinned tooling; supply-chain protections fail closed.

**Acceptance criteria**
- CI runs `bash gov-infra/verifiers/gov-verify-rubric.sh`.
- GitHub Actions are pinned by commit SHA (no `uses: ...@vN`).
- CI archives `gov-infra/evidence/` artifacts for review.

### M3 — Security-critical path hardening + maintainability budgets
**Closes:** SEC-4, MAI-1, MAI-3, CON-3, COM-6  
**Goal:** add targeted regression gates and convergence budgets for high-risk paths.

Suggested M3 sub-milestones:
- P0 regression tests for bootstrap/setup authz + operator sessions + instance-key auth
- SSRF defenses for preview/render fetchers
- AI prompt boundary and schema integrity tests
- Maintainability budgets for “god files” and duplicate security helpers
