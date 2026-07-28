#!/usr/bin/env bash
# scripts/hosted-genesis-microvm-e2e-gate.sh
#
# P52 H1.5 — hosted genesis MicroVM E2E gate. Drives the full lab E2E arc the
# roadmap (docs/roadmap/microvm-only-genesis.md, H1.5) requires:
#
#   real conversation through /mint-conversation with a >30s LLM turn
#     -> 202 accepted-pending
#     -> poll until assistant_turn_ready
#     -> complete -> in-VM declaration extraction
#     -> poll until declaration_ready
#   kill a VM mid-turn -> recovery surfaces failed -> retry works
#
# Two phases:
#   1. STUB/LOCAL-PROVED GATE (always runs, no AWS, CI-safe): exercises the real
#      HTTPControllerDispatcher wired via the H1.5 NewServer seam against an
#      httptest.Server-backed stub controller serving the governed
#      AppTheoryMicrovmController HTTP routes (POST /microvms to run, GET
#      /microvms/{session_id} to reconcile). Proves the happy path, kill-VM
#      recovery, and the MaximumDurationSeconds + IdlePolicy timeout-budget
#      wiring. This is the proof that runs in CI; it does NOT call AWS. It needs NO
#      configuration — in-memory HTTP stubs only.
#   2. LAB DEPLOY GATE (runs only with --stage lab): drives the deployed lab
#      control-plane endpoints. ALL configuration is SYSTEM-SOURCED — no
#      operator-set env vars for system config:
#      - Connection details (control-plane API host) -> read from the CDK stack
#        outputs file (produced by `cdk deploy --outputs-file <path>`, or
#        `aws cloudformation describe-stacks --query Outputs`). The system owns
#        this; the harness only takes the path to it. If the outputs file is
#        absent, Phase 2 FAILS CLOSED with instructions to deploy first — it
#        does NOT fall back to env vars.
#      - Test fixtures (registration id, agent id, model, messages) -> read from
#        the committed system-owned fixture file scripts/lab-e2e-fixtures.json
#        (non-secret lab fixture identifiers only).
#      - The raw instance API key (a credential the system never persists in
#        plaintext — see docs/hosted-genesis-microvm-lab-canary.md) is read from
#        a runtime secret file scripts/.lab-e2e-instance-key (gitignored), which
#        the principal provisions out-of-band when provisioning the lab instance.
#        It is a CREDENTIAL, not system config, and never lives in env vars,
#        git, fixtures, or CloudFormation outputs.
#      The principal runs this against the lab deploy after
#      `theory app up --stage lab`. It exercises the live MicroVM path end-to-end.
#
# Timeout-budget wiring (roadmap decision 7):
#   - accept 202 must return in <2s  (ACCEPT_BUDGET_S=2)
#   - session MaximumDurationSeconds is sized for the longest LLM turn plus
#     in-VM extraction (HOSTED_GENESIS_MICROVM_MAXIMUM_DURATION_SECONDS, default
#     300s) — set on the CDK control-plane Lambda env and threaded onto the
#     dispatched run request (verified by the stub gate).
#   - the AppTheory ProviderIdlePolicy is explicit (default max idle 300s,
#     suspended duration 1800s, auto-resume false) and is threaded onto the same
#     dispatched run request; longer human gaps recover through checkpoint /
#     relaunch / replay, not a Host-owned conversation step machine.
#   - controller Lambda stays at 30s (lifecycle only); the assistant turn runs
#     inside the MicroVM, not the controller Lambda.
#
# Usage:
#   # CI / local stub proof (no AWS, no config):
#   bash scripts/hosted-genesis-microvm-e2e-gate.sh
#
#   # Lab deploy gate (principal only; requires a lab deploy + the CDK outputs
#   # file + a provisioned lab instance key):
#   bash scripts/hosted-genesis-microvm-e2e-gate.sh --stage lab \
#     --outputs-file cdk/lcdk-outputs.json
#   # (the raw instance key must be present at scripts/.lab-e2e-instance-key,
#   # provisioned out-of-band when the lab instance was created)
#
# Exit codes: 0 = both phases passed (or only the stub phase, when --stage lab
# is not requested). Non-zero = a phase failed. Phase 2 fails closed (non-zero)
# if the CDK outputs file or the runtime secret key file is absent — it never
# falls back to operator-set env vars.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

