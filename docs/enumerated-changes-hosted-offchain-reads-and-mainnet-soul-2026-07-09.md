# Enumerated Changes: Hosted/off-chain reads and recovered mainnet Soul configuration

## Inputs and ordering decision

This list implements the approved need in
`docs/scoped-need-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md` and incorporates the completed Soul registry and
InstanceKey audits. The source changes are deliberately small. Runtime activation is an operator action because the
real live values belong in ignored `cdk/cdk.context.local.json`, not tracked defaults.

The hosted/off-chain correction must be deployed and proved while the live Registry address is still empty. Only then
may the recovered mainnet configuration be activated. Otherwise the configuration would hide the defect that this work
is meant to correct.

Implementation starts from a clean feature worktree at refreshed current `main`, and the feature PR targets `staging`,
per `AGENTS.md` and `docs/release-branching.md`. It waits for the independent MicroVM correction to finish so item 3 can
extend its stable E2E gate without bundling that runtime correction.

### 1. Record the approved investigation, scope, enumeration, and roadmap

- **Paths**:
  - `docs/hosted-offchain-conversation-read-investigation-2026-07-09.md`
  - `docs/scoped-need-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md`
  - `docs/enumerated-changes-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md`
  - `docs/roadmap-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md`
- **Surface**: docs
- **Classification**: docs / bug-fix / operational-reliability / soul-registry / on-chain-integrity
- **Governance-rubric impact**: none; no Pack, Verifier, Evidence-policy, or threshold change
- **Multi-tenant-isolation impact**: none; the documents require preservation of the existing per-Slug boundary
- **On-chain impact**: off-chain planning only; no Solidity, transaction, signer, or contract-state change
- **Trust-API / CSP / instance-auth impact**: preserves; the plan requires strict `sha256(raw_key)` InstanceKey
  authentication before configuration disclosure
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic; no framework change or workaround
- **Acceptance**: the four documents agree on the root cause, narrow source fix, direct-wallet mint activation effect,
  two-deployment ordering, and operator-only live context boundary.
- **Validation**: `git diff --check`; Markdown link/path review; secret-pattern review; and
  `bash gov-infra/verifiers/gov-verify-rubric.sh` with fresh Evidence before the PR is handed off
- **Conventional Commit subject**: `docs(soul): plan off-chain reads and mainnet reconnection`

### 2. Decouple hosted/off-chain agent conversation reads from Registry configuration

- **Paths**:
  - `internal/controlplane/handlers_soul_mint_conversation_instance_read.go`
  - `internal/controlplane/handlers_soul_instance_bootstrap_registry_gate_internal_test.go`
- **Surface**: `internal/controlplane`
- **Classification**: bug-fix / security / tenant-isolation / soul-registry / operational-reliability / test-coverage
- **Governance-rubric impact**: none; existing security and quality Verifiers continue to apply unchanged
- **Multi-tenant-isolation impact**: none -- preserves strict InstanceKey, domain, instance Slug, agent-ID, and
  Slug-qualified session checks; negative boundary tests receive elevated scrutiny
- **On-chain impact**: off-chain only; `requireSoulRegistryConfigured()` remains unchanged for genuine on-chain routes
- **Trust-API / CSP / instance-auth impact**: preserves InstanceKey authentication; no Trust API, attestation, or CSP
  change
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic AppTheory and TableTheory usage; no local framework patch
- **Acceptance**: agent-scoped list and single-get return `200` for an authorized hosted/off-chain identity when
  `SoulEnabled=true`, `SoulChainID=0`, and Registry/RPC are empty; missing or revoked keys return `401` before the
  `SoulEnabled` check, an authenticated caller sees fail-closed behavior when Soul is disabled, cross-tenant access
  remains `403`, list output remains bounded and metadata-only, and a real on-chain preflight still rejects an empty
  Registry.
- **Validation**:
  1. Author and run the new regressions first, observing the current `409`; then make the narrow helper correction and
     commit tests plus fix together only after the commit is green.
  2. `go test -count=1 ./internal/controlplane -run 'Test(P0_)?SoulInstance.*(HostedOffchain|MintConversationReads|RegistryRequired)'`
     plus the exact new list/get test names if the aggregate expression does not select them.
  3. `go build ./...`; `go test ./...`; `go vet ./...`; `test -z "$(gofmt -l .)"`.
  4. The repository-pinned golangci-lint job and `bash gov-infra/verifiers/gov-verify-rubric.sh` with fresh Evidence.
