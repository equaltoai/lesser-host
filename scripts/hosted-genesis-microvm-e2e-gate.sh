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
#      ControllerRuntimeDispatcher wired via the H1.5 NewServer seam against an
#      in-memory MemorySessionRegistry + stub provider. Proves the happy path,
#      kill-VM recovery, and the MaximumDurationSeconds timeout-budget wiring.
#      This is the proof that runs in CI; it does NOT call AWS.
#   2. LAB DEPLOY GATE (runs only when LAB_E2E_HOST is set): drives the deployed
#      lab control-plane endpoints with a real instance key + registration id.
#      The principal runs this against the lab deploy after `theory app up
#      --stage lab`. It exercises the live MicroVM path end-to-end.
#
# Timeout-budget wiring (roadmap decision 7):
#   - accept 202 must return in <2s  (ACCEPT_BUDGET_S=2)
#   - session MaximumDurationSeconds is sized for the longest LLM turn plus
#     in-VM extraction (HOSTED_GENESIS_MICROVM_MAXIMUM_DURATION_SECONDS, default
#     300s) — set on the CDK control-plane Lambda env and threaded onto the
#     dispatched run request (verified by the stub gate).
#   - controller Lambda stays at 30s (lifecycle only); the assistant turn runs
#     inside the MicroVM, not the controller Lambda.
#
# Usage:
#   # CI / local stub proof (no AWS):
#   bash scripts/hosted-genesis-microvm-e2e-gate.sh
#
#   # Lab deploy gate (principal only; requires lab deploy + an instance key):
#   LAB_E2E_HOST=https://lesser.host \
#   LAB_E2E_INSTANCE_KEY=<raw instance api key> \
#   LAB_E2E_REGISTRATION_ID=<registration id> \
#   LAB_E2E_AGENT_ID=<agent id hex> \
#   bash scripts/hosted-genesis-microvm-e2e-gate.sh
#
# Exit codes: 0 = both phases passed (or only the stub phase, when
# LAB_E2E_HOST is unset). Non-zero = a phase failed.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# ---------------------------------------------------------------------------
# Phase 1 — stub/local-proved gate (CI-safe, no AWS).
# ---------------------------------------------------------------------------
echo "==> [1/2] stub/local-proved E2E gate (no AWS)"
go test ./internal/controlplane \
  -run 'TestH1_5_E2E_HappyPathAndKillVMRecovery|TestH1_5_E2E_MaximumDurationSecondsWired|TestH1_5_E2E_HarnessConfigCompleteGuard' \
  -count=1
# NewServer wiring + fail-closed + explicit-503 guards (also CI-safe).
go test ./internal/controlplane \
  -run 'TestH1_5_NewServerWiresRealControllerRuntimeDispatcherForDeployedStages|TestH1_5_NewServerSetsNonNilDispatcherOnServer|TestH1_5_NewServerLeavesDispatcherNilWhenConfigIncomplete|TestH1_5_NewServerLeavesDispatcherNilForEmptyConfig|TestH1_5_DispatcherConstructionFailureFailsLoudlyNoSyncFallback|TestH1_5_MicroVMUnavailableAcceptPathReturnsExplicit503' \
  -count=1
go test ./cmd/hosted-genesis-microvm-controller \
  -run 'TestControllerEventFailsClosedWhenDisabled|TestControllerEventFailsClosedWhenAuthLoosened|TestControllerEventGateNoLongerLabOnly' \
  -count=1
echo "==> [1/2] stub/local-proved E2E gate: PASS"

# ---------------------------------------------------------------------------
# Phase 2 — lab deploy gate (principal only).
# ---------------------------------------------------------------------------
if [[ -z "${LAB_E2E_HOST:-}" ]]; then
  echo "==> [2/2] lab deploy gate: SKIPPED (set LAB_E2E_HOST to run against lab)"
  echo "==> H1.5 E2E gate: PASS (stub/local-proved)"
  exit 0
fi

: "${LAB_E2E_INSTANCE_KEY:?LAB_E2E_INSTANCE_KEY (raw instance api key) is required for the lab gate}"
: "${LAB_E2E_REGISTRATION_ID:?LAB_E2E_REGISTRATION_ID is required for the lab gate}"
: "${LAB_E2E_AGENT_ID:?LAB_E2E_AGENT_ID (agent id hex) is required for the lab gate}"

