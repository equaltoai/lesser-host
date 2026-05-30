#!/usr/bin/env bash
# SEC-13: Portal cost redaction + multi-tenant downstream-call ordering.
#
# Verifies that the portal instance-cost endpoint handler:
#   1. Enforces tenant-scoped ownership via requireInstanceAccess before any
#      instance-key secret resolution or managed Lesser metrics HTTP call.
#   2. Does not use the deprecated host-local ListCostTelemetryByInstance path.
#   3. Does not expose PII, cross-tenant fields, tenant content, raw instance
#      keys, account IDs, table keys (PK/SK), TTL, or raw EntriesJSON storage
#      payloads in the customer-facing response DTO, transitive embedded DTOs,
#      tests, or error messages.
#
# Project 42 M0.4 correction: the accepted data source is the managed Lesser
# instance metrics endpoint, reached server-side with an instance API key. The
# SEC-13 proof must therefore gate the secret read and HTTP call, not the old
# host-local cost telemetry store read.
#
# LH-25 hardening: scope the ownership ordering check to the
# handlePortalGetInstanceCost body, scan transitive response DTO structs such as
# costtelemetry.ReconciledCostEntry/ServiceAttribution, and strip Go comments
# before treating test source text as proof.
#
# Source: Issue equaltoai/lesser-host#457, Project 42 M0.4 (#529), and
#         issue equaltoai/lesser-host#588.
#
# Test hooks:
#   SEC13_SOURCE_ROOT=/path/to/source-root analyzes that source tree instead of
#   the repository root. The source root must contain internal/controlplane/*
#   and internal/costtelemetry/*.
#   SEC13_SKIP_FIXTURES=1 skips committed negative-fixture regression checks.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "${REPO_ROOT}"
export SEC13_REPO_ROOT="${REPO_ROOT}"

python3 - <<'PY'
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import os
import re
import sys

REPO_ROOT = Path(os.environ["SEC13_REPO_ROOT"]).resolve()
SOURCE_ROOT = Path(os.environ.get("SEC13_SOURCE_ROOT", ".")).resolve()
SKIP_FIXTURES = os.environ.get("SEC13_SKIP_FIXTURES", "") == "1"
FIXTURE_DIR = REPO_ROOT / "gov-infra/verifiers/sec/fixtures/cost-telemetry-redaction"

HANDLER_FILE = Path("internal/controlplane/handlers_portal_cost.go")
TEST_FILE = Path("internal/controlplane/handlers_portal_cost_internal_test.go")
COSTTELEMETRY_DIR = Path("internal/costtelemetry")

FORBIDDEN_JSON_TAGS = {
    "account_id",
    "pk",
    "PK",
    "sk",
    "SK",
    "ttl",
    "entries_json",
    "EntriesJSON",
    "instance_key",
    "raw_key",
}
FORBIDDEN_RESPONSE_TOKENS = [
    '"account_id"',
    '"pk"',
    '"PK"',
    '"sk"',
    '"SK"',
    '"ttl"',
    '"entries_json"',
    '"EntriesJSON"',
    '"instance_key"',
    '"raw_key"',
]
PRIMITIVE_TYPES = {
    "string",
    "bool",
    "byte",
    "rune",
    "int",
    "int8",
    "int16",
    "int32",
    "int64",
    "uint",
    "uint8",
    "uint16",
    "uint32",
    "uint64",
    "uintptr",
    "float32",
    "float64",
    "complex64",
    "complex128",
    "any",
    "interface{}",
    "error",
}
ALLOWED_EXTERNAL_TYPES = {("time", "Time")}


@dataclass
class StructDef:
    package: str
    name: str
    path: Path
    start_line: int
    end_line: int
    body: list[tuple[int, str]]


def display_path(path: Path) -> str:
    try:
        return str(path.resolve().relative_to(REPO_ROOT))
    except ValueError:
        return str(path)


