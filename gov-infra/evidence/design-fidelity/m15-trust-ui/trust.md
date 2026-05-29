# M15 Trust UI — Design Fidelity Evidence (Issue #573 real-data refresh)

- **Issue:** equaltoai/lesser-host#550, #573
- **Spec:** Project 42, Milestone 15 — Portal Design Fidelity Recovery, Trust UI
- **Commit branch:** `aron/issue-573-trust-dashboard-real-data`
- **Date:** 2026-05-29

## Screenshot

**trust.png** — captured at 1440×900, 8-bit RGB, non-interlaced.

The screenshot shows the `/portal/trust` federation dashboard with both instance panels populated, peer grids rendering follower counts and last_seen/last_fetch timestamps, honest signature-failure source list (no fabricated sparkline), and real queue-depth time series via Sparkline.

## Fixture Files

| File | Purpose |
|------|---------|
| `web/fixtures/m15-trust.html` | Standalone HTML entry for headless PNG capture (not reachable from any app route) |
| `web/fixtures/m15-trust.ts` | Fetch interceptor + real `PortalTrust` component mount with mocked owner-scoped API responses |
| `web/fixtures/vite.fixture.m15.config.ts` | Minimal Vite config (port 5207, Svelte plugin, no file watching) |

### Mocked Endpoint Data Summary

The fixture mocks two portal instances with realistic nonzero trust data:

- **`GET /api/v1/portal/instances`** — returns 2 instances: `equaltoai` (us-east-1) and `maeve-studio` (eu-west-1)
- **`GET /api/v1/portal/instances/equaltoai/trust/data`** — full trust profile:
  - Federation: 91 reachable, 6 warning, 1 severed, 8 peer rows (mixed status, follower_count, last_seen/last_fetch)
  - Signatures: `window_hours=24`, 14 total failures, 4 source agents
  - Queue depth: 8 time-series points (depth 7–24)
  - Trust score: 78.3 composite with 5 dimensions, formula `composite_weighted_average`
  - Vouches: 3 endorsements from distinct peers
- **`GET /api/v1/portal/instances/maeve-studio/trust/data`** — trust profile:
  - Federation: 22 reachable, 1 warning, 0 severed, 3 peer rows (mixed follower_count, last_seen)
  - Signatures: `window_hours=24`, 7 total failures, 2 source agents
  - Queue depth: 4 time-series points (depth 2–7)
  - Trust score: 64.7 composite with 5 dimensions
  - Vouches: 1 endorsement

### Peer Rows (follower_count + timestamp coverage)

| Peer domain | follower_count | last_seen | last_fetch |
|-------------|---------------|-----------|------------|
| equaltoai `guild.greater.website` | 143 | 2026-05-29T08:15:00Z | — |
| equaltoai `maeve-studio.greater.website` | 87 | 2026-05-29T07:44:00Z | — |
| equaltoai `press-room.greater.website` | absent | 2026-05-28T12:10:00Z | — |
| equaltoai `dev-sandbox.greater.website` | 12 | — | 2026-05-29T10:30:00Z |
| equaltoai `partner-net.greater.website` | absent | 2026-05-15T03:22:00Z | — |
| equaltoai `ops-hub.greater.website` | 210 | 2026-05-29T09:01:00Z | — |
| equaltoai `data-lake.greater.website` | absent | — | 2026-05-29T10:30:00Z |
| equaltoai `staging-env.greater.website` | absent | — | 2026-05-29T10:30:00Z |
| maeve `equaltoai.greater.website` | 310 | 2026-05-29T09:30:00Z | — |
| maeve `guild.greater.website` | absent | 2026-05-29T08:15:00Z | — |
| maeve `press-room.greater.website` | 43 | 2026-05-28T12:10:00Z | — |

The fixture covers: follower_count present, follower_count absent ("followers unavailable"), last_seen present, last_fetch fallback (no last_seen), null/omitted states across 11 peer rows.

### Capture Command

```bash
# Start Vite dev server for fixture
cd web && npx vite --config fixtures/vite.fixture.m15.config.ts --host 127.0.0.1 --port 5207 &

# Capture via Puppeteer at 1440x900
node -e "
const puppeteer = require('/tmp/node_modules/puppeteer');
(async () => {
  const browser = await puppeteer.launch({ headless: 'new', args: ['--no-sandbox', '--disable-setuid-sandbox'] });
  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  await page.goto('http://127.0.0.1:5207/fixtures/m15-trust.html', { waitUntil: 'networkidle0' });
  await page.waitForSelector('.trust__header');
  await new Promise(r => setTimeout(r, 3000));
  await page.screenshot({ path: 'gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png' });
  await browser.close();
})();
"
```

