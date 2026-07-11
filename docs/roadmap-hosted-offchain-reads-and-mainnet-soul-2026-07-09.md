# Roadmap: Hosted/off-chain reads and recovered mainnet Soul configuration

## Goal

Restore the intended boundary between Host-backed conversation reads and optional on-chain assurance, then reconnect
the verified recovered Ethereum mainnet Soul contracts without letting on-chain configuration become a prerequisite for
hosted/off-chain reads. The boundary is proven at the source/test layer and by the configured lab Hosted Genesis MicroVM
E2E canary, not by temporarily disconnecting deployed lab or live contract references. The mainnet activation remains a
separately authorized configuration-only deployment from an isolated, approved source state and sends no transaction.

## Classification

Bug-fix / security / tenant-isolation / soul-registry / on-chain-integrity / operational-reliability / test-coverage /
docs.

## Surfaces affected

- `internal/controlplane/handlers_soul_mint_conversation_instance_read.go`
- focused `internal/controlplane` registry-boundary tests
- the Hosted Genesis lab E2E gate and its canary instructions
- operator-local, ignored Soul runtime context in `cdk/cdk.context.local.json` for final Sepolia/mainnet values only;
  temporary contractless lab/live runtime deployments are explicitly out of scope
- Control plane and Trust API synthesized environments and SSM IAM projections, through existing CDK behavior
- `cdk/cdk.context.local.json.example`
- `cdk/test/lesser-host-stack-soul-runtime.test.ts` with synthetic runtime values
- Soul deployment and recovery runbooks under `docs/`
- ignored local deployment Evidence under `docs/deployments/mainnet/`

There is no Store/schema, Trust API handler, CDK implementation/default, web/CSP, Solidity, provisioning, Managed
update, Consumer release verification, dependency, or Gov-infra rubric change.

## Sibling-repo coordination

- **lesser**: awareness and integration validation required, but no code change. Use its existing conversation client to
  prove that an ordinary newly created hosted/off-chain conversation appears through agent list/get without a manual
  Host API call or cleanup step.
- **body**: not required; no communication API or release-contract change.
- **soul**: not required; no JSON-LD namespace, URL, shape, or semantic change.
- **greater**: not required; no SPA component or package change.
- **sim**: optional, non-blocking validation of the existing public surfaces after rollout; no code dependency.

Coordination remains KB-first and non-blocking. No sibling repository is edited from this roadmap.

## Framework coordination

- **AppTheory**: not required; consumption remains idiomatic.
- **TableTheory**: not required; existing queries and strict InstanceKey lookup are retained.
- **FaceTheory**: not required; the web surface is unchanged.

## External-vendor coordination

- **Stripe / billing**: none.
- **SES / email vendors**: none.
- **AI providers**: no configuration change; an available lab model is needed only for the ordinary conversation
  canary.
- **eth_rpc provider**: revalidate the existing Google mainnet SSM parameter without exposing its value. The recovered
  Infura credential is treated as potentially disclosed, must not be reused, and is rotated separately.
- **Safe multisig signers**: verify owners and 2-of-2 threshold; no signer action is required because this rollout does
  not create or execute a Safe transaction.

## Phases

### Phase 0: Isolate source and preserve the independent MicroVM work

- **Items**: prerequisite for items 1-4
- **Dependencies**:
  - First complete the independent Hosted Genesis MicroVM correction currently isolated on
    `theorymcp/equaltoai/host/hosted-genesis-microvm-api-proof` through its own review and rollout. Record its PR and
    deployed SHA and require evidence that the MicroVM uses refreshed execution-role `cfg.Credentials` without
    same-role `sts:AssumeRole`, retains per-turn Store/client initialization, and durably reaches a terminal failed
    state through the controller/worker/watchdog path when Store initialization fails.
  - Refresh current `main` after that correction is promoted, then create a new isolated feature worktree/branch from
    that exact SHA, as required by `AGENTS.md` and `docs/release-branching.md`. The feature PR still targets `staging`.
  - Do not reset, clean, amend, or deploy the current dirty Hosted Genesis MicroVM/Gov-infra/web worktree.
  - Copy only the four planning documents into the isolated worktree.
  - Record the source SHA and require a clean tracked index before any synth or deployment.
