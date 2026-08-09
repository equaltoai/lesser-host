# Roadmap: Legacy TheoryLive authoritative recovery

## Goal

Deliver a read-only Host contract that lets an owning Managed instance recover exact legacy Soul declarations and honest provenance for Body adoption, while preserving immutable version history, InstanceKey hash authentication, tenant isolation, and the rule that missing historical publication evidence is never fabricated.

## Classification

Soul-registry, tenant-isolation, instance-auth, operational-reliability, bug-fix, test-coverage, and docs.

## Surfaces affected

- `scripts/soul-integrity-scan-m2`
- `internal/controlplane` Soul instance reads
- `docs/contracts/openapi.yaml` and hosted-genesis contract docs
- recovery runbook and Gov-infra Evidence

## Sibling-repo coordination

- lesser: no recovery-read dependency. Lesser remains sole owner of binding state, and its private self-scope authorization is not collapsed into InstanceKey authority.
- body: required; call Host directly through the existing managed `LESSER_HOST_INSTANCE_KEY_ARN` credential path, implement later additive operator-gated adoption, and retain Host identifiers/digests with their explicit provenance labels.
- soul: not required; no namespace change.
- greater: not required; no web surface.
- sim: recommended; validate cross-tenant denial, no-write reads, and legacy classifications.

## Framework coordination

- AppTheory: not required; existing route and error patterns are sufficient.
- TableTheory: not required; existing consistent/indexed reads are sufficient.
- FaceTheory: not required.

## External-vendor coordination

None. No Stripe, SES, AI provider, eth_rpc, or Safe signer dependency.

## Phases

### Phase 0: Integrity detection remediation

- Items: 1
- Dependencies: none
- Risks: scanner false-green if a published identity has zero history
- State: implemented locally as `3fe4426`; full green gate and PR still required

### Phase 1: Recovery domain and API contract

- Items: 2, 5
- Dependencies: Phase 0 remains green
- Risks:
  - selecting the wrong legacy conversation;
  - mislabeling a migration-read digest as a historical publication digest;
  - conflating current public registration with original declarations.
- Mitigations: exact Slug/registration/agent/conversation guards, deterministic selection, explicit provenance vocabulary, fixture-first tests.

### Phase 2: Authenticated inventory and detail reads

- Items: 3, 4
- Dependencies: Phase 1 selector/schema
- Risks:
  - cross-tenant enumeration;
  - leaking messages or provider evidence;
  - read-triggered publication convergence;
  - oversized declarations.
- Mitigations: derive Slug only from InstanceKey, domain-bound queries, no message decode, no convergence helper, no-write tests, bounded pagination/response bytes, structured redacted audit.

### Phase 3: Lab/live proof and sibling handoff

- Items: 6
- Dependencies: merged/released Host change; separately reviewed Body consumer change
- Risks:
  - live read unexpectedly mutates legacy state;
  - Body mistakes `legacy_declarations_only` for a valid historical publication.
- Mitigations: reuse Body's existing managed InstanceKey secret reference, pre/post consistent reads, lab cross-tenant tests, schema labels, keep private self-scope authorization separate, operator-gated Body adoption.

## Stage rollout plan

### Lab

- Command: `theory app up --stage lab --execute`
- Authorization: operator executes/authorizes deploy; steward does not deploy autonomously.
- Soak criteria:
  - test agents cover verified publication and declaration-only states;
  - valid InstanceKey succeeds, invalid/revoked key fails;
  - cross-Slug request returns boundary denial;
  - repeated reads yield stable semantic output;
  - pre/post DynamoDB/S3 comparison proves no write, convergence, publication, or version increment;
  - no declaration/message/key content appears in logs;
  - CloudFront/API and Lambda error rates remain normal.

### Live

- Command: `theory app up --stage live --execute`
- Authorization: explicit operator authorization after lab soak; never skip lab.
- Post-deploy monitoring:
  - `control-plane-api` errors and latency;
  - instance-auth failures and boundary denials;
  - response-too-large and integrity-conflict counts;
  - CloudFront 4xx/5xx;
  - audit retention and redaction;
  - pre/post checks for the four agents showing no state mutation.

## On-chain rollout plan

Not applicable. No Solidity, Sepolia, mainnet, Safe-ready payload, or off-chain/on-chain reconciliation change.

## Managed-instance rollout plan

Not a Provisioning worker or Managed update change. After Host live validation, Body adopts one canary identity under operator control, verifies exact content/provenance, then adopts the remaining three. Host does not write Body or Lesser tables.

## Release artifact plan

- GitHub Release: operator-selected next `v*` tag after staging→main promotion.
- Release notes: additive authenticated recovery reads, integrity scan correction, no public registration repair.
- Managed-consumer impact: Body/Lesser contract consumption only; no lesser/body deployment artifact is consumed by Host.

## Rollback plan

- Lambda: operator rolls back to the prior version if read errors/auth regressions appear.
- CDK: no infrastructure change expected; if route packaging requires one, revert commit and redeploy through AppTheory.
- Data: none; reads are side-effect-free.
- On-chain: n/a.
- Silas: public URI remains `404`; no rollbackable fabricated artifact is created.

## AGPL posture

- No proprietary blobs: confirmed.
- Dependency license vetting: no new dependency.

## Advisor-brief authorization

Not applicable. Request came from the principal's interactive session and a sibling steward mailbox, not an advisor address.

## Open questions

1. Whether the principal later authorizes an honest forward publication/re-attestation for Silas as a new version.
2. Project creation: this roadmap warrants a small cross-repo GitHub Project after principal approval because Host, Body, and Sim work must remain sequenced and separately owned.

Resolved coordination question: Body confirmed on 2026-08-09 that its deployed runtime already resolves the Host InstanceKey through `LESSER_HOST_INSTANCE_KEY_ARN`, without storing the raw key inline. The recovery surface is direct Body-to-Host; Lesser does not proxy it.
