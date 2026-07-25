#!/usr/bin/env bash
# Shared helper: resolve the base ref for change-lock verifier git-diffing.
#
# Sourced by SEC-9 (trust-auth-preservation.sh) and SEC-10
# (release-verification-preservation.sh). Provides a single function:
#
#   resolve_base_ref
#
# Pull-request resolution:
#   1. Validate GITHUB_BASE_REF as a branch name.
#   2. Use origin/<GITHUB_BASE_REF> when already present.
#   3. Fetch that exact branch into origin/<GITHUB_BASE_REF>.
#   4. BLOCKED (no fallback to a different branch).
#
# Non-pull-request resolution:
#   1. origin/main (if reachable)
#   2. Fetch origin/main from remote (if origin remote exists)
#   3. local main (fallback)
#   4. BLOCKED (none available)
#
# Returns 0 and prints the ref name to stdout on success.
# Returns 1 on failure (caller should exit 2 / BLOCKED).

_is_pull_request_context() {
  case "${GITHUB_EVENT_NAME:-}" in
    pull_request|pull_request_target)
      return 0
      ;;
  esac

  # GITHUB_BASE_REF is also accepted without GITHUB_EVENT_NAME so the
  # verifiers can reproduce pull-request behavior deterministically locally.
  [[ -n "${GITHUB_BASE_REF:-}" ]]
}

_validate_pull_request_base() {
  local base_branch="$1"

  if [[ -z "${base_branch}" ]]; then
    echo "Pull-request base branch is missing (GITHUB_BASE_REF)" >&2
    return 1
  fi

  # The value must be a branch name, not a caller-supplied full ref. Prefixing
  # it with refs/heads/ and asking git to validate the result rejects refspec
  # metacharacters, traversal, revision expressions, and control characters.
  if [[ "${base_branch}" == refs/* ]] ||
    ! git check-ref-format "refs/heads/${base_branch}" >/dev/null 2>&1; then
    echo "Unsafe pull-request base branch rejected" >&2
    return 1
  fi

  return 0
}

_resolve_pull_request_base_ref() {
  local base_branch="${GITHUB_BASE_REF:-}"
  local remote_ref=""
  local display_ref=""

  if ! _validate_pull_request_base "${base_branch}"; then
    return 1
  fi

  remote_ref="refs/remotes/origin/${base_branch}"
  display_ref="origin/${base_branch}"

  if git rev-parse --verify "${remote_ref}^{commit}" >/dev/null 2>&1; then
    printf '%s' "${display_ref}"
    return 0
  fi

  if git remote get-url origin >/dev/null 2>&1; then
    echo "${display_ref} not in local refs; fetching validated PR base (depth=50)..." >&2
    if git fetch --no-tags --depth=50 origin \
      "+refs/heads/${base_branch}:${remote_ref}" >/dev/null 2>&1; then
      if git rev-parse --verify "${remote_ref}^{commit}" >/dev/null 2>&1; then
        printf '%s' "${display_ref}"
        return 0
      fi
    fi
    echo "Fetch of validated PR base did not resolve ${display_ref}" >&2
  fi

  return 1
}

_resolve_non_pull_request_base_ref() {
  # 1. Prefer existing origin/main.
  if git rev-parse --verify "refs/remotes/origin/main^{commit}" >/dev/null 2>&1; then
    printf '%s' "origin/main"
    return 0
  fi

  # 2. Try to fetch main from origin with bounded depth.
  if git remote get-url origin >/dev/null 2>&1; then
    echo "origin/main not in local refs; fetching origin main (depth=50)..." >&2
    if git fetch --no-tags --depth=50 origin \
      "+refs/heads/main:refs/remotes/origin/main" >/dev/null 2>&1; then
      if git rev-parse --verify "refs/remotes/origin/main^{commit}" >/dev/null 2>&1; then
        printf '%s' "origin/main"
        return 0
      fi
    fi
    echo "Fetch of origin main did not resolve origin/main" >&2
  fi

  # 3. Fall back to local main.
  if git rev-parse --verify "refs/heads/main^{commit}" >/dev/null 2>&1; then
    printf '%s' "main"
    return 0
  fi

  # 4. None available.
  return 1
}

resolve_base_ref() {
  if _is_pull_request_context; then
    _resolve_pull_request_base_ref
    return $?
  fi

  _resolve_non_pull_request_base_ref
}
