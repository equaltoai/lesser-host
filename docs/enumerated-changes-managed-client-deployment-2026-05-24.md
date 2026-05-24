# Enumerated changes: Managed client deployment for `/l/`

Date: 2026-05-24
Author: host steward

Source roadmap: `docs/roadmap-managed-client-deployment-2026-05-24.md`.

These items are scoped as one logical commit or one sibling-repo issue each. Host issues preserve tenant isolation, do not touch on-chain state, and keep consumer release verification explicit. Sibling items are coordination work for their respective stewards; host does not edit those repos.

## Milestone map

- **M0: Managed Client M0 — Contract blockers and artifact readiness** — Freeze the cross-repo contracts that unblock host runner work and Simulacrum default-client readiness.
- **M1: Managed Client M1 — Host verification and governance baseline** — Add host-side catalog/release verification and governance evidence before data or runner implementation.
- **M2: Managed Client M2 — GitHub App and data foundation** — Add tenant-bounded state, GitHub App secret/config loading, and onboarding verification primitives.
- **M3: Managed Client M3 — Portal APIs and webhook ingress** — Expose customer/operator APIs and authenticated GitHub webhook ingestion without deploying clients yet.
- **M4: Managed Client M4 — Runner isolation and infrastructure** — Split custom source builds from tenant deploy, add scoped tenant role, and provision queues/runners safely.
- **M5: Managed Client M5 — Deploy orchestration, default, and reset** — Wire client deploy jobs into provisioning, reset, and install-history/recovery flows.
- **M6: Managed Client M6 — Portal UX, validation, and rollout** — Ship the portal/operator UX, validation harnesses, docs, evidence, and lab/live rollout gates.

## Change list

### 1. [L1] Support ambient credentials for lesser client install

- **Repository**: `equaltoai/lesser`
- **Milestone**: M0
- **Paths**: `cmd/lesser`, Lesser CLI AWS config helpers, client install tests/docs
- **Surface**: lesser CLI
- **Classification**: provisioning / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; enables scoped role
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: `lesser client install --skip-build` works noninteractively from ambient CodeBuild role credentials while `--aws-profile` remains an optional override.
- **Validation**: Lesser CLI tests plus a noninteractive ambient-credential smoke path.
- **Conventional Commit subject**: `fix(cli): allow ambient credentials for client install`

### 2. [L2] Document Lesser client install receipt and least-privilege IAM contract

- **Repository**: `equaltoai/lesser`
- **Milestone**: M0
- **Paths**: Lesser client guide/docs/tests
- **Surface**: docs/tests
- **Classification**: docs / provisioning / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; narrows tenant role
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: Docs name required receipt outputs, explicit-stage automation use, and the install-only S3/CloudFront IAM shape.
- **Validation**: Docs review plus CLI help/tests updated.
- **Conventional Commit subject**: `docs(client): document managed install automation contract`

### 3. [S1] Package immutable Simulacrum managed-client release artifacts

- **Repository**: `equaltoai/simulacrum`
- **Milestone**: M0
- **Paths**: `scripts/package-client-release.mjs`, `package.json`, release docs
- **Surface**: sim release tooling
- **Classification**: release / docs
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: producer artifact contract
- **Framework consumption**: FaceTheory idiomatic
- **Acceptance**: After `pnpm build`, Sim produces an archive containing `build/server`, `build/client`, `facetheory.lesser.json`, release metadata, and checksums.
- **Validation**: `pnpm check`, `pnpm build`, packaging script.
- **Conventional Commit subject**: `feat(release): package managed client artifact`

### 4. [S2] Verify Simulacrum managed-client release artifacts

