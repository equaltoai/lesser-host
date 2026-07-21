#!/usr/bin/env bash
# SEC-16: Hosted-genesis provider telemetry and terminal convergence stay wired.
#
# The hosted-genesis MicroVM must observe every OpenAI/Anthropic SDK streaming
# event without logging content, emit provider-call heartbeats while a call is
# idle, bound the whole SDK retry lifecycle, and durably fail the accepted turn
# when the provider times out or AppTheory reports a terminal/expired MicroVM.
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
workload_telemetry="cmd/hosted-genesis-microvm-workload/provider_telemetry.go"
workload_tests="cmd/hosted-genesis-microvm-workload/provider_telemetry_test.go"
llm_telemetry="internal/ai/llm/provider_telemetry.go"
llm_stream="internal/ai/llm/mint_conversation_stream.go"
llm_declarations="internal/ai/llm/mint_conversation_declarations.go"
openai_helpers="internal/ai/llm/openai_helpers.go"
anthropic_helpers="internal/ai/llm/anthropic_helpers.go"
llm_tests="internal/ai/llm/provider_telemetry_test.go"
observer="internal/controlplane/hosted_genesis_microvm_observer.go"
observer_tests="internal/controlplane/hosted_genesis_microvm_observer_internal_test.go"
read_handler="internal/controlplane/handlers_soul_mint_conversation_instance_read.go"
dispatcher="internal/hostedgenesis/dispatch.go"
store="internal/store/hosted_genesis_sessions.go"
store_tests="internal/store/hosted_genesis_sessions_test.go"

required_files=(
  "${workload_runner}"
  "${workload_telemetry}"
  "${workload_tests}"
  "${llm_telemetry}"
  "${llm_stream}"
  "${llm_declarations}"
  "${openai_helpers}"
  "${anthropic_helpers}"
  "${llm_tests}"
  "${observer}"
  "${observer_tests}"
  "${read_handler}"
  "${dispatcher}"
  "${store}"
  "${store_tests}"
)
for path in "${required_files[@]}"; do
  require_file "${path}"
done

# The workload must consume the real SDK event seam for both providers. A
# discarded func(string){} delta callback is the exact regression this guard is
# intended to make impossible in the MicroVM runtime.
require_pattern "${workload_runner}" 'StreamMintConversationOpenAIWithTelemetry[(]' 'OpenAI streaming uses the telemetry-aware SDK path'
require_pattern "${workload_runner}" 'StreamMintConversationAnthropicWithTelemetry[(]' 'Anthropic streaming uses the telemetry-aware SDK path'
require_pattern "${workload_runner}" 'MintConversationDeclarationsOpenAIWithTelemetry[(]' 'OpenAI declaration extraction uses the telemetry-aware SDK path'
require_pattern "${workload_runner}" 'MintConversationDeclarationsAnthropicWithTelemetry[(]' 'Anthropic declaration extraction uses the telemetry-aware SDK path'
require_pattern "${workload_runner}" 'context[.]WithTimeout[(]' 'provider calls bound the whole SDK retry lifecycle'
require_pattern "${workload_runner}" 'startHeartbeat[(]' 'provider calls start a periodic heartbeat'
require_pattern "${workload_runner}" 'ProviderFailureClass[(]' 'provider failures are reduced to typed content-free classes'
require_pattern "${workload_runner}" 'RecordFailure[(]' 'provider failure converges through the durable completion writer'
reject_pattern "${workload_runner}" 'func[[:space:]]*[(]string[)][[:space:]]*[{][[:space:]]*[}]' 'MicroVM workload has no discarded string-delta callback'

# The seam exposes bounded metadata only. The workload logger may enrich it with
# tenant/correlation identities but may never add raw provider material.
for field in Sequence FirstSDKEvent ElapsedMS IdleMS OutputBytes OutputSHA256 InputTokens OutputTokens StopReason OutputCount; do
  require_pattern "${llm_telemetry}" "${field}[[:space:]]" "provider event records ${field} metadata"
done
require_pattern "${llm_telemetry}" 'func[[:space:]]+[(]r [+*]providerTelemetryRecorder[)][[:space:]]+emitSDK' 'SDK events use the first-event/sequence recorder'
require_pattern "${llm_telemetry}" 'sha256[.]Sum256' 'output identities use SHA256 metadata'
require_pattern "${workload_telemetry}" 'provider_call_heartbeat' 'periodic provider heartbeat is structured'
require_pattern "${workload_telemetry}" 'last_sdk_event_at' 'heartbeat records last SDK-event time'
require_pattern "${workload_telemetry}" 'output_sha256' 'heartbeat records output identity without output content'
reject_pattern "${llm_telemetry}" 'PayloadBytes|PayloadSHA256|payload_bytes|payload_sha256' 'provider events cannot fingerprint prompt/request payloads'
reject_pattern "${workload_telemetry}" 'payload_bytes|payload_sha256' 'workload logs cannot fingerprint prompt/request payloads'

