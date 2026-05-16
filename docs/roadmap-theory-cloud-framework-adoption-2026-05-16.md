# Roadmap: Theory Cloud Framework Adoption — 2026-05-16

## Goal

Deliberately adopt AppTheory `v1.6.0`, TableTheory `v1.8.3`, and Greater Components `greater-v0.8.11` in `lesser-host`, close the current Dependabot security alerts, and then consume only the framework capabilities that improve host's reliability, trust/auth audit posture, worker behavior, and maintenance discipline without weakening multi-tenant isolation, on-chain integrity, consumer release verification, trust API instance-auth, or strict CSP.

## Classification

Dependency maintenance, security, operational reliability, trust-API/auth audit hardening, managed-update/provisioning design analysis, framework-feedback, documentation.

## Surfaces affected

- Go dependency pins: `go.mod`, `go.sum`, `app-theory/app.json`, `app-theory/init.md`
- CDK/dependency security baseline: `cdk/package.json`, `cdk/package-lock.json`, `cdk/vendor/`
- Contracts/dependency security baseline: `contracts/package.json`, `contracts/package-lock.json`
- Web/dependency and Greater baseline: `web/package.json`, `web/package-lock.json`, `web/components.json`, `web/src/lib/greater/**`, `web/README.md`
- Store/runtime adoption: `internal/store/**`, Lambda app/server packages under `internal/*worker`, `internal/controlplane`, `internal/trust`
- Trust/auth/source audit: `internal/controlplane/**`, `internal/trust/**`, `internal/httpx/**`
- Scheduled worker adoption: `internal/provisionworker/server.go`, `internal/renderworker/server.go`, `internal/soulreputationworker/server.go`, related tests
- Managed-update/provisioning feasibility: `internal/provisionworker/update_jobs.go`, `internal/store/models/provision_job.go`, `internal/store/models/update_job.go`, docs only unless separately approved
- Documentation: this roadmap and the investigation note
- Governance: no rubric weakening; verifier/evidence changes only if later milestone explicitly requires additive coverage

## Sibling-repo coordination

- **lesser**: not required for the proposed host framework-adoption milestones; no lesser release-artifact contract change.
- **body**: awareness only. AppTheory MCP runtime work belongs primarily in `lesser-body`; host should not absorb body MCP scope.
- **soul**: not required; no namespace change.
- **greater**: required for framework feedback around markdown sanitization and CSS selector warnings. Host retains local fail-closed sanitizer hardening until upstream provides a strict-CSP-safe shape.
- **sim**: optional validation if Greater feedback reveals shared component behavior, but no immediate sim-side work.

## Framework coordination

- **AppTheory**: required for feedback on native SES event-source support; host will locally adopt source provenance, event workload helpers, and typed binding pilots where idiomatic.
- **TableTheory**: required only if Lambda timeout propagation exposes an interface/ergonomic gap. Initial work is local host consumption.
- **FaceTheory / Greater**: required for markdown sanitizer/CSP feedback and LightningCSS `:global(...)` warnings.

## External-vendor coordination

- **Stripe / billing**: no vendor contract changes. Source-provenance audit may touch webhook logging/tests without changing Stripe signature validation.
- **SES / email vendors**: no vendor contract changes. Native AppTheory SES support is framework feedback; current SES ingestion semantics remain.
- **AI providers**: none.
- **eth_rpc provider**: none.
- **Safe multisig signers**: none; no on-chain deployment.

## Enumerated changes

### 1. Land framework and security dependency baseline

- **Paths**: `go.mod`, `go.sum`, `app-theory/app.json`, `app-theory/init.md`, `cdk/package.json`, `cdk/package-lock.json`, `cdk/vendor/`, `contracts/package.json`, `contracts/package-lock.json`, `web/package.json`, `web/package-lock.json`, `web/components.json`, `web/src/lib/greater/**`, `web/README.md`, `docs/roadmap.md`.
- **Surface**: deps, web, cdk, contracts, docs.
- **Classification**: dependency-maintenance, security, CSP, framework-feedback.
- **Governance-rubric impact**: none; gov verifier must stay green.
- **Multi-tenant-isolation impact**: none.
- **On-chain impact**: none; contract dependencies only, no Solidity/source/deploy change.
- **Trust-API / CSP / instance-auth impact**: preserves CSP; keeps mandatory markdown sanitization.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: idiomatic pins; Greater sanitizer awkwardness reported upstream rather than weakening host.
- **Acceptance**: latest framework pins are in place, Dependabot-vulnerable packages are patched in lockfiles, no vendored proprietary blobs remain, and current validation passes.
- **Validation**: `go test ./...`, `go vet ./...`, `gofmt -l .`, `cd cdk && npm run synth`, `cd contracts && npm test`, `cd web && npm run lint && npm run typecheck && npm test && npm run build`, `bash gov-infra/verifiers/gov-verify-rubric.sh`.
- **Conventional Commit subject**: `chore(deps): update theory frameworks and security pins`

