# Project 42 — Portal Redesign — design spec summary

Per-surface summary of the design fixture
(`https://api.anthropic.com/v1/design/h/sNM_e6zaXsWRLX9VetdC4w`,
extracted to `/tmp/design-sNM/lesser-host/` on the steward's host).

Each section lists:

- **Intent** — one paragraph of what the design wants the surface to do.
- **Reference** — file:line into the design fixture.
- **Primitives consumed** — the design's component vocabulary the
  surface depends on (drives the Foundation milestones).
- **Data shown** — what data needs to be on the wire to render this
  fully (drives whether the surface needs a backend milestone).

When a surface lists data that the existing host endpoints already
provide, the surface's milestone is UI-only. When data is missing,
a backend milestone precedes the UI milestone.

---

## Chrome — PortalShell (3-column app chassis)

**Intent.** A persistent 3-column layout: grouped sidebar (Overview /
Instances / Agents / Settings) + main content + optional contextual
right rail. Sidebar shows per-instance entries with status dots and
alert badges. Topbar carries breadcrumbs, the ⌘K search trigger, and
a notifications bell. Sidebar footer carries the user chip (display
name, role · auth method, sign-out) and — for admins — an "Operator
console" button.

**Reference.** `project/src/shell.jsx:18–186` (Sidebar, Topbar,
ContentWithRail, PageHeader, PortalShell).

**Primitives consumed.** `BrandMark`, `Avatar`, `Eyebrow`, `Badge`,
status `dot` element, `IcSearch`, `IcBell`, `IcLogout`, `IcShield`,
crumb separator.

**Data shown.** Session display name, role, auth method; instance
list (slug, status, alert flag) — already on the wire.

---

## Chrome — Command Palette (⌘K)

**Intent.** A global ⌘K (and Ctrl-K) keybinding opens a centered
overlay with a search input and four groups: Navigate, Actions,
Instances, Souls. Items fuzzy-filter against label and hint. Enter
navigates; Escape closes.

**Reference.** `project/src/shell.jsx:249–329` (CommandPalette),
`project/src/data.jsx:179–192` (COMMAND_PALETTE seed groups).

**Primitives consumed.** Modal backdrop, search input, group label,
item row, `kbd` chip, `IcSearch`, `IcArrowRight`.

**Data shown.** Static action seed + dynamic instance + soul lists —
all client-side, no new endpoint.

---

## Portal — Fleet (`/portal`)

**Intent.** Personalised landing. Greeting + one-sentence smart
summary at the top. Four metric cards (Live instances, Souls, MTD
spend, Federation). A "Fleet" panel with one card per instance:
status dot, CostGauge donut, 7-day activity sparkline, four metric
chips (Souls, Active users, Posts 24h, Federation). Right rail:
Cost pulse (live), Right-now/Provisioning, Heads-up/Alerts.

**Reference.** `project/src/portal-pages.jsx:3–165` (PortalHome,
InstanceCard).

**Primitives consumed.** `PageHeader`, `Panel`, `Metric`,
`InstanceCard`, `CostGauge`, `Sparkline`, `ProgressBar`,
`Badge`, `Tabs` (Cards/Table toggle), `Button`, `Alert`,
`Eyebrow`, live-dot indicator.

**Data shown.** Per-instance: slug, domain, stage, status, region,
spend, budget, projected, souls, peers, severed, sparkActivity,
sparkCost, activeUsers, posts24h, sigFails, provisioning state.
The existing `portalListInstances` + `portalBilling` endpoints cover
most fields; **activeUsers, posts24h, sigFails, sparkActivity,
sparkCost, severed** are missing from the host-side portal endpoints
and require a backend milestone (M5).

---

## Portal — Instance Detail (`/portal/instances/{slug}`)

**Intent.** Tabbed instance shell with six tabs: Overview, Cost &
usage, Configuration, Domains, Keys, Souls. Persistent right rail
with three panels: Snapshot (Instance ID, Region, Created, Domain,
Anchor freshness), Operate (Refresh anchor, Export config, Open
config…), Owners (avatar, handle, role).

**Reference.** `project/src/portal-pages.jsx:167–250` (InstanceDetail,
Tabs wiring, right rail).

**Primitives consumed.** `PageHeader` (with embedded badge + mono
domain), `Tabs` (with badges), `Panel`, `DL`, `DLItem`, `Avatar`,
`Button`.

**Data shown.** Instance metadata (already on the wire), souls
filtered by instance (already on the wire), owner list (needs
backend if not already exposed).

### Tab: Overview

**Intent.** Four metric cards (MTD spend, Active users 30d, Posts
24h, Sig failures). A **Stack card** (Lesser version, Body version
or "Add agentic", MCP wiring state, drift warning). A 7-day Activity
panel with two sparklines (Posts federated+local, Daily spend). A
Souls preview panel listing the souls bound to this instance.

**Reference.** `project/src/portal-pages.jsx:252–301` (InstanceOverview);
Stack card lives in `project/src/releases.jsx` (StackCard).

**Primitives consumed.** `Metric`, `StackCard`, `Panel`, `Sparkline`,
`Avatar`, `SoulStageBadge`, `Button`.

**Data shown.** activeUsers, posts24h, sigFails, sparkActivity,
sparkCost — same data the Fleet card needs; same backend gap.
Stack card needs Lesser version, Body version, MCP-wiring state —
**these are already plumbed** through host's instance state model.

### Tab: Cost & usage

**Intent.** Three header cards (MTD vs budget with progress, Compute
GB-sec sparkline, Egress GB), a "Where the dollars go" service-level
breakdown table with progress bars, and a Budget alarms panel with
configurable thresholds.

**Reference.** `project/src/portal-pages.jsx:303–378`
(InstanceCostUsage, BudgetRow).

**Primitives consumed.** `Panel`, `Sparkline`, `Badge`, `ProgressBar`,
`Switch`, `Button`, `gtable` (data table).

**Data shown.** Real-time cost telemetry **already shipped** in PR
#522. The layout / breakdown table / budget alarm rows need UI work
only; no new backend.

### Tab: Configuration

**Intent.** Three sections (Instance identity, Federation policy with
toggle rows, Rate limits).

**Reference.** `project/src/portal-pages.jsx:380–423`.

**Primitives consumed.** `Panel`, `DL`, `DLItem`, `Switch`,
`ConfigToggle` row primitive.

**Data shown.** Instance config — already on the wire via existing
config endpoints.

### Tab: Domains

**Intent.** Table of attached domains (name, role, state, TLS, issued
date) with an "Add domain" CTA.

**Reference.** `project/src/portal-pages.jsx:425–449`.

**Primitives consumed.** `Panel`, `gtable`, `Badge`, `Button`.

**Data shown.** Domain list — already on the wire.

### Tab: Keys

**Intent.** Table of API keys (label, masked token, scopes, created,
last used) with revoke and issue actions.

**Reference.** `project/src/portal-pages.jsx:451–476`.

**Primitives consumed.** `Panel`, `gtable`, `CopyChip`, `Button`.

**Data shown.** Key metadata — already on the wire.

### Tab: Souls

**Intent.** Table of souls bound to this instance (avatar + handle,
stage, model, anchor freshness, tips MTD) with a "Request soul" CTA.

**Reference.** `project/src/portal-pages.jsx:478–510` (InstanceSouls,
SoulStageBadge).

**Primitives consumed.** `Panel`, `gtable`, `Avatar`, `SoulStageBadge`,
`Badge`, `Button`.

**Data shown.** Soul list — already on the wire. Audit P0 bug: the
existing fetch returns empty for instances with bound souls. M0
investigates and fixes the fetch, not the layout.

---

## Portal — Billing (`/portal/billing`)

**Intent.** Spend analytics. Four metric cards (MTD, Projected EOM,
Per-active-user cost, Per-federated-post cost). Stacked weekly bar
chart (5 weeks, stacks by instance). Per-instance breakdown table
with burn progress. Right rail: This month (with sparkline),
Payment method, Recent invoices.

**Reference.** `project/src/portal-pages-2.jsx:3–169` (PortalBilling).

**Primitives consumed.** `PageHeader`, `Metric`, `Panel`,
`ProgressBar`, stacked-bar primitive (per-week aggregation),
`gtable`, `Badge`, `Button`.

**Data shown.** Per-instance MTD spend, budget, projected, accent
colour. Weekly aggregate of spend stacked by instance. MAU + per-
post denominators (computed). Invoice history. Payment method
(masked card + expiry).

Backend gaps: **invoice history** and **payment-method display**
likely need new control-plane handlers (or new fields on an existing
one). The weekly stacked aggregate may need a new aggregation
endpoint or can be computed in the SPA from existing daily data.

---

## Portal — Souls top-level (`/portal/souls`)

**Intent.** Roster of soul-bound agents tied to the customer's
instances. Filterable table (All / Graduated / In review tabs) with
columns: Soul, Instance, Stage, Anchor, Model, Tips · May. Right
rail: Roster status (totals by stage), Soul minting guidance.

**Reference.** `project/src/portal-pages-2.jsx:171–249` (PortalSouls).

**Primitives consumed.** `PageHeader`, `Panel`, `Tabs`, `gtable`,
`Avatar`, `SoulStageBadge`, `Badge`, `DL`, `DLItem`, `Button`.

**Data shown.** Soul roster — already on the wire.

---

## Portal — Soul Detail (`/portal/souls/{handle}`)

**Intent.** Per-soul detail. Manifest (handle, stage, model,
instance, requested, graduated, reviewer). Anchor gauge. Tip
earnings (this month + last + split). Three metric cards (Posts 30d,
Followers, Avg tip). Continuity-loop 7-day bar chart. Activity
timeline.

**Reference.** `project/src/portal-pages-2.jsx:251–369` (SoulDetail).

**Primitives consumed.** `PageHeader` (with avatar + handle layout),
`Panel`, `DL`, `DLItem`, `Avatar`, `CostGauge`, `Metric`, bar-chart
primitive, activity timeline row, `Alert`, `Badge`, `Button`.

**Data shown.** Soul manifest, anchor state, tip split, 7-day
continuity signals, activity events. The bar-chart + continuity
signal feed may need a backend milestone if not already exposed.

---

## Portal — Trust (`/portal/trust`)

**Intent.** Federation health dashboard. Four metric cards (Reachable
peers, Warnings, Severed last-30d, Sig failures 24h). Peer
constellation grid (each square = one peer with status dot, last
fetch time, follower count). Two sparkline panels (HTTP-sig
failures, Inbound queue depth). Right rail: Trust score gauge,
Vouches from peers (ranked bars), Severed alert.

**Reference.** `project/src/portal-pages-2.jsx:371–469` (PortalTrust).

**Primitives consumed.** `PageHeader`, `Metric`, `Panel`, `Sparkline`,
`Alert`, `ProgressBar`, `CostGauge` (re-used as trust gauge),
`peer` constellation grid primitive, dot indicator, `Badge`,
`Button`.

**Data shown.** Federation peer roll-up (reachable, warning,
severed), sig-failure counters, inbound queue depth, trust score,
vouches. **Most of this is not on the wire today** — Trust UI today
is an attestation API explorer. A trust-data milestone precedes the
trust-UI milestone.

The existing trust attestation endpoints stay; this is a new
operator-facing data surface, not a replacement of the attestation
API.

---

## Portal — Account (`/portal/account`)

**Intent.** Slim identity + session view. Identity DL (username,
display name, email, role). Session DL (method, wallet, token
expiry, IP) with Rotate-token + Sign-out-all actions.

**Reference.** `project/src/portal-pages-2.jsx:471–500` (PortalAccount).

**Primitives consumed.** `PageHeader`, `Panel`, `DL`, `DLItem`,
`Button`.

**Data shown.** Session metadata — already on the wire.

---

## Operator console (Layer 3, deferred)

The design also includes a complete Operator console with distinct
dark warm-charcoal chrome (`project/src/operator-pages.jsx`,
`project/src/releases.jsx`). Layer 3 of this bundle plans those
surfaces (Dashboard, Provisioning list + job detail, Approvals tabs,
Audit, Releases timelines + Stack Matrix, Instances, Tip registry,
Soul registry). Detailed planning happens after Layer 2 lands.

See `milestones/operator-console-layer.md` for the placeholder.

---

## Tokens and CSS

The design provides:

- `assets/tokens.css` — the `--ds-*` design-token surface (typography,
  colour, spacing, radius, shadow).
- `assets/app.css` — component-level CSS bound to those tokens.

Host's Project 39 design tokens (Agent Genesis + Greater defaults +
host bridge) are already on the runtime path per PR #524. The
design's `--ds-*` names should map onto the existing host token set;
where they don't, the **Foundation primitives milestone (M1)** adds
the missing mappings. Token additions are a foundation concern, not
a per-surface one.
