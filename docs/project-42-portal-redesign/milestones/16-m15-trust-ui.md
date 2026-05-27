# M15 — Trust UI

**Branch.** `aron/portal-m15-trust-ui`
**Concern.** Replace the legacy attestation-explorer page at
`/portal/trust` with the design's federation-health dashboard.
The attestation API endpoints (`/.well-known/*`, `/attestations/*`)
are **untouched** — the trust API surface stays intact; this PR
changes what the customer portal renders on the `/portal/trust`
route.

## Scope (≤ 7 tasks)

1. Page header with title "113 peers, 1 severed." pattern, eyebrow "Trust & federation".
2. Four metric cards (Reachable peers, Warnings, Severed last 30d, Sig failures 24h).
3. Peer constellation grid (one square per peer; status dot; follower count; last fetch).
4. Sparkline panel — HTTP signature failures (last 24h).
5. Sparkline panel — Inbound queue depth.
6. Right rail — Trust score gauge.
7. Right rail — Vouches from peers + Severed alert.

## Out of scope

- Trust API attestation endpoints (untouched).
- Data wiring — M16.

## Acceptance criteria

- Per-instance scoping: customers see only their own instances'
  federation data.
- Multi-tenant isolation preserved.
- Side-by-side artifact + tests.

Detail filled in when M14 merges.
