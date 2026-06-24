# ADR 0008: AppTheory MicroVM hosted-genesis exploration

- Status: Proposed exploration
- Date: 2026-06-24
- Related issues: #766, #767

## Context

Project 49 made Lesser's hosted-genesis path JSON-authoritative, but the current implementation still places SQS and an
AI-worker turn processor on the user-visible critical path. Issue #766 scopes the replacement direction: Host owns a
DynamoDB-backed `HostedGenesisSession`, DynamoDB remains the recovery source of truth, and Lambda MicroVMs provide only
the stateful per-session execution context. SQS may survive as telemetry, janitor, or background work, but not as the
source of truth for resume/recovery.

AppTheory v1.14.0 introduces Lambda MicroVM CDK and runtime APIs. This ADR records how Host should map those APIs before
any deploying implementation is attempted. This PR is non-deploying: it updates dependency pins, compiles a bounded API
mapping, and documents the design. It does not create MicroVMs, mutate AWS, touch SSM/Secrets Manager, rotate keys, or
edit sibling repos.

## AppTheory v1.14.0 APIs discovered

### CDK deployment surfaces

Host can consume these TypeScript constructs from `@theory-cloud/apptheory-cdk` v1.14.0:

- `AppTheoryMicrovmNetworkConnector`
  - CloudFormation surface for `AWS::Lambda::NetworkConnector`.
  - Requires caller-provided VPC, subnets, and security groups; AppTheory does not choose a default VPC boundary.
  - Exposes `networkConnectorArn` for image/controller wiring.
- `AppTheoryMicrovmImage`
  - CloudFormation surface for `AWS::Lambda::MicrovmImage`.
  - Requires `baseImageArn`, `baseImageVersion`, `buildRoleArn`, `codeArtifact`, egress connectors, hook config,
    logging config, and one resource requirement.
  - Models image hooks (`ready`, `validate`) and runtime hooks (`run`, `suspend`, `resume`, `terminate`).
- `AppTheoryMicrovmController`
  - Creates a protected HTTP API with routes:
    - `POST /microvms`
    - `POST /microvms/{session_id}/start`
    - `POST /microvms/{session_id}/stop`
    - `GET /microvms/{session_id}/status`
    - `GET /microvms/{session_id}`
  - Creates a controller Lambda and a TableTheory-shaped session registry table with `pk`, `sk`, and `ttl`.
  - Fails closed without a Lambda request authorizer.
  - Wires reserved environment variables including `APPTHEORY_MICROVM_CONTRACT_NAME`,
    `APPTHEORY_MICROVM_CONTRACT_VERSION`, `APPTHEORY_MICROVM_SESSION_REGISTRY_TABLE`,
    `APPTHEORY_MICROVM_IMAGE_REF`, and `APPTHEORY_MICROVM_NETWORK_CONNECTOR_REFS`.
  - Grants MicroVM control-plane actions scoped to the configured image plus network-connector pass-through.

The compile-only mapping in `cdk/lib/hosted-genesis-microvm-exploration.ts` imports these v1.14.0 prop types and records
which properties Host expects to use. It intentionally creates no constructs and is not imported by the live stack.

### Go runtime surfaces

Host can consume these Go APIs from `github.com/theory-cloud/apptheory/runtime/microvm` v1.14.0:

- `ControllerRequest` / `ControllerResponse` with commands `create`, `start`, `stop`, `status`, and `session`.
- `AuthContext` and `SessionSpec` for sanitized, tenant-bound controller envelopes.
- `DefaultControllerContract`, `DefaultSessionRegistryContract`, and `ValidateEscapeHatches` for contract checks.
- `SessionRecord`, `SessionStatus`, `SessionRegistryRecord`, and `TableTheorySessionRegistry` for AppTheory's
  MicroVM execution registry shape.
- `RegistryClient` and `Client` for a constrained controller client abstraction.
- `LifecycleAdapter` with hooks `prepare_image`, `start`, `readiness`, `stop`, `teardown`, and `failure`, and states
  from `requested` through `ready`, `terminated`, or `failed`.