- **Repository**: `equaltoai/simulacrum`
- **Milestone**: M0
- **Paths**: `scripts/verify-client-release.mjs`, tests/docs
- **Surface**: sim release verification
- **Classification**: release / test-coverage / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: producer artifact verifier
- **Framework consumption**: FaceTheory idiomatic
- **Acceptance**: Verifier checks file manifest, checksums, allowed manifest overrides, `/l` metadata, path safety, no symlinks, and required entry files.
- **Validation**: Verifier passes on generated artifact and fails on tampered/missing entries.
- **Conventional Commit subject**: `test(release): verify managed client artifact`

### 5. [S3] Remove or guard Simulacrum preview-domain fallback data

- **Repository**: `equaltoai/simulacrum`
- **Milestone**: M0
- **Paths**: `src/facetheory/loaders.ts`, focused guard/test
- **Surface**: sim FaceTheory runtime
- **Classification**: CSP / docs / test-coverage
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: blocks default catalog promotion
- **Framework consumption**: FaceTheory idiomatic
- **Acceptance**: Managed-instance runtime surfaces do not expose `simulacrum.dev` as an origin or customer-visible deployment assumption.
- **Validation**: Focused grep/test plus `pnpm check` and `pnpm build`.
- **Conventional Commit subject**: `fix(facetheory): remove managed-default preview domains`

### 6. [S4] Publish candidate/stable Simulacrum client artifacts

- **Repository**: `equaltoai/simulacrum`
- **Milestone**: M0
- **Paths**: `.github/workflows/*` or release command docs
- **Surface**: sim release workflow
- **Classification**: release / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP evidence preserving
- **Consumer-release-verification impact**: producer publish path
- **Framework consumption**: FaceTheory idiomatic
- **Acceptance**: A semver-tagged candidate artifact can be published with immutable URLs, release metadata, and sha256 digests for host catalog pinning.
- **Validation**: Release workflow dry run or operator-run release command evidence.
- **Conventional Commit subject**: `ci(release): publish managed client artifacts`

### 7. [H1] Freeze managed client deployment contracts and ADR

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M0
- **Paths**: `docs/`, optional `docs/adr/`
- **Surface**: docs
- **Classification**: docs / provisioning / governance
- **Governance-rubric impact**: none; records gates
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: documents catalog vs custom artifact boundary
- **Framework consumption**: idiomatic
- **Acceptance**: Roadmap/ADR records host-owned GitHub App, Lesser install boundary, Sim artifact contract, runner split, and open product choices.
- **Validation**: Markdown review; no runtime validation.
- **Conventional Commit subject**: `docs(client): freeze managed client deployment contracts`

### 8. [H2] Define managed client catalog schema and fixtures

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M1
- **Paths**: `internal/...`, `scripts/managed-client-release-certification/testdata`, `docs/`
- **Surface**: internal/config / scripts / docs
- **Classification**: consumer-release-verification / docs
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: touches verification pipeline
- **Framework consumption**: idiomatic
- **Acceptance**: Host can represent immutable catalog entries with exact artifact URL/tag/sha256, channel pointer, allowed manifest overrides, and validation evidence fields.
- **Validation**: Go/unit tests or script fixture tests.
- **Conventional Commit subject**: `feat(client): define managed client catalog schema`

### 9. [H3] Add managed client release certification verifier

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M1
- **Paths**: `scripts/managed-client-release-certification/`, fixtures
- **Surface**: scripts
- **Classification**: consumer-release-verification / security
- **Governance-rubric impact**: additive evidence target
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: touches verification pipeline
- **Framework consumption**: idiomatic
- **Acceptance**: Verifier rejects checksum mismatch, path traversal, symlinks/hardlinks, missing entry files, absolute paths, files outside allowed prefixes, and unauthorized manifest mutations.
- **Validation**: Verifier fixture suite including positive and negative cases.
- **Conventional Commit subject**: `feat(client): certify managed client artifacts`

