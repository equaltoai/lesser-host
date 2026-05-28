# M4 Fleet UI — Design Fidelity Evidence

**Milestone:** M4 — Fleet UI  
**Issue:** equaltoai/lesser-host#539  
**Surface:** /portal Fleet page  
**Viewport:** 1440 × 900  
**Captured:** 2026-05-28 (fixture-based, Puppeteer + headless Chrome; re-captured after switching to page-local `formatValue` formatter)

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
- M1 primitives CSS (Metric, Sparkline, CostGauge host wrappers)

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

4. **Active users instead of per-instance souls.** Per-instance soul counts are not
   available from the M5 DTO. The InstanceCard shows `active_users_30d` (from M5) in
   place of a per-instance soul count. The top-level Soul metric card uses the aggregate
   count from `soulListMyAgents`.

5. **Table view is a simple accessible table.** The Cards/Table toggle switches between
   the card grid and a basic `<table>` with instance key columns. This keeps the table
   view scoped (as required) while providing an accessible alternative to the card grid.

## CSP posture

- No inline `<style>` attributes — all styling via component-scoped `<style>` blocks
  and imported CSS files.
- No inline event handlers — all interactivity via Svelte `onclick`/`on:click` bindings.
- No `eval()`, no `unsafe-eval`, no `unsafe-inline`.
- Status dots use CSS `[data-status]` attribute selectors with `::before` pseudo-elements
  — no inline `style` attributes.
- No third-party origins.

## No-budget action path

When an instance has no budget set (`budget.included_credits <= 0`), the `cost` snippet
renders "No budget set" with a "Set budget" link pointing to the instance detail page
(`/portal/instances/{slug}`) where the budget configuration lives. This is an existing
route/action affordance — no new backend or provisioning flow.

## Zero / one / many instance states

| State | Behavior |
|-------|----------|
| Zero instances | Greeting: "Welcome back" (or "Fleet" if no display name). Smart summary: "Create your first instance to get started with managed hosting." Metrics show zeros. Fleet panel shows empty message. Right rail panels show "No instances yet." Provisioning CTA card is always visible. |
| One instance | Same layout as many-instances. Smart summary reflects the single instance. Metric cards show accurate counts. |
| Many instances | Full grid of InstanceCards. New-instance CTA card appears at the end of the grid. |

## No {slug} template leaks

All slug references use `inst.slug` from the API response or fixture data. Routes are
constructed with `linkProps(`/portal/instances/${inst.slug}`)` which interpolates the
actual slug value. No literal `{slug}` string appears in rendered output.

## Fixture data shape

The fixture uses a static array of 4 instances:
1. `my-instance` — healthy, us-east-1, v2.4.1, budget set (210/500 cr), activity data
2. `staging-env` — warning, eu-west-1, v2.4.0, near budget (187/200 cr), activity data
3. `demo-site` — healthy, ap-southeast-1, v2.4.1, no budget, activity-only data
4. `dev-sandbox` — provisioning, us-west-2, v2.3.9, no budget, no activity data

This exercises all status states, budget/no-budget, data/no-data, and the
provisioning CTA alongside existing instances.

## Screenshot content verification

The captured `fleet.png` was programmatically verified to contain the expected
Fleet UI content and to NOT contain error-page text:

| Check | Result |
|-------|--------|
| "Welcome back" | ✓ present |
| "LIVE INSTANCES" (Metric card) | ✓ present |
| "Cost pulse" | ✓ present |
| "Heads up" | ✓ present |
| "Fleet" panel title | ✓ present |
| "Cards" / "Table" tab | ✓ present |
| "my-instance" instance card | ✓ present |
| "staging-env" instance card | ✓ present |
| "demo-site" instance card | ✓ present |
| "dev-sandbox" instance card | ✓ present |
| "New instance" CTA card | ✓ present |
| "No budget set" action path | ✓ present |
| "Not found" | ✗ NOT present |
| "This route does not exist" | ✗ NOT present |

## Validation results

| Check | Result |
|-------|--------|
| `cd web && npm run lint` | PASS |
| `cd web && npm run typecheck` | PASS (0 errors, 0 warnings) |
| `cd web && npm test` | PASS (165 tests, 20 files) |
| `cd web && npm run build` | PASS (CSP clean) |
| `bash gov-infra/verifiers/gov-verify-rubric.sh` | PASS |
| `file fleet.png` | PNG image data, 1440 × 900, 8-bit/color RGB, non-interlaced |
