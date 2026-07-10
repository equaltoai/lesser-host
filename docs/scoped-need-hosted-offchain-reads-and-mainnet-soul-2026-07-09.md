# Scoped Need: Hosted/off-chain conversation reads and recovered live Soul configuration

## Background

Hosted/off-chain Soul conversations are Host-backed. `HostedGenesisSession` is the durable source of truth, while the
Ethereum Soul registry provides optional immutable assurance. An older agent-read helper retained an obsolete
SoulRegistry prerequisite, causing valid hosted/off-chain list/get requests to return `409 soul_mint.conflict`.

Separately, Host has a recovered, Safe-owned Ethereum mainnet Soul deployment that is not connected to live runtime
configuration. These are related surfaces but independent changes: restoring a contract must never become the fix for
an off-chain correctness bug.

## Driver

Principal-direct production incident and explicit operator request to restore verified recovered mainnet Soul details.

## Problem

1. Agent-scoped list/get endpoints incorrectly make Host-state reads depend on an EVM contract.
2. Live currently combines `SOUL_CHAIN_ID=8453` with empty Registry/RPC/attestation contract configuration, while the
   recovered deployment is on Ethereum mainnet chain `1`.
3. Applying the contract merely to suppress the 409 would conceal the hosted/off-chain boundary defect.

## Surface affected

- Control plane API:
  - `GET /api/v1/soul/instance/agents/{agentId}/mint-conversations`
  - `GET /api/v1/soul/instance/agents/{agentId}/mint-conversations/{conversationId}`
- Instance API key authentication and tenant boundary
- Existing Hosted Genesis lab E2E gate, for noninteractive list/get proof after conversation creation
- Soul registry CDK/operator-local runtime configuration
- Focused CDK runtime-projection tests and operator context examples
- Live `control-plane-api` and `trust-api` environments
- Existing Ethereum mainnet read and Safe-ready transaction-preparation paths

## Lambda(s) affected

- `control-plane-api`
- `trust-api` only through propagation of recovered Soul configuration
- No worker behavior change in the narrow scope

## Classification

Bug fix, operational reliability, multi-tenant-sensitive InstanceKey authentication, Soul registry boundary correction,
and on-chain-reaching configuration restoration.

## Narrowest-scope proposal

This need must be delivered as two independent milestones.

### Milestone 1: correct the hosted/off-chain read boundary

Change runtime behavior only in `requireMintConversationInstanceReadContext()`, add focused tests, and extend the
existing governed lab gate:

1. Remove the unconditional `requireSoulRegistryConfigured()` call.
2. Require:
   - initialized Host Store;
   - valid, non-revoked InstanceKey resolved strictly as `sha256(raw bearer)`;
   - only after authentication, `SoulEnabled=true`;
   - valid agent ID and existing off-chain `SoulAgentIdentity`; and
   - verified domain/instance ownership.
3. Do not require chain ID, Registry address, RPC, or Mint-signer.
4. Preserve response limits, metadata-only list projection, private-transcript rules, rate limiting, hashed logging, and
   audit events.
5. Preserve full Registry/RPC guards on genuinely on-chain preflight, mint, rotation, execution-recording, and
   Safe-ready operations.

Focused regression coverage:

- list succeeds with `SoulEnabled=true`, chain `0`, and an empty contract;
- single get succeeds under the same configuration;
- `SoulEnabled=false` still fails closed;
- unauthenticated and revoked keys still return `401` rather than revealing configuration;
- cross-tenant identity remains `403`;
- list remains metadata-only and bounded; and
- an actual on-chain route still rejects missing Registry configuration.

After the independent MicroVM correction lands, the existing lab gate must use the conversation it creates to assert
that agent list contains the ID, single-get returns the same conversation, and list remains metadata-only. This removes
any manual Host API conversation-creation or cleanup prerequisite without bundling MicroVM runtime changes here.

### Milestone 2: reconnect the recovered Ethereum mainnet Soul contracts

Apply all Soul-runtime-relevant values atomically as **live-specific** operator context:

| Context key | Value |
|---|---|
| `soulEnabledLive` | `"true"` |
| `soulChainIdLive` | `"1"` |
| `soulRegistryContractAddressLive` | `0x60FBa71F84BD613118D38F7d0375c36693dAecbA` |
| `soulReputationAttestationContractAddressLive` | `0xE690D736B2c84D550F07aF60cDe1bC9e742C8a9F` |
| `soulValidationAttestationContractAddressLive` | `0x45c50CD0DA080Ae8F934CAD21a9fE30A0fe1aAF4` |
| `soulRpcUrlSsmParamLive` | `/lesser-host/api/google/rpc/mainnet` |
| `soulMintSignerKeySsmParamLive` | `/lesser-host/soul/live/mint-signer-key` |
| `soulAdminSafeAddressLive` | `0xfE63333F303D4f7b2354f7E3eca752C812D65907` |
| `soulTxModeLive` | `"safe"` |
| `soulSupportedCapabilitiesLive` | `"social,commerce"` |

