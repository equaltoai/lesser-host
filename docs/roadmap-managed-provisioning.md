# Managed Provisioning Roadmap (lesser-host)

This tracks the managed provisioning redesign in `lesser-host`: approval gating, wallet-consent, and safe wallet rules.

## Contract (Implemented)
- Provisioning is blocked until a portal user is approved (`User.approved=true`).
  - Admin/operator sessions bypass approval gating.
- Provisioning flow (portal):
  - User selects a slug.
  - Admin username defaults to the slug (editable).
  - User requests a consent challenge and signs it with their linked wallet.
  - Provision job is created only after consent verification.
- Stage behavior:
  - Control-plane stage is passed through (`lab` provisions `lab`, `live` provisions `live`).
- Runner inputs:
  - `STAGE`
  - `ADMIN_USERNAME`
  - `ADMIN_WALLET_ADDRESS`
  - `ADMIN_WALLET_CHAIN_ID`
  - `CONSENT_MESSAGE_B64`
  - `CONSENT_SIGNATURE`
- Setup wizard for provisioned instances is passkey-only (implemented in `lesser`).

## Reserved Wallet Rules (Implemented)
Reserved wallets must never be used as instance admin wallets or tip host wallets:
- `0x80189edb676d51b2fb2257b2ad38e018b20ca46e` (lesser.host admin wallet)
- `0x1e14865a53a994b01b9ccfef42669dc0bfe98805` (Safe + 1% recipient, `TipSplitter.lesserWallet`)

## Milestones (Implemented)
1. Centralize reserved-wallet validation and enforce in provisioning + tip registry.
2. Add portal approval gating and extend provisioning job schema (`admin_username` + consent fields).
3. Implement consent challenge endpoint + signature verification (message includes slug, stage, admin username, nonce, timestamps).
4. Portal UI flow: admin username input, consent signature, and passkey-only setup prompt.
5. Worker integration: propagate admin wallet + username to CodeBuild runner env.
   - Build `provision.json` in CodeBuild and run `lesser up` + `lesser init-admin`.
6. Tip registry preflight: reject reserved + privileged control-plane wallets and require host registration before creating `setHostActive` operations.
7. Add tests for approval gating, reserved-wallet validation, and consent verification; update docs.

## Follow-Ups
- Admin UI/API for approving portal users (currently enforced, but approval actions are not implemented here).
- Consider whether stage should remain identity-mapped or require an explicit mapping contract with `lesser`.
