# Project 42 M16 corrective (#572): Trust federation and queue telemetry

Date: 2026-05-29
Branch: `aron/issue-572-trust-federation-queue-telemetry`

## Scope

Corrects M16's placeholder trust telemetry for:

- `GET /api/v1/portal/instances/{slug}/trust/data` federation counters and peer rows
- signature-failure dashboard window
- queue-depth time series

This change does **not** modify public trust/attestation routes (`/.well-known/*`, `/attestations/*`) and does not do M15/#573 Trust UI rendering work.

## Data sources and windows

### Federation

Source: the requested owner-owned managed Lesser instance, fetched server-side with the instance key resolved through the existing managed-instance secret path (`LesserHostInstanceKeySecretARN` / Bearer instance key pattern used by managed metrics/activity/roster bridges).

Endpoints used:

- `GET /api/v1/admin/federation/statistics` — availability/time-range probe of the managed admin federation surface
- `GET /api/v1/admin/federation/instances?limit=100[&cursor=...]` — peer rows used for counters and bounded DTO rows

Counter semantics:

- `reachable`: peer is not suspended, not silenced, not stale, and not low-trust degraded
- `warning`: peer is silenced, stale (`last_seen` older than 30 days), or has a low non-zero Lesser `trust_score` (`0 < trust_score < 50`)
- `severed`: peer is suspended (`is_suspended=true`)

Bounds:

- Host fetches at most 500 peer rows per request from the managed instance.
- Browser DTO returns at most 50 `federation.peers` rows.
- If Lesser pagination remains after the fetch cap, `federation.truncated=true`; counters then honestly represent fetched rows only.

Peer DTO redaction:

- Returned: `domain`, `status`, and `last_seen` when Lesser provides it; otherwise host returns an honest `last_fetch` timestamp.
- `follower_count` is nullable/omitted because Lesser's federation admin response does not provide a real follower count. `active_users` is **not** mapped to follower count.
- Not returned: hosted AWS account ID, Route53 zone ID, PK/SK/GSI/TTL keys, raw instance keys, raw secrets, admin bearer tokens, message/content data, PAN/CVV, or cross-tenant identifiers.

### Signature failures

Source: `SoulAgentFailure` rows scoped by bound soul agent IDs for the requested instance's verified/managed domains.

Window: `24h` (`signatures.window_hours=24`). Rows older than 24 hours are excluded. The by-source DTO remains bounded to 50 source rows and uses bound soul agent IDs as the source labels, not remote federation peers.

Isolation proof:

1. The handler calls `requireInstanceAccess(slug)` before any signature query.
2. Domain resolution uses only the requested instance's verified domains plus its managed stage domain.
3. Failure queries are per-agent partition (`SOUL#AGENT#{agentId}`) for agents resolved from those domains.
4. Tests cover forbidden/not-owned access and 24h exclusion.

### Queue depth

Source: host-side, instance-scoped soul communication mailbox state.

- Current snapshots count `SoulCommMailboxMessage` rows with `direction=inbound`, `status=queued`, and `deleted=false`.
- Each count query is scoped to a single partition: `COMM#MAILBOX#INSTANCE#{instanceSlug}#AGENT#{agentId}`.
- No global SQS queue, global DynamoDB scan, or cross-tenant queue partition is queried.
- Snapshots persist as `TrustQueueDepthSample` rows under `TRUST#QUEUE_DEPTH#INSTANCE#{instanceSlug}`.

Window/bounds:

- `queue_depth.window_hours=24`
- DTO returns at most 48 points from the 24h window.
- `TrustQueueDepthSample` rows retain for 31 days; the dashboard reads only the 24h window.

DTO redaction:

- Returned: timestamp + aggregate depth + source/window metadata.
- Not returned: agent IDs, delivery IDs, message IDs, content pointers, content hashes, mailbox subjects/previews/body, PK/SK/GSI/TTL keys, account IDs, raw secrets/keys, PAN/CVV, or other tenants' slugs.

## Unavoidable nullable/empty fields

- `federation.peers[].follower_count` is omitted/null until a real follower-count source exists. This is intentional; Lesser's `active_users` is a different metric and is not used as a substitute.
- External/non-managed instances, old managed Lesser releases without the admin federation endpoints, or soul-disabled instances may still return empty federation/queue sections. That is a scoped no-data state, not a placeholder implementation path for managed instances with telemetry.

## Tests added/updated

- Managed Lesser federation happy path with nonzero counters and bounded redacted peer rows.
- Silenced/suspended/degraded/stale status mapping.
- Peer row cap of 50 with counters from real fetched peer rows.
- Forbidden/not-owned access proves upstream federation is not called.
- Signature-failure window changed to 24h and excludes 25h/older rows.
- Queue-depth current snapshot counts only instance+agent scoped mailbox partitions.
- Queue-depth series bounded to 48 points and filters cross-tenant sample rows.
- Store model tests for `TrustQueueDepthSample` keys/TTL/redaction shape.

## Trust-and-safety audit summary

- Public trust API contract: unchanged (`cmd/trust-api`, `internal/trust`, `/.well-known/*`, `/attestations/*` untouched).
- Attestation shape/signing: unchanged.
- Instance-auth for trust API: unchanged (`sha256(raw_key)` matching untouched).
- Portal managed-instance access: uses existing server-side instance-key secret resolution; raw key is used only in the outbound Bearer header to the owner-owned managed instance and is never returned or logged.
- CSP/web rendering: no rendering changes; API TypeScript types were only made additive for new backend DTO fields.
- Governance/rubric: evidence added here; no verifier or pack weakening.
