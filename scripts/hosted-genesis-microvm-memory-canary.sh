#!/usr/bin/env bash
# Lab-only Project 48 M11 proof harness: launch an AppTheory MicroVM session,
# initialize a random non-secret in-process nonce through the workload canary
# endpoint, suspend, wait, resume, and verify whether the nonce survived. This
# script intentionally contains NO AWS CLI calls, no deploy commands, no SSM reads,
# and no raw provider/Lambda MicroVM SDK access. It drives only the governed
# AppTheory controller HTTP routes using a controller bearer token supplied by the
# operator in a local gitignored file.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

stage=""
outputs_file=""
controller_token_file="scripts/.hosted-genesis-microvm-controller-token"
wait_seconds="600"
evidence_file=""
region="us-east-1"

usage() {
  cat >&2 <<'USAGE'
usage: bash scripts/hosted-genesis-microvm-memory-canary.sh \
  --stage lab \
  --outputs-file cdk/lab-outputs.json \
  [--controller-token-file scripts/.hosted-genesis-microvm-controller-token] \
  [--wait-seconds 600] \
  [--evidence-file tmp/hosted-genesis-microvm-memory-canary.json]

Runs the #941 suspend/resume process-memory proof against an already deployed lab
stack. The script itself does not call AWS, deploy, read SSM, mutate secrets, or
persist plaintext nonce/token material. The controller token file is a local
runtime credential provisioned out of band by the operator and must never be
committed or copied into evidence.
USAGE
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
    --controller-token-file)
      controller_token_file="${2:-}"
      shift 2
      ;;
    --wait-seconds)
      wait_seconds="${2:-}"
      shift 2
      ;;
    --evidence-file)
      evidence_file="${2:-}"
      shift 2
      ;;
    --region)
      # Kept as safe evidence metadata only. The script does not call AWS.
      region="${2:-}"
      shift 2
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
  echo "FAIL: this lab-only proof requires --stage lab" >&2
  exit 2
fi
if [[ -z "$outputs_file" || ! -f "$outputs_file" ]]; then
  echo "FAIL: --outputs-file must point to lab CDK outputs JSON" >&2
  exit 1
fi
if [[ ! "$wait_seconds" =~ ^[0-9]+$ || "$wait_seconds" -lt 1 || "$wait_seconds" -gt 1800 ]]; then
  echo "FAIL: --wait-seconds must be an integer from 1 to 1800" >&2
  exit 2
fi
if [[ ! -s "$controller_token_file" ]]; then
  echo "FAIL: controller token file is missing or empty: ${controller_token_file}" >&2
  echo "      Provision it out of band, chmod 600, and never commit it." >&2
  exit 1
fi

resolve_cfn_output() {
  local key="$1"
  python3 - "$outputs_file" "$key" <<'PY'
import json, sys
path, key = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as fh:
    data = json.load(fh)
if isinstance(data, dict):
    for _stack, outs in data.items():
        if isinstance(outs, dict) and key in outs:
            print(outs[key])
            sys.exit(0)
if isinstance(data, list):
    for item in data:
        if isinstance(item, dict) and item.get("OutputKey") == key:
            print(item.get("OutputValue", ""))
            sys.exit(0)
PY
}

json_field() {
  local field="$1"
  python3 -c '
import json, sys
field = sys.argv[1]
try:
    data = json.load(sys.stdin)
except Exception:
    print("")
    sys.exit(0)
value = data
for part in field.split("."):
    if isinstance(value, dict):
        value = value.get(part)
    else:
        value = None
        break
if isinstance(value, bool):
    print("true" if value else "false")
elif value is None:
    print("")
else:
    print(value)
' "$field"
}

controller_summary() {
  python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print(json.dumps({"decode_error": True}, sort_keys=True))
    sys.exit(0)
allowed = {}
for key in [
    "command",
    "request_id",
    "tenant_id",
    "namespace",
    "session_id",
    "state",
    "desired_state",
    "lifecycle_state",
    "microvm_id",
    "provider_microvm_id",
    "provider_state",
    "last_action",
    "registry_version",
]:
    if key in data and data[key] not in (None, ""):
        allowed[key] = data[key]
err = data.get("error")
if isinstance(err, dict):
    allowed["error"] = {k: err.get(k) for k in ["code", "message", "request_id"] if err.get(k)}
print(json.dumps(allowed, sort_keys=True, separators=(",", ":")))
'
}

