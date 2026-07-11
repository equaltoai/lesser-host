# Deployments

This directory is the canonical place to track **latest on-chain contract deployments** and the **required post-deploy admin calls** (Safe-ready calldata).

In the public repo, deployment manifests are gitignored by default (see `.gitignore`) because they often include
environment-specific operational details.

Conventions:

- One subdirectory per network (example: `docs/deployments/sepolia/`).
- `latest.json` is the stable pointer that should always reflect the current “active” deployment for that network.
- Keep a Safe Transaction Builder import file alongside `latest.json` when there are required owner/admin calls.
- Keep `latest.json` free of secrets (SSM parameter *names* are OK; values are not).
- OffchainResolver ENS gateway operations use the canonical lab/live runbook at
  `docs/ens-offchain-resolver.md`. That runbook distinguishes historical Sepolia records from the Project 44 target
  templates (`https://lab.lesser.host/resolve?sender={sender}&data={data}` for lab and
  `https://lesser.host/resolve?sender={sender}&data={data}` for live).
- Resolver addresses are stage-specific CDK context, not legacy SoulRegistry config:
  `ensGatewayResolverAddressLab` maps to lab trust API `ENS_GATEWAY_RESOLVER_ADDRESS`, and
  `ensGatewayResolverAddressLive` maps to live trust API `ENS_GATEWAY_RESOLVER_ADDRESS`. The legacy generic
  `ensGatewayResolverAddress` is a lab-only migration fallback.
- Mainnet runtime reconnection records are operational Evidence, not source defaults. Keep any recovered-runtime
  companion or `docs/deployments/mainnet/latest.json` local/ignored, sanitize it to public addresses, code hashes,
  source-verification references, parameter names, template hashes, and IAM projection only, and follow
  `docs/runbooks/soul-mainnet-runtime-reconnection.md` for the activation/rollback checklist.
- Never record RPC values, Mint-signer material, raw InstanceKeys, signed payload bodies, full transaction bodies, PII, or
  tenant transcripts in deployment manifests.
- When deploying new contracts:
  1. Deploy contracts (Hardhat scripts in `contracts/`).
  2. Update your local `docs/deployments/<network>/latest.json` with addresses + tx hashes + required Safe calls.
  3. Update `cdk/cdk.context.local.json` (not committed) to point `lesser-host` at the new addresses.
  4. Deploy `lesser-host` for the relevant stage.