telemetry_sources=("${llm_telemetry}" "${workload_telemetry}" "${openai_helpers}" "${anthropic_helpers}")
for path in "${telemetry_sources[@]}"; do
  reject_pattern "${path}" 'slog[.](String|Any)[(]"(prompt|transcript|declaration|api_key|token|secret|raw_output|raw_delta)"' 'telemetry has no raw-content or secret logging attribute'
done
require_pattern "${openai_helpers}" 'schema_output_received' 'OpenAI declaration output is observed as metadata'
require_pattern "${anthropic_helpers}" 'tool_input_received' 'Anthropic declaration input is observed as metadata'
require_pattern "${llm_declarations}" 'SchemaName:' 'declaration telemetry names the strict schema'
require_pattern "${llm_declarations}" 'ToolName:' 'declaration telemetry names the strict tool'

# Wait-only client reads observe AppTheory's actual controller GET and may
# converge terminal truth. They never dispatch work or import a raw DynamoDB
# client. Lifecycle semantics remain in AppTheory's reconciliation result.
require_pattern "${read_handler}" 'observeHostedGenesisMicroVMOnRead[(]' 'authenticated single read invokes wait-only lifecycle observation'
require_pattern "${observer}" '[.]ReconcileMicroVM[(]' 'observer uses the existing AppTheory controller adapter'
require_pattern "${observer}" 'result[.]Terminal' 'observer consumes AppTheory-reconciled terminal state'
require_pattern "${observer}" 'FailureCodeMicroVMUnavailable' 'terminal MicroVM state becomes a typed Host failure'
require_pattern "${observer}" '[.]RecordFailure[(]' 'terminal MicroVM state converges through CompletionWriter'
reject_pattern "${observer}" 'DispatchMicroVMRun|Resume|Restart' 'wait-only observer cannot dispatch, resume, or restart execution'
reject_pattern "${observer}" 'aws-sdk-go-v2/service/dynamodb|tabletheory' 'wait-only observer cannot bypass the Host store boundary'
reject_pattern "${read_handler}" 'DispatchMicroVMRun|Resume|Restart' 'client read handler cannot nudge processing work'
require_pattern "${dispatcher}" 'http[.]MethodGet' 'controller reconciliation uses AppTheory MicroVM GET'
require_pattern "${dispatcher}" 'ReconcileMicroVMRegistryStatus[(]' 'controller response uses AppTheory lifecycle reconciliation'

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

# Focused deterministic tests are part of the guard: telemetry/redaction for
# both providers, hung-call heartbeat + durable failure, declaration telemetry,
# and terminal/failed/max-duration lifecycle reconciliation.
require_pattern "${llm_tests}" 'TestProviderStreamTelemetryOpenAIAndAnthropicIsPerEventAndRedacted' 'both SDK streams have deterministic redaction telemetry coverage'
require_pattern "${llm_tests}" 'TestDeclarationExtractionTelemetryHasPhaseBoundariesAndNoRawPayload' 'declaration telemetry has deterministic redaction coverage'
require_pattern "${workload_tests}" 'TestHungProviderHeartbeatsThenPersistsTypedFailureBeforeMicroVMEnvelope' 'hung provider calls heartbeat then durably fail'
require_pattern "${workload_tests}" 'assistant_stream timeout' 'durable provider failure test requires a content-free typed message'
require_pattern "${workload_tests}" 'TestDeclarationParseFailureTelemetryIsCorrelatedRedactedAndDurable' 'declaration failure telemetry stays correlated and durable'
require_pattern "${observer_tests}" 'TestWaitOnlyReadObservesTerminalMicroVMAndConvergesGuardedHostTruth' 'terminal MicroVM lifecycle has guarded convergence coverage'
require_pattern "${observer_tests}" 'terminated|StateTerminated' 'terminal-state coverage includes terminated'
require_pattern "${observer_tests}" 'failed|StateFailed' 'terminal-state coverage includes failed'
require_pattern "${observer_tests}" 'max-duration|expired' 'terminal-state coverage includes max-duration expiry'
require_pattern "${store_tests}" 'TestStore_FailHostedGenesisSessionAndConversationUsesOneGuardedTransaction' 'TableTheory exact-guard transaction has focused coverage'

echo "PASS: Hosted-genesis provider telemetry is content-free and terminal provider/MicroVM states converge through AppTheory + TableTheory without client nudges"
