# Instance-scoped Lesser Soul email — M2 migration runbook

Project 37 M2 implements the safe migration path from legacy bare managed
addresses (`agent@lessersoul.ai`) to canonical instance-scoped addresses
(`agent.<instance>@lessersoul.ai`).

Live execution remains held under
<https://github.com/equaltoai/lesser-host/issues/339> until dry-run evidence,
provider state, and registration authority are reviewed.

## What M2 added

- `SoulEmailLegacyAliasIndex` is a host-internal alias model. It maps a known
  legacy recipient to the canonical current address and owning `agentId`.
- `comm-worker` canonicalizes a legacy alias only after the current
  `SoulEmailAgentIndex` lookup misses, and before channel matching/mailbox
  capture. Unknown bare recipients fail closed.
- v3 registration sync now permits the narrow Project 37 migration:
  a trusted managed Migadu email channel may move from an existing
  old bare `<normalized-agent-local-id>@lessersoul.ai` address to the derived
  `<agent-local-id>.<instance-slug>@lessersoul.ai` address. The sync preserves
  managed-channel metadata, writes the new current `SoulEmailAgentIndex`, and
  creates the internal legacy alias record. Other managed identifier changes
  still require deprovisioning first.
- `go run ./scripts/soul-email-m2-migration` reads M0 inventory evidence,
  plans provider/registration/host-sync actions, and can apply provider-prepare
  forwarding idempotently. It does not execute the live migration gate by
  itself. The command refuses inventory whose `schema_version`, `stage`, or
  `table_name` does not match the requested run config.

## Required sequence

1. Run M0 inventory with redacted provider and registration/signing snapshots.
2. Review the inventory for:
   - zero duplicate proposed addresses;
   - zero 64-octet local-part overflows;
   - valid `Domain.InstanceSlug` for every selected agent;
   - current email channel/index parity;
   - known provider state for old and proposed local-parts; and
   - `can_self_attest=yes`, or an explicitly approved host audit path.
3. Run M2 dry-run evidence:

   ```bash
   go run ./scripts/soul-email-m2-migration \
     --stage live \
     --table-name lesser-host-live-state \
     --inventory gov-infra/evidence/project37/m0-email-inventory-live.json \
     --out gov-infra/evidence/project37/m2-email-migration-live-dry-run.json
   ```

4. After review, prepare provider state only. The command reuses the existing
   per-agent mailbox password from
   `/lesser-host/soul/<stage>/agents/<agentId>/channels/email/migadu_password`
   and ensures the new Migadu mailbox plus inbound forwarding:
   `provider_prepared` requires the exact expected forwarding target
   `<agent-local-id>.<instance-slug>@inbound.lessersoul.ai`; an unrelated
   forwarding or alias is not sufficient.

   ```bash
   SOUL_EMAIL_INBOUND_DOMAIN=inbound.lessersoul.ai \
   go run ./scripts/soul-email-m2-migration \
     --stage live \
     --table-name lesser-host-live-state \
     --inventory gov-infra/evidence/project37/m0-email-inventory-live.json \
     --apply \
     --out gov-infra/evidence/project37/m2-email-provider-prepare-live.json
   ```

5. Each selected agent publishes a self-attested registration update advertising
   only the new instance-scoped email address, or Aron approves a disclosed host
   audit path before any host-side rewrite.
6. Registration sync performs the host switch:
   - `SoulAgentChannel.Identifier` becomes the instance-scoped address;
   - `SoulEmailAgentIndex` resolves the instance-scoped address;
   - `SoulEmailLegacyAliasIndex` maps the old known address to the new address;
   - public channel surfaces advertise only the instance-scoped address.
7. M3 canaries verify new-address inbound, legacy-alias inbound,
   non-advertisement of the legacy address, and body/lesser compatibility.

## Rollback and repair

Any partial failure enters `needs_repair` evidence. Rollback preserves inbound
reachability:

- Do not delete the old bare provider delivery during M2/M3.
- If provider prepare succeeded but registration was not published, leave the
  provider state in place and retry/self-attest when ready.
- If host switch occurred but registration publish failed, restore the previous
  `SoulAgentChannel` and `SoulEmailAgentIndex`, then disable/remove the internal
  legacy alias. Provider state remains until canaries prove safe cleanup.
- Never expose legacy aliases through public channel discovery or portal alias
  management.

## Credential envelope for reviewed apply

The M2 command reads Migadu API credentials through the existing runtime SSM
loader (`/lesser-host/migadu`) and reads the existing per-agent mailbox password
only when `--apply` is present. Evidence and logs must not include Migadu
credentials, mailbox passwords, raw SSM values, message content, or provider
payloads containing secrets.

No live apply under issue #339 is part of this milestone.
