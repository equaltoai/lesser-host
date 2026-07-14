# Soul Agent-First Client Contract

This document is the canonical backend contract for agent-first clients that drive the soul promotion workflow from
`lesser-host`.

It is written for downstream consumers such as the FaceTheory Simulacrum rewrite and shared UI packages such as
`greater-components`. The goal is to make the request, approval, review, signing, and graduation flow consumable
without reverse-engineering Go handlers.

For machine-readable schemas, also use:

- `docs/contracts/openapi.yaml`
- `docs/contracts/soul-mint-conversation-sse.json`
- `docs/contracts/hosted-genesis-conversation.md`
- `docs/spec/v3/schemas/hosted-genesis.conversation.response.schema.json`
- `web/src/lib/api/soul.ts`

## Scope

This contract covers the soul promotion lifecycle introduced for agent-first clients:

- request creation
- approval + optional mint operation preparation
- optional mint execution acknowledgement / on-chain promotion
- mint conversation review
- finalize preflight and signing inputs
- graduation publication
- durable workflow state
- durable lifecycle events

It does not restate the entire public soul registry surface. For the wider registry API, see `docs/soul-surface.md`.

## Project 44 remediation status

The route families documented below are the current `lesser-host` control-plane/portal contract. They remain useful for
operator review, customer portal fallback, and controlled administrative remediation, but they are not the production
Simulacrum bootstrap authority.

The prior browser-held `lesser-host` control-plane bearer-token bridge is superseded. Production Simulacrum soul
creation must not require the Simulacrum browser to hold or replay a `lesser-host` portal/operator session token. The
required Project 44 authority boundary is:

```text
Simulacrum browser -> Lesser same-origin auth/API -> Lesser server-side instance trust -> lesser-host scoped instance-key write API
```

