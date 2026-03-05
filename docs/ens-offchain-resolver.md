# ENS OffchainResolver (CCIP-Read) — Contracts (LH-M8)

This document covers the Solidity contract used for ENS CCIP-Read resolution of `*.lessersoul.eth` via the
`ens-gateway.lessersoul.ai` gateway implemented in `lesser-host`.

## Contracts

- `contracts/contracts/OffchainResolver.sol`
  - Implements ENSIP-10 `resolve(bytes,bytes)` by reverting with EIP-3668 `OffchainLookup`.
  - Implements `resolveWithProof(bytes response, bytes extraData)` to verify gateway signatures and return the
    resolved result bytes.
  - Owner-only admin:
    - `setGatewayUrl(string url)` — update the gateway URL template.
    - `setSigner(address signer)` — rotate signer (keeps `previousSigner` to avoid downtime).

## Gateway URL template

EIP-3668 gateways support `{sender}` and `{data}` substitutions. The recommended template is:

```
https://ens-gateway.lessersoul.ai/resolve?sender={sender}&data={data}
```

## Signer address (KMS)

The ENS gateway signs responses using a secp256k1 key. The resolver contract stores the corresponding Ethereum
address as `signer`.

To derive the signer address for an AWS KMS key:

1) Fetch the DER-encoded public key:

```bash
aws kms get-public-key --key-id "$ENS_GATEWAY_SIGNING_KEY_ID" --query PublicKey --output text
```

2) Convert it to an Ethereum address (Node 24+):

```bash
PUB_B64="$(aws kms get-public-key --key-id "$ENS_GATEWAY_SIGNING_KEY_ID" --query PublicKey --output text)"
PUB_B64="$PUB_B64" node -e 'import { createPublicKey } from "node:crypto"; import { ethers } from "ethers";
const der = Buffer.from(process.env.PUB_B64, "base64");
const jwk = createPublicKey({ key: der, format: "der", type: "spki" }).export({ format: "jwk" });
const x = Buffer.from(jwk.x, "base64url"); const y = Buffer.from(jwk.y, "base64url");
const uncompressed = Buffer.concat([Buffer.from([4]), x, y]);
console.log(ethers.computeAddress(uncompressed));'
```

## Deployment (Ethereum mainnet / 1)

From `lesser-host/contracts/`:

```bash
export MAINNET_RPC_URL="..."
export DEPLOYER_PRIVATE_KEY="0x..."
export INITIAL_OWNER="0x<adminSafe>"
export ENS_GATEWAY_SIGNER="0x<signerAddress>"
export ENS_GATEWAY_URL="https://ens-gateway.lessersoul.ai/resolve?sender={sender}&data={data}"

npx hardhat run --network mainnet scripts/deploy-offchain-resolver.js
```

## Operations

### Set gateway URL

Owner calls:
- `OffchainResolver.setGatewayUrl(<newTemplate>)`

### Rotate signer without downtime

`OffchainResolver` keeps `previousSigner` active so cached gateway responses remain valid during rotation:

1) Deploy/configure the new signer key in the gateway and start signing with it.
2) Owner calls `OffchainResolver.setSigner(<newSignerAddress>)`.
3) After the gateway signature TTL has elapsed, owner can clear the previous signer by calling:
   `OffchainResolver.setSigner(<currentSignerAddress>)` (clears `previousSigner`).

### Transfer ownership to the admin Safe

If the contract was deployed with a non-Safe owner, use the standard `Ownable2Step` flow:

1) Current owner calls `transferOwnership(<adminSafe>)`.
2) The Safe executes `acceptOwnership()` on the resolver contract.

## Local build + tests

From `lesser-host/contracts/`:

```bash
npm test
```
