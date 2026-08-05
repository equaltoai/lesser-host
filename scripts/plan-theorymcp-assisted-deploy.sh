#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: bash scripts/plan-theorymcp-assisted-deploy.sh \
  --client-namespace <namespace> [--agent-id <agent>] \
  [--base-url <https-url>] [--auth-mode <oauth|harness>] \
  [--profile <aws-profile>] [--stage lab]

Prints an offline TheoryMCP connection plan and the symbolic lesser-host
AppTheory deploy preview. It never accepts --execute and never calls AWS.

Defaults:
  --base-url https://lab.theorymcp.ai
  --auth-mode oauth
  --profile default
  --stage lab
USAGE
}

fail() {
  printf 'TheoryMCP-assisted deploy planner: %s\n' "$*" >&2
  exit 2
}

stage="lab"
profile="default"
base_url="https://lab.theorymcp.ai"
auth_mode="oauth"
client_namespace=""
agent_id=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --stage)
      [ "$#" -ge 2 ] || fail "--stage requires a value"
      stage="$2"
      shift 2
      ;;
    --profile)
      [ "$#" -ge 2 ] || fail "--profile requires a value"
      profile="$2"
      shift 2
      ;;
    --base-url)
      [ "$#" -ge 2 ] || fail "--base-url requires a value"
      base_url="$2"
      shift 2
      ;;
    --auth-mode)
      [ "$#" -ge 2 ] || fail "--auth-mode requires a value"
      auth_mode="$2"
      shift 2
      ;;
    --client-namespace)
      [ "$#" -ge 2 ] || fail "--client-namespace requires a value"
      client_namespace="$2"
      shift 2
      ;;
    --agent-id)
      [ "$#" -ge 2 ] || fail "--agent-id requires a value"
      agent_id="$2"
      shift 2
      ;;
    --execute|--confirm-live)
      fail "$1 is forbidden; this planner is offline and symbolic only"
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

[ "$stage" = "lab" ] || fail "only the lab stage is supported"

case "$profile" in
  ""|*[!A-Za-z0-9_+=,.@-]*) fail "invalid AWS profile name" ;;
esac

case "$base_url" in
  https://*) ;;
  *) fail "--base-url must be an absolute HTTPS URL" ;;
esac
case "$base_url" in
  *[[:space:]]*|*\?*|*\#*) fail "--base-url must not contain whitespace, a query, or a fragment" ;;
esac
base_url="${base_url%/}"

validate_namespace() {
  local value="$1"
  local length="${#value}"

  [ "$length" -ge 1 ] && [ "$length" -le 63 ] || \
    fail "client namespace must be between 1 and 63 characters"
  [[ "$value" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || \
    fail "client namespace must use lowercase letters, digits, or hyphens without leading/trailing hyphens"
  [[ "$value" != *--* ]] || fail "client namespace must not contain consecutive hyphens"
}

[ -n "$client_namespace" ] || fail "--client-namespace is required"
validate_namespace "$client_namespace"

if [ -n "$agent_id" ]; then
  [[ "$agent_id" =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || \
    fail "agent id must use lowercase letters, digits, or hyphens"
  [[ "$agent_id" != *--* ]] || fail "agent id must not contain consecutive hyphens"
fi

case "$auth_mode" in
  oauth) auth_description="route-scoped Autheory OAuth" ;;
  harness) auth_description="operator-issued lab harness key (pre-vended namespace only)" ;;
  *) fail "--auth-mode must be oauth or harness" ;;
esac

command -v theory >/dev/null 2>&1 || fail "theory CLI is required"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
origin_url="$(git -C "$repo_root" config --get remote.origin.url 2>/dev/null || true)"
case "${origin_url%.git}" in
  https://github.com/equaltoai/lesser-host|git@github.com:equaltoai/lesser-host) ;;
  *) fail "repo origin must be equaltoai/lesser-host" ;;
esac

superproject="$(git -C "$repo_root" rev-parse --show-superproject-working-tree 2>/dev/null || true)"
if [ -n "$superproject" ]; then
  provenance="Factory submodule checkout detected"
else
  provenance="standalone checkout; actual --execute deploy will be refused by validate-deploy-provenance.sh"
fi

namespace_url="${base_url}/${client_namespace}/mcp"
agent_url=""
if [ -n "$agent_id" ]; then
  agent_url="${base_url}/${client_namespace}/agents/${agent_id}/mcp"
fi

cat <<PLAN
TheoryMCP-assisted lesser-host deployment plan
Mode: OFFLINE / SYMBOLIC ONLY
Stage: ${stage}
AWS profile (symbolic): ${profile}
Checkout: ${provenance}
Namespace route: ${namespace_url}
Agent route: ${agent_url:-not requested}
Authentication: ${auth_description}

Prerequisites before cloud execution:
- TheoryCloud.ai has already vended the namespace and optional agent route.
- The operator has completed route-scoped OAuth, or received a lab harness key from the TheoryMCP operator.
- Valid AWS credentials exist in profile ${profile}.
- The deploy runs from factory/products/lesser-host, not a standalone clone.
- The exact lab invocation has explicit operator authorization.

Symbolic AppTheory preview follows; no deploy command is executed.
PLAN

theory app up --path "$repo_root" --aws-profile "$profile" --stage "$stage"
