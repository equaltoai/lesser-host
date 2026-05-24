# Roadmap: Managed client deployment for `/l/`

Date: 2026-05-24
Author: host steward

## Goal

Deliver a first-class managed client deployment capability for `lesser-host` managed instances: new managed instances default to a checksum-verified EqualToAI client catalog entry, initially **Simulacrum**, operators can safely reset `/l/` back to a selected catalog client, and operators can optionally connect a **host-owned GitHub App** to one customer repository/branch so branch updates deploy a custom Lesser client to `/l/` without exposing AWS credentials.

The design preserves host's boundaries: host remains the managed-hosting control plane, Lesser remains the `/l/` serving/deploy authority through `lesser client install`, Simulacrum owns its client release artifacts, customer custom client source stays tenant-owned, and host does not weaken core Lesser/body consumer release verification.

## Classification

Provisioning, managed-update, consumer-release-verification, security, tenant-isolation, operational-reliability, CSP, governance, docs.

## Surfaces affected

- `internal/store/models`, `internal/store`: instance client state, client deploy jobs, GitHub App bindings, webhook delivery ledger.
- `internal/config`, `internal/secrets`: stage-scoped host-owned GitHub App settings and secret refs.
- `internal/controlplane`: portal APIs for catalog, reset/deploy, GitHub connect/disconnect, webhook ingress, audit visibility.
- `internal/provisionworker`: client deploy job state machine, catalog preflight, CodeBuild polling/receipt ingest.
- `cdk/`: client deploy queues, DLQs, CodeBuild runner split, GitHub App secret access, alarms.
- `cdk/lib/provision-runner` or new runner scripts: trusted catalog deploy and split custom build/deploy paths.
- `web/`: portal and operator UI for managed clients.
- `scripts/managed-client-release-certification`, `scripts/validate`: catalog artifact validation and fixture-gated GitHub/App validation.
- `gov-infra/`: additive verifier/evidence coverage for the new supply-chain and GitHub boundary.
- `docs/`: architecture, release contract, recovery, rollout, and steward coordination docs.

## Key architectural decisions

1. **Use a host-owned GitHub App for v1.** Operators install the stage-scoped `lesser.host` client-deploy app into their chosen repo. Host stores global app credential refs; per-instance bindings store only installation/repository/branch policy and audit metadata.
2. **Drive Lesser's existing `/l/` contract.** Host should call `lesser client install --skip-build` rather than copying Lesser's S3/CloudFront/manifest behavior.
3. **Split custom builds from tenant deploy.** The current provision runner assumes the tenant AWS role in prebuild, so it must not execute customer build commands. Custom GitHub source build runs without tenant AWS credentials; trusted deploy consumes only a bounded build artifact.
4. **Catalog clients are checksum-verified.** Simulacrum/default/reset deployments use a managed client release contract and checksum verification before install.
5. **Custom GitHub clients are tenant-owned artifacts.** They are not host-certified Lesser/body releases. The UI and audit trail must make that boundary explicit.

## Sibling-repo coordination

- **lesser: blocker identified before host runner work.** Lesser confirmed that host should call `lesser client install --skip-build` and not duplicate Lesser's S3/CloudFront/manifest behavior. The Lesser-local blocker is ambient AWS credential support: `lesser client install` must work from a scoped CodeBuild role without requiring `--aws-profile`. Host must pass explicit `--stage`, a non-secret Lesser state receipt containing `ClientBucketName`, `ClientArtifactBucketName`, `ClientInstallManifestKey`, and `FrontendDistributionId`, and a narrow tenant-account client-deploy role.
- **sim: blocker identified before default-client rollout.** Simulacrum confirmed that host should consume immutable built client artifacts plus release metadata/checksums, not a source checkout. Simulacrum must publish a `simulacrum-client-release.json`-style artifact contract, verifier, release packaging path, and clean/guard user-visible `simulacrum.dev` preview fallback data before host uses Simulacrum as the default managed client.
- **body: not required for v1.** No body comm API or lesser-body deploy contract changes are expected.
- **soul: not required for v1.** No soul registry/on-chain/namespace changes are expected.
- **greater: not required for v1.** No greater-components API changes are expected.

## Framework coordination

