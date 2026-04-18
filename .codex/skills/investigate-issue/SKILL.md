---
name: investigate-issue
description: Use when a user reports a bug, regression, or unexpected behavior in host — control-plane API failure, trust-API instance-auth rejection, provisioning worker failure, soul-registry on-chain mismatch, managed-update step failure, release-verification mismatch, CSP violation, gov-infra verifier failure, CDK deploy issue. Runs before any fix is proposed. Produces an investigation note, not a patch.
---

# Investigate an issue

Investigation comes before implementation. host has specific investigation dimensions: 8 Lambdas with different roles (control-plane-api, trust-api, provision-worker, managed-update workers, AI workers, comm-worker, soul-reputation-worker), multi-tenant per-slug AWS accounts, on-chain smart contracts, consumer release artifacts, a governance rubric that fails CI, and a trust posture that third parties audit. A fix against a misunderstood symptom can breach tenant isolation, mint a wrong on-chain token, pass an unverified artifact through to a managed deploy, or silently weaken the rubric.

## Start with memory

Call `memory_recent` first. Scan for prior investigations in the same area — provisioning edge cases, on-chain coordination, multi-tenant isolation findings, trust-API auth subtleties, managed-update recovery patterns, governance-rubric evolution, consumer-release-verification gotchas. Managed-platform investigations are context-dense.

## Capture the claim precisely

Record the user's report literally, then extract:

- **Symptom** — what was observed, verbatim where possible
- **Surface** — control-plane API / trust API / soul registry / provisioning worker / managed update / AI worker / comm worker / email ingress / web portal / on-chain / CDK deploy / gov-infra CI
- **Lambda implicated** — `control-plane-api`, `trust-api`, `email-ingress`, `provision-worker`, `render-worker`, `ai-worker`, `comm-worker`, `soul-reputation-worker`
- **Tenant context** (if tenant-specific) — slug, tenant-side AWS account ID (not the raw ID; a redacted identifier), tenant's lesser / body versions, instance-key hash (identifier only, not the raw key)
- **On-chain context** (if on-chain) — network (Sepolia / mainnet), contract address, transaction hash (if available), signer
- **Customer context** (if portal-related) — wallet address (redacted where prudent), plan, instance state
- **Release context** (if provisioning / managed-update) — lesser / body version being deployed, release-manifest URL, checksum comparison result
- **Gov-infra context** (if rubric-related) — which verifier, which category (QUA / CON / SEC / COM / CMP), current pack.json version
- **Expected vs actual**
- **Reproduction path** — request / payload / transaction shape
- **Recent deploys** — host deploy, tenant-side deploys, contract deploys, SSM changes, `gov-infra/pack.json` changes

## Ground the investigation

Your first structural questions are always:

1. **Is this a multi-tenant isolation issue?** If the symptom suggests cross-tenant data visibility, shared-credential leakage, or an instance-auth bypass — elevate. Tenant-isolation-suspected symptoms get elevated handling.
2. **Is this an on-chain issue?** If the symptom involves a contract call, a mint, a transfer, a Safe-ready payload execution, or on-chain state that disagrees with off-chain state — elevate. On-chain actions are irreversible; treat carefully.
3. **Is this a governance-rubric issue?** If a verifier failed, the rubric shape is wrong, or evidence is missing, route through `maintain-governance-rubric`.
4. **Is this a provisioning / managed-update issue?** Route through `provision-managed-instance`.
5. **Is this a soul-registry issue?** (off-chain state + on-chain references + Safe-ready governance) route through `evolve-soul-registry`.
6. **Is this a trust-API / CSP / instance-auth issue?** Route through `audit-trust-and-safety`.
7. **Is this a consumer-release-verification issue?** (checksum mismatch, release-certification script failure) route through `provision-managed-instance` — release verification is part of the provisioning pipeline.
8. **Is the symptom in host, in a tenant instance, in a sibling repo (lesser / body / soul / greater), in a Theory Cloud framework, on-chain, or in an external vendor (Stripe, AI provider, eth_rpc)?** Confirm before accepting as host's issue.

## Evidence before hypotheses

Gather before theorizing:

- `git log` on the affected package (`cmd/<lambda>/`, `internal/<package>/`, `cdk/`, `contracts/`, `web/`) since the last known-good state
- `git blame` on the specific lines the reproduction implicates
- The affected Lambda's version and deploy timestamp
- CloudWatch logs for the affected Lambda and relevant request / correlation IDs (through the user)
- SNS error-topic messages
- CloudFront access logs (for control-plane / trust-API / portal surfaces)
- SQS dead-letter queue depth for workers (provisioning, managed-update, AI, comm, soul-reputation)
- For on-chain: Etherscan transaction state, contract event logs, gas usage, revert reason
- For provisioning: CodeBuild run logs, deploy receipts, per-slug AWS account status
- For managed-update: step-level status, error recovery attempts
- For gov-infra: verifier output, pack.json version, evidence artifacts, CI run logs
- For tenant-side symptoms: the tenant's instance-auth key hash (not raw), the tenant's lesser / body version, the tenant's trust-API call history
- `query_knowledge` for cross-repo context — AppTheory / TableTheory patterns, sibling equaltoai repos, Safe / Ethereum integration patterns

If `memory_recent` or `query_knowledge` returns an auth error, stop — managed-platform regressions need context continuity.

## The specialist-routing question

Every investigation answers: **which specialist skill, if any, should handle this?**

- **Governance rubric, verifier, evidence** → `maintain-governance-rubric`
- **Provisioning, managed-update, consumer release verification** → `provision-managed-instance`
- **Soul registry (on-chain + off-chain + governance payloads)** → `evolve-soul-registry`
- **Trust API, instance auth, attestations, CSP** → `audit-trust-and-safety`
- **Framework awkwardness (AppTheory, TableTheory, FaceTheory)** → `coordinate-framework-feedback`
- **Advisor-originated brief** → `review-advisor-brief`
- **None** — standard `scope-need` → `enumerate-changes` → `plan-roadmap` → `implement-milestone` → CDK deploy

## Rank hypotheses by evidence

List theories in descending order of support:

1. **Hypothesis** — one sentence
2. **Evidence for** — commits, logs, on-chain state, verifier output, state comparison
3. **Evidence against** — what would be true if this were wrong
4. **Verification step** — the cheapest test to prove or disprove it

## Output: the investigation note

```markdown
## Reported symptom
<verbatim>

## Dimensions
- Surface: <control-plane / trust-API / soul / provisioning / managed-update / AI / comm / email ingress / web / on-chain / CDK / gov-infra>
- Lambda: <...>
- Tenant context (if any): <slug, account identifier, lesser/body versions, instance-key hash identifier>
- On-chain context (if any): <network, contract address, tx hash, signer>
- Release context (if any): <version, manifest URL, checksum comparison>
- Gov-infra context (if any): <verifier, category, pack.json version>
- Recent deploys: <host, tenant, contract, SSM, pack.json>

## Specialist elevation check
<normal / elevate to maintain-governance-rubric / provision-managed-instance / evolve-soul-registry / audit-trust-and-safety / coordinate-framework-feedback / review-advisor-brief>

## What is definitely true
<verified facts — logs, on-chain state, state comparisons, verifier output>

## Fix-locus verdict
<fix here (host) / fix upstream (AppTheory, TableTheory, FaceTheory) / fix in sibling (lesser, body, soul, greater) / fix in tenant instance / fix on-chain via Safe-ready governance / fix in external vendor / fix in gov-infra>

## Hypotheses (ranked)
1. <hypothesis> — evidence: <...>
2. <...>

## Verification step
<the one thing to run next>

## Proposed next skill
<investigate-issue again / fix directly / scope-need / maintain-governance-rubric / provision-managed-instance / evolve-soul-registry / audit-trust-and-safety / coordinate-framework-feedback / review-advisor-brief / none — cross-repo report>
```

## Persist

Append only when the investigation surfaces something worth remembering — a provisioning edge case with a specific tenant-side constraint, an on-chain coordination subtlety, a trust-API auth timing issue, a managed-update recovery pattern, a governance-rubric evolution finding, a consumer-release-verification gotcha, a framework awkwardness worth reporting upstream. Routine "typo" findings aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff rules

- **Multi-tenant-isolation-suspected** — elevate to user immediately; route through specialist skill as applicable.
- **On-chain issue** — `evolve-soul-registry`.
- **Governance rubric issue** — `maintain-governance-rubric`.
- **Provisioning / managed-update / consumer-release-verification** — `provision-managed-instance`.
- **Trust API / CSP / instance-auth** — `audit-trust-and-safety`.
- **Framework awkwardness** — `coordinate-framework-feedback`.
- **Advisor brief** — `review-advisor-brief`.
- **Small, contained fix** — standard `scope-need` → `enumerate-changes` → `implement-milestone` → CDK deploy.
- **Root cause in sibling / framework / tenant / external vendor** — report cleanly; do not cross the boundary.
