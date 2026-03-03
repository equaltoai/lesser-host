# Roadmap: lesser-soul v2 spec completion + hardening (lesser-host)

Date: 2026-03-03  
Audience: EqualtoAI stack maintainers

This document is the **implementation roadmap for `lesser-host`** to fully implement `lesser-soul/SPEC.md` v2.0 and
`lesser-soul/ROADMAP.md`, incorporating gaps and issues found during an implementation audit.

## Scope + constraints

**In scope**
- Close all known correctness/security/data-access gaps discovered in the current `lesser-host` implementation.
- Finish v2 spec surface: signatures, principal accountability, boundaries, capabilities, continuity, relationships,
  sovereignty, lifecycle, mint conversation, reputation/search, frontend completeness.
- Provide a safe migration path for existing v1/v2-ish data already in DynamoDB/S3 and already-minted souls on Sepolia.

**Constraints (non-negotiables)**
- Use **TableTheory** for DynamoDB access and **AppTheory** for HTTP handlers (no raw DynamoDB usage).
- No new databases (use the existing DynamoDB state table + S3 soul pack bucket).
- Preserve the single-origin / strict CSP posture in `lesser-host/web`.

**Non-goals**
- Designing a “perfect” global full-text search system (we can do a pragmatic, spec-aligned index within DynamoDB).
- Retrofitting immutable history guarantees onto already-corrupted artifacts without user re-attestation (we can detect,
  quarantine, and guide remediation).

## Current state summary (audit findings)

The v2 surface is largely implemented in `lesser-host` (handlers/models/contracts/UI), but several issues block spec
compliance and/or weaken verifiability:

### Blockers (must fix before claiming v2 compliance)

1) **Boundary append breaks registration self-attestation + version chain**
- Current boundary append “republishes” `registration.json` by patching the JSON in S3 without producing a new
  `attestations.selfAttestation` and without creating a new version record.
- Result: the published registration file fails signature verification, and the version history chain becomes
  non-tamper-evident.

2) **Relationship signatures are replayable / under-scoped**
- Current relationship verification signs only `keccak256(bytes(message))`, which is not bound to `from/to/type/context/createdAt`.
- Result: the same signature can be reused to create different relationship records.

3) **Lifecycle status inconsistencies after mint**
- Mint side-effects update `Status` but not `LifecycleStatus`, producing mismatched reads and incorrect filtering.

4) **Principal accountability is incomplete (off-chain + on-chain)**
- Principal declaration signature is not actually verified (only hex-shaped).
- Backend minting flow uses permit-based `mintSoul` and does not persist/verify principal declaration end-to-end.
- Contracts already contain principal storage (`principalOf`) and a self-mint path, but backend/UI do not use them.

5) **Mint conversation is not connected to producing a signed v2 registration**
- LLM usage is collected then dropped (no metering/credits ledger impact).
- Produced boundaries include placeholder signatures (e.g., `0x00`), and there is no finalize step that produces a
  valid v2 registration version with correct signatures.

6) **Problematic data access patterns (“stringly typed JSON”)**
- Several models/API fields store JSON as strings (e.g., continuity references, relationship context), limiting
  validation, query-ability, and correctness guarantees.
- Some v2 fields are effectively dropped (e.g., capability `constraints` object is treated as a string in public reads).

7) **Search behavior diverges from SPEC intent**
- Search requires a domain in `q` and does not support spec-described filters like `domain=`; boundary filtering can
  devolve into per-result scans; no structured discovery across self-description.

## Status vs `lesser-soul/ROADMAP.md` (as of 2026-03-03)

This is a high-level “where we are” snapshot to help prioritize. “Implemented” below means “exists in `lesser-host`”,
not necessarily spec-correct.

