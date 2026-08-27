# Issue #1067 (part C2 of #1061) — SoulAgentMintConversation gsi4 agent-scoped time-ordered index

- Status: Implemented (PR pending review; stack deploy + backfill are operator actions)
- Date: 2026-08-27
- Linkage: Closes #1067; Refs #1061 (part C2 of the schema wave; do not close). Part C1 (#1069,
  merged `917fcf4a`) added gsi3 + the marker-gated consumer pattern + the operator backfill tool
  whose `modelPlan` structure was built C2-ready. Related wave sibling: equaltoai/lesser#1469.
- Part A: #1066 bounded the request-path reads; part B (19 partition reads) is a separate
  delegation.

## Problem

The operator mint-conversation list
(`internal/controlplane/handlers_soul_mint_conversation.go` → `listSoulAgentMintConversations`)
selected its page with `Where(PK) + Where(SK BEGINS_WITH MINT_CONVERSATION#) + OrderBy(SK, DESC) +
Limit(limit)`. `conversationId` is a crypto/rand token
(`handlers_soul_mint_conversation_async.go:503-509`), so SK order is arbitrary with respect to
recency: beyond the page limit the returned page is an arbitrary subset, and the portal
auto-resume (`web/src/pages/portal/SoulMintConversation.svelte:218-226`) silently resumes the wrong
conversation. The fix is a key-bounded, recency-ordered GSI (gsi4).

## GSI slot audit (evidence)

The state table (`lesser-host-<stage>-state`, `cdk/lib/lesser-host-stack.ts:94-128`) has 3 of the
default 20 GSIs per table, PAY_PER_REQUEST billing (no capacity planning needed). The next free
slot is **gsi4** — the ONLY index added by this PR (DynamoDB creates exactly one GSI per
`UpdateTable`, so gsi4 deploys in its own stack update).

- **gsi1** (`gsi1PK`/`gsi1SK`): `OWNER#` (instances), `INSTANCE_DOMAINS#` (domains),
  `SOUL_OP_STATUS#` (soul operations), `PROVISION_INSTANCE#` (provisioning), `TIPREG_OP_STATUS#`
  (tip registry ops), `USER_APPROVAL#` (operator approvals), `RENDER_EXPIRES#` (render artifacts),
  comm send idempotency. Overloading would entangle a high-write model with unrelated partitions.
- **gsi2** (`gsi2PK`/`gsi2SK`): `UPDATE_ACTIVE` (update jobs), comm-mailbox threads,
  hosted-genesis sessions (agent-scoped session list). Do not overload.
- **gsi3** (`gsi3PK`/`gsi3SK`): SoulAgentIdentity status enumeration (part C1). Fully allocated to
  the identity model.
- **gsi4** — unused, becomes the mint-conversation time-ordered index.

## Key shape

```
gsi4PK = SOUL#AGENT#<agentId>          (same as the base PK; lowercased hex)
gsi4SK = <createdAt>#<conversationId>  (fixed-width nanosecond UTC timestamp + token)
```

- `gsi4PK` equals the base PK, so the index groups every conversation item of one agent into a
  single partition — the exact set the operator list needs.
- `gsi4SK` is `createdAt.UTC().Format("2006-01-02T15:04:05.000000000Z") + "#" + conversationId`.
  The fixed-width format orders lexicographically exactly like the timestamps (RFC3339Nano drops
  trailing fraction digits and breaks string ordering across differing precisions; the
  HostedGenesisSession gsi2 key shares that caveat and the new index is specified fresh). The
  crypto/rand conversation id suffix guarantees uniqueness.
- Both key sources are **immutable after creation** (`agentId`, `createdAt`), so an item's index
  position never changes — field-scoped updates cannot reorder it.

## Model + write-path coverage

`SoulAgentMintConversation` gains `GSI4PK`/`GSI4SK` (tags `index:gsi4,pk`/`index:gsi4,sk`),
maintained by `UpdateKeys()` → `updateGSI4()`, computed from `AgentID` + `CreatedAt` via the
exported helpers `models.SoulMintConversationGSI4PK`/`GSI4SK` (also used by the store layer and the
backfill tool so all three produce byte-identical keys). TableTheory does **not** invoke the model
hooks, so every write site computes keys explicitly. Because the key sources are immutable, the
maintenance invariant is: **every create writes the keys, and every field-scoped update re-writes
them from the stored CreatedAt** so healed/backfilled items can never silently drop out of the
index. The full writer enumeration (15 sites):

**Creates (whole-item marshal, carry CreatedAt → `UpdateKeys` writes gsi4):**

1. `handlers_soul_mint_conversation.go` → `debitMintConversationStreamCredits` (extraWrites create
   branch, `tx.Create(conv)`, ~lines 1002-1013) — SSE lane, new conversation.
