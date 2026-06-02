# ENS OffchainResolver (CCIP-Read) — lab/live deployment runbook

This is the canonical Project 44 M4 runbook for deploying and operating the `OffchainResolver` that serves
`<name>.<instance-slug>.lessersoul.eth` through host's ENS gateway. It is intentionally docs/runbook focused: do **not**
execute the mutating commands in this document from a PR, CI job, or Codex session. Sepolia, mainnet, AWS, ENS, Safe,
and CDK deploy mutations remain operator-run steps with explicit authorization.

Project 44 M5's host-side ENS inventory/backfill tooling and lab/live canary evidence live in
[`docs/project44-m5-ens-backfill-canaries.md`](project44-m5-ens-backfill-canaries.md). M5 does not replace the
operator-run resolver cutover steps in this M4 runbook; if read-only canaries show an ENS registry resolver mismatch,
the fix remains the explicit ENS/Safe operation documented here.

The low-overhead design is one resolver per chain/stage and one existing `/resolve` gateway per host stage. Do not add a
shared runtime router or gateway dispatcher for M4 unless a hard blocker is discovered and documented first.

## Invariants

- Canonical managed ENS names are instance-scoped:

  ```text
  <name>.<instance-slug>.lessersoul.eth
  ```

- Legacy bare managed names (`<name>.lessersoul.eth`) are migration-only compatibility material and fail closed for
  current public discovery/search/gateway material.
- ENS gateway configuration is separate from optional/legacy `SoulRegistry` configuration.
- `lab` resolves `lessersoul.eth` on Sepolia (`chainId=11155111`).
- `live` resolves `lessersoul.eth` on Ethereum mainnet (`chainId=1`).
- The resolver contract stores the Ethereum address derived from the stage's KMS `ENSGatewaySigningKey`; the raw KMS key
  material is never exportable, logged, or committed.

## Stage matrix

| Stage | ENS network | Chain ID | Required gateway URL template | CDK resolver context key | Trust runtime env |
| --- | --- | ---: | --- | --- | --- |
| `lab` | Sepolia | `11155111` | `https://lab.lesser.host/resolve?sender={sender}&data={data}` | `ensGatewayResolverAddressLab` | `ENS_GATEWAY_RESOLVER_ADDRESS` |
| `live` | Ethereum mainnet | `1` | `https://lesser.host/resolve?sender={sender}&data={data}` | `ensGatewayResolverAddressLive` | `ENS_GATEWAY_RESOLVER_ADDRESS` |

The legacy generic `ensGatewayResolverAddress` CDK context key is a **lab-only migration fallback**. It must not be used
for live. The CDK stack maps the stage-specific key into `ENS_GATEWAY_RESOLVER_ADDRESS` on the trust API Lambda. The ENS
gateway rejects requests whose `{sender}` query parameter, or decoded request sender fallback, does not equal that
configured resolver address.

## Contracts

- `contracts/contracts/OffchainResolver.sol`
  - Implements ENSIP-10 `resolve(bytes,bytes)` by reverting with EIP-3668 `OffchainLookup`.
  - Implements `resolveWithProof(bytes response, bytes extraData)` to verify gateway signatures and return resolved
    result bytes.
  - Owner-only admin:
    - `setGatewayUrl(string url)` — update the gateway URL template.
    - `setSigner(address signer)` — rotate signer while keeping `previousSigner` active for zero-downtime cutover.
  - Uses OpenZeppelin `Ownable2Step`; transfer ownership with `transferOwnership(newOwner)`, then the new owner calls
    `acceptOwnership()`.

## Deployment records

`docs/deployments/sepolia/latest.json` preserves historical Sepolia lab deployment evidence. Its existing
`lesser_host.ens_gateway_url` may reference an older direct API Gateway URL. Treat that manifest as historical evidence;
for Project 44 lab/live rollout, the target gateway templates are the stage templates in the stage matrix above. Do not
invent or commit mainnet resolver addresses. Use placeholders until a verified mainnet deployment record exists.

