#!/usr/bin/env bash
# SEC-5: Web CSP byte-string integrity.
#
# Extracts the `webCsp` and `safeAppCsp` arrays from cdk/lib/lesser-host-stack.ts,
# joins each array with '; ' (per the CDK .join('; ') expression), and compares
# the resulting byte-string against the current locked-in value.
#
# Full points: exact byte-string match (same directives, same order, same values,
#   same join separator, same count).  Zero points: any deviation — missing
#   directive, extra directive, reordered directive, changed value, changed
#   join separator, changed directive count, or any other drift.
#
# The locked-in expected values are the current CDK-synth / CDK-test values
# (cdk/test/lesser-host-stack.test.ts EXPECTED_WEB_CSP / EXPECTED_SAFE_APP_CSP).
#
# Evidence: prints every directive for diagnostics; fails with byte-position diff
#   on mismatch so the exact deviation is visible.
#
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-5
# Issue: equaltoai/lesser-host#397

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
STACK_FILE="${REPO_ROOT}/cdk/lib/lesser-host-stack.ts"

# ── Locked-in expected byte-strings ──────────────────────────────────────
# These are the current CDK source + CDK test values.  The CDK array elements
# are joined with '; ' (per .join('; ')).  Any deviation from these exact
# byte-strings fails the verifier.
EXPECTED_WEB_CSP="default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data: blob:; font-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; manifest-src 'self'"

EXPECTED_SAFE_APP_CSP="default-src 'none'; base-uri 'none'; object-src 'none'; frame-ancestors https://safe.global https://*.safe.global; form-action 'self'; img-src 'self' data: blob:; font-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; manifest-src 'self'"

EXPECTED_COUNT=11

if [[ ! -f "${STACK_FILE}" ]]; then
  echo "BLOCKED: missing CDK stack file: ${STACK_FILE}" >&2
  exit 2
fi

# ── Extract CSP array elements from the CDK TypeScript source ────────────
# Reads lines between "const VAR = [" and "].join(" and prints each
# directive element with quotes and trailing comma stripped.
# Output: one directive per line (e.g. "default-src 'none'").
extract_csp_elements() {
  local var_name="$1"
  awk -v var="${var_name}" '
    BEGIN { found=0; }
    $0 ~ "const[[:space:]]+" var "[[:space:]]*=[[:space:]]*\\[" { found=1; next; }
    found {
      if ($0 ~ /\]\.join/)   { exit; }
      if ($0 ~ /^[[:space:]]*\];?[[:space:]]*$/) { next; }
      gsub(/^[[:space:]]+/, "");
      gsub(/[[:space:]]*,[[:space:]]*$/, "");
      gsub(/^"/, "");  gsub(/"$/, "");
      if ($0 != "") { print; }
    }
  ' "${STACK_FILE}"
}

# ── Validate a single CSP policy ─────────────────────────────────────────
# Args: label, expected_byte_string
validate_csp_bytes() {
  local csp_label="$1"
  local expected="$2"
  local fail=0

  local elements
  elements="$(extract_csp_elements "${csp_label}")"

  if [[ -z "${elements}" ]]; then
    echo "FAIL: could not extract ${csp_label} from ${STACK_FILE}" >&2
    return 1
  fi

  # Collect directive strings and print evidence.
  local directives=()
  local count=0
  while IFS= read -r line; do
    [[ -z "${line}" ]] && continue
    directives+=("${line}")
    count=$((count + 1))
    local dname="${line%% *}"
    echo "  ${count}. ${dname}: ${line#* }"
  done <<< "${elements}"

  # Check directive count.
  if [[ "${count}" -ne "${EXPECTED_COUNT}" ]]; then
    echo "FAIL: ${csp_label} has ${count} directives, expected ${EXPECTED_COUNT}" >&2
    fail=1
  fi

  # Build the actual joined byte-string (matching CDK .join('; ')).
  local actual
  actual="${directives[0]}"
  local i
  for ((i=1; i<count; i++)); do
    actual+="; ${directives[$i]}"
  done

  # Byte-string comparison — this is the definitive pass/fail gate.
  if [[ "${actual}" != "${expected}" ]]; then
    echo "FAIL: ${csp_label} byte-string mismatch" >&2
    echo "Expected (${#expected} bytes):" >&2
    echo "  ${expected}" >&2
    echo "Actual (${#actual} bytes):" >&2
    echo "  ${actual}" >&2

    # Pinpoint the first differing byte.
    local min_len="${#actual}"
    if [[ "${#expected}" -lt "${min_len}" ]]; then min_len="${#expected}"; fi
    local diff_pos=-1
    local j
    for ((j=0; j<min_len; j++)); do
      if [[ "${actual:$j:1}" != "${expected:$j:1}" ]]; then
        diff_pos=$j
        break
      fi
    done
    if [[ "${diff_pos}" -ge 0 ]]; then
      echo "First difference at byte ${diff_pos}:" >&2
      echo "  expected: '${expected:$diff_pos:40}'" >&2
      echo "  actual:   '${actual:$diff_pos:40}'" >&2
    elif [[ "${#actual}" -ne "${#expected}" ]]; then
      echo "Lengths differ: expected ${#expected}, actual ${#actual}" >&2
    fi
    fail=1
  fi

  return "${fail}"
}

echo "SEC-5: CSP byte-string integrity"
echo "Stack file: ${STACK_FILE}"
echo ""

# Validate webCsp.
echo "--- webCsp ---"
validate_csp_bytes "webCsp" "${EXPECTED_WEB_CSP}" || exit 1

echo ""

# Validate safeAppCsp.
echo "--- safeAppCsp ---"
validate_csp_bytes "safeAppCsp" "${EXPECTED_SAFE_APP_CSP}" || exit 1

echo ""
echo "PASS: CSP byte-string integrity verified — webCsp + safeAppCsp exact match"