### 2. Document adoption scope and project governance

- **Paths**: `docs/framework-adoption-investigation-2026-05-16.md`, `docs/roadmap-theory-cloud-framework-adoption-2026-05-16.md`, GitHub Project + issues.
- **Surface**: docs, project management.
- **Classification**: framework-feedback, governance, docs.
- **Governance-rubric impact**: none; documents preserve existing standards.
- **Multi-tenant-isolation impact**: none.
- **On-chain impact**: none.
- **Trust-API / CSP / instance-auth impact**: documents future tightening only.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: records no-local-patch posture.
- **Acceptance**: investigation, enumerated changes, roadmap, and project link exist; Arch review email is sent.
- **Validation**: markdown review, issue/project links, Arch delivery ID recorded.
- **Conventional Commit subject**: `docs(framework): plan Theory Cloud adoption roadmap`

### 3. Propagate TableTheory Lambda timeout awareness through store access

- **Paths**: `internal/store/store.go`, `internal/store/db.go`, `internal/store/db_helpers.go`, selected app/server packages that need request-scoped store wrappers, tests under `internal/store` and affected packages.
- **Surface**: internal/store, workers/APIs.
- **Classification**: operational-reliability, framework-consumption.
- **Governance-rubric impact**: none unless evidence is later added.
- **Multi-tenant-isolation impact**: none; context/deadline propagation must not alter tenant keying or queries.
- **On-chain impact**: off-chain only; no transaction construction changes.
- **Trust-API / CSP / instance-auth impact**: preserves.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: idiomatic TableTheory `WithLambdaTimeout` / `WithLambdaTimeoutBuffer` use.
- **Acceptance**: host DB calls can opt into Lambda-deadline-aware TableTheory behavior while preserving `TransactWrite`; tests prove no-op behavior for mocks/non-Lambda contexts and deadline propagation for supporting DBs.
- **Validation**: targeted `go test ./internal/store/... ./internal/controlplane/... ./internal/trust/... ./internal/*worker/...`, then full Go and gov verifier.
- **Conventional Commit subject**: `feat(store): apply TableTheory Lambda timeout guards`

### 4. Adopt AppTheory source provenance for auth, trust, and audit surfaces

- **Paths**: `internal/httpx/**`, `internal/controlplane/auth_operator.go`, `internal/controlplane/handlers_wallet.go`, `internal/controlplane/handlers_portal_auth.go`, `internal/controlplane/handlers_setup.go`, `internal/controlplane/rate_limits.go`, `internal/controlplane/handlers_comm_webhooks.go`, `internal/trust/auth_instance.go`, `internal/trust/rate_limits.go`, related tests.
- **Surface**: control-plane API, trust API, comm webhooks.
- **Classification**: security, trust-API, operational-reliability, framework-consumption.
- **Governance-rubric impact**: none unless audit evidence verifier coverage is added later.
- **Multi-tenant-isolation impact**: none; must not key tenant access from IP.
- **On-chain impact**: none.
- **Trust-API / CSP / instance-auth impact**: tightens audit/source handling; no auth bypass or raw-key storage.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: idiomatic AppTheory `ctx.SourceProvenance()` / `ctx.SourceIP()` and testkit source IP events.
- **Acceptance**: source IP/provenance in logs/rate-limit/audit code is framework-derived, forwarded headers are not trusted, and tests cover source provenance extraction.
- **Validation**: targeted auth/trust tests, `go test ./...`, `go vet ./...`, gov verifier; `audit-trust-and-safety` review.
- **Conventional Commit subject**: `feat(auth): use AppTheory source provenance`

### 5. Pilot AppTheory EventBridge scheduled workload normalization

- **Paths**: `internal/renderworker/server.go`, `internal/renderworker/server_test.go`, optionally `internal/soulreputationworker/server.go` and tests after the pilot; documentation if behavior is operator-visible.
- **Surface**: workers.
- **Classification**: operational-reliability, framework-consumption.
- **Governance-rubric impact**: none.
- **Multi-tenant-isolation impact**: none.
- **On-chain impact**: none.
- **Trust-API / CSP / instance-auth impact**: none.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: idiomatic AppTheory `NormalizeEventBridgeScheduledWorkload` / workload envelope helpers where available.
- **Acceptance**: one scheduled worker emits/uses normalized run/correlation/deadline metadata without logging raw events or changing sweep semantics.
- **Validation**: targeted worker tests with AppTheory testkit EventBridge events, full Go tests, gov verifier.
- **Conventional Commit subject**: `feat(worker): normalize scheduled workload metadata`

