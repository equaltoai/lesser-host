# ADR 0010: Hosted Genesis conversation-lifetime AppTheory MicroVM actor

## Status

Accepted design contract for Project 48 M11 implementation, with #941 acceptance still blocked on live lab evidence that
proves or disproves AWS Lambda MicroVM process-memory preservation across a human-scale suspend/resume gap.

## Date

2026-07-19

## Related issues

Project 48 M11, parent #940, child #941. Follow-on implementation issues #942 and #943 consume this contract.

## Source gate

This decision was made after inspecting the active Host staging baseline and the pinned AppTheory v1.17.0 surface:

- `go.mod` / `go.sum` pin `github.com/theory-cloud/apptheory v1.17.0`;
- AppTheory docs/source:
  - `docs/cdk/lambda-microvm.md`;
  - `runtime/microvm/controller.go`;
  - `runtime/microvm/provider.go`;
  - `runtime/microvm/provider_invoke.go`;
  - `runtime/microvm/session.go`;
  - `runtime/microvm/operation_contract.go`;
  - `runtime/microvm/controller_contract.go`;
  - `runtime/microvm/lifecycle.go`;
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

The in-VM runtime owns the provider conversation step machine: provider SDK session/trace, turn sequencing,
ask/wait/revise/extract/finalize/fail decisions, and safe checkpoint metadata. The runtime is invoked through
AppTheory's controller `Invoke` route (`/microvms/{session_id}/invoke/hosted-genesis/turn`) and never through a
Host-owned raw endpoint bridge.

The only MicroVM substrate is AppTheory's controller/provider/session API and safe registry: `Run`, `Get`, `Invoke`,
`Suspend`, `Resume`, `Terminate`, and tenant-bound registry/reconstruction. `List` remains acceptable only as
AppTheory's tenant-bound recovery/list operation when a recovery tool needs it; it is not a product conversation source
of truth. Raw AWS Lambda MicroVM SDK clients, local provider forks, route-local memory registries, and AppTheory
vendoring are forbidden.

### Launch, resume, and terminate semantics

1. **Launch**: for a committed first turn with no live AppTheory session, Host calls `Run` with
   `session_id=conversation_id`, safe metadata ids only, the configured `MaximumDurationSeconds`, and the configured
   `IdlePolicy`. Host records the returned lifecycle ref only after validating tenant, namespace, session id, lifecycle
   state, and registry version.
2. **Resume**: for a later accepted turn, Host first reads `HostedGenesisSession`. If the lifecycle ref is present and
   not terminal/expired, Host observes with AppTheory `Get`, then `Resume` if suspended, then `Invoke` the in-VM turn
   endpoint. If `Get` reports terminal/failed/expired, Host relaunches from the last safe checkpoint instead of
   treating stale process state as recoverable truth.
3. **Suspend**: when the in-VM runtime reaches a human wait (`assistant_turn_ready`) or a terminal-ish Host wait
   (`declaration_ready` awaiting explicit publish/finalize), it must write safe checkpoint metadata before Host asks
   AppTheory to `Suspend` or lets the provider `IdlePolicy` suspend the VM.
4. **Terminate**: Host terminates the AppTheory session when the conversation is terminal (`declaration_ready` after
   publish/finalize, terminal `failed` with no retry action), when an operator explicitly ends the run, or when recovery
   determines the MicroVM binding is unsafe. Termination never deletes the Host `HostedGenesisSession` evidence.

## Decision 2: human-gap behavior

`MaximumDurationSeconds` is an active-run safety ceiling, not the human wait budget. It must be sized for the longest
single in-VM provider turn plus in-VM declaration extraction and bounded cleanup/checkpoint work. It must not be
increased merely to keep a process alive while waiting for a human.

`IdlePolicy` is the human-gap control. Follow-on implementation must pass an explicit AppTheory
`ProviderIdlePolicy` on `Run` once the deployed configuration values are chosen:

- `MaxIdleDurationSeconds`: the maximum ready-idle interval before the provider may suspend the VM;
- `SuspendedDurationSeconds`: the maximum suspended interval Host expects to support before checkpoint/relaunch/replay
  becomes the normal path;
- `AutoResumeEnabled`: may be true only if the provider's resume semantics and Host's auth/tenant envelope remain
  explicit and observable through AppTheory `Get` / `Resume` / `Invoke`.

