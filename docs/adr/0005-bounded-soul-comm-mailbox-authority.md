# ADR 0005 — Bounded soul comm mailbox authority

**Date:** 2026-04-25  
**Status:** Accepted

## Context

`lesser-host` already owns communication delivery facts for soul comms: provider message IDs, delivery IDs, thread
correlation, idempotency, inbound routing, outbound provider status, and delivery audit. `lesser-body` requested MCP
mailbox tools (`email_read`, future `email_get`, `email_get_content`, state mutation tools) that need a canonical mailbox
source of truth.

If mailbox content and read state live in `lesser` or `lesser-body` while delivery truth remains in host, the ecosystem
gets a split-brain mailbox:

- host knows delivery/provider/thread/idempotency facts;
- lesser holds notification approximations;
- body is pressured to glue state together or store mailbox truth locally.

That pressure is wrong for body. Body should remain the MCP facade: apply profile/scope gating, call host, and return
MCP-shaped results.

## Decision

`lesser-host` will become the authoritative store for **bounded** soul comm mailbox objects.

Host owns canonical comm objects:

- `deliveryId`
- `messageId`
- `threadId`
- provider status and provider identity metadata
- inbound/outbound metadata
- full bounded content
- content hash / encrypted content pointer
- read/unread/archive/delete or tombstone state
- state-transition and content-access audit events

`lesser` receives notification projections for UX/activity only. Those projections are not authoritative mailbox state.

`lesser-body` remains the MCP interface. It exposes tools over host's contract and must not store mailbox truth locally.

## Required boundaries

### Bounded content, not semantic memory

Host persists message content only as a bounded mailbox delivery artifact. Host does not become permanent semantic memory,
agent memory, or a cross-tenant search system.

Mailbox content storage must have:

- an explicit retention policy;
- encryption at rest;
- lifecycle expiry or purge semantics;
- an access-audit trail for content reads and state mutations;
- no global cross-tenant search or analytics;
- no unbounded body exposure in list endpoints.

### List/content split

Mailbox list endpoints return redacted previews and metadata only. Full body/content is available only through an explicit
get-content/read endpoint, and those calls are audit-worthy.

### Hash-only instance authentication

Mailbox instance APIs must require strict instance authentication: `Authorization: Bearer <raw_key>` is hashed with
`sha256(raw_key)` and matched against the stored instance-key hash. Raw instance keys are never stored, logged, returned
on re-read, or accepted through plaintext fallback paths.

### Immutable audit and protected identity

Mailbox current-state rows may mutate read/archive/delete status, but identity, provenance, and content identity fields
must be protected after creation. Audit/event rows are write-once. State transitions should update the current row and
append an immutable event in the same transaction where practical.

## Non-goals

- Body does not deliver email/SMS locally.
- Body does not persist mailbox truth.
- Lesser does not become canonical mailbox storage for host-delivered soul comms.
- Host does not add tenant-content analytics or cross-tenant mailbox search.
- Host does not loosen CSP, trust API auth, or instance-key handling.
- No on-chain contract changes are required by this mailbox authority decision.

## Consequences

- Host's normal “metadata, not tenant content” posture has an explicit, bounded exception for soul comm mailbox content.
- Governance docs and verifiers must make the exception visible before implementation stores message bodies.
- Storage and API work must preserve tenant isolation: mailbox objects are scoped by instance/agent identity and cannot be
  listed or fetched across instances.
- Future body MCP tools consume host's mailbox contract; body-side storage remains a cache/projection at most, never the
  source of truth.