Tracked guardrails use synthetic values to test stage-specific Control plane/Trust API environment projection, exact
RPC/Mint-signer SSM grants, and TipSplitter/ENS-disabled posture. The tracked local-context example gains placeholder
live keys only. Exact recovered values remain in the ignored operator context and are proved by the deployment
synth/diff rather than hardcoded into tracked CDK defaults or tests.

The recovered record names `/lesser-host/api/infura/mainnet`. During read-only validation, the resolved provider URL
appeared in an internal error trace and must be treated as potentially disclosed. It must not be reused until rotated.
The existing Google mainnet SecureString was independently checked without exposing its value and returned chain ID
`1` plus current blocks, so it is the safe activation candidate. The Infura parameter rotation remains a separate
credential-hygiene follow-up.

The remaining recovered deployment values stay in deployment/on-chain Evidence rather than this Soul-only activation:

- TipSplitter `0xdBCC6fe65D47690703C9d842A4eFB11EF46b0a0D`, its Safe owner, Lesser wallet, token allowlist,
  and host registration remain a separately scoped financial configuration restoration; `tipEnabledLive` remains false
  and no stage-specific Tip override is added here;
- OffchainResolver `0xC4a9887D8F095E85ADfaE40bD528B7a9D2D7C9A2` remains disabled through
  `ensGatewayEnabledLive=false`; and
- EtherealBlobRenderer `0xd46B05D6EC73962E57Be03eCd5B1f4a09d5Cb61E`, SacredGeometryRenderer
  `0x80E0f4bC842e376f3C728703DbcD163aBe792b6d`, and SigilRenderer
  `0xdaF6c0691d23862f50d833523deA7C85F7cD61C6` remain on-chain Evidence because Host has no
  renderer-address runtime keys.

The recovered SoulRegistry Evidence also records Mint-signer
`0x1fee9b85f98ceAe1468D0A4DCD9dd6D8C0B2EC2e`, mint fee `500000000000000` wei, claim window `0`,
unpaused contracts, and Safe ownership. Those values are re-probed and recorded, not duplicated as unsupported CDK
runtime keys.

Enabling or otherwise changing TipSplitter or ENS runtime behavior remains explicitly out of scope.

Preflight evidence already establishes:

- expected AWS live account and region;
- alternate RPC returns Ethereum chain ID `1`;
- Registry, ReputationAttestation, and ValidationAttestation bytecode is present and source-verified;
- Registry and both attestation contracts are owned by the expected 2-of-2 Safe and are unpaused;
- Mint-signer SecureString derives the Registry signer and `isAttestor(signer) == true`;
- Registry fee, claim window, renderers, and executed Safe setup match the recovered deployment; and
- live off-chain state contains no `immutable_onchain` identity or existing Soul operation requiring migration.

Before deployment, synth/diff must prove the exact live environment and SSM IAM grant. Deployment must occur from an
isolated, reviewed source state through:

```bash
AWS_PROFILE=Lesser theory app up --stage live --execute
```

with no timeout and only after the required lab validation/soak boundary. The current dirty feature worktree is not an
acceptable live deployment source because it would bundle unrelated MicroVM and Gov-infra changes.
This is a procedural branch/release gate; the deploy wrapper does not mechanically reject a dirty or unapproved source
tree, so the operator and steward must enforce it before execution.

Activation is not behaviorally read-only even though the rollout itself sends no transaction. Connecting the Registry
and existing Mint-signer enables the existing qualifying registration flow to create a `SoulOperation` and return a
Mint-signer-signed, **direct-wallet** `selfMintSoul` payload. `SOUL_TX_MODE=safe` does not convert that mint payload into
a Safe transaction; Host does not broadcast it, but an authorized user can deliberately submit it. The operator must
acknowledge this activation effect before live configuration is changed, read-only rollout probes must avoid signing
routes, and post-deploy monitoring must detect unexpected `SoulOperation` creation or on-chain submission.

## What this need explicitly does not cover

- No Lesser repository changes.
- No manual API calls as a prerequisite for ordinary conversation creation.
- No further DynamoDB deletion or broad failed-conversation cleanup.
- No MicroVM credential/watchdog implementation; that is separate active work.
- No Solidity change, contract redeploy, rollout-time mint, transfer, broadcast, or new Safe transaction. The existing
  direct-wallet self-mint payload flow becomes available after Milestone 2 as disclosed above.
- No Mint-signer rotation.
- No tracked `cdk/cdk.json` value or CDK implementation change; real deployment values stay operator-local.
- No broad rewrite of every Soul handler still using `requireSoulRegistryConfigured()`; classify those separately.
- No `TipSplitter` activation or `tipEnabledLive=true`; that is a distinct financial/product rollout.
- No ENS gateway activation; that is a separate Trust API/governance rollout.
- No runtime context for renderer addresses; they are already registered on-chain.
- No Gov-infra verifier weakening, Pack exception, or evidence bypass.

## Success criteria

