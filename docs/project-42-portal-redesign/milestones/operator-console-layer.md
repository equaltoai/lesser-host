# Layer 3 — Operator Console (deferred)

Detailed planning is intentionally deferred until Layer 2 (the
customer portal surfaces M4–M17) has landed. The operator console
is functionally separate, lower-traffic, and easier to plan when
the customer-side primitives + shell conventions have stabilised
in production.

## What the design defines

The Claude Design bundle includes a complete Operator console with
**distinct dark warm-charcoal chrome** so it can never be mistaken
for the customer portal. Surfaces:

- **Operator Dashboard** (`/operator`). Engine-room view: 4 metric
  cards (Customers, Instances, MTD revenue, Alarms), Provisioning
  in-progress panel, Approvals queue summary, Live ops right rail,
  Fleet anchor freshness, last-24h alarms.
  Reference: `project/src/operator-pages.jsx:6–106`.
- **Provisioning** list + job detail. Job kinds: `provision`,
  `update-lesser`, `update-body`, `wire-mcp`. Live timeline with
  per-step status; MCP-drift banner with "Wire all" CTA.
- **Releases** (`/operator/releases`). Two side-by-side release
  timelines (Lesser + Body), each with channel / breaking / latest
  badges and per-version fleet adoption bars. Below: Stack Matrix
  table of every instance's Lesser + Body + MCP wiring state with
  per-row "Update" or "Wire MCP" CTA.
  Reference: `project/src/releases.jsx` (entire file).
- **Approvals** (`/operator/approvals/{domains,users,external}`).
  Tabbed surfaces for vanity domains, user signups, external
  registrations. Existing data; new layout.
- **Audit log** (`/operator/audit`). Existing data; new layout.
- **Instances** (`/operator/instances`). Cross-customer instance
  registry view.
- **Tip registry** (`/operator/tip-registry`). On-chain tip
  operations view.
- **Soul registry** (`/operator/soul`). Operator-side soul roster.

## Sub-milestones (placeholder — not detailed)

When this layer is planned, the same bundle rules apply:

- ≤ 8 tasks per milestone.
- Single concern per milestone.
- UI vs. data separation.
- Side-by-side fidelity artifacts per milestone.

Tentative sub-milestone names:

- **L3.M1** Operator dark chrome (OperatorShell + topbar variant + sidebar groups + sidebar footer "Back to portal" button).
- **L3.M2** Operator Dashboard UI.
- **L3.M3** Operator Releases UI (the two timelines).
- **L3.M4** Stack Matrix UI.
- **L3.M5** Provisioning list UI.
- **L3.M6** Provisioning job-detail UI (live timeline).
- **L3.M7** Approvals tabs UI (domains / users / external).
- **L3.M8** Audit log UI.
- **L3.M9** Instances registry UI.
- **L3.M10** Tip registry UI.
- **L3.M11** Soul registry UI.

Data milestones interleave where new operator endpoints are
needed (release-timeline data, per-version adoption counts, MCP
drift roll-up, etc.).

## Trigger to start

Layer 3 detailed planning begins after **M17** merges, **or** after
a clear operator pain point makes a specific Layer 3 surface a
priority. Arch may sequence Layer 3 differently based on
operational needs; this placeholder records the design intent for
that conversation.