validate_agent_list_response() {
  local conversation_id="$1"
  local agent_id="$2"
  python3 -c '
import json
import sys

conversation_id, agent_id = sys.argv[1], sys.argv[2]
data = json.load(sys.stdin)
items = data.get("conversations")
if not isinstance(items, list):
    raise SystemExit("list response missing conversations array")
for item in items:
    if item.get("conversation_id") == conversation_id:
        listed_agent = item.get("agent_id")
        if listed_agent is not None and listed_agent != agent_id:
            raise SystemExit(f"listed conversation has wrong agent_id {listed_agent!r}")
        if "messages" in item or "produced_declarations" in item or "declarations" in item:
            raise SystemExit("listed conversation contains private fields")
        break
else:
    raise SystemExit(f"conversation {conversation_id} was not present in agent-scoped list")
' "$conversation_id" "$agent_id"
}

validate_agent_get_response() {
  local conversation_id="$1"
  local agent_id="$2"
  python3 -c '
import json
import sys

conversation_id, agent_id = sys.argv[1], sys.argv[2]
data = json.load(sys.stdin)
conv = data.get("conversation") or {}
returned_conversation = conv.get("conversation_id")
if returned_conversation != conversation_id:
    raise SystemExit(f"single-get returned wrong conversation_id {returned_conversation!r}")
returned_agent = conv.get("agent_id")
if returned_agent != agent_id:
    raise SystemExit(f"single-get returned wrong agent_id {returned_agent!r}")
' "$conversation_id" "$agent_id"
}

run_agent_read_parser_smoke_tests() {
  printf '%s' '{"conversations":[{"conversation_id":"parser-smoke-conversation","status":"declaration_ready","message_count":1}]}' |
    validate_agent_list_response "parser-smoke-conversation" "0xparser"
  if printf '%s' '{"conversations":[{"conversation_id":"parser-smoke-conversation","agent_id":"0xother"}]}' |
    validate_agent_list_response "parser-smoke-conversation" "0xparser" 2>/dev/null; then
    echo "FAIL: parser smoke test accepted mismatched list agent_id" >&2
    exit 1
  fi
  if printf '%s' '{"conversations":[{"conversation_id":"parser-smoke-conversation","messages":[]}]}' |
    validate_agent_list_response "parser-smoke-conversation" "0xparser" 2>/dev/null; then
    echo "FAIL: parser smoke test accepted private list fields" >&2
    exit 1
  fi
  printf '%s' '{"conversation":{"conversation_id":"parser-smoke-conversation","agent_id":"0xparser"}}' |
    validate_agent_get_response "parser-smoke-conversation" "0xparser"
}

# ---------------------------------------------------------------------------
# Phase 1 — stub/local-proved gate (CI-safe, no AWS, no config).
# ---------------------------------------------------------------------------
echo "==> [1/2] stub/local-proved E2E gate (no AWS)"
go test ./internal/controlplane \
  -run 'TestH1_5_E2E_HappyPathAndKillVMRecovery|TestH1_5_E2E_MaximumDurationSecondsWired|TestH1_5_E2E_HarnessConfigCompleteGuard' \
  -count=1
