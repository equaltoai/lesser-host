# Managed Provisioning (lesser-host) — Agent Notes

Managed provisioning (approval gating, wallet-consent, and reserved-wallet rules) is implemented in this repo.

If you are an automated agent making follow-up changes, start with:
- `docs/agent-impl-managed-provisioning.md` (the original step-by-step implementation prompt)
- `docs/roadmap-managed-provisioning.md` (current contract + follow-ups)

## Contract (Current)

- Provisioning is blocked until a portal user is approved (`User.approved=true`).
- Provisioning consent is a wallet-signature flow; the signed message includes slug, stage, admin username, and a nonce.
- Stage behavior is identity-mapped:
  - lesser-host `lab` provisions lesser `lab`
  - lesser-host `live` provisions lesser `live`
- Reserved wallets must never be used as instance admin wallets or tip host wallets:
  - `<LESSER_HOST_ADMIN_WALLET>` (lesser.host admin wallet)
  - `<TIP_SPLITTER_LESSER_WALLET>` (Safe + 1% recipient, `TipSplitter.lesserWallet`)
