# ADR 0010: Hosted Genesis conversation-lifetime AppTheory MicroVM actor

## Status

Accepted implementation contract for Project 48 M11. Exact-head lab proof of the complete typed-candidate workflow
remains an operator-owned follow-up; process-memory preservation is not a correctness dependency.

## Date

2026-07-19

## Related issues

Project 48 M11, parent #940, ADR issue #941, implementation issue #977. Dependencies #959/#976, #972/#973,
#957/#958, and historical extractor hardening #974/#975.

## Source gate

This decision was made after inspecting the active Host staging baseline and the pinned AppTheory v1.17.0 surface:

- `go.mod` / `go.sum` pin `github.com/theory-cloud/apptheory v1.17.0`;
- `go.mod` / `go.sum` pin `github.com/theory-cloud/tabletheory/v2 v2.0.3`;
- AppTheory docs/source:
  - `docs/cdk/lambda-microvm.md`;
  - `runtime/microvm/controller.go`;
  - `runtime/microvm/provider.go`;
  - `runtime/microvm/provider_invoke.go`;
  - `runtime/microvm/session.go`;
  - `runtime/microvm/operation_contract.go`;
  - `runtime/microvm/controller_contract.go`;
  - `runtime/microvm/lifecycle.go`;
  - the exact controller/provider/invoke/lifecycle/registry tests shipped with v1.17.0;
  - MicroVM foundation and operations fixtures under `contract-tests/fixtures/microvm-*`;
- Host docs:
  - `docs/adr/0008-app-theory-microvm-hosted-genesis-exploration.md`;
  - `docs/adr/0009-app-theory-microvm-lab-gates.md`;
  - `docs/hosted-genesis-microvm-lab-canary.md`;
  - `docs/contracts/hosted-genesis-conversation.md`;
- Host implementation surfaces:
  - `internal/hostedgenesis/microvm_exploration.go`;
  - `internal/hostedgenesis/microvm_controller.go`;
  - `internal/hostedgenesis/dispatch.go`;
  - `internal/hostedgenesis/microvm_turn.go`;
  - `internal/store/models/hosted_genesis_session.go`;
  - `internal/store/hosted_genesis_sessions.go`;
  - `internal/aiworker/hosted_genesis_microvm.go`;
  - `internal/controlplane/hosted_genesis_microvm_dispatch.go`;
  - `cmd/hosted-genesis-microvm-workload/*`;
- TableTheory v2.0.3 source/tests:
  - transaction builders and `pkg/core` transaction conditions;
  - optimistic `AtVersion`, `IfExists`, and field `Condition` APIs;
  - fake transaction and version/condition tests;
- current official provider SDK documentation:
  - OpenAI Agents SDK overview: <https://openai.github.io/openai-agents-python/>;
  - OpenAI Agents SDK sessions: <https://openai.github.io/openai-agents-python/sessions/>;
  - OpenAI Agents SDK tracing: <https://openai.github.io/openai-agents-python/tracing/>;
  - Claude Agent SDK overview: <https://code.claude.com/docs/en/agent-sdk/overview>;
  - Claude Agent SDK sessions: <https://code.claude.com/docs/en/agent-sdk/sessions>;
  - Claude Agent SDK hosting: <https://code.claude.com/docs/en/agent-sdk/hosting>;
  - Claude Agent SDK observability: <https://code.claude.com/docs/en/agent-sdk/observability>;
  - Claude Agent SDK session storage: <https://code.claude.com/docs/en/agent-sdk/session-storage>.

The AppTheory source gate matters because Host is dogfooding the AppTheory MicroVM substrate. Host must not recreate a
per-turn orchestrator, fork AppTheory, call raw AWS Lambda MicroVM SDKs directly, or invent a parallel MicroVM provider.

## Context

The current staging baseline already moved Hosted Genesis onto AppTheory MicroVM infrastructure, but the AI-worker
dispatch path still starts a fresh AppTheory MicroVM run for each queued turn because earlier milestones had not proven
safe human-gap reuse. Project 48 M11 changes the target shape: one Hosted Genesis conversation is serviced by one
conversation-lifetime MicroVM actor, or by explicit checkpoint/relaunch/replay semantics that preserve that actor
contract when the provider VM dies, expires, or fails to preserve process memory.

The durable Host row remains `HostedGenesisSession`. Host's AppTheory registry/cache state remains reconstructible
execution state through `HostedGenesisMicroVMExecution` and `store.NewHostedGenesisMicroVMRegistry`. Follow-on
implementation issues must preserve the recent staging invariants from #937 and #939:

- in-flight nudges are wait-only and do not append turns, debit, or dispatch a second MicroVM;
- retry state is persisted before dispatch;
- dispatch failures become loud typed failures;
- there is no synchronous control-plane LLM fallback.

