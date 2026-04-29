# lesser-host docs

This directory contains maintainer documentation for `lesser-host` (the `lesser.host` control plane).

## Start here

- Roadmap: `docs/roadmap.md`
- Portal + operator API surface: `docs/portal.md`
- Managed provisioning (end-to-end): `docs/managed-instance-provisioning.md`
- Soul registry surface + instance integration (incl. AgentCore MCP): `docs/soul-surface.md`

## Soul registry

- Architecture + component placement: `docs/adr/0001-component-placement.md`
- Canonical IDs + signatures: `docs/adr/0002-canonical-identifiers-and-signatures.md`
- Suspension policy: `docs/adr/0003-suspension-policy.md`
- Agent-first client workflow contract: `docs/soul-agent-first-client-contract.md`
- Agent ID test vectors: `docs/spec/agent-id-test-vectors.md`
- On-chain contracts: `docs/soul-registry.md`
- S3 registry artifact layouts: `docs/soul-pack-bucket-layout.md`

## Managed provisioning

- Lesser release contract: `docs/lesser-release-contract.md`
- lesser-body release contract: `docs/lesser-body-release-contract.md`
- Managed release certification gate: `docs/managed-release-certification.md`
- Managed release readiness sync: `docs/managed-release-readiness.md`
- Managed update recovery contract: `docs/managed-update-recovery.md`
- Managed provisioning roadmap: `docs/roadmap-managed-provisioning.md`
- Recovery runbooks: `docs/recovery.md`, `docs/provisioning-recovery-plan.md`

## Trust & safety

- Attestation schema + verification: `docs/attestations.md`
- Moderation provider notes: `docs/moderation-provider.md`
- Evidence policy v1: `docs/evidence-policy-v1.md`
- Retention sweep: `docs/retention-sweep.md`

## Security remediation records

- Codex Security 2026-04-28 finding ledger: `docs/security/codex-findings-2026-04-28-ledger.md`
- M0 evidence baseline: `docs/security/codex-security-m0-evidence-baseline-2026-04-28.md`
- M5 evidence package: `docs/security/codex-security-m5-evidence-2026-04-29.md`
- M5 rollout checklist: `docs/security/codex-security-m5-rollout-checklist-2026-04-29.md`
- Release notes: `docs/security/codex-security-release-notes-2026-04-29.md`
