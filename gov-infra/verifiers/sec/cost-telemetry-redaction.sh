#!/usr/bin/env bash
# SEC-13: Cost-telemetry redaction + multi-tenant read ordering.
#
# Verifies that the portal cost-telemetry endpoint handler:
#   1. Enforces tenant-scoped ownership via requireInstanceAccess before any
#      cost telemetry database read.
#   2. Does not expose PII, cross-tenant fields, tenant content, raw instance
#      keys, account IDs, table keys (PK/SK), TTL, or raw EntriesJSON storage
#      payloads in the customer-facing response DTO.
#
# Multi-tenant read ordering (Dimension 1):
#   requireInstanceAccess must appear inside handlePortalGetInstanceCost at a
#   line number strictly before ListCostTelemetryByInstance. This ensures the
#   ownership check gates all cost telemetry store reads.
#
# Redaction (Dimension 2):
#   The portalCostResponse, portalCostDayEntry, and ReconciledCostEntry types
#   must exclude account_id, PK, SK, ttl, entries_json, EntriesJSON, raw
#   instance keys, secrets, request bodies, actor/object/content URIs, and
#   tenant content. The Go handler source and test file are both checked.
#
# Full points: requireInstanceAccess before ListCostTelemetryByInstance, and
#   the source + tests prove forbidden fields are excluded.
# Zero points: any violation — wrong ordering, forbidden field present in DTO,
#   or redaction test missing.
#
# No partial credit.
#
# Source: Issue equaltoai/lesser-host#457

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

HANDLER_FILE="internal/controlplane/handlers_portal_cost.go"
TEST_FILE="internal/controlplane/handlers_portal_cost_internal_test.go"

echo "SEC-13: Cost-telemetry redaction + multi-tenant read ordering"
echo "Handler file: ${HANDLER_FILE}"
echo "Test file:    ${TEST_FILE}"
echo ""

# ── Step 1: Files must exist ──────────────────────────────────────────────
FAIL=0

if [[ ! -f "${HANDLER_FILE}" ]]; then
  echo "FAIL: handler file missing: ${HANDLER_FILE}" >&2
  exit 1
fi

if [[ ! -f "${TEST_FILE}" ]]; then
  echo "FAIL: test file missing: ${TEST_FILE}" >&2
  exit 1
fi

# ── Dimension 1: Multi-tenant read ordering ───────────────────────────────
# requireInstanceAccess must be called before ListCostTelemetryByInstance.

if ! grep -q 'requireInstanceAccess' "${HANDLER_FILE}"; then
  echo "FAIL: requireInstanceAccess call not found in handler" >&2
  FAIL=1
fi

# Exclude comment lines (//...) to avoid matching doc comments.
AUTH_LINE="$(grep -n 'requireInstanceAccess' "${HANDLER_FILE}" | grep -v '^\s*[0-9]*:\s*//' | head -1 | cut -d: -f1 || true)"
if [[ -z "${AUTH_LINE}" ]]; then
  echo "FAIL: requireInstanceAccess call not found (non-comment)" >&2
  FAIL=1
else
  echo "Ownership check: requireInstanceAccess at line ${AUTH_LINE}"
fi

# Find ListCostTelemetryByInstance call site (not definition, not comments).
STORE_LINE="$(grep -n 'ListCostTelemetryByInstance' "${HANDLER_FILE}" | grep -v '^\s*[0-9]*:\s*//' | grep -v 'func ' | head -1 | cut -d: -f1 || true)"
if [[ -z "${STORE_LINE}" ]]; then
  echo "FAIL: ListCostTelemetryByInstance call not found in handler" >&2
  FAIL=1
else
  echo "Cost telemetry read: ListCostTelemetryByInstance at line ${STORE_LINE}"
fi