After an operator deploys or updates a resolver, record only non-secret evidence: network, chain id, resolver address,
deployment tx hash, owner/Safe, signer address, gateway URL template, `setResolver` tx/Safe id, and verification output.

## Read-only signer derivation from stage KMS keys

Run these commands from a workstation with read access to the deployed `lesser-host-<stage>-trust-api` Lambda and KMS
`GetPublicKey`. They do not expose secrets; `aws kms get-public-key` returns only the public key for the non-exportable
secp256k1 KMS key.

Install/read dependencies from the contracts package so the Node snippets can import `ethers`:

```bash
cd contracts
npm ci
```

Derive the signer for lab and live:

```bash
# Read-only. Set AWS_PROFILE/AWS_REGION for the target host account before each stage.
export AWS_REGION=us-east-1

derive_ens_gateway_signer() {
  stage="$1" # lab or live
  trust_fn="lesser-host-${stage}-trust-api"

  key_id="$(aws lambda get-function-configuration \
    --function-name "$trust_fn" \
    --query 'Environment.Variables.ENS_GATEWAY_SIGNING_KEY_ID' \
    --output text)"

  if [ -z "$key_id" ] || [ "$key_id" = "None" ]; then
    echo "missing ENS_GATEWAY_SIGNING_KEY_ID on $trust_fn" >&2
    return 1
  fi

  pub_b64="$(aws kms get-public-key \
    --key-id "$key_id" \
    --query PublicKey \
    --output text)"

  STAGE="$stage" KEY_ID="$key_id" PUB_B64="$pub_b64" node --input-type=module <<'NODE'
import { createPublicKey } from "node:crypto";
import { ethers } from "ethers";

const der = Buffer.from(process.env.PUB_B64, "base64");
const jwk = createPublicKey({ key: der, format: "der", type: "spki" }).export({ format: "jwk" });
const x = Buffer.from(jwk.x, "base64url");
const y = Buffer.from(jwk.y, "base64url");
const uncompressed = Buffer.concat([Buffer.from([4]), x, y]);
console.log(`${process.env.STAGE} ENS_GATEWAY_SIGNING_KEY_ID=${process.env.KEY_ID}`);
console.log(`${process.env.STAGE} signer=${ethers.computeAddress(uncompressed)}`);
NODE
}

AWS_PROFILE=<host-lab-profile> derive_ens_gateway_signer lab
AWS_PROFILE=<host-live-profile> derive_ens_gateway_signer live
```

Expected evidence per stage:

- the Lambda environment contains `ENS_GATEWAY_SIGNING_KEY_ID`;
- `aws kms get-public-key` succeeds for that key id;
- the derived Ethereum address exactly matches `OffchainResolver.signer()` for that stage before cutover;
- during rotation, the derived new address may match `signer()` while the old address remains in `previousSigner()` until
  the gateway response TTL has elapsed.

## Read-only resolver and ENS registry inspection

Use this before and after any operator-run mutation. Replace RPC URLs and resolver addresses with operator-verified
values; do not commit them unless they are public deployment evidence.