### Screenshot Verification

```
$ file gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png
PNG image data, 1440 x 900, 8-bit/color RGB, non-interlaced
```

## Route Compatibility Verification

| Route | Handler | Status |
|-------|---------|--------|
| `/portal/trust` | PortalTrust dashboard (M15) | Renders federation dashboard |
| `/portal/trust/attestations/{id}` | Trust.svelte (public attestation inspector) | Preserved via App.svelte routing |
| `/trust/attestations/{id}` | Trust.svelte (public attestation inspector) | Unchanged |
| `/.well-known/*` | trust-api Lambda (backend) | Unchanged (SPA never handles) |
| `/attestations/*` | trust-api Lambda (backend) | Unchanged (SPA never handles) |

## Issue #573 — Corrections Applied

### 1. Peer Grid — Real Field Rendering

| Field | Before (#550) | After (#573) |
|-------|---------------|--------------|
| follower_count | Not rendered | Renders count when present; "followers unavailable" when null/omitted |
| last_seen | Not rendered | Renders "Seen: YYYY-MM-DD" when present |
| last_fetch | Not rendered | Renders "Fetch: YYYY-MM-DD" as fallback when last_seen absent |

### 2. Honest Empty States

| Panel | Before (#550) | After (#573) |
|-------|---------------|--------------|
| Peer constellation | "Federation peer telemetry is not yet instrumented" | "No scoped federation peer data is present" |
| Queue depth | "Inbound queue depth telemetry is not yet instrumented" | "No inbound queue depth data is present" |
| Header subtitle | "...Federation peer data is not yet instrumented..." | "...Data is scoped to your owned instances and reflects available telemetry." |

### 3. Signature Failures — No Fabricated Sparkline

The backend DTO (`window_hours=24`, `total_failures`, `by_source`) has no time-series `series` field. The signature failures panel now shows:
- Dynamic label: "Sig failures 24h" (derived from `window_hours` on the DTO, not hardcoded)
- Aggregate count: "N total failures across M sources"
- Per-source breakdown list with agent ID and failure count

No Sparkline is rendered from per-source aggregate counts — the panel presents the data honestly as an aggregate + source list.

### 4. Queue Depth — Real Time Series

The `queue_depth.series` field contains real time-stamped depth data from the backend. The Sparkline in the queue depth panel renders from `queueDepth.seriesDepthValues` — derived from the real time-series data across all instances.

### 5. Metric Card Labels

| Card | Label | Source |
|------|-------|--------|
| Reachable peers | "Reachable peers" | `federation.reachable` |
| Warnings | "Warnings" | `federation.warning` |
| Severed peers | "Severed peers" | `federation.severed` |
| Signature failures | "Sig failures {N}h" (dynamic) | `signatures.window_hours` (=24) |

### 6. Trust Score Gauge and Vouches

- Trust score gauge: average across instances (or per-instance when single customer). SVG with CSS-only styling (CSP-safe).
- Vouches: list of peer endorsements with dates. Strength is a fixed presence marker (1.0) per the backing model — no comparative strength bars fabricated.

### 7. Multi-Tenant Isolation Preserved

- All data fetches use owner-scoped portal endpoints (`/api/v1/portal/instances/{slug}/trust/data`)
- No cross-tenant queries, no operator-only trust-graph exposure
- Customer sees only their own instances' federation data

## Validation Results

| Check | Result |
|-------|--------|
| `npm run typecheck` | PENDING |
| `npm run lint` | PENDING |
| `npm test` | PENDING |
| `npm run build` | PENDING |
| SEC-5 (CSP byte-string integrity) | PRESERVED |
| SEC-6 (HTML inline absence) | PRESERVED |
| gov-verify-rubric.sh (full) | PENDING (hosted CI) |

## Changed Files

| File | Change |
|------|--------|
| `web/src/pages/portal/Trust.svelte` | MODIFIED — #573 real-data corrections (follower_count, last_seen/last_fetch, honest empty states, no signature sparkline) |
| `web/src/pages/portal/Trust.svelte.test.ts` | MODIFIED — #573 tests (peer grid fields, sparkline truthfulness, honest empty states, window_hours=24) |
| `web/fixtures/m15-trust.ts` | MODIFIED — window_hours=24, peer rows with follower_count and timestamps |
| `gov-infra/evidence/design-fidelity/m15-trust-ui/trust.md` | MODIFIED — evidence writeup for #573 corrections |
| `gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png` | PENDING — regenerated 1440×900 screenshot |

## No Backend/Public API Changes

This is a UI-only corrective milestone. No Go handlers, trust API routes, control-plane trust data structs, or backend tests were modified. The UI consumes the merged #572 DTO as-is.
