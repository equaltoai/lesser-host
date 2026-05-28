# M8 Instance Cost UI — Design Fidelity Evidence

**Milestone:** M8 (Project 42 — `aron/portal-m8-instance-cost-ui`)  
**Issue:** [#542](https://github.com/equaltoai/lesser-host/issues/542)  
**Viewport:** 1440 × 900  
**Evidence file:** `gov-infra/evidence/design-fidelity/m8-instance-cost-ui/cost-usage.png`

## Scope delivered

| # | Item | Status |
|---|---|---|
| 1 | Header cards: MTD spend vs budget (CostGauge + ProgressBar), Compute GB-sec (Metric + Sparkline), Egress GB (Metric) | ✅ Layout present |
| 2 | "Where the dollars go" breakdown table by service with progress bars and percent of total | ✅ Layout present |
| 3 | Budget alarms panel with warning/critical/cap threshold rows (all disabled) | ✅ Switches disabled with honest tooltips |
| 4 | Credit-based labels replaced with honest credit/dollar/GB-sec/GB language | ✅ No fabricated dollar conversions |
| 5 | Compute/egress derived from `entry.metrics[]` when unit data exists | ✅ Unavailable states rendered when metrics absent |

## Design surface walkthrough

### Header metric cards (3-column grid)

1. **MTD spend vs budget** — CostGauge showing credit utilization (used / included), ProgressBar below, dollar total shown as approximate via Badge when cost telemetry is available. Budget API is credit-denominated; credits are labeled honestly and dollar cost is shown as a separate approximate value from the cost telemetry endpoint (different data sources, different units — no fabricated conversion).

2. **Compute GB-sec** — Metric tile with total GB-sec derived from `entry.metrics[]` filtering for `unit` fields containing "GB-sec", "GB*s", or "GBs". When metrics are absent, renders "Not available — no GB-sec metrics in cost data". Sparkline renders daily GB-sec series when data exists.

3. **Egress GB** — Metric tile with total data transfer GB derived from `entry.metrics[]` filtering for data-transfer services and byte/GB units. Bytes are converted to GB (÷ 1024³). When metrics are absent, renders "Not available — no egress metrics in cost data".

### "Where the dollars go" breakdown table

- Grid table with columns: Service | Cost | Progress bar | % of total
- Service aggregation by `categoryLabel()` (same categorization as M3: Lambda, DynamoDB, Egress, others passthrough)
- Sorted by cost descending
- ProgressBar `max` is total_cost; tone accent for rows > 50%
- Empty state when cost data present but no per-service entries
- Loading and error states handled

### Budget alarms panel

- Three threshold rows: Warning (70%), Critical (90%), Cap (100%)
- Each row: label + threshold badge + description + toggle button
- All toggle buttons are `disabled` with `title="Budget alarm persistence is not yet available. Thresholds are local-only."`
- Stacked card style (borders merge between adjacent rows)

### Monthly credit summary

- Retained from prior design: included credits, used credits, remaining credits
- Extended with request/cache stats from usage summary when available

## Deliberate data deviations

| # | Surface | Design expectation | M8 rendering | Rationale |
|---|---|---|---|---|
| D1 | MTD spend value | Real dollar amount from single source | Credits shown via CostGauge; approximate dollar total from cost telemetry shown as separate Badge | Budget API returns credits, cost telemetry returns dollars — different data sources with different units. No fabricated conversion. |
| D2 | Compute GB-sec | Always available metric | "Not available" when cost telemetry lacks GB-sec metrics | Metrics are optional on `CostServiceEntry`; not all AWS services emit GB-sec |
| D3 | Egress GB | Always available metric | "Not available" when cost telemetry lacks data transfer metrics | Metrics are optional; data transfer may be tracked differently by different billing sources |
| D4 | Budget alarm toggles | Functional on/off switches | All toggles disabled with honest tooltip | No persistence endpoint exists for budget alarm configuration |
| D5 | Dollar amounts in breakdown | Direct dollar values from cost API | Dollar values from `PortalCostResponse.total_cost` and per-entry `cost` fields | Cost telemetry endpoint returns dollar-denominated values; no conversion needed |

## Deliberate visual deviations

| # | Surface | Design expectation | M8 rendering | Rationale |
|---|---|---|---|---|
| V1 | Compute sparkline interactivity | Hover tooltips on sparkline points | Static SVG sparkline (no tooltips) | Sparkline primitive is intentionally simple SVG; tooltips deferred to future milestone |
| V2 | Budget alarm switch styling | Toggle switch component | Disabled Button with On/Off label | No toggle-switch primitive exists; Button is the available disabled control primitive |
| V3 | CostGauge center text | Dollar amount | Credit percentage | Gauge compares credit units (used vs included); dollar total shown separately |

## Architectural decisions

| # | Decision | Reasoning |
|---|---|---|
| A1 | No new API endpoints | Per scope: "Use existing API in `web/src/lib/api/portalUsage.ts`; no new endpoints/backend." All data comes from portalGetBudgetMonth, portalGetUsageSummary, portalGetInstanceCost. |
| A2 | Credit/dollar separation | Budget API is credit-denominated. Cost telemetry is dollar-denominated. The two surfaces are kept separate — credits are never multiplied by a fabricated conversion factor to produce dollars. |
| A3 | Compute/egress derived from metrics | `CostAttributionMetric` entries include `unit` and `value` fields. Pattern matching on unit strings (GB-sec, GB, Bytes) derives compute and egress totals. |
| A4 | All alarm switches disabled | No backend endpoint exists for budget alarm CRUD. Disabled buttons with honest tooltips prevent false expectations. |

## States exercised

| State | Trigger | Rendering |
|---|---|---|
| Loaded (budget + usage) | Successful budget/usage fetch | Header cards with credit data, credit summary panel |
| Loaded (cost telemetry) | Successful cost fetch | Dollar badge, breakdown table, compute/egress metrics (or unavailable states) |
| Cost loading | costLoading && !cost | Spinner in cost panels |
| Cost error | costError && !cost | Alert with error message |
| No cost data | cost present but no entries | "No cost breakdown available" alert |
| No compute metrics | cost present but no GB-sec metrics | "Not available" Metric tone=info |
| No egress metrics | cost present but no data transfer metrics | "Not available" Metric tone=info |
| 401 (budget/usage) | 401 from either endpoint | Redirect to /login |
| 401 (cost) | 401 from cost endpoint | Redirect to /login |

## Fixture ↔ runtime alignment

The fixture `M8InstanceCostFixture.svelte` mounts the real `InstanceCost` component with a fixture token and slug. In the fixture environment (no backend), the component renders the loading state followed by error states, exercising the error/unavailable paths. Loaded states are exercised via development against the lab API.

The fixture is captured at 1440×900 via headless browser against `web/fixtures/m8-instance-cost-ui.html`.

## CSP, isolation, and governance

- ✅ Strict single-origin CSP preserved (no inline scripts, no inline styles, no new origins)
- ✅ Multi-tenant isolation preserved (all data sources enforce per-owner / per-slug ownership server-side)
- ✅ Trust-API instance-auth untouched
- ✅ All new Svelte files carry AGPL-3.0-only headers
- ✅ No new API endpoints
- ✅ No on-chain code path changed
- ✅ Budget API credit-denomination labeled honestly; no fabricated dollar conversion
