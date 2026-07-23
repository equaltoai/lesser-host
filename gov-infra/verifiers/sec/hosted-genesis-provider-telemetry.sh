#!/usr/bin/env bash
# SEC-16: Hosted-genesis provider telemetry and terminal convergence stay wired.
#
# The hosted-genesis MicroVM must construct one typed declaration section at a
# time through the official OpenAI/Anthropic SDKs, checkpoint accepted candidate
# state and content-free provider-attempt evidence through guarded TableTheory
# writes, emit provider-call heartbeats while a call is idle, and durably fail
# the accepted turn when the provider times out or AppTheory reports a
# suspended/terminal/expired MicroVM. Lossless exact-candidate review,
# structural affirmation, and exact provider-free finalization replace every
# whole-transcript extraction path and prevent owner-visible artifact misbinding.
# Normal client reads remain wait-only: they may observe AppTheory's canonical
# GET lifecycle but never dispatch, resume, or implement a Host-side controller.
# TableTheory owns the exact status/version/turn guarded convergence write.
#
# Full points: every source, redaction, framework-adoption, guarded-write, and
# focused-test invariant below holds. Zero points: any invariant fails. There is
# no partial credit and no external/deployed-state dependency.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

echo "SEC-16: Hosted-genesis provider telemetry and terminal convergence stay wired"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

require_file() {
  local path="$1"
  [[ -f "${path}" ]] || fail "required file missing: ${path}"
  echo "PASS: required file exists: ${path}"
}

require_pattern() {
  local path="$1"
  local pattern="$2"
  local message="$3"
  grep -Eq "${pattern}" "${path}" || fail "${message} (${path})"
  echo "PASS: ${message}"
}

reject_pattern() {
  local path="$1"
  local pattern="$2"
  local message="$3"
  if grep -En "${pattern}" "${path}"; then
    fail "${message} (${path})"
  fi
  echo "PASS: ${message}"
}

workload_runner="cmd/hosted-genesis-microvm-workload/runner.go"
workload_runner_tests="cmd/hosted-genesis-microvm-workload/runner_test.go"
workload_telemetry="cmd/hosted-genesis-microvm-workload/provider_telemetry.go"
workload_tests="cmd/hosted-genesis-microvm-workload/provider_telemetry_test.go"
llm_telemetry="internal/ai/llm/provider_telemetry.go"
llm_phase="internal/ai/llm/mint_conversation_phase_tools.go"
llm_phase_tests="internal/ai/llm/mint_conversation_phase_tools_test.go"
llm_http="internal/ai/llm/http_client.go"
llm_http_tests="internal/ai/llm/http_client_test.go"
openai_helpers="internal/ai/llm/openai_helpers.go"
anthropic_helpers="internal/ai/llm/anthropic_helpers.go"
llm_tests="internal/ai/llm/provider_telemetry_test.go"
candidate="internal/hostedgenesis/candidate.go"
candidate_tests="internal/hostedgenesis/candidate_test.go"
candidate_review_tests="internal/hostedgenesis/candidate_review_test.go"
observer="internal/controlplane/hosted_genesis_microvm_observer.go"
observer_tests="internal/controlplane/hosted_genesis_microvm_observer_internal_test.go"
read_handler="internal/controlplane/handlers_soul_mint_conversation_instance_read.go"
dispatcher="internal/hostedgenesis/dispatch.go"
dispatcher_tests="internal/hostedgenesis/dispatch_test.go"
completion="internal/hostedgenesis/completion/completion.go"
completion_tests="internal/hostedgenesis/completion/completion_test.go"
workload_hooks="cmd/hosted-genesis-microvm-workload/hooks.go"
workload_hook_tests="cmd/hosted-genesis-microvm-workload/hooks_test.go"
controller="cmd/hosted-genesis-microvm-controller/main.go"
controller_runtime="internal/hostedgenesis/microvm_controller.go"
host_config="internal/config/config.go"
controlplane_dispatch="internal/controlplane/hosted_genesis_microvm_dispatch.go"
aiworker_dispatch="internal/aiworker/hosted_genesis_microvm.go"
microvm_cdk="cdk/lib/hosted-genesis-microvm.ts"
microvm_cdk_tests="cdk/test/lesser-host-stack-microvm.test.ts"
store="internal/store/hosted_genesis_sessions.go"
store_tests="internal/store/hosted_genesis_sessions_test.go"

