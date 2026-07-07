#!/usr/bin/env bash
# SEC-14: Hosted-genesis MicroVM registry boundary.
#
# Verifies that Host deployed code keeps AppTheory MicroVM registry persistence
# behind Host-owned TableTheory models/repositories instead of directly using
# AppTheory's generic SessionRegistryRecord storage shape.
#
# Full points:
#   - deployed Go code does not persist/use runtimemicrovm.SessionRegistryRecord,
#     SessionRecordToRegistryRecord, or NewTableTheorySessionRegistry;
#   - the hosted-genesis MicroVM controller does not import TableTheory and does
#     not use NewMemorySessionRegistry in deployed code;
#   - Host-owned HostedGenesisMicroVMExecution model/repository/adapter exist;
#   - the adapter implements SessionRegistry + SessionRegistryLister;
#   - controller wiring uses store.NewHostedGenesisMicroVMRegistry and keeps
#     HostedGenesisSession reconstruction wired;
#   - CDK grants the controller Host state-table read/write access for cache
#     put/get/list/delete.
# Zero points: any invariant fails. No partial credit.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

echo "SEC-14: Hosted-genesis MicroVM registry boundary"

deployed_go_files() {
  find cmd internal -type f -name '*.go' ! -name '*_test.go' | sort
}

controller_go_files() {
  find cmd/hosted-genesis-microvm-controller -maxdepth 1 -type f -name '*.go' ! -name '*_test.go' | sort
}

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

forbidden_patterns=(
  'runtimemicrovm[.]SessionRegistryRecord'
  'SessionRecordToRegistryRecord'
  'NewTableTheorySessionRegistry'
)
for pattern in "${forbidden_patterns[@]}"; do
  matches="$(deployed_go_files | xargs grep -nE "${pattern}" 2>/dev/null || true)"
  if [[ -n "${matches}" ]]; then
    echo "${matches}" >&2
    fail "deployed Host Go code must not use ${pattern}"
  fi
  echo "PASS: deployed Host Go code does not use ${pattern}"
done

controller_tabletheory_matches="$(controller_go_files | xargs grep -n 'github.com/theory-cloud/tabletheory' 2>/dev/null || true)"
if [[ -n "${controller_tabletheory_matches}" ]]; then
  echo "${controller_tabletheory_matches}" >&2
  fail "hosted-genesis MicroVM controller must not import TableTheory directly"
fi
echo "PASS: controller has no direct TableTheory import"

controller_memory_matches="$(controller_go_files | xargs grep -n 'NewMemorySessionRegistry' 2>/dev/null || true)"
if [[ -n "${controller_memory_matches}" ]]; then
  echo "${controller_memory_matches}" >&2
  fail "deployed controller must not use NewMemorySessionRegistry"
fi
echo "PASS: deployed controller does not use NewMemorySessionRegistry"

model_file="internal/store/models/hosted_genesis_microvm_execution.go"
repo_file="internal/store/hosted_genesis_microvm_executions.go"
adapter_file="internal/store/hosted_genesis_microvm_session_registry.go"
controller_file="cmd/hosted-genesis-microvm-controller/main.go"
cdk_file="cdk/lib/hosted-genesis-microvm.ts"

require_file "${model_file}"
require_file "${repo_file}"
require_file "${adapter_file}"
require_file "${controller_file}"
require_file "${cdk_file}"

require_pattern "${model_file}" 'type[[:space:]]+HostedGenesisMicroVMExecution[[:space:]]+struct' 'Host-owned HostedGenesisMicroVMExecution model is defined'
require_pattern "${model_file}" 'naming:camelCase' 'HostedGenesisMicroVMExecution uses camelCase TableTheory naming'
require_pattern "${model_file}" 'PK[[:space:]]+string[[:space:]]+`[^`]*json:"-"' 'HostedGenesisMicroVMExecution hides PK from JSON'
require_pattern "${model_file}" 'SK[[:space:]]+string[[:space:]]+`[^`]*json:"-"' 'HostedGenesisMicroVMExecution hides SK from JSON'
require_pattern "${model_file}" 'func[[:space:]]+HostedGenesisMicroVMExecutionPK' 'HostedGenesisMicroVMExecution derives its own PK'
require_pattern "${model_file}" 'func[[:space:]]+HostedGenesisMicroVMExecutionSK' 'HostedGenesisMicroVMExecution derives its own SK'

for fn in PutHostedGenesisMicroVMExecution GetHostedGenesisMicroVMExecution ListHostedGenesisMicroVMExecutions DeleteHostedGenesisMicroVMExecution; do
  require_pattern "${repo_file}" "func[[:space:]]+\(s \*Store\)[[:space:]]+${fn}" "Store exposes semantic ${fn} repository method"
done

require_pattern "${adapter_file}" 'type[[:space:]]+HostedGenesisMicroVMRegistry[[:space:]]+struct' 'Host registry adapter is defined'
require_pattern "${adapter_file}" 'var[[:space:]]+_[[:space:]]+runtimemicrovm[.]SessionRegistry[[:space:]]+=' 'adapter asserts SessionRegistry implementation'
require_pattern "${adapter_file}" 'var[[:space:]]+_[[:space:]]+runtimemicrovm[.]SessionRegistryLister[[:space:]]+=' 'adapter asserts SessionRegistryLister implementation'
require_pattern "${adapter_file}" 'func[[:space:]]+NewHostedGenesisMicroVMRegistry' 'Host registry constructor is defined'
require_pattern "${adapter_file}" 'HostedGenesisMicroVMExecutionSlugFromTenantID' 'adapter derives tenant slug from semantic tenant id'

require_pattern "${controller_file}" 'store[.]NewHostedGenesisMicroVMRegistry' 'controller wires Host-owned registry adapter'
require_pattern "${controller_file}" 'HostedGenesisMicroVMReconstructionHook' 'controller keeps HostedGenesisSession reconstruction hook wired'
require_pattern "${cdk_file}" 'stateTable[.]grantReadWriteData\(controller[.]controllerFunction\)' 'CDK grants controller state-table read/write access for Host cache'

echo "PASS: hosted-genesis MicroVM registry boundary is Host-owned, TableTheory-friendly, and reconstruction-backed"
