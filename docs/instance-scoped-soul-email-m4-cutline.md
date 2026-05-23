# Instance-scoped Lesser Soul email — M4 backlog cutline

Project 37 is bounded to the instance-scoped namespace correction and legacy
alias migration. This document records what is **explicitly deferred** to prevent
the release blocker from expanding into a full email product surface.

Issue: [#347](https://github.com/equaltoai/lesser-host/issues/347)

## Deferred items (post-live backlog)

None of the following are required to ship the namespace correction. Each is
tracked as a separate post-live issue or left for future scoping.

| Item | Status | Rationale |
|---|---|---|
| Custom email handles (user-chosen local-parts) | Deferred | Instance-scoped address is derived from `agent-local-id` and `instance-slug`. User-chosen handles are a separate product feature. |
| Alias management UI | Deferred | Legacy bare aliases are host-internal only. No alias CRUD surface is built or planned in Project 37. |
| Vanity email domains (`@custom.domain`) | Deferred | Requires domain verification, DNS configuration, and per-vanity-domain provisioning. Out of scope. |
| Multi-email profiles per soul | Deferred | Each agent has one canonical email channel. Multiple channels are a future design decision. |
| Public alias discovery | Deferred | Aliases are internal canonicalization records, not searchable public data. |
| Cross-instance email search | Deferred | Email lookup is scoped to the owning instance. Cross-instance search is not a namespace-correction concern. |
| Mailbox redesign / multi-mailbox support | Deferred | The existing single-mailbox-per-agent model is unchanged. |
| Authoritative sender-identifier binding for `identity_verify` | Deferred | Message-scoped positive verification requires explicit host authoritative binding. Track separately. |
| Sim upstream queue/backlog hygiene | Deferred | Operational concern for the Simulacrum instance; not a host release blocker. |
| Body-owned mailbox truth or alias resolver | Deferred | Host owns the canonical channel/index/alias state. Body synthesizes from Host. No Host→Body authority inversion in Project 37. |
| `identity_whoami` Host-channel synthesis in Body | Deferred | Body may show legacy signed registration email until republish. Host does not push current channel state into Body's identity response unless Aron explicitly changes the contract after M4. |

## What Project 37 does ship

To confirm the cutline is clean, these are the in-scope deliverables that
constitute the namespace correction:

1. Canonical address contract: `<agent-local-id>.<instance-slug>@lessersoul.ai`.
2. Instance-slug resolution from managed `Domain` records.
3. New provisioning creates only instance-scoped addresses.
4. M2 migration converts existing agents from bare to instance-scoped addresses.
5. Legacy bare addresses remain inbound-only via host-internal `SoulEmailLegacyAliasIndex`.
6. Public channel surfaces advertise only the instance-scoped address.
7. Unknown bare addresses fail closed.
8. M3 canaries prove end-to-end behavior.
9. M4 runbook, release decision, and this cutline document the release gate.

## Live release checklist confirmation

Before live sign-off, confirm:

- [ ] None of the deferred items above have crept into the release scope.
- [ ] No new email features have been added beyond the namespace correction.
- [ ] The project parent issue [#324](https://github.com/equaltoai/lesser-host/issues/324)
      lists this cutline as the final scope boundary.

## Post-live tracking

Deferred items that warrant near-term tracking:

| Item | Proposed tracking |
|---|---|
| Custom handles / alias UI | New GitHub issue (TBD) |
| Vanity email domains | New GitHub issue (TBD) |
| Authoritative sender-identifier binding | New GitHub issue (TBD) |
| Registration republish for agents that self-attested in lab only | Per-agent workflow; not a code change |

Items without proposed tracking are deferred indefinitely until a future scoping
decision.