The replacement Host route family is tracked by
[Project 44 M1 issue #706](https://github.com/equaltoai/lesser-host/issues/706). That follow-on work must add
instance-key authenticated write routes for registration begin, principal declaration preflight, verification, mint
conversation, and hosted/off-chain finalization before Project 44 can treat the `/l/*` Simulacrum soul creation flow as
production-ready. This M0.2 note is documentation-only and does not implement those routes.

## Published Identity Payload

When an agent-first client resolves a published soul profile through `GET /api/v1/soul/agents/{agentId}`, it should
treat the response as machine-readable identity state rather than scraping legacy portal UI behavior.

Important fields include:

- core identity: `agent_id`, `domain`, `local_id`, `ens_name`, `wallet`, `token_id`, `meta_uri`
- declaration metadata: `principal_address`, `principal_signature`, `principal_declaration`, `principal_declared_at`
- lifecycle metadata: `status`, `lifecycle_status`, `lifecycle_reason`, `successor_agent_id`, `predecessor_agent_id`
- publication metadata: `self_description_version`, `mint_tx_hash`, `minted_at`, `updated_at`
- anchor assurance metadata under `anchor_assurance`
  - `state`: `hosted_offchain` or `immutable_onchain`
  - `source`: `host_record` or `onchain_receipt`
  - `capability_gate`: always `false`; assurance is a trust/display signal, not default capability authority
  - `mutable` / `revocable`: display hints for the assurance tier, not permission decisions
  - `evidence[]`: host-registry or mint-transaction evidence (`tx_hash`, `operation_id`, `chain_id`, timestamps when known)
- on-chain avatar metadata under `avatar`
  - `current_style_id`
  - `current_style_name`
  - `current_renderer_address`
  - `image`
  - `styles[]` containing the currently configured avatar variants for client display and selection UI

Clients should prefer these structured fields directly. Fetching `meta_uri` for token metadata should be treated as a
fallback for older records, not the primary integration path.

Anchor assurance is deliberately separate from capability and caller-access policy. Hosted/off-chain souls may still use
their allowed capabilities, x402 caller grants, and communication channels when their policy permits it. Promotion to
`immutable_onchain` adds trust evidence; it must not create a second agent namespace, rotate `agent_id`, or silently
change capability/access policy.

Hosted/off-chain is a first-class production anchor state. A zero-state client may verify a registration, complete the
mint conversation, and finalize publication before any on-chain mint receipt is recorded. That publishes the same
agent namespace with `anchor_state=hosted_offchain`, no `mint_tx_hash` / `minted_at`, and
`self_description_version` plus version history populated. Recording the prepared mint operation later promotes the
same identity to `immutable_onchain`; clients must treat that as an anchor upgrade, not a replacement registration.

Managed ENS material for host-provisioned identities is always instance-scoped:

```text
<local>.<instance-slug>.lessersoul.eth
```

Public profile, search, and ENS gateway resolution should use this canonical name where the domain-to-managed-instance
mapping exists. Legacy bare names such as `<local>.lessersoul.eth` are migration-only and must not be used for new
agent-first routing.

Public clients that expose hosted-bound-soul or x402 flows must also follow the launch-gate disclosures in
`docs/hosted-bound-soul-launch-gates.md`: hosted/off-chain wording must not be conflated with immutable/on-chain
assurance, x402 payment/refund/failure boundaries must be surfaced before paid access, and email/phone/SMS/voice
capabilities require privacy and consent copy before public launch.

## Authentication and route authority

### Current portal/control-plane human route family

All workflow endpoints currently enumerated in this document use the control-plane bearer session token:

- portal customer sessions
- operator/admin sessions

The same bearer token format is used for both. Access is still enforced server-side:

- operators can access any workflow
- customers can access only workflows they own through verified domain ownership

These session-authenticated routes are human control-plane routes. They must not be reinterpreted as a browser-facing
Simulacrum production path by minting, proxying, or storing `lesser-host` control-plane bearer tokens in the Simulacrum
browser.

### Required managed-instance machine route family

Project 44's production Simulacrum path requires a separate managed-instance machine route family, tracked by
[issue #706](https://github.com/equaltoai/lesser-host/issues/706). Those routes must be called by Lesser server-side
with `instanceKeyAuth`, where the presented raw bearer key is hashed and matched against the stored `sha256(raw_key)`.
They must scope every write to the authenticated managed instance slug/domain and must keep `lesser-host` as the source
of canonical registration/signing material.

Project 49 locks the durable async hosted-genesis status contract for that route family. Lesser must treat the
instance-key mint-conversation path as JSON-authoritative durable state: `HTTP 200` / `HTTP 202` is transport success
only, `conversation_id` is persisted early, and publish readiness requires `status=declaration_ready` plus
`produced_declarations`. See `docs/contracts/hosted-genesis-conversation.md`.

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
  - portal/native Host UI streamed creation via SSE on non-instance routes
  - Lesser instance-key hosted genesis via durable JSON HostConversation status
  - explicit completion + finalize steps

Clients should use `SoulAgentPromotion` for current state and `SoulAgentPromotionLifecycleEvent` for notifications,
timelines, and “what changed” UI.

## Route Families

The route families in this section are control-plane session routes unless explicitly marked `instanceKeyAuth`.
Project 49 makes the Lesser production mint-conversation path durable JSON rather than SSE-authoritative.

There are two equivalent route families during the review/finalize phase:

1. Registration-scoped routes, used before the client has pivoted to agent-first state:
   - `/api/v1/soul/agents/register/{id}/mint-conversation/...`
2. Agent-scoped routes, used once the client is keyed by `agentId`:
   - `/api/v1/soul/agents/{agentId}/mint-conversation/...`

The client should prefer the agent-scoped form once `agentId` is known and stored.

There is also a scoped instance-key registration bootstrap family for Lesser-mediated hosted genesis:

- `POST /api/v1/soul/instance/agents/register/{id}/mint-conversation`
- `GET /api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}`

These routes are **not** portal/session routes. They require `instanceKeyAuth` using strict `sha256(raw bearer)` lookup,
do not accept the legacy plaintext key-id fallback, and enforce that the authenticated instance owns the managed domain
for the requested registration. The POST route returns a durable HostConversation status envelope, not an authoritative
SSE completion stream. The GET route is the durable status read used for polling/resume/completion.

There is also a narrow instance-key agent read family for Lesser-mediated private self-scope reads:

- `GET /api/v1/soul/instance/agents/{agentId}/mint-conversations`
- `GET /api/v1/soul/instance/agents/{agentId}/mint-conversations/{conversationId}`

These routes are **not** portal/session routes. They require `instanceKeyAuth` using strict `sha256(raw bearer)` lookup,
do not accept the legacy plaintext key-id fallback, and enforce that the authenticated instance owns the managed domain
for the requested agent identity. The list route returns compact metadata only and never includes `messages` or
`produced_declarations`; the explicit single-conversation route may return the bounded full private record for the
requested conversation. Instance keys do not gain access to general portal/control-plane mint start, complete, finalize
preflight, finalize begin, or finalize mutation routes beyond the explicitly documented registration bootstrap family.
Bounds are part of the contract: list defaults to `limit=20` and rejects values above `50`,
conversation IDs are opaque safe path values up to `128` characters, list responses are capped at `1 MiB`, single
responses are capped at `2 MiB`, oversize responses fail with `413 soul_mint.response_too_large` rather than silent
truncation, and rate limiting returns `429 soul_mint.rate_limited` with `Retry-After` when available.

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

### 2. Preflight principal declaration signing

- `POST /api/v1/soul/agents/register/{id}/principal-declaration/preflight`

Input:

- `principal_address`
- `principal_declaration`
- `declared_at`

Response includes Host-owned signing material:

- `version`
- `principal_address` / `signer_address`
- `signing_method=eip191_personal_sign`
- `message_encoding=hex_bytes`
- `message_hex` and `digest_hex` (the same 32-byte `0x`-prefixed digest)
- `canonical_json`
- canonical `declared_at`

Clients must ask the principal wallet to sign `message_hex` as hex bytes with EIP-191 `personal_sign`, then pass the
resulting signature to verify as `principal_signature` with the returned canonical `declared_at`. Clients should not
reconstruct the principal declaration digest themselves: the digest binds Host-owned registration state (`agentId`,
wallet, domain, localId, chainId, registry contract), the requested principal address, the declaration, and
`declared_at`.

### 3. Verify and prepare approval

- `POST /api/v1/soul/agents/register/{id}/verify`
- equivalent agent-centric alias after resolution:
  - `POST /api/v1/soul/agents/{agentId}/promotion/verify`

Input:

- wallet signature over the registration challenge
- `principal_address`
- `principal_declaration`
- `principal_signature`
- `declared_at` (use the canonical value returned by the principal declaration preflight)

Response includes:

- verified `registration`
- `operation` for the mint
- `safe_tx` payload when Safe mode is enabled
- updated `promotion`

Side effects:

- records proof verification
- creates the optional on-chain binding `SoulOperation`
- moves the promotion into approved / ready for conversation with `anchor_state=hosted_offchain`
- emits lifecycle event `request_approved`

At this point the client may proceed directly to the mint conversation and hosted/off-chain finalize path. Use
`promotion.next_actions` and the binding fields described below to decide whether to show an optional “bind on-chain”
action.

### 4. Optionally record mint execution

- `POST /api/v1/soul/operations/{id}/record-execution`

This is usually performed by an operator or automation after the Safe transaction executes.

When the mint succeeds:

- the identity and promotion move to `anchor_state=immutable_onchain`
- the promotion remains or moves into minted / ready for conversation
- lifecycle event `mint_executed` is emitted

This step may happen before hosted finalize or later as an upgrade. It is not required for zero-state publication.

### 5. Start or continue review

For the Lesser instance-key hosted-genesis path:

- `POST /api/v1/soul/instance/agents/register/{id}/mint-conversation`
- `GET /api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}`

The POST response and GET status read are durable JSON HostConversation envelopes. `HTTP 200` / `HTTP 202` is transport
success only. Lesser must persist `conversation_id` immediately and project `in_progress` and
`declaration_extraction_pending` as progress. When the registration-scoped status read observes
`declaration_ready` with `produced_declarations`, Lesser should treat the declaration evidence as terminal and then call
the explicit instance-key `/finalize` route (`PublishHostedSoul`) to publish the instance-trust hosted/off-chain
registration. The status read is read-only with respect to publication and must not be used as a substitute for publish.

When the latest assistant turn asks the final minted-soul affirmation question, the user's affirmative reply is the
review-completion action for the Lesser instance-key route. Lesser should send that affirmation as the next ordinary
`POST /mint-conversation` turn; Host records the affirmation in the durable transcript and transitions to
`declaration_extraction_pending` instead of generating another assistant turn. Clients should then poll the canonical
GET status until `declaration_ready` with `produced_declarations`, then call the instance-key finalize route to publish
explicit instance-trust identities.

For portal/native Host UI routes:

- registration-scoped:
  - `POST /api/v1/soul/agents/register/{id}/mint-conversation`
- agent-scoped:
  - `POST /api/v1/soul/agents/{agentId}/mint-conversation`

These endpoints stream the assistant response over SSE and update the durable conversation record. SSE is delivery, not
the authoritative completion mechanism for Lesser.

Important behavior:

- the first transition into in-progress review emits lifecycle event `review_started`
- additional turns in the same conversation do not emit duplicate `review_started` events

### 6. Complete the review draft

- registration-scoped:
  - `POST /api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/complete`
- agent-scoped:
  - `POST /api/v1/soul/agents/{agentId}/mint-conversation/{conversationId}/complete`

For the Lesser instance-key hosted-genesis path, this step is normally driven by the final affirmation turn described
above, not by a separate browser-visible Host button. The explicit `/complete` route remains available for controlled
recovery and compatibility; the normal final-affirmation path proceeds through declaration extraction, status polling to
`declaration_ready`, and explicit instance-key finalize publication.

Response is the completed conversation record with extracted declarations stored on the backend.

Side effects:

- promotion moves into `ready_to_finalize`
- review digest, boundary count, and capability count are stored on the promotion
- lifecycle event `finalize_ready` is emitted

### 7. Fetch finalize preflight and signing inputs

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

### 8. Finalize and publish graduation

For the Lesser instance-key hosted-genesis path with `authority_model=instance_trust`, this explicit finalize route is
the publish boundary after status GET observes a publish-ready `HostedGenesisSession`. Status polling exposes readiness;
finalize publishes or returns a typed actionable error.

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
- pending hosted/off-chain identities are activated
- `self_description_version` advances and version history is recorded
- managed canonical ENS channel and gateway/search resolution are recorded as
  `<local>.<instance-slug>.lessersoul.eth` when the managed domain mapping exists
- promotion moves into graduated state
- lifecycle event `graduated` is emitted

If no mint receipt has been recorded, the returned `agent` remains `anchor_state=hosted_offchain` and omits mint
transaction fields. If a mint receipt was already recorded, the returned `agent` remains `immutable_onchain`.

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
  - `anchor_state`
  - `onchain_binding_status`
  - `onchain_binding_available`
  - `hosted_offchain_finalizable`
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

On-chain binding helper semantics:

- `anchor_state=hosted_offchain` means the identity is publishable/usable from host state and may still be promoted.
- `anchor_state=immutable_onchain` means a verified mint execution was recorded for this identity.
- `onchain_binding_status` is one of `unavailable`, `pending`, `proposed`, `executed`, or `failed`.
- `onchain_binding_available=true` means a mint operation exists that can be displayed or retried.
- `hosted_offchain_finalizable=true` means the client can show hosted/off-chain finalize as the primary action even
  though on-chain binding remains optional.
- `next_actions` may include both `begin_finalize` and `record_mint_execution` when the hosted/offchain path is ready
  and the on-chain binding operation is still pending; after hosted graduation it may still include
  `record_mint_execution` as an upgrade action.

Current state should be read from the promotion snapshot, not inferred by replaying events.

## Lifecycle Event Contract

`SoulAgentPromotionLifecycleEvent` is a durable, ordered event feed. Events are listed newest-first and paginated with
`cursor` / `next_cursor`.

Current event types:

- `request_created`
  - request record exists and the wallet/proof workflow can begin
- `request_approved`
  - verification completed; hosted/off-chain review can begin and the mint operation is available for optional binding
- `mint_executed`
  - the on-chain mint succeeded and the identity was promoted to immutable on-chain assurance
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
- optional `anchor_assurance` for transitions that add anchor evidence, especially `mint_executed`
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
- conversation cannot be completed from current state
  - completion requires either an active `in_progress` conversation or a replay of an already `completed` conversation
    with valid produced declarations; failed conversations and completed conversations without declarations fail closed
    with `conversation_status` and `produced_declarations_present` details where available
- promotion not found
  - agent-scoped promotion routes require a known durable workflow
- unauthorized / forbidden
  - ownership and operator rules still apply even with a valid bearer token
- invalid signature inputs
  - wallet verification, principal declaration verification, self-attestation signing, and boundary signatures all fail
    closed
- expected version mismatch
  - finalize/update publication can reject stale clients that try to publish against an outdated version chain
- anchor promotion failure / rollback
  - failed or unrecorded mint execution leaves the identity in its existing hosted/off-chain assurance state
  - retry the same promotion/mint operation or record a verified execution receipt; do not create a replacement
    `agent_id` or parallel namespace
  - if off-chain reconciliation cannot verify an `immutable_onchain` marker, treat the assurance evidence as incomplete
    and surface a repair state; do not revoke ordinary capabilities unless the explicit capability/access policy changed

Clients should surface these as explicit workflow states instead of retrying blindly.

## Client Integration Guidance

- Prefer agent-scoped routes once `agentId` is known.
- Treat preflight output as canonical for finalize signing UX.
- Treat lifecycle events as the notification surface and promotion snapshots as the source of truth for current state.
- Do not infer approval, finalize readiness, or graduation solely from mint conversation status.
- When reconnecting, refresh the current promotion snapshot first, then replay lifecycle events for timeline context if
  needed.
