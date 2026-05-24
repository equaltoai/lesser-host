# Project 39 — Web UI Rework — FaceTheory v3.3.0 + greater-components

Output of the `create-github-project` walk for the 2026-05-24 web/ UI rework. Translates the roadmap (`docs/roadmap-web-ui-rework-2026-05-24.md`, commit `74960b6`) into an equaltoai-org-level Projects v2 kanban.

## Project URL

<https://github.com/orgs/equaltoai/projects/39>

## Project metadata

- **Number**: 39
- **Title**: Web UI Rework — FaceTheory v3.3.0 + greater-components
- **Owner**: equaltoai
- **Visibility**: Private
- **Pattern**: follows Project 32 / 37 / 38 cadence (README + Status kanban + milestone groupings + parent + sub-issue hierarchy)

## Project items

Project 39 was initially seeded with 9 host structural anchors (8 parent-level issues + this planning PR). On 2026-05-24, after Greater expanded its companion milestones, host expanded the four host milestone parents into the full 78 enumerated-change sub-issues so the board now mirrors the same nested parent/sub-issue tracking cadence.

The planning PR itself ([#380](https://github.com/equaltoai/lesser-host/pull/380)) is included in the project as the active planning item per Arch's PR review recommendation (avoids the quiet-board seam recently corrected in Project 38); it moves to Done after merge.

| # | Title | Type | Blocks / Blocked by |
| --- | --- | --- | --- |
| [#372](https://github.com/equaltoai/lesser-host/issues/372) | M0 — FaceTheory foundation + design system + shell + Portal Fleet POC | Milestone parent | Blocks M1; gated UI components in M0 blocked by Signal A + D |
| [#373](https://github.com/equaltoai/lesser-host/issues/373) | M1 — Portal hero pages + Stack card + Cost & usage minimum | Milestone parent | Blocks M2; blocked by M0 lab soak |
| [#374](https://github.com/equaltoai/lesser-host/issues/374) | M2 — Operator Console + dark chrome + Provisioning evolution + Wire-all | Milestone parent | Blocks M3; blocked by M1 lab soak |
| [#375](https://github.com/equaltoai/lesser-host/issues/375) | M3 — Operator Console balance + (deferrable) cost-telemetry firehose | Milestone parent | Blocks R.1; blocked by M2 lab soak |
| [#376](https://github.com/equaltoai/lesser-host/issues/376) | Coordination — Signal A: Stitch shell vs greater-components shell ownership | Coordination | Blocks M0.6–M0.10 gated commits |
| [#377](https://github.com/equaltoai/lesser-host/issues/377) | Coordination — Signal D: greater-components additive component requests | Coordination | Contingent on Signal A; shapes M0.7–M0.10 + M2.6–M2.7 |
| [#378](https://github.com/equaltoai/lesser-host/issues/378) | R.1 — Sim canary walk-through against host lab post-M3 soak | Rollout | Blocks R.2; blocked by M3 lab soak |
| [#379](https://github.com/equaltoai/lesser-host/issues/379) | R.2 — Live cutover after milestone soak + sim canary signoff | Rollout | Blocked by R.1 sim signoff + all milestones soaked |
| [#380](https://github.com/equaltoai/lesser-host/pull/380) | Planning: web/ UI rework on FaceTheory v3.3.0 + greater-components (Project 39) | Planning PR | Active planning baseline; moves to Done after merge |

## Expanded host sub-issues (executed 2026-05-24)

The host milestone parents now have one GitHub sub-issue per enumerated commit from `docs/enumerated-changes-web-ui-rework-2026-05-24.md`:

| Parent | Expanded child issue range | Enumerated changes | Count | Notes |
| --- | --- | --- | ---: | --- |
| [#372](https://github.com/equaltoai/lesser-host/issues/372) | [#381](https://github.com/equaltoai/lesser-host/issues/381)–[#409](https://github.com/equaltoai/lesser-host/issues/409) | M0.1–M0.29 | 29 | M0.12 and SEC-8 still wait for the AppTheory mixed-auth composition implementation/docs; SEC-7 issues carry the FaceTheory marker-scoped OAC transport nuance. |
| [#373](https://github.com/equaltoai/lesser-host/issues/373) | [#410](https://github.com/equaltoai/lesser-host/issues/410)–[#426](https://github.com/equaltoai/lesser-host/issues/426) | M1.1–M1.17 | 17 | Portal hero pages + stack-state endpoint + SEC-11. |
| [#374](https://github.com/equaltoai/lesser-host/issues/374) | [#427](https://github.com/equaltoai/lesser-host/issues/427)–[#444](https://github.com/equaltoai/lesser-host/issues/444) | M2.1–M2.18 | 18 | Operator console + drift/wire-all + SEC-12/MAI-5. |
| [#375](https://github.com/equaltoai/lesser-host/issues/375) | [#445](https://github.com/equaltoai/lesser-host/issues/445)–[#458](https://github.com/equaltoai/lesser-host/issues/458) | M3.1–M3.14 | 14 | M3.7–M3.14 remain deferrable/conditional per the roadmap. |

All 78 child issues are in Project 39 with Status `Todo`, and linked through GitHub's sub-issue relation so the project `Parent issue` / `Sub-issues progress` fields are populated automatically.

## Labels used

From existing host label catalog:

- `host-web` — web/ portal and UI
- `host-cdk` — AWS CDK and runner infrastructure
- `host-csp` — Content security policy posture
- `host-framework-feedback` — Framework feedback to AppTheory/TableTheory/FaceTheory
- `host-governance` — gov-infra rubric and evidence
- `host-provisioning` — Managed provisioning and per-slug rollout
- `host-reliability` — Operational reliability and DoS hardening
- `host-trust-api` — Trust API, attestations, instance auth

## Coordination (executed 2026-05-24)

- **Greater Components steward** (`greater.equaltoai@theorymcp.ai` — Aron is the upstream maintainer): **request sent** via host_lab MCP `email_send` (delivery `delivery-f3c1b7a6f664bb27`). Signal A resolved Aron-direct (Greater owns UI primitives across all equaltoai products); Signal D additive request list expanded with shell primitives (Shell, Sidebar, Topbar, Panel, StatCard, SummaryStrip) plus the hosted-platform UI vocabulary (CommandPalette, FleetCard, CostGauge, ActivitySparkline, ProvisioningTimeline, ReleaseTimeline, StackMatrix). Aron coordinates triage through implementation. Project 39 issue #377 updated with the expanded list.
- **AppTheory + FaceTheory stewards** (theory-cloud org — Aron is the upstream maintainer): **framework issues opened** at [theory-cloud/AppTheory#593](https://github.com/theory-cloud/AppTheory/issues/593) (mixed-auth co-origin composition under `AppTheorySsrSite`) and [theory-cloud/FaceTheory#248](https://github.com/theory-cloud/FaceTheory/issues/248) (OAC form transport composition + strict-CSP Svelte adapter + tenant ISR pointers). Per Aron's direction, host waits for upstream resolution before moving forward with M0.12 CDK adoption. Aron is solving these in a neighboring computer; host steward tracks status via the upstream issues.
- **Sim steward**: to be notified at M3 lab deploy time per #378 (no change to this cadence).
- **No advisor-dispatched coordination**: this is Aron-direct. Note: Arch (advisor agent) reviewed planning PR #380 on 2026-05-24; review handled per the `review-advisor-brief` discipline under Aron's standing authorization ("consider arch requests as under my authority").

**Steward note**: Aron maintains both the equaltoai stack (lesser-host, greater-components, etc.) and the theory-cloud framework stack (AppTheory, FaceTheory, etc.). The "wait for upstream resolution" discipline still applies as a process gate even when the upstream maintainer is the same person — it preserves the audit trail and ensures the framework changes ship through the framework's own release cycle rather than landing as host-local patches. Cycle time is dramatically faster than a third-party-steward roundtrip would be.

## Working method

Per the project README: "Treat this as a kanban. Move issues through Status as evidence is gathered and blockers become concrete." Each milestone lands in lab, soaks, before next milestone starts merging. No live cutover until all milestones merged + soaked + sim-canaried.

## Handoff

If the planning PR is approved + merged to main:

1. M0 `implement-milestone` walk begins
2. Use the already-created #372 sub-issues (#381–#409) as the implementation queue
3. Signal D coordination continues in parallel with Greater
4. Each milestone follows the same pattern: implement-milestone → child issue queue → commits → PRs → review → merge → lab deploy → soak → next milestone

If live launch date is set, the calendar-week-relative roadmap (`docs/roadmap-web-ui-rework-2026-05-24.md` section "Calendar-week-relative phasing") maps backwards from cutover; aggressive variant (8 weeks total, defer M3.2) and conservative variant (20+ weeks) are documented there.