| Milestone | Status in `lesser-host` | Major gaps to close |
|----------|--------------------------|---------------------|
| M1 — Models | Implemented | Typed JSON + migration scaffolding still needed (continuity refs, relationship context, capability constraints). |
| M2 — RegFile v2 | Implemented | Some flows bypass the pipeline (boundary republish). Principal sig verification incomplete. |
| M3 — Contracts | Partial | Contract/interface alignment vs SPEC; backend mint still uses permit path and ignores principal flow. |
| M4 — Boundaries | Implemented | Republish breaks self-attestation + version chain; internal republish truncates at 200. |
| M5 — Capabilities | Partial | Public reads treat `constraints` as string; ensure claimLevel + constraints are preserved end-to-end. |
| M6 — Sovereignty | Partial | State transitions exist; ensure signatures/continuity evidence is spec-aligned; operator vs self clearly distinguished. |
| M7 — Continuity + Versions | Partial | Continuity entries are not fully signature-verified and references are stringly typed. |
| M8 — Relationships | Partial | Signature is under-scoped/replayable; context is stringly typed and filtering is inefficient. |
| M9 — Death + Succession | Partial | Enforce read-only semantics + consistent lifecycle status; ensure continuity evidence is verifiable. |
| M10 — Mint conversation | Partial | Usage metering missing; extracted output not finalizable into a signed v2 registration. |
| M11 — Reputation + Search | Partial | Search filters/params diverge; boundary filtering can degenerate into scans; v2 signals not fully integrated. |
| M12 — Frontend | Partial | UI exists, but needs full v2 flows + multi-signature UX for boundaries/continuity/relationships/finalization. |

## Definition of done (v2 compliance gates)

We consider the v2 implementation “done” only when all of the following are true:

1) **Artifact integrity**
- Any published `registry/v1/agents/<agentId>/registration.json` verifies:
  - JCS canonicalization (RFC 8785) over unsigned doc bytes
  - EIP-191 signature recovery matches the `wallet` field
- No endpoint mutates registration artifacts without:
  - creating a new versioned artifact
  - writing a version record
  - maintaining `previousVersionUri` and hash-chain integrity

2) **Signature scope + replay protection**
- Signatures for boundaries/continuity/relationships/principal declarations are bound to the full signed payload (or a
  typed, canonical representation) and include stable anti-replay fields (`createdAt`, ids, etc).

3) **Lifecycle correctness**
- A single lifecycle state machine exists (no “Status vs LifecycleStatus” divergence).
- Mutations enforce allowed transitions (active → archived/succeeded, etc).

4) **Principal accountability**
- Principal declaration signature is verified and persisted.
- Mint path records principal on-chain (where the contract supports it) and off-chain in the registration file.

5) **Data model robustness**
- No double-encoded JSON in persistent models for fields that must be validated/queried.
- Public APIs return spec-shaped JSON types (objects are objects, arrays are arrays).

6) **Operational safety**
- Mint conversation usage is metered, recorded, and enforced (credits/limits).
- Backfills/migrations are idempotent and safe to run repeatedly.

## Canonical signature payloads (implementation guidance)

The v2 spec uses EIP-191 “personal sign” over a 32-byte digest, where the digest is derived from canonical bytes of the
unsigned payload.

**Registration `attestations.selfAttestation` (SPEC §3.11)**
- `digest = keccak256(JCS(registration_without_selfAttestation))`
- `signature = EIP-191(digest)` recovered address must equal `wallet`

**Principal declaration `principal.signature` (SPEC §3.3)**
- Baseline spec-compatible interpretation:
  - `digest = keccak256(bytes(principal.declaration))`
  - `signature = EIP-191(digest)` recovered address should match `principal.identifier` (when it is a wallet address)
- Hardening (optional, but recommended): define a v2.1 principal payload that binds to a specific agent (e.g.,
  include `agentId/domain/localId/wallet/declaredAt` in a JCS payload) and support both schemes during migration.

**Boundary `signature` (SPEC §3.11)**
- `digest = keccak256(bytes(boundary.statement))`
- `signature = EIP-191(digest)` recovered address must equal `wallet`

**Continuity entry `signature` (SPEC §3.11 + §3.8)**
- `digest = keccak256(JCS(entry_without_signature))`
- `signature = EIP-191(digest)` recovered address must equal `wallet`

**Relationship record `signature` (SPEC §7.2)**
- Not fully specified in the spec today, but MUST be bound to the entire record to prevent replay:
  - `digest = keccak256(JCS(record_without_signature))`
  - Record should include: `fromAgentId`, `toAgentId`, `type`, `context` (object), `message`, `createdAt`
  - `signature = EIP-191(digest)` recovered address must equal the `fromAgentId` wallet

## Key design decisions to resolve early

These decisions materially affect API shapes, UX, and migration work. Resolve them before (or during) M0–M2.