- `testkit/microvm.FakeClient` for deterministic non-AWS controller tests.

The compile-safe scaffold in `internal/hostedgenesis/microvm_exploration.go` builds sanitized AppTheory controller
requests from Host ids only. It proves the mapping against the AppTheory controller/testkit without AWS calls.

## Host mapping decision

### Source-of-truth model

Host must add a first-class `HostedGenesisSession` model in its own DynamoDB state before any real MicroVM-backed
implementation. That model is authoritative for:

- `instance_slug`, `registration_id`, `agent_id`, `conversation_id`, active turn id, and owner boundary;
- expected-version transitions and idempotency request hashes;
- command ledger and current server-authored next action;
- compact transcript/checkpoint references and declaration checkpoints;
- MicroVM execution reference (`microvm_session_id`, endpoint token reference, lifecycle state, image version);
- typed failure category and recovery action; and
- terminal publish/finalize eligibility.

AppTheory's MicroVM session registry is useful controller state and execution cache, but it is not Host's recovery
source of truth. Host reconciles from `HostedGenesisSession` first, then issues or skips MicroVM commands.

### Tenant and auth mapping

- AppTheory `tenant_id`: `slug:<instance_slug>`.
- AppTheory `namespace`: `hosted-genesis`.
- AppTheory `session_id`: Host `conversation_id` unless a later migration requires a separate opaque execution id.
- AppTheory `auth_context.subject`: sanitized service subject (`lesser-host:hosted-genesis`), not a bearer token.
- AppTheory `session_spec.metadata`: safe ids only (`source_of_truth`, `registration_id`, `agent_id`,
  `conversation_id`, optional `turn_id`). No raw transcript, prompt, Instance API key, wallet signature, AWS credential,
  provider key, SSM value, or raw lifecycle payload crosses this boundary.

### Host API surface for the real implementation

The existing Lesser-facing hosted-genesis routes should remain the public compatibility surface, but their internals
should call a `HostedGenesisSession` orchestrator rather than enqueueing critical SQS work:

- `POST /api/v1/soul/instance/agents/register/{id}/mint-conversation`
  - Commit or load `HostedGenesisSession` with expected-version/idempotency semantics.
  - Create/start/resume the MicroVM execution context only after the DynamoDB session checkpoint exists.
  - Return the compact HostConversation envelope from DynamoDB.
- `GET /api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}`
  - Read `HostedGenesisSession` and return the server-authored status/next action.
- `POST /api/v1/soul/instance/agents/register/{id}/mint-conversation/{conversationId}/complete`
  - Advance declaration extraction or return terminal declaration readiness from DynamoDB.
- Recovery/admin/internal follow-ups:
  - `resume/recover`: server-authored action from `HostedGenesisSession` chooses resume existing MicroVM, replay an
    idempotent command, restart from a checkpoint, or terminal operator action.
  - `terminate`: stop/terminate MicroVM execution while keeping terminal Host evidence.
  - `publish/finalize`: still fails closed unless DynamoDB has a valid terminal declaration checkpoint.

### Failure and recovery matrix

| Failure | Authoritative state | Recovery action |
| --- | --- | --- |
| Host process crash | `HostedGenesisSession` expected version + command ledger | Reload session, inspect last committed command, resume/replay idempotently. |
| MicroVM suspended mid-turn | `HostedGenesisSession` nonterminal action + MicroVM lifecycle ref | Resume MicroVM if within TTL; otherwise restart from the last Host checkpoint. |
| MicroVM terminated/expired | Host checkpoint/declaration state | Start a new MicroVM execution session from checkpoint or mark `operator_action` if checkpoint is insufficient. |
| Duplicate Lesser retry | Idempotency row + request hash | Return existing conversation/status without another debit or duplicate user turn. |
| Stale Lesser/Sim state | Host status read | Caller must accept server-authored next action; local UI state is not recovery authority. |
| Deploy during active session | Host session image/version fields | Continue old image when safe, or suspend/restart from checkpoint with explicit version transition. |
| LLM/provider failure | Host typed failure category | `retry_same_step` when safe; otherwise `operator_action` or bounded restart. |
| Publish without declaration checkpoint | Host terminal declaration fields | Fail closed; publish/finalize remains forbidden. |