1. Valid InstanceKey list/get calls work when Registry address, chain ID, and RPC are absent.
2. The same calls retain strict hashed-key authentication and tenant isolation.
3. On-chain operations continue to fail closed without their Registry/RPC prerequisites.
4. Milestone 1 passes targeted tests, `go test ./...`, `go vet ./...`, formatting, and the Gov-infra rubric.
5. The extended governed lab gate creates a conversation and proves agent list/get without a manual Host API call.
6. A synthetic-value CDK test guards live stage-specific environment/IAM projection and disabled TipSplitter/ENS.
7. Synthesized live Soul configuration resolves to chain `1`, the three recovered contracts, the safe RPC parameter,
   the existing Mint-signer parameter, the 2-of-2 Safe, and Safe transaction mode.
8. Lab-equivalent configuration behavior is validated and soaked before live deployment.
9. Post-deploy configuration and read-only RPC probes confirm chain `1` and exact addresses without invoking a signing
   route or sending a transaction.
10. Hosted/off-chain reads remain operational even if contract configuration is later withdrawn.
11. Deployment Evidence records addresses, deployment/code hashes, source verification, Safe ownership/setup
   transaction, and post-deploy probes.

## Specialist routing

- Governance rubric: not touched; no new Verifier currently needed
- Provisioning / Managed update / Consumer release verification: not touched
- Soul registry: required via `evolve-soul-registry`
- Trust API / CSP / instance-auth: focused `audit-trust-and-safety` walk required for InstanceKey preservation
- Framework consumption: idiomatic; no AppTheory/TableTheory workaround
- Advisor brief: n/a

## Consumer impact

- Lesser: its existing self-scope list/get integration starts working without manual cleanup or contract dependency.
- Managed-instance operators: no workflow change.
- Hosted/off-chain agents: private conversation reads become available under the intended InstanceKey boundary.
- On-chain actors: the rollout performs no contract or state mutation; Milestone 2 also re-enables the existing
  direct-wallet self-mint signing path for qualifying, authorized registrations.
- Body, greater, and public Trust API readers: no schema change.

## Multi-tenant isolation impact

Elevated scrutiny is required because the endpoints expose private hosted-genesis state. Preserve all of the following:

- strict InstanceKey hash lookup and revoked-key rejection;
- domain-to-instance ownership verification;
- Slug-qualified `HostedGenesisSession` lookup;
- agent-ID match;
- bounded, metadata-only list projection; and
- no raw key or transcript logging.

## On-chain impact

- Milestone 1: off-chain only.
- Milestone 2: reconnects Host to existing Ethereum mainnet contracts but performs no rollout-time on-chain mutation.
  It enables existing signed direct-wallet self-mint payload generation; Host still does not broadcast.
- Safe ownership and `soulTxMode=safe` remain mandatory for the operations that already use Safe-ready payloads, but
  `soulTxMode=safe` does not make `selfMintSoul` Safe-mediated.

## AGPL posture

No change. No proprietary artifacts, compiled-only contracts, or secret material enter the repository.

## Specialist audit summaries

### Soul-registry audit

- Solidity contracts: unchanged; no Hardhat/Slither/solhint delta is introduced by these milestones.
- On-chain Go code: unchanged; Milestone 2 changes only validated runtime configuration, which makes the existing
  signed direct-wallet self-mint path reachable.
- Off-chain state: no schema change or migration; live scan found one `hosted_offchain` identity and zero Soul
  operations.
- Safe-ready governance: no new payload is required because the rollout sends no on-chain mutation; existing setup used
  the expected 2-of-2 Safe. Existing self-mint payloads remain intentionally direct-wallet rather than Safe-ready.
- Mint-signer: storage and key are unchanged; only its existing SSM parameter is connected to the verified chain-1
  Registry.
- Namespace: no JSON-LD URL or semantic change.
- Verdict: audit clean for enumeration, with configuration deployment kept separate from code implementation.

### Trust-and-safety / InstanceKey audit

- Trust API and attestation shapes: unchanged.
- Instance-auth validation: strict `sha256(raw_key)` lookup, revoked-key handling, and one-time raw-key posture remain
  unchanged.
- Tenant access: domain/Slug/agent checks remain unchanged.
- Audit and rate limits: preserved.
- CSP, web, safety, and AI Evidence: untouched.
- Security effect: removing the pre-auth registry guard eliminates a configuration oracle; unauthenticated callers
  reach the intended `401` path.
- Required tests: empty-contract success plus unauthenticated, revoked-key, and cross-tenant regressions.
- Verdict: audit clean; no governance change or auth loosening is required.

## Open questions

- The repository-wide set of legacy `requireSoulRegistryConfigured()` call sites needs a separate classification audit;
  it must not silently expand this incident fix.
- The potentially disclosed Infura credential should be rotated through a controlled operator flow even if the Google
  RPC parameter is used for Soul activation.

## Handoff

Two milestones are required. Proceed to `enumerate-changes`; implement the off-chain guard correction first. Prepare
the verified operator-local mainnet context independently, but do not deploy it from the current dirty feature branch.
