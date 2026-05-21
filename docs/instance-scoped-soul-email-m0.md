# Instance-scoped Lesser Soul email — M0 decisions

Project 37: <https://github.com/orgs/equaltoai/projects/37>

Parent blocker: <https://github.com/equaltoai/lesser-host/issues/324>

Arch review carried forward: <https://github.com/equaltoai/lesser-host/issues/329#issuecomment-4509202671>

## Status

This document is the M0 contract for implementing instance-scoped managed Lesser Soul email. It is intentionally limited to decisions, invariants, and dry-run inventory shape. It does **not** implement M1 provisioning, M2 migration apply, provider mutation, live migration, custom handles, alias UI, vanity email domains, multiple public email channels, public alias discovery, cross-instance search, or mailbox redesign.

## Canonical address contract

The canonical managed email address for a Lesser Soul agent is:

```text
<agent-local-id>.<instance-slug>@lessersoul.ai
```

The provider local-part is:

```text
<agent-local-id>.<instance-slug>
```

Examples:

```text
pilot.simulacrum@lessersoul.ai
scout.simulacrum@lessersoul.ai
```

Contract requirements for M1/M2:

- `agent-local-id` uses the existing soul local ID normalization (`soul.NormalizeLocalAgentID`).
- `instance-slug` uses the managed instance slug validation already used by host (`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`).
- `instanceSlug` is resolved from the managed `Domain` record's `Domain.InstanceSlug`; code must not parse hostnames to infer instance identity.
- Exact primary domains, stage-aware managed primary aliases, and exact vanity domains must load a `Domain` record and use `Domain.InstanceSlug`.
- Stage-aware vanity aliases are not part of this release blocker. If no exact `Domain` record exists for a vanity domain, resolver paths fail closed unless a later explicit design adds that access pattern.
- The provider/SMTP local-part limit is 64 octets. Because the normalized local ID and slug are ASCII, host enforces `len(<agent-local-id>.<instance-slug>) <= 64` before provider calls.
- Local-part overflow fails closed with a clear conflict. There is no hash fallback in Project 37.
- Agent local IDs may contain dots. Code must never split the email local-part to recover `agentLocalID` or `instanceSlug`; resolver/index state is authoritative.
- New agents only receive the instance-scoped address. New provisioning must not issue bare `<agent>@lessersoul.ai` addresses.
- After migration, public/current channel surfaces advertise only the instance-scoped address.
- Legacy bare addresses are inbound-only aliases for existing agents. They are not public/current channels and are not issued to new agents.

## Domain resolution policy

M1 resolver code must mirror the existing managed-domain access posture:

1. Normalize the agent domain.
2. Attempt exact `Domain` lookup.
3. If exact lookup misses and the control-plane stage is non-live, permit the managed primary stage alias path (`dev.<base-domain>` / `staging.<base-domain>`) only when:
   - the base-domain `Domain` record exists;
   - `Domain.Type == primary`;
   - `Domain.VerificationMethod == managed`;
   - `Domain.Status` is `verified` or `active`;
   - the owning `Instance.HostedBaseDomain` equals the base domain; and
   - `Domain.InstanceSlug` is present and valid.
4. For exact vanity domains, use that vanity `Domain.InstanceSlug` directly.
5. For stage-aware vanity aliases, fail closed unless that support is intentionally added in a later scope.

This preserves Aron's correction in Arch review: vanity domains still require an instance slug at creation, and `Domain.InstanceSlug` remains the source of truth.

## Dry-run inventory command

M0 adds a dry-run inventory command:

```bash
go run ./scripts/soul-email-m0-inventory \
  --stage lab \
  --table-name lesser-host-lab-state \
  --provider-state path/to/redacted-migadu-provider-state.json \
  --registration-state path/to/redacted-registration-signing-state.json \
  --out gov-infra/evidence/project37/m0-email-inventory-lab.json
```

The command is read-only. It scans soul identities and loads only the host records needed to decide whether migration can proceed:

- `SoulAgentIdentity` (`agentId`, `domain`, `localId`, lifecycle, self-description version);
- `Domain` (`Domain.InstanceSlug`, type, status, verification method);
- `Instance` for managed stage-primary alias validation;
- `SoulAgentChannel` for the current email channel;
- `SoulEmailAgentIndex` for the current full-address mapping; and
- `SoulAgentVersion` for registration URI/hash and self-attestation presence.

It computes the proposed `<agent-local-id>.<instance-slug>@lessersoul.ai` address, detects duplicate proposed addresses, and detects local-part overflow before any provider or database mutation. If duplicates, overflow, missing/invalid instance slug state, missing/mismatched current email indexes, or other report-level issues are present, the command exits non-zero (`2`).

The optional provider-state file is a redacted operator snapshot, not a secret export. Shape:

```json
{
  "generated_at": "2026-05-21T00:00:00Z",
  "source": "operator-redacted-migadu-export",
  "addresses": [
    {
      "local_part": "pilot",
      "mailbox_exists": true,
      "forwardings": ["pilot@inbound.lessersoul.ai"],
      "aliases": ["pilot"]
    },
    {
      "local_part": "pilot.simulacrum",
      "mailbox_exists": false,
      "forwardings": [],
      "aliases": []
    }
  ]
}
```