2. `handlers_soul_mint_conversation_async.go` → `persistHostedGenesisAcceptedTurn` (create branch,
   `tx.Create(conv)`, ~lines 924-943) — async lane, new conversation.
3. `internal/store/soul_mint_conversation.go` → `PutSoulAgentMintConversation` (`CreateOrUpdate`,
   ~line 83), called from `internal/aiworker/hosted_genesis.go:317` (loaded conversation →
   `UpdateKeys`).

**Field-scoped updates (`Update(fields...)` / `tx.UpdateWithBuilder` write only the named fields,
so the gsi4 attributes are named on every one, threaded with the stored CreatedAt):**

4. `handlers_soul_mint_conversation.go` → `debitMintConversationStreamCredits` (update branch,
   `tx.UpdateWithBuilder` `ChargedCredits` + `GSI4PK`/`GSI4SK`, ~lines 1015-1030; SSE lane existing
   conversation; `mintConversationSession.createdAt` carries the loaded/stored CreatedAt).
5. `handlers_soul_mint_conversation.go` → `updateMintConversationMessages`
   (`.Update("Messages", "GSI4PK", "GSI4SK")`, ~line 1184; test-only caller).
6. `handlers_soul_mint_conversation.go` → `updateMintConversationTurn`
   (`.Update("Messages", "Usage", "GSI4PK", "GSI4SK")`, ~line 1231; SSE lane).
7. `handlers_soul_mint_conversation.go` → `updateMintConversationStatus`
   (`.Update("Messages", "ProducedDeclarations", "Status", "CompletedAt", "GSI4PK", "GSI4SK")`,
   ~line 1252; SSE lane).
8. `handlers_soul_mint_conversation_async.go` → `persistHostedGenesisProgression`
   (`tx.UpdateWithBuilder`, ~lines 301-329), `CreatedAt: conv.CreatedAt` + guarded `GSI4PK`/`GSI4SK`.
9. `handlers_soul_mint_conversation_async.go` → `persistHostedGenesisAcceptedTurn` (accepted-turn
   update branch, `tx.UpdateWithBuilder`, ~lines 945-973), `CreatedAt` from the hydrated
   `session.conv`.
10. `handlers_soul_mint_conversation_recover.go` → `persistHostedGenesisFailedRetryPending`
    (`tx.UpdateWithBuilder`, ~lines 641-654).
11. `handlers_soul_mint_conversation_recover.go` → `persistHostedGenesisPendingAssistantRetryTransition`
    (`tx.UpdateWithBuilder`, ~lines 683-696).
12. `handlers_soul_mint_conversation_recover.go` → `persistHostedGenesisRetryDispatchFailure`
    (`tx.UpdateWithBuilder`, ~lines 751-764).
13. `handlers_soul_mint_conversation_recover.go` → `persistHostedGenesisMicroVMRecoveryFailure`
    (`tx.UpdateWithBuilder`, ~lines 1069-1081).
14. `internal/store/hosted_genesis_sessions.go` → `FailHostedGenesisSessionAndConversation`
    (`tx.UpdateWithBuilder`, ~lines 261-283; `conversation.BeforeUpdate()` recomputes keys).
15. `internal/store/hosted_genesis_sessions.go` → `PublishHostedGenesisSessionAndConversation`
    (`tx.UpdateWithBuilder`, ~lines 418-434; same).

Every `UpdateWithBuilder` site guards the gsi4 `Set`s on non-empty computed keys
(`if GSI4PK != "" && GSI4SK != ""`): a legacy item without a stored `CreatedAt` keeps its existing
index keys instead of being moved to a zero-time partition, and the `updateGSI4()` model guard
preserves keys when `CreatedAt` is absent (partial update models). Sites 8-13 and 14-15 load (or
receive) the conversation first, so the stored `CreatedAt` is threaded through the update model and
`UpdateKeys` computes the exact keys. No code path hard-deletes a conversation record; if one is
ever introduced, DynamoDB removes the item from all GSIs automatically.

**Writer-coverage tests** (mirroring the C1 lifecycle-test convention):
`handlers_soul_mint_conversation_gsi4_writer_internal_test.go` captures each representative
`UpdateWithBuilder` closure (sites 8, 9, 12) and asserts the update model carries the gsi4 keys and
the closure `Set`s them; `internal/store/soul_mint_conversation_gsi4_writer_test.go` does the same
for the store sites (14, 15); `TestMintConversationPersistenceHelpers_UpdateStoredFields` asserts
the exact field lists of sites 5-7 and the models' computed keys. Sites 10, 11, 13 share the
identical guarded-`Set` block shape as 12 in the same file.

