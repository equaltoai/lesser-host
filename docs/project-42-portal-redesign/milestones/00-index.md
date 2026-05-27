# Milestones — index

One-line summary for every milestone in the bundle. Each milestone is
its own branch, one PR, ≤ 8 tasks, single concern. Detail docs are
filled in only when the predecessor merges (except the first four,
which are detailed up front).

## Layer 0 — correctness

| ID | Title | Branch | Detail |
|---|---|---|---|
| M0 | P0 correctness bundle (6 tasks, no design changes) | `aron/portal-m0-p0-correctness` | [01-m0-p0-correctness.md](01-m0-p0-correctness.md) |

## Layer 1 — foundation

| ID | Title | Branch | Detail |
|---|---|---|---|
| M1 | Foundation primitives — CostGauge, Sparkline, Metric, Eyebrow, gauge variants | `aron/portal-m1-primitives` | [02-m1-primitives.md](02-m1-primitives.md) |
| M2 | PortalShell redesign — grouped sidebar, per-instance entries, identity chip, bell, ⌘K trigger | `aron/portal-m2-shell` | [03-m2-shell.md](03-m2-shell.md) |
| M3 | ⌘K command palette | `aron/portal-m3-cmdk` | [04-m3-cmdk.md](04-m3-cmdk.md) |

## Layer 2 — customer portal surfaces

Each surface is split into a UI-only milestone, and (when needed) a
data milestone that lands first. Outlines below; detail filled in
when the predecessor merges.

| ID | Title | Branch | Detail |
|---|---|---|---|
| M4 | Fleet UI — greeting + summary + 4 metric cards + InstanceCard grid + right rail | `aron/portal-m4-fleet-ui` | [05-m4-fleet-ui.md](05-m4-fleet-ui.md) |
| M5 | Fleet data — active users / posts 24h / sig fails / spark feeds on portal-list-instances | `aron/portal-m5-fleet-data` | [06-m5-fleet-data.md](06-m5-fleet-data.md) |
| M6 | Instance Detail Overview UI — 4 metric cards + Stack card + Activity panel + Souls preview + right rail | `aron/portal-m6-instance-overview-ui` | [07-m6-instance-overview-ui.md](07-m6-instance-overview-ui.md) |
| M7 | Instance Detail Overview data — owners endpoint if missing; sparkline backfill | `aron/portal-m7-instance-overview-data` | [08-m7-instance-overview-data.md](08-m7-instance-overview-data.md) |
| M8 | Instance Detail Cost & usage UI — "Where the dollars go" table + budget-alarm rows | `aron/portal-m8-instance-cost-ui` | [09-m8-instance-cost-ui.md](09-m8-instance-cost-ui.md) |
| M9 | Instance Detail Configuration + Keys UI — identity / federation / limits sections; Keys formatting | `aron/portal-m9-instance-config-keys-ui` | [10-m9-instance-config-keys-ui.md](10-m9-instance-config-keys-ui.md) |
| M10 | Instance Detail Souls tab UI — table + "Request soul" CTA | `aron/portal-m10-instance-souls-ui` | [11-m10-instance-souls-ui.md](11-m10-instance-souls-ui.md) |
| M11 | Billing UI — 4 metric cards + weekly stacked bar + breakdown table | `aron/portal-m11-billing-ui` | [12-m11-billing-ui.md](12-m11-billing-ui.md) |
| M12 | Billing data — invoices history + payment-method endpoint | `aron/portal-m12-billing-data` | [13-m12-billing-data.md](13-m12-billing-data.md) |
| M13 | Souls top-level UI — roster table + Roster Status + Minting guidance | `aron/portal-m13-souls-top-ui` | [14-m13-souls-top-ui.md](14-m13-souls-top-ui.md) |
| M14 | Soul Detail UI — manifest + anchor gauge + continuity-loop + activity timeline | `aron/portal-m14-soul-detail-ui` | [15-m14-soul-detail-ui.md](15-m14-soul-detail-ui.md) |
| M15 | Trust UI — federation health dashboard + peer constellation + sparklines | `aron/portal-m15-trust-ui` | [16-m15-trust-ui.md](16-m15-trust-ui.md) |
| M16 | Trust data — federation peer roll-up + sig-fail counters + trust score + vouches endpoint | `aron/portal-m16-trust-data` | [17-m16-trust-data.md](17-m16-trust-data.md) |
| M17 | Account UI — slim identity + session view; preserved current behaviours | `aron/portal-m17-account-ui` | [18-m17-account-ui.md](18-m17-account-ui.md) |

## Layer 3 — operator console

Detailed planning deferred until Layer 2 lands. Placeholder lives at
[operator-console-layer.md](operator-console-layer.md).

## Dependency posture

- **M0** has no dependencies.
- **M1, M2, M3** depend on M0 landing (so the legacy bugs don't drift
  back in during foundation work).
- **M4 onward** depend on M1, M2, M3 landing (primitives + shell +
  ⌘K must be in place before any per-surface UI work).
- **Data milestones** (M5, M7, M12, M16) land before their UI
  counterparts.
- The remaining UI milestones (M4, M6, M8, M9, M10, M11, M13, M14,
  M15, M17) are independently sequenceable after their data
  prerequisites — arch may reorder to optimise reviewer load or
  customer visibility.

## Acceptance contract (applies to every milestone)

Per the bundle README:

- ≤ 8 tasks; single concern; one PR off `main`.
- Each design-touching milestone commits a side-by-side fidelity
  artifact under
  `gov-infra/evidence/design-fidelity/<milestone>/<surface>.{png,md}`.
- Gov rubric green; no CSP loosening; no new external origins; AGPL
  headers on new files.
- Lab deploy + visual smoke against the design fixture before merge.
