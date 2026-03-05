# Sepolia Contracts Deployment Runbook (TipSplitter + Soul Registry)

Goal: deploy the full on-chain contract set to **Ethereum Sepolia** (chainId `11155111`) and wire `lesser-host` to use
the new addresses so the full application can be validated end-to-end.

This runbook assumes:
- You are deploying **new, non-upgradeable** contracts (no proxies).
- Contract ownership is assigned to the **admin Safe** (`INITIAL_OWNER`) at deploy time.
- Deployments are sent by an **EOA deployer** (the `DEPLOYER_PRIVATE_KEY`), but that EOA is *not* the contract owner.

## 0) Contract set

Production contracts in this repo (`lesser-host/contracts/contracts/`):
- `SoulRegistry.sol` (v2 — burn, transfer tracking, avatar support)
- `ReputationAttestation.sol`
- `ValidationAttestation.sol`
- `TipSplitter.sol` (configured to use `SoulRegistry` for `tipAgent*` calls)
- `EtherealBlobRenderer.sol` (avatar renderer — style 0)
- `SacredGeometryRenderer.sol` (avatar renderer — style 1)
- `SigilRenderer.sol` (avatar renderer — style 2)

Libraries / interfaces (deployed as part of renderers, not standalone):
- `ISoulAvatarRenderer.sol`
- `SoulPRNG.sol`
- `SoulSVGUtils.sol`

Not deployed (test-only):
- `AgentIdHarness.sol`
- `Mock*.sol`

## 1) Preconditions

- Deployer EOA has enough Sepolia ETH for 7 deployments + a buffer.
- You have the wallet addresses you will use in the same roles as historical Sepolia:
  - `INITIAL_OWNER` = admin Safe (contract owner)
  - `LESSER_WALLET` = Lesser fee recipient wallet (1% recipient in `TipSplitter`)
- You have a Sepolia RPC URL:
  - Either use your direct RPC URL in `SEPOLIA_RPC_URL`, or set it to an internally managed URL.
- Decide the Soul claim-window policy:
  - `SOUL_CLAIM_WINDOW_SECONDS=0` means souls are immediately soulbound after mint.

## 2) Pre-flight: local verification (recommended)

From repo root:

```bash
cd contracts
npm ci
npm test
npm run lint
cd ..
bash gov-infra/verifiers/gov-verify-rubric.sh
```

Notes:
- `npm run lint` emits warnings (natspec / gas suggestions). Treat as informational unless your policy says otherwise.
- `gov-verify-rubric.sh` runs Slither (SEC-1) and will FAIL if Solidity SAST finds medium+ issues.

## 3) Configure deployment env

Create or update `contracts/.env`:

```bash
SEPOLIA_RPC_URL=...
DEPLOYER_PRIVATE_KEY=...

# TipSplitter constructor args
LESSER_WALLET=0x...
INITIAL_OWNER=0x...

# SoulRegistry constructor args
SOUL_CLAIM_WINDOW_SECONDS=0
```

Sanity-check required vars are present without printing secrets:

```bash
cd contracts
node -e 'import("dotenv/config"); const req=["SEPOLIA_RPC_URL","DEPLOYER_PRIVATE_KEY","LESSER_WALLET","INITIAL_OWNER","SOUL_CLAIM_WINDOW_SECONDS"]; let ok=true; for (const k of req){ if(!process.env[k]){ console.error("missing",k); ok=false; } } process.exit(ok?0:1);'
```

## 4) Deploy (correct sequence)

Two-phase deployment process.

### Phase 1 — contract deployments (single command)

Deploy order:
1. `SoulRegistry` (v2 — with burn, transfer tracking, avatar support)
2. `ReputationAttestation`
3. `ValidationAttestation`
4. `TipSplitter` (points `agentIdentityRegistry` at the deployed `SoulRegistry`)
5. `EtherealBlobRenderer`
6. `SacredGeometryRenderer`
7. `SigilRenderer`

Command:

```bash
cd contracts
npm run deploy:sepolia:all
```

Expected output:
- contract addresses + deployment tx hashes for all 7 contracts
- read-only sanity checks (owners, lesserWallet, agentIdentityRegistry)
- `setRenderer` Safe transaction instructions for Phase 2
- a JSON snippet with suggested `cdk/cdk.json` context updates

### Phase 2 — Safe multisig transactions (setRenderer)

The deployer EOA is not the contract owner, so renderer registration requires Safe multisig transactions:

- `SoulRegistry.setRenderer(0, <EtherealBlobRenderer>)` — Ethereal Blob
- `SoulRegistry.setRenderer(1, <SacredGeometryRenderer>)` — Sacred Geometry
- `SoulRegistry.setRenderer(2, <SigilRenderer>)` — Sigil

Execute these via the Safe web UI or Safe SDK.

### Standalone renderer deployment (optional)

If you need to deploy only the renderers (e.g., against an existing SoulRegistry):

```bash
cd contracts
SOUL_REGISTRY_ADDRESS=0x... npm run deploy:sepolia:soul-renderers
```

### Phase 3 — Mint signer setup