ACCEPT_BUDGET_S="${ACCEPT_BUDGET_S:-2}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-3}"
POLL_DEADLINE_S="${POLL_DEADLINE_S:-300}"          # >30s LLM turn + extraction headroom
RECOVER_POLL_DEADLINE_S="${RECOVER_POLL_DEADLINE_S:-60}"

base="$LAB_E2E_HOST/api/v1/soul/instance/agents/register/${LAB_E2E_REGISTRATION_ID}/mint-conversation"
auth="Authorization: Bearer ${LAB_E2E_INSTANCE_KEY}"
json_ct="Content-Type: application/json"
now_ms() { date +%s%3N; }

echo "==> [2/2] lab deploy gate against ${LAB_E2E_HOST}"

# --- Accept: POST /mint-conversation -> expect 202 in_progress ---
idem="e2e-$(date +%s)-$$"
accept_body=$(cat <<EOF
{"model":"${LAB_E2E_MODEL:-anthropic:claude-sonnet-4-6}","message":"${LAB_E2E_MESSAGE:-Please draft my soul declaration. Take your time; this is a long reflection.}","idempotencyKey":"${idem}","correlationID":"e2e-${idem}"}
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

conversation_id=$(printf '%s' "$accept_body_clean" | python3 -c 'import sys,json;print(json.load(sys.stdin)["conversation"]["id"])' 2>/dev/null || true)
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
  if [[ "$status" == "assistant_turn_ready" || "$status" == "declaration_extraction_pending" ]]; then
    break
  fi
  if [[ "$status" == "failed" ]]; then
    echo "FAIL: conversation entered failed during accept->ready poll" >&2
    exit 1
  fi
done
if [[ "$status" != "assistant_turn_ready" && "$status" != "declaration_extraction_pending" ]]; then
  echo "FAIL: timed out waiting for assistant_turn_ready (last status=${status})" >&2
  exit 1
fi
echo "    assistant turn ready: ${status}"

# --- Complete -> in-VM declaration extraction -> poll for declaration_ready ---
complete_resp=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X POST "${base}/${conversation_id}/complete" \
  -H "$auth" -H "$json_ct" -d '{}' || true)
complete_status=$(printf '%s' "$complete_resp" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
echo "    complete: HTTP ${complete_status}"
if [[ "$complete_status" != "200" && "$complete_status" != "202" ]]; then
  echo "FAIL: complete expected 200/202, got ${complete_status}: $(printf '%s' "$complete_resp" | sed 's/__HTTP_STATUS__[0-9]*$//')" >&2
  exit 1
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

# --- Kill-VM recovery arc (second conversation) ---
echo "==> kill-VM recovery arc"
idem2="e2e-kill-$(date +%s)-$$"
kill_body=$(cat <<EOF
{"model":"${LAB_E2E_MODEL:-anthropic:claude-sonnet-4-6}","message":"${LAB_E2E_MESSAGE:-A second long reflection for the kill-VM recovery arc.}","idempotencyKey":"${idem2}","correlationID":"e2e-${idem2}"}
EOF
)
kill_accept=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X POST "$base" -H "$auth" -H "$json_ct" -d "$kill_body" || true)
kill_status=$(printf '%s' "$kill_accept" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
conv2=$(printf '%s' "$kill_accept" | sed 's/__HTTP_STATUS__[0-9]*$//' | \
  python3 -c 'import sys,json;print(json.load(sys.stdin)["conversation"]["id"])' 2>/dev/null || true)
echo "    kill-arc accept: HTTP ${kill_status} conversation=${conv2}"
if [[ "$kill_status" != "202" || -z "$conv2" ]]; then
  echo "FAIL: kill-arc accept expected 202 + conversation id, got ${kill_status} ${conv2}" >&2
  exit 1
fi

echo "    ==> OPERATOR ACTION: kill the VM servicing conversation ${conv2} now."
echo "        From the lab AWS account, terminate the Lambda MicroVM session for"
echo "        this conversation (console or: aws lambda terminate-microvm ...)."
echo "        Press ENTER once the VM is killed to continue the recovery poll."
read -r _ < /dev/tty

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
{"model":"${LAB_E2E_MODEL:-anthropic:claude-sonnet-4-6}","message":"Retry after kill-VM recovery.","idempotencyKey":"e2e-retry-$(date +%s)-$$","correlationID":"e2e-retry"}
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
