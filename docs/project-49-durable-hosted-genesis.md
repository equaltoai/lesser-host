# Project 49 — Durable async hosted soul genesis

## M2 host implementation

M2 moves the Lesser instance-key hosted-genesis mint conversation path from transport-coupled SSE completion to a
durable JSON status contract owned by Host.

### Lesser instance-key path

- `POST /api/v1/soul/instance/agents/register/{id}/mint-conversation`
  - Accepts a user turn, persists the Host `conversation_id` before returning, debits exactly once for a matching
    `idempotency_key`, and returns `202` JSON with `conversation.status=in_progress`.
  - Echoes client-safe `idempotency_key`, `correlation_id`, and `lesser_request_id` values through `trace_ids` when
    present.
  - Does not return raw Host browser tokens, raw Instance API keys, or raw LLM transcripts.
- `GET /api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}`
  - Returns the compact durable HostConversation projection for polling/resume.
- `POST /api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/complete`
  - Returns `202` progress while the assistant turn or declaration extraction is still running.
  - Returns terminal success only when valid produced declarations exist.
- Finalize/preflight/begin/finalize fail closed unless the active conversation has valid produced declarations.

SSE can remain for non-instance Host UI routes, but it is no longer the authoritative Lesser completion mechanism.

### Worker and retry model

The control plane enqueues hosted-genesis work on `HOSTED_GENESIS_QUEUE_URL`. The AI worker consumes one hosted-genesis
job at a time and re-loads all durable state before writing:

1. registration id and agent id
2. registration domain
3. managed instance ownership/boundary
4. conversation id/status/latest turn
5. idempotency row when present

The queue has an SQS-managed encrypted DLQ, three receive attempts, and a one-message batch size. Worker failures update
the conversation to `failed` with a bounded recovery action; DLQ visibility is alarmed. Retry with the same
`idempotency_key` and request hash replays the existing conversation/turn and does not append another user message or
debit credits again.

### `created` status decision

Host keeps `created` in the locked enum for internal/future pre-created durable records. For the Lesser instance-key
projection, Host collapses `created` to `in_progress`. Lesser M3 should therefore project any observed `created`
snapshot as `in_progress` and persist `host_conversation_id` immediately.

### Boundaries

M2 is local code, contract, and synthesis work only. Lab canary deploy, live deploy, and any cloud mutation are
operator/steward follow-ups after review and merge.
