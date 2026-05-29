# M15 Trust UI — Design Fidelity Evidence (Issue #573 real-data refresh)

- **Issue:** equaltoai/lesser-host#550, #573
- **Spec:** Project 42, Milestone 15 — Portal Design Fidelity Recovery, Trust UI
- **Commit branch:** `aron/issue-573-trust-dashboard-real-data`
- **Date:** 2026-05-29

## Screenshot

**trust.png** — refreshed from the M15 fixture at **1440×900**, 8-bit RGB, non-interlaced.

The screenshot shows the `/portal/trust` federation dashboard with owner-scoped fixture data: peer rows render follower-count present/absent states plus `last_seen`/`last_fetch`; the signature panel renders a Sparkline from the new `signatures.series[].failures` DTO field; source aggregates remain a separate breakdown; the metric card label reflects the 24h backend window.

The queue-depth panel remains in the page below the first 900px viewport in this capture. It is still populated by the fixture and covered by tests from `queue_depth.series[].depth`; it was not converted to a shared `value` point shape.

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
  - Signatures: `window_hours=24`, 14 total failures, 4 source agents, 7 timestamped hourly series buckets summing to 14
  - Queue depth: 8 time-series points using `{timestamp, depth}` (depth 7–24)
  - Trust score: 78.3 composite with 5 dimensions, formula `composite_weighted_average`
  - Vouches: 3 endorsements from distinct peers
- **`GET /api/v1/portal/instances/maeve-studio/trust/data`** — trust profile:
  - Federation: 22 reachable, 1 warning, 0 severed, 3 peer rows (mixed follower_count, last_seen)
  - Signatures: `window_hours=24`, 7 total failures, 2 source agents, 4 timestamped hourly series buckets summing to 7
  - Queue depth: 4 time-series points using `{timestamp, depth}` (depth 2–7)
  - Trust score: 64.7 composite with 5 dimensions
  - Vouches: 1 endorsement

### Additive Backend DTO Field

Issue #573 required a true signature-failure time series. The control-plane endpoint now returns an additive field:

```json
"signatures": {
  "window_hours": 24,
  "total_failures": 21,
  "by_source": [{ "source": "0x...", "failures": 5 }],
  "series": [{ "timestamp": "2026-05-29T09:00:00Z", "failures": 2 }]
}
```

Backend source and redaction rules:

- Source rows are existing `SoulAgentFailure` records already scoped by resolved agent IDs for the requested owner-owned instance.
- Only `failure_type == "signature_failure"` rows inside the same 24h window are counted.
- Series points are nonzero UTC-hour buckets derived from real failure timestamps.
- Series points expose only `{timestamp, failures}` — no agent IDs, failure IDs, descriptions, PK/SK, account IDs, or secrets.
- Existing `total_failures` and `by_source` semantics remain intact.
- `queue_depth.series[]` remains `{timestamp, depth}`.

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

The fixture covers: follower_count present, follower_count absent (`followers unavailable`), last_seen present, last_fetch fallback (no last_seen), and null/omitted states across 11 peer rows.

### Signature Series Coverage

| Instance | total_failures | by_source rows | series buckets | series sum |
|----------|----------------|----------------|----------------|------------|
| `equaltoai` | 14 | 4 | 7 | 14 |
| `maeve-studio` | 7 | 2 | 4 | 7 |
| **Fleet aggregate** | **21** | **6** | **11** | **21** |

The UI renders the signature Sparkline from `signatures.series[].failures` only. The per-source `by_source` rows remain a textual breakdown and are never used as chart points.

### Capture Command

Started the fixture server from `web/`:

```bash
npx vite --config fixtures/vite.fixture.m15.config.ts --host 127.0.0.1 --port 5207
```

Captured from the repository root:

```bash
node - <<'NODE'
const puppeteer = require('/tmp/node_modules/puppeteer');
(async () => {
  const browser = await puppeteer.launch({ headless: 'new', args: ['--no-sandbox', '--disable-setuid-sandbox'] });
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    await page.goto('http://127.0.0.1:5207/fixtures/m15-trust.html', { waitUntil: 'networkidle0' });
    await page.waitForSelector('.trust__header');
    await new Promise((resolve) => setTimeout(resolve, 3000));
    await page.screenshot({ path: 'gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png' });
  } finally {
    await browser.close();
  }
})();
NODE
```

### Screenshot Verification

