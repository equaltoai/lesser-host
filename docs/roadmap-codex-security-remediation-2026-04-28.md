# Roadmap: Host Security Remediation Coordination — 2026-04-28

## Goal

Coordinate remediation of the 2026-04-28 Codex security report without weakening host's governance rubric,
multi-tenant isolation, on-chain integrity, consumer release verification, trust/API auth posture, or strict CSP. Done
means each reported item is fixed with regression coverage and evidence, resolved as stale/not-applicable with repository
evidence, or routed to a documented external/sibling coordination item. Deployment remains a separate operator phase.

## Public disclosure boundary

This public roadmap intentionally omits unresolved finding titles, exploit narratives, exact affected paths, and raw
scanner output. Those details are tracked in review records only as needed for maintainers to reproduce and fix a finding.
Public documentation should describe remediation sequencing, governance gates, and rollout discipline without becoming a
live attack map.

When a finding is closed, the merged PR body is the public record that maps the finding reference to the concrete fix,
regression coverage, and verification commands. Until then, this roadmap stays cluster-level.

## Classification

Security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry /
trust-API / CSP / operational-reliability / bug-fix / test-coverage / docs.

## Source and tracking

- Source report date: 2026-04-28.
- Detailed report artifacts: not committed to public docs.
- Program tracking: GitHub Project [#26 — Host Security Hardening — Codex Findings 2026-04-28](https://github.com/orgs/equaltoai/projects/26).
- Closure record: merged PR bodies and evidence artifacts, not unresolved public-path inventories.

## Remediation phases

### Phase 0: Evidence baseline and project triage

- Assign each report item to a remediation cluster.
- Record whether it is confirmed, stale/not-applicable, or assigned for verification.
- Require a reproducer, code evidence, or explicit not-applicable rationale before closure.

### Phase 1: Public ingress, comm, mailbox, and privacy blockers

- Harden inbound provider trust, replay/idempotency handling, and sender/verdict treatment.
- Preserve tenant boundaries and bounded mailbox semantics.
- Redact or bound public surfaces where stored evidence or validation material would otherwise be overexposed.
- Keep instance-auth strict and logs sanitized.

### Phase 2: Soul registry and on-chain operation integrity

- Validate operation execution against expected on-chain state transitions before recording irreversible side effects.
- Preserve canonical signature/domain separation across soul lifecycle operations.
- Treat contract changes with the full Slither, solhint, Hardhat, Sepolia, and Safe-ready mainnet discipline.

### Phase 3: Managed provisioning, release verification, and managed-update supply chain

- Remove ambiguous or floating managed-release inputs from provisioning paths.
- Preserve checksum-based consumer release verification for lesser and body artifacts.
- Keep instance-key handling stage-scoped and hash-only.
- Exercise managed update semantics without weakening rollback or per-slug isolation.

### Phase 4: Public read scalability, web/CSP, governance, reputation, and model registration

- Bound public reads and expensive enrichment paths.
- Keep the web UI CSP-safe; do not introduce inline script/style, eval, or third-party script origins.
- Tighten governance verifiers where scanner findings show keyword-only or parser-only blind spots.
- Add regression coverage for reputation/model-registration behavior changes.

### Phase 4.5: Lesser M9 structured init-admin consent alignment

- Preserve exact signed consent bytes through portal verification, stored job state, CodeBuild transport, and
  `provision.json`.
- Keep M9 consumption gated on checksum-verified exact published lesser artifacts, managed-release certification,
  readiness checks, lab dry-run, and canary evidence before live/default rollout.

### Phase 5: Regression evidence, docs, and staged rollout

- Link each closed finding to tests, verifier output, or evidence-backed not-applicable rationale.
- Update operator-facing docs only after behavior changes are merged.
- Produce lab evidence and a live authorization checklist for the operator-managed deployment phase.

## Rollout discipline

### Repository gates

- `go test ./...`
- applicable web/CDK/contract gates for touched surfaces
- `bash gov-infra/verifiers/gov-verify-rubric.sh`
- PR body maps report references to fixes, tests, and verification output

### Host service deployment

Deployment is operator-owned and follows normal host discipline:

1. Merge through PR with governance verifiers green.
2. Deploy to `lab` via the AppTheory contract.
3. Soak and collect evidence for affected surfaces.
4. Deploy to `live` only with explicit operator authorization.
5. Monitor CloudWatch, CloudFront, queue depth, provider callbacks, provisioning jobs, trust/API auth failures, and
   on-chain operation outcomes as applicable.

### On-chain rollout

Contract-affecting work requires Sepolia first, source verification, Slither/solhint/Hardhat output, Safe-ready mainnet
payloads for non-trivial mutations, and post-deploy bytecode/source verification.

### Managed-instance rollout

Provisioning-affecting work requires lab dry-run, a selected canary managed instance, per-slug rollback/remediation
planning, and no bypass of consumer release verification.

## AGPL and framework posture

- No proprietary blobs.
- New dependencies require license compatibility review.
- Framework awkwardness is routed to the relevant Theory Cloud steward instead of local framework patching.
- Public docs remain useful for governance and rollout review without exposing unresolved attack detail.