- **AppTheory: not required initially.** AppTheory routing/SQS/Lambda patterns are sufficient.
- **TableTheory: not required initially.** Existing single-table/GSI modeling patterns are sufficient.
- **FaceTheory: not required initially.** Simulacrum's FaceTheory client release must remain `/l` and CSP-safe, but no framework feature request is known yet.

## External-vendor coordination

- **GitHub:** create/configure the host-owned GitHub App per stage with webhook URL, push event subscription, and contents/metadata read permissions. Installation token permissions should be repository-scoped.
- **AWS:** new SQS/CodeBuild/Secrets Manager/IAM resources; live deploy requires normal operator authorization and no CDK timeouts.
- **Stripe / billing:** not required.
- **SES / comm vendors:** not required.
- **AI providers:** not required.
- **eth_rpc / Safe signers:** not required; no on-chain changes.

## Steward response incorporation

### Lesser response incorporated

- The supported boundary is Lesser's `lesser client install --skip-build`; host must not reimplement `/l/` object publication or CloudFront invalidation semantics.
- Host automation must pass an explicit single stage and a non-secret `lesser up` state receipt. Host should treat `ClientBucketName`, `ClientArtifactBucketName`, `ClientInstallManifestKey`, and `FrontendDistributionId` as required receipt fields.
- Before host depends on CodeBuild automation, Lesser needs ambient AWS credentials/region support for `client install`; `--aws-profile` should remain an optional operator override, not a required automation flag.
- Routine client install only needs `s3:PutObject` to the client artifact install/history paths, active manifest key, client asset prefix `l/_assets/*`, and `cloudfront:CreateInvalidation` scoped to the target distribution. It must not need DynamoDB, Secrets Manager, SSM writes, Lambda, CloudFormation, Route53, IAM mutation, or bootstrap mnemonic access.
- Reset/rollback should prefer reinstalling the exact known-good catalog artifact. Hot activation of an older manifest is optional future Lesser work if host needs receipt-synchronized rollback.

### Simulacrum response incorporated

- Host must consume immutable built artifacts with metadata/checksums and must not clone/build Simulacrum source for catalog/default/reset deployment.
- The expected built shape is `build/server/**`, `build/client/**`, and `facetheory.lesser.json`; host may render only `app_name` and `display_name`, while server/assets paths and handler fields remain checksum-verified invariants.
- Catalog verification must reject path traversal, absolute paths, symlinks/hardlinks, files outside allowed prefixes, missing required entry files, checksum mismatches, and rendered manifest mutations beyond the allowed per-instance fields.
- Simulacrum must remove, derive, or quarantine user-visible `simulacrum.dev` fallback preview URLs before default managed-client promotion.
- Host should accept a `candidate` artifact for lab integration, then promote the same digest to `stable` only after dev/lab install and browser/CSP smoke evidence is green.

### Updated blockers

1. Lesser ambient-credential support for `lesser client install --skip-build` is the runner-work blocker.
2. Simulacrum immutable client artifact packaging/verifier plus preview-domain cleanup is the default-client blocker.
3. Host product choices that remain: initial availability of custom GitHub deploys (recommended: operator allowlist first) and reset semantics (recommended: preserve GitHub binding disabled unless explicitly disconnected).

## Phases

### Phase 0: Coordination and contract freeze

- **Items**: L1, L2, S1, S2, S3, S4, H1, steward briefs.
- **Dependencies**: none.
- **Risks**:
  - Lesser's CLI contract may need small changes before host automation.
  - Simulacrum may not yet publish deploy-ready release artifacts.
- **Exit criteria**:
  - Lesser confirms the stable `lesser client install --skip-build` automation surface and rollback/receipt contract.
  - Simulacrum confirms the managed client artifact contract or returns repo-local change requirements.
  - The host-owned GitHub App choice is recorded as the v1 product boundary.

### Phase 1: Governance and release-verification baseline

- **Items**: H2, H3, H4, H5.
- **Dependencies**: Phase 0 contract answers for final schema details.
- **Risks**:
  - Silent weakening of supply-chain gates if client artifacts are treated as ordinary downloads.
  - Confusion between host-certified catalog artifacts and tenant-owned custom artifacts.
- **Exit criteria**:
  - Managed client release schema and certification scripts validate known-good fixtures and reject checksum/path/schema mismatches.
  - Gov verifier additions are additive only and emit evidence.

### Phase 2: Data, config, and GitHub App foundation

