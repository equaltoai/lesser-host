# M5 Evidence Package — Codex Security 2026-04-28

## Scope

M5 covers the repository-owned closeout for GitHub Project
[#26](https://github.com/orgs/equaltoai/projects/26), parent issue
[#186](https://github.com/equaltoai/lesser-host/issues/186), and child issue
[#209](https://github.com/equaltoai/lesser-host/issues/209). It does not run stage deploys: `theory app up` lab/live
rollouts, on-chain deployments, and managed-instance canaries remain operator-run handoffs after this PR merges.

This package records the regression/evidence baseline at synced `main` commit `81f4ee3` after M0-M4.5 merged.

## Merged remediation PRs

| Phase | PR | Merge time (UTC) | Merge commit | Primary evidence |
|---|---|---:|---|---|
| M0 | [#212](https://github.com/equaltoai/lesser-host/pull/212) `docs(security): baseline Codex finding ledger` | 2026-04-29T01:07:47Z | `4f42007` | Finding ledger and M0 evidence baseline |
| M1 | [#213](https://github.com/equaltoai/lesser-host/pull/213) `fix(security): remediate M1 ingress privacy findings` | 2026-04-29T02:50:30Z | `634aeae` | Mailbox, comm webhook, SES verdict, channel ownership, privacy regressions |
| M2 | [#214](https://github.com/equaltoai/lesser-host/pull/214) `fix(security): remediate M2 soul integrity findings` | 2026-04-29T04:10:06Z | `291aaf0` | Soul operation receipt binding, lifecycle/signature replay, contract tests/lint |
| M3 | [#215](https://github.com/equaltoai/lesser-host/pull/215) `fix(security): remediate M3 provisioning release verification` | 2026-04-29T11:37:06Z | `5eae3e0` | Pinned release/version checks, checksum-preserving managed release gates, instance-key stage isolation |
| M4 | [#216](https://github.com/equaltoai/lesser-host/pull/216) `fix(security): remediate M4 public reliability findings` | 2026-04-29T14:38:16Z | `988402f` | Public read bounds, CSP-safe markdown, CMP-4 semantics, trust-route/runtime registration, AppTheory deploy hardening |
| M4.5 | [#222](https://github.com/equaltoai/lesser-host/pull/222) `fix(provision): align init-admin consent with lesser M9` | 2026-04-29T15:33:52Z | `81f4ee3` | Lesser M9 structured init-admin consent and exact-byte provisioning transport |

## Local validation at M5 start

Run from synced `main` on 2026-04-29 before opening the M5 evidence branch:

| Command | Result | Notes |
|---|---|---|
| `gofmt -l .` | PASS | Empty output |
| `go test ./...` | PASS | All Go packages passed |
| `go vet ./...` | PASS | No findings |
| `bash gov-infra/verifiers/gov-verify-rubric.sh` | PASS | `29` pass, `0` fail, `0` blocked |

The local gov-rubric report was regenerated at `2026-04-29T15:36:32Z` with pack version `4221f0e715b1`. CI will rerun
the same verifier for the M5 PR; `gov-infra` verifiers were not weakened.

## Regression coverage by finding cluster

| Cluster | Findings | Evidence status |
|---|---|---|
| M1 public ingress, comm, mailbox, and privacy | #1, #3, #4, #7-#11, #14, #19-#21, #23, #26, #29-#30, #39, #41, #43, #48-#49, #58-#62, #67 | PR #213 added focused Go regressions across control-plane, mailbox, comm-worker, email-ingress, and model paths; full Go validation passed locally and in CI. |
| M2 soul registry and on-chain operation integrity | #6, #22, #28, #31-#34, #38, #40, #44-#45, #68 | PR #214 added receipt/effect validation, signature/canonicalization regressions, bounded relationship/endorsement tests, and Solidity tests/lint. Contract bytecode changes still require post-merge Sepolia evidence before any mainnet Safe-ready execution. |
| M3 managed provisioning and release verification | #5, #12-#18, #47, #51-#55, #60, #63-#65 | PR #215 added release/version compatibility, certification/readiness, stage-isolated instance-key, trust verification, managed-update, and portal regressions. Consumer checksum verification remains mandatory. |
| M4 public read scalability, web/CSP, governance, reputation, and model registration | #2, #24-#25, #27, #35-#37, #42, #46, #50, #56-#57, #66, #69 | PR #216 added bounded public read/search/version/ENS coverage, web markdown sanitization tests, gov-infra CMP-4 semantics, trust-route env/IAM coverage, and AppTheory deploy-contract hardening. |
| M4.5 Lesser M9 provisioning alignment | Cross-repo handoff #217-#221 | PR #222 added structured consent and exact-byte transport regressions. M9 consumption remains blocked until lesser publishes exact M9 assets and host certifies them through checksum-verified managed-release/canary evidence. |

## Open rollout gates

These gates are intentionally not closed by repository implementation alone:

1. **Lab deploy and soak** — run `theory app up --stage lab` only as an operator-run rollout step, then follow
   `docs/security/codex-security-m5-rollout-checklist-2026-04-29.md`.
2. **Live deploy authorization** — live deploy requires explicit operator authorization after lab soak.
3. **On-chain contract rollout** — PR #214 included contract bytecode changes. Sepolia deploy/test evidence and any
   mainnet Safe-ready payloads are separate on-chain operations; never single-signer deploy to mainnet.
4. **Lesser M9 consumption** — PR #222 aligned host, but issue #221 remains blocked until lesser PR #901 merges and an
   exact published M9 release is available for checksum-verified certification and canary evidence.
5. **Managed-instance canary** — provisioning/update changes require a lab dry-run and a selected managed canary before
   broader rollout; do not bypass consumer release verification.

## Release note inputs

Operator-facing release notes live in
`docs/security/codex-security-release-notes-2026-04-29.md`. The live rollout checklist lives in
`docs/security/codex-security-m5-rollout-checklist-2026-04-29.md`.

## Non-goals

- No `theory app up` or CDK deploy was run by this implementation PR.
- No live deployment was performed.
- No Sepolia or mainnet on-chain transaction was sent.
- No managed-instance rollout or M9 release certification was performed.
- No gov-infra verifier, release-verification gate, trust-API instance-auth rule, or CSP posture was weakened.
