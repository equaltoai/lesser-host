#!/usr/bin/env bash
# SEC-5: Web CSP byte-string integrity.
#
# Parses the `webCsp` and `safeAppCsp` arrays in cdk/lib/lesser-host-stack.ts
# and validates every directive against the strict single-origin CSP policy:
#
#   - Allowed directive names: default-src, base-uri, object-src,
#     frame-ancestors, form-action, img-src, font-src, style-src,
#     script-src, connect-src, manifest-src
#   - script-src value is exactly 'self' (no 'unsafe-inline', 'unsafe-eval',
#     nonce-, sha256-, or third-party origins)
#   - style-src value is exactly 'self' (same restrictions)
#   - connect-src value is exactly 'self' (same restrictions)
#   - default-src is exactly 'none'
#   - safeAppCsp.frame-ancestors matches the Safe allowlist:
#     must contain https://safe.global and https://*.safe.global
#     (must not contain other origins or 'none')
#
# Pass: all directives match the allowlist. Fail: any directive deviates.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-5
# Issue: equaltoai/lesser-host#397

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
STACK_FILE="${REPO_ROOT}/cdk/lib/lesser-host-stack.ts"

if [[ ! -f "${STACK_FILE}" ]]; then
  echo "BLOCKED: missing CDK stack file: ${STACK_FILE}" >&2
  exit 2
fi

# Extract the webCsp array content between the array brackets.
# The arrays span multiple lines; we extract the source text and parse.
extract_csp_directives() {
  local var_name="$1"
  local tmpfile
  tmpfile="$(mktemp)"

  # Extract the array body for the given variable name.
  # Matches from "const var_name = [" to the closing "].join('; ');"
  awk -v var="${var_name}" '
    BEGIN { found=0; depth=0; }
    $0 ~ "const[[:space:]]+" var "[[:space:]]*=[[:space:]]*\\[" { found=1; depth=1; next; }
    found && depth > 0 {
      if ($0 ~ /^\];?$/) { depth--; if (depth == 0) exit; next; }
      if ($0 ~ /\]\.join/) { exit; }
      gsub(/^[[:space:]]+/, "");
      gsub(/[[:space:]]*,[[:space:]]*$/, "");
      gsub(/^"/, ""); gsub(/"$/, "");
      print;
    }
    found && $0 ~ /^[[:space:]]*"[^"]*"[[:space:]]*,?[[:space:]]*$/ {
      gsub(/^[[:space:]]+/, "");
      gsub(/[[:space:]]*,[[:space:]]*$/, "");
      gsub(/^"/, ""); gsub(/"$/, "");
      print;
    }
  ' "${STACK_FILE}" > "${tmpfile}"

  cat "${tmpfile}"
  rm -f "${tmpfile}"
}

# Allowed directive names for host's strict CSP.
ALLOWED_DIRECTIVES=(
  "default-src"
  "base-uri"
  "object-src"
  "frame-ancestors"
  "form-action"
  "img-src"
  "font-src"
  "style-src"
  "script-src"
  "connect-src"
  "manifest-src"
)

is_allowed_directive_name() {
  local name="$1"
  for d in "${ALLOWED_DIRECTIVES[@]}"; do
    [[ "${name}" == "${d}" ]] && return 0
  done
  return 1
}