Never include Migadu credentials, mailbox passwords, raw SSM values, message content, or provider payloads containing secrets in this file. The inventory report stores only mailbox existence, forwarding destinations, alias local-parts, and operator notes.

The optional registration-state file records the current registration email channel and signing posture without embedding full registration JSON or private key material. Shape:

```json
{
  "generated_at": "2026-05-21T00:00:00Z",
  "source": "redacted-registration-export",
  "agents": [
    {
      "agent_id": "0xabc...",
      "email_channel": "pilot@lessersoul.ai",
      "can_self_attest": "yes"
    }
  ]
}
```

`can_self_attest` is `yes`, `no`, or `unknown`. M2 apply remains blocked for `unknown` until the agent signing path or a separately approved host migration/audit path is known.


### Known seed addresses

The current local references that motivated M0 inventory are seeds only; the inventory command must verify live host/provider state rather than trust this list as authoritative:

- `agent-0@lessersoul.ai`
- `pilot@lessersoul.ai`
- `scout@lessersoul.ai`
- `counsel@lessersoul.ai`
- `medic@lessersoul.ai`
- `ops@lessersoul.ai`
- `advocate@lessersoul.ai`

### Inventory output fields

For each active managed soul email agent, the report records:

- `agent_id`, `domain`, `local_id`, status, lifecycle, and self-description version;
- domain resolution result and `Domain.InstanceSlug`;
- current email channel identifier/provider/status and whether an SSM secret reference is present;
- current email index owner and whether it matches the channel and agent;
- registration version URI/hash/change summary, whether a self-attestation is present, and the current registration email channel when a redacted registration-state snapshot is supplied;
- proposed provider local-part, proposed public address, local-part length, duplicate flag, and overflow flag;
- redacted provider state for current and proposed local-parts when supplied; and
- migration-readiness flags.

M0 inventory signoff requires:

- zero duplicate proposed addresses;
- zero local-part overflows;
- every live managed email agent has a verified/active `Domain` record with a valid `Domain.InstanceSlug`;
- every current managed email channel has a matching `SoulEmailAgentIndex`;
- provider state is known for every current bare address before M2 apply; and
- each agent's self-attestation/signing path is known before its public channel changes.

Fail-closed overflow is acceptable for new provisioning only if this inventory proves no existing live agent would overflow. If any existing live agent overflows, M1/M2 remain blocked until Aron explicitly chooses defer-vs-deterministic-fallback in a new scoped decision.

## Self-description integrity decision

Changing a public email channel from `agent@lessersoul.ai` to `agent.<instance>@lessersoul.ai` changes the agent's declared contact identity. It must not be a silent unsigned rewrite.

Decision:

- The preferred migration path is a new registration/self-description version signed by the agent using the existing self-attestation flow.
- M2 inventory must classify every existing agent as `can_self_attest=yes`, `can_self_attest=no`, or `can_self_attest=unknown` before apply.
- If an agent cannot sign, host does not silently rewrite that agent's public registration. The agent's public channel update is deferred until it can sign, unless Aron approves a separate host migration/audit path with explicit disclosure before implementation.
- A host migration/audit path, if later approved, must record why self-attestation was unavailable, what authority allowed the host-side change, old and new address, affected registration version(s), and rollback/notification evidence.
- The M2 live release gate blocks on any `unknown` signing state.

This preserves soul declaration integrity while still allowing Project 37 to proceed for agents that can re-attest.

## Legacy bare-address alias decision

Arch found that provider-only aliases are insufficient: inbound normalization produces `local@lessersoul.ai`, recipient resolution looks up `SoulEmailAgentIndex` by full normalized address, and channel matching requires the notification recipient to equal `SoulAgentChannel.Identifier`.

Decision: **M2 must add a host-internal legacy alias/canonicalization model, not rely on Migadu/provider aliases alone.**

Required contract:

- `SoulAgentChannel.Identifier` remains the current advertised address: `agent.<instance>@lessersoul.ai`.
- `SoulEmailAgentIndex` remains the current-address lookup for canonical public channels.
- Legacy bare aliases are represented by a separate host-internal alias/index record, scoped to existing migrated agents only.
- Canonicalization occurs in comm-worker recipient resolution after exact current `SoulEmailAgentIndex` lookup misses and before channel matching/mailbox capture.
- A known legacy alias maps `agent@lessersoul.ai` to the canonical `agent.<instance>@lessersoul.ai` and the owning `agentId`.
- Unknown bare addresses fail closed.
- Alias records are never returned as public/current channels and never issued for new agents.
- M1 new provisioning creates no legacy alias records.
- M2 migration creates legacy alias records only for the existing bare addresses discovered and approved in inventory.

M2.2 provider work must still keep old bare addresses inbound-reachable at Migadu, but provider state alone does not satisfy host delivery. M2.3 must add the host alias/index and comm-worker canonicalization required above.

