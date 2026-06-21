# Release branching and branch protection

`lesser-host` uses the release-alignment branch model:

```text
feature branch -> staging -> main -> manual v* release tag
```

## Branch roles

- **Feature branches** (`aron/*`, `chore/*`, `codex/*`, `issue/*`, `feat/*`, `fix/*`) branch from current `main` and open PRs to `staging`.
- **`staging`** is the long-lived integration branch. Feature -> staging PRs require review plus the existing GitHub Actions checks: `gov-rubric` (`bash gov-infra/verifiers/gov-verify-rubric.sh`) and the seven parallel CI jobs (`go-test`, `golangci-lint`, `cdk-synth`, `contracts-compile`, `slither`, `web-build`, `contract-verify`). The staging protection spec requires branches to be up to date before merge.
- **`main`** is canonical production, protected, and operator-owned. Main promotion accepts PRs from `staging` only. Main protection requires the default CI jobs (`go-test`, `golangci-lint`, `cdk-synth`, `contracts-compile`, `slither`, `web-build`, `contract-verify`) and intentionally does **not** require `gov-rubric`; the Gov-infra rubric evidence is produced at the feature -> staging gate.
- **Releases** are manual `v*` tags cut from `main`. The release workflow asserts the release target commit is an ancestor of `origin/main` before publishing assets.

`premain` is not part of host's active release model. Treat stale `premain` refs as legacy unless an operator explicitly directs cleanup.

## Git branch `staging` vs deploy stages

The git branch named `staging` is a source-control integration branch. It is not an AppTheory deploy stage and does not introduce a third host environment.

Host deploy stages remain:

```text
lab -> live
```

Deploys continue to use the AppTheory contract, for example `theory app up --stage lab --execute` and `theory app up --stage live --execute`, with a lab soak before live unless an operator explicitly authorizes skipping it. Never set a timeout on a CDK deploy.

## Branch-protection specs

The committed specs are:

- `.github/branch-protection/staging.json`
- `.github/branch-protection/main.json`

They contain two layers:

1. `policy`: the human-readable host release policy.
2. `github_branch_protection.payload`: the GitHub REST branch-protection payload to apply.

GitHub classic branch protection does not expose a PR head-branch allowlist field. The `main` spec therefore records `allowed_pr_sources: ["staging"]` as the operator merge policy, while the API payload enforces the machine-enforceable parts: required PR, operator-owned branch updates, required default checks, no direct pushes, and no force-pushes. Do not add `gov-rubric` to `main` required status checks; `gov-rubric` is the staging gate.

## Operator apply commands

Branch-protection application is an operator action. Run these from a checkout containing the committed specs after confirming the operator actor restrictions in `main.json` are still correct.

```bash
# staging: feature -> staging requires gov-rubric + seven parallel CI jobs and up-to-date branches
jq '.github_branch_protection.payload' .github/branch-protection/staging.json \
  | gh api --method PUT \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2026-03-10" \
      /repos/equaltoai/lesser-host/branches/staging/protection \
      --input -

# main: operator-owned; PRs only by policy from staging; default checks only; no gov-rubric required gate
jq '.github_branch_protection.payload' .github/branch-protection/main.json \
  | gh api --method PUT \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2026-03-10" \
      /repos/equaltoai/lesser-host/branches/main/protection \
      --input -
```

## Operator proof commands

After applying, capture the live protection dumps:

```bash
gh api /repos/equaltoai/lesser-host/branches/staging/protection | jq .
gh api /repos/equaltoai/lesser-host/branches/main/protection | jq .
```

For a live negative test, confirm direct pushes to `main` and force-pushes to both protected branches are rejected. Because classic branch protection cannot machine-enforce the PR source branch, the operator-owned staging-only promotion rule is verified by review/merge discipline: reject or retarget any PR to `main` whose head branch is not `staging`.