required_files=(
  "${workload_runner}"
  "${workload_runner_tests}"
  "${workload_telemetry}"
  "${workload_tests}"
  "${llm_telemetry}"
  "${llm_phase}"
  "${llm_phase_tests}"
  "${llm_http}"
  "${llm_http_tests}"
  "${openai_helpers}"
  "${anthropic_helpers}"
  "${llm_tests}"
  "${candidate}"
  "${candidate_tests}"
  "${candidate_review_tests}"
  "${observer}"
  "${observer_tests}"
  "${read_handler}"
  "${dispatcher}"
  "${dispatcher_tests}"
  "${completion}"
  "${completion_tests}"
  "${workload_hooks}"
  "${workload_hook_tests}"
  "${controller}"
  "${controller_runtime}"
  "${host_config}"
  "${controlplane_dispatch}"
  "${aiworker_dispatch}"
  "${microvm_cdk}"
  "${microvm_cdk_tests}"
  "${store}"
  "${store_tests}"
)
for path in "${required_files[@]}"; do
  require_file "${path}"
done

# The workload must consume the real phase-local SDK/tool seam for both
# providers and immediately persist accepted section/candidate progress.
require_pattern "${workload_runner}" 'RunMintConversationPhase' 'MicroVM workload invokes the phase-local provider SDK/tool loop'
require_pattern "${workload_runner}" 'ApplyDeclarationTool[(]' 'MicroVM workload applies canonical Host section validation'
require_pattern "${workload_runner}" 'CheckpointHostedGenesisCandidate[(]' 'accepted sections and provider evidence checkpoint through Host store guards'
require_pattern "${workload_runner}" 'FinalizeDeclarationCandidate[(]' 'affirmed candidate finalization is deterministic and provider-free'
require_pattern "${llm_phase}" 'runMintConversationPhaseOpenAI[(]' 'OpenAI implements the phase-local declaration tool loop'
require_pattern "${llm_phase}" 'runMintConversationPhaseAnthropic[(]' 'Anthropic implements the phase-local declaration tool loop'
require_pattern "${llm_phase}" 'withProviderAttemptTelemetry[(]' 'both provider loops expose durable SDK attempt observations'
require_pattern "${openai_helpers}" 'WithMaxRetries[(]DefaultProviderSDKRetryBudget[)]' 'OpenAI SDK retry budget is explicit'
require_pattern "${anthropic_helpers}" 'WithMaxRetries[(]DefaultProviderSDKRetryBudget[)]' 'Anthropic SDK retry budget is explicit'
require_pattern "${workload_runner}" 'context[.]WithTimeout[(]' 'provider calls bound the whole SDK retry lifecycle'
require_pattern "${workload_runner}" 'startHeartbeat[(]' 'provider calls start a periodic heartbeat'
require_pattern "${workload_runner}" 'ProviderFailureClass[(]' 'provider failures are reduced to typed content-free classes'
require_pattern "${workload_runner}" 'RecordFailure[(]' 'provider failure converges through the durable completion writer'

for tool in declaration_identity_put declaration_philosophy_put declaration_discipline_put declaration_boundaries_put declaration_soul_put; do
  require_pattern "${candidate}" "${tool}" "typed candidate exposes ${tool}"
