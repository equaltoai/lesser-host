# M16 — Trust data

**Branch.** `aron/portal-m16-trust-data`
**Concern.** Add the data the federation-health dashboard needs:
peer roll-up (reachable / warning / severed counters), sig-fail
counters, inbound queue depth, trust score, vouches.

## Scope (≤ 6 tasks)

1. Federation peer roll-up endpoint (per-instance).
2. Sig-fail counters (24h, broken out by source instance).
3. Inbound-queue depth time series.
4. Trust score endpoint (computed; not on chain).
5. Vouches endpoint (peer → strength pairs).
6. Handler tests + redaction proof + tenant-isolation tests.

## Out of scope

- Trust API (`.well-known/*`, `/attestations/*`) — untouched.
- On-chain anchor coordination — out of bundle scope.

Detail filled in when M15 merges (or precedes M15 if arch
sequences data-first).
