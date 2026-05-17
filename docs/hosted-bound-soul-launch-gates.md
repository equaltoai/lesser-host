# Hosted bound souls + x402 launch gates

`public_launch_status: blocked`

This document is the Host-side launch gate register for public hosted-bound-soul and x402 access surfaces. It records the
product/legal/privacy disclosures that must exist before `lesser.host` presents hosted bound souls or public paid x402
invocation as a public launch surface.

This is **not** a deployment, provisioning, migration, real on-chain operation, or public-launch approval. Parent issue
`lesser-host#311` remains the launch coordination record; this Host-side gate can be complete while public launch remains
blocked for downstream client work, counsel/product review, or explicit operator authorization.

## Authority and status

- Host M4.1 scope: terminology review, x402 payment/refund/failure documentation, and comms privacy/consent language.
- Public launch remains blocked until the parent milestone explicitly records that every child issue is complete or
  explicitly deferred with owner/rationale.
- Changing `public_launch_status` away from `blocked` requires a separate, explicit launch-authorization change. A docs
  PR that records these gates must not enable code paths, feature flags, deploys, provisioning, migrations, or on-chain
  actions.

## Launch gate register

| Gate | Host-side checkoff | Required posture |
|------|--------------------|------------------|
| HBS-LG-01 hosted/offchain terminology | Checked off by this document and `docs/soul-surface.md` references | `hosted_offchain` and `immutable_onchain` are anchor assurance states, not separate agent namespaces or capability tiers. |
| HBS-LG-02 x402 payment obligations and failure handling | Checked off by this document and `docs/pricing-and-services.md` references | Public paid access is a scoped off-chain invocation grant with disclosed payment, refund, failure, idempotency, and evidence-minimization boundaries. |
| HBS-LG-03 comms privacy and consent | Checked off by this document and comm mailbox docs references | Email, phone, SMS, and voice capabilities require clear consent, entitlement, privacy, retention, and opt-out/revocation language before public exposure. |
| HBS-LG-04 public launch block | Preserved as blocked | Public launch remains blocked until Pilot/Arch/Aron coordination records every launch gate as complete or explicitly deferred. |

## HBS-LG-01 — hosted/offchain terminology

Use the policy vocabulary from `docs/soul-surface.md` exactly:

- `hosted_offchain`: the soul exists as host-managed off-chain registry state and registration artifacts.
- `immutable_onchain`: a mint execution has been recorded on-chain and reconciled into host state.
- `anchor_assurance.capability_gate: false`: anchor assurance is public trust/display metadata, not default capability
  authority.
- `hosted_bound_soul`: the agent is operationally bound to a host-managed lesser/body instance through host's domain and
  instance-key authorization checks.

Required product/client wording:

- Hosted/off-chain souls are valid hosted bound souls when policy allows the requested capability.
- Promotion to `immutable_onchain` adds assurance evidence; it does not create a second namespace, rotate `agent_id`, or
  silently change capability/access policy.
- On-chain anchoring is **not a prerequisite** for basic hosted-bound-soul communication or public x402 grant issuance
  when the explicit policy permits those operations.

Forbidden wording:

- Do not call `hosted_offchain` souls incomplete, untrusted by default, pre-souls, shadow identities, or capability-
  downgraded identities.
- Do not imply that `immutable_onchain` is required before a hosted bound soul can send email or use a policy-allowed
  scoped x402 invocation grant.
- Do not describe anchor assurance as authorization. Capability authorization remains controlled by capability/access
  policy.

## HBS-LG-02 — x402 payment obligations, refunds, and failures

Public x402 grant issuance is a paid-access boundary, not a control-plane login flow. A public caller may request
`POST /api/v1/soul/x402/grants` only for a soul whose `caller-access-payment/v1` policy allows
`publicPaidCaller.access=grantable`.

Required public-client disclosure before launch:

- **Payment obligations:** the caller is responsible for the requested x402 payment amount, network, asset/currency, and
  facilitator route presented by the public client. Host records the grant as off-chain policy state and stores only
  minimized hashes of sensitive caller/payment evidence.
