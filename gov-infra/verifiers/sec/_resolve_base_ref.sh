#!/usr/bin/env bash
# Shared helper: resolve the base ref for change-lock verifier git-diffing.
#
# Sourced by SEC-9 (trust-auth-preservation.sh) and SEC-10
# (release-verification-preservation.sh). Provides a single function:
#
#   resolve_base_ref
#
# Resolution priority:
#   1. origin/main (if reachable)
#   2. Fetch origin/main from remote (if origin remote exists)
#   3. local main (fallback)
#   4. BLOCKED (none available)
#
# Returns 0 and prints the ref name to stdout on success.
# Returns 1 on failure (caller should exit 2 / BLOCKED).

resolve_base_ref() {
  # 1. Prefer existing origin/main.
  if git rev-parse --verify origin/main >/dev/null 2>&1; then
    printf '%s' "origin/main"
    return 0
  fi

  # 2. Try to fetch main from origin with bounded depth.
  if git remote get-url origin >/dev/null 2>&1; then
    echo "origin/main not in local refs; fetching origin main (depth=50)..." >&2
    if git fetch origin main --depth=50 --no-tags 2>/dev/null; then
      if git rev-parse --verify origin/main >/dev/null 2>&1; then
        printf '%s' "origin/main"
        return 0
      fi
    fi
    echo "Fetch of origin main did not resolve origin/main" >&2
  fi

  # 3. Fall back to local main.
  if git rev-parse --verify main >/dev/null 2>&1; then
    printf '%s' "main"
    return 0
  fi

  # 4. None available.
  return 1
}
