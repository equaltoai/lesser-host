# M5 — Fleet data

**Branch.** `aron/portal-m5-fleet-data`
**Concern.** Add the fields the Fleet card needs that aren't on the
wire today: active users (rolling 30d), posts in last 24h, signature
failures (24h), 7-day activity sparkline, 7-day cost sparkline, and
federation-peer counts (peers + severed). Backend-only; no UI.

## Scope (≤ 6 tasks)

1. Extend `portalListInstances` DTO with the missing fields,
   redacted (no account_id, no PK/SK).
2. Compute active-user counts in the existing portal usage service
   or add a thin handler if needed.
3. Compute posts-24h via the existing activity counters; expose in
   the DTO.
4. Compute sig-fail counters; verify tenant-isolation (sig fails
   are per-instance, not cross-tenant).
5. Compute spark series (activity + cost) — 7 bucketed daily
   values per instance.
6. Unit + handler tests; redaction proof in `_test.go` similar to
   the M3.11 pattern.

## Out of scope

- New UI consumption — M4 already renders skeletons; this PR
  populates them.
- Federation health detail — M16.

## Acceptance criteria

- Handler tests cover happy / forbidden / not-found / empty.
- Redaction proof: marshalled DTO does not contain account_id,
  PK, SK, ttl, or EntriesJSON fields.
- Existing `portalListInstances` consumers unaffected (additive).
- Web side smoke: M4-rendered Fleet now shows real numbers.
- Gov rubric green; no SEC verifier additions unless redaction
  surface changes.

Detail filled in when M4 merges (or precedes M4 if arch sequences
data-first).