1) **Boundary writes: 1-step vs 2-step**
- **1-step (update-registration only):** boundaries are edited via `update-registration`, with server enforcing append-only
  rules against the previous version.
  - Pros: one canonical mutation endpoint; avoids extra begin/confirm APIs.
  - Cons: client must submit the full registration doc; can be heavier.
- **2-step (begin/confirm):** boundary endpoint returns canonical bytes/digest for both boundary + updated registration;
  client signs and confirms.
  - Pros: minimal client-side canonicalization risk; better UX for “add one boundary”.
  - Cons: more endpoints; more state to manage; still requires multiple signatures in practice (boundary + selfAttestation).

2) **Continuity semantics: curated journal vs system event log**
- If continuity is curated, avoid automatically emitting entries for every system event.
- If we want system-emitted entries, define a separate host-attested event feed (do not pretend they are continuity
  journal entries unless they can be wallet-signed).

3) **Principal signature hardening**
- SPEC allows a principal signature over free-text `declaration` only, which is replayable across agents if reused.
- Decide whether to:
  - accept spec as-is, or
  - introduce a v2.1 principal signature payload that binds to the agent identity and support both during migration.

4) **Search/index design**
- Decide whether boundary search is:
  - “keyword contains” (requires an index strategy), or
  - “tag-based” (safer and cheaper; boundaries provide explicit tags for indexing).
- Decide whether to index self-description (e.g., curated keywords) or keep search strictly identity/capability/lifecycle.

## Milestone plan (implementation in lesser-host)

Milestones below are structured to align with `lesser-soul/ROADMAP.md` (M1–M12), but include a **pre-flight hardening
milestone (M0)** for the blockers.

Each milestone includes: deliverables, key decisions, acceptance criteria, and rollout notes.

---

### M0 — Stop-the-bleeding hardening (blockers)

**Deps:** none  
**Goal:** eliminate behaviors that publish unverifiable artifacts or allow signature replay.

**Key files (likely)**
- `internal/controlplane/handlers_soul_boundaries.go`
- `internal/controlplane/handlers_soul_relationships.go`
- `internal/controlplane/handlers_soul_operations.go`
- `internal/controlplane/handlers_soul_update_registration.go`
- `internal/controlplane/handlers_soul_public.go`
- `internal/store/models/soul_agent_identity.go`

**Deliverables**
- Registration artifact integrity:
  - Remove/disable any S3 “patch-and-republish” flows that mutate `registration.json` without a new self-attestation
    and version record.
  - Add a server-side guard: reject any attempt to publish a v2 registration that fails signature verification.
- Relationship signature scope:
  - Define a canonical relationship signing payload (JCS over record-without-signature, or equivalent), and verify it.
  - Add replay resistance by ensuring the signed payload includes `fromAgentId`, `toAgentId`, `type`, `context`,
    `message`, and `createdAt` (and/or a deterministic `relationshipId`).
- Lifecycle consistency:
  - Ensure mint side-effects update the canonical lifecycle fields used by public reads/search filters.
- Emergency telemetry:
  - Add structured logs/metrics for: invalid signature attempts, failed republish, and version-chain violations.

**Acceptance**
- [ ] No code path can modify S3 registration artifacts “in place” without a corresponding new version record.
- [ ] Relationship creation rejects signatures not bound to the full relationship record.
- [ ] Reads/searches use the same lifecycle status source consistently.

**Rollout**
- Ship behind a feature flag if needed (`SOUL_V2_STRICT_INTEGRITY=true`) to allow staged enablement.
- If existing artifacts are already invalid, expose them as “invalid/needs re-attestation” rather than silently serving.

---

### M1 — Data foundation v2.1 (typed fields + migration scaffolding)

**Deps:** none (can start after M0)  
**Goal:** eliminate stringly-typed JSON for fields that must be validated/filtered, while preserving backward
compatibility during migration.

**Key files (likely)**
- `internal/store/models/soul_agent_continuity.go`
- `internal/store/models/soul_agent_relationship.go`
- `internal/store/models/soul_agent_dispute.go`
- `internal/controlplane/handlers_soul_continuity.go`
- `internal/controlplane/handlers_soul_relationships.go`
- `internal/controlplane/handlers_soul_capabilities.go`
- `internal/soul/registration_v2.go`
- `scripts/` (new: idempotent backfills)

