#!/usr/bin/env bash
# SEC-9: Trust auth + attestation signing change-lock.
#
# Verifies that the trust-auth and attestation-signing code paths remain
# unchanged from the pull request's validated target branch (origin/main/main
# outside pull-request contexts). Runs git diff to detect any semantic changes
# to the locked files:
#
#   - internal/trust/auth_instance.go
#   - internal/trust/auth_instance_test.go
#   - internal/attestations/kms_service.go
#   - internal/attestations/kms_service_internal_test.go
#   - internal/trust/attestations_issue.go
#
# A semantic change is any non-whitespace diff. Whitespace-only diffs pass.
# File additions or deletions are treated as semantic changes.
#
# Pass: no semantic diff in locked files. Fail: semantic diff detected.
# No partial credit. A modification to these files requires explicit
# governance event + audit-trust-and-safety walk.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-9
# Issue: equaltoai/lesser-host#401

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

echo "SEC-9: Trust auth + attestation signing change-lock"

LOCKED_FILES=(
  "internal/trust/auth_instance.go"
  "internal/trust/auth_instance_test.go"
  "internal/attestations/kms_service.go"
  "internal/attestations/kms_service_internal_test.go"
  "internal/trust/attestations_issue.go"
)

# Verify we're in a git worktree.
if ! command -v git >/dev/null 2>&1; then
  echo "BLOCKED: git is required" >&2
  exit 2
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "BLOCKED: not in a git worktree" >&2
  exit 2
fi

# Resolve the base ref deterministically.
# Sources shared helper with fetch-fallback for CI (shallow-clone) environments.
VERIFIER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./_resolve_base_ref.sh
source "${VERIFIER_DIR}/_resolve_base_ref.sh"

BASE_REF=""
if ! BASE_REF="$(resolve_base_ref)"; then
  echo "BLOCKED: cannot resolve required change-lock base ref" >&2
  exit 2
fi

BASE_SHA="$(git rev-parse "${BASE_REF}")"
HEAD_SHA="$(git rev-parse HEAD)"
echo "Base ref: ${BASE_REF} (${BASE_SHA:0:12})"
echo "HEAD: ${HEAD_SHA:0:12}"
echo ""

failed=0
checked=0

diff_payload_lines() {
  grep -E '^[+-]' | grep -vE '^(---|\+\+\+)' || true
}

payload_equals() {
  local actual="$1"
  shift
  local expected
  expected="$(printf '%s\n' "$@")"
  [[ "${actual}" == "${expected}" ]]
}

# AppTheory v1.17.0 / TableTheory v2.0.3 migration allowance:
# the instance-auth mechanism remains locked (sha256 bearer matching, raw-key
# handling, revocation, and audit behavior must not change), but the framework
# major-version migration necessarily updates TableTheory import paths and the
# AppTheory error constructor type. Keep this exception exact: any additional
# locked-file hunk still fails SEC-9.
is_allowed_framework_migration_diff() {
  local file="$1"
  local diff_output="$2"
  local payload
  payload="$(printf '%s\n' "${diff_output}" | diff_payload_lines)"

  case "${file}" in
    "internal/trust/attestations_issue.go")
      payload_equals "${payload}" \
        '-	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"' \
        '+	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"'
      ;;
    "internal/trust/auth_instance_test.go")
      payload_equals "${payload}" \
        '-	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"' \
        '-	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"' \
        '+	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"' \
        '+	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"'
      ;;
    "internal/trust/auth_instance.go")
      payload_equals "${payload}" \
        '-	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"' \
        '+	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"' \
        '-		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}' \
        '+		return "", newAppTheoryError("app.internal", "internal error")' \
        '-		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}' \
        '+		return "", newAppTheoryError("app.internal", "internal error")' \
        '-		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}' \
        '+		return "", newAppTheoryError("app.internal", "internal error")'
      ;;
    *)
      return 1
      ;;
  esac
}

for f in "${LOCKED_FILES[@]}"; do
  checked=$((checked + 1))

  if [[ ! -f "${f}" ]]; then
    echo "FAIL: locked file missing: ${f}" >&2
    failed=1
    continue
  fi

  # Get the diff for this file (excluding whitespace-only changes).
  # -w ignores whitespace-only changes.
  diff_output="$(git diff -w "${BASE_REF}"..HEAD -- "${f}" 2>/dev/null || true)"
  diff_full="$(git diff "${BASE_REF}"..HEAD -- "${f}" 2>/dev/null || true)"

  if [[ -z "${diff_full}" ]]; then
    echo "  ${f}: no diff"
  elif [[ -z "${diff_output}" ]]; then
    echo "  ${f}: whitespace-only diff (acceptable)"
  elif is_allowed_framework_migration_diff "${f}" "${diff_output}"; then
    echo "  ${f}: framework-major compatibility diff only (TableTheory v2/AppTheoryError migration; instance-auth semantics preserved)"
  else
    lines=""
    lines="$(printf '%s\n' "${diff_output}" | wc -l | tr -d ' ')"
    echo "FAIL: ${f}: semantic diff detected (${lines} non-whitespace lines changed)" >&2
    printf '%s\n' "${diff_output}" | head -n 20 >&2
    echo "  (first 20 lines shown; full diff in evidence output)" >&2
    failed=1
  fi
done

echo ""

if [[ "${checked}" -eq 0 ]]; then
  echo "BLOCKED: no locked files checked" >&2
  exit 2
fi

if [[ "${failed}" -ne 0 ]]; then
  echo "FAIL: trust auth + attestation signing change-lock violated" >&2
  exit 1
fi

echo "PASS: trust auth + attestation signing paths unchanged (${checked} files locked)"