## Decision 1: conversation lifetime and identity

The Hosted Genesis conversation actor is an AppTheory MicroVM session whose `session_id` is the Host
`conversation_id`.

| Boundary | Locked value / owner |
| --- | --- |
| AppTheory `tenant_id` | `slug:<instance_slug>` |
| AppTheory `namespace` | `hosted-genesis` |
| AppTheory `session_id` | `conversation_id` |
| Durable business truth | Host `HostedGenesisSession` |
| Operational cache | AppTheory registry through Host-owned `HostedGenesisMicroVMExecution` |
| Provider conversation loop | In-VM runtime |

The first accepted user turn launches or resumes the conversation MicroVM only after Host has durably committed the
`HostedGenesisSession`, idempotency ledger entry, credit/debit decision, and latest turn id. The AppTheory session id
must remain the same `conversation_id` for the lifetime of that conversation. A relaunch after expiry/death may produce
a new provider MicroVM id, but it must reuse the same AppTheory `session_id=conversation_id` and Host tenant/namespace
binding.

Host is control/gateway/observer and durable source of truth. It authorizes the caller, binds the tenant, accepts or
rejects user turns, enforces billing/debit policy, records status/version/checkpoint transitions, publishes governed
declarations, and terminates or recovers MicroVM sessions. Host does **not** own the model/provider conversation step
machine.

The in-VM runtime owns the provider conversation step machine: provider SDK session/trace, phase-local typed tool
construction, validation repair, deterministic review/finalization decisions, and safe checkpoint metadata. The runtime is invoked through
AppTheory's controller `Invoke` route (`/microvms/{session_id}/invoke/hosted-genesis/turn`) and never through a
Host-owned raw endpoint bridge.

The only MicroVM substrate is AppTheory's controller/provider/session API and safe registry: `Run`, `Get`, `Invoke`,
`Suspend`, `Resume`, `Terminate`, and tenant-bound registry/reconstruction. `List` remains acceptable only as
AppTheory's tenant-bound recovery/list operation when a recovery tool needs it; it is not a product conversation source
of truth. Raw AWS Lambda MicroVM SDK clients, local provider forks, route-local memory registries, and AppTheory
vendoring are forbidden.

### Launch, resume, and terminate semantics

1. **Launch**: for a committed first turn with no live AppTheory session, Host calls `Run` with
   `session_id=conversation_id`, safe metadata ids only, and the configured `MaximumDurationSeconds`, with no
   `ProviderIdlePolicy`. Host records the returned lifecycle ref only after validating tenant, namespace, session id, lifecycle
   state, and registry version.
2. **Resume**: for a later accepted turn, Host first reads `HostedGenesisSession`. If the lifecycle ref is present and
   not terminal/expired, Host observes with AppTheory `Get`, then `Resume` if suspended, then `Invoke` the in-VM turn
   endpoint. If `Get` reports terminal/failed/expired, Host relaunches from the last safe checkpoint instead of
   treating stale process state as recoverable truth.
3. **Human wait**: when the runtime reaches `assistant_turn_ready` or `declaration_ready`, durable candidate/session
   state is complete before the invocation returns. Endpoint-idle suspension is not used as an actor-work signal.
4. **Terminate**: Host terminates the AppTheory session when the conversation is terminal (`declaration_ready` after
   publish/finalize, terminal `failed` with no retry action), when an operator explicitly ends the run, or when recovery
   determines the MicroVM binding is unsafe. Termination never deletes the Host `HostedGenesisSession` evidence.

## Decision 2: typed candidate, review, affirmation, and human gaps

The durable `DeclarationCandidate` nested in `HostedGenesisSession` is the declaration source of truth. Its binding includes
Managed instance Slug, registration, agent, conversation, source turn, model, schema/guidance versions, phase, completed
sections, typed five bodies and approved satellites, monotonic revision, canonical section/candidate hashes, hashed
provider tool-call identities, bounded provider-attempt evidence, review checkpoint, and affirmation checkpoint.

The AppTheory MicroVM exposes exactly one provider tool for the current phase:

1. `declaration_identity_put`
2. `declaration_philosophy_put`
3. `declaration_discipline_put`
4. `declaration_boundaries_put`
5. `declaration_soul_put`

Provider schemas are affordances, not authority. Each tool result is normalized and validated immediately by Host's
canonical validator. Rejections return bounded `{section,path,code}` issues to the provider and do not mutate the
candidate. Acceptance checkpoints the candidate through TableTheory under exact tenant/session/turn/version/revision/hash
conditions. Duplicate call identities with identical input are idempotent; reuse with different input and stale
revision/hash submissions fail closed.

