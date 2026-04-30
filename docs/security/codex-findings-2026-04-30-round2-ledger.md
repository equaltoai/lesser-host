# Codex Security round 2 ledger — 2026-04-30

This repository-safe ledger records the second Codex Security review for host. The source CSV remains local at
`.theory/codex-security-findings-2026-04-30T13-51-20.970Z.csv`; do not copy exploit-ready finding text into public
documentation.

## Review baseline

- Reviewed against host `main` commit `f613299716b442c3bb82466dfa293bdc8a0c73da`.
- Deployment assumption for this remediation line: host is pre-live, with active validation limited to lab and Sepolia.
- There are no live users, live data, or live tipping events to preserve.
- Existing lab data may be migrated, denied, or reset where that simplifies secure pre-live state.
- `theory` is the internal validation stage; `simulacrum` is the host-lab canary.
- Parallel Lesser security remediation is coordinated before host consumes or canaries relevant Lesser release artifacts.

## Triage counts

| Classification | Count | Rows |
| --- | ---: | --- |
| Fixed or stale after the prior merged work | 37 | `1-6`, `22-39`, `59-65`, `67-69`, `71-73` |
| Confirmed outstanding | 37 | Tracked through M6-M9 below |
| Partial / residual / policy | 3 | Tracked through M6-M9 below |

## Remediation tracking

| Milestone | GitHub issue | Scope | Rows |
| --- | --- | --- | --- |
| M6 | #226 | Pre-live isolation and provisioning hardening | `7-12`, `43`, `45`, `51`, `76`, plus partial/policy `20`, `48` |
| M7 | #227 | Trust, AI, managed-update, and billing hardening | `13-16`, `40-42`, `52-58`, `77`, plus residual `57` |
| M8 | #228 | Comms, operator surface, web, and test reliability | `18`, `19`, `21`, `44`, `49`, `50`, `66`, `70`, `74`, `75` |
| M9 | #229 | Sepolia TipSplitter hardening | `17`, `47` |

## Disclosure discipline

This ledger intentionally records row numbers, counts, assumptions, and milestone ownership only. Detailed finding text
stays in the local scan artifact and the private triage context until the corresponding fix lands. Public release notes
should summarize fixed classes of risk after merge rather than advertise unresolved exploit paths before remediation.