canary_summary() {
  python3 -c '
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    print(json.dumps({"decode_error": True}, sort_keys=True))
    sys.exit(0)
allowed = {}
for key in [
    "canary",
    "request_id",
    "tenant_id",
    "namespace",
    "session_id",
    "correlation_id",
    "nonce_hash",
    "checkpoint_marker",
    "initialized",
    "memory_preserved",
]:
    if key in data and data[key] is not None:
        allowed[key] = data[key]
print(json.dumps(allowed, sort_keys=True, separators=(",", ":")))
'
}

controller_endpoint="$(resolve_cfn_output 'HostedGenesisMicrovmControllerEndpoint')"
if [[ -z "$controller_endpoint" ]]; then
  echo "FAIL: HostedGenesisMicrovmControllerEndpoint not found in ${outputs_file}" >&2
  exit 1
fi
controller_endpoint="${controller_endpoint%/}"
controller_token="$(tr -d '\r\n' < "$controller_token_file")"
if [[ -z "$controller_token" ]]; then
  echo "FAIL: controller token file is empty after trimming newlines" >&2
  exit 1
fi

session_id="p48-m11-memory-canary-$(date +%s)-$$"
tenant_id="slug:lab-memory-canary"
namespace="hosted-genesis"
checkpoint_marker="checkpoint:p48-m11:${session_id}"
canary_path="/hosted-genesis/lab/process-memory-canary"
max_duration_seconds=300
idle_max_seconds=300
idle_suspended_seconds=1800
idle_auto_resume=false
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if [[ -z "$evidence_file" ]]; then
  evidence_file="tmp/hosted-genesis-microvm-memory-canary-${session_id}.json"
fi
mkdir -p "$(dirname "$evidence_file")"

auth_header="Authorization: Bearer ${controller_token}"
tenant_header="x-tenant-id: ${tenant_id}"
namespace_header="x-namespace-id: ${namespace}"
json_ct="Content-Type: application/json"

http_status=""
http_body=""
controller_request() {
  local method="$1"
  local url="$2"
  local request_id="$3"
  local body="${4:-}"
  local response
  if [[ -n "$body" ]]; then
    response=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X "$method" "$url" \
      -H "$auth_header" -H "$tenant_header" -H "$namespace_header" -H "x-request-id: ${request_id}" -H "$json_ct" \
      -d "$body" || true)
  else
    response=$(curl -sS -w '\n__HTTP_STATUS__%{http_code}' -X "$method" "$url" \
      -H "$auth_header" -H "$tenant_header" -H "$namespace_header" -H "x-request-id: ${request_id}" || true)
  fi
  http_status=$(printf '%s' "$response" | sed -n 's/.*__HTTP_STATUS__\([0-9]*\).*/\1/p')
  http_body=$(printf '%s' "$response" | sed 's/__HTTP_STATUS__[0-9]*$//')
}

require_2xx() {
  local label="$1"
  if [[ ! "$http_status" =~ ^2[0-9][0-9]$ ]]; then
    echo "FAIL: ${label} expected 2xx, got HTTP ${http_status}: ${http_body}" >&2
    exit 1
  fi
}

run_body=$(python3 - <<PY
import json
print(json.dumps({
  "session_id": "${session_id}",
  "maximum_duration_seconds": ${max_duration_seconds},
  "idle_policy": {
    "max_idle_duration_seconds": ${idle_max_seconds},
    "suspended_duration_seconds": ${idle_suspended_seconds},
    "auto_resume_enabled": False,
  },
  "session_spec": {"metadata": {
    "canary": "process-memory",
    "project": "48",
    "milestone": "M11",
    "issue": "941",
    "checkpoint_marker": "${checkpoint_marker}",
    "source_of_truth": "host-lab-canary-metadata-only",
  }},
}, separators=(",", ":")))
PY
)

echo "==> run: ${session_id} through AppTheory POST /microvms"
controller_request POST "$controller_endpoint" "canary-run-${session_id}" "$run_body"
require_2xx "run"
run_summary="$(printf '%s' "$http_body" | controller_summary)"
run_state="$(printf '%s' "$http_body" | json_field 'lifecycle_state')"
[[ -n "$run_state" ]] || run_state="$(printf '%s' "$http_body" | json_field 'state')"
provider_before="$(printf '%s' "$http_body" | json_field 'provider_microvm_id')"
[[ -n "$provider_before" ]] || provider_before="$(printf '%s' "$http_body" | json_field 'microvm_id')"
echo "    run: HTTP ${http_status} state=${run_state:-unknown} provider_microvm_id=${provider_before:-unknown}"

