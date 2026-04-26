# Soul comm mailbox migration path

This document records the cross-repo migration from legacy soul comm delivery surfaces to the bounded, host-authoritative
mailbox contract approved in ADR 0005 and implemented by `lesser-host`.

The intent is to keep authority in one place:

- **host** owns canonical comm objects: `deliveryId`, `messageId`, `threadId`, provider status, delivery metadata,
  bounded content, and read/unread/archive/delete state.
- **lesser** receives notification projections only. It may render summaries or ActivityPub/UX notifications, but it is
  not the mailbox source of truth.
- **body** remains the MCP facade. It gates scope/profile, calls host's instance-authenticated mailbox APIs, and returns
  MCP-shaped tool results without storing mailbox truth locally.

## Migration phases

1. **Host canonical capture is present**
   - inbound email/SMS/voice capture writes canonical `SoulCommMailboxMessage` rows and bounded content objects before
     projection work is queued to lesser.
   - outbound `POST /api/v1/soul/comm/send` writes canonical mailbox rows in addition to the legacy delivery status
     record.
   - legacy activity and queue rows may still be written for compatibility, but new reads should not depend on them.

2. **Host mailbox APIs become the body contract**
   - `GET /api/v1/soul/comm/contactability/{agentId}` resolves exact-agent receive/send affordances.
   - `GET /api/v1/soul/comm/mailbox/{agentId}/messages` lists redacted canonical messages.
   - `GET /api/v1/soul/comm/mailbox/{agentId}/messages/{deliveryId}` returns canonical metadata for one delivery.
   - `GET /api/v1/soul/comm/mailbox/{agentId}/messages/{deliveryId}/content` returns full content only by explicit
     delivery fetch.
   - read state APIs mutate canonical state:
     - `POST .../{deliveryId}/read`
     - `POST .../{deliveryId}/unread`
     - `POST .../{deliveryId}/archive`
     - `POST .../{deliveryId}/unarchive`
     - `POST .../{deliveryId}/delete`

3. **Portal reads canonical mailbox state**
   - portal activity and inbound queue views read host mailbox rows rather than legacy activity/queue tables.
   - list responses contain preview/content metadata and mailbox state only; full bodies are not returned by portal list
     views.

4. **Body implements MCP tools over host**
   - `email_get` / `email_read` list or fetch metadata from host list/get endpoints.
   - `email_get_content` calls host's explicit content endpoint.
   - `email_mark_read`, `email_mark_unread`, archive, and delete tools call the canonical state endpoints.
   - body must not copy full message bodies or read/archive/delete state into a durable body-owned mailbox store.

5. **Lesser keeps projections non-authoritative**
   - lesser may receive notifications for UX/activity and may keep local notification summaries.
   - lesser must treat `deliveryId`/`threadId` and read state as host-owned. If a lesser UX needs full content or state,
     it should call through the host/body contract instead of becoming the mailbox authority.

## Backward compatibility

- `POST /api/v1/soul/comm/send` and `GET /api/v1/soul/comm/status/{messageId}` remain available for existing outbound
  send/status callers.
- Legacy `SoulAgentCommActivity` and `SoulAgentCommQueue` records may continue to be written during the migration window,
  but they are no longer authoritative for portal/body mailbox behavior.
- Portal queue responses no longer include a `body` field. They expose redacted `preview`, `content.available`, content
  byte/hash metadata, and mailbox state. Full content requires the explicit content endpoint.
- Body should tolerate instances that have not yet generated canonical mailbox rows by returning an empty mailbox result
  or a clear capability-not-ready error rather than reconstructing mailbox state from legacy rows.

## Auth and tenant isolation

- Mailbox body-facing endpoints use **strict instance API key auth**: bearer raw key -> `sha256(raw_key)` -> stored
  `InstanceKey` hash match. Host never stores or logs raw instance keys.
- The authenticated instance slug must match the agent's verified domain/instance relationship before mailbox rows are
  listed, fetched, or mutated.
- Portal views continue to use operator/customer sessions and existing domain/instance ownership checks. Portal reads are
  instance+agent-scoped and do not allow cross-tenant mailbox scans.
- Contactability is exact-agent only. There is no global search/list affordance for mailbox contactability.

## Rate limits and bounded content

- Body-facing mailbox list/get/content/state endpoints are rate-limited with the control-plane comm API limits.
- Lists are bounded and paginated where applicable; list responses include metadata/previews only.
- Full content is stored for the retention window defined by `SoulCommMailboxRetentionDays` and the mailbox content bucket
  lifecycle. Host must not turn mailbox content into permanent semantic memory.
- Content objects are encrypted at rest and addressed only through host-owned content pointers. Storage bucket/key values
  are never returned in list responses.

## Audit and evidence

- Explicit content reads write audit evidence with action `soul_comm_mailbox.content_read`.
- Read/unread/archive/delete operations append immutable mailbox state-change events alongside the current-row mutation.
- Delivery/provider facts remain in canonical mailbox metadata so operators can reconcile provider status without reading
  tenant message bodies.
- Governance evidence for this exception lives in `gov-infra` via the CMP-4 bounded mailbox verifier and ADR 0005.

## Projection semantics for lesser

Lesser projections are intentionally lossy summaries:

- projection identity: `deliveryId`, `messageId`, `threadId`, `agentId`, channel type, direction, and timestamp
- projection content: subject and preview only, not full body
- projection state: optional notification state for UX only; canonical read/archive/delete state stays in host
- projection retry: duplicate projections should be idempotent by `deliveryId`

If lesser needs to display authoritative state or full content, the request path should be: client/body scope check -> host
mailbox API -> response. Lesser should not synthesize or persist mailbox truth.

## Body MCP implementation checklist

Before body enables host-dependent mailbox tools against a stage:

- host has deployed the Host 2+3+4 mailbox changes to that stage
- body has an instance API key for the managed instance and never logs the raw token
- body calls host list/get endpoints for metadata and host content endpoint only for explicit content reads
- body maps host rate-limit and auth failures to MCP errors without retry storms
- body writes no durable mailbox-content/read-state store of its own
- lesser consumes projections as notifications, not source-of-truth mailbox records