**Deliverables**
- Model improvements (TableTheory):
  - `SoulAgentContinuity.References`: migrate from `string` (JSON array) to `[]string` (native list) or a structured
    `[]Reference` type if we need typed refs.
  - `SoulAgentRelationship.Context`: store as a DynamoDB map and also persist extracted `taskType` for filtering.
  - Capability constraints: ensure constraints are stored and returned as objects (not string fields).
- Migration strategy:
  - Dual-read / dual-write during transition (read old+new; write new; optionally backfill old for older clients).
  - Add idempotent backfill scripts that can:
    - parse old string fields
    - populate new typed fields
    - validate and quarantine malformed legacy records
- API versioning policy:
  - Define when to bump API response `version` fields and how long to support legacy shapes.

**Acceptance**
- [ ] No public endpoint returns an object field as a quoted JSON string.
- [ ] Backfill scripts are idempotent, safe, and have a dry-run mode.
- [ ] Typecheck + tests cover both legacy and migrated data reads.

---

### M2 — Registration file v2: single publishing pipeline for all mutations

**Deps:** M0, M1  
**Goal:** ensure that **every** change that affects the v2 self-definition results in a new signed registration version
and a version record.

**Key files (likely)**
- `internal/controlplane/handlers_soul_update_registration.go`
- `internal/controlplane/handlers_soul_registry.go` (where Phase 1 registration is created)
- `internal/controlplane/handlers_soul_boundaries.go`
- `internal/controlplane/handlers_soul_lifecycle.go`
- `internal/store/models/soul_agent_version.go`
- `internal/soul/registration_v2.go`

**Deliverables**
- Consolidate publishing:
  - All mutation handlers that affect the self-definition must call a single “publish new registration version” helper
    that:
    - builds/validates the v2 document
    - enforces `previousVersionUri`
    - verifies EIP-191 self-attestation over JCS bytes
    - writes versioned artifact + current artifact
    - writes a version record (with hash chaining)
    - updates identity’s `SelfDescriptionVersion`
- Concurrency and idempotency:
  - Add optimistic concurrency control (e.g., require `expectedVersion` on updates) or idempotency keys to prevent
    accidental double-writes or inconsistent version increments.
- Repair tooling:
  - Add an operator-only endpoint or script that:
    - verifies all version records match S3 `sha256`
    - detects “current registration” that does not match any version record
    - flags the agent as requiring re-attestation

**Acceptance**
- [ ] Any boundary/capability/self-description change results in a new S3 versioned object + version record.
- [ ] `previousVersionUri` chain is enforced for v2 documents.
- [ ] A background integrity scan can detect all inconsistencies introduced by prior behavior.

---

### M3 — Contracts + mint flow alignment (principal + self-mint)

**Deps:** M0 (for integrity posture), M2 (registration publishing)  
**Goal:** align on-chain minting and principal storage with the v2 spec’s accountability goals.

**Key files (likely)**
- `contracts/contracts/SoulRegistry.sol`
- `contracts/test/SoulRegistry.test.js`
- `internal/controlplane/handlers_soul_registry.go` (register begin/verify + mint payload construction)
- `internal/controlplane/handlers_soul_operations.go` (mint side-effects)
- `internal/soul/registration_v2.go` (principal validation)
- `web/src/pages/portal/SoulRegister.svelte`

**Key decision to resolve early**
- The current contract implementation uses an **attestor-signed EIP-712 self-mint attestation** and stores `principal`.
  The spec’s v2 interface describes `selfMintSoul(... principal, principalSig)` and a domain-attestation scheme.
  We need an explicit decision:
  - **Option A (spec-strict):** update contract to match SPEC interface + attestation model.
  - **Option B (implementation-led):** update SPEC/ROADMAP to reflect the attestor-signed typed attestation design.

**Deliverables (regardless of option)**
- Backend mint flow:
  - Persist principal address + principal signature (verified) at Phase 1 (begin/verify).
  - Use the contract’s self-mint/principal path for new mints (and record `principalOf` off-chain for reads).
- Contract updates (as needed based on option):
  - Ensure `principalOf(agentId)` exists and is covered by tests.
  - Ensure attestor registry management is Safe-first and has tests.
- Wallet UX:
  - Ensure portal/frontend has a clear responsibility statement to sign for the principal declaration.

