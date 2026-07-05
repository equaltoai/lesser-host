#!/usr/bin/env bash
# Provision the explicit CI/test deploy-local domain fixture.
#
# This script is intentionally CI-only. It copies placeholder values from the
# committed example file into the gitignored deploy-local config path so CDK
# synth jobs can exercise the same fail-closed product path without carrying
# deployer/account-specific identifiers in git.
#
# Do not call this from real deploy paths (`theory app up/down --execute`).

set -euo pipefail

if [[ "${CI:-}" != "true" && "${GITHUB_ACTIONS:-}" != "true" ]]; then
  echo "Refusing to provision deploy-local fixture outside CI/test context; set CI=true for local verification." >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "${repo_root}" ]]; then
  echo "Refusing to provision deploy-local fixture outside a git checkout." >&2
  exit 1
fi

example_path="${repo_root}/app-theory/deploy.local.json.example"
target_path="${repo_root}/app-theory/deploy.local.json"

if [[ ! -f "${example_path}" ]]; then
  echo "Missing deploy-local fixture source: ${example_path}" >&2
  exit 1
fi

if [[ -e "${target_path}" ]]; then
  echo "Refusing to overwrite existing deploy-local config: ${target_path}" >&2
  exit 1
fi

cp "${example_path}" "${target_path}"
echo "Provisioned CI deploy-local domain fixture: ${target_path#"${repo_root}/"}"
