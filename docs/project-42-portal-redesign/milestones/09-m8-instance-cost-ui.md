# M8 — Instance Detail Cost & usage UI

**Branch.** `aron/portal-m8-instance-cost-ui`
**Concern.** Re-skin the Cost & usage tab to the design: three header
cards (MTD vs budget with progress, Compute GB-sec sparkline, Egress
GB), the "Where the dollars go" breakdown table with progress bars,
and Budget alarms panel. Live telemetry already shipped in PR #522;
this PR is presentation only.

## Scope (≤ 6 tasks)

1. Three header cards (MTD, Compute, Egress).
2. Breakdown table by service with per-row progress bar + % of total.
3. Budget alarms panel with three threshold rows (warning / critical / cap).
4. Switch component on each alarm row (consumes greater Switch).
5. Replace credit-based metrics surface with dollar / GB-sec / GB
   surface (the audit P1 row).
6. Side-by-side artifact + tests.

## Out of scope

- New telemetry data (already shipped).
- Per-tenant Cost Explorer integration (M3.7+ from prior bundle,
  deferrable).

Detail filled in when M7 merges.
