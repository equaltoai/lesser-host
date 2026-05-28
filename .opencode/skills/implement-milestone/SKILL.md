---
name: implement-milestone
description: Use to execute a single milestone (or GitHub Project phase) of work — feature branch off main, commits per enumerated task, PR review with gov-infra verifiers, merge to main. Runs one milestone at a time. Deploys themselves (CDK lab/live, on-chain Sepolia/mainnet, managed-instance rollout) are handled separately.
---

# Implement a milestone

This skill moves host work through code, review (with CI-enforced gov-infra verifiers), and merge to `main`. host uses a single-main branch model with feature branches. Once merged, stage deploys (`theory app up --stage <stage>`), on-chain deploys (Sepolia → Safe-ready mainnet), and managed-instance rollouts follow per the roadmap.

## Hard preconditions

Do not start without all of the following:

- **A specific milestone named**, from `plan-roadmap` or a GitHub Project phase
- **Clean working tree on `main`** at a known-green commit
- **MCP tools healthy.** Call `memory_recent` first.
- **`go test ./...` passes** on `main`
- **`go vet ./...` passes**
- **`gofmt -l .`** returns empty
- **`cdk synth --context stage=lab`** succeeds if the milestone touches CDK
- **`hardhat test`** passes in `contracts/` if the milestone touches Solidity
- **Slither + solhint** clean if Solidity touched
- **`web/` build + CSP validation** pass if `web/` touched
- **gov-infra verifiers pass** (current evidence state aligned with pack.json)
- **Enumerated tasks are in-mission** — not scope growth
- **Specialist walks complete** for governance / provisioning / soul / trust / framework / advisor-brief work
- **Advisor-dispatched milestones** have Aron's authorization from `review-advisor-brief` recorded

If any precondition fails, stop and surface.

## Branch and PR setup

One feature branch per milestone. One PR per milestone. One commit per task.

- **Branch name**: observed patterns — `aron/issue-<N>-<topic>`, `codex/<topic>`, `issue/<N>-<topic>`, `chore/<maintenance>`
- **Branched from**: `main` at a known-green commit
- **PR target**: `main`
- **PR title**: clear, Conventional Commits style encouraged
- **Open PR as draft**

PR description template:

```markdown
## Milestone
<short-name> — <goal from roadmap or project README>

## Classification
<security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry / trust-API / CSP / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Surfaces affected
<enumerated>

## Specialist walks referenced
- Governance rubric: <...>
- Provisioning / managed-update / release verification: <...>
- Soul registry: <...>
- Trust API / CSP / instance-auth: <...>
- Framework: <idiomatic / reported upstream>

## Multi-tenant isolation impact
<none / elevated scrutiny>

## On-chain impact
<none / Sepolia / mainnet Safe-ready>

## Consumer impact
<managed operators / customers / sibling repos / on-chain actors / external vendors>

## Tasks
- [ ] <issue 1 title>
- [ ] <issue 2 title>

## Validation
- `go test ./...`
- `go vet ./...`
- `gofmt -l .` (empty)
- `cdk synth --context stage=lab` (if CDK changed)
- `hardhat test` (if Solidity changed)
- Slither + solhint (if Solidity changed)
- `web/` build + CSP validation (if web/ changed)
- gov-infra verifiers (CI)

## Stage rollout plan (handoff after merge)
- [ ] Merged to main
- [ ] Deployed to lab
- [ ] Lab soak complete
- [ ] Deployed to live
- [ ] Post-deploy monitoring verified
- [ ] (If on-chain) Sepolia deploy + Etherscan verification
- [ ] (If on-chain) Safe-ready payload prepared
- [ ] (If on-chain) Mainnet multisig execution
- [ ] (If provisioning) Canary customer stable
- [ ] (If provisioning) Broader rollout

## Cross-repo coordination
<required / none>

## Advisor-brief authorization (if applicable)
<summary from review-advisor-brief>
```

## The per-task loop

For each issue in order:

1. **Read the issue.** Confirm acceptance and planned commit subject. If drifted, stop.
2. **`memory_recent`** — refresh context.
3. **For bug fixes: add regression test first.** Especially for multi-tenant, trust-API-auth, on-chain-adjacent, provisioning, governance-verifier bugs.
4. **Make the change.** Only files in enumerated paths.
5. **Run validation.**
   - `go test ./...` (or targeted `go test ./internal/<pkg>/...`).
   - `go vet ./...`, `gofmt -l .` empty.
   - For Solidity: `hardhat test`, Slither, solhint.
   - For `web/`: build + CSP validation (dev server + automated check).
   - For CDK: `cdk synth --context stage=lab`.
   - For gov-infra: run the relevant verifier locally where possible.