```bash
$ file gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png
gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png: PNG image data, 1440 x 900, 8-bit/color RGB, non-interlaced

$ sha256sum gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png
e6b3326727a9a571357b067c444dc5a9d00298ba2e96f7e03b9f248f8db1eec0  gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png
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
| follower_count | Not rendered | Renders count when present; `followers unavailable` when null/omitted |
| last_seen | Not rendered | Renders `Seen: YYYY-MM-DD` when present |
| last_fetch | Not rendered | Renders `Fetch: YYYY-MM-DD` as fallback when last_seen absent |

### 2. Honest Empty States

| Panel | Before (#550) | After (#573) |
|-------|---------------|--------------|
| Peer constellation | `Federation peer telemetry is not yet instrumented` | `No scoped federation peer data is present` |
| Queue depth | `Inbound queue depth telemetry is not yet instrumented` | `No inbound queue depth data is present` |
| Header subtitle | `...Federation peer data is not yet instrumented...` | `...Data is scoped to your owned instances and reflects available telemetry.` |

### 3. Signature Failures — Real Time Series

The signature failures panel now shows:

- Dynamic label: `Sig failures 24h` derived from `signatures.window_hours`
- Aggregate count: `N total failures across M sources`
- Sparkline from `signatures.series[].failures`
- Per-source breakdown list from `signatures.by_source`

No chart is derived from per-source aggregate counts.

### 4. Queue Depth — DTO Preserved

The `queue_depth.series` field remains a real time-stamped depth series. The UI and tests read `queue_depth.series[].depth`; no `value` field was introduced.

### 5. Metric Card Labels

| Card | Label | Source |
|------|-------|--------|
| Reachable peers | `Reachable peers` | `federation.reachable` |
| Warnings | `Warnings` | `federation.warning` |
| Severed peers | `Severed peers` | `federation.severed` |
| Signature failures | `Sig failures {N}h` (dynamic) | `signatures.window_hours` (=24) |

### 6. Trust Score Gauge and Vouches

- Trust score gauge: average across instances (or per-instance when single customer). SVG with CSS-only styling (CSP-safe).
- Vouches: list of peer endorsements with dates. Strength is a fixed presence marker (1.0) per the backing model — no comparative strength bars fabricated.

### 7. Multi-Tenant Isolation Preserved

- Backend signature series is computed only after `requireInstanceAccess` resolves the owner-owned instance.
- Signature rows are queried per resolved bound agent ID for that instance; there is no global failure scan.
- UI fetches only owner-scoped portal endpoints (`/api/v1/portal/instances/{slug}/trust/data`).
- No cross-tenant queries, no operator-only trust-graph exposure.

## Validation Results

| Check | Result |
|-------|--------|
| `go test ./internal/controlplane ./internal/store/...` | PASS |
| `go vet ./internal/controlplane ./internal/store/...` | PASS |
| `cd web && npm run typecheck` | PASS — 0 errors, 0 warnings |
| `cd web && npm run lint` | PASS |
| `cd web && npm test` | PASS — 23 files, 237 tests |
| `cd web && npm run build` | PASS — build sidecars + no-inline HTML + OAC form integrity |
| `bash gov-infra/verifiers/gov-verify-rubric.sh` | PASS — 40 pass, 0 fail, 0 blocked |
| `git diff --check` | PASS |
| PNG dimensions via `file` | PASS — 1440×900 |

## Changed Files

| File | Change |
|------|--------|
| `internal/controlplane/handlers_portal_trust.go` | MODIFIED — additive `signatures.series` from real 24h `SoulAgentFailure` timestamps; queue-depth DTO preserved |
| `internal/controlplane/handlers_portal_trust_internal_test.go` | MODIFIED — backend coverage for signature series, redaction, older/non-signature exclusion, queue-depth `.depth` preservation |
| `web/src/lib/api/portalTrust.ts` | MODIFIED — additive `TrustSignatureSeriesPoint` + `signatures.series` type |
| `web/src/pages/portal/Trust.svelte` | MODIFIED — signature Sparkline from `signatures.series[].failures`, peer metadata, honest empty states |
| `web/src/pages/portal/Trust.svelte.test.ts` | MODIFIED — #573 tests for signature-series rendering, queue-depth `.depth`, peer metadata, route preservation |
| `web/fixtures/m15-trust.ts` | MODIFIED — fixture signature series values matching the additive DTO |
| `gov-infra/evidence/design-fidelity/m15-trust-ui/trust.md` | MODIFIED — evidence writeup for #573 corrections |
| `gov-infra/evidence/design-fidelity/m15-trust-ui/trust.png` | MODIFIED — regenerated 1440×900 screenshot |

## Deliberate Deviations / Caveats

- `follower_count` is rendered when the DTO supplies it and shown as unavailable when null/omitted. The current backend federation mapper does not derive follower counts from Lesser `active_users`; this avoids fabricating follower data.
- Signature `by_source.source` remains the existing bound soul agent ID breakdown from #572. The new time-series points do not expose agent IDs.
- The 1440×900 crop focuses on peer and signature evidence; the queue-depth panel sits below the first viewport after the real signature sparkline was added. Queue-depth behavior is covered by fixture data and tests.