- **Dependency rationale**: the list/get helper correction is source-independent, but the reproducible **new
  conversation** gate extends the script being changed by the separate MicroVM work. Waiting for its reviewed baseline
  avoids a collision and makes the user's no-manual-API acceptance reproducible. No MicroVM runtime change enters this
  branch.
- **Risks**:
  - Unrelated dirty source could be bundled into a Lambda/CDK asset because the deploy wrapper does not itself enforce
    clean Git state.
  - Editing the active E2E script before that work lands could collide with the MicroVM correction.
- **Controls**: separate worktree, pinned SHA, clean-status gate, path-limited diff, and prerequisite MicroVM closeout.

### Phase 1: Land the narrow source correction, automated proof, and runtime guardrails

- **Items**: 1, 2, 3, and 4
- **Dependencies**:
  - Item 1 records the approved baseline.
  - For item 2, author and run the list/get regressions first, observe the current `409`, then change only
    `requireMintConversationInstanceReadContext()` and commit tests plus fix together once green.
  - The helper order is Store/request validation -> strict InstanceKey authentication -> `SoulEnabled` ->
    identity/domain/Slug/agent access. This prevents a configuration oracle.
  - Item 3 extends the stable E2E gate to prove agent list/get for the conversation it creates; no credential is copied
    into fixtures or logs.
  - Item 4 may be prepared in parallel after item 1. It uses synthetic values in the CDK test and placeholders only in
    tracked example context; exact recovered values stay operator-local and are proved by synth/diff.
- **PR and branch gate**:
  - Feature branch targets `staging`.
  - Required review, the existing `gov-rubric` check, and all seven parallel CI jobs (`go-test`, `golangci-lint`,
    `cdk-synth`, `contracts-compile`, `slither`, `web-build`, and `contract-verify`) must pass with the branch current.
  - Do not merge to `main`, tag, release, or deploy live as the steward.
- **Risks**:
  - Moving `SoulEnabled` ahead of authentication would retain the configuration oracle.
  - Removing or weakening the shared Registry guard itself would expose genuine on-chain routes.
  - Committing real context would publish environment-specific operational state and could expose secret material.
  - An integration gate that omits agent-scoped list/get would leave rollout dependent on an ad hoc manual call.
- **Controls**: focused negative tests, unchanged on-chain fail-closed control, extended E2E assertions, synthetic CDK
  projection/IAM tests, full validation, placeholder-only tracked context, and exact no-change review.

### Phase 2: Prove deployed lab MicroVM conversation acceptance

- **Items**: operator checkpoint 1, corrected 2026-07-11 after operator rejection of temporary contract disconnects
- **Dependencies**:
  - Items 1-4 and the E2E gate hardening are merged to `staging`; pin the exact `staging` SHA.
  - The independent MicroVM correction is already the reviewed/deployed baseline from Phase 0.
  - Lab remains configured with the real verified Sepolia Soul runtime references. Do not blank, replace, or disconnect
    real deployed contract references merely to prove a helper boundary.
- **Exercise**:
  1. Assert the deployed lab Control plane and Trust API are using the expected Sepolia Soul context by reading Lambda
     environment names/addresses only; do not resolve or print secret values.
  2. Run the extended governed lab gate against the deployed lab. It creates a hosted/off-chain conversation through the
     real Hosted Genesis MicroVM path, proves the new ID appears in agent list, proves single-get returns that same
     conversation, proves list is metadata-only, and exercises the kill-VM recovery arc. No manual Host API
     conversation-creation or data-deletion call is required.
  3. Use committed tests/CI evidence for the registry-independence boundary: missing-key, revoked-key, disabled-Soul,
     cross-tenant, metadata-only, bounded-list, and genuine on-chain fail-closed cases. Do not manufacture cross-tenant
     live data and do not use a temporary runtime disconnect as evidence.
  4. Confirm no raw InstanceKey, transcript, provider credential, or signed payload appears in harness output or
     relevant logs.
