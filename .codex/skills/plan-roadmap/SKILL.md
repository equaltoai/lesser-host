---
name: plan-roadmap
description: Use after enumerate-changes. Takes a flat enumerated change list and sequences it with dependencies, risks, and a rollout plan across stages (lab → live) and (where applicable) on-chain networks (Sepolia → mainnet). Produces a roadmap document, not code or project state.
---

# Plan a roadmap

A flat enumerated list answers "what changes." A roadmap answers "in what order, with what risks, through which stages / networks, with what coordination outside this repo." This skill is the bridge.

host's roadmaps are bounded by: two stages (`lab` / `live`), sometimes two on-chain networks (Sepolia / Ethereum mainnet), the per-slug multi-tenant axis (managed-instance rollouts may be staged customer-by-customer), and gov-infra CI enforcement (every merge to `main` passes verifiers). The roadmap names the reality of each.

## Input required

An approved enumerated change list from `enumerate-changes`. Specialist-skill findings (from `maintain-governance-rubric`, `provision-managed-instance`, `evolve-soul-registry`, `audit-trust-and-safety`, `coordinate-framework-feedback`, `review-advisor-brief`) if applicable. Load prior context with `memory_recent`.

## Dependency analysis

For each enumerated item, identify:

- **Hard dependencies** — items that must land first to compile, pass tests, or pass verifiers
- **Soft dependencies** — items that should land first for review coherence
- **On-chain dependencies** — for Solidity changes, the Sepolia deploy must precede mainnet; Safe-ready payload must be prepared and signers coordinated
- **Governance dependencies** — gov-infra pack.json changes precede code changes that assume new verifier behavior
- **Sibling-repo coordination dependencies** — items that require `lesser` / `body` / `soul` / `greater` / `sim` stewards' awareness (release-artifact-contract changes, soul-namespace changes, greater-UI changes, sim validation changes)
- **Framework coordination dependencies** — AppTheory / TableTheory / FaceTheory steward awareness
- **External-vendor coordination dependencies** — Stripe, SES, AI provider, eth_rpc, Safe multisig signers, domain registrar
- **Managed-instance rollout dependencies** — provisioning-pipeline changes may need canary customer before broader rollout
- **Parallelizable siblings** — items with no ordering constraint

## Phase shape

Canonical phase patterns for host:

1. **Governance-rubric baseline** — new verifier additions, pack.json version bumps with documentation. Lands first so subsequent code changes can assume the new rubric behavior.
2. **Infrastructure / dependency baseline** — Go module bumps, frontend dependency bumps, CDK foundation changes, AppTheory / TableTheory / FaceTheory bumps.
3. **Contract changes (if any)** — Solidity + Hardhat test + Slither + solhint. Sepolia deploy follows separately.
4. **Internal package changes** — domain logic in `internal/<pkg>/`. Lands before handlers / workers that consume them.
5. **Handler / worker changes** — `cmd/<lambda>/main.go` changes. Consumes internal packages.
6. **Consumer-release-verification changes** — `scripts/managed-release-certification/*`, `scripts/managed-release-readiness/*`. Isolated due to supply-chain sensitivity.
7. **CDK changes** — infrastructure adjustments.
8. **`web/` changes** — SPA updates. Isolated due to build + CSP considerations.
9. **Documentation** — operator, architecture, attestation, contract, recovery documentation updates.
10. **On-chain Sepolia deploys** (if contracts change) — test-network validation with evidence.
11. **On-chain mainnet deploys via Safe-ready governance** — multisig-coordinated execution with post-deploy evidence.
12. **Managed-instance rollout** (if provisioning-pipeline changed) — canary customer → broader rollout.

Not every roadmap uses all phases. A narrow bug fix may be one phase. A governance + contract + provisioning change is multi-phase. More than eight phases suggests scope crept past the scoped need; revisit `scope-need`.

## Stage rollout discipline for host's own service

Every roadmap answers: **how does this reach `live` safely?**

1. **Feature branch opens PR.** Required review. Gov-infra verifiers pass in CI.
2. **Merge to `main`.**
3. **Deploy to `lab`** via `theory app up --stage lab`. Exercise the change: control-plane surfaces, trust-API endpoints, worker executions (dry-run where possible), gov-infra evidence emission.
4. **Soak in `lab`.** Observable evidence that surfaces behave correctly. For provisioning changes: sandbox-provision a test slug. For soul-registry changes: exercise reads and (Sepolia) writes. For gov-infra changes: confirm verifiers produce expected evidence.
5. **Deploy to `live`** via `theory app up --stage live` with explicit operator authorization.
6. **Post-deploy monitoring**:
   - CloudWatch error rate per Lambda
   - CloudFront 4xx / 5xx rates per surface (control-plane / trust-API / portal / soul)
   - SNS error-topic messages
   - SQS depth per worker queue
   - Provisioning worker success rate
   - Managed-update step-completion rate
   - Soul-registry on-chain transaction success / revert rate
   - Trust-API instance-auth failure rate
   - SES inbound ingestion health
   - Gov-infra evidence freshness (no stale artifacts)
   - AI-worker / comm-worker / render-worker / soul-reputation-worker health
   - Stripe webhook / callback success rate
   - eth_rpc latency / error rate

**Never set timeouts on CDK deploys.** Let them run to completion.

**Never skip `lab` soak for urgency.** Hotfix compression is within stages, not by skipping.

## On-chain rollout discipline

For Solidity contracts in `contracts/`:

