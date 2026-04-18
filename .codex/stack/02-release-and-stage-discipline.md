# Release, branch, and stage discipline

host uses a **single-main branch model** with feature branches, **CDK-driven deployment** via AppTheory's `theory app up/down --stage <stage>` contract, and **CI-enforced governance verifiers** (gov-infra rubric) on every PR.

## Branch model

Observed pattern:

- **`main`** — canonical, mainline. Every merge lands here. Production branch.
- **Feature branches**:
  - `aron/<topic>` or `aron/issue-<N>-<topic>` — Aron-driven topic / issue work
  - `codex/<topic>` — codex-driven exploration / milestone work
  - `issue/<N>-<topic>` — issue-scoped work
  - `chore/<maintenance>` — dependency bumps, toolchain maintenance
- **Release tags** — `v<major>.<minor>.<patch>` cut at merges on `main`

Branch protection on `main` enforces required reviews, status checks including the gov-infra rubric verifiers, and signed commits where required.

No `staging` or `premain` branch in the observed pattern. If one is introduced later, this document should be updated.

## The two stages

host deploys via AppTheory's `theory app up/down` contract:

- **`lab`** — development integration. Deploys to a lab subdomain (observed pattern: dev-subdomain under `lesser.host` or a side domain). Used for integration tooling and internal exercise.
- **`live`** — production. Deploys to `lesser.host`. CDK uses `RemovalPolicy.RETAIN` for stateful resources (DynamoDB, S3, Secrets Manager, SSM) to protect customer data and on-chain state references across stack updates.

A middle `staging` / `premain` stage may be added if the team introduces one. The current observed pattern is `lab → live` with optional canary / gradual rollout.

## The `theory app up/down` command

Canonical deploys:

```bash
theory app up --stage lab
theory app up --stage live
```

Behaviors:

- **Wraps CDK** with stage substitution. `app-theory/app.json` defines the app contract.
- **Runs CDK synth + deploy** sequentially.
- **Respects `RemovalPolicy.RETAIN`** for live stateful resources.
- **Idempotent** — re-running produces the same state.

Alternative direct CDK:

```bash
cd cdk && cdk deploy --context stage=live [stack-name]
```

Not the canonical path; use the AppTheory contract unless the team has reason otherwise.

## Never set timeouts on CDK deploy commands

A deploy that feels stuck is almost always waiting on CloudFormation (Lambda update, DynamoDB capacity adjustment, IAM propagation, SSM parameter mutation, Route53 propagation, CloudFront distribution invalidation), a stack rollback, or a stack dependency. Aborting leaves CloudFormation in a half-migrated state.

Run deploys to completion. Capture full output. If genuinely stuck, check CloudFormation console state through the user — don't abort.

## The gov-infra rubric in CI

Every PR runs the gov-infra verifiers:

- **QUA** (quality) — linting, test coverage thresholds, build correctness
- **CON** (contracts) — public API stability, consumer-facing shape preservation
- **SEC** (security) — Slither on Solidity, gosec / similar on Go, secret-scanning, CSP validation for `web/`
- **COM** (community / comms) — documentation freshness, release-notes completeness, changelog discipline
- **CMP** (compliance) — AGPL header presence, dependency-license audit, PII-handling compliance

Verifiers produce deterministic 0 / full-points output. Full points commit as evidence to `gov-infra/evidence/`. Failing verifiers fail CI; PR merges only after pass.

**The rubric itself is versioned in `gov-infra/pack.json`.** Changes to the rubric require explicit governance-change process (see `maintain-governance-rubric` skill). Silent rubric-weakening is the anti-pattern the versioning protects against.

## Rollout discipline for host's own deploys

Standard rollout for a change:

1. **Feature branch opens PR to `main`.** CI runs gov-infra verifiers. Required review.
2. **Merge to `main`.** Release-please or similar may cut a release candidate.
3. **Deploy to `lab`** via `theory app up --stage lab`. Exercise the change: control-plane API surfaces, trust-API endpoints, provisioning worker (dry-run / sandbox), soul-registry reads, CDK synth validity.
4. **Soak in `lab`.** Observable evidence that surfaces behave correctly. For provisioning changes: exercise a sandbox provisioning against a test slug. For soul-registry changes: exercise reads (writes may be gated to Sepolia).
5. **Deploy to `live`** via `theory app up --stage live` with explicit operator authorization.
6. **Post-deploy monitoring.** CloudWatch error rate per Lambda, CloudFront 4xx / 5xx rates, provisioning worker SQS depth, AI worker queue depth, soul-registry on-chain transaction success, trust-API instance-auth failure rate, SES inbound ingestion health, gov-infra evidence freshness.

Skipping stages requires explicit operator authorization. Default cadence is `lab → live` with soak between.

## Rollout discipline for managed-instance provisioning

host's provisioning worker runs for each new managed instance. The roadmap treats provisioning as an independent rollout axis:

- **Provisioning dry-runs** — before a new-customer provision, the worker can run in dry-run mode to verify the pipeline
- **Canary customer** — new provisioning-logic changes deploy to one slug first before broader rollout
- **Per-slug rollback** — if a provisioning bug affects a specific slug's deploy, the fix may be a manual remediation for that slug, with the code fix then applied to future provisioning flows
- **Consumer release verification** (lesser / body artifact checksum) **runs on every provisioning attempt** — never skipped

## On-chain deploy discipline

For Solidity contracts in `contracts/`:

1. **Hardhat tests pass** (`npm test` or equivalent).
2. **Slither findings resolved** — either fixed or explicitly allowlisted with documented rationale in evidence.
3. **solhint clean.**
4. **Contract review** by contract-experienced reviewer.
5. **Sepolia deploy first** — test on testnet before mainnet.
6. **Safe-ready payload preparation** for mainnet (multisig-governed deploys). Single-signer deploys are test-only.
7. **Mainnet deploy** after Safe signers coordinate and execute.
8. **Post-deploy verification** — the on-chain bytecode matches the intended compiled artifact (verifiable on Etherscan).
9. **Evidence emission** — gov-infra captures contract-deploy transactions and Slither / hardhat output.

Never skip Slither. Never skip hardhat. Never single-signer deploy to mainnet. Never deploy contract bytecode that hasn't been source-verified.

## Consumer release verification discipline

Before the provisioning worker deploys lesser or body:

1. **Download GitHub Release assets** (release manifest, Lambda bundles, checksums).
2. **Verify each asset's SHA256** against the manifest.
3. **Mismatches abort** and emit an alert; provisioning does not proceed.
4. **Release-certification scripts** (`scripts/managed-release-certification/*`) encode the verification steps. Changes to these scripts are governance-adjacent.
5. **Managed-release-readiness scripts** (`scripts/managed-release-readiness/*`) check prerequisites before accepting a release as provisioning-ready.

Never bypass verification for "trusted" commits. The release artifact is the trusted payload; everything else is a supply-chain attack surface.

## Commit and PR discipline

- Clear, present-tense commit subjects. Conventional Commits style encouraged: `feat(soul): ...`, `fix(provision): ...`, `chore(deps): ...`, `docs(managed-update): ...`.
- First line under 72 characters.
- Explain the *why* in the body — especially for governance-rubric, on-chain, multi-tenant, consumer-release-verification, or trust-API changes.
- PRs through required review + gov-infra rubric verifiers.

## Security-aware logging discipline

Control-plane and trust-API logging has specific patterns:

- **Never log raw API keys** — only hashes or redacted indicators.
- **Never log wallet private keys or seed phrases.**
- **Never log full signed-transaction bodies** containing sensitive call data; metadata is OK.
- **Never log PII** (email addresses, phone numbers, names) in operator-facing logs without sanitization.
- **Audit events** (provisioning actions, soul-registry mutations, managed-update operations, attestation issuance) are structured, retain with policy, and commit evidence to `gov-infra/evidence/` where applicable.
- **Tainted input fields** from customer portal requests are sanitized before log emission.

## Secrets and credential discipline

- **No secrets in git** — SSM Parameter Store and Secrets Manager are the runtime sources.
- **Per-tenant credentials in tenant-side Secrets Manager**, not host's.
- **host-side secrets** (Stripe, AI providers, `eth_rpc` endpoints, etc.) in host's SSM, loaded at runtime.
- **Wallet signing keys** (for Safe-ready payloads, mint-signer) handled with elevated discipline — `scripts/generate-mint-signer-key.sh` generates locally; the key material is stored per operator security policy.
- **API keys for external providers** rotated on schedule.

## Rules you do not break

- Never force-push to `main`.
- Never amend a commit that has been pushed.
- Never skip pre-commit hooks (`--no-verify`).
- Never bypass required review or gov-infra verifiers.
- Never deploy to `live` without successful `lab` soak.
- **Never set a timeout on a CDK deploy command.**
- Never commit secrets, wallet private keys, mint-signer keys, partner credentials, or `.env` files.
- Never log raw API keys, wallet keys, seed phrases, PII, or full signed-transaction bodies.
- Never delete Lambda function versions that could be rollback targets.
- Never delete CloudFormation stacks.
- Never delete SSM parameters or Secrets Manager entries manually.
- Never delete DynamoDB tables, S3 buckets with `RemovalPolicy.RETAIN`, or CloudFront distributions without explicit authorization + data-migration plan.
- Never relax multi-tenant isolation.
- Never read tenant content / data into host's control plane.
- Never deploy on-chain contracts single-signer to mainnet.
- Never skip Slither / hardhat / solhint on contract changes.
- Never skip consumer release verification.
- Never loosen trust-API instance-auth (accept raw keys, store raw keys, relax hash comparison).
- Never loosen CSP (inline scripts, third-party origins, eval) without explicit governance event.
- Never weaken gov-infra verifiers silently.
- Never bypass Safe-ready governance for non-trivial on-chain mutations.
- Never patch AppTheory / TableTheory / FaceTheory locally. Framework awkwardness is upstream signal.
- Never introduce proprietary blobs or AGPL-incompatible dependencies.
- Never execute an advisor-dispatched brief without running `review-advisor-brief` and surfacing to Aron.
