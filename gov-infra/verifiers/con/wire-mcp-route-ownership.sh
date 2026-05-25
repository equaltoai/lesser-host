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
#   2. cdk/lib/provision-runner/build.sh (dispatcher) — asserts the
#      RUN_MODE=lesser-mcp branch exists, the INLINE marker references
#      build-lesser-mcp.sh (not a different MCP script), and no MCP wiring
#      URLs or host-owned routes appear in the dispatcher itself.
#   3. cdk/.build/provision-runner-buildspec.json (rendered buildspec) —
#      asserts the inlined build-lesser-mcp.sh content appears in the
#      rendered output (proving the INLINE mechanism works) and no
#      host-owned MCP routes appear.  Built via CDK synth from
#      cdk/lib/provision-runner-buildspec.ts.  If the rendered artifact is
#      absent, the verifier synthesizes it deterministically from cdk/
#      (tsc + cdk synth) before inspecting; a synth failure exits 2
#      (BLOCKED), and continued absence after synth exits 1 (FAIL).
#      There is no "artifact absent but PASS" fallback.
#   4. The Go code in internal/provisionworker/advance_body_mcp.go — asserts
#      the mode "lesser-mcp" is used to start the deploy runner (line 143).
#
# The verifier does NOT allow:
#   - A host-owned MCP route (any URL containing lesser.host)
#   - A body-side MCP-wiring endpoint
#   - A static/hardcoded domain in the MCP URL
#   - A dispatcher that omits the RUN_MODE=lesser-mcp branch
#   - A dispatcher that contains inline MCP wiring (should delegate via INLINE)
#
# Pass: canonical lesser route is the only MCP-wiring target, dispatcher
#   delegates correctly, rendered buildspec confirms inlining.
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
DISPATCHER="${PROVISION_RUNNER_DIR}/build.sh"
ADVANCE_MCP_GO="internal/provisionworker/advance_body_mcp.go"
BUILDSPEC_JSON="cdk/.build/provision-runner-buildspec.json"

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

# --- Check 2: Dispatcher (build.sh) delegates MCP wiring correctly ---
if [[ ! -f "${DISPATCHER}" ]]; then
  echo "FAIL: dispatcher not found: ${DISPATCHER}" >&2
  fail=1
else
  echo "--- Dispatcher: ${DISPATCHER} ---"

  # Must have the RUN_MODE=lesser-mcp branch.
  if grep -q 'elif.*RUN_MODE.*"lesser-mcp"' "${DISPATCHER}"; then
    echo "  PASS: dispatcher has RUN_MODE=lesser-mcp branch"
  else
    echo "FAIL: dispatcher missing RUN_MODE=lesser-mcp branch" >&2
    fail=1
  fi

  # Must have the INLINE marker for build-lesser-mcp.sh.
  if grep -q '### INLINE: build-lesser-mcp\.sh ###' "${DISPATCHER}"; then
    echo "  PASS: dispatcher has INLINE marker for build-lesser-mcp.sh"
  else
    echo "FAIL: dispatcher missing INLINE marker for build-lesser-mcp.sh" >&2
    fail=1
  fi

  # Must NOT contain host-owned MCP routes (lesser.host).
  if grep -qi 'lesser\.host' "${DISPATCHER}"; then
    echo "FAIL: dispatcher references lesser.host — MCP wiring must target tenant API" >&2
    fail=1
  else
    echo "  PASS: no host-owned route reference in dispatcher"
  fi

  # Must not contain inline mcp_url references — the dispatcher should
  # delegate MCP wiring to build-lesser-mcp.sh via the INLINE marker.
  if grep -qE 'mcp_url' "${DISPATCHER}"; then
    echo "FAIL: dispatcher contains mcp_url — MCP URL must live in build-lesser-mcp.sh, not the dispatcher" >&2
    fail=1
  else
    echo "  PASS: dispatcher delegates MCP wiring to INLINE-d script (no inline mcp_url)"
  fi

  echo ""
fi

# --- Check 3: Rendered buildspec confirms INLINE mechanism works ---
# When the rendered artifact is absent, synthesize it deterministically from cdk/.
# If synthesis fails, exit 2 (BLOCKED) — the verifier cannot judge correctness
# without inspecting the rendered output.
if [[ ! -f "${BUILDSPEC_JSON}" ]]; then
  echo "--- Rendered buildspec: not found — synthesizing from cdk/ ---"

  if [[ ! -d cdk/node_modules ]]; then
    echo "  Installing CDK dependencies (npm ci)..."
    if (cd cdk && npm ci --no-audit --no-fund); then
      echo "  CDK dependencies installed."
    else
      echo "BLOCKED: CDK dependency installation failed — cannot synthesize buildspec" >&2
      exit 2
    fi
  fi

  echo "  Running CDK synth (tsc + cdk synth)..."
  if (cd cdk && npm run synth); then
    echo "  CDK synth succeeded."
  else
    echo "BLOCKED: CDK synth failed — cannot verify rendered buildspec" >&2
    exit 2
  fi

  if [[ ! -f "${BUILDSPEC_JSON}" ]]; then
    echo "FAIL: rendered buildspec still absent after CDK synth —" >&2
    echo "  provision-runner-buildspec.ts did not produce the expected output file." >&2
    fail=1
  else
    echo "  Rendered buildspec generated successfully."
  fi
  echo ""
fi

if [[ -f "${BUILDSPEC_JSON}" ]]; then
  echo "--- Rendered buildspec: ${BUILDSPEC_JSON} ---"

  # The rendered buildspec must contain the inlined MCP URL template,
  # proving that buildspec TS correctly inlined build-lesser-mcp.sh.
  if grep -qF 'https://api.$STAGE_DOMAIN/mcp/{actor}' "${BUILDSPEC_JSON}"; then
    echo "  PASS: rendered buildspec contains canonical MCP URL template (INLINE mechanism verified)"
  else
    echo "FAIL: rendered buildspec does not contain canonical MCP URL template — INLINE mechanism broken or build-lesser-mcp.sh not inlined" >&2
    fail=1
  fi

  # Must NOT contain host-owned routes in the rendered commands.
  if grep -qi 'lesser\.host' "${BUILDSPEC_JSON}"; then
    echo "FAIL: rendered buildspec contains lesser.host reference" >&2
    failure_lines="$(grep -ni 'lesser\.host' "${BUILDSPEC_JSON}" | head -n 5)"
    printf '%s\n' "${failure_lines}" >&2
    fail=1
  else
    echo "  PASS: no host-owned routes in rendered buildspec"
  fi

  echo ""
fi

# --- Check 4: Go code uses the correct mode ---
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
