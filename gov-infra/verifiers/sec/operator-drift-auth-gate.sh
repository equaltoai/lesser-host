#!/usr/bin/env bash
# SEC-12: Operator drift/release/remediation auth gate.
#
# Verifies that all three operator fleet endpoints are protected by both
# request authentication (apptheory.RequireAuth()) and operator-role
# authorization (requireOperator(ctx)). The endpoints are:
#
#   1. GET  /api/v1/operators/releases               → handleOperatorReleases
#   2. GET  /api/v1/operators/instances/drift          → handleOperatorInstancesDrift
#   3. POST /api/v1/operators/instances/remediate-mcp-drift → handleOperatorRemediateMCPDrift
#
# Full points: all three endpoints have the exact expected method/path/handler
#   route with apptheory.RequireAuth(), and requireOperator(ctx) appears inside
#   the expected handler body before any store/fleet read or write operation.
# Zero points: any endpoint is missing, routed to the wrong handler, lacks
#   RequireAuth(), lacks requireOperator(ctx), or performs sensitive work before
#   requireOperator(ctx).
#
# No partial credit.
#
# Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.14
# Issue: equaltoai/lesser-host#440

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

python3 - <<'PY'
from pathlib import Path
import re
import sys

ROUTES_FILE = Path("internal/controlplane/server.go")
ENDPOINTS = [
    {
        "method": "Get",
        "path": "/api/v1/operators/releases",
        "handler": "handleOperatorReleases",
        "file": Path("internal/controlplane/handlers_operator_releases.go"),
        "sensitive": ["requireStoreDB", "listActiveInstances", "gatherFleetEvidence", "buildOperatorReleasesResponse", "apptheory.JSON"],
    },
    {
        "method": "Get",
        "path": "/api/v1/operators/instances/drift",
        "handler": "handleOperatorInstancesDrift",
        "file": Path("internal/controlplane/handlers_operator_drift.go"),
        "sensitive": ["requireStoreDB", "listActiveInstances", "gatherFleetEvidence", "computeFleetDrift", "apptheory.JSON"],
    },
    {
        "method": "Post",
        "path": "/api/v1/operators/instances/remediate-mcp-drift",
        "handler": "handleOperatorRemediateMCPDrift",
        "file": Path("internal/controlplane/handlers_operator_remediate_mcp.go"),
        "sensitive": [
            "requireStoreDB",
            "listActiveInstances",
            "gatherFleetEvidence",
            "computeFleetDrift",
            "ListActiveUpdateJobs",
            "remediateWireStaleInstances",
            "createRemediationMCPJob",
            ".Create(",
            "emitRemediationAudit",
            "apptheory.JSON",
        ],
    },
]

print("SEC-12: Operator drift/release/remediation auth gate")
print(f"Routes file: {ROUTES_FILE}")
print("")

if not ROUTES_FILE.is_file():
    print(f"FAIL: routes file missing: {ROUTES_FILE}", file=sys.stderr)
    sys.exit(1)

failures: list[str] = []
route_lines = ROUTES_FILE.read_text(encoding="utf-8").splitlines()


def code_lines(lines: list[str]):
    for idx, line in enumerate(lines, start=1):
        stripped = line.strip()
        if stripped.startswith("//") or stripped.startswith("/*") or stripped.startswith("*"):
            continue
        yield idx, line


def find_function_body(path: Path, handler: str):
    if not path.is_file():
        failures.append(f"handler file missing for {handler}: {path}")
        return None
    lines = path.read_text(encoding="utf-8").splitlines()
    start = None
    pattern = re.compile(rf"^func\s+\([^)]*\)\s+{re.escape(handler)}\s*\(")
    for idx, line in enumerate(lines, start=1):
        if pattern.search(line):
            start = idx
            break
    if start is None:
        failures.append(f"handler function not found: {handler} in {path}")
        return None
    end = len(lines) + 1
    for idx in range(start + 1, len(lines) + 1):
        if re.match(r"^func\s", lines[idx - 1]):
            end = idx
            break
    return path, lines, start, end

for ep in ENDPOINTS:
    method = ep["method"]
    path = ep["path"]
    handler = ep["handler"]
    route_re = re.compile(
        rf'app\.{method}\(\s*"{re.escape(path)}"\s*,\s*s\.{handler}\s*,\s*apptheory\.RequireAuth\(\)\s*\)'
    )
    exact_matches = [(idx, line) for idx, line in code_lines(route_lines) if route_re.search(line)]
    if exact_matches:
        idx, line = exact_matches[0]
        print(f"PASS: {method.upper()} {path} line {idx}: expected handler {handler} + RequireAuth()")
    else:
        nearby = [
            f"line {idx}: {line.strip()}"
            for idx, line in code_lines(route_lines)
            if f'"{path}"' in line
        ]
        detail = "; ".join(nearby) if nearby else "route not found"
        failures.append(
            f"{method.upper()} {path}: expected s.{handler} with apptheory.RequireAuth(); found {detail}"
        )

    body = find_function_body(ep["file"], handler)
    if body is None:
        continue
    handler_file, lines, start, end = body
    body_code = list(code_lines(lines[start - 1 : end - 1]))
    # code_lines returns slice-relative line numbers; convert to absolute.
    body_code = [(start + rel_idx - 1, line) for rel_idx, line in body_code]

    require_lines = [idx for idx, line in body_code if "requireOperator(ctx)" in line]
    if not require_lines:
        failures.append(f"{handler}: requireOperator(ctx) not found inside {handler_file}:{start}-{end - 1}")
        continue
    require_line = require_lines[0]

    sensitive_hits: list[tuple[int, str, str]] = []
    for idx, line in body_code:
        for token in ep["sensitive"]:
            if token in line:
                sensitive_hits.append((idx, token, line.strip()))
                break
    sensitive_hits = [hit for hit in sensitive_hits if hit[0] != require_line]
    if not sensitive_hits:
        failures.append(f"{handler}: no sensitive operation tokens found to order-check")
        continue
    first_sensitive = min(sensitive_hits, key=lambda item: item[0])
    if require_line < first_sensitive[0]:
        print(
            f"PASS: {handler}: requireOperator(ctx) line {require_line} precedes first sensitive operation "
            f"{first_sensitive[1]} line {first_sensitive[0]}"
        )
    else:
        failures.append(
            f"{handler}: requireOperator(ctx) line {require_line} does not precede first sensitive operation "
            f"{first_sensitive[1]} line {first_sensitive[0]} ({first_sensitive[2]})"
        )

print("")
if failures:
    for failure in failures:
        print(f"FAIL: {failure}", file=sys.stderr)
    print("FAIL: one or more operator endpoints lack auth/role protection", file=sys.stderr)
    sys.exit(1)

print("PASS: all three operator endpoints protected by exact route RequireAuth() + pre-read requireOperator(ctx)")
PY
