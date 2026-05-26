#!/usr/bin/env bash
# SEC-11: Portal stack-state tenant scoping.
#
# Verifies that the stack-state endpoint handler enforces tenant-scoped
# ownership before performing any stack-state database reads. The check
# is a source-level assertion on internal/controlplane/handlers_portal_stack.go:
# the requireInstanceAccess ownership-check call must appear at a line
# number strictly before any stack-state DB read function calls
# (categorizeLatestUpdateJobs, loadProvisionJobFallback) within the
# handlePortalGetInstanceStack function body.
#
# Rationale: multi-tenant isolation requires that every endpoint serving
# instance-scoped data perform an explicit ownership check before reading
# any per-instance state. The requireInstanceAccess helper enforces this
# by loading the instance via slug and verifying the caller's ownership.
# Any stack-state data read that precedes the ownership check would leak
# another tenant's data.
#
# Full points: requireInstanceAccess present, and its line number is
#   strictly before every stack-state read call. Zero points: any
#   violation — requireInstanceAccess missing, or a stack-state read
#   call precedes the ownership check.
#
# No partial credit.
#
# Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M1.14
# Issue: equaltoai/lesser-host#423

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

HANDLER_FILE="internal/controlplane/handlers_portal_stack.go"

echo "SEC-11: Portal stack-state tenant scoping"
echo "Handler file: ${HANDLER_FILE}"
echo ""

# ── Step 1: File must exist ──────────────────────────────────────────────
if [[ ! -f "${HANDLER_FILE}" ]]; then
  echo "FAIL: handler file missing: ${HANDLER_FILE}" >&2
  exit 1
fi

# ── Step 2: requireInstanceAccess must be called ─────────────────────────
if ! grep -q 'requireInstanceAccess' "${HANDLER_FILE}"; then
  echo "FAIL: requireInstanceAccess call not found in handler" >&2
  exit 1
fi

# Exclude comment lines (//...) to avoid matching the function-level doc comment.
AUTH_LINE="$(grep -n 'requireInstanceAccess' "${HANDLER_FILE}" | grep -v '^\s*[0-9]*:\s*//' | head -1 | cut -d: -f1)"
echo "Ownership check: requireInstanceAccess at line ${AUTH_LINE}"

# ── Step 3: Find stack-state DB read calls ───────────────────────────────
# These are the function calls (not definitions) that perform stack-state
# database reads. We exclude lines that start with "func" to avoid matching
# the function definitions themselves, and instead match only the call sites
# inside handlePortalGetInstanceStack.

# categorizeLatestUpdateJobs queries update jobs via GSI1.
CAT_LINE="$(grep -n 'categorizeLatestUpdateJobs(' "${HANDLER_FILE}" | grep -v 'func' | head -1 | cut -d: -f1 || true)"
# loadProvisionJobFallback loads the initial provision job from the store.
PROV_LINE="$(grep -n 'loadProvisionJobFallback(' "${HANDLER_FILE}" | grep -v 'func' | head -1 | cut -d: -f1 || true)"

# ── Step 4: Emit evidence ────────────────────────────────────────────────
echo ""
echo "Stack-state read call sites:"
if [[ -n "${CAT_LINE}" ]]; then
  echo "  categorizeLatestUpdateJobs call at line ${CAT_LINE}"
else
  echo "  categorizeLatestUpdateJobs call: NOT FOUND"
fi
if [[ -n "${PROV_LINE}" ]]; then
  echo "  loadProvisionJobFallback call at line ${PROV_LINE}"
else
  echo "  loadProvisionJobFallback call: NOT FOUND"
fi

if [[ -z "${CAT_LINE}" ]] && [[ -z "${PROV_LINE}" ]]; then
  echo "FAIL: no stack-state read calls found in handler" >&2
  exit 1
fi

# ── Step 5: Ownership check must precede all stack-state reads ───────────
FAIL=0

if [[ -n "${CAT_LINE}" ]] && [[ "${CAT_LINE}" -le "${AUTH_LINE}" ]]; then
  echo "FAIL: categorizeLatestUpdateJobs call (line ${CAT_LINE}) precedes ownership check (line ${AUTH_LINE})" >&2
  FAIL=1
fi

if [[ -n "${PROV_LINE}" ]] && [[ "${PROV_LINE}" -le "${AUTH_LINE}" ]]; then
  echo "FAIL: loadProvisionJobFallback call (line ${PROV_LINE}) precedes ownership check (line ${AUTH_LINE})" >&2
  FAIL=1
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo ""
  echo "FAIL: one or more stack-state reads precede the ownership check" >&2
  exit 1
fi

echo ""
echo "PASS: ownership check (line ${AUTH_LINE}) precedes all stack-state reads"