### 10. [H4] Add gov-infra evidence for managed client supply-chain checks

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M1
- **Paths**: `gov-infra/verifiers`, `gov-infra/planning`, `gov-infra/evidence`
- **Surface**: gov-infra
- **Classification**: governance / consumer-release-verification
- **Governance-rubric impact**: additive verifier/evidence only
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: touches verification pipeline
- **Framework consumption**: idiomatic
- **Acceptance**: Gov verifier emits evidence for managed-client artifact verification without weakening existing rubric checks.
- **Validation**: `bash gov-infra/verifiers/gov-verify-rubric.sh`.
- **Conventional Commit subject**: `feat(gov): verify managed client release evidence`

### 11. [H5] Document managed client release readiness and recovery

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M1
- **Paths**: `docs/managed-client-release-certification.md`, `docs/recovery.md`, related docs
- **Surface**: docs
- **Classification**: docs / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: documents verification gate
- **Framework consumption**: idiomatic
- **Acceptance**: Docs distinguish host-certified catalog artifacts from tenant-owned custom GitHub artifacts and describe reset/recovery.
- **Validation**: Docs review.
- **Conventional Commit subject**: `docs(client): describe release readiness and recovery`

### 12. [H6] Add TableTheory models for client deploy state

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M2
- **Paths**: `internal/store/models`, `internal/store`
- **Surface**: internal/store
- **Classification**: tenant-isolation / provisioning
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: owner/slug bounded
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: TableTheory idiomatic
- **Acceptance**: Models cover catalog entry reference, instance client state, deploy jobs, GitHub binding, and webhook ledger without storing secrets or raw repo source.
- **Validation**: `go test ./internal/store/...` plus projection tests.
- **Conventional Commit subject**: `feat(store): add managed client deploy models`

### 13. [H7] Implement owner-bounded client deploy store accessors

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M2
- **Paths**: `internal/store`
- **Surface**: internal/store
- **Classification**: tenant-isolation / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; no cross-tenant query
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: TableTheory idiomatic
- **Acceptance**: All client-state/binding/job accessors are slug and owner bounded with tests preventing cross-tenant reads.
- **Validation**: `go test ./internal/store/...`.
- **Conventional Commit subject**: `feat(store): bound client deploy access by owner`

### 14. [H8] Load stage-scoped host-owned GitHub App secrets

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M2
- **Paths**: `internal/config`, `internal/secrets`, `cdk` env refs
- **Surface**: internal/config / internal/secrets
- **Classification**: security / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: none
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: AppTheory idiomatic
- **Acceptance**: Host loads app id, private-key ref, webhook secret ref, and callback settings from stage-scoped SSM/Secrets refs without logging values.
- **Validation**: `go test ./internal/config/... ./internal/secrets/...` with redaction tests.
- **Conventional Commit subject**: `feat(github): load client app secret refs`

### 15. [H9] Implement GitHub App installation verification service

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M2
- **Paths**: `internal/controlplane` or new `internal/githubapp`
- **Surface**: internal service
- **Classification**: security / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; repo binding owner-gated
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: Service mints short-lived installation tokens, verifies installations server-side, derives repository candidates, and never persists tokens.
- **Validation**: Unit tests with mocked GitHub responses and redacted logs.
- **Conventional Commit subject**: `feat(github): verify client app installations`

### 16. [H10] Add managed client audit and redaction helpers

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M2
- **Paths**: `internal/controlplane`, logging helpers/tests
- **Surface**: internal/controlplane
- **Classification**: security / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: Audit events record connect/deploy/reset/disable decisions while redacting tokens, webhook signatures, repo secrets, AWS creds, and PII.
- **Validation**: Unit tests for redaction and audit event shape.
- **Conventional Commit subject**: `feat(client): audit managed client actions safely`

### 17. [H11] Add portal APIs for catalog, deploy status, history, and reset

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M3
- **Paths**: `internal/controlplane`, API docs
- **Surface**: control-plane API
- **Classification**: provisioning / managed-update / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: owner bounded
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: uses verified catalog entries
- **Framework consumption**: AppTheory idiomatic
- **Acceptance**: Portal customers can list catalog options, inspect safe deploy history/status, and enqueue reset for only their instance.
- **Validation**: `go test ./internal/controlplane/...` with auth/ownership cases.
- **Conventional Commit subject**: `feat(portal): expose managed client catalog and reset`