After all sections are accepted, cross-section validation runs on the stored candidate. Host deterministically renders
the owner review as a stable human header followed by a byte-counted, delimited copy of the exact `CanonicalJSON` bytes.
The block is reversibly extracted byte-for-byte without a provider, so every five-body note, self-description field,
capability, derived boundary, transparency field, refusal, and adversarial-review value authenticated by `CandidateHash`
is inspectable in `ReviewText`. Host checkpoints renderer/schema/guidance versions, source turn, candidate revision/hash,
review hash, text, and timestamp. `CandidateHash` authenticates the canonical JSON while `ReviewHash` separately
authenticates the complete rendered review. Owner authority is structural: `candidate_action=affirm` must match the
reviewed revision, candidate hash, and review hash. `candidate_action=edit` selects a canonical section and invalidates
the previous review and affirmation. Free-form affirmation phrases have no authority.

The fifth tool acceptance does not require a follow-up provider request to produce review prose. The runtime projects
the stored deterministic review directly. If the process or MicroVM stops after the candidate checkpoint but before
the assistant projection commits, recovery renders the same stored review provider-free and advances only the guarded
session/public-conversation projection.

An affirmed candidate enters provider-free finalization. Finalization revalidates the stored candidate and atomically
writes its exact `CanonicalJSON` bytes/hash to `HostedGenesisSession` and the `SoulAgentMintConversation` public projection; no provider request,
new generated id, or retry-time timestamp can alter the owner-affirmed semantic bytes. Repeated finalization preserves
the same bytes/hash and converges on the terminal publication truth established by #972/#973.

AppTheory `ProviderIdlePolicy` is deliberately omitted. AWS Lambda MicroVM endpoint-idle measures inbound endpoint
traffic, not guest CPU, goroutines, or an outbound provider SDK call. Applying endpoint-idle suspension while the actor
continues provider work would suspend valid work. Host instead uses the validated 27,900-second provider HTTP-attempt and
whole-call bounds, 28,200-second detached workload bound, and 28,800-second AppTheory/AWS MicroVM maximum, retaining
guarded persistence and lifecycle-cleanup margins.

Human waits are durable waits, not process-memory waits. The actor checkpoints before returning
`assistant_turn_ready`; a later accepted turn invokes or relaunches the same tenant-bound AppTheory session from
`HostedGenesisSession`. Accepted sections and provider-attempt evidence survive process/VM recovery. Provider failure
retries the current candidate phase/revision without replaying accepted sections. Existing lanes without typed candidate
state return `restart_soul_bootstrap`; Host does not reconstruct them through a transcript extractor.
## Decision 3: failure classes

Follow-on implementation must map these failure classes explicitly and never collapse them into a generic transport
failure:

| Failure class | Detection boundary | Host state / recovery contract |
| --- | --- | --- |
| Provider failure | OpenAI or Anthropic SDK returns an API, model, tool, refusal, timeout, or session error inside the VM. | VM preserves accepted candidate checkpoints, records content-free attempt evidence, and Host writes `assistant_turn_failed` with `retry_same_step` only while the Host-owned budget remains. Recovery resumes the current stored section/revision. |
| VM dead/expired | AppTheory `Get`/`Invoke`/`Resume` reports terminal, expired, not found, failed, or an invalid lifecycle ref. | Host records a loud `microvm_unavailable` failure and relaunches only through AppTheory from the stored typed candidate. The Control plane never calls a provider. |
| Checkpoint conflict | Option A write sees stale Host version, unexpected status, stale checkpoint sequence/ref, or mismatched latest turn. | Host rejects the write. The VM must reload Host state before deciding whether work already committed, a retry is still safe, or operator action is required. It must not overwrite concurrent Host truth. |
| Auth/tenant mismatch | AppTheory tenant/namespace/session binding, Host instance auth, or VM write envelope does not match the `HostedGenesisSession`. | Fail closed as security/tenant mismatch. Do not retry automatically from the mismatched VM. Do not leak the rejected ids beyond hashed audit logs. |
| Section validation failure | A section tool or final cross-section validation rejects the typed candidate. | The provider receives only bounded section/path/code issues and revises the identified section. Invalid provider content is not persisted. A final invariant failure that cannot be repaired fails closed. |

## Decision 4: Option A direct write contract

Option A is accepted: the in-VM runtime may write Host session truth directly, but only through explicit conditional
guards and a tenant/auth envelope. This is not permission for the VM to own all Host business policy.

Every direct write from the VM to Host state must carry:

- authenticated VM/Host service envelope, scoped to `slug:<instance_slug>`, `hosted-genesis`, and
  `session_id=conversation_id`;
- Host ids: `instance_slug`, `registration_id`, `agent_id`, `conversation_id`, and `latest_turn_id` when a turn is in
  scope;
