# Roadmap: Host Security Hardening — Codex Findings 2026-04-28

## Goal

Remediate the 69 new Codex Security findings reported in `.theory/codex-security-findings-2026-04-28T21-19-20.568Z.csv` without weakening host's governance rubric, multi-tenant isolation, on-chain integrity, consumer release verification, trust/API auth posture, or strict CSP. Done means every finding is either fixed with regression coverage and evidence, explicitly marked stale/not-applicable with repository evidence, or routed to a documented external/sibling coordination item; lab soak and live rollout are completed under normal host deployment discipline.

## Classification

Security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry / trust-API / CSP / operational-reliability / bug-fix / test-coverage / docs.

## Source findings

- Report: `.theory/codex-security-findings-2026-04-28T21-19-20.568Z.csv`
- GitHub Project: [#26 — Host Security Hardening — Codex Findings 2026-04-28](https://github.com/orgs/equaltoai/projects/26)
- Parent issues: [#181](https://github.com/equaltoai/lesser-host/issues/181) through [#186](https://github.com/equaltoai/lesser-host/issues/186); cluster issues [#187](https://github.com/equaltoai/lesser-host/issues/187) through [#211](https://github.com/equaltoai/lesser-host/issues/211)
- Current investigation commit: `ecbc94340825c6c346f3bf637561bdd27cd816c3`
- Finding count: 69 total — 13 high, 34 medium, 13 low, 9 informational
- Confirmed live high-risk clusters: mailbox content overwrite, public soul chain fanout, unauthenticated comm webhooks/voice billing, SES spoofed sender trust, email/channel/ENS takeover, unbound soul operation tx recording, public dispute evidence exposure, unpinned `latest` provisioning, forced remote-agent enablement.
- Stale/partially obsolete: high finding "Unverified lesser-body tarball executed in privileged runner" is materially changed on current HEAD because lesser-body named release assets are checksum-verified before execution; residual risk remains through `latest` defaults and certification gates.

## Surfaces affected

- `cmd/control-plane-api`, `internal/controlplane`: public soul APIs, comm webhooks, portal instance/update APIs, channel provisioning, operation recording, billing/metering, public dispute/validation surfaces.
- `cmd/email-ingress`, `internal/emailingress`: SES inbound bridge and sender/authentication verdict handling.
- `cmd/comm-worker`, `internal/commworker`, `internal/commmailbox`: inbound routing, mailbox capture/content storage, preview/listing, queue/status, sender enrichment.
- `cmd/provision-worker`, `internal/provisionworker`, `cdk/lib/provision-runner/*`: managed provisioning, managed update, release verification, lesser-body certification, instance-key secrets, trust verification.
- `internal/store/models`: soul identity/channel/index/mailbox/operation models and TableTheory registration.
- `contracts/`: SoulRegistry, TipSplitter, OffchainResolver contract issues.
- `scripts/managed-release-certification`, `scripts/managed-release-readiness`: release evidence/certification semantics.
- `gov-infra/verifiers`: CMP-4 verifier semantic hardening.
- `web/`: MarkdownRenderer sanitization and portal update UX correctness.
- `cdk/`: CodeBuild runner, CloudFront/trust routing/IAM, webhook base URLs.
- `app-theory/app.json`: AppTheory deployment contract command interpolation.

## Sibling-repo coordination

- lesser: required for managed provisioning behavior validation and canary managed-instance rollout expectations; no immediate lesser code edit planned.
- body: required for comm/mailbox API compatibility and lesser-body release certification expectations; no immediate body code edit planned unless host-side contract changes require body adaptation.
- soul: awareness required for public soul namespace semantics, channel ownership/index invariants, and public privacy posture; no namespace URL change planned.
- greater: awareness only if MarkdownRenderer fix needs upstream greater-components alignment; host's local web consumption must remain CSP-safe.
- sim: required for integration validation of portal/trust/soul/comm tightened behavior.

## Framework coordination

- AppTheory: required for finding #24 if safe deploy-command interpolation belongs in the framework rather than host's app contract; otherwise host must consume the framework idiomatically without local patches.
- TableTheory: possible if conditional create/update ownership semantics need framework support; default path is host-side conditional reads/writes, not local framework patches.
- FaceTheory: possible only if MarkdownRenderer sanitization belongs upstream; no local CSP loosening.

## External-vendor coordination

- Stripe / billing: no Stripe change expected; voice billing idempotency must preserve ledger correctness.
- SES / email vendors: SES receipt verdicts, Migadu mailbox conflict behavior, and forwarding idempotency need vendor-aware handling.
- Telnyx: webhook signature validation and canonical base URL handling required.
- AI providers: OpenAI mint stream token cap / cost guard hardening.
- eth_rpc provider: public soul read fanout must be bounded/cached to protect provider availability and cost.
- Safe multisig signers: required only if contract changes progress beyond Sepolia to mainnet.

## Phases

### Phase 0: Evidence baseline and project triage

- Items: all findings #1–#69.
- Dependencies: none.
- Risks:
  - False positives or stale findings consume security bandwidth.
  - Fixing without reproducer coverage can hide regressions.
- Deliverables:
  - Finding ledger in issue bodies with status: confirmed / stale / fixed / not-applicable.
  - Minimal reproducer or code evidence for every high and medium finding.
  - Priority labels and owner surfaces assigned.

### Phase 1: P0 public ingress, comm, mailbox, and privacy blockers

- Items: #1, #3, #4, #7, #8, #9, #10, #11, #14, #19, #20, #21, #23, #26, #29, #30, #39, #41, #43, #48, #49, #58, #59, #61, #62, #67.
- Dependencies: Phase 0 triage for exact repro/acceptance; `audit-trust-and-safety` walk.
- Risks:
  - Tightening webhook auth can interrupt inbound email/SMS/voice delivery if vendor signatures are mis-modeled.
  - Mailbox content immutability must not create ghost rows or cross-agent reads.
  - Public privacy fixes must preserve intentionally public soul read semantics.
- Deliverables:
  - Provider-authenticated comm webhook handling with replay/idempotency controls.
  - SES SPF/DKIM/DMARC verdict gating before sender identity enrichment.
  - Mailbox content write reservation/immutability and digest consistency.
  - Email/phone/ENS/channel uniqueness and ownership guards.
  - Public dispute/validation/channel/search responses redacted, bounded, or authenticated as appropriate.
  - Header/log injection and mailbox preview/normalization DoS fixes.

### Phase 2: Soul registry and on-chain operation integrity

- Items: #6, #22, #28, #31, #32, #33, #34, #38, #40, #44, #45, #68.
- Dependencies: `evolve-soul-registry` walk; contract changes require Hardhat tests, Slither, solhint, Sepolia evidence before mainnet Safe-ready payloads.
- Risks:
  - On-chain operation side effects are irreversible if tx receipts remain unbound.
  - Contract changes can require forward-fix deploys rather than rollback.
  - Signature canonicalization must align web and Go exactly.
- Deliverables:
  - Tx receipt validation bound to expected contract, calldata/event logs, value, and agent-specific state transition before status/side effects.
  - Mint/rotation/lifecycle signature replay controls and canonicalization fixes.
  - Public relationship/endorsement merge bounds.
  - Contract fixes or explicit not-applicable evidence for OffchainResolver, SoulRegistry, and TipSplitter findings.

### Phase 3: Managed provisioning, release verification, and managed-update supply chain

- Items: #5, #12, #13, #15, #16, #17, #18, #47, #51, #52, #53, #54, #55, #60, #63, #64, #65.
- Dependencies: `provision-managed-instance` walk; release-verification discipline; canary managed instance selected before live broad rollout.
- Risks:
  - Removing `latest` defaults changes operator workflows and may block misconfigured deploys immediately.
  - Release verification changes can stop managed provisioning if manifests are incomplete.
  - Instance-key secret stage isolation touches tenant trust/auth and must not expose raw keys.
- Deliverables:
  - Explicit pinned release requirements or operator-approved resolution flow; no silent `latest` default in managed provisioning.
  - `git_sha` validation as immutable 40-hex commit SHA where source checkout is required.
  - lesser-body template certification enabled by default where safety requires it, with truthful evidence status.
  - Stage-scoped instance key secret names and authenticated trust verification restored.
  - Body-only/update metadata semantics corrected, including portal/operator blank-version config and key-rotation actions.
  - Tip config validation prevents deploy-wedging configuration.

### Phase 4: Public read scalability, web/CSP, governance, reputation, and model registration

- Items: #2, #24, #25, #27, #35, #36, #37, #42, #46, #50, #56, #57, #66, #69.
- Dependencies: `audit-trust-and-safety` for CSP/web and public reads; `maintain-governance-rubric` for CMP-4; possible `coordinate-framework-feedback` if TableTheory model registration friction is framework-level.
- Risks:
  - CSP must not be loosened to accommodate Markdown rendering.
  - Public read bounding must not break legitimate registry discovery.
  - Governance verifier fixes must tighten semantics without creating flaky CI.
- Deliverables:
  - Public soul reads bounded/cached/paginated with rate/cost awareness.
  - MarkdownRenderer sanitization is mandatory for untrusted content; no `unsafe-inline`, no `unsafe-eval`.
  - CMP-4 verifier validates semantics, not keyword presence.
  - Reputation and model registration fixes with tests.
  - AppTheory deploy-command interpolation finding routed to a safe host contract change or framework-feedback issue.

### Phase 4.5: Lesser M9 structured init-admin consent alignment

- Items: [#217](https://github.com/equaltoai/lesser-host/issues/217),
  [#218](https://github.com/equaltoai/lesser-host/issues/218),
  [#219](https://github.com/equaltoai/lesser-host/issues/219),
  [#220](https://github.com/equaltoai/lesser-host/issues/220),
  [#221](https://github.com/equaltoai/lesser-host/issues/221).
- Dependencies: Phase 4 merged; `provision-managed-instance` walk; lesser PR
  [#901](https://github.com/equaltoai/lesser/pull/901) before final release certification/canary evidence.
- Risks:
  - host currently transports provisioning consent but must emit the structured JSON that Lesser M9 accepts.
  - Any whitespace trimming or reserialization between wallet signing, `ProvisionJob`, CodeBuild, `provision.json`, and
    `lesser init-admin` invalidates signature/replay semantics.
  - Certifying M9 before exact published release assets exist would weaken consumer release verification.
- Deliverables:
  - host-generated `consent_message` is compact JSON with `kind=lesser.init_admin_consent.v1`, managed stage `instance`,
    `username`, `nonce`, and `expires_at` only.
  - Exact signed bytes are preserved through portal verification, stored job state, CodeBuild environment transport, and
    `provision.json`.
  - Managed provisioning docs and Lesser release contract document M9 consent shape, instance-stage domain derivation,
    CORS sequencing, and no-bypass release-certification expectations.
  - M9 consumption remains gated on checksum-verified exact published assets, managed-release certification/readiness,
    lab dry-run, and canary evidence before live/default rollout.

### Phase 5: Regression evidence, docs, and staged rollout

- Items: all fixed findings.
- Dependencies: Phases 1–4.5 fixes merged through PR with gov-infra verifiers.
- Risks:
  - Evidence gaps can make a fix unreviewable.
  - Lab-only success may not cover live vendor/provider behavior.
- Deliverables:
  - Tests for each finding cluster.
  - Updated docs/runbooks where behavior changes operator or customer flows.
  - `gov-infra/evidence/` updated by verifiers where applicable.
  - Lab deployment and soak evidence.
  - Live deploy authorization checklist and post-deploy monitoring notes.
- M5 repository-owned records:
  - Evidence package: `docs/security/codex-security-m5-evidence-2026-04-29.md`
  - Rollout checklist: `docs/security/codex-security-m5-rollout-checklist-2026-04-29.md`
  - Release notes: `docs/security/codex-security-release-notes-2026-04-29.md`

## Stage rollout plan (host's own service)

### Lab

- Command: `theory app up --stage lab`
- Soak duration: at least one business day for non-critical phases; compressed but not skipped for P0 hotfixes.
- Soak criteria:
  - `go test ./...`, web lint/typecheck/tests as applicable, CDK synth, gov-infra rubric pass.
  - Public soul endpoints return bounded/redacted responses with expected cache/rate behavior.
  - Comm webhook tests pass with valid/invalid/replayed provider signatures.
  - SES ingress test events handle authentication verdicts correctly.
  - Managed provisioning dry-run/test slug succeeds with pinned release and checksum verification.
  - Soul tx receipt validation tested against Sepolia receipts.

### Live

- Command: `theory app up --stage live`
- Authorization: explicit operator approval after lab soak.
- Post-deploy monitoring:
  - CloudWatch error rate per Lambda.
  - CloudFront 4xx/5xx by route family.
  - comm-worker queue depth and DLQ.
  - SES ingress failures and verdict rejects.
  - Telnyx/Migadu webhook success/failure rates.
  - provisioning-worker SQS depth and CodeBuild failures.
  - trust/API instance-auth failure rate.
  - eth_rpc latency/error rates.
  - soul operation record-execution rejects and successful validated receipts.
  - gov-infra evidence freshness.

## On-chain rollout plan

- Sepolia deploy: required for any `contracts/` fix; include deploy tx, Etherscan verification, Slither/solhint/Hardhat output.
- Safe-ready payload: prepare only after Sepolia evidence and review.
- Mainnet execution: Safe multisig only; never single-signer.
- Post-deploy verification: bytecode/source match, contract addresses recorded under `docs/deployments/`, evidence committed.
- Off-chain reconciliation: update contract config/DynamoDB references only through normal deploy/config process.

## Managed-instance rollout plan

- Dry-run target: lab test slug first.
- Canary customer: one managed instance selected by Aron/operator after lab dry-run.
- Broader rollout: staged by managed instance; pause on first provisioning/update regression.
- Per-slug rollback: rollback/remediation runbook per affected slug; release verification remains mandatory.

## Release artifact plan

- GitHub Release: normal host release after fixed phases merge to `main`.
- Release notes: call out webhook authentication requirements, public API redaction/bounds, managed provisioning pinning, and any on-chain/contract updates.
- Managed-consumer impact: host consumes lesser/body artifacts; no unverified artifact deployment permitted.

## Rollback plan

- Lambda-version rollback: revert commit and redeploy through AppTheory/CDK; do not delete rollback versions.
- CDK stack rollback: revert commit + `theory app up --stage <stage>`; no destructive stack operations.
- On-chain rollback: not rollbackable; forward-fix via new on-chain transaction/Safe governance.
- Governance-rubric rollback: avoid unless verifier is proven wrong; any rollback is a governance event.
- Managed-update per-slug rollback: remediate per slug; do not bypass release verification.

## AGPL posture

- No proprietary blobs.
- New dependencies require AGPL-compatible license vetting.
- No vendored closed-source security tooling.

## Open questions

1. Which live managed instance, if any, should be the provisioning canary after lab dry-run?
2. Which public soul fields are intentionally public versus redacted by default for dispute/validation/channel evidence?
3. Which Telnyx/Migadu/SES signature/verdict headers are present in current live provider payloads?
4. Which contract findings require Solidity changes versus off-chain guardrails/not-applicable evidence?
5. Should this initiative cut one release after all phases, or multiple hotfix releases after P0/P1 and provisioning phases?