### 18. [H12] Add no-paste GitHub App connect and disconnect APIs

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M3
- **Paths**: `internal/controlplane`, web callback docs
- **Surface**: control-plane API
- **Classification**: security / tenant-isolation
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: owner bounded
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: AppTheory idiomatic
- **Acceptance**: Start/callback/bind/disconnect flow validates state, verifies installation/repo server-side, stores only binding metadata, and supports disabling without deleting audit history.
- **Validation**: Auth, CSRF/state, ownership, and installation mismatch tests.
- **Conventional Commit subject**: `feat(portal): connect GitHub App client deploys`

### 19. [H13] Add GitHub webhook HMAC verification and delivery ledger

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M3
- **Paths**: `internal/controlplane`, `cmd/control-plane-api` routing, store accessors
- **Surface**: control-plane API / store
- **Classification**: security / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: binding-scoped
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: custom artifact boundary
- **Framework consumption**: AppTheory idiomatic
- **Acceptance**: Webhook accepts only valid `X-Hub-Signature-256` push events matching the bound repo and exact `refs/heads/<branch>`, dedupes deliveries, and ignores non-matching events safely.
- **Validation**: Fixture tests for HMAC, replay, event/ref mismatch, and redacted logs.
- **Conventional Commit subject**: `feat(github): queue client deploy webhooks safely`

### 20. [H14] Gate custom GitHub deploy availability by feature flag or allowlist

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M3
- **Paths**: `internal/controlplane`, config/docs
- **Surface**: control-plane API
- **Classification**: security / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: Custom GitHub deploys can launch as operator-allowlisted while catalog reset remains available by policy.
- **Validation**: Unit tests for gated/ungated portal behavior.
- **Conventional Commit subject**: `feat(client): gate custom GitHub deploy access`

### 21. [H15] Provision managed client queues, DLQs, alarms, and runner settings

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M4
- **Paths**: `cdk/`, `app-theory/app.json` if needed
- **Surface**: cdk
- **Classification**: provisioning / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: AppTheory CDK idiomatic
- **Acceptance**: CDK adds client deploy queue/DLQ/alarm resources and CodeBuild settings without changing live stateful retention policies.
- **Validation**: `cd cdk && npm run synth`.
- **Conventional Commit subject**: `feat(cdk): add managed client deploy infrastructure`

### 22. [H16] Add narrow tenant client-deploy IAM role policy

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M4
- **Paths**: `cdk/`, runner role docs
- **Surface**: cdk / docs
- **Classification**: tenant-isolation / security / provisioning
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: tightens tenant boundary
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: Tenant deploy role grants only the required S3 PutObject prefixes and CloudFront CreateInvalidation on the target distribution for routine client install.
- **Validation**: CDK synth plus IAM snapshot/policy assertion tests.
- **Conventional Commit subject**: `feat(cdk): scope tenant client deploy role`

### 23. [H17] Implement catalog deploy runner path

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M4
- **Paths**: `cdk/lib/provision-runner` or new runner scripts
- **Surface**: runner scripts
- **Classification**: consumer-release-verification / provisioning
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP verification
- **Consumer-release-verification impact**: touches verification pipeline
- **Framework consumption**: idiomatic
- **Acceptance**: Runner downloads exact catalog artifact, verifies schema/checksums/path safety, renders only allowed manifest fields, then calls `lesser client install --skip-build`.
- **Validation**: Runner script tests with good/bad fixtures.
- **Conventional Commit subject**: `feat(client): install verified catalog artifacts`