**Acceptance**
- [ ] Principal declaration signatures are verified server-side against a well-defined payload.
- [ ] New mints record principal on-chain (and are queryable via `principalOf`).
- [ ] Contracts test suite covers valid/invalid self-mint scenarios and replay protection.

**Rollout**
- Sepolia-only: deploy new contract version and update `lesser-host` config; keep old mint path available for a short
  transition window if needed.

---

### M4 — Boundaries (append-only + correct republish)

**Deps:** M2  
**Goal:** restore spec-compliant boundaries while keeping append-only invariants.

**Key files (likely)**
- `internal/controlplane/handlers_soul_boundaries.go`
- `internal/store/models/soul_agent_boundary.go`
- `internal/controlplane/handlers_soul_update_registration.go` (if boundary changes require a new self-attested version)
- `web/src/pages/portal/SoulAgentDetail.svelte`

**Deliverables**
- Boundary write flow must produce a new registration version:
  - Either:
    - move boundary creation into `update-registration` with append-only enforcement on the boundaries array, **or**
    - implement a 2-step boundary append (`begin` → returns canonical unsigned doc digest; `confirm` → accepts signature)
      so the server can republish a newly signed registration.
- Pagination and completeness:
  - Public boundaries reads must paginate reliably.
  - Any internal “build registration from DB” must fetch all boundaries (no fixed `Limit(200)` truncation).
- Validation:
  - Boundary signatures are verified over `keccak256(bytes(statement))` (EIP-191), and the `category` enum matches SPEC.

**Acceptance**
- [ ] Appending a boundary never produces an unverifiable `registration.json`.
- [ ] Boundaries remain append-only; supersession uses `supersedes`.
- [ ] Registration versions reflect boundaries exactly and are signature-valid.

---

### M5 — Structured capabilities (constraints + claim-level correctness)

**Deps:** M1, M2  
**Goal:** ensure v2 capabilities are preserved end-to-end (including `constraints`) and claim-level filtering works.

**Key files (likely)**
- `internal/controlplane/handlers_soul_update_registration.go`
- `internal/controlplane/handlers_soul_capabilities.go`
- `internal/store/models/soul_agent_index_items.go`
- `internal/soul/registration_v2.go`
- `web/src/lib/api/soul.ts`

**Deliverables**
- Public reads:
  - `GET .../capabilities` returns v2-shaped capabilities, including object-typed `constraints`.
- Indexing:
  - Capability index items include `claimLevel` and are maintained consistently on registration updates.
- Validation:
  - Enforce valid `claimLevel` transitions per SPEC (and/or document current policy if SPEC is flexible).

**Acceptance**
- [ ] No capability fields are dropped when round-tripping through registration publishing and public reads.
- [ ] Search filters by capability + claimLevel return correct agents.

---

### M6 — Sovereignty primitives (self-suspend + disputes + validation opt-in)

**Deps:** M1, M2, M7 (for continuity signing strategy)  
**Goal:** implement sovereignty actions with a correct lifecycle state machine and verifiable continuity evidence.

**Key files (likely)**
- `internal/controlplane/handlers_soul_sovereignty.go`
- `internal/controlplane/handlers_soul_suspension.go`
- `internal/controlplane/handlers_soul_validation.go`
- `internal/store/models/soul_agent_dispute.go`
- `internal/controlplane/handlers_soul_lifecycle.go`

**Deliverables**
- Self actions:
  - `self-suspend` / `self-reinstate` enforce allowed transitions and persist reason + timestamps.
- Validation opt-in:
  - Persist accept/decline decisions without penalizing reputation; expose in reads.
- Disputes:
  - Store dispute records with evidence references; ensure disputes can be linked to reputation signals.
- Continuity:
  - Decide and implement how sovereignty events emit continuity entries **with signatures** (see M7).

**Acceptance**
- [ ] Self-suspend/reinstate transitions are correct and distinguishable from operator suspension.
- [ ] Disputes are persisted and visible in a way that downstream reputation aggregation can consume.

---

### M7 — Continuity (signed journal + typed references)

**Deps:** M1  
**Goal:** continuity becomes a signed, verifiable journal; no unsigned system writes.

**Key files (likely)**
- `internal/store/models/soul_agent_continuity.go`
- `internal/controlplane/handlers_soul_continuity.go`
- `internal/controlplane/handlers_soul_versions.go`
- `internal/controlplane/soul_store_helpers.go`
- `web/src/lib/api/soul.ts`

