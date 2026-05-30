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
#   route with apptheory.RequireAuth(), and each expected handler has a
#   top-level, executable requireOperator(ctx) guard before any store/fleet read
#   or write operation. The guard must act on the returned error, using the
#   canonical `if err := requireOperator(ctx); err != nil { return nil, err }`
#   shape or an equivalent top-level assignment followed by an err != nil
#   return block before sensitive work.
# Zero points: any endpoint is missing, routed to the wrong handler, lacks
#   RequireAuth(), lacks a guarded requireOperator(ctx), has only spoofed
#   requireOperator(ctx) markers in comments/strings, ignores the returned
#   error, or performs sensitive work before the operator-role guard.
#
# No partial credit.
#
# Source: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.14
# Issue: equaltoai/lesser-host#440; hardened for LH-23 / #587
#
# Test hooks:
#   SEC12_SOURCE_ROOT=/path/to/source-root analyzes that source tree instead of
#   the repository root. The source root must contain internal/controlplane/*.
#   SEC12_SKIP_FIXTURES=1 skips committed negative-fixture regression checks.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"

python3 - <<'PY'
from pathlib import Path
import os
import re
import sys

REPO_ROOT = Path.cwd()
SOURCE_ROOT = Path(os.environ.get("SEC12_SOURCE_ROOT", ".")).resolve()
SKIP_FIXTURES = os.environ.get("SEC12_SKIP_FIXTURES", "") == "1"
FIXTURE_DIR = REPO_ROOT / "gov-infra/verifiers/sec/fixtures/operator-drift-auth-gate"

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

GUARD_IF_RE = re.compile(
    r"^\s*if\s+([A-Za-z_]\w*)\s*:=\s*requireOperator\s*\(\s*ctx\s*\)\s*;\s*\1\s*!=\s*nil\s*\{"
)
ASSIGN_RE = re.compile(r"^\s*([A-Za-z_]\w*)\s*:=\s*requireOperator\s*\(\s*ctx\s*\)\s*$")
ERR_IF_RE_TEMPLATE = r"^\s*if\s+{err}\s*!=\s*nil\s*\{{"
CALL_RE = re.compile(r"\brequireOperator\s*\(\s*ctx\s*\)")


def display_path(path: Path) -> str:
    try:
        return str(path.relative_to(REPO_ROOT))
    except ValueError:
        return str(path)


def scrub_go_source(text: str, *, strip_literals: bool) -> str:
    """Remove Go comments; optionally blank Go string/rune literals.

    The verifier must not accept requireOperator(ctx) markers after `//` or
    inside Go strings/raw strings/runes. This lexer preserves line numbers and
    enough column shape for source evidence while blanking non-executable text.
    """
    out: list[str] = []
    i = 0
    n = len(text)
    state = "code"

    def emit_char(ch: str) -> None:
        out.append(ch)

    def emit_blank(ch: str) -> None:
        out.append("\n" if ch == "\n" else " ")

    while i < n:
        ch = text[i]
        nxt = text[i + 1] if i + 1 < n else ""

        if state == "code":
            if ch == "/" and nxt == "/":
                emit_blank(ch)
                emit_blank(nxt)
                i += 2
                state = "line_comment"
                continue
            if ch == "/" and nxt == "*":
                emit_blank(ch)
                emit_blank(nxt)
                i += 2
                state = "block_comment"
                continue
            if ch == '"':
                (emit_blank if strip_literals else emit_char)(ch)
                i += 1
                state = "double_string"
                continue
            if ch == "`":
                (emit_blank if strip_literals else emit_char)(ch)
                i += 1
                state = "raw_string"
                continue
            if ch == "'":
                (emit_blank if strip_literals else emit_char)(ch)
                i += 1
                state = "rune"
                continue
            emit_char(ch)
            i += 1
            continue

        if state == "line_comment":
            if ch == "\n":
                emit_char(ch)
                state = "code"
            else:
                emit_blank(ch)
            i += 1
            continue

        if state == "block_comment":
            if ch == "*" and nxt == "/":
                emit_blank(ch)
                emit_blank(nxt)
                i += 2
                state = "code"
                continue
            emit_blank(ch)
            i += 1
            continue

        if state == "double_string":
            (emit_blank if strip_literals else emit_char)(ch)
            if ch == "\\" and i + 1 < n:
                (emit_blank if strip_literals else emit_char)(text[i + 1])
                i += 2
                continue
            if ch == '"':
                state = "code"
            i += 1
            continue

        if state == "raw_string":
            (emit_blank if strip_literals else emit_char)(ch)
            if ch == "`":
                state = "code"
            i += 1
            continue

        if state == "rune":
            (emit_blank if strip_literals else emit_char)(ch)
            if ch == "\\" and i + 1 < n:
                (emit_blank if strip_literals else emit_char)(text[i + 1])
                i += 2
                continue
            if ch == "'":
                state = "code"
            i += 1
            continue

        raise AssertionError(f"unexpected lexer state {state}")

    return "".join(out)