- **Current evidence**: on 2026-07-11 from `staging` SHA `775a42c`, the configured lab Hosted Genesis gate passed:
  accept returned `202`, assistant turn reached ready, declaration extraction reached `declaration_ready`, agent list/get
  returned the created conversation with metadata-only list projection, kill-VM recovery surfaced `failed`, and retry
  accept returned `202`.
- **Soak**: observe at least two hours after the configured lab gate with no new Control plane failures, unexpected
  `401`/`403` posture, cross-Slug anomalies, or Hosted Genesis lifecycle regression.
- **Risks**: an unhealthy MicroVM flow could be mistaken for a list/get failure; over-focusing on optional on-chain
  configuration can distract from whether MicroVM-driven conversations actually work.
- **Controls**: source-level registry-independence tests, configured-lab MicroVM E2E proof, metadata-only assertions,
  and no deploy-level contract-reference churn.

### Phase 3: Promote only after lab MicroVM acceptance and an actual live reason

- **Items**: operator checkpoint 1 completion
- **Dependencies**:
  - Configured lab MicroVM acceptance and soak are complete.
  - The operator promotes `staging` to `main`; `main` accepts only the `staging` PR and uses its normal branch checks.
    Do not rerun `gov-rubric` as a staging-to-main promotion gate.
  - Pin the approved `main` SHA. Any `v*` tag is manual and operator-owned.
  - The live Soul context remains the operator-reviewed production context. Do not deploy live with deliberately blank
    Registry/RPC/attestation references just to manufacture a no-user proof.
- **Pre-deploy gate**:
  - tracked and staged diffs empty; no untracked deploy input;
  - ignored context explicitly reviewed and confirmed to preserve intended live references;
  - synth/template hash recorded;
  - CDK diff contains only the reviewed source deployment and no unrelated MicroVM, worker, Gov-infra, framework,
    web, TipSplitter, ENS, or Soul runtime-reference churn.
- **Deploy**: only with fresh explicit operator authorization and a real live acceptance reason, run
  `AWS_PROFILE=Lesser theory app up --stage live --execute` from the pinned checkout without a timeout.
- **Acceptance and soak**:
  - If a live canary instance exists, an ordinary Hosted Genesis conversation appears in agent list/get through that
    instance's normal InstanceKey path. If no live agents/users/canary instance exist, do not invent customer data or
    treat absence of users as rollout evidence; retain lab E2E plus source-level tests as the acceptance basis.
  - Observe at least two hours with zero valid-request `soul_mint.conflict` responses on these reads, no tenant-boundary
    anomaly, and no related Control plane error increase.
  - Do not delete conversation data merely to make a canary pass.
- **Risks**: a source rollback would reintroduce the known `409`; a synthetic live deployment with no users can waste
  time without improving confidence in MicroVM-driven conversations.
- **Controls**: configured lab MicroVM proof, exact live context review, template diff, optional real canary only, and a
  mandatory boundary before Phase 4.

### Phase 4: Activate the recovered Ethereum mainnet configuration separately