The portal mint flow uses `selfMintSoul`: the control plane signs an EIP-712 self-mint attestation with a hot key (stored in SSM), and the Safe (or user) submits the transaction (paying gas + mint fee).

1. **Generate keypair:**
   ```bash
   bash scripts/generate-mint-signer-key.sh
   ```

2. **Store private key in SSM:**
   ```bash
   aws ssm put-parameter \
     --name /lesser-host/soul/lab/mint-signer-key \
     --value <hex-private-key> \
     --type SecureString
   ```

3. **Add attestor via Safe** (required for `selfMintSoul`):
   ```
   SoulRegistry.addAttestor(<address-from-step-1>)
   ```

4. **Set mint fee via Safe** (0.0005 ETH = 5e14 wei; must match control plane default unless you change it):
   ```
   SoulRegistry.setMintFee(500000000000000)
   ```

5. **Optional: set permit mint signer via Safe** (only needed if you use `mintSoul`, not `selfMintSoul`):
   ```
   SoulRegistry.setMintSigner(<address-from-step-1>)
   ```

6. **Verify:**
   ```
   SoulRegistry.isAttestor(<address-from-step-1>) → true
   SoulRegistry.mintFee()                        → 500000000000000
   SoulRegistry.mintSigner()                     → expected address (only if step 5 was done)
   ```

### Record the deployment in-repo

Update the canonical record at `docs/deployments/sepolia/latest.json` with:

- deployed contract addresses + tx hashes
- the SSM parameter names used by `lesser-host` (no secrets)
- required Safe admin calls (renderers, mint fee, attestor)

## 5) Wire `lesser-host` to the new addresses

### 5.1 Update CDK context (lab stage)

Edit `cdk/cdk.json` and update (or add) these keys under `context` for `lab`:

- Tip config:
  - `tipEnabledLab: "true"`
  - `tipChainIdLab: "11155111"`
  - `tipContractAddressLab: "<TipSplitter>"`
  - `tipRpcUrlSsmParamLab: "/lesser-host/api/infura/sepolia"` (or your chosen SSM path)
- Soul config:
  - `soulEnabledLab: "true"`
  - `soulChainIdLab: "11155111"`
  - `soulRegistryContractAddressLab: "<SoulRegistry>"`
  - `soulReputationAttestationContractAddressLab: "<ReputationAttestation>"`
  - `soulValidationAttestationContractAddressLab: "<ValidationAttestation>"`
  - `soulRpcUrlSsmParamLab: "/lesser-host/api/infura/sepolia"` (or your chosen SSM path)

No new CDK context keys needed for renderer addresses — they are registered on-chain via `setRenderer`, not in the control plane config.

Keep your existing Safe addresses and tx modes:
- `tipAdminSafeAddress`, `tipTxMode`
- `soulAdminSafeAddress`, `soulTxMode`

### 5.2 Ensure SSM RPC param exists

Confirm the parameter referenced by `tipRpcUrlSsmParamLab` / `soulRpcUrlSsmParamLab` exists in AWS SSM Parameter Store
(SecureString recommended).

### 5.3 Deploy the control plane

Use your normal deploy flow for stage `lab` (examples):

```bash
AWS_PROFILE=... theory app up --stage lab
```

or:

```bash
cd cdk
npm ci
AWS_PROFILE=... npx cdk deploy --all -c stage=lab --require-approval never
```

## 6) Post-deploy validation checklist

### 6.1 Control-plane config endpoints

- `GET /api/v1/tip-registry/config`
  - returns `enabled: true`, `chain_id: 11155111`, `contract_address: <TipSplitter>`
- `GET /api/v1/soul/config`
  - returns `enabled: true`, `chain_id: 11155111`, `registry_contract_address: <SoulRegistry>`

### 6.2 Tip registry smoke test

Follow `docs/testing-plan.md` section **7) Tip Registry (Sepolia)**.

Minimum checks:
- create a host registration operation (Safe payload generated)
- execute Safe tx(s) to register + activate host
- tip via client integration using `/api/v1/tip-registry/config` discovery

### 6.3 Soul registry smoke test

Minimum checks:
- registration begin/verify flow completes (`/api/v1/soul/agents/register/*`)
- verify response returns a `mint_tx` payload (permit-based) with `to`, `value`, `data`, `chain_id`, `deadline`
- user submits `mintSoul` tx with the permit and mint fee (0.0005 ETH)
- `TipSplitter.tipAgentETH` works once `SoulRegistry.getAgentWallet(agentId)` is non-zero

### 6.4 Soul registry v2 features

Additional checks for the new features:
- `burnSoul` reverts when called by non-owner (sanity check)
- `transferCount(agentId)` returns `0` for a freshly minted token
- `tokenURI(agentId)` returns the metaURI before renderers are registered, and returns `data:application/json;base64,...` after `setRenderer` is called
- Each renderer's `renderAvatar(1)` returns valid SVG (call directly on the renderer contract)

## 7) Roll-forward / re-deploy policy

Contracts are non-upgradeable:
- If you need to change behavior, deploy new contracts and update `cdk/cdk.json` + consumers.
- Avoid reusing old addresses in configs after a new deploy; treat address changes as a staged migration.
