# M0 Evidence Baseline — Codex Security 2026-04-28

## Scope

M0 implements the evidence baseline for GitHub Project [#26](https://github.com/orgs/equaltoai/projects/26), parent issue [#181](https://github.com/equaltoai/lesser-host/issues/181), and ledger issue [#187](https://github.com/equaltoai/lesser-host/issues/187). It does not remediate vulnerabilities; it makes the remediation work non-abandoned by assigning every finding to a milestone cluster and recording the code evidence needed to start implementation safely.

## Inputs

- Codex Security CSV: `.theory/codex-security-findings-2026-04-28T21-19-20.568Z.csv`
- Investigation commit: `ecbc94340825c6c346f3bf637561bdd27cd816c3`
- Project: [Host Security Hardening — Codex Findings 2026-04-28](https://github.com/orgs/equaltoai/projects/26)
- Roadmap: `docs/roadmap-codex-security-remediation-2026-04-28.md`
- Ledger: `docs/security/codex-findings-2026-04-28-ledger.md`

## Finding counts

- Total: 69
- high: 13
- medium: 34
- low: 13
- informational: 9

## Triage guarantees

- Every finding #1–#69 has an owner issue in Project #26.
- Every high-severity finding has an M0 disposition note in the ledger.
- Finding #24 was added to Project #26 as issue #211 after M0 coverage review found it was not represented by a child cluster.
- Finding #13 is explicitly marked partially stale/residual-risk rather than treated as silently closed.
- The scanner CSV and `.theory/` project scratch files are not committed; repository docs carry the durable planning record.

## Validation baseline

The following checks were run before M0 implementation work:

- `go test ./...` — pass
- `go vet ./...` — pass
- `gofmt -l .` — empty on re-run
- `bash gov-infra/verifiers/gov-verify-rubric.sh` — pass (29 pass / 0 fail / 0 blocked)

No CDK, Solidity, or web code is changed by M0, so CDK synth, Hardhat/Slither/solhint, and web checks are deferred to milestones that touch those surfaces.

## Specialist routing

- M1 requires `audit-trust-and-safety` for public comm, mailbox, webhook, privacy, and CSP-adjacent surfaces.
- M2 requires `evolve-soul-registry` for operation receipt binding, signature/canonicalization, and contract findings.
- M3 requires `provision-managed-instance` for managed provisioning, managed update, instance-key stage isolation, and consumer release verification.
- M4 requires `maintain-governance-rubric` for CMP-4, `audit-trust-and-safety` for web/CSP/public read surfaces, and `coordinate-framework-feedback` if deploy-command interpolation belongs upstream in AppTheory.

## Non-goals

- No vulnerability fix is implemented in M0.
- No gov-infra verifier is weakened.
- No CSP, trust-auth, consumer-release-verification, multi-tenant, or on-chain shortcuts are introduced.
