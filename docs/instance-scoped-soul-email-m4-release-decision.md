# Instance-scoped Lesser Soul email — M4 final release decision

Project 37 M4.2 records the final go/no-go decision for live release of the
instance-scoped `lessersoul.ai` namespace correction, linking the full M0–M4
evidence chain.

Issue: [#346](https://github.com/equaltoai/lesser-host/issues/346)

## Status

**TEMPLATE** — this document is the decision framework. The actual release
decision is recorded by filling the fields below with live evidence and
explicit sign-offs after M4.1 execution.

## Evidence chain

| Milestone | Evidence | Status |
|---|---|---|
| M0 | `gov-infra/evidence/project37/m0-email-inventory-live.json` | pending |
| M2 dry-run | `gov-infra/evidence/project37/m2-email-migration-live-dry-run.json` | pending |
| M2 provider prep | `gov-infra/evidence/project37/m2-email-provider-prepare-live.json` | pending |
| M3 canaries | `gov-infra/evidence/project37/m3-email-canary-live.json` | pending |
| M3 validation | `gov-infra/evidence/project37/m3-email-canary-live-validation.json` | pending |
| M4 runbook | `docs/instance-scoped-soul-email-m4-runbook.md` | complete |
| M4 cutline | `docs/instance-scoped-soul-email-m4-cutline.md` | complete |

### Lab evidence (reference)

Lab evidence from `v0.4.3`/`v0.4.4` on `lesser-host-lab`:

| File | Content |
|---|---|
| `m3-email-canary-lab.json` | Host-controlled canary evidence for Agent-0 and Arch |
| `m3-email-canary-lab-host-validation.json` | Passing host-only validation (legacy alias + unknown alias) |
| `m3-email-canary-lab-full-validation-blocked.json` | Full gate blocked on `--require-body-mcp` |
| `m3-email-alias-mapping-lab.json` | Redacted old→new mapping for 12 migrated lab advisor agents |
| `m3-email-canary-lab-blockers.json` | Explicit blockers and dependencies |

## Review outcomes

### Arch contract review

- **Status:** pending
- **Reviewer:** Arch (arch.simulacrum@lessersoul.ai)
- **Evidence reviewed:** (to be filled after live evidence is produced)
- **Findings:** (to be filled)
- **Recommendation:** proceed / proceed with caveats / block

### Ops operational review

- **Status:** pending
- **Reviewer:** Ops
- **Evidence reviewed:** (to be filled)
- **Findings:** (to be filled)
- **Recommendation:** proceed / proceed with caveats / block

## Risk classification

### Accepted risks (non-blocking)

| Risk | Rationale |
|---|---|
| `identity_whoami` may show legacy signed/on-chain registration email until registration republish | Body does not synthesize Host current channel state; accepted for dev/prerelease unless Aron explicitly changes the contract |
| Registration republish gap | Agents that self-attested in lab may need fresh attestation for live; accepted as a per-agent workflow, not a release blocker |

### Deferred risks (tracked separately)

| Risk | Tracking |
|---|---|
| Authoritative sender-identifier binding for `identity_verify(email, ..., messageId=...)` | Separate issue — not Project 37 |
| Custom handles, alias UI, vanity domains, multi-email profiles, public alias discovery | [#347](https://github.com/equaltoai/lesser-host/issues/347) backlog cutline |
| Sim upstream queue/backlog hygiene | Out of Project 37 scope |

### Blocking risks (must be resolved before live)

| Risk | Resolution required |
|---|---|
| (to be filled from live evidence) | |
| `lesser-body#275` — body MCP identity/mailbox canary evidence | Merge and deploy body release, then rerun M3 canaries with `--require-body-mcp` on live, OR record explicit Aron caveat accepting this gap |

## Caveats carried forward from M3

1. **Signed registration surfaces** (`/api/v1/soul/agents/{agentId}/registration`)
   returned legacy bare email addresses during the lab M3 pass. Host channel/index
   state is canonical but signed registration documents lag until agents republish
   self-attestations. This caveat is accepted for the live release gate unless
   Aron requires registration republish before sign-off.

2. **Body MCP evidence** (`--require-body-mcp`) was not available during the lab
   host-only M3 pass. If body release [#275](https://github.com/equaltoai/lesser-body/issues/275)
   is not merged before the live release decision, this gap must be explicitly
   accepted or the release deferred.

## Sign-off owners

| Role | Name | Sign-off |
|---|---|---|
| Contract (Arch) | Arch | pending |
| Operations (Ops) | Ops | pending |
| Release authority (Aron) | Aron | pending |

## Decision

**Release status:** pending

- [ ] All blocking risks resolved
- [ ] All evidence links populated
- [ ] Arch/Ops review complete
- [ ] Aron sign-off obtained
- [ ] Project 37 parent issue [#324](https://github.com/equaltoai/lesser-host/issues/324) updated

## Decision date

(to be filled)