# NewServer wiring + fail-closed + explicit-503 guards (also CI-safe).
go test ./internal/controlplane \
  -run 'TestH1_5_NewServerWiresHTTPControllerDispatcherForDeployedStages|TestH1_5_NewServerSetsNonNilDispatcherOnServer|TestH1_5_NewServerLeavesDispatcherNilWhenConfigIncomplete|TestH1_5_NewServerLeavesDispatcherNilForEmptyConfig|TestH1_5_DispatcherConstructionFailureFailsLoudlyNoSyncFallback|TestH1_5_MicroVMUnavailableAcceptPathReturnsExplicit503' \
  -count=1
go test ./cmd/hosted-genesis-microvm-controller \
  -run 'TestControllerEventFailsClosedWhenDisabled|TestControllerEventFailsClosedWhenAuthLoosened|TestControllerEventGateNoLongerLabOnly' \
  -count=1
run_agent_read_parser_smoke_tests
echo "==> [1/2] stub/local-proved E2E gate: PASS"

# ---------------------------------------------------------------------------
# Phase 2 — lab deploy gate (principal only, system-sourced config).
# ---------------------------------------------------------------------------
stage=""
outputs_file=""
auto_kill_controller="false"
fixtures_file="scripts/lab-e2e-fixtures.json"
key_file="scripts/.lab-e2e-instance-key"

usage() {
  cat >&2 <<EOF
usage: bash scripts/hosted-genesis-microvm-e2e-gate.sh [--stage lab] [--outputs-file <path>] [--auto-kill-controller]

  (no args)         Phase 1 stub/local-proved gate only (CI-safe, no AWS).
  --stage lab       Also run Phase 2 (lab deploy gate). Requires:
                      --outputs-file <path>  CDK stack outputs JSON (produced by
                                             'cdk deploy --outputs-file <path>'
                                             or 'aws cloudformation
                                             describe-stacks --query Outputs').
                    And the runtime instance-key secret at ${key_file}
                    (provisioned out-of-band; see scripts/lab-e2e-fixtures.json).
                    Fixtures are read from ${fixtures_file} (committed).
  --auto-kill-controller
                    Lab only. Drive the kill-VM recovery arc noninteractively by
                    reading the CDK-owned MicroVM controller bearer token from
                    SSM with the active AWS credentials and issuing
                    DELETE /microvms/{conversation}. The raw token is never
                    printed or persisted by this harness. Without this flag the
                    kill arc keeps the manual operator /dev/tty pause.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --stage)
      stage="${2:-}"
      shift 2
      ;;
    --outputs-file)
      outputs_file="${2:-}"
      shift 2
      ;;
    --auto-kill-controller)
      auto_kill_controller="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ "$stage" != "lab" ]]; then
  echo "==> [2/2] lab deploy gate: SKIPPED (pass --stage lab to run against lab)"
  echo "==> H1.5 E2E gate: PASS (stub/local-proved)"
  exit 0
fi

# --- System config: CDK stack outputs file (connection details). ---
# The system produces this via `cdk deploy --outputs-file <path>`; the harness
# only consumes it. Fail closed if absent — NEVER fall back to env vars.
if [[ -z "$outputs_file" ]]; then
  echo "FAIL: --stage lab requires --outputs-file <path> (the CDK stack outputs JSON)." >&2
  echo "      Produce it with: cdk deploy --stage lab --outputs-file cdk/lab-outputs.json" >&2
  echo "      (or: aws cloudformation describe-stacks --stack-name <lesser-host-lab> --query 'Stacks[0].Outputs' > cdk/lab-outputs.json)" >&2
  exit 1
fi
if [[ ! -f "$outputs_file" ]]; then
  echo "FAIL: CDK outputs file not found at ${outputs_file}." >&2
  echo "      System config is absent — run 'cdk deploy --stage lab --outputs-file ${outputs_file}' first." >&2
  echo "      The harness does NOT fall back to operator-set env vars." >&2
  exit 1
fi