# Validate a single CSP directive value.
# Returns 0 if valid, 1 with reason on stderr if invalid.
validate_directive_value() {
  local directive="$1"
  local value="$2"
  local csp_label="$3"

  case "${directive}" in
    script-src)
      if [[ "${value}" != "'self'" ]]; then
        echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'self'" >&2
        return 1
      fi
      ;;
    style-src)
      if [[ "${value}" != "'self'" ]]; then
        echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'self'" >&2
        return 1
      fi
      ;;
    connect-src)
      if [[ "${value}" != "'self'" ]]; then
        echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'self'" >&2
        return 1
      fi
      ;;
    default-src)
      if [[ "${value}" != "'none'" ]]; then
        echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'none'" >&2
        return 1
      fi
      ;;
    base-uri)
      if [[ "${value}" != "'none'" ]]; then
        echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'none'" >&2
        return 1
      fi
      ;;
    object-src)
      if [[ "${value}" != "'none'" ]]; then
        echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'none'" >&2
        return 1
      fi
      ;;
    frame-ancestors)
      # webCsp: must be 'none'.
      if [[ "${csp_label}" == "webCsp" ]]; then
        if [[ "${value}" != "'none'" ]]; then
          echo "FAIL: ${csp_label} ${directive} value is '${value}' — must be exactly 'none'" >&2
          return 1
        fi
      elif [[ "${csp_label}" == "safeAppCsp" ]]; then
        # Must contain https://safe.global and https://*.safe.global.
        # May also contain 'self'. Must not contain 'none' or other origins.
        if [[ "${value}" == "'none'" ]]; then
          echo "FAIL: ${csp_label} ${directive} value is 'none' — Safe app requires frame-ancestors allowlist" >&2
          return 1
        fi
        if ! printf '%s' "${value}" | grep -q 'https://safe\.global'; then
          echo "FAIL: ${csp_label} ${directive} missing https://safe.global in '${value}'" >&2
          return 1
        fi
        if ! printf '%s' "${value}" | grep -q 'https://\*\.safe\.global'; then
          echo "FAIL: ${csp_label} ${directive} missing https://*.safe.global in '${value}'" >&2
          return 1
        fi
        # No other origins permitted beyond 'self' and the two Safe domains.
        local remaining
        remaining="$(printf '%s' "${value}" | sed "s/'self'//g; s/https:\/\/safe\.global//g; s/https:\/\/\*\.safe\.global//g" | xargs)"
        if [[ -n "${remaining}" ]]; then
          echo "FAIL: ${csp_label} ${directive} contains unexpected origins: '${remaining}'" >&2
          return 1
        fi
      fi
      ;;
    *)
      # Other directives (img-src, font-src, form-action, manifest-src):
      # no further structural requirement beyond being a valid directive name.
      ;;
  esac
  return 0
}

validate_csp() {
  local csp_label="$1"
  local csp_content="$2"
  local fail=0

  # Parse each directive line (format: "directive-name value")
  while IFS= read -r line; do
    [[ -z "${line}" ]] && continue

    # Split into directive name and value.
    local directive_name="${line%% *}"
    local directive_value="${line#* }"

    if [[ "${directive_name}" == "${directive_value}" ]]; then
      echo "FAIL: ${csp_label} malformed directive: '${line}'" >&2
      fail=1
      continue
    fi

    echo "  ${directive_name}: ${directive_value}"

    # Check directive name is allowed.
    if ! is_allowed_directive_name "${directive_name}"; then
      echo "FAIL: ${csp_label} unknown directive: '${directive_name}'" >&2
      fail=1
      continue
    fi

    # Structural checks.
    if [[ "${directive_value}" =~ "'unsafe-inline'" ]] || [[ "${directive_value}" =~ "'unsafe-eval'" ]]; then
      echo "FAIL: ${csp_label} ${directive_name} contains 'unsafe-inline' or 'unsafe-eval' — forbidden" >&2
      fail=1
    fi
    if [[ "${directive_value}" =~ nonce- ]] || [[ "${directive_value}" =~ sha256- ]] || [[ "${directive_value}" =~ sha384- ]] || [[ "${directive_value}" =~ sha512- ]]; then
      echo "FAIL: ${csp_label} ${directive_name} contains nonce- or sha- hash — forbidden" >&2
      fail=1
    fi

    # Validate specific directive values.
    validate_directive_value "${directive_name}" "${directive_value}" "${csp_label}" || fail=1
  done <<< "${csp_content}"

  return "${fail}"
}

echo "SEC-5: CSP byte-string integrity"
echo "Stack file: ${STACK_FILE}"
echo ""

# Extract and validate webCsp.
echo "--- webCsp ---"
web_csp="$(extract_csp_directives "webCsp")"
if [[ -z "${web_csp}" ]]; then
  echo "FAIL: could not extract webCsp from ${STACK_FILE}" >&2
  exit 1
fi
validate_csp "webCsp" "${web_csp}" || exit 1

echo ""

# Extract and validate safeAppCsp.
echo "--- safeAppCsp ---"
safe_csp="$(extract_csp_directives "safeAppCsp")"
if [[ -z "${safe_csp}" ]]; then
  echo "FAIL: could not extract safeAppCsp from ${STACK_FILE}" >&2
  exit 1
fi
validate_csp "safeAppCsp" "${safe_csp}" || exit 1

echo ""
echo "PASS: CSP byte-string integrity verified (webCsp + safeAppCsp all directives compliant)"