```bash
cd contracts
export ENS_REGISTRY_ADDRESS=0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e
export ENS_ROOT_NAME=lessersoul.eth
export RPC_URL="$SEPOLIA_RPC_URL" # or MAINNET_RPC_URL for live
export EXPECTED_RESOLVER_ADDRESS="0x<resolver>"
export EXPECTED_GATEWAY_URL="https://lab.lesser.host/resolve?sender={sender}&data={data}"
export EXPECTED_SIGNER_ADDRESS="0x<kms-derived-signer>"

node --input-type=module <<'NODE'
import { ethers } from "ethers";

const provider = new ethers.JsonRpcProvider(process.env.RPC_URL);
const registryAddress = process.env.ENS_REGISTRY_ADDRESS;
const rootName = process.env.ENS_ROOT_NAME;
const expectedResolver = ethers.getAddress(process.env.EXPECTED_RESOLVER_ADDRESS);
const expectedSigner = ethers.getAddress(process.env.EXPECTED_SIGNER_ADDRESS);
const expectedGatewayUrl = process.env.EXPECTED_GATEWAY_URL;
const node = ethers.namehash(rootName);

const registry = new ethers.Contract(registryAddress, [
  "function owner(bytes32 node) view returns (address)",
  "function resolver(bytes32 node) view returns (address)",
], provider);
const resolver = new ethers.Contract(expectedResolver, [
  "function owner() view returns (address)",
  "function pendingOwner() view returns (address)",
  "function gatewayUrl() view returns (string)",
  "function signer() view returns (address)",
  "function previousSigner() view returns (address)",
], provider);

const actualResolver = await registry.resolver(node);
const rootOwner = await registry.owner(node);
const owner = await resolver.owner();
const pendingOwner = await resolver.pendingOwner();
const gatewayUrl = await resolver.gatewayUrl();
const signer = await resolver.signer();
const previousSigner = await resolver.previousSigner();

console.log(JSON.stringify({
  rootName,
  node,
  rootOwner,
  registryResolver: actualResolver,
  resolverOwner: owner,
  resolverPendingOwner: pendingOwner,
  gatewayUrl,
  signer,
  previousSigner,
}, null, 2));

if (ethers.getAddress(actualResolver) !== expectedResolver) throw new Error("ENS registry resolver mismatch");
if (ethers.getAddress(signer) !== expectedSigner) throw new Error("resolver signer mismatch");
if (gatewayUrl !== expectedGatewayUrl) throw new Error("resolver gateway URL mismatch");
NODE
```

## Operator-run deployment steps (mutating; do not run in PR/CI/Codex)

### 1. Local validation before any deployment

From repo root:

```bash
cd contracts
npm ci
npm test
npm run lint
cd ..
bash gov-infra/verifiers/gov-verify-rubric.sh
```

If Solidity bytecode changed, also run Slither and preserve contract validation evidence. M4 expects no Solidity change.

### 2. Deploy the resolver for lab (Sepolia)

```bash
cd contracts
export SEPOLIA_RPC_URL="https://..."
export DEPLOYER_PRIVATE_KEY="0x..." # operator-held deployer; never commit or log
export INITIAL_OWNER="0x<lab-admin-safe-or-owner>"
export ENS_GATEWAY_SIGNER="0x<lab-kms-derived-signer>"
export ENS_GATEWAY_URL="https://lab.lesser.host/resolve?sender={sender}&data={data}"

# Mutating: sends a Sepolia transaction.
npm run deploy:sepolia:offchain-resolver
```

Record the resolver address and deployment transaction hash in deployment evidence. If the deploy owner is not the final
admin Safe, complete the `Ownable2Step` flow below before relying on the resolver for live-like lab tests.

### 3. Deploy the resolver for live (Ethereum mainnet)

Mainnet deployment must use the live KMS-derived signer and live gateway URL. `INITIAL_OWNER` should be the admin Safe;
non-trivial follow-up mutations must be Safe-ready.

```bash
cd contracts
export MAINNET_RPC_URL="https://..."
export DEPLOYER_PRIVATE_KEY="0x..." # operator-held deployer; never commit or log
export INITIAL_OWNER="0x<mainnet-admin-safe>"
export ENS_GATEWAY_SIGNER="0x<live-kms-derived-signer>"
export ENS_GATEWAY_URL="https://lesser.host/resolve?sender={sender}&data={data}"

# Mutating: sends an Ethereum mainnet transaction. Operator/Safe authorization required.
npm run deploy:mainnet:offchain-resolver
```

Do not proceed to mainnet `setResolver` until the live cutover gate checklist below is complete.

## Operator-run ENS `setResolver` runbook (mutating; do not run in PR/CI/Codex)

`lessersoul.eth` should point at the stage resolver on that stage's ENS network. Individual
`<name>.<instance-slug>.lessersoul.eth` records are not written on-chain; the offchain resolver receives the full DNS name
and the gateway serves host-backed material.

