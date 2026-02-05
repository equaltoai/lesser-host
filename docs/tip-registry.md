# Tip Host Registry (M8)

This document describes the `lesser.host` tip host registry administration flow and the on-chain `hostId` hashing rules.

## Contract

- Contract source: `contracts/contracts/TipSplitter.sol`
- Admin methods used by `lesser.host`:
  - `registerHost(bytes32 hostId, address wallet, uint16 feeBps)`
  - `updateHost(bytes32 hostId, address wallet, uint16 feeBps)`
  - `setHostActive(bytes32 hostId, bool active)`
  - `setTokenAllowed(address token, bool allowed)`

## Canonical domain normalization + `hostId`

1. Normalize the domain using `internal/domains.NormalizeDomain`:
   - trim whitespace, strip trailing dot, lowercase
   - reject scheme/path/port/credentials/wildcards
   - IDNA → ASCII (UTS#46) via `idna.Lookup`
2. Compute:
   - `hostId = keccak256(utf8(normalizedDomain))` (see `internal/tips.HostIDFromDomain`)

## Proofs (external registrations)

The external registration flow accepts **DNS TXT** and/or **HTTPS well-known** proofs.

- **DNS TXT**
  - name: `_lesser-host-tip-registry.<normalizedDomain>`
  - value: `lesser-host-tip-registry=<token>`
- **HTTPS well-known**
  - URL: `https://<normalizedDomain>/.well-known/lesser-host-tip-registry`
  - body: `lesser-host-tip-registry=<token>`

Updates that **increase** wallet/fee require **both** proofs.

## API (control-plane)

Public:
- `POST /api/v1/tip-registry/registrations/begin`
  - body: `{ kind?, domain, wallet_address, host_fee_bps }`
  - response includes:
    - wallet message to sign
    - DNS + HTTPS proof instructions
- `POST /api/v1/tip-registry/registrations/{id}/verify`
  - body: `{ signature, proofs? }`
  - returns:
    - the created on-chain operation
    - a Safe-ready payload `{ to, value, data }`

Admin (requires operator auth):
- `GET /api/v1/tip-registry/operations?status=pending|proposed|executed|failed`
- `GET /api/v1/tip-registry/operations/{id}`
- `POST /api/v1/tip-registry/operations/{id}/record-execution`
  - body: `{ exec_tx_hash }` (stores receipt + state snapshot)
- `POST /api/v1/tip-registry/hosts/{domain}/ensure`
  - creates an idempotent `registerHost` / `updateHost` / `setHostActive` operation using defaults
- `POST /api/v1/tip-registry/hosts/{domain}/active`
  - body: `{ active }`
- `POST /api/v1/tip-registry/tokens/allowlist`
  - body: `{ token_address, allowed }`

## Configuration (env)

Control-plane uses:

```bash
TIP_ENABLED=true
TIP_CHAIN_ID=8453
TIP_RPC_URL=https://...
TIP_CONTRACT_ADDRESS=0x...
TIP_ADMIN_SAFE_ADDRESS=0x...
TIP_DEFAULT_HOST_WALLET_ADDRESS=0x...
TIP_DEFAULT_HOST_FEE_BPS=500
TIP_TX_MODE=safe
```