def executable_lines(text: str, *, strip_literals: bool):
    scrubbed = scrub_go_source(text, strip_literals=strip_literals)
    original_lines = text.splitlines()
    scrubbed_lines = scrubbed.splitlines()
    # Pad in case the file ends with trailing newlines represented differently.
    max_len = max(len(original_lines), len(scrubbed_lines))
    original_lines += [""] * (max_len - len(original_lines))
    scrubbed_lines += [""] * (max_len - len(scrubbed_lines))
    for idx, (code, original) in enumerate(zip(scrubbed_lines, original_lines), start=1):
        if code.strip() == "":
            continue
        yield idx, code, original


def brace_delta(code: str) -> int:
    return code.count("{") - code.count("}")


def find_function_body(source_root: Path, path: Path, handler: str, failures: list[str]):
    abs_path = source_root / path
    if not abs_path.is_file():
        failures.append(f"handler file missing for {handler}: {display_path(abs_path)}")
        return None

    text = abs_path.read_text(encoding="utf-8")
    original_lines = text.splitlines()
    scrubbed_lines = scrub_go_source(text, strip_literals=True).splitlines()
    max_len = max(len(original_lines), len(scrubbed_lines))
    original_lines += [""] * (max_len - len(original_lines))
    scrubbed_lines += [""] * (max_len - len(scrubbed_lines))

    pattern = re.compile(rf"^\s*func\s+\([^)]*\)\s+{re.escape(handler)}\s*\(")
    start_index = None
    for idx, code in enumerate(scrubbed_lines):
        if pattern.search(code):
            start_index = idx
            break
    if start_index is None:
        failures.append(f"handler function not found: {handler} in {display_path(abs_path)}")
        return None

    records: list[dict[str, object]] = []
    depth = 0
    body_started = False
    start_line = start_index + 1
    end_line = len(scrubbed_lines)

    for idx in range(start_index, len(scrubbed_lines)):
        code = scrubbed_lines[idx]
        original = original_lines[idx]
        if not body_started:
            if "{" not in code:
                continue
            body_started = True
            depth += brace_delta(code)
            continue

        depth_before = depth
        records.append(
            {
                "line": idx + 1,
                "code": code,
                "original": original,
                "depth": depth_before,
                "delta": brace_delta(code),
            }
        )
        depth += brace_delta(code)
        if depth <= 0:
            end_line = idx + 1
            break

    if not body_started:
        failures.append(f"handler function has no body: {handler} in {display_path(abs_path)}")
        return None
    if depth != 0:
        failures.append(f"handler function body did not close: {handler} in {display_path(abs_path)}:{start_line}")
        return None

    return abs_path, records, start_line, end_line