# cdk deploy --outputs-file writes {"<StackName>": {<OutputKey>: <Value>, ...}}.
# aws cloudformation describe-stacks --query Outputs writes [{"OutputKey":..,"OutputValue":..}].
# Support both shapes by resolving the ControlPlaneUrl value defensively.
resolve_cfn_output() {
  # $1 = output key. Prints the value or empty.
  local key="$1"
  python3 - "$outputs_file" "$key" <<'PY'
import json, sys
path, key = sys.argv[1], sys.argv[2]
with open(path) as fh:
    data = json.load(fh)
# Shape A: cdk deploy --outputs-file -> {StackName: {Key: Value}}
if isinstance(data, dict):
    for _stack, outs in data.items():
        if isinstance(outs, dict) and key in outs:
            print(outs[key])
            sys.exit(0)
# Shape B: aws cloudformation describe-stacks --query Outputs -> [{OutputKey, OutputValue}]
if isinstance(data, list):
    for item in data:
        if isinstance(item, dict) and item.get("OutputKey") == key:
            print(item.get("OutputValue", ""))
            sys.exit(0)
PY
}

lab_host="$(resolve_cfn_output 'ControlPlaneUrl')"
if [[ -z "$lab_host" ]]; then
  echo "FAIL: 'ControlPlaneUrl' not found in CDK outputs file ${outputs_file}." >&2
  echo "      Ensure the lab stack exports ControlPlaneUrl (cdk/lib/lesser-host-stack.ts) and re-run" >&2
  echo "      'cdk deploy --stage lab --outputs-file ${outputs_file}'." >&2
  exit 1
fi
controller_endpoint="$(resolve_cfn_output 'HostedGenesisMicrovmControllerEndpoint')"
if [[ "$auto_kill_controller" == "true" && -z "$controller_endpoint" ]]; then
  echo "FAIL: --auto-kill-controller requires HostedGenesisMicrovmControllerEndpoint in ${outputs_file}." >&2
  exit 1
fi

# --- System fixtures: committed, non-secret identifiers (scripts/lab-e2e-fixtures.json). ---
if [[ ! -f "$fixtures_file" ]]; then
  echo "FAIL: system fixture file not found at ${fixtures_file}." >&2
  exit 1
fi
fixture_json_field() {
  # $1 = field name. Prints the value or empty.
  python3 - "$fixtures_file" "$1" <<'PY'
import json, sys
path, field = sys.argv[1], sys.argv[2]
with open(path) as fh:
    data = json.load(fh)
val = data.get(field)
print(val if isinstance(val, str) else "")
PY
}
registration_id="$(fixture_json_field 'registrationId')"
instance_slug="$(fixture_json_field 'instanceSlug')"
agent_id_hex="$(fixture_json_field 'agentIdHex')"
fixt_model="$(fixture_json_field 'model')"
fixt_message="$(fixture_json_field 'message')"
fixt_kill_message="$(fixture_json_field 'killArcMessage')"
fixt_retry_message="$(fixture_json_field 'retryMessage')"
if [[ -z "$registration_id" || -z "$instance_slug" || -z "$agent_id_hex" ]]; then
  echo "FAIL: system fixture file ${fixtures_file} missing registrationId, instanceSlug, or agentIdHex." >&2
  exit 1
fi
e2e_model="${fixt_model:-gpt-5.6-luna}"
e2e_message="${fixt_message:-Please draft my soul declaration. Take your time; this is a long reflection.}"
e2e_kill_message="${fixt_kill_message:-A second long reflection for the kill-VM recovery arc.}"
e2e_retry_message="${fixt_retry_message:-Retry after kill-VM recovery.}"

# --- Runtime credential: raw instance API key (secret, never committed/env-var). ---
# The system stores only sha256(raw_key); the raw key is shown once at creation
# (admin endpoint, wallet-authed) and provisioned out-of-band into this file by
# the principal. See docs/hosted-genesis-microvm-lab-canary.md.
if [[ ! -s "$key_file" ]]; then
  echo "FAIL: runtime instance-key secret not found (or empty) at ${key_file}." >&2
  echo "      Provision the raw lab instance API key there out-of-band (chmod 600 recommended)." >&2
  echo "      It is a CREDENTIAL, not system config — never commit it, never put it in an env var." >&2
  exit 1
