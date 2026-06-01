# Project 44 M5 ENS backfill and canary evidence

Date: 2026-06-01

This note is the bounded M5 evidence/runbook for #639 / #664-#668. It complements the canonical
OffchainResolver runbook in [`docs/ens-offchain-resolver.md`](ens-offchain-resolver.md).

## Safety posture

- No on-chain mutations were performed: no ENS `setResolver`, registrar writes, Safe transactions, contract deployments,
  resolver owner changes, signer rotations, or mainnet transactions.
- The only mutating operation performed was the lab host-side DynamoDB backfill described below.
- Apply mode required a local `--rollback-out` file. Rollback files contain bounded raw rollback material and are operator
  local only; they must not be committed or pasted into PRs/logs.
- Legacy bare managed names remain fail-closed for current public discovery/search/gateway. The backfill does not create
  runtime bare-name aliases.

## Tooling commands

Dry-run inventory:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill \
  --stage lab \
  --table-name lesser-host-lab-state \
  --out /tmp/project44-m5-lab-dry-run.json
```

Apply an idempotent managed-only backfill:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill \
  --stage lab \
  --table-name lesser-host-lab-state \
  --apply \
  --rollback-out /tmp/project44-m5-lab-rollback.json \
  --out /tmp/project44-m5-lab-apply.json
```

Live uses the same commands with `--stage live --table-name lesser-host-live-state`. The script now derives the matching
stage table when `--stage` is explicit, but operators should still pass `--table-name` in evidence commands to make the
stage/table binding reviewable.

## Lab evidence

### Dry-run before apply

Command summary:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill --stage lab --out /tmp/project44-m5-lab-dry-run.json
```

Result: exit `0`.

Summary:

- scanned ENS channels: 12
- scanned `SoulAgentENSResolution` records: 12
- legacy managed bare channels: 12
- proposed channel updates: 12
- proposed canonical resolution creates: 12
- proposed legacy resolution deletes: 12
- ambiguous / blocked / error records: 0

### Apply

Command summary:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill \
  --stage lab \
  --apply \
  --rollback-out /tmp/project44-m5-lab-rollback.json \
  --out /tmp/project44-m5-lab-apply.json
```

Result: exit `0`.

Summary:

- applied channel updates: 12
- applied canonical resolution creates: 12
- applied legacy resolution deletes: 12
- ambiguous / blocked / error records: 0
- rollback entries captured locally: 36

### Idempotency dry-run after apply

Command summary:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill --stage lab --out /tmp/project44-m5-lab-post-apply-dry-run-v2.json
```

Result: exit `0`.

Summary:

- scanned ENS channels: 12
- scanned `SoulAgentENSResolution` records: 12
- canonical managed channels: 12
- canonical managed resolutions: 12
- proposed / applied mutations: 0
- ambiguous / blocked / error records: 0

### Gateway and discovery canaries

- `GET https://lab.lesser.host/health` returned HTTP `200` with `{"ok":true,"service":"ens-gateway"}`.
- Wrong-sender gateway request to `https://lab.lesser.host/resolve` returned HTTP `404` with `ccip.sender_unsupported`.
- Public canonical ENS discovery for `agent-0.simulacrum.lessersoul.eth` returned HTTP `200`.
- Public legacy bare ENS discovery for `agent-0.lessersoul.eth` returned HTTP `404`.
- Direct lab gateway CCIP-read compatibility request for `agent-0.simulacrum.lessersoul.eth` returned HTTP `200` with
  signed response data and a non-zero redacted address result (`0xEfd9…A2B6`).
- Direct lab gateway request for legacy bare `agent-0.lessersoul.eth` returned zero address material, preserving the M3
  fail-closed/no-current-alias behavior for bare names.

### Sepolia read-only resolver inspection

Read-only ENS registry inspection of `lessersoul.eth` on Sepolia returned resolver
`0xE99638b40E4Fff0129D56f03b55b6bbC4BBE49b5`, while the deployed lab trust API expects resolver sender
`0x6baE3b24bf87bdA876C1B2A0EF6f0D78AD0fc61d`.

Because M5 does not authorize ENS `setResolver` or other on-chain mutations, the full Sepolia ENS-aware client canary is
blocked until the operator-run M4 resolver cutover step points the Sepolia ENS registry at the configured resolver. The
host gateway itself was canaried directly as described above.

## Live evidence

### Dry-run

Command summary:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill --stage live --out /tmp/project44-m5-live-dry-run-v3.json
```

Result: exit `0`.

Summary:

- scanned ENS channels: 0
- scanned `SoulAgentENSResolution` records: 0
- proposed / applied mutations: 0
- ambiguous / blocked / error records: 0

### Apply/no-op confirmation

Command summary:

```bash
AWS_PROFILE=Lesser go run ./scripts/soul-ens-m5-backfill \
  --stage live \
  --apply \
  --rollback-out /tmp/project44-m5-live-rollback-v2.json \
  --out /tmp/project44-m5-live-apply-noop-v2.json
```

Result: exit `0`.

Summary:

- scanned ENS channels: 0
- scanned `SoulAgentENSResolution` records: 0
- proposed / applied mutations: 0
- rollback entries captured locally: 0

No live data mutations occurred.

### Gateway and discovery canaries

- `GET https://lesser.host/health` returned HTTP `200` with `{"ok":true,"service":"ens-gateway"}`.
- Wrong-sender gateway request to `https://lesser.host/resolve` returned HTTP `404` with `app.not_found`, i.e. live fails
  closed before sender-specific CCIP handling.
- Public canonical ENS discovery for `agent-0.simulacrum.lessersoul.eth` returned HTTP `404` because live currently has no
  ENS channel/resolution records.
- Public legacy bare ENS discovery for `agent-0.lessersoul.eth` returned HTTP `404`.
- Direct live gateway CCIP-read request for `agent-0.simulacrum.lessersoul.eth` returned HTTP `404` with `app.not_found`.

### Mainnet read-only resolver inspection

Read-only ENS registry inspection of `lessersoul.eth` on Ethereum mainnet returned resolver
`0xF29100983E058B709F3D539b0c765937B804AC15`, while the deployed live trust API expects resolver sender
`0x6baE3b24bf87bdA876C1B2A0EF6f0D78AD0fc61d`.

Because M5 does not authorize ENS `setResolver` or other on-chain mutations, the full mainnet ENS-aware resolver/gateway
canary is blocked until the operator-run M4 mainnet resolver cutover is performed. No Safe transaction was prepared or
submitted by this milestone PR.

## Rollback notes

- Lab rollback material was captured locally in `/tmp/project44-m5-lab-rollback.json` with 36 bounded entries. It was not
  committed.
- Live rollback material was captured locally in `/tmp/project44-m5-live-rollback-v2.json` with 0 entries because live had
  no target records.
- For lab rollback, restore the captured channel identifiers and legacy resolution items from the local rollback file,
  then delete any canonical resolution items listed by `canonical_key`. Re-run the dry-run afterwards; the expected
  rollback state is 12 legacy managed bare channels/resolutions and 0 ambiguous records.