def block_returns_error(records: list[dict[str, object]], guard_pos: int, err_name: str) -> bool:
    return_re = re.compile(rf"\breturn\s+nil\s*,\s*{re.escape(err_name)}\b")
    guard = records[guard_pos]
    guard_line = str(guard["code"])
    if return_re.search(guard_line):
        return True

    for later in records[guard_pos + 1 :]:
        depth = int(later["depth"])
        if depth <= int(guard["depth"]):
            break
        if return_re.search(str(later["code"])):
            return True
    return False


def find_later_err_guard(records: list[dict[str, object]], assign_pos: int, err_name: str, before_line: int):
    err_if_re = re.compile(ERR_IF_RE_TEMPLATE.format(err=re.escape(err_name)))
    assign_depth = int(records[assign_pos]["depth"])
    for pos in range(assign_pos + 1, len(records)):
        rec = records[pos]
        line_no = int(rec["line"])
        if line_no >= before_line:
            return None
        if int(rec["depth"]) != assign_depth:
            continue
        if err_if_re.search(str(rec["code"])) and block_returns_error(records, pos, err_name):
            return rec
    return None


def find_authorizing_guard(records: list[dict[str, object]], first_sensitive_line: int):
    invalid_call_before_sensitive = None

    for pos, rec in enumerate(records):
        line_no = int(rec["line"])
        if line_no >= first_sensitive_line:
            break
        code = str(rec["code"])
        if CALL_RE.search(code) and int(rec["depth"]) != 1:
            invalid_call_before_sensitive = invalid_call_before_sensitive or (
                line_no,
                "requireOperator(ctx) is not a top-level handler statement",
            )
            continue
        if int(rec["depth"]) != 1:
            continue

        guarded = GUARD_IF_RE.search(code)
        if guarded:
            err_name = guarded.group(1)
            if block_returns_error(records, pos, err_name):
                return (line_no, f"if {err_name} := requireOperator(ctx); {err_name} != nil {{ ... return nil, {err_name} }}"), None
            invalid_call_before_sensitive = invalid_call_before_sensitive or (
                line_no,
                "requireOperator(ctx) guard does not return the checked error",
            )
            continue

        assigned = ASSIGN_RE.search(code)
        if assigned:
            err_name = assigned.group(1)
            err_guard = find_later_err_guard(records, pos, err_name, first_sensitive_line)
            if err_guard is not None:
                return (line_no, f"{err_name} := requireOperator(ctx) with later {err_name} != nil return guard"), None
            invalid_call_before_sensitive = invalid_call_before_sensitive or (
                line_no,
                "requireOperator(ctx) assignment is not followed by err != nil return guard before sensitive work",
            )
            continue

        if CALL_RE.search(code):
            invalid_call_before_sensitive = invalid_call_before_sensitive or (
                line_no,
                "requireOperator(ctx) call is not in an accepted guarded error-handling form",
            )

    return None, invalid_call_before_sensitive


