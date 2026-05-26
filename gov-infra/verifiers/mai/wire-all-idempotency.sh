#!/usr/bin/env bash
# MAI-5: Wire-all idempotency and auditability.
#
# Verifies that the POST /api/v1/operators/instances/remediate-mcp-drift
# endpoint is idempotent and auditable. The handler must:
#
#   1. Check for existing active jobs through the GSI2 UPDATE_ACTIVE index.
#   2. Filter active jobs by MCPOnly == true before populating activeMCPSlugs.
#   3. Skip duplicate slugs before creating new UpdateJobs.
#   4. Create new UpdateJobs with MCPOnly: true.
#   5. Emit an audit event with slug and job ID context.
#
# Full points: all five conditions are met in the scoped function bodies.
# Zero points: any condition is absent or ordered after the operation it guards.
#
# No partial credit.
#
# Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.15
# Issue: equaltoai/lesser-host#441

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

python3 - <<'PY'
from pathlib import Path
import re
import sys

HANDLER_FILE = Path("internal/controlplane/handlers_operator_remediate_mcp.go")
STORE_FILE = Path("internal/store/updates.go")

print("MAI-5: Wire-all idempotency and auditability")
print(f"Handler file: {HANDLER_FILE}")
print(f"Store file:    {STORE_FILE}")
print("")

failures: list[str] = []


def load(path: Path) -> list[str]:
    if not path.is_file():
        failures.append(f"file missing: {path}")
        return []
    return path.read_text(encoding="utf-8").splitlines()


def code_lines(lines: list[str], offset: int = 0):
    for rel, line in enumerate(lines, start=1):
        stripped = line.strip()
        if stripped.startswith("//") or stripped.startswith("/*") or stripped.startswith("*"):
            continue
        yield offset + rel, line


def function_body(path: Path, name: str):
    lines = load(path)
    if not lines:
        return []
    pattern = re.compile(rf"^func(?:\s+\([^)]*\))?\s+{re.escape(name)}\s*\(")
    start = None
    for idx, line in enumerate(lines, start=1):
        if pattern.search(line):
            start = idx
            break
    if start is None:
        failures.append(f"function not found: {name} in {path}")
        return []
    end = len(lines) + 1
    for idx in range(start + 1, len(lines) + 1):
        if re.match(r"^func\s", lines[idx - 1]):
            end = idx
            break
    return list(code_lines(lines[start - 1 : end - 1], start - 1))


def first_line(body, needle: str):
    for idx, line in body:
        if needle in line:
            return idx, line.strip()
    return None


def first_regex(body, pattern: str):
    rx = re.compile(pattern)
    for idx, line in body:
        if rx.search(line):
            return idx, line.strip()
    return None


def require(condition: bool, message: str):
    if not condition:
        failures.append(message)

store_body = function_body(STORE_FILE, "ListActiveUpdateJobs")
handler_body = function_body(HANDLER_FILE, "handleOperatorRemediateMCPDrift")
remediate_body = function_body(HANDLER_FILE, "remediateWireStaleInstances")
create_body = function_body(HANDLER_FILE, "createRemediationMCPJob")
audit_body = function_body(HANDLER_FILE, "emitRemediationAudit")

# Step 1: ListActiveUpdateJobs must use the active update-job GSI precisely.
index_hit = first_regex(store_body, r'\.?Index\(\s*"gsi2"\s*\)')
where_hit = first_regex(store_body, r'\.?Where\(\s*"gsi2PK"\s*,\s*"="\s*,\s*"UPDATE_ACTIVE"\s*\)')
order_hit = first_regex(store_body, r'\.?OrderBy\(\s*"gsi2SK"\s*,\s*"ASC"\s*\)')
require(index_hit is not None, "ListActiveUpdateJobs does not select Index(\"gsi2\")")
require(where_hit is not None, "ListActiveUpdateJobs does not require Where(\"gsi2PK\", \"=\", \"UPDATE_ACTIVE\")")
require(order_hit is not None, "ListActiveUpdateJobs does not order by gsi2SK ASC")
if index_hit and where_hit and order_hit:
    print(f"PASS: ListActiveUpdateJobs uses gsi2 active index at lines {index_hit[0]}, {where_hit[0]}, {order_hit[0]}")