**Deliverables**
- API contract:
  - `POST .../continuity` accepts a complete continuity entry including `signature` and validates it using the v2 signing
    model (JCS over entry-without-signature).
- System events:
  - Remove the pattern of server-written unsigned continuity entries, **or** introduce a separate “host-attested event”
    mechanism that is explicitly not the continuity journal (and document it).
- Storage:
  - References stored as typed arrays and preserved in reads.

**Acceptance**
- [ ] Every stored continuity entry is signature-verifiable against the agent wallet.
- [ ] Reads paginate stably and return correct JSON types.

---

### M8 — Relationships (schema + signature binding + context typing)

**Deps:** M1  
**Goal:** relationships become task-specific trust signals with robust signatures and queryable context.

**Key files (likely)**
- `internal/store/models/soul_agent_relationship.go`
- `internal/store/models/soul_agent_index_relationship_from.go`
- `internal/controlplane/handlers_soul_relationships.go`
- `internal/controlplane/handlers_soul_public.go` (public relationship reads + filtering)
- `web/src/lib/api/soul.ts`

**Deliverables**
- Signature scheme:
  - Define a canonical signed payload for relationship records (including ids, type, context, createdAt).
    - Verify signature against `fromAgent` wallet and enforce non-malleability.
- Storage:
  - Store `context` as an object/map (or store extracted fields like `taskType` alongside raw JSON).
  - Add/maintain the `SOUL#RELATIONSHIPS_FROM#...` index consistently.
- Revocation behavior:
  - Ensure revocations do not delete prior grants (they are additive signals).

**Acceptance**
- [ ] A relationship signature cannot be replayed to create a different record.
- [ ] `GET .../relationships?type=&taskType=` filtering works without per-item JSON parsing.
- [ ] V1 endorsements are still visible as type `endorsement` for backward compatibility.

---

### M9 — Lifecycle: archive + succession (read-only enforcement)

**Deps:** M2, M7  
**Goal:** implement “death” semantics without burning the on-chain token, while enforcing read-only behavior off-chain.

**Key files (likely)**
- `internal/controlplane/handlers_soul_lifecycle.go`
- `internal/controlplane/handlers_soul_lifecycle_transitions_internal_test.go`
- `internal/store/models/soul_agent_identity.go`
- `internal/controlplane/handlers_soul_update_registration.go` (enforce read-only after archive)
- `internal/controlplane/handlers_soul_public.go` (public reads + search status)

**Deliverables**
- Lifecycle status machine:
  - Enforce one-way transitions: active → archived/succeeded; prohibit edits after archive/succession.
- Successor link:
  - Persist successor relationships and emit signed continuity entries for both agents (declared/received).
- Public reads:
  - Ensure status is reflected consistently across `GET agent`, search filters, and registration reads.

**Acceptance**
- [ ] Archived agents cannot update registration/boundaries/capabilities.
- [ ] Succession creates bidirectional continuity entries and is visible in reads.

---

### M10 — Minting conversation (phase 2): metered, persisted, and finalizable

**Deps:** M1, M2, M5  
**Goal:** make the mint conversation produce a **real**, **signed** v2 registration version and charge credits for LLM use.

**Key files (likely)**
- `internal/controlplane/handlers_soul_mint_conversation.go`
- `internal/ai/` (provider adapters)
- `internal/store/models/soul_agent_mint_conversation.go`
- `internal/store/models/ai_shared.go` (provider usage metadata)
- `internal/store/models/usage_ledger_entry.go` (credits debits)
- `internal/store/models/instance_budget_month.go` (monthly budget tracking)
- `web/src/pages/portal/SoulRegister.svelte`
- `web/src/pages/portal/SoulAgentDetail.svelte`

**Deliverables**
- Metering:
  - Persist LLM usage/cost and apply it to the credits/usage ledger (fail closed if insufficient credits).
- Conversation persistence:
  - Store transcripts and extracted declarations reliably; provide conversation history reads if needed by UI.
- Finalization flow:
  - Convert extracted declarations into a draft v2 registration update that can be reviewed and signed.
  - Replace placeholder boundary signatures with real wallet signatures (and enforce their verification).
  - Produce a new registration version via the shared publishing pipeline (M2).

