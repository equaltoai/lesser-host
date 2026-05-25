#!/usr/bin/env bash
# SEC-7: OAC form transport integrity.
#
# Verifies three invariants on the built web/dist/ output:
#
#   A. Every <form data-facetheory-oac-form> has same-origin action +
#      mutating method (POST/PUT/PATCH/DELETE — never GET)
#   B. The OAC marker does NOT appear on forms targeting bearer-auth API
#      path prefixes (/api/*, /auth/*, /setup/*, /.well-known/*, /attestations/*)
#   C. The client bundle imports startAwsOacFormTransport from
#      @theory-cloud/facetheory/client
#
# Uses the M0.16 build-time verifier script
# (web/scripts/verify-oac-form-integrity.mjs). Runs against the already-built
# web/dist/ output (from SEC-6 or a prior build).
#
# Pass: all three invariants satisfied. Fail: any violation detected.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-7
# Issue: equaltoai/lesser-host#399

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WEB_DIR="${REPO_ROOT}/web"
DIST_DIR="${WEB_DIR}/dist"
VERIFY_SCRIPT="${WEB_DIR}/scripts/verify-oac-form-integrity.mjs"

echo "SEC-7: OAC form transport integrity"

if [[ ! -d "${WEB_DIR}" ]]; then
  echo "BLOCKED: web/ directory not found at ${WEB_DIR}" >&2
  exit 2
fi

if [[ ! -f "${VERIFY_SCRIPT}" ]]; then
  echo "BLOCKED: verify script not found: ${VERIFY_SCRIPT}" >&2
  exit 2
fi

if [[ ! -d "${DIST_DIR}" ]]; then
  echo "BLOCKED: web/dist/ not found at ${DIST_DIR}; run web build first (SEC-6)" >&2
  exit 2
fi

if ! command -v node >/dev/null 2>&1; then
  echo "BLOCKED: node is required" >&2
  exit 2
fi

echo "Verifying OAC form transport integrity against web/dist/..."

set +e
node "${VERIFY_SCRIPT}" 2>&1
ec=$?
set -e

if [[ $ec -eq 2 ]]; then
  echo "" >&2
  echo "BLOCKED: OAC form check could not complete (missing dist/ or no HTML files)" >&2
  exit 2
elif [[ $ec -ne 0 ]]; then
  echo "" >&2
  echo "FAIL: OAC form transport integrity violations detected" >&2
  exit 1
fi

echo ""
echo "PASS: OAC form transport integrity verified (same-origin, mutating methods, no bearer-auth paths marked, transport imported)"