6. **For on-chain-adjacent changes**: confirm transaction-preparation code paths are idempotent / dry-run-capable / explicit-confirmation.
7. **For multi-tenant-adjacent changes**: confirm no cross-tenant read / write leaks.
8. **For governance-rubric changes**: confirm pack.json version bumps, evidence-policy updates, verifier documentation.
9. **For consumer-release-verification changes**: confirm `scripts/managed-release-certification/*` and `scripts/managed-release-readiness/*` pass end-to-end against a test release.
10. **Commit.** Planned subject. First line under 72 chars. Explain *why* for governance / multi-tenant / on-chain / release-verification / trust-API / AGPL / framework-adjacent changes.
11. **Push.** Never force-push.
12. **Check task off** in PR; update GitHub Project item status.
13. **`memory_append`** only when worth remembering — governance evolution, on-chain coordination, multi-tenant edge case, release-verification subtlety, trust-API pattern, framework awkwardness, advisor pattern. Five meaningful entries beat fifty log-shaped ones.

## The mission rule enforced at commit time

Inside a milestone:

- **Every commit must be in-mission.** Scope growth → `scope-need`.
- **Bug-fix commits follow test-first pattern.**
- **Solidity commits isolated**; Slither + hardhat + solhint evidence in commit body.
- **Dependency bumps isolated** for bisect.
- **CDK commits isolated** from Go code.
- **`web/` commits isolated**; CSP validation runs.
- **`gov-infra/pack.json` commits isolated**; documentation explains the rubric shift.
- **Consumer-release-verification commits isolated**; elevated scrutiny noted.
- **No hardcoded secrets, wallet keys, mint-signer keys, raw instance keys, partner credentials, `.env` files.**
- **No raw-key / seed-phrase / full-signed-tx / PII / full-recipient logging.**
- **No changes to `AGENTS.md`, branch protection, CODEOWNERS, or governance policies without explicit governance authorization.**
- **No on-chain Ethereum mainnet deploy single-signer.**
- **No multi-tenant isolation traversal.**
- **No framework patches** to AppTheory / TableTheory / FaceTheory locally.
- **No AGPL-incompatible dependencies or proprietary blobs.**
- **No consumer-release-verification bypass.**
- **No trust-API auth loosening or CSP loosening silently.**

## If tests or verifiers go red mid-milestone

- **Do not** add a "fix tests" or "fix verifier" commit touching unrelated code.
- **Do** stop, investigate, surface.
- **Do not** weaken a test or verifier.
- If failure is caused by your most recent commit, revert with a new revert commit and re-plan.
- If a verifier reveals a gov-infra issue, route through `maintain-governance-rubric`.

## Finishing the milestone (PR side)

When all tasks committed and pushed:

1. Run `go test ./...` on the tip.
2. Run `go vet ./...`, `gofmt -l .`.
3. Run `cdk synth --context stage=lab` if CDK changed.
4. Run `hardhat test` + Slither + solhint if Solidity changed.
5. Run `web/` build + CSP validation if web/ changed.
6. Confirm gov-infra verifiers pass (CI will re-run).
7. Promote PR out of draft.
8. Request required review.
9. **Leave merging to a reviewer** who confirms CI including gov-infra verifiers is green.

## Hand off after merge

Once merged to `main`, downstream flows take over per the roadmap:

- `theory app up --stage lab` deploy (operator-run)
- Lab soak
- `theory app up --stage live` deploy (operator-authorized)
- Live post-deploy monitoring
- On-chain Sepolia deploy (if contracts changed)
- On-chain mainnet Safe-ready execution (if contracts changed, after signer coordination)
- Managed-instance rollout (if provisioning changed, canary customer → broader)
- Release artifact publication (if relevant)

`implement-milestone` does not run these. Its output is a merged PR + handoff.

## What this skill will not do

- Will not implement more than one milestone per run.
- Will not accept scope growth as a task.
- Will not merge PRs — required review (with gov-infra verifiers green).
- Will not skip review or verifiers.
- Will not run `theory app up` commands — separate step.
- Will not run `hardhat deploy` to Sepolia or mainnet — separate step; mainnet requires Safe multisig.
- Will not run managed-instance rollout — separate step.
- Will not skip specialist walks.
- Will not delete Lambda versions, DynamoDB tables, S3 buckets with RETAIN, Route53 zones, SSM, Secrets Manager entries, or CloudFormation stacks.
- Will not force-push, amend pushed commits, rewrite history.
- Will not bump Go runtime version in ordinary milestone.
- Will not add unsanitized logging or raw-credential logging.
- Will not set timeouts on CDK commands.
- Will not patch AppTheory / TableTheory / FaceTheory locally.
- Will not introduce AGPL-incompatible dependencies.
- Will not act on advisor briefs without `review-advisor-brief` authorization.
- Will not weaken a gov-infra verifier.
