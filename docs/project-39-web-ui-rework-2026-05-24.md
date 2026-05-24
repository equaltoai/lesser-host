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

## Initial seed items

Nine initial items in Project 39: 8 parent-level issues + this planning PR. Sub-issues will be created at each milestone's `implement-milestone` walk start (per the parent issue bodies). This mirrors Project 37/38 cadence (~30 items each) by seeding the project with the structural anchors and letting sub-issues populate as work begins.

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

## Sub-issue creation cadence

Per each parent issue body, sub-issues are created at `implement-milestone` walk start, not now. This avoids creating 25 issues that may need revision once Signal A/D resolve.

When M0 implement-milestone begins, the sub-issues for #372 will be:

- M0 foundation (M0.1–M0.5)
- M0 CDK + tests (M0.12–M0.16)
- M0 gated UI components (M0.6–M0.11) — gated on Signal A + D
- M0 verifiers (M0.17–M0.23)
- M0 governance + docs (M0.24–M0.29)
- M0 lab deploy + soak

Equivalent sub-issue shapes for M1, M2, M3 are listed in their parent issue bodies.

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
2. Sub-issues for #372 are created at that time
3. Signal A + D coordination (parallel track, started at M0 implement-milestone kickoff)
4. Each milestone follows the same pattern: implement-milestone → sub-issues → commits → PRs → review → merge → lab deploy → soak → next milestone

If Signal A is resolved early (Week 1–2), M0 gated commits can land alongside the rest of M0; otherwise the provisional Stitch adoption per the enumerated change list gating note unblocks M0 by Week 3–4.

If live launch date is set, the calendar-week-relative roadmap (`docs/roadmap-web-ui-rework-2026-05-24.md` section "Calendar-week-relative phasing") maps backwards from cutover; aggressive variant (8 weeks total, defer M3.2) and conservative variant (20+ weeks) are documented there.