- **Items**: operator checkpoint 2
- **Dependencies**:
  - Phase 3 is accepted and its configured-lab/live-canary Evidence is retained.
  - The operator explicitly acknowledges that activation enables signed direct-wallet `selfMintSoul` payload and
    `SoulOperation` creation for qualifying authorized registrations even when `SOUL_TX_MODE=safe`. Host does not
    broadcast, and this rollout invokes no signing endpoint or transaction.
  - From the same pinned clean `main` SHA, apply this atomic live-only context:

    | Key | Reviewed value |
    |---|---|
    | `soulEnabledLive` | `true` |
    | `soulChainIdLive` | `1` |
    | `soulRegistryContractAddressLive` | `0x60FBa71F84BD613118D38F7d0375c36693dAecbA` |
    | `soulReputationAttestationContractAddressLive` | `0xE690D736B2c84D550F07aF60cDe1bC9e742C8a9F` |
    | `soulValidationAttestationContractAddressLive` | `0x45c50CD0DA080Ae8F934CAD21a9fE30A0fe1aAF4` |
    | `soulRpcUrlSsmParamLive` | `/lesser-host/api/google/rpc/mainnet` |
    | `soulMintSignerKeySsmParamLive` | `/lesser-host/soul/live/mint-signer-key` |
    | `soulAdminSafeAddressLive` | `0xfE63333F303D4f7b2354f7E3eca752C812D65907` |
    | `soulTxModeLive` | `safe` |
    | `soulSupportedCapabilitiesLive` | `social,commerce` |

  - Keep `tipEnabledLive=false` and `ensGatewayEnabledLive=false`; add no TipSplitter override or renderer runtime key.
  - Preserve and re-probe the recovered evidence-only inventory rather than inventing unsupported runtime keys:
    - SoulRegistry Mint-signer `0x1fee9b85f98ceAe1468D0A4DCD9dd6D8C0B2EC2e`, mint fee
      `500000000000000` wei, and claim window `0`;
    - EtherealBlobRenderer `0xd46B05D6EC73962E57Be03eCd5B1f4a09d5Cb61E`, SacredGeometryRenderer
      `0x80E0f4bC842e376f3C728703DbcD163aBe792b6d`, and SigilRenderer
      `0xdaF6c0691d23862f50d833523deA7C85F7cD61C6`;
    - disabled, separately scoped TipSplitter `0xdBCC6fe65D47690703C9d842A4eFB11EF46b0a0D` and OffchainResolver
      `0xC4a9887D8F095E85ADfaE40bD528B7a9D2D7C9A2`.
- **Immediate preflight** (no secret output and no signing route):
  - confirm expected AWS account/region;
  - resolve the Google parameter in-process and prove `eth_chainId == 1` plus current block response;
  - verify code and expected code hashes at all three addresses;
  - verify Registry/Reputation/Validation ownership, unpaused status, and source verification;
  - verify Safe owners and threshold remain 2-of-2;
  - derive only the Mint-signer public address and prove Registry signer match plus `isAttestor=true`;
  - repeat the live DynamoDB classification scan and block on unexplained `immutable_onchain`, mixed-chain references,
    or pre-existing `SoulOperation` state requiring reconciliation;
  - synthesize the exact live template and prove the Control plane receives RPC plus Mint-signer parameter grants while
    Trust API receives only the RPC grant expected by existing CDK;
  - require a configuration-only CDK diff. Any unrelated source, environment, IAM, TipSplitter, or ENS delta blocks.
- **Deploy**: with a new explicit operator authorization for this activation, run
  `AWS_PROFILE=Lesser theory app up --stage live --execute` without a timeout. The rollout sends no transaction and does
  not need a Safe signature.
- **Post-deploy verification**:
  - read back Lambda environment names and scoped IAM projection without resolving secrets;
  - repeat chain, code, owner, paused, Safe, signer/attestor, and current-block probes;
  - repeat hosted/off-chain list/get where a configured canary exists, and rely on source tests for Registry-optional
    read independence rather than disconnecting deployed contracts;
  - confirm TipSplitter and ENS remain disabled;
  - confirm no rollout-time `SoulOperation`, signed payload, mint, transfer, broadcast, or Safe transaction was created.
- **Soak**: two hours of active observation followed by a 24-hour passive review of Control plane/Trust API errors,
  CloudFront 4xx/5xx, SNS errors, RPC latency/failures, SSM/KMS failures, auth posture, unexpected `SoulOperation`
  creation, and observable on-chain submissions.
- **Risks**: chain/address mismatch, mainnet state drift, RPC outage, accidental direct-mint activation surprise, or
  unrelated CDK change.
- **Controls**: atomic context, repeated read-only probes, informed acknowledgement, config-only diff, no signing route,
  and operations/on-chain monitoring.

### Phase 5: Close out Evidence and credential hygiene

