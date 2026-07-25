#!/usr/bin/env bash
# Deterministic regression tests for the SEC-9/SEC-10 change-lock base resolver.

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPER="${TEST_DIR}/_resolve_base_ref.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/lesser-host-base-ref-test.XXXXXX")"

cleanup() {
  rm -rf -- "${TEST_ROOT}"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_equal() {
  local expected="$1"
  local actual="$2"
  local label="$3"

  if [[ "${actual}" != "${expected}" ]]; then
    fail "${label}: expected '${expected}', got '${actual}'"
  fi
}

init_repo() {
  local repo="$1"

  git init -q -b main "${repo}"
  git -C "${repo}" config user.name "Base Resolver Test"
  git -C "${repo}" config user.email "base-resolver-test@example.invalid"
  git -C "${repo}" config commit.gpgSign false
  printf '%s\n' "main" >"${repo}/state.txt"
  git -C "${repo}" add state.txt
  git -C "${repo}" commit -q -m "main"
}

resolve_in_repo() {
  local repo="$1"
  local event_name="$2"
  local base_branch="$3"

  (
    cd "${repo}"
    export GITHUB_EVENT_NAME="${event_name}"
    if [[ "${base_branch}" == "__UNSET__" ]]; then
      unset GITHUB_BASE_REF
    else
      export GITHUB_BASE_REF="${base_branch}"
    fi
    # shellcheck source=./_resolve_base_ref.sh
    source "${HELPER}"
    resolve_base_ref
  )
}

test_pull_request_base_already_present() {
  local repo="${TEST_ROOT}/present"
  local actual=""

  init_repo "${repo}"
  git -C "${repo}" update-ref refs/remotes/origin/staging HEAD

  actual="$(resolve_in_repo "${repo}" pull_request staging)"
  assert_equal "origin/staging" "${actual}" "present PR base"
  echo "PASS: pull-request base already present"
}

test_pull_request_base_fetched_in_shallow_clone() {
  local remote="${TEST_ROOT}/remote.git"
  local seed="${TEST_ROOT}/seed"
  local clone="${TEST_ROOT}/shallow"
  local actual=""
  local expected_sha=""

  git init -q --bare "${remote}"
  init_repo "${seed}"
  git -C "${seed}" remote add origin "${remote}"
  git -C "${seed}" push -q origin main

  git -C "${seed}" switch -q -c staging
  printf '%s\n' "staging" >"${seed}/state.txt"
  git -C "${seed}" commit -qam "staging"
  git -C "${seed}" push -q origin staging

  git -C "${seed}" switch -q -c feature
  printf '%s\n' "feature" >"${seed}/feature.txt"
  git -C "${seed}" add feature.txt
  git -C "${seed}" commit -q -m "feature"
  git -C "${seed}" push -q origin feature

  git -c protocol.file.allow=always clone -q --depth=1 --branch feature \
    "file://${remote}" "${clone}"
  assert_equal "true" "$(git -C "${clone}" rev-parse --is-shallow-repository)" \
    "shallow test precondition"
  if git -C "${clone}" rev-parse --verify refs/remotes/origin/staging >/dev/null 2>&1; then
    fail "shallow test precondition: origin/staging unexpectedly present"
  fi

  actual="$(resolve_in_repo "${clone}" pull_request staging)"
  assert_equal "origin/staging" "${actual}" "fetched PR base"
  expected_sha="$(git --git-dir="${remote}" rev-parse refs/heads/staging)"
  assert_equal "${expected_sha}" \
    "$(git -C "${clone}" rev-parse refs/remotes/origin/staging)" \
    "fetched PR base SHA"
  echo "PASS: safe pull-request base fetched in shallow clone"
}

test_unsafe_and_missing_pull_request_base_fail_closed() {
  local repo="${TEST_ROOT}/unsafe"
  local stderr_file="${TEST_ROOT}/unsafe.stderr"

  init_repo "${repo}"
  git -C "${repo}" update-ref refs/remotes/origin/main HEAD

  if resolve_in_repo "${repo}" pull_request \
    'staging:refs/remotes/origin/main' 2>"${stderr_file}"; then
    fail "unsafe PR base unexpectedly resolved"
  fi
  grep -q "Unsafe pull-request base branch rejected" "${stderr_file}" ||
    fail "unsafe PR base rejection was not explicit"

  if resolve_in_repo "${repo}" pull_request "__UNSET__" 2>"${stderr_file}"; then
    fail "missing PR base unexpectedly fell back"
  fi
  grep -q "Pull-request base branch is missing" "${stderr_file}" ||
    fail "missing PR base rejection was not explicit"
  echo "PASS: unsafe and missing pull-request bases fail closed"
}

test_non_pull_request_fallbacks() {
  local origin_repo="${TEST_ROOT}/non-pr-origin"
  local local_repo="${TEST_ROOT}/non-pr-local"
  local actual=""

  init_repo "${origin_repo}"
  git -C "${origin_repo}" update-ref refs/remotes/origin/main HEAD
  actual="$(resolve_in_repo "${origin_repo}" push "__UNSET__")"
  assert_equal "origin/main" "${actual}" "non-PR origin/main fallback"

  init_repo "${local_repo}"
  actual="$(resolve_in_repo "${local_repo}" push "__UNSET__")"
  assert_equal "main" "${actual}" "non-PR local main fallback"
  echo "PASS: non-pull-request origin/main and main fallbacks"
}

test_pull_request_base_already_present
test_pull_request_base_fetched_in_shallow_clone
test_unsafe_and_missing_pull_request_base_fail_closed
test_non_pull_request_fallbacks

echo "PASS: all change-lock base resolver tests"
