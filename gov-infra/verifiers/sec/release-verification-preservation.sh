#!/usr/bin/env bash
# SEC-10: Release-verification change-lock.
#
# Verifies that the two-channel release verification code paths remain
# unchanged from origin/main. Runs git diff to detect any semantic changes
# to the locked files:
#
#   - internal/provisionworker/release_compatibility.go
#   - internal/provisionworker/release_compatibility_lesser_body.go
#   - internal/provisionworker/release_preflight.go
#   - internal/provisionworker/release_preflight_lesser_body.go
#   - scripts/managed-release-certification/main.go
#   - scripts/managed-release-readiness/**/*.go
#
# This locks the checksum-based consumer release verification gate and the
# minimum-supported-release-version constants. Bypassing these files would
# allow unverified lesser/body artifacts through the provisioning pipeline.
#
# A semantic change is any non-whitespace diff. Whitespace-only diffs pass.
#
# Pass: no semantic diff in locked files. Fail: semantic diff detected.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-10
# Issue: equaltoai/lesser-host#402

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

echo "SEC-10: Release-verification change-lock"

# Locked files (exact paths).
LOCKED_FILES=(
  "internal/provisionworker/release_compatibility.go"
  "internal/provisionworker/release_compatibility_internal_test.go"
  "internal/provisionworker/release_compatibility_lesser_body.go"
  "internal/provisionworker/release_compatibility_lesser_body_internal_test.go"
  "internal/provisionworker/release_preflight.go"
  "internal/provisionworker/release_preflight_internal_test.go"
  "internal/provisionworker/release_preflight_lesser_body.go"
  "scripts/managed-release-certification/main.go"
  "scripts/managed-release-certification/main_test.go"
  "scripts/managed-release-readiness/main.go"
  "scripts/managed-release-readiness/main_test.go"
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

# Determine the base ref.
BASE_REF="origin/main"
if ! git rev-parse --verify "${BASE_REF}" >/dev/null 2>&1; then
  if git rev-parse --verify "main" >/dev/null 2>&1; then
    BASE_REF="main"
  else
    echo "BLOCKED: cannot resolve origin/main or main" >&2
    exit 2
  fi
fi

BASE_SHA="$(git rev-parse "${BASE_REF}")"
HEAD_SHA="$(git rev-parse HEAD)"
echo "Base ref: ${BASE_REF} (${BASE_SHA:0:12})"
echo "HEAD: ${HEAD_SHA:0:12}"
echo ""

failed=0
checked=0

for f in "${LOCKED_FILES[@]}"; do
  checked=$((checked + 1))

  if [[ ! -f "${f}" ]]; then
    echo "FAIL: locked file missing: ${f}" >&2
    failed=1
    continue
  fi

  # Get the diff for this file (excluding whitespace-only changes).
  diff_output="$(git diff -w "${BASE_REF}"..HEAD -- "${f}" 2>/dev/null || true)"
  diff_full="$(git diff "${BASE_REF}"..HEAD -- "${f}" 2>/dev/null || true)"

  if [[ -z "${diff_full}" ]]; then
    echo "  ${f}: no diff"
  elif [[ -z "${diff_output}" ]]; then
    echo "  ${f}: whitespace-only diff (acceptable)"
  else
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
  echo "FAIL: release-verification change-lock violated" >&2
  exit 1
fi

echo "PASS: release-verification paths unchanged (${checked} files locked)"
