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
  - Returns terminal `200` success as the compact HostConversation `declaration_ready` envelope only when valid produced
    declarations exist; it does not expose raw transcripts.
- Finalize/preflight/begin/finalize fail closed unless the active conversation has valid produced declarations.

SSE can remain for non-instance Host UI routes, but it is no longer the authoritative Lesser completion mechanism.
CloudFront may still route the mint-conversation subtree through Host's REST control-plane mint-conversation origin for
transport compatibility; the Lesser instance-key contract is JSON state, not SSE state.


## Project 51 M2 runtime binding

Project 51 M2 dogfoods the `HostedGenesisSession` model/repository foundation before AppTheory MicroVM execution is
introduced. The Lesser instance-key hosted-genesis route family now treats the Host session row as user-visible truth:

- POST creates or updates `HostedGenesisSession`, and the debit/idempotency transaction is the exactly-once authority.
  Project 51 M4 removes hosted-genesis SQS from this user-visible path; queue/DLQ/AI-worker state is not success,
  recovery, or finalize authority.
- GET/status and instance read endpoints project compact HostConversation state from `HostedGenesisSession`; stale or
  missing queue delivery cannot invent status.
- Complete/declaration/finalize reads publish readiness from `HostedGenesisSession.status=declaration_ready` plus a valid
  declaration checkpoint. The PR does not mutate cloud or on-chain state.
- `SoulAgentMintConversation` remains only compatibility/projection/migration input. Host does not delete compatibility
  rows without a separate migration/runbook and does not import raw transcripts into the session source of truth.

The M0 AppTheory MicroVM decision is preserved: no raw-AWS MicroVM substitute or local framework fork is introduced in
this milestone.

### Worker and retry model (M4 demoted)

Hosted-genesis SQS may remain available for operator/backfill/janitor recovery, but the control plane no longer receives
`HOSTED_GENESIS_QUEUE_URL` and does not enqueue user-visible conversation turns. If an operator/backfill path feeds the
queue, the AI worker consumes one hosted-genesis job at a time and re-loads all durable state before writing:

1. registration id and agent id
2. registration domain
3. managed instance ownership/boundary
4. conversation id/status/latest turn
5. idempotency row when present

The queue has an SQS-managed encrypted DLQ, three receive attempts, and a one-message batch size. Queue loss, DLQ
backlog, or AI-worker outage must not block status reads, recovery guidance, or finalize gate decisions once
`HostedGenesisSession` truth exists. Retry with the same `idempotency_key` and request hash replays the existing
conversation/turn and does not append another user message or debit credits again.

### `created` status decision

Host keeps `created` in the locked enum for internal/future pre-created durable records. For the Lesser instance-key
projection, Host collapses `created` to `in_progress`. Lesser M3 should therefore project any observed `created`
snapshot as `in_progress` and persist `host_conversation_id` immediately.

### Boundaries

M2 is local code, contract, and synthesis work only. Lab canary deploy, live deploy, and any cloud mutation are
operator/steward follow-ups after review and merge.
