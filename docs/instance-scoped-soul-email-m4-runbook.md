# Instance-scoped Lesser Soul email — M4 rollout, rollback, and monitoring runbook

Project 37 M4 is the final milestone: this document serves as the live-release
runbook covering deploy order, migration sequence, canaries, rollback, monitoring,
and stop conditions for the instance-scoped `lessersoul.ai` namespace correction.

Parent: [#344](https://github.com/equaltoai/lesser-host/issues/344)

## Prerequisites

Before any live migration step, the operator must have:

- M0 inventory evidence passing all signoff gates (zero duplicates, zero
  local-part overflows, valid `Domain.InstanceSlug` for every agent, known signing
  path for every agent).
- M2 dry-run evidence confirming the migration plan against live host state.
- Redacted provider-state snapshot covering every selected agent's current and
  proposed local-parts.
- Arch/Ops review of the inventory, dry-run, and provider-state evidence.
- M3 lab canary evidence (`--require-legacy-alias --require-unknown-alias`)
  passing host-only validation.
- Full M3 canary evidence (`--require-body-mcp`) passing or the remaining
  body-MCP dependency explicitly accepted as a known caveat for the live gate.
- `lesser-body` release [#275](https://github.com/equaltoai/lesser-body/issues/275)
  status understood (merged or caveated).

## Deploy order

The release is not a new deploy of host or lesser. It is a **state migration**
executed through the existing host control plane against live DynamoDB and Migadu
provider state.

1. **Host live deployment** — Ensure host `main` is deployed to `live`
   (canonical `AWS_PROFILE=Lesser theory app up --stage live --execute`).
   The live deployment must include the M1 provisioning/resolver code, M2
   migration command, and M3 canary verifier.

2. **Inventory snapshot** — Run M0 inventory against the live table with the
   live redacted provider-state and registration-state snapshots:

   ```bash
   go run ./scripts/soul-email-m0-inventory \
     --stage live \
     --table-name lesser-host-live-state \
     --provider-state path/to/redacted-migadu-provider-state-live.json \
     --registration-state path/to/redacted-registration-signing-state-live.json \
     --out gov-infra/evidence/project37/m0-email-inventory-live.json
   ```

   Exit code 0 and zero blockers in the report must be confirmed before
   proceeding.

3. **M2 dry-run** — Generate the migration plan against live state:

   ```bash
   go run ./scripts/soul-email-m2-migration \
     --stage live \
     --table-name lesser-host-live-state \
     --inventory gov-infra/evidence/project37/m0-email-inventory-live.json \
     --out gov-infra/evidence/project37/m2-email-migration-live-dry-run.json
   ```

   Review every agent's proposed address, provider actions, and host-sync
   actions. Confirm zero unexpected diffs from the lab dry-run.

4. **Provider prepare** — Create new Migadu mailboxes and forwardings for
   `<agent-local-id>.<instance-slug>` while preserving old bare delivery:

   ```bash
   SOUL_EMAIL_INBOUND_DOMAIN=inbound.lessersoul.ai \
   go run ./scripts/soul-email-m2-migration \
     --stage live \
     --table-name lesser-host-live-state \
     --inventory gov-infra/evidence/project37/m0-email-inventory-live.json \
     --apply \
     --out gov-infra/evidence/project37/m2-email-provider-prepare-live.json
   ```

   After this step, new addresses accept inbound delivery but no public
   channel switch has occurred. Old bare addresses still accept inbound.

5. **Self-attestation collection** — Each agent publishes a new registration
   self-description version advertising only the instance-scoped email address,
   OR Aron approves a disclosed host audit path for agents that cannot
   self-attest.

6. **Registration sync (host switch)** — For each attested agent, registration
   sync writes:
   - `SoulAgentChannel.Identifier` → new instance-scoped address
   - `SoulEmailAgentIndex` → new instance-scoped address
   - `SoulEmailLegacyAliasIndex` → legacy bare → canonical mapping

   This step is idempotent; re-running sync after a partial apply continues
   from the next incomplete agent. Public channel surfaces now advertise only
   the instance-scoped address.

7. **M3 live canaries** — Run the full canary suite against live:

   ```bash
   go run ./scripts/soul-email-m3-canary \
     --stage live \
     --evidence gov-infra/evidence/project37/m3-email-canary-live.json \
     --require-legacy-alias \
     --require-unknown-alias \
     --out gov-infra/evidence/project37/m3-email-canary-live-validation.json
   ```

   If body-MCP evidence is available, add `--require-body-mcp`.

8. **Arch/Ops final review** — Link the canary evidence, provider-prepare
   evidence, registration-publish evidence, and operator sign-off in the M4.2
   release decision document.

## Canaries (before live sign-off)

Per M3 acceptance, the following must pass against live:

| Check | Command flag | What it proves |
|---|---|---|
| Primary inbox delivery | (always) | `<agent-local-id>.<instance-slug>@lessersoul.ai` reaches mailbox |
| Primary outbound | (always) | Outbound sender is instance-scoped address |
| Mailbox list/get/content/search | (always) | Canonical address exposed, content redacted |
| Resolve / contactability | (always) | Instance-scoped address resolves; legacy bare fails closed |
| Legacy alias inbound | `--require-legacy-alias` | `agent@lessersoul.ai` canonicalizes and delivers to same mailbox |
| Legacy non-advertisement | `--require-legacy-alias` | Public channel surfaces do not advertise legacy bare address |
| Unknown alias fail-closed | `--require-unknown-alias` | `unknown@lessersoul.ai` inbound dropped, resolve returns not_found |
| Body MCP identity tools | `--require-body-mcp` | `identity_whoami_email` and `identity_lookup_email` match canonical address |

## Monitoring checks

### Migadu provider state

- Confirm each `<agent-local-id>.<instance-slug>` mailbox exists and has the
  correct inbound forwarding target `<agent-local-id>.<instance-slug>@inbound.lessersoul.ai`.
- Confirm each legacy `<agent-local-id>` mailbox/alias still accepts inbound.
- Verify no unintended mailboxes or forwardings were created.

### DynamoDB state

- `SoulAgentChannel.Identifier` equals `<agent-local-id>.<instance-slug>@lessersoul.ai`
  for every migrated agent.
- `SoulEmailAgentIndex` resolves each instance-scoped address to the correct agent.
- `SoulEmailLegacyAliasIndex` maps each legacy bare address to the canonical
  address and agent, and does NOT resolve any unknown bare address.
- `SoulAgentVersion` reflects the new registration version with the instance-scoped
  email channel for self-attested agents.
- No stale bare-address channels or indexes remain.

### CloudWatch / logs

- `comm-worker` log group: inbound deliveries to legacy bare addresses show
  canonicalization events (resolved alias → canonical address) without errors.
- `email-ingress` log group: SES inbound receipts show correct recipient
  normalization for both canonical and legacy addresses.
- No ERROR-level logs from `SoulEmailAgentIndex` or `SoulEmailLegacyAliasIndex`
  lookups.
- Provisioning/update workers show no unexpected agent-configuration errors.

### Queues / DLQs

- SQS DLQ depth for `comm-worker`, `email-ingress`, and `ai-worker` queues
  remains at baseline (no migration-induced spike).
- No poison-pill messages from legacy-alias canonicalization.

## Rollback steps

### Stop conditions — do not proceed if

- M0 inventory shows duplicate proposed addresses, local-part overflow, missing
  `Domain.InstanceSlug`, missing channel/index parity, or `can_self_attest=unknown`
  for any agent without an approved host audit path.
- M2 dry-run shows unexpected diffs from the lab dry-run that cannot be explained
  by stage differences.
- Provider-prepare fails for any agent (Migadu API error, credential issue,
  forwarding-target mismatch).
- Any agent's self-attestation is unavailable and no approved host audit path
  exists.
- M3 canaries show any primary or legacy inbound failure, non-advertisement
  failure, or unknown-alias pass-through.

### Rollback procedure

Rollback preserves inbound reachability as the highest priority.

1. **If provider prepare succeeded but no host switch occurred:**
   - Leave new provider state in place. It is inert (no public channel
     advertises the new address).
   - Retry self-attestation when ready or obtain host audit path approval.

2. **If host switch occurred but registration publish failed:**
   - Restore the previous `SoulAgentChannel.Identifier` and
     `SoulEmailAgentIndex` to the pre-migration values.
   - Remove or disable the `SoulEmailLegacyAliasIndex` records created during
     the partial switch.
   - Leave provider state (both old and new) intact until canaries prove
     safe cleanup.

3. **If host switch and registration publish both succeeded but canaries fail:**
   - Do NOT delete provider state.
   - Revert host public state: restore pre-migration `SoulAgentChannel`,
     `SoulEmailAgentIndex`, remove legacy alias records.
   - Republish the previous registration version with the old email channel.
   - The new provider mailbox remains as a safety net; clean it up after
     canaries confirm old path is operational.

4. **Full rollback invariant:**
   - Old bare provider delivery is never removed during M2/M3/M4.
   - Legacy alias records are never exposed through public channel discovery.
   - Every rollback step is idempotent and auditable.

## Sign-off authority

Live migration requires explicit sign-off from:

1. **Arch** — contract review of inventory, dry-run, and canary evidence.
2. **Ops** — provider-state verification, Migadu health check, CloudWatch
   baseline confirmation.
3. **Aron** — final release authorization.

No single signer can authorize live migration. All three must confirm before
provider-prepare or host-switch steps execute against live.

## Failure examples (sanitized)

### Migadu API rejection
```
provider_prepare failed for agent pilot.simulacrum:
  migadu error: mailbox local_part "pilot.simulacrum" already exists
  but forwarding target is not "<pilot.simulacrum>@inbound.lessersoul.ai"
```
**Action:** Inspect the existing mailbox. If it belongs to the same agent and
the forwarding target is correct, mark provider_prepared and continue. If it
belongs to a different agent, stop — this is an identity collision requiring
operator resolution.

### Local-part overflow
```
inventory: agent "very-long-agent-identifier-for-testing.simulacrum"
  proposed local_part "very-long-agent-identifier-for-testing.simulacrum"
  length 57, but Migadu limit is 64 — OK
```
**Action:** Continue. Only actual overflows (>64) block migration.

### Unknown signing state
```
inventory: agent 0xdef789 can_self_attest=unknown
```
**Action:** Stop. Do not proceed with this agent until its signing path is
confirmed or an approved host audit path exists. Other agents with known
signing state may proceed independently.

### Legacy alias inbound failure
```
canary: legacy inbound for pilot@lessersoul.ai
  expected: delivered to pilot.simulacrum@lessersoul.ai
  actual:   dropped (unknown recipient)
```
**Action:** Stop live migration. Verify `SoulEmailLegacyAliasIndex` record
exists and comm-worker canonicalization path is active. Do not proceed until
the canary passes.

## Evidence archive

All M4 evidence commits to `gov-infra/evidence/project37/`:

| File | Source |
|---|---|
| `m0-email-inventory-live.json` | M0 inventory (step 2) |
| `m2-email-migration-live-dry-run.json` | M2 dry-run (step 3) |
| `m2-email-provider-prepare-live.json` | Provider prepare (step 4) |
| `m3-email-canary-live.json` | M3 canaries (step 7) |
| `m3-email-canary-live-validation.json` | M3 validation output |
| `m4-release-decision.json` | Final release decision (M4.2) |

All evidence must be redacted — no message bodies, provider payloads, bearer
tokens, passwords, API secrets, authorization headers, or raw SSM values.
