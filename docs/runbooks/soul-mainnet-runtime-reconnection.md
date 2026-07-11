# Soul mainnet runtime reconnection runbook

This runbook is the operator checklist for Phase 4 of
`docs/roadmap-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md`. It reconnects
host to the recovered Ethereum mainnet Soul registry runtime after the source fix has already proved hosted/off-chain
agent conversation list/get with the Registry absent. It is a configuration-only rollout: no contract is deployed, no
Safe transaction is created, and no signing or broadcast endpoint is exercised during the rollout.

## Required operator acknowledgement

Before activation, the operator must explicitly acknowledge this boundary:

- setting the mainnet Registry, RPC parameter, and Mint-signer parameter enables host to prepare signed direct-wallet
  `selfMintSoul` payloads and `SoulOperation` records for qualifying authorized registrations even when
  `SOUL_TX_MODE=safe`;
- host does not broadcast those transactions in this rollout;
- this rollout must not call any route that signs, submits, records execution for, or broadcasts a Soul transaction.

Without that acknowledgement, stop before changing context or deploying.

## Reviewed live context

Apply these values atomically in the ignored operator-local `cdk/cdk.context.local.json`. Do not commit that file and do
not copy secret values into Evidence.

| Key | Value |
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
| `tipEnabledLive` | `false` |
| `ensGatewayEnabledLive` | `false` |

Keep TipSplitter and ENS disabled. Do not add a TipSplitter runtime override, renderer runtime key, recovered Infura
credential, RPC value, Mint-signer material, raw InstanceKey, tenant content, signed payload body, or transaction body to
tracked files, logs, or Evidence.

## Evidence-only inventory to re-probe

The following recovered public values are evidence and probe inputs only. Do not turn them into runtime context keys unless
a separate roadmap explicitly adds that surface.

| Item | Public value | Runtime posture |
|---|---|---|
| SoulRegistry Mint-signer | `0x1fee9b85f98ceAe1468D0A4DCD9dd6D8C0B2EC2e` | Must match derived signer; private key never printed |
| SoulRegistry mint fee | `500000000000000` wei | Probe only |
| SoulRegistry claim window | `0` | Probe only |
| EtherealBlobRenderer | `0xd46B05D6EC73962E57Be03eCd5B1f4a09d5Cb61E` | Probe only; no renderer runtime key |
| SacredGeometryRenderer | `0x80E0f4bC842e376f3C728703DbcD163aBe792b6d` | Probe only; no renderer runtime key |
| SigilRenderer | `0xdaF6c0691d23862f50d833523deA7C85F7cD61C6` | Probe only; no renderer runtime key |
| TipSplitter | `0xdBCC6fe65D47690703C9d842A4eFB11EF46b0a0D` | Disabled; no TipSplitter override |
| OffchainResolver | `0xC4a9887D8F095E85ADfaE40bD528B7a9D2D7C9A2` | Disabled; `ensGatewayEnabledLive=false` |

## Atomic preflight

Run these checks from the pinned clean `main` SHA approved for activation. Stop on any mismatch.

1. Confirm the AWS account and region are the intended live host account/region.
2. Read the RPC SSM parameter by name only; do not print the value. Use it in-process to prove `eth_chainId == 1` and
   that a current block can be read.
3. Verify bytecode and expected code hashes at the Registry, ReputationAttestation, and ValidationAttestation addresses.
4. Verify ownership, unpaused state, and source verification for all three contracts.
5. Verify the Safe owners and threshold remain 2-of-2 for `0xfE63333F303D4f7b2354f7E3eca752C812D65907`.
6. Derive only the Mint-signer public address from the SSM parameter and prove it matches the Registry signer and has
   `isAttestor=true`; do not print or persist the private key.
7. Repeat the live DynamoDB Soul-state classification scan. Block on unexplained `immutable_onchain`, mixed-chain
   references, or pre-existing `SoulOperation` state that needs reconciliation.
8. Synthesize the exact live template and prove the Control plane receives the RPC and Mint-signer SSM parameter names,
   while the Trust API receives only the RPC parameter name. Confirm TipSplitter and ENS remain disabled and the
   Render worker receives no Soul runtime keys.
9. Require a configuration-only CDK diff. Any source, IAM beyond the reviewed SSM projection, TipSplitter, ENS,
   renderer, web, worker, or unrelated environment delta blocks activation.

## Activation

After a fresh explicit operator authorization, deploy with the AppTheory contract only:

```bash
AWS_PROFILE=Lesser theory app up --stage live --execute
```

Never set a timeout on the deploy. Let CloudFormation finish or roll back. Do not use direct CDK deploy commands for
activation and do not manually edit deployed Lambda environment variables.

## Post-deploy verification

1. Read back Lambda environment **names** and IAM projection without resolving secrets.
2. Repeat the chain, current-block, code/hash, ownership, paused-state, Safe, signer, and attestor probes.
3. Repeat hosted/off-chain agent conversation list/get and confirm it remains independent of Registry availability.
4. Confirm TipSplitter and ENS remain disabled.
5. Confirm the rollout created no `SoulOperation`, signed payload, mint, transfer, broadcast, or Safe transaction.
6. Observe active metrics for two hours, then passively review 24 hours of Control plane/Trust API errors, CloudFront
   4xx/5xx, SNS errors, RPC latency/failures, SSM/KMS failures, auth posture, unexpected `SoulOperation` creation, and
   observable on-chain submissions.

## Rollback

Restore the captured pre-activation live context with empty Registry/RPC/attestation addresses and redeploy the same
pinned source through `theory app up --stage live --execute` without a timeout. Do not delete Lambda versions, SSM
parameters, KMS keys, DynamoDB tables, S3 buckets, CloudFront distributions, Route53 zones, contracts, or deployment
records, and do not patch environment variables manually.

## Evidence allowlist

Evidence may include the pinned source SHA, synthesized-template hash, public addresses and code hashes,
source-verification links, chain proof, Safe owner/threshold state, parameter names, IAM projection, CloudFormation
completion, contractless/configured read proofs, and monitoring outcome.

Evidence must not include RPC values, Mint-signer material, raw InstanceKeys, provider credentials, signed payload bodies,
full transaction bodies, PII, or tenant transcripts. Store the local mainnet record under ignored
`docs/deployments/mainnet/` according to `docs/deployments/README.md`.
