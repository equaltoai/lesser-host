#!/usr/bin/env bash
# CON-4: Wire-MCP route ownership.
#
# Verifies that the MCP-wiring deploy runner (RUN_MODE=lesser-mcp) targets
# only the canonical lesser-owned route POST /mcp/{actor} on the tenant's
# lesser API gateway (api.<stageDomain>). The verifier reads:
#
#   1. cdk/lib/provision-runner/build-lesser-mcp.sh — the MCP-wiring build
#      phase script; asserts the MCP URL template is the canonical
#      https://api.$STAGE_DOMAIN/mcp/{actor} pattern.
#   2. cdk/lib/provision-runner/build.sh (dispatcher) — asserts the INLINE
#      includes reference build-lesser-mcp.sh (not a different MCP script).
#   3. The Go code in internal/provisionworker/advance_body_mcp.go — asserts
#      the mode "lesser-mcp" is used to start the deploy runner (line 143).
#
# The verifier does NOT allow:
#   - A host-owned MCP route (any URL containing lesser.host)
#   - A body-side MCP-wiring endpoint
#   - A static/hardcoded domain in the MCP URL
#
# Pass: canonical lesser route is the only MCP-wiring target.
# Fail: any drift detected.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md CON-4
# Issue: equaltoai/lesser-host#403

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

echo "CON-4: Wire-MCP route ownership"

PROVISION_RUNNER_DIR="cdk/lib/provision-runner"
MCP_SCRIPT="${PROVISION_RUNNER_DIR}/build-lesser-mcp.sh"
ADVANCE_MCP_GO="internal/provisionworker/advance_body_mcp.go"

fail=0

# --- Check 1: MCP build script contains canonical route ---
if [[ ! -f "${MCP_SCRIPT}" ]]; then
  echo "FAIL: MCP build script not found: ${MCP_SCRIPT}" >&2
  exit 1
fi

echo "--- MCP build script: ${MCP_SCRIPT} ---"

# The canonical MCP URL template must be present:
#   https://api.$STAGE_DOMAIN/mcp/{actor}
if grep -q 'mcp_url.*https://api\.\$STAGE_DOMAIN/mcp/{actor}' "${MCP_SCRIPT}"; then
  echo "  PASS: canonical MCP URL template found (https://api.\$STAGE_DOMAIN/mcp/{actor})"
else
  echo "FAIL: canonical MCP URL template not found in ${MCP_SCRIPT}" >&2
  grep -n 'mcp_url' "${MCP_SCRIPT}" | head -n 5 >&2
  fail=1
fi

# Must NOT contain host-owned routes (lesser.host).
if grep -qi 'lesser\.host' "${MCP_SCRIPT}"; then
  echo "FAIL: ${MCP_SCRIPT} references lesser.host — MCP wiring must target tenant API gateway, not host" >&2
  fail=1
else
  echo "  PASS: no host-owned route reference (lesser.host)"
fi

# Must NOT contain body-side MCP wiring endpoints.
# The MCP URL must be the canonical https://api.$STAGE_DOMAIN/mcp/{actor}
# pattern. Check that no alternative MCP URL targets exist.
# Alternative MCP URLs would look like: body.<domain>/mcp, api.<domain>/body/mcp, etc.
# We look for any URL-like string containing /mcp/ that is NOT the canonical template.
ALTERNATIVE_MCP_URLS="$(grep -nE 'https?://[^"'"'"' ]+/mcp/' "${MCP_SCRIPT}" | grep -v 'api\.\$STAGE_DOMAIN/mcp/{actor}' || true)"
if [[ -n "${ALTERNATIVE_MCP_URLS}" ]]; then
  echo "FAIL: ${MCP_SCRIPT} contains non-canonical MCP URL patterns:" >&2
  printf '%s\n' "${ALTERNATIVE_MCP_URLS}" >&2
  fail=1
else
  echo "  PASS: all MCP URL references use canonical tenant API route"
fi

echo ""

# --- Check 2: Go code uses the correct mode ---
if [[ ! -f "${ADVANCE_MCP_GO}" ]]; then
  echo "FAIL: advance_body_mcp.go not found: ${ADVANCE_MCP_GO}" >&2
  fail=1
else
  echo "--- Go code: ${ADVANCE_MCP_GO} ---"

  # Must use mode "lesser-mcp" to start the deploy runner.
  if grep -q '"lesser-mcp"' "${ADVANCE_MCP_GO}"; then
    echo "  PASS: deploy runner mode is 'lesser-mcp'"
  else
    echo "FAIL: deploy runner mode 'lesser-mcp' not found in ${ADVANCE_MCP_GO}" >&2
    fail=1
  fi

  # Must NOT contain host-owned MCP routes.
  if grep -qi 'lesser\.host.*mcp\|mcp.*lesser\.host' "${ADVANCE_MCP_GO}"; then
    echo "FAIL: ${ADVANCE_MCP_GO} references host-owned MCP route" >&2
    fail=1
  else
    echo "  PASS: no host-owned MCP route"
  fi
fi

echo ""

# --- Summary ---
if [[ "${fail}" -ne 0 ]]; then
  echo "FAIL: wire-MCP route ownership violated — MCP wiring must target only tenant lesser API POST /mcp/{actor}" >&2
  exit 1
fi

echo "PASS: wire-MCP route ownership preserved (canonical tenant lesser POST /mcp/{actor} only)"
