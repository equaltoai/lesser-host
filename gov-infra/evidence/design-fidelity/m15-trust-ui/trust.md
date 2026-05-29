# M15 Trust UI — Design Fidelity Evidence

- **Issue:** equaltoai/lesser-host#550
- **Spec:** Project 42, Milestone 15 — Portal Design Fidelity Recovery, Trust UI
- **Commit branch:** `aron/portal-m15-trust-ui`
- **Date:** 2026-05-29

## Screenshot

**trust.png**: NOT YET CAPTURED. Browser screenshot tooling was not available
in the agent execution environment. The PR is being opened with this caveat
documented. A 1440×900 screenshot of the `/portal/trust` federation dashboard
must be captured and committed before merging to main.

Expected visual layout:
- Page header with eyebrow "Trust & federation" and title "X peers, Y severed."
- Four StatCard metric cards (Reachable peers, Warnings, Severed last 30d, Sig failures 24h)
- Two-column grid: left panel with peer constellation grid, signature failures sparkline + source list, queue depth sparkline; right rail with circular trust score gauge + dimension progress bars, vouches list, severed alert
- Responsive: collapses to single column below 864px viewport width

## Route Compatibility Verification

| Route | Handler | Status |
|-------|---------|--------|
| `/portal/trust` | PortalTrust dashboard (M15) | ✓ Renders federation dashboard |
| `/portal/trust/attestations/{id}` | Trust.svelte (public attestation inspector) | ✓ Preserved via App.svelte routing |
| `/trust/attestations/{id}` | Trust.svelte (public attestation inspector) | ✓ Unchanged |
| `/.well-known/*` | trust-api Lambda (backend) | ✓ Unchanged (SPA never handles) |
| `/attestations/*` | trust-api Lambda (backend) | ✓ Unchanged (SPA never handles) |

## Deliberate Visual Deviations

### M16 Fields Not Yet Instrumented (Honest Empty States)

1. **Federation peer data**: The M16 endpoint returns `federation.reachable=0`, `federation.warning=0`, `federation.severed=0`, and `federation.peers=[]`. The UI renders an honest "not yet instrumented" callout in the peer constellation panel.

2. **Follower count / last-fetch**: The M16 `portalTrustFederationPeerRow` carries only `domain` and `status`. The peer grid does NOT fabricate follower counts or last-fetch timestamps.

3. **Queue depth time series**: The M16 endpoint returns `queue_depth.series=[]`. The UI renders an honest "not yet instrumented" callout.

### Vouches Rendered as List/Count (Per M16 Contract)

- `vouches.items[].strength` is a fixed presence marker (`1.0`). The M15 UI renders vouches as a list of peer names with dates, NOT as comparative strength bars. This is documented in both the M16 data contract and the M15 design spec.

### Signature Failures Sparkline

- The M16 endpoint returns per-source aggregate counts (`by_source`), not time-bucketed series. The sparkline visualizes the per-source failure counts as a ranked series rather than a time series. The source list below the sparkline provides the agent ID and count for each data point.

### Trust Score Gauge

- When multiple instances exist, the gauge shows the average composite score across all instances. Single-instance customers see the per-instance score. This is documented in the UI helper text.
- The gauge uses SVG with CSS class-based status coloring (ok/warning/danger) — strictly CSP compliant (no inline styles).

## Validation Results

| Check | Result |
|-------|--------|
| `npm run typecheck` | PASS (0 errors, 0 warnings) |
| `npm run lint` | PASS (0 errors) |
| `npm test` | PASS (23 files, 222 tests) |
| `npm run build` | PASS |
| SEC-5 (CSP byte-string integrity) | PASS |
| SEC-6 (HTML inline absence) | PASS |
| gov-verify-rubric.sh (full) | NOT RUN (script exceeds agent timeout; targeted SEC-5/SEC-6 passed) |

## Changed Files

| File | Change |
|------|--------|
| `web/src/lib/api/portalTrust.ts` | NEW — TypeScript API client with types for M16 trust data |
| `web/src/pages/portal/Trust.svelte` | NEW — M15 federation trust dashboard page |
| `web/src/pages/portal/Trust.svelte.test.ts` | NEW — 21 tests (CSP, route, DOM, data contract) |
| `web/src/App.svelte` | MODIFIED — split trust routing: exact `/portal/trust` → PortalTrust, `/portal/trust/attestations/*` → attestation inspector |
| `web/src/pages/Portal.svelte` | MODIFIED — added `portalTrust` route kind for robustness |

## No Backend/Public API Changes

This is a UI-only milestone. No Go handlers, trust API routes, control-plane trust data structs, or backend tests were modified.