def analyze_source(source_root: Path, *, label: str) -> tuple[bool, list[str]]:
    messages: list[str] = []
    failures: list[str] = []
    routes_abs = source_root / ROUTES_FILE

    if not routes_abs.is_file():
        failures.append(f"routes file missing: {display_path(routes_abs)}")
        return False, failures

    route_text = routes_abs.read_text(encoding="utf-8")
    route_lines = list(executable_lines(route_text, strip_literals=False))

    for ep in ENDPOINTS:
        method = ep["method"]
        path = ep["path"]
        handler = ep["handler"]
        route_re = re.compile(
            rf'app\.{method}\(\s*"{re.escape(path)}"\s*,\s*s\.{handler}\s*,\s*apptheory\.RequireAuth\(\)\s*\)'
        )
        exact_matches = [(idx, original) for idx, code, original in route_lines if route_re.search(code)]
        if exact_matches:
            idx, _line = exact_matches[0]
            messages.append(f"PASS: {method.upper()} {path} line {idx}: expected handler {handler} + RequireAuth()")
        else:
            nearby = [
                f"line {idx}: {original.strip()}"
                for idx, code, original in route_lines
                if f'"{path}"' in code
            ]
            detail = "; ".join(nearby) if nearby else "route not found"
            failures.append(
                f"{method.upper()} {path}: expected s.{handler} with apptheory.RequireAuth(); found {detail}"
            )

        body = find_function_body(source_root, ep["file"], handler, failures)
        if body is None:
            continue
        handler_file, records, start, end = body

        sensitive_hits: list[tuple[int, str, str]] = []
        for rec in records:
            code = str(rec["code"])
            if code.strip() == "":
                continue
            for token in ep["sensitive"]:
                if token in code:
                    sensitive_hits.append((int(rec["line"]), token, str(rec["original"]).strip()))
                    break
        if not sensitive_hits:
            failures.append(f"{handler}: no sensitive operation tokens found to order-check")
            continue

        first_sensitive = min(sensitive_hits, key=lambda item: item[0])
        guard, invalid_call = find_authorizing_guard(records, first_sensitive[0])
        if guard is None:
            if invalid_call is not None:
                call_line, reason = invalid_call
                failures.append(
                    f"{handler}: {reason} at {display_path(handler_file)}:{call_line}; first sensitive operation "
                    f"{first_sensitive[1]} line {first_sensitive[0]} ({first_sensitive[2]})"
                )
            else:
                failures.append(
                    f"{handler}: guarded requireOperator(ctx) not found before first sensitive operation "
                    f"{first_sensitive[1]} line {first_sensitive[0]} ({first_sensitive[2]}) "
                    f"inside {display_path(handler_file)}:{start}-{end}"
                )
            continue

        guard_line, guard_shape = guard
        messages.append(
            f"PASS: {handler}: guarded requireOperator(ctx) line {guard_line} ({guard_shape}) precedes first sensitive operation "
            f"{first_sensitive[1]} line {first_sensitive[0]}"
        )

    if failures:
        return False, failures
    return True, messages


print("SEC-12: Operator drift/release/remediation auth gate")
print(f"Source root: {display_path(SOURCE_ROOT)}")
print(f"Routes file: {ROUTES_FILE}")
print("")

ok, output = analyze_source(SOURCE_ROOT, label="source")
for message in output:
    stream = sys.stdout if ok else sys.stderr
    print(("PASS" if ok else "FAIL") + f": source: {message}" if not message.startswith(("PASS:", "FAIL:")) else message, file=stream)

if not ok:
    print("FAIL: one or more operator endpoints lack auth/role protection", file=sys.stderr)
    sys.exit(1)

if not SKIP_FIXTURES and SOURCE_ROOT == REPO_ROOT and FIXTURE_DIR.is_dir():
    print("")
    print("SEC-12 negative fixture regression checks")
    expected = [
        ("fail-string-literal-marker", "guarded requireOperator(ctx) not found"),
        ("fail-inline-comment-marker", "guarded requireOperator(ctx) not found"),
        ("fail-ignored-error-call", "not in an accepted guarded error-handling form"),
    ]
    for name, expected_fragment in expected:
        fixture_root = FIXTURE_DIR / name
        fixture_ok, fixture_output = analyze_source(fixture_root, label=name)
        joined = "\n".join(fixture_output)
        if fixture_ok:
            print(f"FAIL: negative fixture {name} unexpectedly passed", file=sys.stderr)
            for message in fixture_output:
                print(message, file=sys.stderr)
            sys.exit(1)
        if expected_fragment not in joined:
            print(
                f"FAIL: negative fixture {name} failed, but did not emit expected evidence fragment: {expected_fragment}",
                file=sys.stderr,
            )
            for message in fixture_output:
                print(message, file=sys.stderr)
            sys.exit(1)
        print(f"PASS: negative fixture {name} failed closed ({expected_fragment})")

print("")
print("PASS: all three operator endpoints protected by exact route RequireAuth() + guarded pre-read requireOperator(ctx)")
PY
