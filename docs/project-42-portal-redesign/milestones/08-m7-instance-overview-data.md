# M7 — Instance Detail Overview data

**Branch.** `aron/portal-m7-instance-overview-data`
**Concern.** Add the data fields the Overview right-rail and Stack
card need: owners list (handles + roles + avatars), anchor freshness
relative timestamp, and any additional Stack-card fields not yet
exposed. Backend-only.

## Scope (≤ 5 tasks)

1. Owners endpoint or DTO extension (handle, role, avatar hash).
2. Anchor-freshness field on instance state.
3. Stack-card field audit: confirm Lesser version, Body version,
   MCP-wiring state, MCP drift flag are all exposed.
4. Handler tests + redaction proof.
5. SEC-* verifier addition only if a new sensitive field is
   added (probably not).

## Out of scope

- UI consumption — M6.

Detail filled in when M6 outline solidifies.