done
require_pattern "${candidate}" 'ApplyDeclarationCandidateAction[(]' 'owner action is structurally bound to candidate review state'
require_pattern "${candidate}" 'CandidateRevision[[:space:]]+int64' 'owner action binds the exact candidate revision'
require_pattern "${candidate}" 'CandidateHash[[:space:]]+string' 'owner action binds the exact candidate hash'
require_pattern "${candidate}" 'ReviewHash[[:space:]]+string' 'owner action binds the exact review hash'
require_pattern "${candidate}" 'CanonicalJSON' 'candidate stores exact canonical publication bytes'
require_pattern "${candidate}" 'RecoverDeclarationOwnerReviewCanonicalJSON[(]' 'owner review exposes a reversible exact canonical JSON block'
require_pattern "${candidate}" 'reviewCanonicalJSON != c[.]CanonicalJSON' 'stored review must recover the exact candidate bytes'
require_pattern "${candidate}" 'MaxDeclarationOwnerReviewRunes[[:space:]]*=[[:space:]]*65536' 'lossless owner review remains inside the documented API limit'
require_pattern "${candidate}" 'ProviderAttempts' 'candidate stores bounded content-free provider attempts'
require_pattern "${candidate}" 'ProviderAttempts.*excluded from CanonicalJSON|outside CanonicalJSON' 'provider telemetry cannot alter owner-reviewed semantic bytes'
require_pattern "${candidate}" 'CandidateRevision \*int64[[:space:]]+`json:"candidateRevision,omitempty"`' 'phase tool payload carries the exact candidate revision'
require_pattern "${candidate}" 'CandidateHash[[:space:]]+string[[:space:]]+`json:"candidateHash,omitempty"`' 'phase tool payload carries the exact candidate hash'
require_pattern "${candidate}" 'DisallowUnknownFields[(]' 'canonical tool decoding rejects unknown payload fields'
require_pattern "${llm_phase}" '"candidateRevision"' 'provider schemas require the candidate revision binding'
require_pattern "${llm_phase}" '"candidateHash"' 'provider schemas require the candidate hash binding'
require_pattern "${workload_runner}" 'runDeterministicReviewAndPersist[(]' 'accepted five-body state renders the stored review without a provider'

[[ ! -e "internal/ai/llm/mint_conversation_declarations.go" ]] || fail "whole-transcript declaration extractor file must be deleted"
echo "PASS: whole-transcript declaration extractor file is deleted"
for forbidden in StatusDeclarationExtractionPending FailureCodeDeclarationExtractionFailed startHostedGenesisDeclarationExtraction extractMintConversationDeclarations parseMintConversationCompleteDeclarations resolveMintConversationCompletion hostedGenesisSyncAssistantFallbackEnabled actorTranscriptRequestsFinalAffirmation; do
  if find cmd/hosted-genesis-microvm-workload internal/aiworker internal/controlplane internal/hostedgenesis internal/store \
      -type f -name '*.go' ! -name '*_test.go' -print0 | xargs -0 grep -En "${forbidden}"; then
    fail "removed Hosted Genesis compatibility symbol remains in deployed Go: ${forbidden}"
  fi
  echo "PASS: removed Hosted Genesis compatibility symbol is absent: ${forbidden}"
done

# The seam exposes bounded metadata only. The workload logger may enrich it with
# tenant/correlation identities but may never add raw provider material.
for field in Sequence FirstSDKEvent ElapsedMS IdleMS OutputBytes OutputSHA256 InputTokens OutputTokens StopReason SDKAttemptOrdinal SDKRetryBudget HTTPStatus ProviderRequestID ToolName ToolCallHash ValidationCodes ValidationPaths; do
  require_pattern "${llm_telemetry}" "${field}[[:space:]]" "provider event records ${field} metadata"
done
require_pattern "${llm_telemetry}" 'func[[:space:]]+[(]r [+*]providerTelemetryRecorder[)][[:space:]]+emitSDK' 'SDK events use the first-event/sequence recorder'
require_pattern "${llm_telemetry}" 'sha256[.]Sum256' 'provider output and tool identities use SHA256 metadata'
require_pattern "${llm_http}" 'sdk_http_attempt' 'provider transport emits every SDK HTTP attempt'
require_pattern "${llm_http}" 'boundedProviderRequestID' 'provider request ids are bounded before observation'
require_pattern "${workload_telemetry}" 'provider_call_heartbeat' 'periodic provider heartbeat is structured'
require_pattern "${workload_telemetry}" 'defaultProviderHeartbeatInterval[[:space:]]*=[[:space:]]*10 [*] time[.]Second' 'production provider heartbeat remains ten seconds'
require_pattern "${workload_telemetry}" 'last_sdk_event_at' 'heartbeat records last SDK-event time'
require_pattern "${workload_telemetry}" 'output_sha256' 'heartbeat records output identity without output content'
require_pattern "${workload_telemetry}" 'sdk_attempt_ordinal' 'runtime telemetry records the SDK attempt ordinal'
require_pattern "${workload_telemetry}" 'provider_request_id' 'runtime telemetry records only bounded provider request ids'
require_pattern "${workload_telemetry}" 'validation_codes' 'runtime telemetry records machine validation codes without tool content'