fi
instance_key="$(cat "$key_file")"
if [[ -z "$instance_key" ]]; then
  echo "FAIL: runtime instance-key file ${key_file} is empty." >&2
  exit 1
fi

ACCEPT_BUDGET_S="${ACCEPT_BUDGET_S:-2}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-3}"
POLL_DEADLINE_S="${POLL_DEADLINE_S:-300}"          # >30s LLM turn + extraction headroom
RECOVER_POLL_DEADLINE_S="${RECOVER_POLL_DEADLINE_S:-60}"

base="${lab_host}/api/v1/soul/instance/agents/register/${registration_id}/mint-conversation"
auth="Authorization: Bearer ${instance_key}"
json_ct="Content-Type: application/json"
now_ms() { date +%s%3N; }

echo "==> [2/2] lab deploy gate against ${lab_host} (config: CDK outputs ${outputs_file}; fixtures: ${fixtures_file})"
if [[ "$auto_kill_controller" == "true" ]]; then
  echo "    kill-VM arc: auto-controller termination enabled (token read from SSM, not logged)"
fi

# --- Accept: POST /mint-conversation -> expect 202 in_progress ---
idem="e2e-$(date +%s)-$$"
accept_body=$(cat <<EOF
{"model":"${e2e_model}","message":"${e2e_message}","idempotencyKey":"${idem}","correlationID":"e2e-${idem}"}
EOF
)
accept_start=$(now_ms)
accept_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X POST "$base" \
  -H "$auth" -H "$json_ct" -d "$accept_body" || true)