if [[ -n "${AUTH_LINE}" ]] && [[ -n "${STORE_LINE}" ]]; then
  if [[ "${STORE_LINE}" -le "${AUTH_LINE}" ]]; then
    echo "FAIL: ListCostTelemetryByInstance (line ${STORE_LINE}) precedes requireInstanceAccess (line ${AUTH_LINE})" >&2
    FAIL=1
  else
    echo "PASS: ownership check precedes cost telemetry read (${AUTH_LINE} < ${STORE_LINE})"
  fi
fi

echo ""

# ── Dimension 2: DTO redaction — handler source ────────────────────────────
# The portalCostResponse, portalCostDayEntry, and ReconciledCostEntry types
# (plus buildPortalCostResponse) must not reference forbidden fields on
# non-comment lines. Doc comments describing excluded fields are legitimate.

HANDLER_CONTENT="$(cat "${HANDLER_FILE}")"

# Extract non-comment source lines (exclude // and block-comment lines).
NON_COMMENT="$(echo "${HANDLER_CONTENT}" | grep -v '^\s*//' | grep -v '^\s*\*' | grep -v '^\s*/\*')"

echo "Dimension 2a: DTO redaction in handler source"

# EntriesJSON is expected in buildPortalCostResponse for unmarshaling,
# but must not appear in the DTO struct field tags or response fields.
# Allow EntriesJSON only in the unmarshal line (rec.EntriesJSON).
if echo "${NON_COMMENT}" | grep -qF 'EntriesJSON' && ! echo "${NON_COMMENT}" | grep -qF 'rec.EntriesJSON'; then
  echo "FAIL: EntriesJSON found on non-comment line outside rec.EntriesJSON unmarshal" >&2
  FAIL=1
elif echo "${NON_COMMENT}" | grep -qF 'EntriesJSON'; then
  echo "PASS: EntriesJSON only in rec.EntriesJSON unmarshal (expected)"
fi

if echo "${NON_COMMENT}" | grep -qF 'entries_json'; then
  echo "FAIL: entries_json found on non-comment line" >&2
  FAIL=1
fi

# Verify the forbidden tokens are explicitly absent from the DTO struct definitions.
# portalCostResponse and portalCostDayEntry are defined in this file.
DTO_REGION="$(echo "${HANDLER_CONTENT}" | sed -n '/^type portalCostResponse struct/,/^}/p'; echo; echo "${HANDLER_CONTENT}" | sed -n '/^type portalCostDayEntry struct/,/^}/p')"

# The DTO region must NOT contain json:"account_id", json:"pk", json:"sk", json:"ttl", etc.
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

echo "PASS: DTO struct json tags are clean of forbidden fields"

echo ""

# ── Dimension 2b: Redaction proof in tests ─────────────────────────────────
# The test file must contain a redaction assertion that proves the response
# JSON does not contain account_id, PK, SK, ttl, entries_json, or EntriesJSON.

TEST_CONTENT="$(cat "${TEST_FILE}")"

echo "Dimension 2b: Redaction proof in test file"

# Verify the test calls jsonContains with forbidden field names.
if echo "${TEST_CONTENT}" | grep -q 'jsonContains.*account_id.*PK.*SK.*ttl.*entries_json.*EntriesJSON'; then
  echo "PASS: test redaction assertion found (jsonContains with forbidden fields)"
else
  echo "FAIL: no jsonContains redaction assertion covering account_id, PK, SK, ttl, entries_json, EntriesJSON" >&2
  FAIL=1
fi

# Verify the WrongOwnerForbidden test proves cost store was never read.
if echo "${TEST_CONTENT}" | grep -q 'AssertNotCalled.*All'; then
  echo "PASS: test proves cost telemetry not read on forbidden (AssertNotCalled)"
else
  echo "FAIL: no AssertNotCalled proof that cost telemetry is skipped on forbidden" >&2
  FAIL=1
fi

echo ""

if [[ "${FAIL}" -ne 0 ]]; then
  echo "FAIL: SEC-13 cost-telemetry redaction check failed" >&2
  exit 1
fi

echo "PASS: SEC-13 cost-telemetry redaction + multi-tenant read ordering verified"