telemetry_sources=("${llm_telemetry}" "${workload_telemetry}" "${llm_http}" "${llm_phase}" "${openai_helpers}" "${anthropic_helpers}")
for path in "${telemetry_sources[@]}"; do
  reject_pattern "${path}" 'slog[.](String|Any)[(]"(prompt|transcript|declaration|api_key|token|secret|raw_output|raw_delta)"' 'telemetry has no raw-content or secret logging attribute'
done
require_pattern "${llm_phase}" 'tool_validation_completed' 'phase-local tool validation is observed as metadata'
require_pattern "${llm_phase}" 'provider_call_completed' 'phase-local provider output is observed as metadata'

# Wait-only client reads observe AppTheory's actual controller GET and may
# converge terminal truth. They never dispatch work or import a raw DynamoDB
# client. Lifecycle semantics remain in AppTheory's reconciliation result.
require_pattern "${read_handler}" 'observeHostedGenesisMicroVMOnRead[(]' 'authenticated single read invokes wait-only lifecycle observation'
require_pattern "${observer}" '[.]ReconcileMicroVM[(]' 'observer uses the existing AppTheory controller adapter'
require_pattern "${observer}" 'result[.]CannotCompletePendingTurn' 'observer consumes AppTheory-reconciled non-runnable state'
require_pattern "${observer}" 'FailureCodeMicroVMUnavailable' 'terminal MicroVM state becomes a typed Host failure'
require_pattern "${observer}" '[.]RecordFailure[(]' 'terminal MicroVM state converges through CompletionWriter'
reject_pattern "${observer}" 'DispatchMicroVMRun|Resume|Restart' 'wait-only observer cannot dispatch, resume, or restart execution'
reject_pattern "${observer}" 'aws-sdk-go-v2/service/dynamodb|tabletheory' 'wait-only observer cannot bypass the Host store boundary'
reject_pattern "${read_handler}" 'DispatchMicroVMRun|Resume|Restart' 'client read handler cannot nudge processing work'
require_pattern "${dispatcher}" 'http[.]MethodGet' 'controller reconciliation uses AppTheory MicroVM GET'
require_pattern "${dispatcher}" 'ReconcileMicroVMRegistryStatus[(]' 'controller response uses AppTheory lifecycle reconciliation'
require_pattern "${dispatcher}" 'StateSuspended' 'suspended accepted work is classified as unable to complete'
require_pattern "${dispatcher}" 'CannotCompletePendingTurn' 'dispatcher exposes the non-runnable accepted-turn verdict'

# Endpoint-dispatched provider work continues after the ingress response. AWS
# defines MicroVM idleness using inbound endpoint traffic, not guest CPU or
# outbound provider traffic, so Hosted Genesis must omit ProviderIdlePolicy
# entirely. There is no polling keepalive, raw SDK escape hatch, or legacy idle
# env path. AppTheory remains the sole Run/Get/Invoke lifecycle substrate.
idle_policy_sources=(
  "${dispatcher}"
  "${controller}"
  "${controller_runtime}"
  "${host_config}"
  "${controlplane_dispatch}"
  "${aiworker_dispatch}"
  "${microvm_cdk}"
)
for path in "${idle_policy_sources[@]}"; do
  reject_pattern "${path}" 'HOSTED_GENESIS_MICROVM_IDLE_(MAX|SUSPENDED|AUTO_RESUME)' 'Hosted Genesis has no legacy endpoint-idle env/config path'
  reject_pattern "${path}" '^[[:space:]]*IdlePolicy[[:space:]]*:' 'Hosted Genesis has no deployed AppTheory idle-policy field or assignment'
done
reject_pattern "${dispatcher}" 'json:"idle_policy' 'Hosted Genesis AppTheory run payload omits ProviderIdlePolicy'
reject_pattern "${host_config}" 'json:"idle"' 'Hosted Genesis compact deployment config has no legacy idle-policy field'
reject_pattern "${dispatcher}" 'aws-sdk-go-v2/service/lambdamicrovms' 'Host dispatcher has no raw MicroVM SDK escape hatch'
require_pattern "${workload_hook_tests}" 'TestHookServer_TurnEndpointDetachedProviderOutlivesFormerIdleBoundary' 'detached in-VM provider work has beyond-boundary regression coverage'
require_pattern "${dispatcher_tests}" 'TestHTTPControllerDispatcherPassesAppTheoryRuntimeEnvelopeWithoutSecretsOrIdlePolicy' 'AppTheory run payload omission is tested'
require_pattern "${dispatcher_tests}" 'TestHTTPControllerDispatcherReconcileSuspendedAcceptedTurnRequiresDurableConvergence' 'suspended accepted work has no-resume convergence coverage'
require_pattern "${completion}" 'FailureCodeMicroVMUnavailable' 'MicroVM failure preserves the prior durable recovery budget'
require_pattern "${completion_tests}" 'TestRecordFailure_ExhaustedMicroVMUnavailableRetryBecomesRestart' 'exhausted MicroVM retry gives exact restart guidance'
require_pattern "${completion_tests}" 'TestRecordFailure_SuspendedVMOnExhaustedCurrentSectionRetryStaysExhausted' 'suspended observation cannot reset an exhausted provider-step budget'

