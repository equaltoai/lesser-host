# Soul Agent-First Client Contract

This document is the canonical backend contract for agent-first clients that drive the soul promotion workflow from
`lesser-host`.

It is written for downstream consumers such as the FaceTheory Simulacrum rewrite and shared UI packages such as
`greater-components`. The goal is to make the request, approval, review, signing, and graduation flow consumable
without reverse-engineering Go handlers.

For machine-readable schemas, also use:

- `docs/contracts/openapi.yaml`
- `web/src/lib/api/soul.ts`

## Scope

This contract covers the soul promotion lifecycle introduced for agent-first clients:

- request creation
- approval + mint operation preparation
- mint execution acknowledgement
- mint conversation review
- finalize preflight and signing inputs
- graduation publication
- durable workflow state
- durable lifecycle events

It does not restate the entire public soul registry surface. For the wider registry API, see `docs/soul-surface.md`.

## Authentication

All workflow endpoints in this document use the control-plane bearer session token:

- portal customer sessions
- operator/admin sessions

The same bearer token format is used for both. Access is still enforced server-side:

- operators can access any workflow
- customers can access only workflows they own through verified domain ownership

## Canonical Resources

These are the resources an agent-first client should treat as canonical:

- `SoulAgentPromotion`
  - durable, current workflow snapshot
  - read with `GET /api/v1/soul/agents/{agentId}/promotion`
  - list owned workflows with `GET /api/v1/soul/promotions/mine`
- `SoulAgentPromotionLifecycleEvent`
  - durable workflow event history
  - list owned events with `GET /api/v1/soul/promotions/mine/events`
  - list per-agent events with `GET /api/v1/soul/agents/{agentId}/promotion/events`
- `SoulAgentMintConversation`
  - review conversation record
  - streamed creation via SSE
  - explicit completion + finalize steps

Clients should use `SoulAgentPromotion` for current state and `SoulAgentPromotionLifecycleEvent` for notifications,
timelines, and “what changed” UI.

## Route Families

There are two equivalent route families during the review/finalize phase:

1. Registration-scoped routes, used before the client has pivoted to agent-first state:
   - `/api/v1/soul/agents/register/{id}/mint-conversation/...`
2. Agent-scoped routes, used once the client is keyed by `agentId`:
   - `/api/v1/soul/agents/{agentId}/mint-conversation/...`

The client should prefer the agent-scoped form once `agentId` is known and stored.

## End-to-End Sequence

### 1. Create the request

- `POST /api/v1/soul/agents/register/begin`

Input:

- `domain`
- `local_id`
- `wallet_address`
- optional `capabilities`

Response includes:

- `registration`
- `wallet` challenge to sign
- `proofs` for DNS / HTTPS ownership when auto-verification is not available
- `promotion` snapshot

Side effects:

- creates the pending `SoulAgentRegistration`
- creates the durable `SoulAgentPromotion`
- emits lifecycle event `request_created`

### 2. Verify and prepare approval

- `POST /api/v1/soul/agents/register/{id}/verify`
- equivalent agent-centric alias after resolution:
  - `POST /api/v1/soul/agents/{agentId}/promotion/verify`

Input:

- wallet signature over the registration challenge
- `principal_address`
- `principal_declaration`
- `principal_signature`
- `declared_at`

Response includes:

- verified `registration`
- `operation` for the mint
- `safe_tx` payload when Safe mode is enabled
- updated `promotion`

Side effects:

- records proof verification
- creates the mint `SoulOperation`
- moves the promotion into approved / awaiting mint
- emits lifecycle event `request_approved`

### 3. Record mint execution

- `POST /api/v1/soul/operations/{id}/record-execution`

This is usually performed by an operator or automation after the Safe transaction executes.

When the mint succeeds:

- the promotion moves into minted / ready for conversation
- lifecycle event `mint_executed` is emitted

### 4. Start or continue review

- registration-scoped:
  - `POST /api/v1/soul/agents/register/{id}/mint-conversation`
- agent-scoped:
  - `POST /api/v1/soul/agents/{agentId}/mint-conversation`

This endpoint streams the assistant response over SSE and updates the durable conversation record.

Important behavior:

- the first transition into in-progress review emits lifecycle event `review_started`
- additional turns in the same conversation do not emit duplicate `review_started` events

### 5. Complete the review draft

- registration-scoped:
  - `POST /api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/complete`
- agent-scoped:
  - `POST /api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/complete`

Response is the completed conversation record with extracted declarations stored on the backend.