### Required legacy alias tests/canaries

M2/M3 must prove:

- sending to `agent.<instance>@lessersoul.ai` resolves through `SoulEmailAgentIndex`, matches `SoulAgentChannel.Identifier`, and stores/delivers normally;
- sending to a known migrated `agent@lessersoul.ai` canonicalizes to `agent.<instance>@lessersoul.ai` before channel matching;
- unknown bare `unknown@lessersoul.ai` fails closed;
- public channel discovery returns only the instance-scoped address;
- an agent local ID containing a dot is not misparsed from the local-part; and
- two instances can host the same local ID without email collision.

## Migration state machine and invariants

M2 apply must be idempotent, dry-run-first, auditable, and safe to resume. The state machine below defines the required ordering; implementation details may use a script-local state file, operation records, or DynamoDB operation rows, but the observable states and invariants must hold.

### States

1. `inventory_dry_run`
   - Load host state and redacted provider state.
   - Compute proposed addresses.
   - Refuse to proceed on duplicate proposed addresses, local-part overflow, missing/invalid `Domain.InstanceSlug`, missing current channel/index, or unknown signing state.

2. `provider_prepare_pending`
   - No public host state changes yet.
   - Create or verify the new provider mailbox/forwarding for `<agent-local-id>.<instance-slug>`.
   - Preserve the old bare provider mailbox/alias/forwarding.
   - Do not delete unrelated legacy aliases.

3. `provider_prepared`
   - New provider local-part accepts inbound delivery to the same canonical host inbound bridge.
   - Old bare address still accepts inbound delivery.
   - No public channel switch has occurred yet.

4. `self_attestation_collected`
   - A new registration document advertising only the instance-scoped email channel has been prepared and signed by the agent, or a separately approved host migration/audit path exists.
   - The operation records the expected previous self-description version.

5. `host_switch_pending`
   - Preconditions revalidated: provider still prepared, current DB channel/index still match inventory expectations, expected registration version still current, and alias decision still applies.

6. `host_switched`
   - Host writes the canonical email channel/index and the legacy alias/index in an idempotent operation.
   - `SoulAgentChannel.Identifier` becomes the instance-scoped address.
   - `SoulEmailAgentIndex` resolves the instance-scoped address.
   - Legacy alias/index resolves only the known bare address to the canonical address/agent.
   - Public DB-backed surfaces may now show only the instance-scoped address.

7. `registration_published`
   - The signed registration/self-description version is published with the instance-scoped email channel.
   - The registration version/hash/self-attestation evidence is recorded.

8. `verified`
   - Canaries confirm new primary inbound delivery, legacy bare alias delivery, public non-advertisement of the bare alias, mailbox visibility, and body/Lesser/Greater/Sim compatibility evidence where applicable.

9. `needs_repair`
   - Any partial failure enters this state with redacted evidence and a stop condition. Live release remains blocked until repaired or explicitly rolled back.

10. `rolled_back`
    - Rollback preserves inbound reachability. If the host switch already occurred but registration publish failed, rollback restores the previous public channel/index and removes or disables the new host alias/index while leaving provider state intact until safe cleanup.

### Ordering invariants

- Provider new mailbox/forwarding exists before any public/current channel advertises the new address.
- Old bare provider delivery remains in place until M3 verifies legacy alias behavior.
- Self-attestation or an approved host migration/audit path exists before host switches public channel state.
- Host canonical channel/index and legacy alias/index switch before the release gate treats the new address as usable.
- Registration publish follows the host switch immediately in the same migration operation. If publish fails, the operation enters `needs_repair` and either resumes publish or rolls host public state back.
- Public surfaces advertise only the instance-scoped address after `host_switched`; legacy alias records remain internal.
- Unknown bare addresses fail closed before and after migration.
- Re-running apply at any state is safe: it verifies the state already reached, completes the next missing step, or stops with `needs_repair` without deleting unrelated provider state.

## M1/M2 implementation checklist carry-forward

M1 must:

- add the canonical address builder and `Domain.InstanceSlug` resolver;
- enforce the 64-octet provider local-part limit before provider calls;
- fail closed on missing, unverified, or cross-instance domain state;
- update begin/confirm preview parity so stale/bare local-part requests are rejected;
- use `<agent-local-id>.<instance-slug>` for Migadu mailbox and inbound forwarding local-parts;
- keep full email addresses opaque through inbound and comm-worker paths; and
- update UI/API examples so the address is shown as derived, not user-selected.

M2 must:

- use the inventory command before apply;
- add the host-internal legacy alias/index and comm-worker canonicalization boundary;
- require self-attestation or an explicitly approved host migration/audit path per agent;
- implement the state machine above with dry-run, idempotent retry, partial-failure resume, and rollback notes;
- preserve old bare provider delivery as inbound-only for migrated agents; and
- produce redacted evidence for the release gate.

## Scope cut

The following remain explicitly out of Project 37:

- custom handles;
- alias UI;
- vanity email domains;
- multiple public email channels per soul;
- public alias discovery;
- cross-instance email search; and
- general mailbox redesign.
