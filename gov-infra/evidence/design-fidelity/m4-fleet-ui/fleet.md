# M4 Fleet UI — Design Fidelity Evidence

**Milestone:** M4 — Fleet UI
**Issue:** equaltoai/lesser-host#539
**Surface:** /portal Fleet page
**Viewport:** 1440 × 900
**Captured:** 2026-05-28 (fixture-based, Puppeteer + headless Chrome; re-captured after review rework)

## Evidence artifacts

- `fleet.png` — 1440×900 PNG screenshot of the isolated M4 fixture rendering
- `fleet.md` — this document

## Capture details

| Property | Value |
|----------|-------|
| Tool | Puppeteer 25.1.0 (Chromium headless) |
| Viewport | 1440×900px (1× DPR) |
| URL captured | `http://localhost:5202/fixtures/m4-fleet.html` |
| Fixture HTML | `web/fixtures/m4-fleet.html` |
| Fixture component | `web/src/lib/components/__fixtures__/M4FleetFixture.svelte` |
| Fixture entry | `web/fixtures/m4-fleet.ts` |
| Vite config | `web/fixtures/vite.fixture.m4.config.ts` |
| Wait strategy | `waitForFunction` on `#fixture-root` children + 2s settle delay |
| Screenshot API | `page.screenshot({ path, fullPage: false })` |

### Why Puppeteer instead of `--headless=new --screenshot`

Chrome 148's `--headless=new --screenshot` does not wait for JavaScript execution
before capturing. `--virtual-time-budget` (used in M2/M3 evidence) is no longer
supported in newer Chromium `--headless=new` mode. Puppeteer's `waitForFunction`
ensures the Svelte component mounts before capture.

## Method

The screenshot was captured using a standalone fixture (`M4FleetFixture.svelte`) served via
`vite.fixture.m4.config.ts` and captured with Puppeteer (headless Chromium) at 1440×900. The fixture
renders the complete Fleet UI surface with realistic static data — no API calls.

Imported CSS stack (mirrors the real app):
- Greater tokens → Agent Genesis bridge → Greater primitives
- Greater Shell CSS
- Greater Host Platform CSS (FleetCard, CostGauge, etc.)
- M1 primitives CSS (Metric, Sparkline, CostGauge host wrappers, Eyebrow)

### Credit formatting

Budget gauges display values in credits (`cr`) rather than ISO currency codes. Since
`Intl.NumberFormat` rejects non-ISO codes, the `CostGauge`'s `currency` prop is not used
for credit display. Instead, `PortalFleet.svelte` and `M4FleetFixture.svelte` define a
page-local formatter (`formatCreditValue`) and pass it via CostGauge's `formatValue`
prop without a `currency` prop:

```ts
function formatCreditValue(value: number, _currency?: string): string {
    return `${formatSpend(value)} cr`;
}
```

This keeps credit formatting page-local and avoids any modification to vendored Greater
components (`web/src/lib/greater/host-platform/`). The `CostGauge.formatValue` hook is
the canonical extension point for custom formatting.

## Deliberate visual deviations from design spec

### Architectural deviations (correct by design)

1. **FleetCard composition.** The design spec references an `InstanceCard` primitive that the
   greater host-platform library ships as `FleetCard`. The M4 Fleet UI uses the existing
   `FleetCard` component with M5 DTO fields wired into metadata rows, cost/activity snippets,
   and the existing status badge. This is the canonical host-platform consumption path.

2. **Right rail inside page, not PageFrame aside.** The PortalShell already provides a
   `PageFrame` wrapper; the Fleet rail is implemented as a CSS grid column within
   `PortalFleet.svelte` rather than using `PageFrame`'s `aside` slot. This preserves
   the PortalShell's own use of `PageFrame` without cross-component slot coordination.

3. **Metric card tone keywords.** The design spec uses accent colors; the implementation
   uses CSP-safe CSS tone classes (`tone="accent"`, `tone="warning"`, etc.) mapped to
   `--ds-*` tokens through the `Metric` component's CSS class system. No inline styles.

4. **Per-instance souls not available.** Per-instance soul counts are not available
   from the M5 DTO. The InstanceCard shows `peak_daily_users_30d` (from M5) in place
   of a per-instance soul count. The top-level Soul metric card uses the aggregate
   count from `soulListMyAgents`. The metadata row label says "Active users" to
   accurately reflect the `peak_daily_users_30d` field semantics (max single-day
   unique users, not a 30-day reach).

5. **Table view is a simple accessible table.** The Cards/Table toggle switches between
   the card grid and a basic `<table>` with instance key columns. This keeps the table
   view scoped (as required) while providing an accessible alternative to the card grid.

