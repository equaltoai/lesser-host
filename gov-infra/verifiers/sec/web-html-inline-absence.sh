#!/usr/bin/env bash
# SEC-6: Built HTML inline absence.
#
# Builds the web/ project and verifies that every built HTML file under
# web/dist/ satisfies strict no-inline CSP policy:
#
#   1. No <script> element with inline body text
#   2. No <style> element with inline body text
#   3. No inline event handler attributes (onclick=, onload=, etc.)
#   4. No data: script URLs
#   5. All <script src="..."> and <link rel=stylesheet href="..."> URLs
#      are same-origin
#
# Uses the M0.15 build-time verifier script (web/scripts/verify-no-inline-html.mjs)
# which is already chained into `npm run build`. This verifier invokes the full
# web build to ensure the built output is scanned.
#
# Pass: all constraints satisfied. Fail: any violation detected.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-6
# Issue: equaltoai/lesser-host#398

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WEB_DIR="${REPO_ROOT}/web"
VERIFY_SCRIPT="${WEB_DIR}/scripts/verify-no-inline-html.mjs"

echo "SEC-6: Built HTML inline absence"

if [[ ! -d "${WEB_DIR}" ]]; then
  echo "BLOCKED: web/ directory not found at ${WEB_DIR}" >&2
  exit 2
fi

if [[ ! -f "${WEB_DIR}/package.json" ]]; then
  echo "BLOCKED: web/package.json not found" >&2
  exit 2
fi

if ! command -v node >/dev/null 2>&1; then
  echo "BLOCKED: node is required" >&2
  exit 2
fi

if ! command -v npm >/dev/null 2>&1; then
  echo "BLOCKED: npm is required" >&2
  exit 2
fi

# Run the full web build which chains verify-no-inline-html.mjs.
# npm run build = vite build && build-sidecars.mjs && verify-no-inline-html.mjs && verify-oac-form-integrity.mjs
echo "Building web/ and verifying no inline HTML..."
echo ""

set +e
(
  cd "${WEB_DIR}"
  npm ci --no-audit --no-fund 2>&1
  npm run build 2>&1
)
ec=$?
set -e

if [[ $ec -eq 2 ]]; then
  echo "" >&2
  echo "BLOCKED: web build could not complete (missing dependencies or build error)" >&2
  exit 2
elif [[ $ec -ne 0 ]]; then
  echo "" >&2
  echo "FAIL: web build failed or inline HTML violations detected" >&2
  exit 1
fi

echo ""
echo "PASS: built HTML contains no inline scripts, styles, event handlers, data: URLs, or cross-origin resources"
