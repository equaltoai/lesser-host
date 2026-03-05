# Managed Provisioning (lesser-host) — Agent Implementation Prompt

You are the implementation agent for the `lesser-host` repo. Follow this plan exactly, commit after each milestone, and push.

Constraints and facts:
- Reserved wallets that must never be used as instance wallets:
- `<LESSER_HOST_ADMIN_WALLET>` (lesser.host admin wallet)
- `<TIP_SPLITTER_LESSER_WALLET>` (Safe + 1% recipient, `TipSplitter.lesserWallet`)
- Provisioning is blocked until a user is approved by an admin.
- Provisioning flow: user submits slug + optional admin username (default exactly to slug), then wallet signature to consent, then provisioning job is created.
- Stage behavior: lab provisions lab instance, live provisions live instance.
- Setup wizard for provisioned instances is passkey-only.
- Keep CSP strict, no inline scripts.

Branching and workflow:
1. Create a branch: `git checkout -b feat/managed-provisioning-flow`.
2. Work in milestones, 1 commit per milestone.
3. Push after each commit: `git push -u origin feat/managed-provisioning-flow`.
4. Update `docs/roadmap-managed-provisioning.md` if plan details change.

Milestone 1: Data model + validations
1. Add reserved-wallet validation helpers in control-plane (centralized).
2. Enforce reserved-wallet block in:
1. Portal provisioning handler.
2. Tip registry host registration/update endpoints.
3. Domain registration endpoints if instance wallet appears there.
4. Ensure slug uniqueness checks remain.
5. Commit: `feat: add reserved wallet validations for provisioning`.

Milestone 2: Approval gating + provisioning request schema
1. Ensure portal user has an `approved` boolean or equivalent in store.
2. Update `/api/v1/portal/instances` create flow to return 403 unless approved.
3. Add `admin_username` field default to slug.
4. Update store + API payloads to include `admin_username` and consent signature fields.
5. Commit: `feat: gate provisioning by approval and add admin username`.

Milestone 3: Wallet consent + signature verification
1. Create a challenge endpoint for provisioning consent.
2. Use existing wallet auth message signing utilities.
3. Store signature + message hash in provisioning job record.
4. Ensure the consent message includes:
1. slug
2. stage
3. admin username
4. timestamp/nonce
5. Commit: `feat: add provisioning consent signature flow`.

Milestone 4: UI flow (portal)
1. Update portal instance provisioning UI to:
1. Collect slug and optional admin username with default = slug.
2. Perform consent signature before submit.
3. Show errors for reserved-wallet and approval gating.
2. Add passkey-only setup step after provisioning completes.
3. Commit: `feat: portal provisioning flow updates`.

Milestone 5: Provisioning worker integration
1. Ensure worker passes admin wallet and admin username to `lesser up`/runner.
2. Ensure stage mapping uses control-plane stage.
3. Store any bootstrap JSON in artifacts if it still exists.
4. Commit: `feat: propagate admin wallet and username to provisioning worker`.

Milestone 6: Tip registry preflight
1. Before creating a tip registry operation, validate:
1. wallet not in reserved list
2. wallet not in lesser-host admin wallet list
3. host is registered
2. Commit: `feat: tip registry preflight validations`.

Milestone 7: Docs and tests
1. Add/update tests for provisioning validation and consent verification.
2. Update docs in `docs/roadmap-managed-provisioning.md`.
3. Commit: `test/docs: coverage for managed provisioning and wallet validation`.

Notes:
- Avoid changing CloudFront routing unless required.
- Do not add inline styles/scripts.
- Use existing session storage handling.
