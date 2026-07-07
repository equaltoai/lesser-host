# Tracking — Hosted genesis MicroVM registry boundary

Last updated: 2026-07-07
Owner: host steward + operator
Stage focus: `lab` first; `live` only after explicit operator approval

## Purpose

Track the corrective work for the hosted-genesis MicroVM controller failure seen in lab after the lab `lesser.host`
rebuild. This file is the local progress ledger for the approved plan: replace the rejected direct AppTheory
TableTheory registry persistence with a Host-owned, TableTheory-friendly operational cache and adapter.

## Current incident context

A portal-hosted genesis attempt for the `pan` Managed instance reached hosted-genesis execution and failed with:

- User-visible error: `MicroVM execution dispatch failed`
- Control plane error: `soul_instance.microvm_unavailable`
- Controller initialization reproduction: `field validation failed: invalid struct tag: attribute name must be camelCase (got "tenant_id")`

Root cause: Host wired AppTheory's generic `runtimemicrovm.SessionRegistryRecord` directly into TableTheory from the
controller. That exposed framework/table-row shape (`tenant_id`, table keys, generic registry record) at Host's deployed
boundary. This is a Host implementation mistake, not a TableTheory problem.

## Locked decisions

- `HostedGenesisSession` remains Host source truth for hosted genesis business/user-visible state.
- Add a Host-owned operational cache model for MicroVM execution state.
- Reconstruct operational cache state from `HostedGenesisSession` when needed.
- Implement controller `list` and `delete` routes fully; no canary-only shortcut.
- Do not persist AppTheory's generic `runtimemicrovm.SessionRegistryRecord` in Host deployed code.
- Do not directly map controller/domain code to TableTheory `PK`/`SK` keys.
- Do not fork AppTheory or add a raw AWS MicroVM substitute.
- Stop before framework-feedback coordination unless a later explicit operator instruction reopens it.
- Add a Gov-infra rubric guard so this boundary cannot silently regress.

## Non-goals

- No on-chain or contract migration.
- No tenant data import into the Control plane.
- No live deploy or live data mutation without explicit operator authorization.
- No secret/token logging, no raw Instance API key persistence, no raw MicroVM auth token exposure.

## Progress states

Use:

- `[ ]` not started
- `[~]` in progress
- `[x]` complete
- `[!]` blocked / needs operator decision

## Work plan

### 0. Workspace hygiene

Status: `[x]`

- [x] Re-ground on current branch and dirty worktree before implementation.
- [x] Preserve unrelated provision-worker hotfix edits; do not lose or reset them destructively.
- [x] Supersede the rejected temporary `NewMemorySessionRegistry()` controller patch with the real Host registry wiring.
- [x] Confirm PR target remains `staging` and branch/release docs are re-read before any GitHub action.

Known dirty files at plan creation:

- `cmd/hosted-genesis-microvm-controller/main.go`
- `cmd/hosted-genesis-microvm-controller/main_test.go`
- `internal/provisionworker/receipts.go`
- `internal/provisionworker/state_machine_flow_internal_test.go`
- `internal/provisionworker/state_machine_optional_steps_branches_internal_test.go`
- `internal/provisionworker/update_jobs_more_internal_test.go`

### 1. Host-owned MicroVM execution cache model

Status: `[x]`

Target files:

- `internal/store/models/hosted_genesis_microvm_execution.go`
- model tests under `internal/store/models`
- any store model registration path required by `internal/store/db.go`

Acceptance:

- Model uses repo-local camelCase TableTheory attributes.
- `PK`/`SK` are derived internally and are not part of controller/domain API shape.
- Fields are semantic Host fields: instance slug, namespace, conversation/session id, lifecycle state, provider ids,
  image ref/version, timestamps, TTL/version, sanitized metadata.
- No raw tokens, provider secrets, raw Instance API keys, wallet signatures, or raw transcripts are modeled.

### 2. Store repository for execution cache

Status: `[x]`

Target files:

- `internal/store/hosted_genesis_microvm_executions.go`
- repository tests

Acceptance:

- Repository exposes semantic operations only: put/get/list/delete by slug + namespace + session/conversation id.
- Repository callers never pass `PK`/`SK` directly.
- List/delete semantics are complete enough to back AppTheory controller routes.
- Missing rows are distinguishable from hard failures for reconstruction.

### 3. AppTheory `SessionRegistry` adapter backed by Host cache

Status: `[x]`

Target files:

- `internal/store/hosted_genesis_microvm_session_registry.go`
- adapter tests

Acceptance:

- Compile-time assertions for `runtimemicrovm.SessionRegistry` and `runtimemicrovm.SessionRegistryLister`.
- `Put`, `Get`, `Delete`, and `List` map AppTheory session semantics into Host-owned cache records.
- Missing/stale `Get` cache state is reconstructed from `HostedGenesisSession` by `microvm.NewReconstructingSessionRegistry` and written back through the Host adapter.
- Adapter validates slug/namespace/session boundaries and fails closed on malformed keys.
- No use of `runtimemicrovm.SessionRegistryRecord`, `SessionRecordToRegistryRecord`, or `NewTableTheorySessionRegistry`.

### 4. Controller wiring

Status: `[x]`

Target files:

- `cmd/hosted-genesis-microvm-controller/main.go`
- `cmd/hosted-genesis-microvm-controller/main_test.go`

Acceptance:

- Controller initializes Host store and Host-backed registry adapter.
- No direct TableTheory import in `cmd/hosted-genesis-microvm-controller`.
- No deployed `NewMemorySessionRegistry()` fallback.
- No deployed `NewTableTheorySessionRegistry()` call.
- AppTheory controller routes remain the registered route surface; list/delete are not disabled.

### 5. CDK permissions

Status: `[x]`