### Prepare calldata for Safe or EOA execution

```bash
cd contracts
export ENS_REGISTRY_ADDRESS=0x00000000000C2E074eC69A0dFb2997BA6C7d2e1e
export ENS_ROOT_NAME=lessersoul.eth
export RESOLVER_ADDRESS="0x<stage-resolver>"

node --input-type=module <<'NODE'
import { ethers } from "ethers";
const registry = new ethers.Interface(["function setResolver(bytes32 node, address resolver)"]);
const node = ethers.namehash(process.env.ENS_ROOT_NAME);
const resolver = ethers.getAddress(process.env.RESOLVER_ADDRESS);
console.log(JSON.stringify({
  target: process.env.ENS_REGISTRY_ADDRESS,
  value: "0",
  method: "ENSRegistry.setResolver(bytes32,address)",
  rootName: process.env.ENS_ROOT_NAME,
  node,
  resolver,
  data: registry.encodeFunctionData("setResolver", [node, resolver]),
}, null, 2));
NODE
```

Execution policy:

- Sepolia lab may be executed by the current `lessersoul.eth` owner or its Safe after operator approval.
- Ethereum mainnet live should be executed by the owner Safe. Do not use a single-signer mainnet shortcut for
  non-trivial ownership/governance changes.
- Before execution, record the current resolver so rollback can restore it if needed.
- After execution, run the read-only inspection command and confirm the registry resolver equals the stage resolver.

## Operator-run ownership runbook (`Ownable2Step`)

If the resolver owner is not already the stage admin Safe, transfer ownership with the two-step OpenZeppelin flow. The
commands below only prepare calldata; submit them through the current owner / Safe workflow.

```bash
cd contracts
export RESOLVER_ADDRESS="0x<stage-resolver>"
export ADMIN_SAFE="0x<stage-admin-safe>"

node --input-type=module <<'NODE'
import { ethers } from "ethers";
const iface = new ethers.Interface([
  "function transferOwnership(address newOwner)",
  "function acceptOwnership()",
]);
console.log(JSON.stringify({
  transferOwnership: {
    target: process.env.RESOLVER_ADDRESS,
    value: "0",
    caller: "current resolver owner",
    newOwner: ethers.getAddress(process.env.ADMIN_SAFE),
    data: iface.encodeFunctionData("transferOwnership", [process.env.ADMIN_SAFE]),
  },
  acceptOwnership: {
    target: process.env.RESOLVER_ADDRESS,
    value: "0",
    caller: "pending owner / admin Safe",
    data: iface.encodeFunctionData("acceptOwnership", []),
  },
}, null, 2));
NODE
```

Post-conditions:

- after `transferOwnership`, `pendingOwner()` is the admin Safe;
- after `acceptOwnership`, `owner()` is the admin Safe and `pendingOwner()` is zero;
- mainnet evidence includes the Safe transaction(s), signer set, and final read-only inspection output.

## Operator-run signer rotation and gateway URL updates

### Signer rotation

1. Derive the new stage KMS signer address with the read-only signer derivation command.
2. Deploy/update the host stage so `/resolve` signs with the new KMS key. Confirm gateway health before changing the
   resolver.
3. Prepare and execute `setSigner(newSigner)` from the resolver owner/Safe.
4. Wait at least `ENS_GATEWAY_TTL_SECONDS` plus client/cache buffer.
5. Prepare and execute `setSigner(currentSigner)` to clear `previousSigner()` once cached old signatures are no longer
   needed.

Calldata helper:

```bash
cd contracts
export RESOLVER_ADDRESS="0x<stage-resolver>"
export NEW_SIGNER="0x<kms-derived-signer>"
node --input-type=module <<'NODE'
import { ethers } from "ethers";
const iface = new ethers.Interface(["function setSigner(address signer)"]);
console.log(JSON.stringify({
  target: process.env.RESOLVER_ADDRESS,
  value: "0",
  method: "OffchainResolver.setSigner(address)",
  signer: ethers.getAddress(process.env.NEW_SIGNER),
  data: iface.encodeFunctionData("setSigner", [process.env.NEW_SIGNER]),
}, null, 2));
NODE
```

