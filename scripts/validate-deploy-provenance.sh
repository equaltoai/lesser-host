#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: validate-deploy-provenance.sh [repo-root]" >&2
}

fail() {
  echo "deploy provenance guard: $*" >&2
  exit 1
}

normalize_github_repo() {
  local url="$1"
  url="${url%.git}"
  case "$url" in
    https://github.com/*)
      printf '%s\n' "${url#https://github.com/}"
      ;;
    http://github.com/*)
      printf '%s\n' "${url#http://github.com/}"
      ;;
    git@github.com:*)
      printf '%s\n' "${url#git@github.com:}"
      ;;
    ssh://git@github.com/*)
      printf '%s\n' "${url#ssh://git@github.com/}"
      ;;
    github.com/*)
      printf '%s\n' "${url#github.com/}"
      ;;
    *)
      printf '%s\n' "$url"
      ;;
  esac
}

if [ "$#" -gt 1 ]; then
  usage
  exit 2
fi

if [ "$#" -eq 1 ]; then
  repo_root="$1"
else
  repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
fi

[ -n "$repo_root" ] || fail "not running inside a git work tree"
[ -d "$repo_root" ] || fail "repo root does not exist: $repo_root"
repo_root="$(cd "$repo_root" && pwd -P)"

git_top="$(git -C "$repo_root" rev-parse --show-toplevel 2>/dev/null || true)"
[ -n "$git_top" ] || fail "not a git work tree: $repo_root"
git_top="$(cd "$git_top" && pwd -P)"
[ "$git_top" = "$repo_root" ] || fail "expected repository root, got $repo_root (top-level is $git_top)"

superproject="$(git -C "$repo_root" rev-parse --show-superproject-working-tree 2>/dev/null || true)"
[ -n "$superproject" ] || fail "refusing deploy from non-Factory-submodule checkout: missing superproject"
superproject="$(cd "$superproject" && pwd -P)"

gitmodules="$superproject/.gitmodules"
[ -f "$gitmodules" ] || fail "superproject lacks .gitmodules: $gitmodules"

expected_path="products/lesser-host"
expected_abs="$(cd "$superproject/$expected_path" 2>/dev/null && pwd -P || true)"
[ -n "$expected_abs" ] || fail "expected submodule path does not exist under superproject: $expected_path"
[ "$repo_root" = "$expected_abs" ] || fail "refusing deploy from $repo_root; expected registered submodule $expected_abs"

submodule_name=""
while IFS= read -r line; do
  key="${line%% *}"
  value="${line#* }"
  if [ "$value" = "$expected_path" ]; then
    submodule_name="${key#submodule.}"
    submodule_name="${submodule_name%.path}"
    break
  fi
done < <(git -C "$superproject" config -f "$gitmodules" --get-regexp '^submodule\..*\.path$' || true)

[ -n "$submodule_name" ] || fail ".gitmodules does not register $expected_path"

submodule_url="$(git -C "$superproject" config -f "$gitmodules" --get "submodule.${submodule_name}.url" || true)"
[ -n "$submodule_url" ] || fail ".gitmodules entry for $expected_path lacks url"
[ "$(normalize_github_repo "$submodule_url")" = "equaltoai/lesser-host" ] || \
  fail ".gitmodules maps $expected_path to unexpected url: $submodule_url"

origin_url="$(git -C "$repo_root" config --get remote.origin.url || true)"
[ -n "$origin_url" ] || fail "repository lacks origin remote"
[ "$(normalize_github_repo "$origin_url")" = "equaltoai/lesser-host" ] || \
  fail "origin remote is not equaltoai/lesser-host: $origin_url"

printf 'deploy provenance guard: OK registered Factory submodule %s with origin %s\n' "$expected_path" "$origin_url" >&2