### 6. Pilot AppTheory typed request binding on one low-risk handler family

- **Paths**: one selected low-risk handler family under `internal/controlplane` or `internal/trust`, corresponding tests, possibly `internal/httpx` documentation comments.
- **Surface**: control-plane or trust API.
- **Classification**: maintainability, framework-consumption, test-coverage.
- **Governance-rubric impact**: none; must preserve MAI-3 canonical HTTP helper intent.
- **Multi-tenant-isolation impact**: none.
- **On-chain impact**: none unless an on-chain-adjacent handler is selected, which should be avoided for the pilot.
- **Trust-API / CSP / instance-auth impact**: preserves; avoid instance-auth paths for pilot unless separately reviewed.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: idiomatic AppTheory `BindRequest` / `BindConfig`; no local parser fork.
- **Acceptance**: one handler family uses typed binding with unchanged status/error semantics and explicit tests for empty/malformed/unknown-field behavior.
- **Validation**: targeted handler tests, `go test ./...`, gov verifier.
- **Conventional Commit subject**: `refactor(api): pilot AppTheory typed request binding`

### 7. Assess TableTheory release-state helpers for provisioning and managed updates

- **Paths**: `docs/managed-update-recovery.md` or a new design note, `internal/provisionworker/update_jobs.go`, `internal/store/models/provision_job.go`, `internal/store/models/update_job.go` for read-only analysis only.
- **Surface**: docs, provisioning/managed-update design.
- **Classification**: provisioning, managed-update, framework-feedback, docs.
- **Governance-rubric impact**: none unless later verifier/evidence changes are proposed.
- **Multi-tenant-isolation impact**: none for analysis. Any later migration would need elevated tenant-isolation review.
- **On-chain impact**: none.
- **Trust-API / CSP / instance-auth impact**: none.
- **Consumer-release-verification impact**: analysis touches the supply-chain frontier conceptually; no code changes without `provision-managed-instance` walk.
- **Framework consumption**: evaluate idiomatic TableTheory `pkg/releasestate` helpers; do not refactor in this item.
- **Acceptance**: a feasibility note identifies whether release-state helpers are a fit, a non-fit, or require framework feedback, with cost/risk accounting.
- **Validation**: docs review; no code validation beyond `go test ./...` if docs-only branch includes no code.
- **Conventional Commit subject**: `docs(provision): assess TableTheory release-state fit`

### 8. Relay framework feedback for AppTheory SES and Greater strict-CSP concerns

- **Paths**: framework-feedback issue/comment in the GitHub Project, outbound notes via Aron/stewards; no local framework patches.
- **Surface**: framework-feedback.
- **Classification**: framework-feedback, CSP, operational-reliability.
- **Governance-rubric impact**: none.
- **Multi-tenant-isolation impact**: none.
- **On-chain impact**: none.
- **Trust-API / CSP / instance-auth impact**: preserves strict CSP and markdown sanitizer posture.
- **Consumer-release-verification impact**: none.
- **Framework consumption**: explicit no-local-patch posture.
- **Acceptance**: AppTheory SES event-source feedback and Greater/FaceTheory sanitizer/CSS warnings are documented and routed through Aron/framework stewards.
- **Validation**: outbound coordination record; memory append for framework-feedback signals.
- **Conventional Commit subject**: `docs(framework): record upstream feedback signals`

## Phases

### Phase 1: Dependency/security and planning baseline

- **Items**: 1, 2
- **Dependencies**: none beyond current branch validation.
- **Risks**:
  - GitHub Dependabot alerts remain open until merged and rescanned.
  - Vendored Greater changes must preserve host's mandatory sanitizer hardening.
  - Existing uncommitted branch must become a coherent PR before new code milestones start.

### Phase 2: Runtime reliability hardening

- **Items**: 3, 5
- **Dependencies**: Phase 1 merged or at least rebased cleanly.
- **Risks**:
  - Store interface changes could affect many packages if not isolated behind helper methods.
  - Scheduled workload metadata must not change worker semantics or accidentally log raw event details.

### Phase 3: Trust/auth source hardening