- expected current Host `status`;
- expected Host `version`;
- expected candidate revision/hash/phase and current source turn;
- expected checkpoint sequence/ref/hash when advancing from a previous checkpoint;
- requested transition (`status_from`, `status_to`, failure class, checkpoint metadata ref);
- safe trace/correlation ids only.

Host must validate all tenant/auth/id fields against `HostedGenesisSession` before applying the write. The durable write
must use TableTheory expected-version and expected-status conditions equivalent to `UpdateHostedGenesisSession`, plus a
checkpoint guard for VM-authored progress. A stale version, stale status, stale checkpoint, or mismatched tenant is a
hard conflict.

Retry budgets remain Host-owned. The VM may report a failure and request `retry_same_step`, but Host decides whether a
retry budget exists, persists that retry state before any re-dispatch, and chooses the next recovery action. The VM must
not hide retry counters in provider SDK state or process memory.

For a durable `provider_timeout`, Host first uses AppTheory `Get` / `Terminate` / `Run` / `Get` to prepare one fresh
runtime on the deployment-pinned current image version and execution role while preserving the Host conversation and
accepted turn. Preparation never invokes provider work. Host then atomically records the fresh lifecycle identity with
the TableTheory retry/debit transaction and performs one AppTheory `Invoke`. A failure before that governed dispatch
transaction does not decrement the retry budget or debit credits. The recorded lifecycle identity is content-free:
MicroVM id, image ref/version, execution-role ARN, maximum duration, and runtime log-group destination only.

Host also remains the only authority for billing/debit, idempotency conflict decisions, final publication, Soul registry
mutations, public attestations, and operator/admin actions.

## Decision 5: provider SDK and durable attempt evidence

The MicroVM uses the existing official OpenAI and Anthropic Go SDKs directly with phase-local tools. Both clients pin an
explicit SDK retry budget of two retries (the documented default) and share the validated per-attempt HTTP timeout.
A content-free transport observer records each SDK HTTP attempt's ordinal, configured retry budget, bounded HTTP status
and provider request id when available, duration, and failure class. Tool observations add only tool name, hashed call
identity, accepted flag, and bounded validation codes/paths. Completion observations add output byte count/hash, usage,
stop reason, and duration. Raw prompts, transcripts, tool arguments/results, provider responses, auth headers, and keys
never enter state or logs.

These operational records are bounded and stored alongside the candidate but excluded from `CanonicalJSON` and
`CandidateHash`; telemetry and retry timing cannot change reviewed bytes. The provider SDK/tool loop remains inside the
AppTheory MicroVM. TableTheory remains the only durable mutation path. No raw AWS SDK, custom persistence layer, local
AppTheory substitute, or Control plane provider call is permitted.
## Lab evidence status

Existing Host lab evidence proves the runnable actor, content-free provider telemetry, terminal publication
convergence, no-legacy invariant, and reviewed integration lineage. The inspected materials show:

- `docs/hosted-genesis-microvm-lab-canary.md` documents the required lab/live AppTheory MicroVM path and the existing
  non-deploying canary;
- `scripts/hosted-genesis-microvm-e2e-gate.sh` describes happy path, kill-VM recovery, and
  `MaximumDurationSeconds` wiring;
- `docs/roadmap-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md` records a prior lab run proving happy-path
  Hosted Genesis status/read behavior and kill-VM recovery failure/retry behavior.

Exact-head lab proof for #977 still must exercise a fresh typed-candidate lane, forced same-section repair, review/hash
binding, zero-provider post-affirmation finalization, publication, and Ptah read/list truth. That proof is rollout
evidence, not a reason to reintroduce endpoint-idle suspension or make process memory a correctness dependency.

## Consequences

- `DeclarationCandidate` and guarded TableTheory transactions are the source of truth; transcript prose is not.
- Each phase tool payload is structurally bound to the current candidate revision/hash and rejects unknown fields before mutation.
- The production whole-transcript extractor, extraction target states, phrase-only affirmation decision, and
  compatibility paths are removed. Old lanes restart.
- OpenAI and Anthropic expose the same five phase tools; provider-schema relaxation never relaxes Host validation.
- Finalization and repeated terminal convergence are deterministic and provider-free.
- AppTheory owns MicroVM lifecycle and invocation; TableTheory owns durable guarded state. Framework awkwardness is
  routed upstream rather than patched with local substitutes.
- The Control plane remains auth, tenant, debit/idempotency, status/projection, and publication authority.
- Body/Ptah mirrors the structural candidate/review/action response contract through lesser-body#452; that sibling work
  is not implemented here.
- Lab then live rollout remains operator-owned. This ADR grants no deployment, publication, signing, or merge authority.
