# Host Security round 2 pre-live rollout notes

This note accompanies the M6-M9 round-2 hardening work. It is intentionally operational and does not duplicate detailed
finding text from the local Codex Security CSV.

## Current deployment posture

- Host validation is lab/Sepolia only for this line of work.
- No live users, live data, or live tipping events are in scope.
- Existing lab data can be migrated, denied, or reset if the stricter security model requires it.

## Stage order

1. Merge each milestone through PR review with gov-infra verifiers green.
2. Deploy host lab through the AppTheory deploy contract.
3. Validate the `theory` dev stage first.
4. Use `simulacrum` as the host-lab canary for managed provisioning and managed-release consumption.
5. Defer any live deployment until Aron explicitly authorizes a future live-readiness rollout.

## Lesser coordination gate

Parallel Lesser remediation must be consumed through host's normal managed-release discipline:

- Host does not sign off from Lesser source review or fixtures alone.
- The exact published Lesser release artifacts must be downloaded and checksum-verified through host's real consumer path.
- `theory` validation precedes `simulacrum` canary for relevant producer/consumer contract changes.
- Any interface drift is coordinated through Aron with the Lesser steward before host canary signoff.

## M6 canary checklist

M6 is considered ready for broader lab soak only after all of the following are true:

- Host lab deploy includes the M6 control-plane, CDK, and provisioning-worker changes.
- `theory` validates account adoption, consent one-time use, provisioning leases, and managed-update runner dispatch.
- Host consumes a published Lesser artifact through managed-release certification; no source-only signoff.
- `simulacrum` runs as the host-lab canary after `theory` passes, using the same verified Lesser artifact line.
- Any Lesser-side rollout blocker is recorded on the M6 tracking issue/PR before canary signoff.

### Lab canary trust-policy remediation

M6 intentionally keeps the Organizations vending role scoped to host-side orchestration; the CodeBuild deploy runner must
not receive that high-privilege role. Before a managed provisioning or managed-update runner starts, the provision worker
now ensures the per-instance `MANAGED_INSTANCE_ROLE_NAME` trust policy includes the concrete
`MANAGED_PROVISION_RUNNER_ROLE_ARN`. Existing lab tenant accounts (`theory` first, `simulacrum` canary second) should be
migrated by the normal managed-update path; do not restore org-vending-role access to the deploy runner as a shortcut.

## M9 Sepolia TipSplitter handoff

M9 hardens the pre-live TipSplitter contract before any live/mainnet tipping is enabled:

- wallet rotations no longer migrate recipient-keyed pending balances; pending liabilities remain with the credited
  wallet, and rotations affect future tips only
- `pause()` is the emergency global freeze for both new tips and withdrawals
- `setWithdrawalsPaused(true)` remains available for withdrawal-only freezes and is still required, together with
  `pause()`, before stray-balance sweep operations

Post-merge, deploy a fresh TipSplitter to Sepolia using the standalone TipSplitter redeploy path in
`docs/runbook-sepolia-contract-deploy.md`. Update `docs/deployments/sepolia/latest.json` and lab CDK context with the new
TipSplitter address only after the Sepolia deployment transaction is confirmed and read-only constructor checks pass.

Mainnet Safe execution remains deferred until a future live-readiness authorization. Do not prepare or execute a mainnet
TipSplitter Safe transaction as part of this round-2 pre-live milestone.