# Runtime stdout/stderr is real only when AppTheory propagates the Host-owned
# execution role and that role can assume/tag the runtime session and write the
# documented default /aws/lambda/microvms/<image> group. Do not pin all runtime
# output to a misleading build stream. Application logs remain content-free.
require_pattern "${microvm_cdk}" 'executionRole,' 'AppTheory controller receives the Host-owned execution role'
require_pattern "${microvm_cdk}" '[.]withSessionTags[(]' 'MicroVM execution-role trust allows tagged sessions'
require_pattern "${microvm_cdk}" 'logs:CreateLogStream' 'MicroVM execution role can create runtime log streams'
require_pattern "${microvm_cdk}" 'logs:PutLogEvents' 'MicroVM execution role can emit runtime log events'
require_pattern "${microvm_cdk}" '/aws/lambda/microvms/' 'runtime logs use the AWS-documented default MicroVM group'
reject_pattern "${microvm_cdk}" 'logStream:[[:space:]]*"build"' 'runtime logging is not pinned to a build-only stream'
require_pattern "${microvm_cdk_tests}" 'APPTHEORY_MICROVM_EXECUTION_ROLE_ARN' 'synth tests prove AppTheory execution-role propagation'
require_pattern "${workload_hook_tests}" 'turn execution started' 'runtime start logging is tested'
require_pattern "${workload_hook_tests}" 'provider_call_completed' 'runtime provider completion logging is tested'
require_pattern "${workload_hook_tests}" 'provider_call_heartbeat' 'runtime heartbeat logging is tested'
require_pattern "${workload_hook_tests}" 'persist_completed' 'runtime persistence-boundary logging is tested'
require_pattern "${workload_hook_tests}" 'turn execution completed' 'runtime completion logging is tested'
require_pattern "${workload_hooks}" 'turn execution failed' 'runtime failure logging remains wired'
require_pattern "${workload_tests}" 'failure_persist_completed' 'runtime typed-failure persistence logging is tested'
reject_pattern "${workload_hooks}" 'body_preview|readBodyPreview' 'unmatched routes never log request bodies'

# The durable failure mutation must remain one guarded TableTheory transaction:
# the stale in_progress writer wins only at the exact session version and turn,
# and the compatibility conversation row is guarded by the same turn.
require_pattern "${store}" '[.]TransactWrite[(]' 'Host convergence uses a TableTheory transaction'
require_pattern "${store}" 'tx[.]UpdateWithBuilder[(]' 'Host convergence uses guarded TableTheory updates'
require_pattern "${store}" 'tabletheory[.]AtVersion[(]' 'session convergence uses optimistic version guarding'
require_pattern "${store}" 'tabletheory[.]Condition[(]"Status"' 'session and conversation convergence guard in_progress status'
turn_guard_count="$(grep -Ec 'tabletheory[.]Condition[(]"LatestTurnID"' "${store}")"
[[ "${turn_guard_count}" -ge 2 ]] || fail "both session and conversation updates must guard LatestTurnID"
echo "PASS: session and conversation convergence both guard the exact turn"

