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

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

HANDLER_BODY_FILE="${TMP_DIR}/handlePortalGetInstanceStack.body.go"
HANDLER_CODE_FILE="${TMP_DIR}/handlePortalGetInstanceStack.body.code.go"

# ── Step 2: Extract only handlePortalGetInstanceStack's body ─────────────
extract_handler_body() {
  local source_file="$1"
  local body_file="$2"

  awk '
    function strip_go_non_code(line,    i, c, n, out) {
      out = ""
      n = length(line)
      for (i = 1; i <= n; i++) {
        c = substr(line, i, 1)

        if (block_comment) {
          if (c == "*" && substr(line, i + 1, 1) == "/") {
            block_comment = 0
            i++
          }
          continue
        }

        if (raw_string) {
          if (c == "`") {
            raw_string = 0
          }
          continue
        }

        if (double_string) {
          if (c == "\\") {
            i++
            continue
          }
          if (c == "\"") {
            double_string = 0
          }
          continue
        }

        if (rune_literal) {
          if (c == "\\") {
            i++
            continue
          }
          if (c == "'\''") {
            rune_literal = 0
          }
          continue
        }

        if (c == "/" && substr(line, i + 1, 1) == "/") {
          break
        }
        if (c == "/" && substr(line, i + 1, 1) == "*") {
          block_comment = 1
          i++
          continue
        }
        if (c == "`") {
          raw_string = 1
          continue
        }
        if (c == "\"") {
          double_string = 1
          continue
        }
        if (c == "'\''") {
          rune_literal = 1
          continue
        }

        out = out c
      }
      return out
    }

    function count_char(text, needle,    i, total) {
      total = 0
      for (i = 1; i <= length(text); i++) {
        if (substr(text, i, 1) == needle) {
          total++
        }
      }
      return total
    }

    $0 ~ /^func[[:space:]]+\(s \*Server\)[[:space:]]+handlePortalGetInstanceStack[[:space:]]*\(/ {
      in_handler = 1
    }

    in_handler {
      print
      code = strip_go_non_code($0)
      opens = count_char(code, "{")
      closes = count_char(code, "}")
      if (opens > 0) {
        opened = 1
      }
      depth += opens - closes
      if (opened && depth == 0) {
        completed = 1
        exit
      }
    }

    END {
      if (!in_handler) {
        print "FAIL: handlePortalGetInstanceStack function not found" > "/dev/stderr"
        exit 2
      }
      if (!completed) {
        print "FAIL: handlePortalGetInstanceStack closing brace not found" > "/dev/stderr"
        exit 3
      }
    }
  ' "${source_file}" >"${body_file}"
}

strip_non_code_lines() {
  local body_file="$1"
  local code_file="$2"

  awk '
    function strip_go_non_code(line,    i, c, n, out) {
      out = ""
      n = length(line)
      for (i = 1; i <= n; i++) {
        c = substr(line, i, 1)

        if (block_comment) {
          if (c == "*" && substr(line, i + 1, 1) == "/") {
            block_comment = 0
            i++
          }
          continue
        }

        if (raw_string) {
          if (c == "`") {
            raw_string = 0
          }
          continue
        }

        if (double_string) {
          if (c == "\\") {
            i++
            continue
          }
          if (c == "\"") {
            double_string = 0
          }
          continue
        }

        if (rune_literal) {
          if (c == "\\") {
            i++
            continue
          }
          if (c == "'\''") {
            rune_literal = 0
          }
          continue
        }

        if (c == "/" && substr(line, i + 1, 1) == "/") {
          break
        }
        if (c == "/" && substr(line, i + 1, 1) == "*") {
          block_comment = 1
          i++
          continue
        }
        if (c == "`") {
          raw_string = 1
          continue
        }
        if (c == "\"") {
          double_string = 1
          continue
        }
        if (c == "'\''") {
          rune_literal = 1
          continue
        }

        out = out c
      }
      return out
    }

    { print strip_go_non_code($0) }
  ' "${body_file}" >"${code_file}"
}

extract_handler_body "${HANDLER_FILE}" "${HANDLER_BODY_FILE}"
strip_non_code_lines "${HANDLER_BODY_FILE}" "${HANDLER_CODE_FILE}"

echo "Scoped handler body: handlePortalGetInstanceStack ($(wc -l <"${HANDLER_BODY_FILE}") lines)"

# ── Step 3: requireInstanceAccess must be an in-body call ────────────────
mapfile -t AUTH_LINES < <(grep -nE '(^|[^[:alnum:]_])requireInstanceAccess[[:space:]]*\(' "${HANDLER_CODE_FILE}" | cut -d: -f1)
if [[ "${#AUTH_LINES[@]}" -eq 0 ]]; then
  echo "FAIL: requireInstanceAccess call not found in handlePortalGetInstanceStack body" >&2
  exit 1
fi

AUTH_LINE="${AUTH_LINES[0]}"
echo "Ownership check: requireInstanceAccess call at handler-body line ${AUTH_LINE}"
if [[ "${#AUTH_LINES[@]}" -gt 1 ]]; then
  echo "Additional requireInstanceAccess calls at handler-body lines: ${AUTH_LINES[*]:1}"
fi

# ── Step 4: Find stack-state DB read calls in the handler body ───────────
# These are the function calls (not definitions) that perform stack-state
# database reads. Matching uses the comment/string-stripped handler body so
# mentions outside executable code cannot satisfy the verifier.

# categorizeLatestUpdateJobs queries update jobs via GSI1.
mapfile -t CAT_LINES < <(grep -nE '(^|[^[:alnum:]_])categorizeLatestUpdateJobs[[:space:]]*\(' "${HANDLER_CODE_FILE}" | cut -d: -f1)
# loadProvisionJobFallback loads the initial provision job from the store.
mapfile -t PROV_LINES < <(grep -nE '(^|[^[:alnum:]_])loadProvisionJobFallback[[:space:]]*\(' "${HANDLER_CODE_FILE}" | cut -d: -f1)

# ── Step 5: Emit evidence ────────────────────────────────────────────────
echo ""
echo "Stack-state read call sites:"
if [[ "${#CAT_LINES[@]}" -gt 0 ]]; then
  for line in "${CAT_LINES[@]}"; do
    echo "  categorizeLatestUpdateJobs call at handler-body line ${line}"
  done
else
  echo "  categorizeLatestUpdateJobs call: NOT FOUND"
fi
if [[ "${#PROV_LINES[@]}" -gt 0 ]]; then
  for line in "${PROV_LINES[@]}"; do
    echo "  loadProvisionJobFallback call at handler-body line ${line}"
  done
else
  echo "  loadProvisionJobFallback call: NOT FOUND"
fi

if [[ "${#CAT_LINES[@]}" -eq 0 ]] && [[ "${#PROV_LINES[@]}" -eq 0 ]]; then
  echo "FAIL: no stack-state read calls found in handlePortalGetInstanceStack body" >&2
  exit 1
fi

# ── Step 6: Ownership check must precede all stack-state reads ───────────
FAIL=0

for line in "${CAT_LINES[@]}"; do
  if [[ "${line}" -le "${AUTH_LINE}" ]]; then
    echo "FAIL: categorizeLatestUpdateJobs call (handler-body line ${line}) precedes ownership check (handler-body line ${AUTH_LINE})" >&2
    FAIL=1
  fi
done

for line in "${PROV_LINES[@]}"; do
  if [[ "${line}" -le "${AUTH_LINE}" ]]; then
    echo "FAIL: loadProvisionJobFallback call (handler-body line ${line}) precedes ownership check (handler-body line ${AUTH_LINE})" >&2
    FAIL=1
  fi
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo ""
  echo "FAIL: one or more stack-state reads precede the ownership check" >&2
  exit 1
fi

echo ""
echo "PASS: ownership check (handler-body line ${AUTH_LINE}) precedes all stack-state reads"
