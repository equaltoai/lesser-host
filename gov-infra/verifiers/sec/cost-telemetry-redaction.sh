#!/usr/bin/env bash
# SEC-13: Portal cost redaction + multi-tenant downstream-call ordering.
#
# Verifies that the portal instance-cost endpoint handler:
#   1. Enforces tenant-scoped ownership via requireInstanceAccess before any
#      instance-key secret resolution or managed Lesser metrics HTTP call.
#   2. Does not use the deprecated host-local ListCostTelemetryByInstance path.
#   3. Does not expose PII, cross-tenant fields, tenant content, raw instance
#      keys, account IDs, table keys (PK/SK), TTL, or raw EntriesJSON storage
#      payloads in the customer-facing response DTO or error messages.
#
# Project 42 M0.4 correction: the accepted data source is the managed Lesser
# instance metrics endpoint, reached server-side with an instance API key. The
# SEC-13 proof must therefore gate the secret read and HTTP call, not the old
# host-local cost telemetry store read.
#
# Source: Issue equaltoai/lesser-host#457 and Project 42 M0.4 (#529).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

HANDLER_FILE="internal/controlplane/handlers_portal_cost.go"
METRICS_FILE="internal/controlplane/managed_instance_metrics.go"
TEST_FILE="internal/controlplane/handlers_portal_cost_internal_test.go"

echo "SEC-13: Portal cost redaction + multi-tenant downstream-call ordering"
echo "Handler file: ${HANDLER_FILE}"
echo "Metrics file: ${METRICS_FILE}"
echo "Test file:    ${TEST_FILE}"
echo ""

FAIL=0

for file in "${HANDLER_FILE}" "${METRICS_FILE}" "${TEST_FILE}"; do
  if [[ ! -f "${file}" ]]; then
    echo "FAIL: required file missing: ${file}" >&2
    exit 1
  fi
done

non_comment_line() {
  local pattern="$1"
  local file="$2"
  grep -n "${pattern}" "${file}" | grep -v '^\s*[0-9]*:\s*//' | grep -v '^\s*[0-9]*:\s*\*' | head -1 | cut -d: -f1 || true
}

echo "Dimension 1: ownership gates secret resolution and managed Lesser HTTP call"
AUTH_LINE="$(non_comment_line 'requireInstanceAccess' "${HANDLER_FILE}")"
KEY_LINE="$(non_comment_line 'resolvePortalCostInstanceKey' "${HANDLER_FILE}")"
FETCH_LINE="$(non_comment_line 'fetchManagedInstanceMetrics' "${HANDLER_FILE}")"

if [[ -z "${AUTH_LINE}" ]]; then
  echo "FAIL: requireInstanceAccess call not found in handler" >&2
  FAIL=1
else
  echo "Ownership check: requireInstanceAccess at line ${AUTH_LINE}"
fi

for entry in "secret resolution:${KEY_LINE}:resolvePortalCostInstanceKey" "managed metrics fetch:${FETCH_LINE}:fetchManagedInstanceMetrics"; do
  label="${entry%%:*}"
  rest="${entry#*:}"
  line="${rest%%:*}"
  symbol="${entry##*:}"
  if [[ -z "${line}" ]]; then
    echo "FAIL: ${symbol} call not found in handler" >&2
    FAIL=1
    continue
  fi
  echo "${label}: ${symbol} at line ${line}"
  if [[ -n "${AUTH_LINE}" ]] && [[ "${line}" -le "${AUTH_LINE}" ]]; then
    echo "FAIL: ${symbol} (line ${line}) precedes requireInstanceAccess (line ${AUTH_LINE})" >&2
    FAIL=1
  fi
done

if grep -q 'ListCostTelemetryByInstance' "${HANDLER_FILE}"; then
  echo "FAIL: handler still reads host-local cost telemetry store" >&2
  FAIL=1
else
  echo "PASS: handler does not use deprecated ListCostTelemetryByInstance path"
fi