- **Items**: H6, H7, H8, H9, H10.
- **Dependencies**: Phase 1 schema names stable enough to reference.
- **Risks**:
  - Storing credential material in DynamoDB instead of refs.
  - Unbounded binding queries crossing instance ownership.
- **Exit criteria**:
  - TableTheory models and accessors are slug/owner bounded.
  - Host-owned GitHub App credentials load from stage-scoped refs.
  - Installation tokens are short-lived, repo-scoped, never persisted, and redacted from logs/tests.

### Phase 3: Portal APIs and webhook ingress

- **Items**: H11, H12, H13, H14.
- **Dependencies**: Phase 2 models/accessors and GitHub auth service.
- **Risks**:
  - Treating callback `installation_id` as proof instead of verifying server-side.
  - Webhook replay or branch/ref confusion causing unintended deploys.
- **Exit criteria**:
  - No-paste onboarding flow: start install, callback state validation, server-side installation verification, server-derived repo candidates, exact repo+branch binding.
  - Webhook requires valid `X-Hub-Signature-256`, exact `push` event, exact `refs/heads/<branch>`, dedupe ledger, and safe ignored outcomes for non-matching events.

### Phase 4: Infrastructure and runner split

- **Items**: H15, H16, H17, H18, H19, H20.
- **Dependencies**: Phase 1 verification scripts and Phase 2 config.
- **Risks**:
  - Custom client build accidentally runs after assuming tenant AWS credentials.
  - Tenant deploy role is broader than needed.
  - Build artifacts leak source content or secrets into host logs/artifacts.
- **Exit criteria**:
  - CDK adds queues, DLQs, alarms, and separate runner paths.
  - Catalog runner verifies release artifacts and runs trusted deploy.
  - Custom build runner has no tenant AWS role; trusted deploy runner installs only validated artifact inventory.
  - Buildspec tests assert the tenant role is not assumed before custom source build.

### Phase 5: Provision-worker client deploy state machine

- **Items**: H21, H22, H23, H24.
- **Dependencies**: Phase 4 runner paths.
- **Risks**:
  - Client deploy races with core Lesser/body/MCP managed updates.
  - Default-client failure leaves provisioned tenant infrastructure without clear recovery.
- **Exit criteria**:
  - `ClientDeployJob` supports catalog deploy, reset, and custom GitHub deploy.
  - New provisions install default Simulacrum before marking ready, with recoverable failure state.
  - Reset is idempotent, disables custom auto-triggering unless explicitly re-enabled, and never touches tenant data/body/soul/instance keys.

### Phase 6: Portal/operator UX

- **Items**: H25, H26.
- **Dependencies**: Phase 3 APIs and Phase 5 job responses.
- **Risks**:
  - UI loosens CSP through GitHub embeds/scripts.
  - Operator views expose raw logs, tokens, repo source, or PII.
- **Exit criteria**:
  - Portal supports catalog selection, reset, deploy history, GitHub connect/disconnect, and branch display.
  - Operator support views show safe job/delivery metadata only.
  - Web build remains strict-CSP compatible with no inline script/style additions.

### Phase 7: Validation, docs, and rollout evidence

- **Items**: H27, H28, H29.
- **Dependencies**: all behavior implemented.
- **Risks**:
  - Fixture-only validation could miss the real published artifact path.
  - GitHub webhook validation could be untested against live delivery headers.
- **Exit criteria**:
  - Fixture-gated harness covers HMAC handling, redaction, no-paste onboarding, catalog checksum mismatch abort, custom GitHub build/deploy in a disposable repo, and reset.
  - Recovery runbooks cover disable binding, rotate GitHub App credentials, retry failed deploy, reset to Simulacrum, and previous client install rollback.
  - Gov-infra evidence is fresh and committed with the verifier output.

## Stage rollout plan for host

### Lab