### 24. [H18] Implement custom source build runner without tenant AWS credentials

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M4
- **Paths**: runner scripts/buildspecs, tests
- **Surface**: runner scripts / CodeBuild
- **Classification**: security / tenant-isolation / provisioning
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; source build has no tenant role
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: custom artifact boundary
- **Framework consumption**: idiomatic
- **Acceptance**: Custom GitHub source builds run with repository token only, no tenant AWS role, no tenant secrets, and output a bounded deploy artifact inventory.
- **Validation**: Buildspec/static tests proving tenant role is not assumed before custom build.
- **Conventional Commit subject**: `feat(client): build custom clients without tenant creds`

### 25. [H19] Implement deploy-only runner validation for custom artifacts

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M4
- **Paths**: runner scripts/buildspecs, tests
- **Surface**: runner scripts / CodeBuild
- **Classification**: security / provisioning
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves; deploy role only after artifact validation
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP verification
- **Consumer-release-verification impact**: custom artifact verification
- **Framework consumption**: idiomatic
- **Acceptance**: Deploy-only runner validates bounded artifact inventory before assuming scoped tenant role and running Lesser install.
- **Validation**: Runner tests for tampered inventory, path traversal, missing entries, and allowed manifest fields.
- **Conventional Commit subject**: `feat(client): deploy validated custom client artifacts`

### 26. [H20] Assert runner split and credential redaction in tests

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M4
- **Paths**: runner tests, `scripts/validate`, gov evidence hooks
- **Surface**: tests / scripts
- **Classification**: security / test-coverage / governance
- **Governance-rubric impact**: additive evidence if wired
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: touches custom verification
- **Framework consumption**: idiomatic
- **Acceptance**: Tests fail if custom build assumes tenant role early or logs GitHub tokens/webhook signatures/AWS credentials.
- **Validation**: Runner tests plus gov verifier if added.
- **Conventional Commit subject**: `test(client): prove runner credential isolation`

### 27. [H21] Implement ClientDeployJob state machine

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M5
- **Paths**: `internal/provisionworker`, `internal/store`, worker entrypoint
- **Surface**: provision-worker
- **Classification**: provisioning / managed-update / reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: slug bounded
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: uses verified artifacts
- **Framework consumption**: AppTheory idiomatic
- **Acceptance**: Jobs cover catalog deploy, reset, and custom GitHub deploy with retryable/nonretryable states and safe receipt ingest.
- **Validation**: Provision-worker unit/state-machine tests.
- **Conventional Commit subject**: `feat(worker): orchestrate client deploy jobs`

### 28. [H22] Install default Simulacrum during new managed provisioning

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M5
- **Paths**: `internal/provisionworker`, provisioning docs/tests
- **Surface**: provision-worker
- **Classification**: provisioning / consumer-release-verification
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP validation
- **Consumer-release-verification impact**: uses verified Sim artifact
- **Framework consumption**: idiomatic
- **Acceptance**: New managed provisions install the pinned verified Simulacrum catalog artifact before ready, with recoverable client-deploy failure state.
- **Validation**: Provisioning tests and lab disposable slug install.
- **Conventional Commit subject**: `feat(provision): install default managed client`

### 29. [H23] Implement reset semantics for catalog client deployment

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M5
- **Paths**: `internal/controlplane`, `internal/provisionworker`, docs/tests
- **Surface**: portal API / provision-worker
- **Classification**: managed-update / provisioning / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: owner bounded
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP validation
- **Consumer-release-verification impact**: uses verified catalog
- **Framework consumption**: idiomatic
- **Acceptance**: Reset reinstalls selected catalog artifact idempotently and disables custom auto-triggering unless the operator explicitly re-enables it.
- **Validation**: API/worker tests for idempotency and disabled-binding behavior.
- **Conventional Commit subject**: `feat(client): reset managed client to catalog`

