# M15 Trust UI — Design Fidelity Evidence

- **Issue:** equaltoai/lesser-host#550
- **Spec:** Project 42, Milestone 15 — Portal Design Fidelity Recovery, Trust UI
- **Commit branch:** `aron/portal-m15-trust-ui`
- **Date:** 2026-05-29

## Screenshot

**trust.png** — captured at 1440×900, 8-bit RGB, non-interlaced, 277 KB.

The screenshot shows the `/portal/trust` federation dashboard with both instance panels populated and all UI sections visible.

## Fixture Files

| File | Purpose |
|------|---------|
| `web/fixtures/m15-trust.html` | Standalone HTML entry for headless PNG capture (not reachable from any app route) |
| `web/fixtures/m15-trust.ts` | Fetches interceptor + real `PortalTrust` component mount with mocked owner-scoped API responses |
| `web/fixtures/vite.fixture.m15.config.ts` | Minimal Vite config (port 5207, Svelte plugin, no file watching) |

### Mocked Endpoint Data Summary

The fixture mocks two portal instances with realistic nonzero trust data:

- **`GET /api/v1/portal/instances`** — returns 2 instances: `equaltoai` (us-east-1) and `maeve-studio` (eu-west-1)
- **`GET /api/v1/portal/instances/equaltoai/trust/data`** — full trust profile:
  - Federation: 91 reachable, 6 warning, 1 severed, 8 peer rows (mixed status)
  - Signatures: `window_hours=168`, 14 total failures, 4 source agents
  - Queue depth: 8 time-series points (depth 7–24)
  - Trust score: 78.3 composite with 5 dimensions, formula `composite_weighted_average`
  - Vouches: 3 endorsements from distinct peers
- **`GET /api/v1/portal/instances/maeve-studio/trust/data`** — trust profile:
  - Federation: 22 reachable, 1 warning, 0 severed, 3 peer rows
  - Signatures: `window_hours=168`, 7 total failures, 2 source agents
  - Queue depth: 4 time-series points (depth 2–7)
  - Trust score: 64.7 composite with 5 dimensions
  - Vouches: 1 endorsement

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
$ ls -la
-rw-rw-r-- 1 aron aron 276,701 ... trust.png
```

## Route Compatibility Verification

| Route | Handler | Status |
|-------|---------|--------|
| `/portal/trust` | PortalTrust dashboard (M15) | Renders federation dashboard |
| `/portal/trust/attestations/{id}` | Trust.svelte (public attestation inspector) | Preserved via App.svelte routing |
| `/trust/attestations/{id}` | Trust.svelte (public attestation inspector) | Unchanged |
| `/.well-known/*` | trust-api Lambda (backend) | Unchanged (SPA never handles) |
| `/attestations/*` | trust-api Lambda (backend) | Unchanged (SPA never handles) |

## Deliberate Visual Deviations

### M16 Fields Not Yet Instrumented (Honest Empty States)

1. **Federation peer data**: The M16 endpoint returns `federation.reachable=0`, `federation.warning=0`, `federation.severed=0`, and `federation.peers=[]`. The fixture provides realistic nonzero data for visual evidence; the production UI renders an honest "not yet instrumented" callout when peer data is empty.

2. **Follower count / last-fetch**: The M16 `portalTrustFederationPeerRow` carries only `domain` and `status`. The peer grid does NOT fabricate follower counts or last-fetch timestamps.

3. **Queue depth time series**: The M16 endpoint returns `queue_depth.series=[]`. The fixture provides realistic time-series data for visual evidence; the production UI renders an honest "not yet instrumented" callout when series is empty.

### Vouches Rendered as List/Count (Per M16 Contract)

- `vouches.items[].strength` is a fixed presence marker (`1.0`). The M15 UI renders vouches as a list of peer names with dates, NOT as comparative strength bars. This is documented in both the M16 data contract and the M15 design spec.

### Signature Failures Sparkline

- The M16 endpoint returns per-source aggregate counts (`by_source`), not time-bucketed series. The sparkline visualizes the per-source failure counts as a ranked series rather than a time series. The source list below the sparkline provides the agent ID and count for each data point.

### Trust Score Gauge

- When multiple instances exist, the gauge shows the average composite score across all instances. Single-instance customers see the per-instance score.
- The gauge uses SVG with CSS class-based status coloring (ok/warning/danger) — strictly CSP compliant (no inline styles).

### Dynamic Labels (Arch Rework)

- **Signature failures card**: Label is dynamic — `Sig failures {windowHours}h` — computed from `signatures.windowHours` (M16 backend returns `window_hours=168`). Not hardcoded "24h".
- **Severed peers card**: Label is `Severed peers` — honest representation of the M16 `federation.severed` field without an unsupported "last 30d" window claim.

## Validation Results

| Check | Result |
|-------|--------|
| `npm run typecheck` | PASS (0 errors, 0 warnings) |
| `npm run lint` | PASS (0 errors) |
| `npm test` | PASS (23 files, 226 tests) |
| `npm run build` | PASS |
| SEC-5 (CSP byte-string integrity) | PASS |
| SEC-6 (HTML inline absence) | PASS |
| gov-verify-rubric.sh (full) | NOT RUN locally (script exceeds environment timeout); relies on hosted `gov-rubric` CI check |

## Changed Files

| File | Change |
|------|--------|
| `web/src/lib/api/portalTrust.ts` | NEW — TypeScript API client with types for M16 trust data |
| `web/src/pages/portal/Trust.svelte` | NEW — M15 federation trust dashboard page (957→961 lines, dynamic labels fix) |
| `web/src/pages/portal/Trust.svelte.test.ts` | NEW — 26 tests (CSP, route, DOM, data contract, label correctness) |
| `web/src/App.svelte` | MODIFIED — split trust routing |
| `web/src/pages/Portal.svelte` | MODIFIED — added `portalTrust` route kind |
| `web/fixtures/m15-trust.html` | NEW — fixture HTML entry |
| `web/fixtures/m15-trust.ts` | NEW — fixture TypeScript entry with mock fetch + component mount |
| `web/fixtures/vite.fixture.m15.config.ts` | NEW — Vite config for fixture |
| `gov-infra/evidence/design-fidelity/m15-trust-ui/trust.md` | MODIFIED — evidence writeup |
| `gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png` | NEW — 1440×900 screenshot |

## No Backend/Public API Changes

This is a UI-only milestone. No Go handlers, trust API routes, control-plane trust data structs, or backend tests were modified.
