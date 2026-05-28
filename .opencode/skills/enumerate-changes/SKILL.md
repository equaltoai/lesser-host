---
name: enumerate-changes
description: Use after scope-need and relevant specialist skills approve work. Takes the scoped-need document and produces a flat, ordered list of discrete changes required. Each change is scoped to be a single commit.
---

# Enumerate changes

A scoped need describes *what* is being delivered. An enumerated change list describes *what must move in the repo*. This skill is the transformation.

host's change lists vary: a narrow bug fix might be two to three commits; a new verifier addition might be four to six; a provisioning-flow refinement with managed-update recovery might be eight to twelve; a soul-registry on-chain contract addition with Safe-ready governance coordination is larger. The single-commit rule holds regardless.

## Input required

An approved scoped-need document from `scope-need`. Specialist-skill findings (from `maintain-governance-rubric`, `provision-managed-instance`, `evolve-soul-registry`, `audit-trust-and-safety`, `coordinate-framework-feedback`, `review-advisor-brief`) if applicable. Load prior context with `memory_recent`.

## The walk

Walk the scoped need against every surface of host:

1. **`cmd/<lambda>/`** — Lambda entrypoints (control-plane-api, trust-api, email-ingress, provision-worker, render-worker, ai-worker, comm-worker, soul-reputation-worker)
2. **`internal/controlplane/`** — operators, portal, provisioning, billing, tips
3. **`internal/trust/`** — attestations, previews, instance auth
4. **`internal/store/`** — TableTheory models (DynamoDB)
5. **`internal/secrets/`** — SSM Parameter Store reads
6. **`internal/soul*/`** — soul-registry subsystems (registration, avatars, reputation, local identity resolution, search)
7. **`internal/<other-domain-package>/`** — ~33 total domain packages
8. **`cdk/`** — AWS CDK (TypeScript)
9. **`web/`** — Svelte 5 SPA
10. **`contracts/`** — Solidity + Hardhat
11. **`docs/`** — ~40 markdown files (managed-instance-provisioning, attestations, lesser-release-contract, lesser-body-release-contract, adr/, deployments/, contracts/, roadmaps, recovery runbooks)
12. **`scripts/`** — `build_release_assets.sh`, `generate-mint-signer-key.sh`, `managed-release-certification/*`, `managed-release-readiness/*`, soul-backfill scripts
13. **`gov-infra/`** — verifiers, evidence, pack.json, planning (threat model, controls matrix, etc.), AGENTS.md, README.md
14. **`go.mod` / `go.sum`** — Go dependency changes
15. **`web/package.json` / `web/pnpm-lock.yaml`** (or equivalent) — frontend dependencies
16. **`contracts/package.json`** — Hardhat / Solidity dependencies
17. **`app-theory/app.json`** — AppTheory deployment contract
18. **`AGENTS.md`** — repository guidelines. Rarely touched; governance-level.
19. **`README.md`** — top-level overview.
20. **`CONTRIBUTING.md`** — contributor quickstart.

A change that touches none of these isn't really a change.

## The ordering rules

1. **Test-first for bug fixes.** Regression test first (fails against current code), then fix. Especially important for multi-tenant-isolation, trust-API-auth, on-chain-adjacent, provisioning, managed-update bugs.
2. **Governance-rubric additions land together.** A new verifier + its evidence policy + its documentation land in one commit where practical.
3. **Solidity contract changes land separately.** Each contract change is its own commit with Slither / hardhat / solhint run results cited in the commit body.
4. **On-chain deploy steps land in isolated commits.** Sepolia deploy → mainnet Safe-ready payload → mainnet execution; each as separate events, each committed with its evidence.
5. **Consumer-release-verification changes** (scripts / certification / readiness) land in isolated commits given their supply-chain sensitivity.
6. **CDK changes land separately from Go code.** CDK affects every deploy; isolation matters for bisect and rollback.
7. **`web/` changes land separately.** The SPA has its own build and CSP story.
8. **SSM parameter / Secrets Manager access changes** land with the Go code that uses them.
9. **`gov-infra/pack.json` changes** are governance events in their own commits with documentation explaining the rubric shift.
10. **Dependency bumps land in isolated commits** for bisect clarity.
11. **Framework-consumption changes** — idiomatic consumption of new framework version lands with the bump; framework awkwardness routes to `coordinate-framework-feedback`.
12. **Documentation rides with the behavior it describes** — provisioning-doc updates ride with provisioning-code changes, attestation-doc with trust-API changes, contract-docs with contract changes.

## The mission-scope rule

Every enumerated item must answer: **is this host-mission work, or scope growth outside?**

- **In-mission**: governance, multi-tenant, on-chain, provisioning, managed-update, trust-API, soul-registry, consumer-release-verification, operator-portal, comm-routing, AGPL, operational-reliability, framework-feedback, bug-fix, test-coverage, docs.
- **Scope growth (refuse)**: tenant-side content / user / moderation, general identity provider for non-lesser, general payments beyond tipping + host billing, cross-tenant queries, reading tenant content into host's plane, local framework patches.

If any item is scope growth, stop and revisit `scope-need`.

## The governance-rubric rule

Every enumerated item must also answer: **does this touch the gov-infra rubric (verifiers, evidence policy, pack.json)?**