# Step 2: Handler must query active jobs before remediation creates jobs.
list_hit = first_line(handler_body, "ListActiveUpdateJobs")
map_decl_hit = first_line(handler_body, "activeMCPSlugs :=")
mcp_filter_hit = first_regex(handler_body, r'job\s*!=\s*nil\s*&&\s*job\.MCPOnly|job\.MCPOnly\s*&&\s*job\s*!=\s*nil')
map_assign_hit = first_regex(handler_body, r'activeMCPSlugs\s*\[[^]]+\]\s*=\s*true')
remediate_call_hit = first_line(handler_body, "s.remediateWireStaleInstances")
audit_call_hit = first_regex(handler_body, r'emitRemediationAudit\(\s*ctx\s*,\s*remediatedSlugs\s*,\s*createdJobIDs')
require(list_hit is not None, "handleOperatorRemediateMCPDrift does not call ListActiveUpdateJobs")
require(map_decl_hit is not None, "handleOperatorRemediateMCPDrift does not build activeMCPSlugs map")
require(mcp_filter_hit is not None, "handleOperatorRemediateMCPDrift does not filter active jobs by job.MCPOnly")
require(map_assign_hit is not None, "handleOperatorRemediateMCPDrift does not assign active MCP slugs")
require(remediate_call_hit is not None, "handleOperatorRemediateMCPDrift does not call remediateWireStaleInstances")
require(audit_call_hit is not None, "handleOperatorRemediateMCPDrift does not pass remediatedSlugs and createdJobIDs to emitRemediationAudit")
if list_hit and remediate_call_hit:
    require(list_hit[0] < remediate_call_hit[0], "ListActiveUpdateJobs must occur before remediation job creation helper is called")
if mcp_filter_hit and map_assign_hit:
    require(mcp_filter_hit[0] < map_assign_hit[0], "job.MCPOnly filter must precede activeMCPSlugs assignment")
if map_assign_hit and remediate_call_hit:
    require(map_assign_hit[0] < remediate_call_hit[0], "activeMCPSlugs population must precede remediation helper call")
if remediate_call_hit and audit_call_hit:
    require(remediate_call_hit[0] < audit_call_hit[0], "audit emission must occur after remediation helper returns created jobs")
if list_hit and mcp_filter_hit and map_assign_hit and remediate_call_hit and audit_call_hit:
    print(
        "PASS: handler queries active jobs, filters MCPOnly, populates activeMCPSlugs, "
        "then remediates and audits in order"
    )

# Step 3: Remediation helper must skip duplicates before create/persist.
skip_hit = first_regex(remediate_body, r'activeMCPSlugs\s*\[[^]]+\]')
continue_after_skip = None
if skip_hit:
    for idx, line in remediate_body:
        if idx > skip_hit[0] and "continue" in line:
            continue_after_skip = (idx, line.strip())
            break
create_call_hit = first_line(remediate_body, "s.createRemediationMCPJob")
persist_hit = first_regex(remediate_body, r'\.Model\(job\)\.Create\(\)')
require(skip_hit is not None, "remediateWireStaleInstances does not check activeMCPSlugs")
require(continue_after_skip is not None, "remediateWireStaleInstances duplicate check does not continue before create")
require(create_call_hit is not None, "remediateWireStaleInstances does not call createRemediationMCPJob")
require(persist_hit is not None, "remediateWireStaleInstances does not persist created UpdateJob")
if skip_hit and create_call_hit:
    require(skip_hit[0] < create_call_hit[0], "duplicate activeMCPSlugs check must precede createRemediationMCPJob")
if continue_after_skip and create_call_hit:
    require(continue_after_skip[0] < create_call_hit[0], "duplicate-skip continue must precede createRemediationMCPJob")
if create_call_hit and persist_hit:
    require(create_call_hit[0] < persist_hit[0], "UpdateJob creation helper must precede persistence")
if skip_hit and continue_after_skip and create_call_hit:
    print(f"PASS: duplicate activeMCPSlugs check lines {skip_hit[0]}-{continue_after_skip[0]} precedes job creation line {create_call_hit[0]}")

# Step 4: Created UpdateJobs must be MCP-only.
mcp_true_hit = first_regex(create_body, r'MCPOnly\s*:\s*true\b')
require(mcp_true_hit is not None, "createRemediationMCPJob does not set MCPOnly: true")
if mcp_true_hit:
    print(f"PASS: createRemediationMCPJob sets MCPOnly: true at line {mcp_true_hit[0]}")

# Step 5: Audit must include action, slugs, and job IDs in the audit target.
action_hit = first_line(audit_body, 'Action:    "operator.fleet.remediate_mcp_drift"')
slugs_join_hit = first_regex(audit_body, r'strings\.Join\(\s*slugs\s*,')
jobids_join_hit = first_regex(audit_body, r'strings\.Join\(\s*jobIDs\s*,')
try_write_hit = first_line(audit_body, "tryWriteAuditLog")
require(action_hit is not None, "emitRemediationAudit does not use operator.fleet.remediate_mcp_drift action")
require(slugs_join_hit is not None, "emitRemediationAudit does not include slug list context")
require(jobids_join_hit is not None, "emitRemediationAudit does not include job ID list context")
require(try_write_hit is not None, "emitRemediationAudit does not write an audit log entry")
if action_hit and slugs_join_hit and jobids_join_hit and try_write_hit:
    print(f"PASS: audit action and slug/job context present at lines {action_hit[0]}, {slugs_join_hit[0]}, {jobids_join_hit[0]}")

print("")
if failures:
    for failure in failures:
        print(f"FAIL: {failure}", file=sys.stderr)
    print("FAIL: wire-all idempotency/auditability conditions not met", file=sys.stderr)
    sys.exit(1)

print("PASS: wire-all remediation is idempotent and auditable")
PY