The configured idle values are deployment policy, not business truth. They must be shorter than the Host session TTL and
long enough to cover the operator-approved lab human-gap canary. If provider limits force shorter durations than real
human conversations, the actor contract still holds through checkpoint/relaunch/replay.

### Checkpoint / relaunch / replay expectations

Host and the in-VM runtime must assume process memory is an optimization, not a recovery source. Before any human wait,
suspend, or provider SDK session handoff, the in-VM runtime writes a safe checkpoint transition under the Option A write
contract below. A safe checkpoint contains ids and bounded metadata only:

- Host ids: `instance_slug`, `registration_id`, `agent_id`, `conversation_id`, `latest_turn_id`;
- state-machine marker: `step`, `status_from`, `status_to`, checkpoint sequence/ref/hash, runtime image/version;
- provider metadata: provider family, model id, provider SDK session id if any, trace/span/request ids if safe;
- recovery metadata: last completed durable turn, retry budget observed from Host, and failure class when applicable.

No checkpoint may carry raw prompts, raw transcripts, message lists, provider secrets, bearer tokens, Instance API keys,
SSM values, AWS credentials, wallet signatures, MicroVM endpoint auth tokens, or raw provider SDK payloads.

If resume preserves process memory, the runtime may continue as a fast path after revalidating Host status/version and
checkpoint sequence. If resume does **not** preserve process memory, or if the VM is dead/expired, a new provider
MicroVM instance relaunches under the same AppTheory `session_id=conversation_id`, loads the latest safe checkpoint, and
replays or continues from that checkpoint. Replaying must be idempotent with respect to Host's turn ledger,
debit/idempotency rows, declaration checkpoints, and provider-tool side effects.

### Known unknown: process memory preservation

No inspected Host evidence proves that AWS Lambda MicroVM suspend/resume preserves process memory across a human-scale
idle interval. The existing canary and roadmap evidence prove local AppTheory M16 route/operation coverage, configured
`MaximumDurationSeconds` wiring, metadata-only reads, and kill-VM recovery behavior, but not process-memory survival
through suspend/resume. Until the live lab canary in `docs/hosted-genesis-microvm-lab-canary.md` records that proof,
#942/#943 must implement checkpoint/relaunch/replay as the correctness path and treat memory preservation as a possible
optimization only.

## Decision 3: failure classes

Follow-on implementation must map these failure classes explicitly and never collapse them into a generic transport
failure:

| Failure class | Detection boundary | Host state / recovery contract |
| --- | --- | --- |
| Provider failure | Provider SDK/direct call returns an API, model, context-window, tool, refusal, timeout, trace, or session error inside the VM. | VM writes a safe failed checkpoint request. Host records `failed` with the existing typed failure surface (`assistant_turn_failed`, `declaration_extraction_failed`, `llm_unavailable`, or a narrowed successor) and `retry_same_step` only when Host-owned retry budget remains. |
| VM dead/expired | AppTheory `Get`/`Invoke`/`Resume` returns terminal, expired, not found, failed, or cannot validate the lifecycle ref. | Host records or keeps a loud MicroVM-unavailable failure, then relaunches from safe checkpoint only through the existing recovery seam. No sync LLM fallback. |
| Checkpoint conflict | Option A write sees stale Host version, unexpected status, stale checkpoint sequence/ref, or mismatched latest turn. | Host rejects the write. The VM must reload Host state before deciding whether work already committed, a retry is still safe, or operator action is required. It must not overwrite concurrent Host truth. |
| Auth/tenant mismatch | AppTheory tenant/namespace/session binding, Host instance auth, or VM write envelope does not match the `HostedGenesisSession`. | Fail closed as security/tenant mismatch. Do not retry automatically from the mismatched VM. Do not leak the rejected ids beyond hashed audit logs. |
| Declaration-validation failure | In-VM extraction produces missing/invalid declarations or Host declaration validator rejects stable field codes. | Host records `failed` with bounded stable validation reason and `restart_soul_bootstrap` or `operator_action` according to the existing contract. Raw model output stays out of Host state and logs. |

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

Host also remains the only authority for billing/debit, idempotency conflict decisions, final publication, Soul registry
mutations, public attestations, and operator/admin actions.

## Decision 5: provider SDK decision gate for #942

#941 does not add provider SDK dependencies. #942 may choose OpenAI Agents SDK, Claude Agent SDK, or direct provider
calls inside the MicroVM only after preserving the AppTheory/Host boundary above.

### Use OpenAI Agents SDK when