- **Command**: `AWS_PROFILE=<lab-profile> theory app up --stage lab --execute`
- **Timeout discipline**: no timeout on deploy; let CloudFormation finish or roll back.
- **Soak duration**: at least one full successful disposable test-slug cycle plus a webhook-triggered redeploy cycle.
- **Soak criteria**:
  - `go test ./...`, `cd cdk && npm test && npm run synth`, `cd web && npm run lint && npm run typecheck && npm test && npm run build`, and `bash gov-infra/verifiers/gov-verify-rubric.sh` pass.
  - Known-good Simulacrum catalog artifact installs to `/l/` for a disposable lab slug.
  - Known-bad/mismatched Simulacrum artifact aborts before deploy.
  - GitHub App can connect a disposable repo/branch and a push enqueues exactly one client deploy.
  - Custom build logs and CodeBuild environment do not expose GitHub tokens, webhook signatures, AWS credentials, or repo source content beyond safe build output.
  - Reset returns the lab slug to Simulacrum and disables custom auto-deploy.
  - `/l/`, `/l/_assets/*`, `/l/identity` or equivalent client routes, and `/auth/login` remain healthy.

### Live

- **Command**: `AWS_PROFILE=<live-profile> theory app up --stage live --execute`
- **Authorization**: explicit Aron/operator approval after lab soak.
- **Post-deploy monitoring**:
  - control-plane Lambda error rate and latency;
  - provision-worker queue depth, DLQ count, and active client deploy age;
  - CodeBuild failure rate by mode: catalog, custom-build, custom-deploy;
  - CloudFront 4xx/5xx for portal/API;
  - GitHub webhook rejected/ignored/queued counts;
  - secret-redaction verifier/evidence freshness;
  - canary `/l/` response health for selected slug.

## On-chain rollout plan

No on-chain changes. No contracts, Safe-ready payloads, Sepolia/mainnet deploys, mint-signer handling, or TipSplitter changes are expected.

## Managed-instance rollout plan

1. **Lab disposable slug**: exercise catalog install, custom GitHub deploy, reset, disconnect, and previous-install rollback.
2. **Live internal canary slug**: enable managed client deploy for one operator-owned managed instance; observe at least one catalog reset and one custom GitHub deploy from a disposable repo.
3. **New-instance default**: enable Simulacrum default on new live provisions after canary success.
4. **Existing managed instances**: expose reset/deploy UI as opt-in; do not automatically alter existing `/l/` clients without operator/customer action unless Aron explicitly chooses a migration campaign.
5. **Broader custom GitHub availability**: enable after webhook delivery ledger and credential-rotation runbook are proven in live canary.

## Release artifact plan

- **Host release**: standard host release/PR with gov-infra evidence and release notes.
- **Simulacrum release**: requires a managed-client release artifact published by `equaltoai/simulacrum` before default/reset can be live-enabled.
- **Lesser release**: may require a confirmed or updated Lesser CLI release if `lesser client install --skip-build` needs host-specific automation stability or receipt fields.
- **Managed consumer impact**: host consumes Simulacrum and Lesser artifacts; host must verify exact published artifacts through the real consumer path before declaring readiness.

## Rollback plan

- **Portal/API feature rollback**: disable managed-client feature flags/config, revert code, redeploy host through normal lab/live flow.
- **Webhook rollback**: disable GitHub App webhook processing or mark all bindings disabled; keep delivery ledger for audit.
- **Catalog client rollback**: run client reset to previous catalog release or use Lesser's active install rollback procedure if available.
- **Custom client rollback**: disable the binding/branch trigger, run reset to Simulacrum or previous install, and rotate GitHub App credentials if exposure is suspected.
- **CDK rollback**: revert commit and redeploy; retain stateful queues/buckets/tables per stage policy.
- **On-chain rollback**: n/a.

## AGPL posture

- No proprietary blobs in host.
- Catalog/client artifacts consumed from published GitHub Releases with source available in the producer repos.
- New Go/TS dependencies, if any, require AGPL-compatible license vetting.
- Custom customer client source is tenant-owned input processed by the deployment pipeline; host does not import it into host source or certify it as EqualToAI code.

## Steward briefs

Prepared separately in `docs/steward-briefs-managed-client-deployment-2026-05-24.md`.

## Open questions

1. Host product: should custom GitHub builds be enabled for all paid managed instances at launch, or initially only by operator allowlist? Current recommendation: operator allowlist first, then broaden after live canary.
2. Host product: should reset preserve GitHub binding disabled-by-default, or fully disconnect by default? Current recommendation: preserve disabled unless explicit disconnect.
3. Host implementation: should host persist the Lesser state receipt from provisioning as the canonical client-install input, or should a later Lesser SSM/install-target resolver become the preferred source? Current recommendation: persist and pass the non-secret receipt for v1.