- **No** — default.
- **Yes — additive (new verifier, new evidence requirement)** — proceed with `maintain-governance-rubric` findings referenced.
- **Yes — modifying or loosening** — refuse unless explicitly authorized with governance-change process documented; `maintain-governance-rubric` walk is mandatory.

## The multi-tenant-isolation rule

Every enumerated item must also answer: **does this touch the multi-tenant boundary?**

- **No** — default (preserves tenant isolation).
- **Yes — change traverses tenant boundary** — refuse unless explicitly authorized with documented reasoning and elevated review.

## The on-chain rule

Every enumerated item must also answer: **does this touch Solidity contracts, on-chain-reaching Go code, or Safe-ready governance payloads?**

- **No** — default.
- **Yes — Solidity** — Slither + solhint + hardhat test evidence referenced; Sepolia deploy precedes mainnet; Safe-ready for non-trivial mutations.
- **Yes — on-chain Go code only** — idempotency, dry-run mode, explicit confirmation patterns preserved.

## The consumer-release-verification rule

Every enumerated item must also answer: **does this touch the release-verification pipeline (scripts, certification, readiness checks)?**

- **No** — default.
- **Yes** — elevated scrutiny because this is a supply-chain frontier; coordinate with `provision-managed-instance` walk.

## The trust-API / CSP / instance-auth rule

Every enumerated item must also answer: **does this touch the trust API, instance-auth, attestations, or CSP?**

- **No** — default.
- **Yes — tightening or preserving** — proceed with `audit-trust-and-safety` findings.
- **Yes — loosening** — refuse unless explicitly authorized with documented reasoning and elevated review.

## The framework-consumption rule

Every enumerated item must also answer: **does this consume AppTheory / TableTheory / FaceTheory idiomatically?**

- **Idiomatic** — proceed.
- **Workaround** — stop. Route through `coordinate-framework-feedback`.

## The single-commit rule

Each enumerated item fits in one commit:

- One logical intent
- `go build ./...` succeeds
- `go test ./...` passes
- `go vet ./...` passes
- `gofmt -l .` empty
- For CDK: `cdk synth --context stage=lab` succeeds
- For `web/`: lint + build clean; CSP validation passes
- For `contracts/`: `hardhat test` passes; Slither clean; solhint clean
- For `gov-infra/`: verifiers pass (including this commit's scope); evidence emits
- No commit depends on a later item to compile or pass tests / verifiers

## Output format

```markdown
### N. <imperative title>

- **Paths**: <files or directories touched>
- **Surface**: <cmd / internal/<pkg> / cdk / web / contracts / gov-infra / scripts / docs / deps>
- **Classification**: <security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry / trust-API / CSP / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>
- **Governance-rubric impact**: <none / additive / modifies — refuse if loosens silently>
- **Multi-tenant-isolation impact**: <none — default; traverses — refuse without authorization>
- **On-chain impact**: <none / off-chain only / Solidity — Slither + hardhat + solhint run / Safe-ready governance required>
- **Trust-API / CSP / instance-auth impact**: <none / preserves / tightens — refuse if loosens>
- **Consumer-release-verification impact**: <none / touches verification pipeline — elevated scrutiny>
- **Framework consumption**: <idiomatic / reported upstream>
- **Acceptance**: <one sentence: what makes this commit done>
- **Validation**: <`go test ./...`, `go vet ./...`, `gofmt -l .`, `cdk synth`, `hardhat test`, Slither, solhint, gov-infra verifiers, web build + CSP check>
- **Conventional Commit subject**: `<type(scope): subject>`
```

## Self-check before handing off

- [ ] Every item is in-mission
- [ ] No item weakens governance rubric silently
- [ ] No item traverses multi-tenant isolation
- [ ] On-chain items cite Slither / hardhat / solhint status + Safe-ready path where required
- [ ] Consumer-release-verification items flagged for elevated scrutiny
- [ ] No item loosens trust-API instance-auth or CSP
- [ ] Framework awkwardness routed to `coordinate-framework-feedback`, not patched locally
- [ ] Bug fixes follow test-first ordering
- [ ] Solidity commits isolated
- [ ] CDK commits isolated from Go code
- [ ] `gov-infra/pack.json` changes in isolated commits with documentation
- [ ] `web/` changes isolated; CSP validation runs
- [ ] Documentation rides with behavior changes (provisioning, attestation, contracts)
- [ ] Every item has test / synth / verifier validation
- [ ] No item requires a future item to compile
- [ ] No hardcoded secrets, wallet keys, raw instance keys
- [ ] No raw key / seed phrase / full signed-transaction / PII logging
- [ ] No deletion of Lambda versions, DynamoDB tables, stateful S3, Route53 zones, SSM, Secrets Manager entries
- [ ] No AGPL-incompatible dependencies or proprietary blobs
- [ ] Full list satisfies the scoped need's success criteria

## Persist

Append only if enumeration surfaces something unusual — a verifier interaction subtlety, an on-chain ordering gotcha, a provisioning coordination detail, a CSP edge case, a framework-consumption pattern worth reporting. Routine enumerations aren't memory material. Five meaningful entries beat fifty log-shaped ones.

## Handoff

Invoke `plan-roadmap` to sequence the flat list into phases and identify the rollout plan across stages and (where applicable) on-chain networks.