echo ""
echo "Dimension 2a: DTO redaction in handler source"
HANDLER_CONTENT="$(cat "${HANDLER_FILE}")"
NON_COMMENT="$(echo "${HANDLER_CONTENT}" | grep -v '^\s*//' | grep -v '^\s*\*' | grep -v '^\s*/\*')"
DTO_REGION="$(echo "${HANDLER_CONTENT}" | sed -n '/^type portalCostResponse struct/,/^}/p'; echo; echo "${HANDLER_CONTENT}" | sed -n '/^type portalCostDayEntry struct/,/^}/p')"

DTO_FORBIDDEN=(
  '"account_id"'
  '"pk"'
  '"sk"'
  '"ttl"'
  '"entries_json"'
  '"EntriesJSON"'
  '"instance_key"'
  '"raw_key"'
)

for forbidden in "${DTO_FORBIDDEN[@]}"; do
  if echo "${DTO_REGION}" | grep -qF "${forbidden}"; then
    echo "FAIL: DTO struct contains forbidden json tag ${forbidden}" >&2
    FAIL=1
  fi
done

if echo "${NON_COMMENT}" | grep -qF 'EntriesJSON'; then
  echo "FAIL: handler references raw EntriesJSON on a non-comment line" >&2
  FAIL=1
fi
if echo "${NON_COMMENT}" | grep -qF 'entries_json'; then
  echo "FAIL: handler references entries_json on a non-comment line" >&2
  FAIL=1
fi

echo "PASS: DTO struct json tags are clean of forbidden fields"

echo ""
echo "Dimension 2b: redaction and fail-closed proof in tests"
TEST_CONTENT="$(cat "${TEST_FILE}")"

if echo "${TEST_CONTENT}" | grep -q 'Bearer "+testRawKey' && echo "${TEST_CONTENT}" | grep -q 'NotContains(t, string(resp.Body), testRawKey)'; then
  echo "PASS: test proves bearer key is sent upstream and excluded from response"
else
  echo "FAIL: missing bearer-auth send plus response redaction proof" >&2
  FAIL=1
fi

for token in 'account_id' 'PK' 'SK' 'ttl' 'entries_json' 'EntriesJSON' 'instance_key' 'raw_key'; do
  if ! echo "${TEST_CONTENT}" | grep -q "${token}"; then
    echo "FAIL: test redaction assertion missing forbidden token ${token}" >&2
    FAIL=1
  fi
done
if echo "${TEST_CONTENT}" | grep -q 'forbidden := range' && echo "${TEST_CONTENT}" | grep -q 'require.NotContains(t, string(resp.Body), forbidden)'; then
  echo "PASS: test asserts forbidden response fields are absent"
else
  echo "FAIL: test does not assert forbidden response fields are absent" >&2
  FAIL=1
fi

if echo "${TEST_CONTENT}" | grep -q 'WrongOwnerForbiddenBeforeSecretOrHTTP' && echo "${TEST_CONTENT}" | grep -q 'require.Zero(t, secretReads)' && echo "${TEST_CONTENT}" | grep -q 'require.Zero(t, httpCalls)'; then
  echo "PASS: forbidden tenant proof skips secret resolution and HTTP call"
else
  echo "FAIL: missing wrong-owner proof that secret resolution and HTTP are skipped" >&2
  FAIL=1
fi

if echo "${TEST_CONTENT}" | grep -q 'Upstream5xxDoesNotLeakKeyOrBody' && echo "${TEST_CONTENT}" | grep -q 'KeyResolverFailureDoesNotLeak'; then
  echo "PASS: tests cover upstream/key-resolution error redaction"
else
  echo "FAIL: missing upstream/key-resolution redaction tests" >&2
  FAIL=1
fi

echo ""
if [[ "${FAIL}" -ne 0 ]]; then
  echo "FAIL: SEC-13 portal cost redaction check failed" >&2
  exit 1
fi

echo "PASS: SEC-13 portal cost redaction + downstream-call ordering verified"