canary_event_1=$(python3 - <<PY
import json
print(json.dumps({
  "request_id": "canary-init-${session_id}",
  "tenant_id": "${tenant_id}",
  "namespace": "${namespace}",
  "session_id": "${session_id}",
  "hook": "run",
  "state": "running",
  "metadata": {
    "canary": "process-memory",
    "checkpoint_marker": "${checkpoint_marker}",
  },
}, separators=(",", ":")))
PY
)

echo "==> invoke: initialize process-memory nonce (hash only returned)"
controller_request POST "${controller_endpoint}/${session_id}/invoke${canary_path}" "canary-init-${session_id}" "$canary_event_1"
require_2xx "canary init invoke"
init_summary="$(printf '%s' "$http_body" | canary_summary)"
nonce_hash="$(printf '%s' "$http_body" | json_field 'nonce_hash')"
correlation_id="$(printf '%s' "$http_body" | json_field 'correlation_id')"
if [[ -z "$nonce_hash" || "$nonce_hash" != sha256:* || -z "$correlation_id" ]]; then
  echo "FAIL: canary init did not return hash/correlation metadata: ${http_body}" >&2
  exit 1
fi
echo "    canary init: nonce_hash=${nonce_hash} correlation_id=${correlation_id}"

controller_request GET "${controller_endpoint}/${session_id}" "canary-get-before-${session_id}"
require_2xx "get before suspend"
get_before_summary="$(printf '%s' "$http_body" | controller_summary)"
state_before_suspend="$(printf '%s' "$http_body" | json_field 'lifecycle_state')"
[[ -n "$state_before_suspend" ]] || state_before_suspend="$(printf '%s' "$http_body" | json_field 'state')"
provider_get_before="$(printf '%s' "$http_body" | json_field 'provider_microvm_id')"
[[ -n "$provider_get_before" ]] || provider_get_before="$provider_before"

echo "==> suspend: AppTheory POST /microvms/{session_id}/suspend"
controller_request POST "${controller_endpoint}/${session_id}/suspend" "canary-suspend-${session_id}" '{}'
require_2xx "suspend"
suspend_summary="$(printf '%s' "$http_body" | controller_summary)"
suspend_state="$(printf '%s' "$http_body" | json_field 'lifecycle_state')"
[[ -n "$suspend_state" ]] || suspend_state="$(printf '%s' "$http_body" | json_field 'state')"
echo "    suspend: HTTP ${http_status} state=${suspend_state:-unknown}"

echo "==> wait: ${wait_seconds}s human-scale idle interval"
sleep "$wait_seconds"

echo "==> resume: AppTheory POST /microvms/{session_id}/resume"
controller_request POST "${controller_endpoint}/${session_id}/resume" "canary-resume-${session_id}" '{}'
require_2xx "resume"
resume_summary="$(printf '%s' "$http_body" | controller_summary)"
resume_state="$(printf '%s' "$http_body" | json_field 'lifecycle_state')"
[[ -n "$resume_state" ]] || resume_state="$(printf '%s' "$http_body" | json_field 'state')"
provider_after="$(printf '%s' "$http_body" | json_field 'provider_microvm_id')"
[[ -n "$provider_after" ]] || provider_after="$(printf '%s' "$http_body" | json_field 'microvm_id')"
echo "    resume: HTTP ${http_status} state=${resume_state:-unknown} provider_microvm_id=${provider_after:-unknown}"

controller_request GET "${controller_endpoint}/${session_id}" "canary-get-after-${session_id}"
require_2xx "get after resume"
get_after_summary="$(printf '%s' "$http_body" | controller_summary)"
state_after_resume="$(printf '%s' "$http_body" | json_field 'lifecycle_state')"
[[ -n "$state_after_resume" ]] || state_after_resume="$(printf '%s' "$http_body" | json_field 'state')"
provider_get_after="$(printf '%s' "$http_body" | json_field 'provider_microvm_id')"
[[ -n "$provider_get_after" ]] || provider_get_after="$provider_after"

canary_event_2=$(python3 - <<PY
import json
print(json.dumps({
  "request_id": "canary-verify-${session_id}",
  "tenant_id": "${tenant_id}",
  "namespace": "${namespace}",
  "session_id": "${session_id}",
  "hook": "run",
  "state": "ready",
  "metadata": {
    "canary": "process-memory",
    "checkpoint_marker": "${checkpoint_marker}",
    "expected_nonce_hash": "${nonce_hash}",
  },
}, separators=(",", ":")))
PY
)