- **Items**: operator checkpoint 3
- **Dependencies**: Phase 4 deployment and active observation complete.
- **Evidence**:
  - update ignored `docs/deployments/mainnet/latest.json` or its recovered-runtime companion according to
    `docs/deployments/README.md`;
  - record pinned source SHA, synthesized-template hash, public addresses/code hashes, source-verification references,
    chain proof, Safe state, parameter **names**, environment/IAM projection, CloudFormation completion,
    registry-independence test proof, configured lab MicroVM E2E proof, optional live canary proof, and monitoring
    outcome;
  - append the durable deployment decision/outcome to steward memory;
  - never record the RPC value, Mint-signer material, raw InstanceKey, signed payload body, full transaction body, PII,
    or tenant transcript.
- **Credential follow-up**: rotate the potentially disclosed Infura credential through a separate controlled operator
  flow. This is not a blocker for Google-backed activation, but the old value must not be reused.
- **Risks**: evidence may accidentally capture credentials or tenant content.
- **Controls**: sanitized field allowlist, ignored operational record, secret scan, and no transcript collection.

## Stage rollout plan (Host's own service)

### Lab

- **Command**: `AWS_PROFILE=Lesser theory app up --stage lab --execute`
- **Authorization**: operator-approved lab deployment from the pinned `staging` SHA.
- **Sequence**: preserve verified Sepolia Soul context; run the extended automated Hosted Genesis MicroVM list/get gate;
  then soak.
- **Soak duration**: minimum two hours after the configured lab gate passes.
- **Soak criteria**: normal MicroVM-driven conversation lifecycle; successful list and single-get for the created
  conversation; metadata-only list projection; existing Sepolia-backed behavior healthy; no auth, tenant, log-secrecy,
  or worker regression.

### Live

- **Commands**: live deploys are operator-authorized only. Do not perform a temporary empty-Registry live deployment.
  If Phase 3 has a real live acceptance reason, use one invocation of
  `AWS_PROFILE=Lesser theory app up --stage live --execute` with no timeout. Phase 4's mainnet activation remains a
  separate invocation from the same pinned source with recovered chain-1 operator context.
- **Authorization**: explicit operator authorization is required for each deployment. Phase 4 additionally requires
  informed acknowledgement of direct-wallet mint-signing availability.
- **Post-deploy monitoring plan**: route status/latency, valid-request `soul_mint.conflict`, `401`/`403` posture,
  cross-Slug audit anomalies, Control plane and Trust API errors/throttles, CloudFront 4xx/5xx, SNS errors, SSM/KMS
  access, RPC latency/failure, unexpected `SoulOperation` creation, and observable on-chain submission.

CloudFormation must be allowed to complete or roll back. Never abort a deployment with a timeout.

## On-chain rollout plan

- **Solidity / Sepolia deploy**: none; contracts and bytecode are unchanged. Existing Sepolia is used only for lab
  regression validation.
- **Safe-ready payload**: none for this rollout.
- **Mainnet execution**: none; no signer or Safe transaction is submitted.
- **Post-deploy verification**: read-only source/code/owner/paused/Safe/signer-attestor checks against existing mainnet
  contracts, plus monitoring for unexpected submissions.
- **Off-chain reconciliation**: repeat the state classification scan, activate only runtime references, and perform no
  DynamoDB rewrite or conversation cleanup.

Future contract mutations remain subject to Hardhat, Slither, solhint, Sepolia-first, and Safe-ready governance; this
roadmap does not waive those gates.

## Managed-instance rollout plan

Not applicable. This does not modify the Provisioning worker, Managed update flow, Consumer release verification, or
any per-Slug tenant deployment. There is no canary customer or broader Managed instance rollout.

## Release artifact plan

- **GitHub Release**: no producer artifact or automated release is required. If the operator records this change with a
  `v*` tag, it is cut manually from the approved `main` SHA.
- **Release notes**: state that hosted/off-chain agent list/get no longer requires Registry configuration, authentication
  and response schemas are unchanged, and recovered mainnet configuration is activated separately.
- **Managed-consumer impact**: no Lesser/body release manifest, checksum, certification, or readiness change. Lesser's
  existing client integration is the validation consumer.

## Rollback plan

- **Control plane rollback**: this stack does not publish a Control plane alias/version for an instant version switch.
  Revert the bounded source commit or check out the pinned prior approved SHA and redeploy through
  AppTheory/CloudFormation. Before Phase 4 this may reintroduce the known fail-closed `409` but does not mutate data.