Target files:

- `cdk/lib/hosted-genesis-microvm.ts`
- relevant CDK tests

Acceptance:

- Controller Lambda has the minimum DynamoDB permissions needed for Host cache put/get/list/delete.
- Existing workload role permissions remain scoped; no cross-tenant broadening.
- No CDK context secret or manual token context is reintroduced.

### 6. Documentation updates

Status: `[x]`

Target files:

- `docs/hosted-genesis-microvm-lab-canary.md`
- `docs/project-49-durable-hosted-genesis.md`
- `docs/contracts/hosted-genesis-conversation.md`
- `docs/adr/0009-app-theory-microvm-lab-gates.md`
- this tracking file

Acceptance:

- Docs say AppTheory MicroVM registry state is operational cache, not Host business truth.
- Docs identify the Host-owned cache model and reconstruction path from `HostedGenesisSession`.
- Docs remove any implication that Host should persist AppTheory generic TableTheory records directly.

### 7. Gov-infra rubric guard

Status: `[x]`

Planned verifier:

- `gov-infra/verifiers/sec/hosted-genesis-microvm-registry-boundary.sh`
- proposed control id: `SEC-14`
- evidence: `gov-infra/evidence/SEC-14-output.log`

Acceptance:

- Verifier fails if deployed Host code persists `runtimemicrovm.SessionRegistryRecord`.
- Verifier fails if deployed Host code calls `NewTableTheorySessionRegistry`.
- Verifier fails if the hosted-genesis MicroVM controller imports TableTheory directly.
- Verifier fails if deployed controller code uses `NewMemorySessionRegistry`.
- Verifier requires Host-owned `HostedGenesisMicroVMExecution` model/repository/adapter evidence.
- Pack/rubric/controls/threat/evidence docs are updated explicitly as a governance event.

### 8. Validation gates

Status: `[x]`

Run before PR / deploy decision:

- [x] Tracked Go formatting is clean: `git ls-files -z '*.go' | xargs -0 gofmt -l | sed '/^$/d'`.
- [x] `go test ./...`
- [x] `go vet ./...`
- [x] CDK tests: `cd cdk && npm test`
- [x] CDK synth through the repo's accepted local path: `cd cdk && npm run synth`
- [x] Hosted-genesis MicroVM local canary script: not run pre-PR; live MicroVM canary remains a Phase 9 lab rollout
  acceptance item after deploy.
- [x] `bash gov-infra/verifiers/gov-verify-rubric.sh`
- [x] Check generated Evidence under `gov-infra/evidence/`.

Validation notes:

- Full Gov-infra verifier passed on 2026-07-07 at `2026-07-07T15:13:14Z`: 41 pass / 0 fail / 0 blocked.
- QUA-3 coverage passed at 80.1% after removing generated `web/node_modules` from the local Go package view. No verifier
  threshold was weakened.
- SEC-14 evidence was emitted for the hosted-genesis MicroVM registry boundary.

### 9. Lab rollout and soak

Status: `[ ]`

Deploy command, only when implementation is ready:

```bash
AWS_PROFILE=Lesser theory app up --stage lab --execute
```

Rules:

- Never set a timeout on the deploy.
- Let CloudFormation finish or roll back.
- Do not bypass the AppTheory deploy contract.

Lab acceptance:

- Hosted-genesis MicroVM preflight passes without reading/logging raw tokens.
- `pan` hosted genesis no longer fails with `MicroVM execution dispatch failed` / controller unavailable.
- Operational cache row is created/updated through Host model.
- Status recovery reconstructs from `HostedGenesisSession` when cache is absent.
- Controller list/delete routes work and do not leak tokens or cross slug boundaries.
- CloudWatch logs contain no raw tokens, provider secrets, raw Instance API keys, wallet signatures, or raw transcripts.
- Soak for at least 30 minutes without hosted-genesis 5xx regressions.

### 10. Live rollout

Status: `[ ]`

Requires explicit operator approval after lab soak.

Deploy command, only after approval:

```bash
AWS_PROFILE=Lesser theory app up --stage live --execute
```

Live acceptance mirrors lab, with extra care that live/mainnet contract posture is untouched unless separately authorized.

## Rollback posture

- Code rollback: revert controller/adapter/CDK changes and redeploy through `theory app up`.
- Data rollback: operational cache rows are additive/inert if ignored by older code; do not delete cache rows unless a
  separate runbook and operator approval require it.
- No on-chain rollback is involved.

## Evidence / decision log

| Date | Boundary | Result | Evidence |
| --- | --- | --- | --- |
| 2026-07-07 | Scope decision | Use Host-owned operational cache model, reconstruct from `HostedGenesisSession`, implement list/delete fully. | Memory: `mem-41b8b2a554746352` |
| 2026-07-07 | Governance audit | Add SEC-14 tightening verifier for MicroVM registry boundary. | Memory: `mem-040a2521cdde856f` |
| 2026-07-07 | Roadmap | Sequence: hygiene → model/repo → adapter → controller → CDK → docs → gov → validation → lab → live. | Memory: `mem-35f31243a7fc7483` |
| 2026-07-07 | SEC-14 local verifier | Host-owned MicroVM registry boundary verifier passes and writes `SEC-14-output.log`. | `gov-infra/evidence/SEC-14-output.log` |
| 2026-07-07 | Full validation | Gov-infra verifier passed: 41 pass / 0 fail / 0 blocked. Go tests, Go vet, tracked gofmt, CDK tests, and CDK synth passed. | `gov-infra/evidence/gov-rubric-report.json`; Memory: `mem-fb9badb06f95eb84` |

## Next action

Commit/push the MicroVM registry-boundary changes and open a draft PR to `staging`; do not deploy until the operator
explicitly approves Phase 9 lab rollout.