echo "==> invoke: verify process-memory nonce survival"
controller_request POST "${controller_endpoint}/${session_id}/invoke${canary_path}" "canary-verify-${session_id}" "$canary_event_2"
require_2xx "canary verify invoke"
verify_summary="$(printf '%s' "$http_body" | canary_summary)"
memory_preserved="$(printf '%s' "$http_body" | json_field 'memory_preserved')"
verify_hash="$(printf '%s' "$http_body" | json_field 'nonce_hash')"
if [[ "$memory_preserved" != "true" && "$memory_preserved" != "false" ]]; then
  echo "FAIL: canary verify did not return memory_preserved boolean: ${http_body}" >&2
  exit 1
fi
if [[ "$verify_hash" != "$nonce_hash" ]]; then
  echo "FAIL: canary verify hash mismatch: init=${nonce_hash} verify=${verify_hash}" >&2
  exit 1
fi

echo "==> terminate: cleanup AppTheory session"
controller_request DELETE "${controller_endpoint}/${session_id}" "canary-terminate-${session_id}"
# Termination should succeed, but evidence remains useful if the proof already ran.
terminate_status="$http_status"
terminate_summary="$(printf '%s' "$http_body" | controller_summary)"
if [[ ! "$terminate_status" =~ ^2[0-9][0-9]$ ]]; then
  echo "WARN: terminate returned HTTP ${terminate_status}; operator should clean up session ${session_id}" >&2
fi
completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

python3 - "$evidence_file" <<PY
import json, sys
path = sys.argv[1]
evidence = {
  "schema": "hosted_genesis_microvm_process_memory_canary.v1",
  "issue": 941,
  "stage": "${stage}",
  "region": "${region}",
  "controller_endpoint_source": "${outputs_file}",
  "controller_endpoint_output_key": "HostedGenesisMicrovmControllerEndpoint",
  "controller_token_source": "local gitignored file; plaintext not persisted in evidence",
  "started_at": "${started_at}",
  "completed_at": "${completed_at}",
  "tenant_id": "${tenant_id}",
  "namespace": "${namespace}",
  "session_id": "${session_id}",
  "checkpoint_marker": "${checkpoint_marker}",
  "maximum_duration_seconds": ${max_duration_seconds},
  "idle_policy": {
    "max_idle_duration_seconds": ${idle_max_seconds},
    "suspended_duration_seconds": ${idle_suspended_seconds},
    "auto_resume_enabled": False,
  },
  "idle_interval_seconds": int("${wait_seconds}"),
  "provider_microvm_id_before_suspend": "${provider_get_before}",
  "provider_microvm_id_after_resume": "${provider_get_after}",
  "lifecycle_states": {
    "run": "${run_state}",
    "before_suspend": "${state_before_suspend}",
    "suspend": "${suspend_state}",
    "resume": "${resume_state}",
    "after_resume": "${state_after_resume}",
  },
  "canary": {
    "correlation_id": "${correlation_id}",
    "nonce_hash": "${nonce_hash}",
    "memory_preserved": True if "${memory_preserved}" == "true" else False,
  },
  "command_summaries": {
    "run": json.loads('''${run_summary}'''),
    "invoke_initialize": json.loads('''${init_summary}'''),
    "get_before_suspend": json.loads('''${get_before_summary}'''),
    "suspend": json.loads('''${suspend_summary}'''),
    "resume": json.loads('''${resume_summary}'''),
    "get_after_resume": json.loads('''${get_after_summary}'''),
    "invoke_verify": json.loads('''${verify_summary}'''),
    "terminate": {"http_status": "${terminate_status}", "summary": json.loads('''${terminate_summary}''')},
  },
  "safety_review": {
    "plaintext_nonce_persisted": False,
    "raw_controller_token_persisted": False,
    "raw_lifecycle_payload_persisted": False,
    "raw_prompt_or_transcript_persisted": False,
    "provider_keys_or_ssm_values_persisted": False,
    "microvm_endpoint_tokens_persisted": False,
    "checkpoint_marker_contains_nonce_plaintext": False,
  },
}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(evidence, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

unset controller_token auth_header
echo "==> memory_preserved=${memory_preserved}"
echo "==> evidence: ${evidence_file}"
