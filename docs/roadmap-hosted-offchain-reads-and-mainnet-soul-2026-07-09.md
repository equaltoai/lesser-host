# Roadmap: Hosted/off-chain reads and recovered mainnet Soul configuration

## Goal

Restore the intended boundary between Host-backed conversation reads and optional on-chain assurance, then reconnect
the verified recovered Ethereum mainnet Soul contracts without allowing that configuration to mask the bug. The first
live deployment proves agent list/get while the Registry is absent. A later, separately authorized configuration-only
deployment activates the recovered chain-1 values from an isolated, approved source state and sends no transaction.

## Classification

Bug-fix / security / tenant-isolation / soul-registry / on-chain-integrity / operational-reliability / test-coverage /
docs.

## Surfaces affected

- `internal/controlplane/handlers_soul_mint_conversation_instance_read.go`
- focused `internal/controlplane` registry-boundary tests
- the Hosted Genesis lab E2E gate and its canary instructions
- operator-local, ignored Soul runtime context in `cdk/cdk.context.local.json`
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

### Phase 2: Prove contract independence in lab

- **Items**: operator checkpoint 1
- **Dependencies**:
  - Items 1-4 are merged to `staging`; pin the exact `staging` SHA.
  - Deploy from an isolated clean checkout using an exact deliberately contractless lab Soul context:
    `SoulEnabled=true`, `SoulChainID=0`, and empty-string RPC, Registry, ReputationAttestation, and
    ValidationAttestation addresses. Never substitute the syntactically valid EVM zero address; assert the synthesized
    Lambda environment before deployment.
  - The independent MicroVM correction is already the reviewed/deployed baseline from Phase 0.
- **Exercise**:
  1. Deploy with `AWS_PROFILE=Lesser theory app up --stage lab --execute`; never set a timeout.
  2. Run the extended governed lab gate. It creates a hosted/off-chain conversation, proves the new ID appears in agent
     list, proves single-get returns that same conversation, and proves list is metadata-only. No manual Host API
     conversation-creation or data-deletion call is required.
  3. Use the committed tests/CI evidence for missing-key, revoked-key, disabled-Soul, cross-tenant, metadata-only,
     bounded-list, and genuine on-chain fail-closed cases; do not manufacture cross-tenant live data.
  4. Confirm no raw InstanceKey, transcript, provider credential, or signed payload appears in logs.
  5. Restore the normal verified Sepolia lab context, redeploy through AppTheory, and repeat the successful read path to
     show that the correction also coexists with configured assurance.
- **Soak**: after both lab variants pass, observe at least two hours with no new Control plane failures, unexpected
  `401`/`403` posture, cross-Slug anomalies, or Hosted Genesis lifecycle regression.
- **Risks**: configuration could accidentally mask the bug, or an unhealthy MicroVM flow could be mistaken for a
  list/get failure.
- **Controls**: contractless variant first, separate lifecycle and read-route evidence, then Sepolia restoration.

### Phase 3: Promote and deploy the correction to live with the Registry absent

- **Items**: operator checkpoint 1 completion
- **Dependencies**:
  - Lab acceptance and soak are complete.
  - The operator promotes `staging` to `main`; `main` accepts only the `staging` PR and uses its normal branch checks.
    Do not rerun `gov-rubric` as a staging-to-main promotion gate.
  - Pin the approved `main` SHA. Any `v*` tag is manual and operator-owned.
  - Use the captured **pre-activation** live Soul context: `SoulEnabled=true`, existing chain value retained, and empty
    Registry/RPC/attestation addresses. Do not use the already prepared chain-1 context for this deployment.
  - The independently deployed MicroVM correction must still be healthy in live.
- **Pre-deploy gate**:
  - tracked and staged diffs empty; no untracked deploy input;
  - ignored context explicitly reviewed;
  - synth/template hash recorded;
  - CDK diff contains only the reviewed source deployment and no unrelated MicroVM, worker, Gov-infra, framework,
    web, TipSplitter, or ENS change.
- **Deploy**: with fresh explicit operator authorization, run
  `AWS_PROFILE=Lesser theory app up --stage live --execute` from the pinned checkout without a timeout.
- **Acceptance and soak**:
  - An ordinary Lesser-created hosted/off-chain conversation appears in agent list/get while the Registry remains
    empty. This is the decisive proof that configuration did not fix the bug.
  - Observe at least two hours with zero valid-request `soul_mint.conflict` responses on these reads, no tenant-boundary
    anomaly, and no related Control plane error increase.
  - Do not delete conversation data merely to make the canary pass.
- **Risks**: deploying from prepared mainnet context would invalidate the root-cause proof; a source rollback would
  reintroduce the known `409`.
- **Controls**: exact pre-activation context, template diff, ordinary user flow, and a mandatory boundary before Phase 4.

### Phase 4: Activate the recovered Ethereum mainnet configuration separately

- **Items**: operator checkpoint 2
- **Dependencies**:
  - Phase 3 is accepted and its contractless-live Evidence is retained.
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
  - repeat hosted/off-chain list/get and confirm it remains independent of Registry availability;
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
    chain proof, Safe state, parameter **names**, environment/IAM projection, CloudFormation completion, contractless and
    configured read proofs, and monitoring outcome;
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
- **Sequence**: contractless Soul context first; extended automated Hosted Genesis list/get gate; restore Sepolia
  context; repeat; then soak.
- **Soak duration**: minimum two hours after both variants pass.
- **Soak criteria**: successful list and single-get without Registry; normal conversation lifecycle on the independently
  corrected MicroVM baseline; existing Sepolia-backed behavior healthy; no auth, tenant, log-secrecy, or worker
  regression.

### Live

- **Commands**: two separate invocations of
  `AWS_PROFILE=Lesser theory app up --stage live --execute`, each with no timeout:
  1. approved code with the pre-activation empty Registry;
  2. the same pinned source with recovered chain-1 operator context.
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
  AppTheory. For mainnet configuration, restore the captured pre-activation live context and redeploy the same pinned
  source. Never modify deployed environment variables manually.
- **On-chain rollback**: no rollout transaction exists to reverse. Any future user-submitted mint is immutable and is
  outside this configuration rollback; forward governance would be required for later on-chain changes.
- **Governance-rubric rollback**: not applicable; the rubric is unchanged.
- **Managed-update per-Slug rollback**: not applicable.
- **Stateful resources**: do not delete Lambda rollback versions, SSM parameters, KMS keys, DynamoDB tables, S3 buckets,
  CloudFront distributions, Route53 zones, contracts, or deployment records.

## Risk register

| Risk | Mitigation | Blocker condition |
|---|---|---|
| Registry configuration masks the defect | Mandatory contractless lab and live deployments before activation | No contractless-live proof |
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
3. Select the operator-owned deployment window and, if desired, manual `v*` release tag after `staging` promotion.
4. Assign the separate Infura rotation follow-up; it does not block Google-backed activation.
5. The repository-wide classification of legacy `requireSoulRegistryConfigured()` call sites remains a separate need.

## Handoff

If this roadmap is approved, implement one milestone at a time through `implement-milestone`. Do not create a GitHub
Project unless the principal requests one. Deployment and mainnet configuration activation remain separate,
operator-authorized actions after source review and lab Evidence.