accept_status=$(printf '%s' "$accept_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
accept_body_clean=$(printf '%s' "$accept_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')
accept_elapsed_ms=$(( $(now_ms) - accept_start ))
accept_elapsed_s=$(awk "BEGIN{printf \"%.3f\", ${accept_elapsed_ms}/1000}")

echo "    accept: HTTP ${accept_status} in ${accept_elapsed_s}s (budget ${ACCEPT_BUDGET_S}s)"
if [[ "$accept_status" != "202" ]]; then
  echo "FAIL: expected 202 accepted-pending, got ${accept_status}: ${accept_body_clean}" >&2
  exit 1
fi
if awk "BEGIN{exit !(${accept_elapsed_s} >= ${ACCEPT_BUDGET_S})}"; then
  echo "FAIL: accept took ${accept_elapsed_s}s, expected < ${ACCEPT_BUDGET_S}s budget" >&2
  exit 1
fi

conversation_id=$(printf '%s' "$accept_body_clean" | python3 -c 'import sys,json; c=json.load(sys.stdin).get("conversation",{}); print(c.get("id") or c.get("conversation_id") or "")' 2>/dev/null || true)
if [[ -z "$conversation_id" || "$conversation_id" == "null" ]]; then
  echo "FAIL: could not parse conversation id from accept response: ${accept_body_clean}" >&2
  exit 1
fi
echo "    conversation: ${conversation_id}"

# --- Poll for assistant_turn_ready ---
poll_deadline=$(( $(date +%s) + POLL_DEADLINE_S ))
status=""
while [[ $(date +%s) -lt $poll_deadline ]]; do
  sleep "$POLL_INTERVAL_S"
  poll_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X GET "${base}/${conversation_id}" -H "$auth" || true)
  status=$(printf '%s' "$poll_resp" | sed 's/__HTTP_STATUS__[0-9]*$//' | \
    python3 -c 'import sys,json;print(json.load(sys.stdin)["conversation"]["status"])' 2>/dev/null || true)
  echo "    poll: status=${status}"
  if [[ "$status" == "assistant_turn_ready" || "$status" == "declaration_extraction_pending" || "$status" == "declaration_ready" ]]; then
    break
  fi
  if [[ "$status" == "failed" ]]; then
    echo "FAIL: conversation entered failed during accept->ready poll" >&2
    exit 1
  fi
done
if [[ "$status" != "assistant_turn_ready" && "$status" != "declaration_extraction_pending" && "$status" != "declaration_ready" ]]; then
  echo "FAIL: timed out waiting for assistant_turn_ready (last status=${status})" >&2
  exit 1
fi
echo "    assistant turn ready: ${status}"

if [[ "$status" == "declaration_ready" ]]; then
  echo "    declaration ready: ${status}"
  echo "==> happy path: PASS"
else
  if [[ "$status" == "assistant_turn_ready" ]]; then
    # --- Complete -> in-VM declaration extraction -> poll for declaration_ready ---
    complete_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X POST "${base}/${conversation_id}/complete" \
      -H "$auth" -H "$json_ct" -d '{}' || true)
    complete_status=$(printf '%s' "$complete_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
    echo "    complete: HTTP ${complete_status}"
    if [[ "$complete_status" != "200" && "$complete_status" != "202" ]]; then
      echo "FAIL: complete expected 200/202, got ${complete_status}: $(printf '%s' "$complete_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')" >&2
      exit 1
    fi
  else
    echo "    declaration extraction already pending in MicroVM; skipping complete"
  fi

  # Poll for declaration_ready (extraction completes in-VM).
  poll_deadline2=$(( $(date +%s) + POLL_DEADLINE_S ))
  while [[ $(date +%s) -lt $poll_deadline2 ]]; do
    sleep "$POLL_INTERVAL_S"
    poll_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X GET "${base}/${conversation_id}" -H "$auth" || true)
    status=$(printf '%s' "$poll_resp" | sed 's/__HTTP_STATUS__[0-9]*$//' | \
      python3 -c 'import sys,json;print(json.load(sys.stdin)["conversation"]["status"])' 2>/dev/null || true)
    echo "    poll: status=${status}"
    if [[ "$status" == "declaration_ready" ]]; then
      break
    fi
    if [[ "$status" == "failed" ]]; then
      echo "FAIL: conversation entered failed during extraction->ready poll" >&2
      exit 1
    fi
  done
  if [[ "$status" != "declaration_ready" ]]; then
    echo "FAIL: timed out waiting for declaration_ready (last status=${status})" >&2
    exit 1
  fi
echo "    declaration ready: ${status}"
echo "==> happy path: PASS"
fi