Use OpenAI Agents SDK when the in-VM runtime is using OpenAI models and benefits from SDK-owned agent loop primitives:
session memory, tool invocation, handoffs/guardrails, streaming, and default tracing. The Host mapping must set or
record:

- Host `conversation_id` as the SDK session key when the selected session backend supports that safely, or as the trace
  `group_id` when the SDK allocates an internal session id;
- OpenAI trace/workflow ids with workflow name `hosted-genesis` or a more specific safe successor;
- model id, model request id, provider SDK session id, trace id/group id, latest turn id, checkpoint ref/sequence, and
  failure class back into Host safe checkpoint metadata.

Do not use an in-memory-only OpenAI session backend as the correctness path. Use a durable backend, encrypted/TTL session
store, or direct checkpoint mapping when the MicroVM can relaunch.

### Use Claude Agent SDK when

Use Claude Agent SDK when the in-VM runtime is using Claude and needs Claude Code's SDK-owned agent loop, tool/context
handling, long-running streaming session, or Claude-specific session resume/fork behavior. The Host mapping must account
for the Claude SDK hosting model:

- one SDK session maps to a spawned `claude` subprocess;
- local transcripts and working artifacts are process/container filesystem state unless a `SessionStore` mirror is
  configured;
- serverless or multi-host relaunch requires a durable `SessionStore` or an equivalent Host-safe checkpoint/replay
  design;
- telemetry is opt-in through OpenTelemetry environment variables and must map only safe trace/session ids back to Host.

If the Claude SDK is used in the MicroVM, #942 must pin where `CLAUDE_CONFIG_DIR`, working directory, and session-store
keys live inside the VM, and how their safe session ids map to Host `conversation_id` without writing raw transcripts or
provider secrets into Host state.

### Use direct provider calls when

Use direct provider API calls inside the VM when Hosted Genesis only needs the narrow ask/wait/revise/extract/finalize
loop and an SDK-managed loop/session/trace would duplicate or obscure the VM's own state machine. Direct calls remain
inside the VM; Host still must not reintroduce a per-turn provider orchestrator. The same safe checkpoint, telemetry,
failure-class, and Option A write contracts apply.

### Required telemetry/session/recovery mapping

Whichever provider path #942 chooses, the VM must map these fields back to Host as safe metadata:

- provider family and provider model id;
- provider SDK name/version when applicable;
- provider SDK session id when safe;
- trace id/span id/group id/workflow name when safe;
- provider request id when safe;
- Host `conversation_id`, `latest_turn_id`, checkpoint ref/sequence/hash, and runtime image/version;
- `status_from`, `status_to`, failure class, recovery request, and Host-owned retry budget observed/remaining.

The mapping must never include raw transcript content, raw provider payloads, API keys, bearer tokens, Instance API keys,
wallet/signing material, SSM parameter values, AWS credentials, MicroVM endpoint auth tokens, or full signed transaction
bodies.

## Lab evidence status

Existing Host lab evidence is insufficient for #941 closure. The inspected materials show:

- `docs/hosted-genesis-microvm-lab-canary.md` documents the required lab/live AppTheory MicroVM path and the existing
  non-deploying canary;
- `scripts/hosted-genesis-microvm-e2e-gate.sh` describes happy path, kill-VM recovery, and
  `MaximumDurationSeconds` wiring;
- `docs/roadmap-hosted-offchain-reads-and-mainnet-soul-2026-07-09.md` records a prior lab run proving happy-path
  Hosted Genesis status/read behavior and kill-VM recovery failure/retry behavior.

None of those prove whether process memory survives AppTheory/AWS Lambda MicroVM suspend/resume over a human-scale idle
gap. Therefore this ADR locks the contract for #942/#943 but does not satisfy #941 acceptance until the lab canary
records that proof.

## Consequences

- #942 must move the provider conversation step machine into the MicroVM runtime and must not add another Host-side
  per-turn orchestrator.
- #943 must invert Host's control-plane dispatch to launch/resume/observe the conversation actor, not run one fresh
  MicroVM per user turn as the correctness path.
- Checkpoint/relaunch/replay is mandatory until live lab evidence proves process memory can be safely used.
- Missing AppTheory actor conveniences are framework-feedback signal, not permission to fork AppTheory or build a raw
  Lambda MicroVM substitute in Host.
- No provider SDK dependency is justified by this ADR alone; any #942 dependency must be narrow, in-VM, and mapped to
  the telemetry/session/recovery contract above.
