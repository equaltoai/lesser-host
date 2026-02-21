# Tip Host Registry (M8)

This document describes the `lesser.host` tip host registry administration flow and the on-chain `hostId` hashing rules.

## Contract

- Contract source: `contracts/contracts/TipSplitter.sol`
- Admin methods used by `lesser.host`:
  - `registerHost(bytes32 hostId, address wallet, uint16 feeBps)`
  - `updateHost(bytes32 hostId, address wallet, uint16 feeBps)`
  - `setHostActive(bytes32 hostId, bool active)`
  - `setTokenAllowed(address token, bool allowed)`
  - `setLesserWallet(address newWallet)`
  - `setAgentIdentityRegistry(address registry)`
  - `setWithdrawalsPaused(bool paused_)`

Emergency owner-only methods (intended for incident response; not normal registration flow):

- `sweepStrayETH()`
  - callable only when both `paused()` and `withdrawalsPaused` are true
  - sweeps only untracked ETH (`address(this).balance - totalPendingETH`) to `lesserWallet`
- `sweepStrayToken(address token)`
  - callable only when both `paused()` and `withdrawalsPaused` are true
  - sweeps only untracked token balance (`balanceOf(this) - totalPendingToken[token]`) to `lesserWallet`

## Emergency runbook (operator)

Use this only during incident response. The contract owner is expected to be the admin Safe.

### 1) Freeze movement

1. Execute `pause()`.
2. Execute `setWithdrawalsPaused(true)`.
3. Verify:
   - `paused() == true`
   - `withdrawalsPaused == true`

### 2) Sweep untracked stray balances

Use this to recover assets not represented in `pending*` liabilities.

1. For ETH stray:
   - call `sweepStrayETH()`
2. For token stray:
   - call `sweepStrayToken(token)`
3. Expected behavior:
   - only `balance - tracked_liabilities` is moved
   - destination is always `lesserWallet`
   - if no stray balance exists, call reverts with `no stray`

### 3) Reconcile and resume

1. Confirm expected events and state:
   - `StraySwept(...)` for each successful sweep
2. Confirm all intended recipients can still withdraw their remaining pending balances.
3. Resume withdrawals: `setWithdrawalsPaused(false)`.
4. Resume tip intake: `unpause()`.

### 4) Post-incident checklist

1. Record Safe tx hashes and on-chain receipts in the incident log.
2. Record any stray amounts swept.
3. Publish operator-facing summary with timestamps and final contract state.

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

- `GET /api/v1/tip-registry/config`
  - returns `{ enabled, chain_id, contract_address }` for client integrations
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