# --- Agent-scoped list/get proof for the newly created hosted/off-chain conversation. ---
# These reads use the ordinary InstanceKey-authenticated agent routes that Lesser
# calls after it knows the agent id. The list assertion is deliberately
# metadata-only: it rejects transcript/declaration/private-field leakage and
# never prints the raw InstanceKey.
agent_read_base="${lab_host}/api/v1/soul/instance/agents/${agent_id_hex}/mint-conversations"
list_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X GET "${agent_read_base}?limit=50" -H "$auth" || true)
list_status=$(printf '%s' "$list_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
list_body=$(printf '%s' "$list_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')
echo "    agent list: HTTP ${list_status}"
if [[ "$list_status" != "200" ]]; then
  echo "FAIL: agent-scoped list expected 200, got ${list_status}: ${list_body}" >&2
  exit 1
fi
if [[ "$list_body" == *"$instance_key"* ]]; then
  echo "FAIL: agent-scoped list leaked the raw InstanceKey" >&2
  exit 1
fi
if [[ "$list_body" == *'"messages"'* || "$list_body" == *'"produced_declarations"'* || "$list_body" == *'"declarations"'* || "$list_body" == *"${e2e_message}"* ]]; then
  echo "FAIL: agent-scoped list leaked transcript/declaration/private fields" >&2
  exit 1
fi
printf '%s' "$list_body" | validate_agent_list_response "$conversation_id" "$agent_id_hex"
echo "    agent list: contains ${conversation_id} and remains metadata-only"

get_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X GET "${agent_read_base}/${conversation_id}" -H "$auth" || true)
get_status=$(printf '%s' "$get_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
get_body=$(printf '%s' "$get_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')
echo "    agent get: HTTP ${get_status}"
if [[ "$get_status" != "200" ]]; then
  echo "FAIL: agent-scoped get expected 200, got ${get_status}: ${get_body}" >&2
  exit 1
fi
if [[ "$get_body" == *"$instance_key"* ]]; then
  echo "FAIL: agent-scoped get leaked the raw InstanceKey" >&2
  exit 1
fi
printf '%s' "$get_body" | validate_agent_get_response "$conversation_id" "$agent_id_hex"
echo "    agent get: returned ${conversation_id} for ${agent_id_hex}"

# --- Kill-VM recovery arc (second conversation) ---
echo "==> kill-VM recovery arc"
idem2="e2e-kill-$(date +%s)-$$"
kill_body=$(cat <<EOF
{"model":"${e2e_model}","message":"${e2e_kill_message}","idempotencyKey":"${idem2}","correlationID":"e2e-${idem2}"}
EOF
)
kill_accept=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X POST "$base" -H "$auth" -H "$json_ct" -d "$kill_body" || true)
kill_status=$(printf '%s' "$kill_accept" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
conv2=$(printf '%s' "$kill_accept" | sed 's/__HTTP_STATUS__[0-9]*$//' | \
  python3 -c 'import sys,json; c=json.load(sys.stdin).get("conversation",{}); print(c.get("id") or c.get("conversation_id") or "")' 2>/dev/null || true)
echo "    kill-arc accept: HTTP ${kill_status} conversation=${conv2}"
if [[ "$kill_status" != "202" || -z "$conv2" ]]; then
  echo "FAIL: kill-arc accept expected 202 + conversation id, got ${kill_status} ${conv2}" >&2
  exit 1
fi

if [[ "$auto_kill_controller" == "true" ]]; then
  if ! command -v aws >/dev/null 2>&1; then
    echo "FAIL: --auto-kill-controller requires the aws CLI." >&2
    exit 1
  fi
  controller_auth_token_param="/lesser-host/${stage}/hosted-genesis/microvm/auth-token"
  controller_auth_token="$(aws ssm get-parameter \
    --region us-east-1 \
    --with-decryption \
    --name "$controller_auth_token_param" \
    --query 'Parameter.Value' \
    --output text)"
  if [[ -z "$controller_auth_token" || "$controller_auth_token" == "None" ]]; then
    echo "FAIL: --auto-kill-controller could not read controller auth token from SSM parameter ${controller_auth_token_param}." >&2
    exit 1
  fi

  controller_auth="Authorization: Bearer ${controller_auth_token}"
  controller_tenant="x-tenant-id: slug:${instance_slug}"
  controller_namespace="x-namespace-id: hosted-genesis"
  controller_request_id="x-request-id: e2e-kill-${conv2}"

  # The control plane returns before the AI worker starts the MicroVM. Poll the
  # governed controller until the AppTheory session/cache record exists, then
  # immediately terminate it. This is the noninteractive equivalent of the
  # manual "kill the VM mid-turn" operator step and still exercises the real
  # controller/provider/recovery path.
  kill_deadline=$(( $(date +%s) + RECOVER_POLL_DEADLINE_S ))
  terminated="false"
  while [[ $(date +%s) -lt $kill_deadline ]]; do
    get_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' \
      -H "$controller_auth" \
      -H "$controller_tenant" \
      -H "$controller_namespace" \
      -H "$controller_request_id" \
      "${controller_endpoint}/${conv2}" || true)
    get_status=$(printf '%s' "$get_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
    get_body=$(printf '%s' "$get_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')
    get_error=$(printf '%s' "$get_body" | python3 -c 'import sys,json
try:
    data=json.load(sys.stdin)
    err=data.get("error") or {}
    print(err.get("code") or "")
except Exception:
    print("")' 2>/dev/null || true)
    if [[ "$get_status" =~ ^2[0-9][0-9]$ && -z "$get_error" ]]; then
      term_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' \
        -X DELETE \
        -H "$controller_auth" \
        -H "$controller_tenant" \
        -H "$controller_namespace" \
        -H "$controller_request_id" \
        "${controller_endpoint}/${conv2}" || true)
      term_status=$(printf '%s' "$term_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
      term_body=$(printf '%s' "$term_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')
      term_state=$(printf '%s' "$term_body" | python3 -c 'import sys,json
try:
    data=json.load(sys.stdin)
    print(data.get("lifecycle_state") or data.get("state") or "")
except Exception:
    print("")' 2>/dev/null || true)
      echo "    controller terminate: HTTP ${term_status} state=${term_state}"
      if [[ "$term_status" =~ ^2[0-9][0-9]$ ]]; then
        terminated="true"
        break
      fi
    else
      echo "    controller wait: HTTP ${get_status} error=${get_error:-none}"
    fi
    sleep "$POLL_INTERVAL_S"
  done
  unset controller_auth_token controller_auth
  if [[ "$terminated" != "true" ]]; then
    echo "FAIL: --auto-kill-controller did not observe and terminate MicroVM session ${conv2} before deadline." >&2
    exit 1
  fi
else
  echo "    ==> OPERATOR ACTION: kill the VM servicing conversation ${conv2} now."
  echo "        From the lab AWS account, terminate the Lambda MicroVM session for"
  echo "        this conversation (console or: aws lambda terminate-microvm ...)."
  echo "        Press ENTER once the VM is killed to continue the recovery poll."
  read -r _ < /dev/tty
fi

# Poll for the recover path to surface failed.
recover_deadline=$(( $(date +%s) + RECOVER_POLL_DEADLINE_S ))
kill_observed=""
while [[ $(date +%s) -lt $recover_deadline ]]; do
  sleep "$POLL_INTERVAL_S"
  # The recover endpoint drives the controller get + maps terminal -> failed.
  curl -sS -o /dev/null -X POST "${base}/${conv2}/recover" -H "$auth" -H "$json_ct" -d '{}' || true
  poll_resp=$(curl -sS -X GET "${base}/${conv2}" -H "$auth" || true)
  kill_observed=$(printf '%s' "$poll_resp" | python3 -c 'import sys,json;print(json.load(sys.stdin)["conversation"]["status"])' 2>/dev/null || true)
  echo "    kill-arc poll: status=${kill_observed}"
  if [[ "$kill_observed" == "failed" ]]; then
    break
  fi
done
if [[ "$kill_observed" != "failed" ]]; then
  echo "FAIL: kill-VM recovery did not surface failed (last status=${kill_observed})" >&2
  exit 1
fi
echo "    recovery surfaced failed: ${kill_observed}"

# Retry works: a new accept on a fresh conversation dispatches a fresh VM.
retry_accept=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X POST "$base" -H "$auth" -H "$json_ct" -d \
  "$(cat <<EOF
{"model":"${e2e_model}","message":"${e2e_retry_message}","idempotencyKey":"e2e-retry-$(date +%s)-$$","correlationID":"e2e-retry"}
EOF
)" || true)
retry_status=$(printf '%s' "$retry_accept" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
echo "    retry accept: HTTP ${retry_status}"
if [[ "$retry_status" != "202" ]]; then
  echo "FAIL: retry accept expected 202, got ${retry_status}" >&2
  exit 1
fi
echo "==> kill-VM recovery arc: PASS"
echo "==> H1.5 E2E gate: PASS (stub/local-proved + lab deploy)"