## Consumer changes (bounded GSI query)

`listSoulAgentMintConversations` now answers from
`store.ListSoulAgentMintConversationsByAgent`:
`Index("gsi4")` + `Where("gsi4PK", "=", SOUL#AGENT#<agentId>)` + `OrderBy("gsi4SK", "DESC")`
(compiles to `ScanIndexForward=false`) + `Limit(limit)` — one bounded page, recency-ordered by the
index itself. The in-memory `CreatedAt` sort is kept as a defensive no-op for the response
contract. The response shape is unchanged (`version`/`conversations`/`count`), so the portal
auto-resume picks the genuinely newest conversation.

## Graceful degradation (failure shape, chosen + documented)

The stack update that creates gsi4 deploys **before** the backfill completes. During that window
the consumer must fail explicitly, never silently return an empty/partial conversation set as if it
were complete:

1. **Index absent** (deploy not yet run): the gsi4 query fails with a DynamoDB error; the handler
   propagates HTTP 500 `failed to list mint conversations`. Explicit.
2. **Index present but un-backfilled**: a gsi4 query would return zero/partial items with no
   error. The backfill tool writes the **completeness marker**
   (`PK=META#SOULAGENTMINTCONVERSATION#GSI4`, `SK=BACKFILL`, model
   `SoulAgentMintConversationGSI4BackfillMarker`) ONLY after a complete apply pass with zero errors
   for THAT model. The handler calls `store.RequireSoulAgentMintConversationGSI4BackfillComplete`
   first and fails closed with an explicit error naming the tool if the marker is absent.

Tests: `..._FailsClosedWhenBackfillIncomplete` (marker missing → 500, list query never runs) and
`..._FailsClosedWhenGSIQueryFails` (index absent → 500). The reworked
`..._SortsNewestFirst` test asserts the query shape (Index gsi4, gsi4PK partition, OrderBy gsi4SK
DESC, Limit) AND the selection semantics: the seeded page's SK token order is reversed against
recency, so the response can only contain the genuinely newest conversations when the query really
went through the index.

## Operator backfill tool (extended, not renamed)

`scripts/soul-agent-identity-gsi3-backfill` now covers **both** models in one run (the name is
deliberately kept so the #1069 deploy-notes invocation `go run
./scripts/soul-agent-identity-gsi3-backfill --profile <p> --stage <s> --apply` keeps working; the
README documents the dual-model semantics):

- `backfillPlans()` = SoulAgentIdentity (gsi3) + SoulAgentMintConversation (gsi4). One bounded
  scan routes each item to its plan by `SK` prefix (`SK = IDENTITY` vs
  `begins_with(SK, "MINT_CONVERSATION#")`); the projection is the union of both plans' key inputs.
- All C1 properties hold for the new model: dry-run default; conditional `attribute_not_exists`
  writes for absent keys (condition failure ⇒ already-correct); stale-key repair conditioned on the
  OBSERVED stale values with ANY failure counted as an error (marker withheld); mode/stage/table
  binding; throttle; per-model count report.
- **Per-model checkpoint state**: the resume checkpoint is now versioned (v2) with a per-model
  counter map; a v1 (identity-only flat) checkpoint is refused on resume.
- **Per-model completeness marker**: each model's marker is written only after a complete
  error-free apply pass for THAT model — a clean identity pass certifies the identity marker even
  while a mint-conversation error withholds the mint marker (tested).
- The mint plan computes `gsi4PK`/`gsi4SK` via the same `models.SoulMintConversationGSI4PK/GSI4SK`
  helpers as live writes (byte-identical keys); a missing/unparseable stored `createdAt` is a
  classify error (marker withheld) — never a guessed key.
- Preflight now requires **both** gsi3 and gsi4 present and `ACTIVE` (one GSI per deploy; run the
  tool once after the C2 stack update).

## Deploy sequence (operator)

1. Deploy the stack update (this PR) — creates gsi4 (one GSI per deploy).
2. Run the backfill: `go run ./scripts/soul-agent-identity-gsi3-backfill --profile <p> --stage <s> --apply`
   (dry-run first; checkpoint/resume on interruption; BOTH markers written on a complete
   error-free pass; a model with errors withholds only its own marker).
3. The operator mint-conversation list answers from gsi4 with bounded reads, correctly
   recency-ordered; before step 2 it fails closed with the explicit backfill error.

## Non-goals (explicit)

- Part B (19 partition reads, incl. `internal/store/soul_mint_conversation.go:100`) — separate
  delegation.
- No other model/index work; no changes to C1's shipped behavior (identity GSI, its consumers, its
  marker).
- No deploy/merge/cloud mutation by this PR.