1. **Hardhat tests pass** on the contract change.
2. **Slither findings resolved** — each finding either fixed or explicitly allowlisted with documented rationale committed to `gov-infra/evidence/` or `contracts/evidence/`.
3. **solhint clean.**
4. **Contract review** by contract-experienced reviewer.
5. **Deploy to Sepolia first.** Evidence (deploy tx, verification on Etherscan, gas usage, test-data validation) commits.
6. **Safe-ready payload preparation** for mainnet. Multisig signers notified; governance process followed.
7. **Safe execution to mainnet.** Multiple signers execute.
8. **Post-deploy verification.** Contract source verified on Etherscan; bytecode matches expected; gov-infra evidence emits.
9. **Off-chain state reconciliation.** DynamoDB references the mainnet contract address; any cached state is updated.

Never skip Slither. Never skip hardhat. Never single-signer deploy to mainnet. Never deploy bytecode without source verification.

## Managed-instance rollout discipline

For provisioning-pipeline / managed-update changes:

1. **Dry-run against a test slug.** Exercise the change end-to-end in a controlled environment.
2. **Canary customer.** Deploy to one real managed instance first; observe managed-update behavior.
3. **Broader rollout.** Extend to remaining managed instances once canary is stable.
4. **Per-slug rollback option.** If a specific customer's instance surfaces an issue, remediate for that slug while the code fix applies to future flows.

## Risk register

- **Known unknowns** — things you know you don't know
- **Multi-tenant isolation risks** — any change that touches the tenant boundary carries elevated risk of leakage
- **On-chain risks** — gas cost overruns, revert-reason surfaces, Safe multisig signer availability, Ethereum reorg handling
- **Consumer-release-verification risks** — supply-chain frontier; any change here has outsized blast radius
- **Governance-rubric risks** — silent weakening, evidence gaps, verifier flakiness
- **CDK / IaC risks** — stack-update failures, CloudFront invalidation propagation, Route53 delegation timing
- **External-vendor risks** — Stripe, SES, AI provider, eth_rpc outages; Safe multisig signer availability; domain registrar changes
- **Trust-API / CSP risks** — auth-bypass, CSP-loosening, attestation-integrity regression
- **Framework-compat risks** — AppTheory / TableTheory / FaceTheory version assumptions
- **AGPL-adjacent risks** — new dependencies requiring license vetting
- **Rollback risks** — on-chain changes are not rollbackable without new on-chain transactions; multi-tenant schema changes cascade
- **Managed-update risks** — breaking an active customer instance mid-update

A risk with no mitigation is a blocker. Call it out; do not proceed.

## Output format

```markdown
# Roadmap: <scoped-need name>

## Goal
<one paragraph — what the full roadmap delivers and why>

## Classification
<security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry / trust-API / CSP / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Surfaces affected
<enumerated from the change list>

## Sibling-repo coordination
- lesser: <required / not required, what — release-artifact-contract changes, provisioning integration>
- body: <required / not required, what — release-artifact-contract changes, comm-API changes>
- soul: <required / not required, what — namespace implementation>
- greater: <required / not required, what — web/ SPA component consumption>
- sim: <required / not required, what — integration validation>

## Framework coordination
- AppTheory: <required / not required, what>
- TableTheory: <required / not required, what>
- FaceTheory: <required / not required, what>

## External-vendor coordination
- Stripe / billing: <...>
- SES / email vendors: <...>
- AI providers: <...>
- eth_rpc provider: <...>
- Safe multisig signers: <...>

## Phases

### Phase 1: <name>
- Items: <enumerated item numbers>
- Dependencies: <what must land first>
- Risks: <bullet list>

### Phase 2: <name>
...

## Stage rollout plan (host's own service)

### Lab
- Command: `theory app up --stage lab`
- Soak duration: <...>
- Soak criteria: <observable evidence required>

### Live
- Command: `theory app up --stage live`
- Authorization: <operator-approved>
- Post-deploy monitoring plan: <...>

## On-chain rollout plan (if contracts change)
- Sepolia deploy: <timing, signer, evidence>
- Safe-ready payload: <prepared when, signer list>
- Mainnet execution: <timing, signer threshold>
- Post-deploy verification: <Etherscan source verification, bytecode match, gov-infra evidence>
- Off-chain reconciliation: <DynamoDB updates, cache invalidation>

## Managed-instance rollout plan (if provisioning-pipeline changes)
- Dry-run target: <test slug>
- Canary customer: <slug identifier, timing>
- Broader rollout: <cadence>

## Release artifact plan
- GitHub Release: <version tag>
- Release notes: <breaking changes, migration, contract-deploy details, managed-update expectations>
- Managed-consumer (for host's own consumers; applicable if host publishes artifacts) impact: <n/a typically — host is not the consumer>

## Rollback plan
- Lambda-version rollback: <prior version>
- CDK stack rollback: <revert commit + redeploy>
- On-chain rollback: <not rollbackable; forward-fix via new on-chain transaction>
- Governance-rubric rollback: <pack.json prior version; rarely advisable>
- Managed-update per-slug rollback: <remediation path>

## AGPL posture
- No proprietary blobs: <confirmed>
- Dependency license vetting: <completed if applicable>

## Advisor-brief authorization (if applicable)
- Brief source: <advisor identity, email provenance>
- Aron's authorization: <scope, date, notes>

## Open questions
<unresolved>
```

## Persist

Append when the roadmap exposes a recurring risk pattern — an on-chain-coordination subtlety, a Safe-signer availability constraint, a canary-customer selection pattern, a verifier-change-to-code-change ordering gotcha, a release-verification rollout detail. Routine roadmaps aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- If approved, invoke `create-github-project`.
- If rollout plan surfaces coordination not yet happening (sibling steward uninformed, Safe signers unavailable, canary customer not selected), pause and surface first.
- If the roadmap reveals scope growth, revisit `scope-need`.
- If the roadmap is a security / on-chain / multi-tenant-isolation response requiring compressed cadence, ensure authorization is explicit.