### Visual fidelity deviations (intentional, with rationale)

6. **Greeting: sans-serif Heading, not the design's serif display type.** The
   `Heading` component from Greater Shell renders in the design system's sans-serif
   stack. The design mockup uses a serif/display typeface for the time-of-day
   greeting. Implementing a serif face would require either a new Greater Heading
   variant or a page-local override — deferred. The time-of-day pattern ("Good
   morning, Alice.") and the CUSTOMER PORTAL eyebrow are both implemented.

7. **Hero "Provision instance" button scrolls to the CTA card rather than opening
   an inline dialog.** The current provisioning flow requires a slug text field +
   create button available in the end-of-grid CTA card. The hero button scrolls
   to that card (smooth scroll). A truly inline quick-create dialog would require
   additional UI work. The end-of-grid CTA card remains as both the primary create
   interface and the scroll target. This is a two-step UX (hero → card) rather than
   one-step, but preserves the existing create flow intact.

8. **Cost pulse: wow% (week-over-week change) deferred.** Computing wow% requires
   a comparable previous-period value. The M5 DTO provides only the current 7-day
   sparkline window; a prior-period comparison value is not available from the
   current `portalListInstances` or `/api/v1/instance/metrics/daily` endpoints.

9. **Cost pulse: "LIVE" badge deferred.** The design mockup shows a "LIVE" badge
   next to the cost figure. The live-dot CSS animation (pulsing green dot) serves
   the same semantic purpose (indicating live data). Adding a text badge is a
   minor CSS enhancement deferred for a future pass.

10. **Right-now panel: step counter, ETA, and "View timeline" CTA unavailable.**
    The `InstanceResponse` DTO does not carry provisioning job step numbers, ETAs,
    or timeline links. Those fields live on the `ProvisionJobResponse` type
    (separate endpoint: `GET /api/v1/portal/instances/{slug}/provision`). Adding
    them to the fleet list response would require backend changes to join
    provisioning job state inline, which is outside the M4 UI-only scope. The
    right-now panel shows slug, region, provision status, and a "View details"
    link to the instance page as the closest available affordance.

11. **Metric cards use simpler sub-lines than the design's rich sub-context.**
    The design shows detailed sub-context per metric (e.g. "3 healthy, 1 warning"
    for instances). The implementation uses data that is already in hand without
    additional API calls: "of N total" for instances, "deployed agents" for souls,
    "of X cr budget" / "no budgets set" for spend, and "across N instances" for
    federation peers. These sub-lines are computed from the same DTO fields that
    feed the metric values and the card grid, so they stay in sync without
    additional loading states.

## CSP posture

- No inline `<style>` attributes — all styling via component-scoped `<style>` blocks
  and imported CSS files.
- No inline event handlers — all interactivity via Svelte `onclick` bindings.
- No `eval()`, no `unsafe-eval`, no `unsafe-inline`.
- Status dots use CSS `[data-status]` attribute selectors with `::before` pseudo-elements
  — no inline `style` attributes.
- Budget progress bar uses CSS `[data-ratio="N"]` selectors (0–100) for width — no
  inline `style` attributes. This mirrors the M1 ProgressBar pattern.
- No third-party origins.

## No-budget action path

When an instance has no budget set (`budget.included_credits <= 0`), the `cost` snippet
renders "No budget set" with a "Set budget" link pointing to the instance detail page
(`/portal/instances/{slug}`) where the budget configuration lives. This is an existing
route/action affordance — no new backend or provisioning flow.

## Zero / one / many instance states

| State | Behavior |
|-------|----------|
| Zero instances | Greeting: CUSTOMER PORTAL eyebrow + "Fleet" heading (no display name). Smart summary: "Create your first instance to get started with managed hosting." Metrics show zeros. Fleet panel shows empty message. Right rail panels show "No instances yet." Hero Provision instance button and CTA card are both visible. |
| One instance | Same layout as many-instances. Smart summary reflects the single instance. Metric cards show accurate counts. |
| Many instances | Full grid of InstanceCards. New-instance CTA card appears at the end of the grid. |

## No {slug} template leaks

All slug references use `inst.slug` from the API response or fixture data. Routes are
constructed with `linkProps(`/portal/instances/${inst.slug}`)` which interpolates the
actual slug value. No literal `{slug}` string appears in rendered output.

## Fleet cost sparkline correctness

The cost pulse sparkline is computed by **element-wise sum** across all instances' per-instance
`spark_cost` arrays. Each instance's `spark_cost` represents daily cost for the same N-day
window (last 7 days, oldest→newest). Summing by day index produces the fleet-level daily
cost curve. The previous implementation used `instances.flatMap(i => i.spark_cost).slice(0, 30)`,
which concatenated per-instance series end-to-end — producing a false trend that did not
represent fleet daily totals.

The corrected computation:

```ts
const maxLen = Math.max(0, ...instances.map(i => i.spark_cost?.length ?? 0));
const result = new Array(maxLen).fill(0);
for (const inst of instances) {
  const costs = inst.spark_cost ?? [];
  for (let day = 0; day < costs.length; day += 1) {
    result[day] += costs[day];
  }
}
```

## Budget utilization

The cost pulse panel shows aggregate fleet budget utilization:
- Period eyebrow: current month label (e.g. "May 2026")
- Spend: aggregate `used_credits` across all budgeted instances
- Budget cap: aggregate `included_credits` across all budgeted instances
- Progress bar: CSS `data-ratio` 0–100, ratio of total used to total budget
- Projected EOM: `mtdSpend + avgDailyCost × remainingDays`, computed when fleet sparkline data is available. Rounded with `formatSpend`.
- Sparkline: element-wise fleet cost sparkline (see above)

## Fixture data shape

The fixture uses a static array of 4 instances:
1. `my-instance` — healthy, us-east-1, v2.4.1, budget set (210/500 cr), activity data, peakDailyUsers=1243
2. `staging-env` — warning, eu-west-1, v2.4.0, near budget (187/200 cr), activity data, peakDailyUsers=312
3. `demo-site` — healthy, ap-southeast-1, v2.4.1, no budget, activity-only data, peakDailyUsers=0
4. `dev-sandbox` — provisioning, us-west-2, v2.3.9, no budget, no activity data, peakDailyUsers=0

This exercises all status states, budget/no-budget, data/no-data, and the
provisioning CTA alongside existing instances.

### Fixture ↔ runtime alignment

The fixture faithfully mirrors the runtime `PortalFleet.svelte`:
- Same component imports (FleetCard, CostGauge, Sparkline, Metric, Eyebrow, Panel, Heading, Text, Button, Link, TextField, Alert, Spinner, Badge)
- Same greeting structure: CUSTOMER PORTAL Eyebrow + time-of-day Heading + smart summary Text
- Same metric card layout: 4× Metric with sub props
- Same Fleet panel: Cards/Table tabs, FleetCard grid, CTA card
- Same right rail: Cost pulse (eyebrow, live dot, spend/budget, progress bar, sparkline), Right now (provisioning list with region + View details), Heads up (degraded + over-budget)
- Same CSS layout: grid-template-columns 1fr 260px, sticky right rail, auto-fill card grid
- Same CSP-safe patterns: data-ratio budget bar, data-status dots, CSS animations
- Same credit formatting: page-local formatCreditValue via CostGauge.formatValue hook

## Screenshot content verification

The captured `fleet.png` was programmatically verified to contain the expected
Fleet UI content and to NOT contain error-page text:

| Check | Result |
|-------|--------|
| "CUSTOMER PORTAL" (eyebrow) | ✓ present |
| "Good afternoon, Aron." (greeting) | ✓ present |
| "LIVE INSTANCES" (Metric card) | ✓ present |
| "Cost pulse" | ✓ present |
| "Heads up" | ✓ present |
| "Right now" | ✓ present |
| "May 2026" (period eyebrow) | ✓ present |
| "Fleet" panel title | ✓ present |
| "Cards" / "Table" tab | ✓ present |
| "my-instance" instance card | ✓ present |
| "staging-env" instance card | ✓ present |
| "demo-site" instance card | ✓ present |
| "dev-sandbox" instance card | ✓ present |
| "New instance" CTA card | ✓ present |
| "Provision instance" hero button | ✓ present |
| "No budget set" action path | ✓ present |
| "Not found" | ✗ NOT present |
| "This route does not exist" | ✗ NOT present |

## Field naming

The M5 Fleet DTO uses `peak_daily_users_30d` (max single-day unique users over the
30-day window). The card metadata row label says "Active users" for readability;
the underlying field name is `peak_daily_users_30d` throughout the Go backend,
TypeScript types, and Svelte component code.

## Validation results

| Check | Result |
|-------|--------|
| `cd web && npm run lint` | PASS |
| `cd web && npm run typecheck` | PASS (0 errors, 0 warnings) |
| `cd web && npm test` | PASS |
| `cd web && npm run build` | PASS (CSP clean) |
| `bash gov-infra/verifiers/gov-verify-rubric.sh` | PASS |
| `file fleet.png` | PNG image data, 1440 × 900, 8-bit/color RGB, non-interlaced |
