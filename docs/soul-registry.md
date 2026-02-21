# Soul Registry (lesser-soul) — Contracts (M1)

This document covers the on-chain contracts that implement the `lesser-soul` registry anchor described in
`lesser-soul/SPEC.md`.

## Contracts

All contracts live in `lesser-host/contracts/contracts/` and are built/tested with Hardhat.

### `SoulRegistry.sol`

- ERC-721 soul tokens where **`tokenId == agentId`** (deterministic).
- Implements EIP-8004 compatibility: `getAgentWallet(uint256 agentId) -> address`.
- Transfer policy:
  - Normal ERC-721 transfers are allowed only during a **claim window** after mint.
  - After the claim window, tokens are **soulbound** (normal transfers revert).
  - Wallet rotation remains possible even when soulbound (via a signature-verified admin call).
- Wallet rotation:
  - Safe-first (`onlyOwner`) but requires **two EIP-712 signatures** (current wallet + new wallet).
  - Replay protection via an on-chain per-agent nonce.

### `ReputationAttestation.sol` / `ValidationAttestation.sol`

- Owner-only `publishRoot(bytes32 root, uint256 blockRef, uint256 count)`.
- `latestRoot()` returns `(root, blockRef, count, timestamp)`.

## Deployment (Base / 8453)

Policy:
- **Owner is the admin Safe** (multi-sig), consistent with TipSplitter governance.
- Contracts are **not upgradeable**; deploy new versions and update consumers.

Suggested deploy sequence:

1) Deploy `SoulRegistry` with:
   - `initialOwner = <adminSafeAddress>`
   - `claimWindowSeconds = <seconds>` (set policy; `0` means immediately soulbound)

2) Deploy `ReputationAttestation` and `ValidationAttestation` with:
   - `initialOwner = <adminSafeAddress>`

3) Update TipSplitter to point to the deployed SoulRegistry:
   - Safe executes `TipSplitter.setAgentIdentityRegistry(<soulRegistryAddress>)`

## Local build + tests

From `lesser-host/contracts/`:

```bash
npm test
```

This runs `hardhat compile` and contract unit tests (including an integration test proving TipSplitter can tip by
`agentId` using the real `SoulRegistry` implementation).