def scrub_go_source(text: str, *, strip_literals: bool) -> str:
    """Remove Go comments; optionally blank string/rune/raw-string literals.

    Line count is preserved so evidence line numbers still point at the source.
    When strip_literals is true, markers in Go strings cannot satisfy source
    ordering checks. When false, struct tags and test string assertions remain
    visible while comments are still removed.
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


def brace_delta(code: str) -> int:
    return code.count("{") - code.count("}")


def read_required_file(source_root: Path, path: Path, failures: list[str]) -> str | None:
    abs_path = source_root / path
    if not abs_path.is_file():
        failures.append(f"required file missing: {display_path(abs_path)}")
        return None
    return abs_path.read_text(encoding="utf-8")


def extract_function_body(
    source_root: Path,
    path: Path,
    receiver: str,
    function_name: str,
    failures: list[str],
) -> tuple[Path, list[dict[str, object]], int, int] | None:
    abs_path = source_root / path
    if not abs_path.is_file():
        failures.append(f"handler file missing: {display_path(abs_path)}")
        return None

    text = abs_path.read_text(encoding="utf-8")
    original_lines = text.splitlines()
    scrubbed_lines = scrub_go_source(text, strip_literals=True).splitlines()
    max_len = max(len(original_lines), len(scrubbed_lines))
    original_lines += [""] * (max_len - len(original_lines))
    scrubbed_lines += [""] * (max_len - len(scrubbed_lines))

    pattern = re.compile(
        rf"^\s*func\s+\(\s*{re.escape(receiver)}\s+\*Server\s*\)\s+{re.escape(function_name)}\s*\("
    )
    start_index = None
    for idx, code in enumerate(scrubbed_lines):
        if pattern.search(code):
            start_index = idx
            break
    if start_index is None:
        failures.append(f"handler function not found: {function_name} in {display_path(abs_path)}")
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
                "body_line": len(records) + 1,
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
        failures.append(f"handler function has no body: {function_name} in {display_path(abs_path)}")
        return None
    if depth != 0:
        failures.append(f"handler function body did not close: {function_name} in {display_path(abs_path)}:{start_line}")
        return None

    return abs_path, records, start_line, end_line


def find_call(records: list[dict[str, object]], symbol: str) -> list[dict[str, object]]:
    call_re = re.compile(rf"(^|[^A-Za-z0-9_]){re.escape(symbol)}\s*\(")
    return [rec for rec in records if call_re.search(str(rec["code"]))]


def verify_handler_ordering(source_root: Path, failures: list[str], messages: list[str]) -> None:
    body = extract_function_body(source_root, HANDLER_FILE, "s", "handlePortalGetInstanceCost", failures)
    if body is None:
        return

    handler_path, records, start, end = body
    messages.append(
        f"Scoped handler body: handlePortalGetInstanceCost at {display_path(handler_path)}:{start}-{end} "
        f"({len(records)} body lines)"
    )

    auth_calls = find_call(records, "requireInstanceAccess")
    if not auth_calls:
        failures.append("requireInstanceAccess call not found inside handlePortalGetInstanceCost body")
        return
    auth = auth_calls[0]
    messages.append(
        f"Ownership check: requireInstanceAccess at handler-body line {auth['body_line']} "
        f"(file line {auth['line']})"
    )

    sensitive_hits: list[tuple[dict[str, object], str]] = []
    for symbol in ("resolvePortalCostInstanceKey", "fetchManagedInstanceMetrics"):
        calls = find_call(records, symbol)
        if not calls:
            failures.append(f"{symbol} call not found inside handlePortalGetInstanceCost body")
            continue
        first = calls[0]
        sensitive_hits.append((first, symbol))
        messages.append(
            f"Downstream call: {symbol} at handler-body line {first['body_line']} (file line {first['line']})"
        )

    if not sensitive_hits:
        return

    first_sensitive, first_symbol = min(sensitive_hits, key=lambda item: int(item[0]["body_line"]))
    if int(first_sensitive["body_line"]) <= int(auth["body_line"]):
        failures.append(
            f"{first_symbol} (handler-body line {first_sensitive['body_line']}, file line {first_sensitive['line']}) "
            f"precedes ownership check (handler-body line {auth['body_line']}, file line {auth['line']}) "
            "inside handlePortalGetInstanceCost"
        )
        return

    messages.append(
        f"PASS: ownership check precedes first secret/HTTP operation {first_symbol} "
        f"(handler-body line {first_sensitive['body_line']})"
    )


def parse_package(text: str, path: Path, failures: list[str]) -> str | None:
    match = re.search(r"(?m)^\s*package\s+(\w+)\s*$", text)
    if not match:
        failures.append(f"package declaration missing: {display_path(path)}")
        return None
    return match.group(1)


def parse_import_aliases(text: str) -> dict[str, str]:
    aliases: dict[str, str] = {}
    for match in re.finditer(r'(?m)^\s*(?:(\w+)\s+)?"([^"]+)"', text):
        explicit, import_path = match.groups()
        package = import_path.rsplit("/", 1)[-1]
        alias = explicit or package
        aliases[alias] = package
    return aliases


def parse_structs_from_file(path: Path, text: str, failures: list[str]) -> dict[tuple[str, str], StructDef]:
    package = parse_package(text, path, failures)
    if package is None:
        return {}

    lines = text.splitlines()
    code_lines = scrub_go_source(text, strip_literals=False).splitlines()
    max_len = max(len(lines), len(code_lines))
    lines += [""] * (max_len - len(lines))
    code_lines += [""] * (max_len - len(code_lines))

    structs: dict[tuple[str, str], StructDef] = {}
    type_re = re.compile(r"^\s*type\s+(\w+)\s+struct\s*\{")
    idx = 0
    while idx < len(code_lines):
        code = code_lines[idx]
        type_match = type_re.search(code)
        if not type_match:
            idx += 1
            continue

        name = type_match.group(1)
        start_line = idx + 1
        depth = brace_delta(code)
        body: list[tuple[int, str]] = []
        idx += 1
        while idx < len(code_lines):
            body_code = code_lines[idx]
            depth += brace_delta(body_code)
            if depth <= 0:
                end_line = idx + 1
                structs[(package, name)] = StructDef(
                    package=package,
                    name=name,
                    path=path,
                    start_line=start_line,
                    end_line=end_line,
                    body=body,
                )
                break
            body.append((idx + 1, body_code))
            idx += 1
        else:
            failures.append(f"struct {name} in {display_path(path)} does not close")
            return structs
        idx += 1

    return structs


def type_files(source_root: Path, failures: list[str]) -> list[Path]:
    files = [source_root / HANDLER_FILE]
    cost_dir = source_root / COSTTELEMETRY_DIR
    if not cost_dir.is_dir():
        failures.append(f"cost telemetry source dir missing: {display_path(cost_dir)}")
        return files
    files.extend(sorted(cost_dir.glob("*.go")))
    return files


def clean_field_type(raw_type: str) -> str:
    t = raw_type.strip()
    # Remove common type wrappers until a named element type remains.
    changed = True
    while changed:
        changed = False
        for prefix in ("[]", "*", "..."):
            if t.startswith(prefix):
                t = t[len(prefix) :].strip()
                changed = True
        if t.startswith("map["):
            close = t.find("]")
            if close != -1:
                t = t[close + 1 :].strip()
                changed = True
        if t.startswith("chan "):
            t = t[5:].strip()
            changed = True
        if t.startswith("<-chan "):
            t = t[7:].strip()
            changed = True
    return t


def field_type_from_line(line: str) -> str | None:
    before_tag = line.split("`", 1)[0].strip()
    if not before_tag or before_tag.startswith("}"):
        return None
    # Drop trailing inline punctuation that cannot be part of a type name.
    before_tag = before_tag.rstrip(",")
    parts = before_tag.split()
    if not parts:
        return None
    if len(parts) == 1:
        return parts[0]
    return parts[-1]


def extract_json_tag(line: str) -> str | None:
    tag_match = re.search(r"`([^`]*)`", line)
    if not tag_match:
        return None
    json_match = re.search(r'json:"([^"]*)"', tag_match.group(1))
    if not json_match:
        return None
    return json_match.group(1).split(",", 1)[0]


def resolve_type(
    current_package: str,
    raw_type: str | None,
    import_aliases: dict[str, str],
) -> tuple[str, str] | None:
    if raw_type is None:
        return None
    t = clean_field_type(raw_type)
    if not t or t in PRIMITIVE_TYPES:
        return None
    if t.startswith("struct{") or t.startswith("func(") or t.startswith("interface{"):
        return None
    if "." in t:
        alias, name = t.split(".", 1)
        if (alias, name) in ALLOWED_EXTERNAL_TYPES:
            return None
        package = import_aliases.get(alias, alias)
        return package, name
    return current_package, t


def verify_transitive_dto_redaction(source_root: Path, failures: list[str], messages: list[str]) -> None:
    handler_text = read_required_file(source_root, HANDLER_FILE, failures)
    if handler_text is None:
        return
    handler_abs = source_root / HANDLER_FILE
    import_aliases = parse_import_aliases(handler_text)

    structs: dict[tuple[str, str], StructDef] = {}
    for path in type_files(source_root, failures):
        if not path.is_file():
            failures.append(f"type source file missing: {display_path(path)}")
            continue
        text = path.read_text(encoding="utf-8")
        structs.update(parse_structs_from_file(path, text, failures))

    visited: set[tuple[str, str]] = set()
    stack: list[tuple[str, str]] = []

    def scan_struct(package: str, name: str) -> None:
        key = (package, name)
        if key in visited:
            return
        struct_def = structs.get(key)
        if struct_def is None:
            failures.append(f"response DTO references unknown non-allowlisted type {package}.{name}")
            return
        visited.add(key)
        stack.append(key)
        messages.append(
            f"DTO scan: {package}.{name} at {display_path(struct_def.path)}:{struct_def.start_line}-{struct_def.end_line}"
        )

        for line_no, line in struct_def.body:
            tag_name = extract_json_tag(line)
            if tag_name and tag_name != "-" and tag_name in FORBIDDEN_JSON_TAGS:
                failures.append(
                    f"forbidden json tag {tag_name!r} in transitive response DTO {package}.{name} "
                    f"at {display_path(struct_def.path)}:{line_no}"
                )
            field_type = field_type_from_line(line)
            resolved = resolve_type(package, field_type, import_aliases)
            if resolved is None:
                continue
            if resolved in structs:
                scan_struct(*resolved)
                continue
            resolved_package, resolved_name = resolved
            if (resolved_package, resolved_name) in ALLOWED_EXTERNAL_TYPES:
                continue
            failures.append(
                f"response DTO {package}.{name} embeds unknown non-allowlisted type "
                f"{resolved_package}.{resolved_name} at {display_path(struct_def.path)}:{line_no}"
            )

        stack.pop()

    scan_struct("controlplane", "portalCostResponse")
    if not failures:
        messages.append(
            "PASS: transitive portalCostResponse DTO graph has no forbidden json tags "
            f"({', '.join(f'{pkg}.{name}' for pkg, name in sorted(visited))})"
        )

    # This catches direct raw storage-payload references in handler executable
    # source even if they are not part of a struct tag.
    non_comment_handler = scrub_go_source(handler_text, strip_literals=False)
    for token in ("EntriesJSON", "entries_json"):
        if token in non_comment_handler:
            failures.append(f"handler references raw {token} on a non-comment line: {display_path(handler_abs)}")


def verify_test_proof(source_root: Path, failures: list[str], messages: list[str]) -> None:
    test_text = read_required_file(source_root, TEST_FILE, failures)
    if test_text is None:
        return
    test_code = scrub_go_source(test_text, strip_literals=False)

    def has(pattern: str) -> bool:
        return pattern in test_code

    if has('Bearer "+testRawKey') and has("NotContains(t, string(resp.Body), testRawKey)"):
        messages.append("PASS: test proves bearer key is sent upstream and excluded from response")
    else:
        failures.append("missing bearer-auth send plus response redaction proof")

    for token in FORBIDDEN_RESPONSE_TOKENS:
        if token not in test_code:
            failures.append(f"test redaction assertion missing forbidden token {token}")
    if "forbidden := range" in test_code and "require.NotContains(t, string(resp.Body), forbidden)" in test_code:
        messages.append("PASS: test asserts forbidden response fields are absent")
    else:
        failures.append("test does not assert forbidden response fields are absent")

    if (
        "WrongOwnerForbiddenBeforeSecretOrHTTP" in test_code
        and "require.Zero(t, secretReads)" in test_code
        and "require.Zero(t, httpCalls)" in test_code
    ):
        messages.append("PASS: forbidden tenant proof skips secret resolution and HTTP call")
    else:
        failures.append("missing wrong-owner proof that secret resolution and HTTP are skipped")

    if "Upstream5xxDoesNotLeakKeyOrBody" in test_code and "KeyResolverFailureDoesNotLeak" in test_code:
        messages.append("PASS: tests cover upstream/key-resolution error redaction")
    else:
        failures.append("missing upstream/key-resolution redaction tests")

    marshal_requirements = [
        "TestPortalCostResponseJSONOmitsCostTelemetrySensitiveFields",
        "json.Marshal(body)",
        "costtelemetry.ReconciledCostEntry",
        "require.NotContains(t, payload, forbidden)",
    ]
    missing_marshal = [token for token in marshal_requirements if token not in test_code]
    if missing_marshal:
        failures.append("missing transitive DTO marshal redaction proof tokens: " + ", ".join(missing_marshal))
    else:
        messages.append("PASS: test marshals portalCostResponse and asserts transitive DTO redaction")


def verify_no_deprecated_store_read(source_root: Path, failures: list[str], messages: list[str]) -> None:
    handler_text = read_required_file(source_root, HANDLER_FILE, failures)
    if handler_text is None:
        return
    handler_code = scrub_go_source(handler_text, strip_literals=True)
    if "ListCostTelemetryByInstance" in handler_code:
        failures.append("handler still reads host-local cost telemetry store")
    else:
        messages.append("PASS: handler does not use deprecated ListCostTelemetryByInstance path")


def analyze_source(source_root: Path, *, label: str) -> tuple[bool, list[str]]:
    del label
    failures: list[str] = []
    messages: list[str] = []

    verify_handler_ordering(source_root, failures, messages)
    verify_no_deprecated_store_read(source_root, failures, messages)
    verify_transitive_dto_redaction(source_root, failures, messages)
    verify_test_proof(source_root, failures, messages)

    if failures:
        return False, failures
    return True, messages


def print_result(ok: bool, output: list[str]) -> None:
    stream = sys.stdout if ok else sys.stderr
    for message in output:
        if message.startswith(("PASS:", "FAIL:")):
            print(message, file=stream)
        else:
            print(("PASS: " if ok else "FAIL: ") + message, file=stream)


print("SEC-13: Portal cost redaction + multi-tenant downstream-call ordering")
print(f"Source root: {display_path(SOURCE_ROOT)}")
print(f"Handler file: {HANDLER_FILE}")
print(f"Test file:    {TEST_FILE}")
print("")

ok, output = analyze_source(SOURCE_ROOT, label="source")
print_result(ok, output)

if not ok:
    print("FAIL: SEC-13 portal cost redaction check failed", file=sys.stderr)
    sys.exit(1)

if not SKIP_FIXTURES and SOURCE_ROOT == REPO_ROOT and FIXTURE_DIR.is_dir():
    print("")
    print("SEC-13 negative fixture regression checks")
    expected = [
        ("fail-decoy-auth-preauth-fetch", "precedes ownership check"),
        ("fail-forbidden-reconciled-tag", "forbidden json tag 'account_id'"),
        ("fail-comment-only-test-proof", "missing bearer-auth send plus response redaction proof"),
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
                f"FAIL: negative fixture {name} failed, but did not emit expected evidence fragment: "
                f"{expected_fragment}",
                file=sys.stderr,
            )
            for message in fixture_output:
                print(message, file=sys.stderr)
            sys.exit(1)
        print(f"PASS: negative fixture {name} failed closed ({expected_fragment})")

print("")
print("PASS: SEC-13 portal cost redaction + downstream-call ordering verified")
PY