- **Conventional Commit subject**: `fix(soul): decouple hosted conversation reads from registry`

### 3. Automate the agent-scoped list/get rollout proof

- **Paths**:
  - `scripts/hosted-genesis-microvm-e2e-gate.sh`
  - `docs/hosted-genesis-microvm-lab-canary.md`
- **Surface**: scripts / docs
- **Classification**: test-coverage / security / tenant-isolation / soul-registry / operational-reliability
- **Governance-rubric impact**: none; the existing gate and Verifiers are extended without lowering a threshold
- **Multi-tenant-isolation impact**: none -- the gate uses one governed lab InstanceKey and adds assertions rather than
  cross-tenant traversal
- **On-chain impact**: off-chain only
- **Trust-API / CSP / instance-auth impact**: preserves; the raw InstanceKey remains in the existing gitignored runtime
  credential file and is never printed, persisted, or placed in fixtures
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic; no framework change
- **Acceptance**: after the existing lab gate creates a new Hosted Genesis conversation through the supported flow, it
  calls the agent-scoped list endpoint, proves that the new conversation ID is present, calls single-get and proves the
  same ID and agent under the fixture's InstanceKey, and rejects transcript/declaration/private fields in list output.
  The gate is noninteractive for this proof and requires no manual Host API conversation-creation or cleanup call.
- **Validation**: `bash -n scripts/hosted-genesis-microvm-e2e-gate.sh`; the CI-safe local/stub phase; targeted Go tests;
  the deployed `--stage lab` gate after the independent MicroVM correction is healthy; secret/log review; and
  `bash gov-infra/verifiers/gov-verify-rubric.sh` with fresh Evidence
- **Conventional Commit subject**: `test(soul): automate hosted conversation read proof`

### 4. Define and guard the operator-local mainnet Soul reconnection gate

- **Paths**:
  - `cdk/cdk.context.local.json.example`
  - `cdk/test/lesser-host-stack-soul-runtime.test.ts`
  - `docs/runbooks/soul-mainnet-runtime-reconnection.md`
  - `docs/deployments/README.md`
- **Surface**: cdk / docs
- **Classification**: docs / operational-reliability / soul-registry / on-chain-integrity / security
- **Governance-rubric impact**: none; no Pack, Verifier, or committed generated-Evidence change
- **Multi-tenant-isolation impact**: none; runtime values are stage-specific and introduce no cross-tenant access
- **On-chain impact**: on-chain-reaching configuration only; no Solidity change, deployment, transaction, or Safe-ready
  payload is created by this commit
- **Trust-API / CSP / instance-auth impact**: none; Trust API receives existing configuration keys through existing CDK
  behavior, with no auth, attestation, or CSP change
- **Consumer-release-verification impact**: none
- **Framework consumption**: idiomatic; existing AppTheory deployment contract is unchanged
- **Acceptance**: a focused CDK test uses synthetic values to prove live stage-specific chain/contracts/RPC/Mint-signer/
  Safe/capability projection, exact RPC and signer SSM grants, and TipSplitter/ENS-disabled posture without committing
  real deployment values. The example contains placeholder-only live Soul keys, and the runbook gives an executable
  atomic chain-1 preflight/activation/rollback procedure using the recovered Registry, attestation contracts, Google
  RPC SSM parameter, existing Mint-signer parameter, 2-of-2 Safe, Safe transaction mode, and supported capabilities.
  It keeps TipSplitter and ENS disabled, excludes renderer runtime keys, never records secret values, avoids the
  potentially disclosed Infura credential, discloses direct-wallet `selfMintSoul` signing despite Safe mode, requires operator
  acknowledgement, and forbids rollout-time signing or broadcast.
