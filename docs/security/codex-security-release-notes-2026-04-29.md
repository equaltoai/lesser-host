# Release notes — Codex Security remediation 2026-04-29

These notes summarize the M0-M4.5 security hardening line for operators preparing a `lesser.host` lab/live rollout.
They are not a deploy authorization by themselves.

## Security fixes

### Public ingress, comms, mailbox, and privacy

- Comm provider webhooks are authenticated and replay/idempotency checked before state changes or billing effects.
- SES-derived sender identity is gated on authentication verdicts before enrichment.
- Mailbox content writes are reserved/immutable and previews are bounded.
- Email, phone, ENS, and channel index writes enforce ownership/uniqueness before public mappings are updated.
- Public dispute/validation/channel/search surfaces are redacted, bounded, or auth-gated where required.

### Soul registry and on-chain operation integrity

- Soul operation execution recording is bound to expected transaction target, calldata/value/effects, and agent-specific
  state transitions before status or side effects are recorded.
- Lifecycle, continuity, registration, and relationship signatures/canonicalization have replay and history-integrity
  guardrails.
- Public relationship/endorsement merges are bounded and cursor-safe.
- Solidity contract fixes were tested and linted; any bytecode rollout still requires Sepolia evidence before mainnet
  Safe-ready execution.

### Managed provisioning, managed updates, and release verification

- Managed provisioning/update release versions must be pinned or explicitly resolved; no silent `latest` default is used.
- Release `git_sha` values and compatibility contracts are validated before source checkout or managed update execution.
- lesser-body template certification/readiness evidence fails closed when evidence mismatches or deployment fails.
- Managed-release canary routing is restricted to approved HTTPS host origins.
- Instance-key secret names are stage-isolated, and trust verification remains authenticated.
- Managed-update metadata handling, receipt semantics, and tip config validation were hardened.

### Public read scalability, CSP/web, governance, and reliability

- Public soul reads/search/version/ENS paths are bounded, cached, paginated, or rate/cost aware.
- Markdown rendering remains mandatory-sanitized under strict single-origin CSP; no `unsafe-inline`, `unsafe-eval`, or
  third-party script origin was added.
- CMP-4 governance verification now checks semantics, not just keyword presence.
- Trust-route runtime/IAM gaps and TableTheory soul model registration gaps were fixed.
- AppTheory deploy command interpolation was hardened without patching AppTheory locally.

### Lesser M9 provisioning alignment

- Host emits Lesser M9 `lesser.init_admin_consent.v1` structured JSON for managed `init-admin` consent.
- The exact signed consent bytes are preserved from browser wallet signing through `ProvisionJob`, CodeBuild
  `CONSENT_MESSAGE_B64`, `provision.json`, and `lesser init-admin`.
- Host will not consume/certify Lesser M9 until exact published release assets pass checksum verification,
  managed-release certification/readiness, lab dry-run, and canary evidence.

## Operator action required

1. Review `docs/security/codex-security-m5-evidence-2026-04-29.md` and the merged PR validation sections.
2. Deploy to lab via `theory app up --stage lab` without adding a timeout.
3. Complete the lab soak checklist in `docs/security/codex-security-m5-rollout-checklist-2026-04-29.md`.
4. Capture Sepolia evidence for contract bytecode changes before any mainnet Safe-ready execution.
5. Select a managed-instance canary for provisioning/update changes.
6. Seek explicit live deploy authorization after lab soak.

## Backward compatibility and customer impact

- Public APIs may return redacted/bounded responses where prior responses exposed dispute/validation/channel data or
  performed unbounded reads.
- Provider webhook calls without valid authentication or replay freshness are rejected.
- Managed provisioning and managed updates now fail closed on unpinned, incompatible, or checksum-mismatched releases.
- Portal/self-service paths enforce stricter policy around soul provisioning and update versions.
- Existing Lesser releases remain usable when they pass the managed compatibility/release verification contract; Lesser M9
  adds the structured `init-admin` consent expectation described in managed provisioning docs.

## Rollback notes

- Host service rollback is by reverting the offending commit and redeploying through AppTheory/CDK. Do not delete Lambda
  versions, retained state resources, SSM parameters, Secrets Manager values, Route53 zones, or CloudFormation stacks.
- On-chain rollback is not available; forward-fix through Sepolia-tested and Safe-ready governance paths.
- Managed-instance rollback/remediation is per slug and must not bypass release checksum verification.
