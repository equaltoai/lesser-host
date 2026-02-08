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
  - `0x80189edb676d51b2fb2257b2ad38e018b20ca46e` (lesser.host admin wallet)
  - `0x1e14865a53a994b01b9ccfef42669dc0bfe98805` (Safe + 1% recipient, `TipSplitter.lesserWallet`)