### 30. [H24] Add install history, retry, and concurrency guards

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M5
- **Paths**: `internal/store`, `internal/provisionworker`, docs/tests
- **Surface**: store / provision-worker
- **Classification**: reliability / managed-update / provisioning
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: slug bounded
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: none
- **Consumer-release-verification impact**: preserves verified artifact references
- **Framework consumption**: TableTheory/AppTheory idiomatic
- **Acceptance**: Client deploys cannot race core provisioning or managed updates; install history supports safe retry/recovery without storing raw artifacts or secrets.
- **Validation**: Concurrency and retry tests.
- **Conventional Commit subject**: `feat(client): record install history and deploy guards`

### 31. [H25] Build portal managed clients UI

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M6
- **Paths**: `web/`
- **Surface**: web
- **Classification**: host-web / CSP / portal
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: owner scoped views
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: preserves strict CSP
- **Consumer-release-verification impact**: displays catalog/custom boundary
- **Framework consumption**: Svelte/FaceTheory patterns idiomatic
- **Acceptance**: Portal supports catalog selection, reset, deploy status/history, GitHub connect/disconnect, and branch display with no inline scripts/styles or third-party embeds.
- **Validation**: `cd web && npm run lint && npm run typecheck && npm test && npm run build` plus CSP scan.
- **Conventional Commit subject**: `feat(web): add managed client portal`

### 32. [H26] Add operator support views for managed client deployments

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M6
- **Paths**: `web/`, `internal/controlplane` as needed
- **Surface**: web / control-plane API
- **Classification**: operational-reliability / security
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: safe metadata only
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic
- **Acceptance**: Operator/support surfaces show safe job/delivery metadata only, without raw logs, tokens, repo source, or PII.
- **Validation**: API/UI tests and redaction checks.
- **Conventional Commit subject**: `feat(web): show managed client support metadata`

### 33. [H27] Add end-to-end validation harness for catalog, custom, reset, and CSP

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M6
- **Paths**: `scripts/validate`, `scripts/managed-client-release-certification`, test fixtures
- **Surface**: scripts / tests
- **Classification**: test-coverage / security / provisioning
- **Governance-rubric impact**: additive evidence path
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP verification
- **Consumer-release-verification impact**: real consumer path validation
- **Framework consumption**: idiomatic
- **Acceptance**: Harness covers HMAC handling, no-paste onboarding, catalog checksum mismatch abort, custom disposable repo deploy, reset, and `/l/*` CSP smoke.
- **Validation**: Harness dry run and lab disposable slug evidence.
- **Conventional Commit subject**: `test(client): validate managed client deployment paths`

### 34. [H28] Finalize managed client runbooks and rollout docs

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M6
- **Paths**: `docs/recovery.md`, `docs/managed-instance-provisioning.md`, new managed-client docs
- **Surface**: docs
- **Classification**: docs / operational-reliability
- **Governance-rubric impact**: none
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP preserving
- **Consumer-release-verification impact**: documents verification and recovery
- **Framework consumption**: idiomatic
- **Acceptance**: Runbooks cover disable binding, rotate GitHub App credentials, retry failed deploy, reset to Simulacrum, previous install rollback, lab soak, and live canary gates.
- **Validation**: Docs review.
- **Conventional Commit subject**: `docs(client): finalize runbooks and rollout`

### 35. [H29] Collect lab and live-canary rollout evidence

- **Repository**: `equaltoai/lesser-host`
- **Milestone**: M6
- **Paths**: `gov-infra/evidence`, `docs/deployments` or release notes
- **Surface**: evidence / docs
- **Classification**: governance / operational-reliability
- **Governance-rubric impact**: evidence emission
- **Multi-tenant-isolation impact**: preserves
- **On-chain impact**: none
- **Trust-API / CSP / instance-auth impact**: CSP evidence
- **Consumer-release-verification impact**: real artifact verification evidence
- **Framework consumption**: idiomatic
- **Acceptance**: Evidence captures exact Sim artifact digest, disposable lab slug deploy/reset/custom cycles, and live internal canary outcome before broader availability.
- **Validation**: Gov verifier plus recorded lab/live canary checks.
- **Conventional Commit subject**: `chore(client): record managed client rollout evidence`
