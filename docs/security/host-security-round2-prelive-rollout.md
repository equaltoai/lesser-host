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