- **Validation**: `python3 -m json.tool cdk/cdk.context.local.json.example >/dev/null`; `cd cdk && npm ci && npm test`;
  diagnostic lab/live synth with synthetic and then operator-local context; `git diff --check`; secret-pattern review;
  `git check-ignore cdk/cdk.context.local.json docs/deployments/mainnet/latest.json`; and
  `bash gov-infra/verifiers/gov-verify-rubric.sh` with fresh Evidence
- **Conventional Commit subject**: `test(cdk): guard Soul runtime reconnection`

## Non-commit operator checkpoints

These are roadmap actions, not synthetic Git commits:

1. **Contractless correctness deployment** -- from the approved clean source SHA, deploy to lab and then live with the
   Registry/RPC still absent. Prove list/get through the ordinary Lesser conversation flow or an automated canary; no
   manual Host API cleanup or conversation-creation call is a prerequisite.
2. **Mainnet configuration activation** -- apply the reviewed real values only to ignored
   `cdk/cdk.context.local.json`, update the ignored local `docs/deployments/mainnet/` record, synth/diff the exact
   environment and scoped SSM IAM grants, repeat read-only mainnet preflights, obtain explicit operator authorization,
   and run `AWS_PROFILE=Lesser theory app up --stage live --execute` without a timeout. Do not invoke a signing route or
   send a transaction.
3. **Post-activation Evidence** -- retain the source/template hashes, public code hashes and addresses, Safe state,
   parameter names, CloudFormation result, contractless and configured read proofs, and monitoring outcome in the
   canonical ignored deployment record and durable steward memory. Never record RPC values, Mint-signer material, raw
   InstanceKeys, signed payload bodies, or tenant transcripts.

## No-change surface matrix

- **Lambda entrypoints (`cmd/`)**: unchanged.
- **Control plane**: only the shared agent-read context helper and focused tests change; route registration, list/get
  response code, projections, audit behavior, rate limits, and other Soul guards remain unchanged.
- **Trust API (`internal/trust/`)**: unchanged.
- **Store/models/schema (`internal/store/`)**: unchanged; no migration or cleanup.
- **Secrets/config packages**: unchanged; no new parameter, credential, or raw-secret handling.
- **Other `internal/soul*` and domain packages**: unchanged.
- **CDK implementation/defaults**: `cdk/lib/**`, `cdk/bin/**`, and tracked `cdk/cdk.json` remain unchanged; real values
  remain operator-local.
- **Web/CSP**: unchanged.
- **Contracts**: unchanged; Hardhat, Slither, solhint, Sepolia deploy, and mainnet contract execution are not triggered
  by this scope.
- **Scripts**: only the lab E2E assertion extension changes after the independent MicroVM work is complete;
  `managed-release-certification`, `managed-release-readiness`, and MicroVM runtime behavior remain unchanged.
- **Gov-infra rubric**: `gov-infra/pack.json`, Verifiers, policies, and thresholds are unchanged; fresh Evidence is still
  required.
- **Dependencies and deploy contract**: Go/npm locks and `app-theory/app.json` are unchanged.
- **OpenAPI and public schemas**: unchanged; the list/get contract already omits a Registry-driven `409`.
- **Repository policy docs**: `AGENTS.md`, `README.md`, and `CONTRIBUTING.md` are unchanged.
- **Sibling repositories**: no code changes.
- **Excluded runtime features**: TipSplitter, ENS, renderer keys, Mint-signer rotation, and Infura reuse remain out of
  scope.

## Enumeration self-check

- [x] Every source item is in Host's mission and fits one independently green commit.
- [x] The bug is developed test-first without landing a deliberately failing commit.
- [x] No item weakens the Gov-infra rubric or bypasses Evidence.
- [x] No item traverses the tenant boundary or loosens InstanceKey authentication.
- [x] No Solidity or Consumer release verification change is present.
- [x] No Trust API, attestation, or CSP behavior is loosened.
- [x] CDK tests/docs, Go behavior, and the dependent E2E gate remain isolated in separate commits.
- [x] No secret, raw key, seed phrase, signed payload, full transaction body, PII, or proprietary blob enters Git.
- [x] No destructive cloud, on-chain, tenant, or repository action is enumerated.
- [x] The full list meets the scoped need without making mainnet configuration the off-chain bug fix.