- **Items**: 4
- **Dependencies**: Phase 1; `audit-trust-and-safety` review before implementation.
- **Risks**:
  - Source provenance must be audit/rate-limit context only, never tenant authorization.
  - Existing tests may assert error/log shapes that need careful preservation.

### Phase 4: Maintainability pilot

- **Items**: 6
- **Dependencies**: Phase 1; avoid high-risk/on-chain/trust-auth handlers for the first pilot.
- **Risks**:
  - AppTheory typed binding default errors may differ from `httpx.ParseJSON`; must preserve public API contracts.
  - Gov MAI-3 canonical helper intent must not be accidentally invalidated.

### Phase 5: Provisioning/managed-update design assessment

- **Items**: 7
- **Dependencies**: Phase 1; `provision-managed-instance` before any code follow-up.
- **Risks**:
  - Release-state helpers may be a poor fit for existing heavily-tested state machines.
  - Any later migration would be high-blast-radius and must use canary/dry-run discipline.

### Phase 6: Upstream framework feedback

- **Items**: 8
- **Dependencies**: evidence from Phase 1 and any later implementation pilots.
- **Risks**:
  - Framework feedback must be specific and not become local framework patching.
  - Greater sanitizer feedback is security/CSP-sensitive; host should keep its local fail-closed stance until upstream provides an equivalent.

## Stage rollout plan (host's own service)

### Lab

- **Command**: `theory app up --stage lab`
- **Soak duration**: at least one operator-observed lab exercise after each merged milestone that changes runtime behavior.
- **Soak criteria**:
  - Control-plane auth and portal login smoke tests pass.
  - Trust API public reads and instance-auth writes smoke tests pass when applicable.
  - Worker-specific scheduled/SQS tests are exercised in lab when worker behavior changes.
  - CloudWatch errors remain flat; SQS queue depth does not grow unexpectedly.
  - CSP remains strict (`script-src 'self'`, `style-src 'self'`, no inline/eval/third-party origin loosening).

### Live

- **Command**: `theory app up --stage live`
- **Authorization**: explicit operator approval after lab soak. Do not set a timeout on CDK deploys.
- **Post-deploy monitoring plan**:
  - CloudWatch error rate per Lambda.
  - CloudFront 4xx/5xx per surface.
  - Trust-API instance-auth failure rate.
  - SQS depth for provision/render/AI/comm queues.
  - SES inbound ingestion health.
  - Stripe/Telnyx webhook failure rate if source-provenance milestone touched webhooks.
  - Gov-infra evidence freshness.

## On-chain rollout plan

No Solidity, Safe-ready payload, Sepolia, or mainnet work is planned. If a later milestone discovers an on-chain implication, stop and run `evolve-soul-registry`.

## Managed-instance rollout plan

No provisioning runner behavior changes are planned in the initial implementation milestones. The release-state assessment is docs-only. If later code touches provisioning/managed-update execution:

- Dry-run against a test slug.
- Canary one managed instance.
- Broader rollout only after canary stability.
- Keep consumer release verification checksum enforcement unchanged.

## Release artifact plan

Host is the consumer, not publishing deployable managed-instance artifacts for this work. The dependency baseline PR should include release notes that identify framework pins and security alert closure.

## Rollback plan

- **Dependency baseline**: revert the PR and redeploy via standard lab/live path if validation fails.
- **Lambda runtime/store changes**: revert the milestone PR; Lambda-version rollback remains operator-owned after deploy.
- **CDK changes**: none planned after dependency baseline; if changed, revert commit + `theory app up --stage lab` then live after soak.
- **On-chain rollback**: not applicable.
- **Managed-update per-slug rollback**: not applicable unless later provisioning code is approved.

## AGPL posture

- No proprietary blobs.
- Retire vendored CDK tarball baseline in favor of registry lockfile pins.
- No framework vendoring or local monkey patches to AppTheory/TableTheory/FaceTheory.
- Dependency license posture remains AGPL-compatible unless a later lockfile audit says otherwise.

## Advisor-brief authorization

- This work is Aron/user-directed, not advisor-dispatched.
- Arch will be asked to review the scope. Any inbound Arch response is advisor-originated and must be surfaced/reviewed before being treated as executable direction.

## Open questions

1. Should TableTheory Lambda timeout propagation be implemented globally through `Store` helpers or only at selected Lambda handler boundaries first?
2. Which low-risk handler family should be the typed-binding pilot?
3. Should AppTheory SES event-source support be filed as a framework issue immediately, or first discussed with Aron/framework steward after Arch review?
4. Should the Greater markdown/CSP sanitizer feedback be filed against Greater directly or routed through Aron's Greater steward workflow?