### Gateway URL update

Only the stage templates in the stage matrix are valid for Project 44 lab/live. Prepare `setGatewayUrl(newTemplate)` if a
historical deployment still points at an older direct API Gateway URL or if a future first-party host URL changes.

```bash
cd contracts
export RESOLVER_ADDRESS="0x<stage-resolver>"
export NEW_GATEWAY_URL="https://lab.lesser.host/resolve?sender={sender}&data={data}" # or live template
node --input-type=module <<'NODE'
import { ethers } from "ethers";
const url = process.env.NEW_GATEWAY_URL;
if (!url.startsWith("https://") || !url.includes("{sender}") || !url.includes("{data}")) {
  throw new Error("invalid EIP-3668 gateway URL template");
}
const iface = new ethers.Interface(["function setGatewayUrl(string url)"]);
console.log(JSON.stringify({
  target: process.env.RESOLVER_ADDRESS,
  value: "0",
  method: "OffchainResolver.setGatewayUrl(string)",
  gatewayUrl: url,
  data: iface.encodeFunctionData("setGatewayUrl", [url]),
}, null, 2));
NODE
```

## Rollback runbook

Capture rollback inputs before every mutation: previous ENS registry resolver, previous gateway URL, previous signer,
previous owner, current Safe/owner access, and last known-good host deployment.

Rollback options, in order of safety:

1. **Gateway/host rollback:** restore the previous host deployment or CDK context, then redeploy through the normal
   `theory app up --stage <stage> --execute` operator path. Never set a deploy timeout.
2. **Gateway URL rollback:** owner/Safe calls `setGatewayUrl(previousTemplate)` and read-only inspection verifies it.
3. **Signer rollback:** owner/Safe calls `setSigner(previousSigner)` after the gateway is signing with that key again.
4. **ENS registry rollback:** owner/Safe calls `ENSRegistry.setResolver(namehash("lessersoul.eth"), previousResolver)`.
5. **Ownership rollback:** use `Ownable2Step` only if ownership transfer is the failing operation and the current/pending
   owner can safely complete or reverse it.

Do not delete deployment records, KMS keys, SSM parameters, Safe transactions, or historical evidence during rollback.

## Lab smoke-test checklist (Sepolia before live)

Complete this checklist against Sepolia lab before any mainnet live action:

- [ ] M1-M3 Project 44 changes are merged/deployed to lab.
- [ ] `ensGatewayEnabledLab=true`; `ensGatewayResolverAddressLab` equals the Sepolia resolver address.
- [ ] The lab trust API Lambda environment has `ENS_GATEWAY_CHAIN_ID=11155111`, `ENS_GATEWAY_CHAIN_NAME=sepolia`,
      `ENS_GATEWAY_ROOT_NAME=lessersoul.eth`, and `ENS_GATEWAY_RESOLVER_ADDRESS=<lab resolver>`.
- [ ] The derived lab KMS signer equals `OffchainResolver.signer()`.
- [ ] `OffchainResolver.gatewayUrl()` equals `https://lab.lesser.host/resolve?sender={sender}&data={data}`.
- [ ] The Sepolia ENS registry resolver for `lessersoul.eth` equals the lab resolver.
- [ ] At least one canary identity exists as `<name>.<instance-slug>.lessersoul.eth` in lab host state.
- [ ] The legacy bare name `<name>.lessersoul.eth` is not required for the canary and fails closed.
- [ ] Gateway response TTL and signer rotation state (`previousSigner`) are understood before testing.

Optional smoke-test script outline for an ENS-aware CCIP-read client:

```bash
cd contracts
export SEPOLIA_RPC_URL="https://..."
export CANARY_ENS_NAME="agent-alice.simulacrum.lessersoul.eth"
export EXPECTED_RESOLVER_ADDRESS="0x<lab-resolver>"
export EXPECTED_ETH_ADDRESS="0x<expected-address-record-or-zero>"

node --input-type=module <<'NODE'
import { ethers } from "ethers";

function dnsEncode(name) {
  return "0x" + name.split(".").filter(Boolean).map((label) => {
    if (label.length > 63) throw new Error(`label too long: ${label}`);
    return label.length.toString(16).padStart(2, "0") + Buffer.from(label, "utf8").toString("hex");
  }).join("") + "00";
}

const provider = new ethers.JsonRpcProvider(process.env.SEPOLIA_RPC_URL, 11155111);
const resolverAddress = ethers.getAddress(process.env.EXPECTED_RESOLVER_ADDRESS);
const name = process.env.CANARY_ENS_NAME.toLowerCase();
const node = ethers.namehash(name);
const addrIface = new ethers.Interface(["function addr(bytes32 node) view returns (address)"]);
const resolverIface = new ethers.Interface(["function resolve(bytes name, bytes data) view returns (bytes)"]);
const callData = resolverIface.encodeFunctionData("resolve", [dnsEncode(name), addrIface.encodeFunctionData("addr", [node])]);

const rawResolveResult = await provider.call({ to: resolverAddress, data: callData, enableCcipRead: true });
const [resolvedBytes] = resolverIface.decodeFunctionResult("resolve", rawResolveResult);
const [resolvedAddress] = addrIface.decodeFunctionResult("addr", resolvedBytes);
console.log(JSON.stringify({ name, resolverAddress, resolvedAddress }, null, 2));

if (process.env.EXPECTED_ETH_ADDRESS) {
  const expected = ethers.getAddress(process.env.EXPECTED_ETH_ADDRESS);
  if (ethers.getAddress(resolvedAddress) !== expected) throw new Error("unexpected canary address record");
}
NODE
```

If the script fails, stop the live cutover. Preserve gateway logs and read-only resolver/registry inspection output for
investigation.

## Mainnet live cutover gate checklist

Do not execute mainnet deploy, `setResolver`, signer rotation, gateway URL update, or ownership transfer until every gate
is satisfied:

- [ ] Project 44 M1, M2, and M3 are complete and deployed where required.
- [ ] Sepolia lab smoke-test evidence is attached to the rollout record.
- [ ] No unresolved legacy bare-name alias dependency remains; live canaries use
      `<name>.<instance-slug>.lessersoul.eth`.
- [ ] `ensGatewayResolverAddressLive` is configured with the verified mainnet resolver address; the legacy generic
      `ensGatewayResolverAddress` is not used for live.
- [ ] The live trust API Lambda environment maps that context into `ENS_GATEWAY_RESOLVER_ADDRESS` and uses
      `ENS_GATEWAY_CHAIN_ID=1`, `ENS_GATEWAY_CHAIN_NAME=mainnet`.
- [ ] The derived live KMS signer matches `OffchainResolver.signer()`.
- [ ] `OffchainResolver.gatewayUrl()` equals `https://lesser.host/resolve?sender={sender}&data={data}`.
- [ ] The live gateway is healthy from a first-party origin and signs only for the configured resolver sender.
- [ ] Owner/Safe access is confirmed for rollback (`setResolver`, `setSigner`, `setGatewayUrl`, and ownership recovery).
- [ ] Previous resolver, signer, gateway URL, owner, and host deployment evidence are captured.
- [ ] Safe-ready calldata for `ENSRegistry.setResolver`, `setSigner`, `setGatewayUrl`, and any ownership action has been
      reviewed by an operator.
- [ ] No mainnet single-signer shortcut is planned for non-trivial mutations.
- [ ] Post-cutover monitoring is assigned: gateway 4xx/5xx, KMS signing errors, resolver sender mismatch rejects, and
      canary ENS resolution.

## Local build + tests

From `lesser-host/contracts/`:

```bash
npm test
```

If this document's commands are changed in a way that touches CDK config, Go gateway behavior, Solidity bytecode, or
contract scripts, run the corresponding validation listed in `AGENTS.md` and preserve the evidence in the PR.