- **CDK stack rollback**: let CloudFormation finish. For the source fix, revert the bounded commit and redeploy through
  AppTheory. For mainnet configuration, restore the captured pre-activation live context with the intended production
  references and redeploy the same pinned source. Never modify deployed environment variables manually.
- **On-chain rollback**: no rollout transaction exists to reverse. Any future user-submitted mint is immutable and is
  outside this configuration rollback; forward governance would be required for later on-chain changes.
- **Governance-rubric rollback**: not applicable; the rubric is unchanged.
- **Managed-update per-Slug rollback**: not applicable.
- **Stateful resources**: do not delete Lambda rollback versions, SSM parameters, KMS keys, DynamoDB tables, S3 buckets,
  CloudFront distributions, Route53 zones, contracts, or deployment records.

## Risk register

| Risk | Mitigation | Blocker condition |
|---|---|---|
| Registry configuration masks the defect | Source-level registry-independence tests plus configured lab MicroVM E2E proof; no temporary contract-reference disconnects | Tests or configured lab MicroVM gate fail |
| InstanceKey or tenant-isolation regression | Auth-before-config ordering plus missing/revoked/cross-tenant tests | Any unexpected status or cross-Slug result |
| Private transcript leakage in list/logs | Preserve metadata-only projection, bounds, and no-secret logging | Transcript/key appears in output or logs |
| Dirty source reaches live | Isolated main-derived worktree, pinned SHA, clean status, exact diff | Any unrelated source/template delta |
| MicroVM failure obscures end-to-end acceptance | Keep correction separate; require healthy ordinary flow before final Milestone 1 acceptance | Cannot complete a normal new conversation |
| Chain, address, signer, or Safe drift | Repeat immediate pre/post read-only probes | Any mismatch, pause, code absence, or threshold drift |
| Direct-mint behavior activated without informed consent | Explicit operator acknowledgement and `SoulOperation`/chain monitoring | Acknowledgement absent |
| RPC credential/outage risk | Use verified Google parameter; rotate and do not reuse Infura | Google probe fails or value exposure occurs |
| CDK/CloudFormation update failure | Config-only diff, retained versions, no timeout, CloudFormation rollback | Stack cannot reach a terminal safe state |
| TipSplitter or ENS is enabled accidentally | Explicit false settings plus synthesized/post-deploy assertions | Either feature becomes enabled |
| Evidence leaks a secret or tenant data | Allowlisted sanitized fields; ignored deployment record; secret review | Sensitive value present |

Every blocker stops progression to the next phase; none is waived for urgency.

## AGPL posture

- **No proprietary blobs**: confirmed; no binary, minified bundle, compiled-only contract, or secret context enters Git.
- **Dependency license vetting**: not applicable; there is no dependency change.
- **Public-source posture**: tracked behavior, tests, and runbook remain AGPL-3.0 source; environment-specific values and
  secrets remain operator-local.

## Advisor-brief authorization

Not applicable. This work is a principal-direct incident and operator request, not an advisor-dispatched brief.

## Open questions and authorization gates

1. **Blocking before Phase 4**: the operator must explicitly acknowledge that connecting the existing Mint-signer
   enables direct-wallet `selfMintSoul` payload generation and `SoulOperation` creation despite Safe mode.
2. **Blocking before Phase 0 exits**: record the separate MicroVM correction's reviewed PR and deployed SHA plus the
   no-self-assume and terminal-failure-persistence Evidence described in Phase 0.
3. Select whether there is a real live acceptance reason/canary before any Phase 3 live deploy, and if desired choose a
   manual `v*` release tag after `staging` promotion.
4. Assign the separate Infura rotation follow-up; it does not block Google-backed activation.
5. The repository-wide classification of legacy `requireSoulRegistryConfigured()` call sites remains a separate need.

## Handoff

If this roadmap is approved, implement one milestone at a time through `implement-milestone`. Do not create a GitHub
Project unless the principal requests one. Deployment and mainnet configuration activation remain separate,
operator-authorized actions after source review and lab Evidence.