**Acceptance**
- [ ] Conversation usage impacts billing/credits and is auditable.
- [ ] Finalization produces a v2 registration file that passes signature verification and version-chain checks.

---

### M11 — Reputation worker v2 + search (spec-aligned filters, no scans)

**Deps:** M1, M4, M8  
**Goal:** extend reputation to v2 signals and make search align with spec filters without degenerate scans.

**Key files (likely)**
- `internal/controlplane/handlers_soul_public.go` (search + public reads)
- `internal/controlplane/handlers_soul_transparency.go`
- `internal/controlplane/handlers_soul_config.go`
- `internal/soulreputationworker/server.go`
- `internal/store/models/soul_agent_reputation.go`
- `internal/store/models/soul_agent_failure.go`
- `internal/store/models/soul_agent_boundary.go`
- `internal/store/models/soul_agent_index_items.go` (and any new index models)

**Deliverables**
- Search:
  - Support spec query parameters (`q`, `domain`, `capability`, `claimLevel`, `boundary`, `status`).
  - Replace boundary “contains scan per agent” with an indexable approach (e.g., boundary keyword index items or a
    curated boundary-tag field).
- Reputation worker:
  - Incorporate v2 signals: integrity, failure/recovery, boundary violations, relationship outcomes.
  - Publish transparency endpoint (`GET .../transparency`) and config weights (`GET .../config`) as per spec.

**Acceptance**
- [ ] Search filters work individually and in combination at scale (no full table scans).
- [ ] Reputation worker computes v2 dimensions deterministically and publishes roots as before.

---

### M12 — Frontend v2 completeness (portal UX + signing flows)

**Deps:** M4–M11  
**Goal:** ship a complete and coherent portal UI for all v2 features with safe wallet signing UX.

**Key files (likely)**
- `web/src/lib/api/soul.ts`
- `web/src/pages/portal/SoulRegister.svelte`
- `web/src/pages/portal/SoulAgentDetail.svelte`
- `web/src/pages/portal/Souls.svelte`
- `web/src/lib/wallet/` (signing helpers, if present)

**Deliverables**
- API client + types: full coverage for all v2 endpoints and response shapes.
- Signing UX:
  - Boundary/continuity/relationship signing flows use clear, human-readable payload previews.
  - Avoid “double-signing surprise”: if an operation requires multiple signatures (e.g., boundary + registration
    self-attestation), the UI explicitly sequences them.
- Mint conversation UI:
  - Streaming SSE display, conversation history, extracted draft preview, “finalize + sign” flow.

**Acceptance**
- [ ] All v2 features are usable end-to-end from the portal UI.
- [ ] Typecheck passes and UI is CSP-compliant (no inline scripts/styles).

---

## Cross-cutting security checklist (apply across milestones)

- **Domain separation for signatures:** every signature payload must include context that prevents reuse in other
  endpoints/records (e.g., include `type`, ids, timestamps, and an explicit “kind” field).
- **Replay protection:** ensure deterministic ids or include `createdAt` and enforce uniqueness on storage keys.
- **AuthZ consistency:** every write must check portal ownership/domain access for the agent(s) involved.
- **Artifact immutability posture:** treat versioned S3 objects as immutable; consider enabling bucket versioning and/or
  object lock policies (if acceptable) for defense-in-depth.
- **Input size limits:** enforce reasonable max sizes for statements, context, and transcripts to prevent cost/DoS.
- **No silent best-effort on integrity:** best-effort behavior is acceptable for non-critical indexes, but not for
  publishing signed artifacts or recording version history.

## Rollout strategy (recommended)

1) **Ship M0 behind a strict-integrity flag**; enable on test/staging first.
2) **Run integrity scan + backfills** (M1/M2 tooling) and fix/quarantine inconsistent agents.
3) **Deploy contract changes (M3)** on Sepolia and update `lesser-host` config.
4) **Enable v2 mutation flows** (boundaries/capabilities/relationships/continuity) only once publishing is unified (M2).
5) **Enable metered mint conversation (M10)** once credits ledger integration is in place.
6) **Enable new search filters + indexes (M11)** after indexes are built and validated.

## Suggested verification gates per milestone

