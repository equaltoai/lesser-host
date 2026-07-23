#!/usr/bin/env bash
# SEC-15: Hosted-genesis declaration contract is five-body-only.
#
# Verifies that hosted-genesis declaration production cannot route to a legacy
# declaration lane anywhere in deployed Host Go code. The generic legacy
# selector/wrapper vocabulary (LegacyDeclarationContract,
# DeclarationContractFromVersions, the no-contract prompt wrapper
# MintConversationSystemPrompt) must not exist or be referenced in any deployed
# Go file — not only the fresh runtime paths. Contract selection resolves
# through the fail-closed selectors, and a missing/unknown contract surfaces as
# operator_action_required, never as the legacy builder's boundaries.required.
#
# Full points:
#   - the permissive env selector DeclarationContractFromEnv does not exist in
#     any deployed Go code;
#   - no deployed Go code defines or references LegacyDeclarationContract,
#     DeclarationContractFromVersions, or the no-contract prompt wrapper
#     MintConversationSystemPrompt( (the contract-scoped
#     MintConversationSystemPromptForContract remains legal);
#   - deployed fresh hosted-genesis runtime code (MicroVM workload only)
#     does not reference the legacy DeclarationCodeBoundaries emission;
#   - the MicroVM runtime calls RequireFiveBodyDeclarationContractFromEnv and
#     routes ErrDeclarationContractUnconfigured to the operator_action lane;
#   - its builder guards on IsFiveBody and fails closed with
#     ErrDeclarationContractUnconfigured;
#   - aiworker remains transport-only and cannot select/build/extract declarations;
#   - the fail-closed selectors (env + version parser) + sentinel exist in
#     internal/hostedgenesis;
#   - CDK still provisions the five-body contract env for the MicroVM image.
# Zero points: any invariant fails. No partial credit.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

echo "SEC-15: Hosted-genesis declaration contract is five-body-only"

deployed_go_files() {
  find cmd internal -type f -name '*.go' ! -name '*_test.go' | sort
}

fresh_runtime_go_files() {
  find cmd/hosted-genesis-microvm-workload -type f -name '*.go' ! -name '*_test.go' | sort
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

# The permissive selector must stay deleted everywhere in deployed code. The
# leading non-word guard keeps RequireFiveBodyDeclarationContractFromEnv legal.
selector_matches="$(deployed_go_files | xargs grep -nE '(^|[^A-Za-z0-9_])DeclarationContractFromEnv[(]' 2>/dev/null || true)"
if [[ -n "${selector_matches}" ]]; then
  echo "${selector_matches}" >&2
  fail "deployed Host Go code must not reintroduce the permissive DeclarationContractFromEnv selector"
fi
echo "PASS: permissive DeclarationContractFromEnv selector absent from deployed Go code"

# The generic legacy selector/wrapper vocabulary must stay deleted from ALL
# deployed Host Go code, definitions included. The trailing '(' keeps the
# contract-scoped MintConversationSystemPromptForContract legal; the leading
# non-word guard keeps differently-named helpers (buildMintConversation...)
# legal while still catching qualified references and definitions.
deployed_forbidden_patterns=(
  '(^|[^A-Za-z0-9_])LegacyDeclarationContract[(]'
  '(^|[^A-Za-z0-9_])DeclarationContractFromVersions[(]'
  '(^|[^A-Za-z0-9_])MintConversationSystemPrompt[(]'
)
for pattern in "${deployed_forbidden_patterns[@]}"; do
  matches="$(deployed_go_files | xargs grep -nE "${pattern}" 2>/dev/null || true)"
  if [[ -n "${matches}" ]]; then
    echo "${matches}" >&2
    fail "deployed Host Go code must not reference ${pattern}"
  fi
  echo "PASS: deployed Host Go code does not reference ${pattern}"
done

fresh_forbidden_patterns=(
  'DeclarationCodeBoundaries([^A-Za-z]|$)'
)
for pattern in "${fresh_forbidden_patterns[@]}"; do
  matches="$(fresh_runtime_go_files | xargs grep -nE "${pattern}" 2>/dev/null || true)"
  if [[ -n "${matches}" ]]; then
    echo "${matches}" >&2
    fail "fresh hosted-genesis runtime code must not reference ${pattern}"
  fi
  echo "PASS: fresh hosted-genesis runtime code does not reference ${pattern}"
done

selector_file="internal/hostedgenesis/fivebody.go"
workload_runner="cmd/hosted-genesis-microvm-workload/runner.go"
aiworker_file="internal/aiworker/hosted_genesis.go"
cdk_microvm="cdk/lib/hosted-genesis-microvm.ts"

require_file "${selector_file}"
require_file "${workload_runner}"
require_file "${aiworker_file}"
require_file "${cdk_microvm}"

require_pattern "${selector_file}" 'func[[:space:]]+RequireFiveBodyDeclarationContractFromEnv' 'fail-closed five-body contract selector is defined'
require_pattern "${selector_file}" 'func[[:space:]]+ParseFiveBodyDeclarationContract' 'fail-closed five-body version parser is defined'
require_pattern "${selector_file}" 'ErrDeclarationContractUnconfigured[[:space:]]*=[[:space:]]*errors[.]New' 'unconfigured-contract sentinel error is defined'

require_pattern "${workload_runner}" 'RequireFiveBodyDeclarationContractFromEnv[(]' 'MicroVM workload resolves the contract through the fail-closed selector'
require_pattern "${workload_runner}" 'errors[.]Is[(][^)]*ErrDeclarationContractUnconfigured[)]' 'MicroVM workload routes the unconfigured contract to a dedicated failure lane'
require_pattern "${workload_runner}" 'FailureCodeOperatorActionRequired' 'MicroVM workload records operator_action_required for an unconfigured contract'
require_pattern "${workload_runner}" 'if[[:space:]]+!contract[.]IsFiveBody[(][)]' 'MicroVM workload declaration builder guards on the five-body contract'

for forbidden in RequireFiveBodyDeclarationContractFromEnv BuildMintConversationDeclarations ExtractMintConversationDeclarations declaration_extraction; do
  if grep -En "${forbidden}" "${aiworker_file}"; then
    fail "aiworker transport path must not select, build, or extract declarations: ${forbidden}"
  fi
  echo "PASS: aiworker transport path omits ${forbidden}"
done

require_pattern "${cdk_microvm}" 'HOSTED_GENESIS_DECLARATION_SCHEMA_VERSION' 'CDK provisions the declaration schema env for the MicroVM image'

echo "PASS: MicroVM-only hosted-genesis declaration production is five-body-only and fails closed without an explicit contract"