- **Scope of access:** a successful issue returns a one-time raw `grantToken` for the bound `agentId`, capability, tool,
  resource, request hash, expiry, and max usage. It does not grant principal/operator authority, session authority,
  wallet authority, tenant-data access, mailbox browsing, or on-chain authority.
- **Evidence boundary:** host may accept caller-supplied facilitator/payment evidence and records it as hashes. Product
  copy must not represent caller-provided facilitator data as host-verified settlement finality unless a future, reviewed
  facilitator-verification integration provides that guarantee.
- **Idempotency:** the caller must retain the first `grantToken`; idempotent replays return metadata with
  `tokenReturned=false`.
- **Failure handling:** invalid payment shape, policy denial, expired grants, exhausted grants, scope mismatch,
  idempotency conflicts, or authenticated-instance rejection produce no invocation authority. Generic
  `x402.grant_unavailable` failures intentionally avoid disclosing private soul state.
- **Refund handling:** before public launch, the product surface must name the operator-approved refund/support route for
  captured payments that do not result in a usable grant. Until an automated refund integration exists, client copy must
  not promise automatic refunds, instant reversals, or host custody of returned funds. Support workflows must reconcile
  against payment evidence hashes or facilitator records without exposing raw payment evidence publicly.

Operational requirements:

- Do not log raw caller subjects, raw payment evidence, raw payment IDs, raw grant tokens, wallet material, tenant data,
  private comms reachability, or unresolved security details.
- Keep grant lifetime and max-usage limits visible to clients so paid access does not look unlimited.
- Keep public issue failures generic enough that an unauthenticated caller cannot enumerate private comms reachability,
  payment evidence, tenant data, wallet material, provider configuration, or whether a private soul exists.

## HBS-LG-03 — email, phone, SMS, and voice privacy/consent

Hosted-bound-soul communication capabilities are not public contact directories. They are exact-agent capability surfaces
with instance-key authorization and policy gates.

Required consent language before public client exposure:

- **Email:** email is available only when an active, verified, provisioned email channel exists and policy permits it.
  Client copy must say that messages may transit host-managed mail infrastructure and provider systems, and that bounded
  mailbox metadata/content is retained under host's retention and audit policies.
- **Phone/SMS/voice:** phone, SMS, and voice remain denied until a paid or provisioned entitlement exists and the relevant
  `smsAllowed` / `voiceAllowed` policy flag is true. Client copy must require explicit opt-in by the responsible
  operator/principal for phone-number provisioning and must give recipients/provider workflows a revocation, opt-out, or
  support path appropriate to the channel before public use.
- **Private reachability:** public unauthenticated reads must not expose raw email addresses, phone numbers, provider
  routing details, verification status, mailbox contents, private comms reachability, or policy fields.
- **Content privacy:** mailbox list views expose redacted previews/metadata only. Explicit content reads require the
  authorized mailbox content endpoint and emit access-audit evidence.
- **Retention and audit:** full content is bounded by retention, encryption, and access audit. Host must not convert
  mailbox content into permanent semantic memory or cross-tenant analytics.
- **Provider disclosure:** public UX must disclose that email, SMS, phone, and voice delivery may involve third-party
  communications providers and that delivery failures, filtering, carrier rules, or provider outages can prevent
  delivery without exposing private provider diagnostics to public callers.

## HBS-LG-04 — public launch block

Public launch is blocked until all of the following are true and recorded on the parent milestone:

- [ ] Host M4.1 disclosure gates are reviewed and accepted.
- [ ] Sim/client launch UX surfaces the same terminology, payment, refund/failure, and consent boundaries without adding
      principal/operator authority to public x402 callers.
- [ ] Counsel/product review signs off on current public copy and refund/support workflow.
- [ ] Arch/Pilot/Aron coordination records that child issues are complete or explicitly deferred with owner/rationale.
- [ ] No unresolved security issue requires withholding private comms reachability, payment evidence, tenant data, wallet
      material, or implementation details from public comments/docs.
- [ ] No deploy, provisioning, migration, or on-chain operation is bundled into the launch-gate documentation PR.

Until those checkboxes are explicitly completed in the coordinating milestone, public clients should treat hosted bound
souls and x402 access as pre-launch or gated surfaces even if Host's backend contract supports the underlying operations.