- Go backend: `go test ./...`
- Contracts: `cd contracts && npm test`
- Web: `cd web && npm run typecheck`
- Add targeted tests for:
  - signature scope / replay attempts
  - registration self-attestation verification
  - version chain integrity (`previousVersionUri`, sha256 chain)
  - migration/backfill idempotency

## Appendix A — SPEC endpoint checklist (SPEC §11)

This is a quick checklist to ensure we don’t miss endpoints while hardening behavior.

**Public**
- [x] `GET /api/v1/soul/config`
- [x] `GET /api/v1/soul/agents/{agentId}`
- [x] `GET /api/v1/soul/agents/{agentId}/registration` (verify integrity; quarantine invalid artifacts)
- [x] `GET /api/v1/soul/agents/{agentId}/reputation`
- [x] `GET /api/v1/soul/agents/{agentId}/validations`
- [x] `GET /api/v1/soul/agents/{agentId}/capabilities` (fix constraints typing)
- [x] `GET /api/v1/soul/agents/{agentId}/boundaries` (ensure reg/version pipeline correctness)
- [x] `GET /api/v1/soul/agents/{agentId}/transparency`
- [x] `GET /api/v1/soul/agents/{agentId}/continuity` (ensure signature-verifiable + typed refs)
- [x] `GET /api/v1/soul/agents/{agentId}/versions`
- [x] `GET /api/v1/soul/agents/{agentId}/relationships` (fix signature + context typing)
- [x] `GET /api/v1/soul/search` (align params/filters; avoid scans)

**Portal (customer auth required)**
- [x] `POST /api/v1/soul/agents/register/begin` (add/verify principal declaration)
- [x] `POST /api/v1/soul/agents/register/{id}/verify` (mint payload alignment)
- [x] `GET /api/v1/soul/agents/mine`
- [x] `POST /api/v1/soul/agents/{agentId}/rotate-wallet/begin`
- [x] `POST /api/v1/soul/agents/{agentId}/rotate-wallet/confirm`
- [x] `POST /api/v1/soul/agents/{agentId}/update-registration` (becomes the only publication path for reg mutations)
- [x] `POST /api/v1/soul/agents/register/{id}/mint-conversation` (metering + finalization)
- [x] `POST /api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}/complete` (ensure outputs are signed + published)
- [x] `GET /api/v1/soul/agents/register/{id}/mint-conversation/{conversationId}`
- [x] `POST /api/v1/soul/agents/{agentId}/self-suspend`
- [x] `POST /api/v1/soul/agents/{agentId}/self-reinstate`
- [x] `POST /api/v1/soul/agents/{agentId}/boundaries`
- [x] `POST /api/v1/soul/agents/{agentId}/continuity`
- [x] `POST /api/v1/soul/agents/{agentId}/archive`
- [x] `POST /api/v1/soul/agents/{agentId}/successor`
- [x] `POST /api/v1/soul/agents/{agentId}/dispute`
- [x] `POST /api/v1/soul/agents/{agentId}/validations/challenges/{challengeId}/opt-in`

**Operator/admin**
- [x] `GET /api/v1/soul/operations`
- [x] `GET /api/v1/soul/operations/{id}`
- [x] `POST /api/v1/soul/operations/{id}/record-execution` (ensure lifecycle fields are kept consistent)
- [x] `POST /api/v1/soul/reputation/publish`
- [x] `POST /api/v1/soul/validation/publish`
- [x] `POST /api/v1/soul/agents/{agentId}/suspend`
- [x] `POST /api/v1/soul/agents/{agentId}/reinstate`

## Appendix B — Data-model migration matrix (targeted fixes)

This lists the specific “stringly JSON” issues to remove, with recommended migration patterns.

| Area | Current storage | Target storage | Migration approach |
|------|-----------------|----------------|-------------------|
| Continuity `references` | JSON array encoded as `string` | `[]string` | Add new attr (e.g., `referencesV2`), dual-read, backfill, then switch writes. |
| Relationship `context` | JSON object encoded as `string` | `map[string]any` + extracted `taskType` | Add typed attr + `taskType`; dual-read; stop per-item JSON parsing in filters. |
| Capability `constraints` | Treated as string in public reads | `map[string]any` in API output | Update extraction and TS types; ensure `constraints` round-trips through reg publishing. |
| Mint conversation `producedDeclarations` | JSON object encoded as `string` | (optional) typed structure | Keep raw JSON string if only stored for display, but validate on write and version fields. |
