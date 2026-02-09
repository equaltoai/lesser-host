# Client-Side Tipping Integration Guide (TipSplitter)

This guide is for client implementers integrating `lesser.host` tipping into an instance frontend (for example, UI served
under `/l/*` on an instance domain).

References:
- TipSplitter contract: `contracts/contracts/TipSplitter.sol`
- Tip registry + `hostId` rules: `docs/tip-registry.md`

---

## Overview

Tips are sent on-chain to the `TipSplitter` contract, which splits the payment between:
- the Lesser org wallet (fixed 1% fee),
- the host instance wallet (configurable host fee),
- the actor wallet (remainder).

Client transactions call one of:
- `tipETH(bytes32 hostId, address actor, bytes32 contentHash)` (native ETH)
- `tipToken(address token, bytes32 hostId, address actor, uint256 amount, bytes32 contentHash)` (ERC-20)

The emitted `TipSent` event includes:
- `hostId` (bytes32) — identifies the instance domain being tipped through
- `actor` (address) — the recipient actor wallet
- `contentHash` (bytes32) — links the tip to the content being tipped for

---

## 1) Compute `hostId` (domain → bytes32)

`hostId` must use the **canonical lesser.host normalization + hashing rule**.

At a high level:
1. Normalize the domain (trim, strip trailing `.`, lowercase, reject scheme/path/port, IDNA → ASCII).
2. Hash: `hostId = keccak256(utf8(normalizedDomain))`.

Full canonical rules (and the server implementation) are documented in `docs/tip-registry.md`.

### Practical client guidance

- If you are tipping for the current instance domain, prefer `window.location.hostname` as the input domain.
  - It is already scheme/path/port-free, and is typically already ASCII (punycode) for IDNs.
- Always apply the final canonical rule: lowercase + `keccak256(utf8(...))`.

Example (TypeScript, ethers v6):

```ts
import { keccak256, toUtf8Bytes } from "ethers";

const normalizedDomain = window.location.hostname.replace(/\.$/, "").trim().toLowerCase();
const hostId = keccak256(toUtf8Bytes(normalizedDomain)); // 0x… (bytes32)
```

---

## 2) Discover tip config (chain + contract) for managed instances

Do not hardcode the chain or contract address.

Managed instances should fetch the current platform tip config from the control plane:

- `GET /api/v1/tip-registry/config`

Response shape:

```json
{
  "enabled": true,
  "chain_id": 11155111,
  "contract_address": "0xf5Fecc44276dBc1Bf45c40dC7f3cCb1aAfb2AAfe"
}
```

Notes:
- If `enabled` is false (or the endpoint returns 404), the instance should hide/disable tipping UI.
- Clients should treat this as cacheable configuration (it rarely changes).

---

## 3) Recommended `contentHash` convention (`TipSent.contentHash`)

`contentHash` is intended for ecosystem-wide analytics and attribution. To avoid fragmentation, clients should use a
single convention.

### Canonical convention (recommended)

Use the ActivityPub object ID URI (`object.id`) for the content being tipped, and compute:

`contentHash = keccak256(utf8(object.id.trim()))`

Guidelines:
- Use the **canonical ActivityPub ID** (`object.id`), not the HTML URL.
- Do not reserialize or normalize the URI beyond `trim()` (avoid library-specific canonicalization differences).
- If there is no specific object (e.g., “tip this actor” from a profile page), use `bytes32(0)` and rely on `actor`.

Example (TypeScript, ethers v6):

```ts
import { keccak256, toUtf8Bytes, ZeroHash } from "ethers";

const contentHash = objectId ? keccak256(toUtf8Bytes(objectId.trim())) : ZeroHash;
```

---

## 4) Resolving ActivityPub actor → EVM address (until first-class API exists)

The `TipSplitter` contract requires an EVM address for `actor`. Until there is a first-class resolution API, clients
need a convention.

### Recommended convention

Prefer a **CAIP-10** account ID in the actor’s profile metadata:

- Field name: `Wallet` (or `Ethereum`)
- Field value: `eip155:<chainId>:<0xAddress>`
  - Example: `eip155:8453:0x1234…abcd`

Fallbacks (in order):
1. A plain `0x…` address in the same profile field (interpreted on the platform tip chain).
2. If no address is present, disable tipping for that actor and prompt the user/actor to add a wallet field.

Important:
- Treat profile-provided wallet addresses as **self-asserted** (not verified).
- Always display the resolved address to the tipper before sending.

---

## Suggested UX flows

### Wallet connect (EIP-1193)
- Detect an injected provider (`window.ethereum`), and request accounts with `eth_requestAccounts`.
- Persist only the selected account address; re-check on each page load.
- Handle user rejection explicitly (do not treat as an error state).

### Chain switching
- Fetch current chain via `eth_chainId`.
- If mismatch, attempt `wallet_switchEthereumChain`.
- If the chain isn’t installed (common error code `4902`), fall back to `wallet_addEthereumChain` then retry switch.

### Token selection / allowlist display
- Always offer ETH (native token).
- Optionally offer a curated set of ERC-20s (e.g. stablecoins) and hide any that are not allowed by the contract:
  - Query `TipSplitter.allowedTokens(tokenAddress)` before rendering token options.
- For ERC-20 tips:
  - Check allowance and guide the user through `approve` (or `permit` if you implement it) before sending the tip.

### Transaction status + error handling
- Show clear states: “waiting for wallet”, “submitted”, “confirmed”, “failed”.
- Provide an explorer link from the tx hash.
- Handle common revert reasons gracefully:
  - “host not active” (domain not registered/active)
  - “token not allowed”
  - “amount below minimum”
  - “cannot tip yourself”