## Consumer release verification and provisioning impact

This exploration does not modify managed-instance provisioning, managed-update, or consumer release verification.
Future MicroVM deployment work must preserve per-slug AWS account isolation and checksum-based release verification. If
MicroVM images become managed release assets, they must enter the `managed-release-certification` / `managed-release-readiness`
path before provisioning or managed update consumes them.

## Framework-feedback signal

### Target framework

AppTheory CDK and AppTheory Go runtime, v1.14.0.

### Concern under Host constraints

Host needs a MicroVM controller for a governance-sensitive, multi-tenant hosted-genesis workflow where Host's own
DynamoDB model is authoritative and MicroVM state is execution/cache only.

### Idiomatic shape Host wants

```go
session := loadHostedGenesisSessionFromHostDynamoDB(...)
request := microvm.ControllerRequest{ /* tenant-bound ids only */ }
response, err := controller.Handle(ctx, request) // backed by an AppTheory AWS MicroVM client adapter
recordMicroVMExecutionRef(session, response)
```

```ts
new AppTheoryMicrovmController(this, "HostedGenesisMicrovmController", {
  controller,
  authorizer,
  microvmImage,
  egressNetworkConnectors,
  sessionRegistry: importedOrExistingHostScopedRegistry,
});
```

### Current gap / risk

- The CDK controller construct creates its own MicroVM session registry table. Host can treat that as controller cache,
  but a future implementation needs an explicit pattern for importing or delegating registry state without confusing it
  with Host's authoritative `HostedGenesisSession` table.
- The Go runtime exposes a constrained `Client` interface and a registry-backed test/client path, but this exploration did
  not find a concrete AWS Lambda MicroVM client adapter for `RunMicrovm`, `ResumeMicrovm`, auth-token creation, suspend,
  and terminate. If Host implements that adapter directly, it must avoid turning raw AWS SDK access into an escape hatch.
- The runtime controller vocabulary has `create/start/stop/status/session`; Host's user-facing recovery model also needs
  `resume/recover`, `publish/finalize`, and checkpoint replay. Those should stay Host-owned, but the boundary should be
  documented upstream so consumers do not overload MicroVM controller state as workflow state.

### Proposed next step

Ask the AppTheory steward to scope: imported/external session-registry support or a documented controller-cache pattern;
a concrete constrained AWS MicroVM client adapter; and guidance for workflow-owned recovery models layered over MicroVM
execution sessions. Host should not patch or vendor AppTheory locally.

## Risks and blockers before implementation

- AWS Lambda MicroVM APIs are new and may not be available in every deploy region/account context Host uses.
- Host needs a lab-only canary plan that proves suspend/resume and duplicate retry before any live rollout.
- MicroVM image build inputs (`baseImageArn`, build role, code artifact, lifecycle hooks) require an operator-owned
  deployment design; this PR intentionally does not create them.
- The authorizer for `AppTheoryMicrovmController` must fail closed and must not accept raw Instance API keys or Host
  control-plane sessions directly from Lesser/Sim.
- MicroVM endpoints and auth tokens must never be returned to browsers or sibling repos; Host proxies/mediates through
  server-authored actions.
- Existing `SOUL_REG` / `MINT_CONVERSATION` rows need a migration plan that preserves early `conversation_id`
  persistence and maps pre-queue `in_progress` rows to a deterministic recovery action.

## Validation plan for this exploration

- Dependency pin proof: Go `github.com/theory-cloud/apptheory` and CDK `@theory-cloud/apptheory-cdk` move to v1.14.0.
- Compile proof: Go tests validate AppTheory MicroVM contracts and the sanitized controller envelope.
- CDK proof: TypeScript build imports v1.14.0 MicroVM prop types without adding stack resources; synth remains
  non-deploying.
- Guard proof: Existing hosted-genesis/template/custom-domain guards still pass against synthesized templates.