Side effects:

- promotion moves into `ready_to_finalize`
- review digest, boundary count, and capability count are stored on the promotion
- lifecycle event `finalize_ready` is emitted

### 6. Fetch finalize preflight and signing inputs

- preferred:
  - `POST /api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize/preflight`
  - `POST /api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize/preflight`
- compatibility alias:
  - same routes under `/finalize/begin`

The preflight response is the canonical source for finalize UI and signing preparation. It includes:

- `registration_preview`
- `declarations_preview`
- `boundary_requirements`
- `self_attestation_signing`
- `finalize_request_template`
- `expected_version`
- `next_version`
- `digest_hex`
- `issued_at`

Clients should not reconstruct these values locally when the server already provides them.

### 7. Finalize and publish graduation

- registration-scoped:
  - `POST /api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/finalize`
- agent-scoped:
  - `POST /api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/finalize`

Input:

- signed self-attestation
- required boundary signatures
- any finalize payload fields described by the preflight response

Response includes:

- published `agent`
- `published_version`

Side effects:

- versioned registration JSON is published
- promotion moves into graduated state
- lifecycle event `graduated` is emitted

## Durable State Contract

`SoulAgentPromotion` is the current-state projection. Clients should expect these fields to drive UI:

- identity:
  - `agent_id`
  - `registration_id`
  - `requested_by`
  - `domain`
  - `local_id`
  - `wallet`
- state machine:
  - `stage`
  - `request_status`
  - `review_status`
  - `approval_status`
  - `readiness_status`
- review/mint metadata:
  - `mint_operation_id`
  - `mint_operation_status`
  - `principal_address`
  - `latest_conversation_id`
  - `latest_conversation_status`
  - `latest_review_sha256`
  - `latest_boundary_count`
  - `latest_capability_count`
  - `published_version`
- timestamps:
  - `requested_at`
  - `verified_at`
  - `approved_at`
  - `minted_at`
  - `review_started_at`
  - `review_ready_at`
  - `graduated_at`
  - `created_at`
  - `updated_at`
- computed UX helpers:
  - `prerequisites`
  - `next_actions`

Current state should be read from the promotion snapshot, not inferred by replaying events.

## Lifecycle Event Contract

`SoulAgentPromotionLifecycleEvent` is a durable, ordered event feed. Events are listed newest-first and paginated with
`cursor` / `next_cursor`.

Current event types:

- `request_created`
  - request record exists and the wallet/proof workflow can begin
- `request_approved`
  - verification completed and the mint operation is ready for execution
- `mint_executed`
  - the on-chain mint succeeded and review can begin
- `review_started`
  - the workflow entered a live mint-conversation review
- `finalize_ready`
  - declarations were extracted and finalize preflight can be shown
- `graduated`
  - publication completed and the agent has a new published version

Each event includes:

- `event_id`
- `event_type`
- `summary`
- `occurred_at`
- optional linkage fields:
  - `request_id`
  - `operation_id`
  - `conversation_id`
- `promotion`
  - a snapshot of the workflow state at that transition

Recommended client behavior:

- use `/api/v1/soul/promotions/mine/events` for notification trays, inbox-style timelines, and LLM polling loops
- use `/api/v1/soul/agents/{agentId}/promotion/events` for an agent detail timeline
- use `promotion.next_actions` from the embedded snapshot instead of hardcoding UI branching

## Failure and Guard Conditions

These are the most important contract-level failure cases for clients to handle explicitly:

- registration already published
  - review/finalize routes will reject workflows that already graduated
- conversation is not in progress
  - completion requires an active conversation
- promotion not found
  - agent-scoped promotion routes require a known durable workflow
- unauthorized / forbidden
  - ownership and operator rules still apply even with a valid bearer token
- invalid signature inputs
  - wallet verification, principal declaration verification, self-attestation signing, and boundary signatures all fail
    closed
- expected version mismatch
  - finalize/update publication can reject stale clients that try to publish against an outdated version chain

Clients should surface these as explicit workflow states instead of retrying blindly.

## Client Integration Guidance

- Prefer agent-scoped routes once `agentId` is known.
- Treat preflight output as canonical for finalize signing UX.
- Treat lifecycle events as the notification surface and promotion snapshots as the source of truth for current state.
- Do not infer approval, finalize readiness, or graduation solely from mint conversation status.
- When reconnecting, refresh the current promotion snapshot first, then replay lifecycle events for timeline context if
  needed.
