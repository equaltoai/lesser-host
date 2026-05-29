---
name: create-github-project
description: Use after plan-roadmap is approved, if the roadmap warrants a tracked GitHub Project at the equaltoai org level. Translates a roadmap document into a Projects v2 kanban board with issues across the affected repos. Follows equaltoai's established project pattern.
---

# Create a GitHub Project

equaltoai tracks initiative-level work in **GitHub Projects v2** at the org level, cross-repo by default. host's roadmaps often span multiple repos: host for the control-plane work, lesser / body for release-artifact-contract updates, soul for namespace changes, greater for web/ UI changes, sim for validation.

This skill turns an approved roadmap into a project board with a clear README, status-kanban, and issues in the right repos.

## Check what tools you have

- **`gh` CLI** with project scope: `gh project create`, `gh project field-list`, `gh project item-add`, `gh issue create`, `gh issue edit --add-project`.
- If not available, produce a well-shaped markdown draft.

Surface which mode you're in at the start.

## When this skill runs

Invoke when:

- Roadmap is large enough for tracked kanban (multiple phases, cross-repo coordination, multi-week cadence)
- Roadmap is an initiative (governance-rubric evolution, soul-registry feature, trust-API expansion, multi-phase provisioning rework) rather than a single bug fix
- Aron has asked for a project created

Skip when:

- Roadmap is a handful of issues on host's repo
- Kanban discipline adds no value

## The equaltoai project shape (reference)

From org practice (e.g. Project 20 — "Federation Readiness — Second Instance Proof"):

- **Title**: initiative-named. `<Initiative> — <qualifier>`.
- **Short description**: one-sentence scope.
- **README**: structured by **Goal / Repos involved / Non-goals / Success means / Working method**. Includes: "Treat this as a kanban. Move issues through explicit status as evidence is gathered and blockers become concrete."
- **Status field**: simple three-state — `Todo` / `In Progress` / `Done`.
- **Standard fields (10)**: Title, Assignees, Status, Labels, Linked pull requests, Milestone, Repository, Reviewers, Parent issue, Sub-issues progress.
- **Items**: GitHub Issues in one or more in-scope repos.
- **Milestones**: separate from Status — aggregate issues into delivery groupings.
- **Parent / sub-issue hierarchy**: used to break non-trivial work.

## The create walk

### Step 1: Draft the project README

```markdown
## <Initiative title>

<Brief paragraph on what this initiative proves / ships / unblocks.>

### Goal

<The specific outcome — what "done" looks like.>

### Repos involved

- **lesser-host**: <specific work scoped to host>
- **lesser**: <if lesser-side release contract / provisioning changes>
- **lesser-body**: <if body-side release contract / comm-API changes>
- **lesser-soul**: <if namespace changes>
- **greater-components**: <if web/ UI changes>
- **simulacrum**: <if validation / integration>

### Non-goals

- <explicit out-of-scope items>

### Success means

- <observable, testable condition>
- <gov-infra verifiers pass including any new ones>
- <on-chain deploy evidence on Etherscan (if applicable)>
- <managed-instance rollout stable (if provisioning changes)>

### Working method

Treat this as a kanban. Move issues through explicit status as evidence is gathered and blockers become concrete.
```

### Step 2: Create the project

```bash
gh project create --owner equaltoai --title "<initiative title>"
```

Capture project number `<N>`.

### Step 3: Populate README

```bash
gh project edit <N> --owner equaltoai \
  --readme "$(cat readme-draft.md)" \
  --description "<short-description>"
```

### Step 4: Confirm default fields

```bash
gh project field-list <N> --owner equaltoai --format json
```

### Step 5: Create issues and link

For each enumerated change, create in the right repo:

```bash
gh issue create \
  --repo equaltoai/<repo> \
  --title "<title>" \
  --body "$(cat issue-body.md)" \
  --label "<labels>" \
  --milestone "<milestone>"
```

Issue body template:

```markdown
**Source**: Roadmap <roadmap name>, Phase <phase>
**Enumerated item**: #<N>

## Paths
<...>

## Surface
<cmd / internal/<pkg> / cdk / web / contracts / gov-infra / scripts / docs / deps>

## Classification
<security / tenant-isolation / on-chain-integrity / governance / provisioning / managed-update / soul-registry / trust-API / CSP / operational-reliability / AGPL / framework-feedback / bug-fix / test-coverage / dependency-maintenance / docs>

## Specialist walks referenced
- Governance rubric: <...>
- Provisioning / managed-update / release verification: <...>
- Soul registry: <...>
- Trust API / CSP / instance-auth: <...>
- Framework: <idiomatic / reported upstream>

## Multi-tenant isolation impact
<none / elevated>

## On-chain impact
<none / Sepolia deploy / mainnet Safe-ready>

## Acceptance criterion
<one sentence>

## Validation commands
<go test ./..., hardhat test, Slither, gov-infra verifiers, cdk synth, web build + CSP>

## Stage rollout checkpoints
- [ ] Merged to main
- [ ] Deployed to lab
- [ ] Lab soak complete
- [ ] Deployed to live
- [ ] Post-deploy monitoring verified
- [ ] (If on-chain) Sepolia deploy verified
- [ ] (If on-chain) Mainnet Safe-ready deploy executed
- [ ] (If provisioning) Canary customer stable
- [ ] (If provisioning) Broader rollout complete

## Planned commit subject
<type(scope): subject>

## Parent issue
<link if sub-issue>
```

Link into project:

```bash
gh project item-add <N> --owner equaltoai --url <issue-url>
```

### Step 6: Set project fields

Status: `Todo` initially. Milestone: roadmap phase. Labels: scope. Parent issue: for sub-tasks.

### Step 7: Parent / sub-issue hierarchy

Example for a multi-repo initiative:

- Parent: `equaltoai/lesser-host#XXX — "Harden managed-release certification for lesser v1.5"`
- Sub-issues:
  - `equaltoai/lesser-host#YYY — scripts/managed-release-certification/ updates`
  - `equaltoai/lesser-host#ZZZ — provision-worker changes`
  - `equaltoai/lesser#AAA — lesser-release-contract.md update`
  - `equaltoai/lesser-host#BBB — gov-infra verifier addition`

## Labels

Apply consistently:

- `host-security` — CVE / isolation / auth
- `host-governance` — gov-infra rubric
- `host-tenant-isolation` — multi-tenant boundary
- `host-on-chain` — Solidity / Safe-ready / Ethereum
- `host-provisioning` — provisioning worker / managed-instance bring-up
- `host-managed-update` — managed-update pipeline
- `host-soul-registry` — soul-registry subsystem
- `host-trust-api` — trust-API / attestations
- `host-csp` — content-security-policy
- `host-consumer-release-verification` — supply-chain verification
- `host-portal` — customer portal / `web/`
- `host-comm` — comm worker / outbound email/SMS
- `host-reliability` — operational-reliability
- `host-docs` — documentation
- `host-deps` — dependency bumps
- `host-framework-feedback` — upstream signal to Theory Cloud
- `host-agpl` — license discipline
- Surface scopes: `host-control-plane`, `host-cdk`, `host-contracts`, `host-web`, etc.
- `breaking` — breaking changes requiring coordination
- `advisor-brief` — originated from advisor dispatch
- Specialist gates: `needs-governance-walk`, `needs-provisioning-walk`, `needs-soul-walk`, `needs-trust-walk`

## Priority and sequencing

Status drives kanban; Milestone groups into phases; priority by label + project order.

## The markdown-draft fallback

If `gh` CLI unavailable:

```markdown
# GitHub Project draft: <initiative title>

## Project README
<README draft>

## Default fields
Status: Todo / In Progress / Done
Milestones: <phase names>
Labels: <list>

## Issues

### In equaltoai/lesser-host
1. **<issue title>** — [`<labels>`]
   ...

### In equaltoai/lesser
1. ...
```

## Persist

Append project URL + scope when project exists. Five meaningful entries beat fifty log-shaped ones.

## Handoff

- Project exists and issues linked → `implement-milestone` with first item.
- User wants to revise → back to `plan-roadmap`.
- Cross-repo coordination surfaces → sibling stewards looped in before their issues begin.
- Too small for a project → skip; roadmap drives `implement-milestone` directly.