# Focused deterministic tests are part of the guard: phase-tool parity,
# malformed-section repair, guarded candidate atomicity, durable content-free
# attempts, provider-free exact finalization, and lifecycle reconciliation.
require_pattern "${llm_tests}" 'TestProviderStreamTelemetryOpenAIAndAnthropicIsPerEventAndRedacted' 'both SDK streams have deterministic redaction telemetry coverage'
require_pattern "${llm_phase_tests}" 'TestMintConversationPhaseToolsOpenAIAnthropicParity' 'OpenAI and Anthropic expose the same five phase tools'
require_pattern "${llm_http_tests}" 'TestProviderHTTPClientEmitsBoundedSDKAttemptEvidence' 'SDK transport attempts have bounded metadata coverage'
require_pattern "${candidate_tests}" 'TestDeclarationCandidateMalformedSectionCanBeRevisedInPlace' 'malformed section repair stays phase-local'
require_pattern "${candidate_tests}" 'TestDeclarationCandidateFiveToolsIdempotencyAndGuards' 'all five tools enforce idempotency and stale guards'
require_pattern "${candidate_tests}" 'TestDeclarationCandidateToolPayloadBindingFailsClosed' 'missing, mismatched, and unknown phase payload bindings fail closed'
require_pattern "${candidate_tests}" 'TestDeclarationCandidateReviewAffirmationAndDeterministicBytes' 'review, affirmation, and final bytes are structurally bound'
require_pattern "${candidate_review_tests}" 'TestDeclarationCandidateOwnerReviewLosslesslyExposesExactCanonicalCandidate' 'owner review exposes every hash-covered semantic value and recovers exact canonical bytes'
require_pattern "${candidate_review_tests}" 'TestDeclarationCandidatePreviouslyHiddenSemanticEditsInvalidateStructuralAffirmation' 'previously hidden semantic edits invalidate the old structural affirmation'
require_pattern "${candidate_review_tests}" 'TestDeclarationCandidateOwnerReviewOverflowStaysInSectionRepairLoop' 'lossless review overflow stays inside the bounded section repair loop'
require_pattern "${candidate_tests}" 'TestDeclarationCandidateProviderAttemptsAreBoundedDurableAndNonSemantic' 'provider attempts stay bounded and outside semantic bytes'
require_pattern "${workload_tests}" 'TestHungProviderHeartbeatsThenPersistsTypedFailureBeforeMicroVMEnvelope' 'hung provider calls heartbeat then durably fail'
require_pattern "${workload_tests}" 'TestDefaultProviderHeartbeatIntervalIsTenSeconds' 'ten-second production heartbeat has regression coverage'
require_pattern "${workload_tests}" 'TestRuntimeTelemetryEmitsRequestHeartbeatResponseToolAndTerminalEventsContentFree' 'declaration phase telemetry stays content-free'
require_pattern "${workload_runner_tests}" 'TestDeclarationPhasePersistsContentFreeProviderAttemptEvidence' 'durable provider-attempt evidence is exercised through the actor store path'
require_pattern "${workload_runner_tests}" 'TestProviderFailurePreservesAcceptedSectionForRecovery' 'accepted sections survive a later provider failure'
require_pattern "${workload_runner_tests}" 'TestReviewCheckpointRecoveryRendersStoredReviewWithoutProvider' 'review checkpoint recovery remains provider-free'
require_pattern "${workload_runner_tests}" 'TestAffirmedFinalizationMakesZeroProviderCallsAndIsStable' 'post-affirmation finalization has a zero-provider stability spy'
require_pattern "${observer_tests}" 'TestWaitOnlyReadObservesTerminalMicroVMAndConvergesGuardedHostTruth' 'terminal MicroVM lifecycle has guarded convergence coverage'
require_pattern "${observer_tests}" 'terminated|StateTerminated' 'terminal-state coverage includes terminated'
require_pattern "${observer_tests}" 'failed|StateFailed' 'terminal-state coverage includes failed'
require_pattern "${observer_tests}" 'suspended|StateSuspended' 'non-runnable-state coverage includes suspended'
require_pattern "${observer_tests}" 'max-duration|expired' 'terminal-state coverage includes max-duration expiry'
require_pattern "${store_tests}" 'TestStore_FailHostedGenesisSessionAndConversationUsesOneGuardedTransaction' 'TableTheory exact-guard transaction has focused coverage'
require_pattern "${store_tests}" 'TestStore_TypedCandidateCheckpointAndProjectionAreGuardedAtomically' 'candidate/session/projection checkpoint is one guarded transaction'
require_pattern "${store_tests}" 'TestStore_TypedCandidateFinalizationPublishesExactBytesOnce' 'exact candidate finalization is atomic and repeatable'

echo "PASS: Hosted-genesis phase tools, lossless exact-candidate review, typed candidate integrity, content-free provider evidence, and terminal convergence use AppTheory + TableTheory without extractor or client nudges"
