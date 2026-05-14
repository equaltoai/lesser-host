# Codex Security Finding Ledger — 2026-04-28

This ledger is the repository-safe public index for the 2026-04-28 Codex security report. It intentionally does not
publish unresolved finding titles, exploit narratives, exact affected paths, raw request shapes, tokens, credentials,
or private scanner output.

## Public disclosure boundary

- Detailed scanner output is not committed to public docs.
- Unresolved findings are tracked by reference ID, severity, remediation cluster, and closure state only.
- Merged PR bodies become the public per-finding record after they include the concrete fix, regression coverage, and
  verification evidence.
- If a finding is stale or not applicable, the closing PR/issue must include code evidence and reviewer rationale without
  exposing unnecessary exploit detail.

## Disposition meanings

- **assigned-for-verification** — in mission and assigned to a remediation cluster; closure still requires tests or
  evidence-backed rationale.
- **fixed** — merged PR linked the reference to code/docs changes and verification output.
- **stale/not-applicable** — reviewed against current code and closed with repository evidence.
- **external-coordination** — requires sibling-repo, framework, vendor, or operator coordination before closure.

## Cluster index

| Cluster | Scope | Closure evidence |
| --- | --- | --- |
| M1 | Public ingress, comm, mailbox, and privacy blockers | Provider/auth regression tests, privacy/redaction assertions, sanitized logs, and trust/API auth checks. |
| M2 | Soul registry and on-chain operation integrity | Go regression tests, contract tests where applicable, Slither/solhint/Hardhat output for Solidity changes, and Sepolia/Safe-ready evidence when deployment is required. |
| M3 | Managed provisioning, release verification, and managed-update supply chain | Release-certification/readiness tests, pinned-artifact verification evidence, provisioning dry-run or canary notes, and instance-key isolation checks. |
| M4 | Public read scalability, web/CSP, governance, reputation, and model registration | Bounded-read tests, CSP-safe web checks, governance verifier output, and model/reputation regression tests. |
| M4.5 | Structured managed-provisioning consent alignment | Exact-byte consent preservation tests and checksum-verified release certification evidence before rollout. |
| M5 | Regression evidence, documentation, and rollout preparation | PR mapping, verifier output, lab evidence, and operator live-deploy checklist. |

## Finding-status register

The register below keeps only the minimum public state needed to audit closure. Detailed descriptions live in the owning
PR once the fix is ready for review.

| Reference range | Severity mix | Current public state | Remediation cluster |
| --- | --- | --- | --- |
| 1–14 | high/medium | Assigned or merged through cluster PRs; see merged PR bodies for closed references. | M1/M2/M3 |
| 15–30 | medium | Assigned or merged through cluster PRs; see merged PR bodies for closed references. | M1/M2/M3/M4 |
| 31–47 | medium | Assigned or merged through cluster PRs; see merged PR bodies for closed references. | M1/M2/M3/M4 |
| 48–60 | low | Assigned or merged through cluster PRs; see merged PR bodies for closed references. | M1/M3/M4 |
| 61–69 | informational | Assigned or merged through cluster PRs; see merged PR bodies for closed references. | M1/M2/M3/M4 |
| M4.5 follow-up | cross-repo alignment | Tracked separately from the 2026-04-28 scanner output; closure requires exact published-artifact certification. | M4.5 |

## Closure rule

A finding leaves the active register only when its owning PR or issue records one of the following:

1. a merged fix with regression coverage and verification output;
2. a reviewed stale/not-applicable disposition with code evidence; or
3. an explicit external-coordination handoff with the remaining host-side risk bounded.

Public docs must not be used as live inventories of unresolved attack detail. Keep unresolved detail in scoped review
threads and move only the closure evidence into public PR records.
