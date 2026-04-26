#!/usr/bin/env bash
# GovTheory Rubric Verifier (Single Entrypoint)
# Generated from pack version: 4221f0e715b1
# Project: lesser-host (lesser-host)
#
# This script is the deterministic verifier entrypoint for gov.validate.
# It reads planning state from gov-infra/planning/, runs repo-specific check
# commands, writes evidence under gov-infra/evidence/, and emits a fixed JSON
# report at gov-infra/evidence/gov-rubric-report.json.
#
# Usage (from repo root; scripts may be non-executable by default):
#   bash gov-infra/verifiers/gov-verify-rubric.sh
#
# Exit codes:
#   0 - All rubric items PASS
#   1 - One or more rubric items FAIL or BLOCKED
#   2 - Script error (missing dependencies, invalid config, etc.)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
GOV_INFRA="${REPO_ROOT}/gov-infra"
PLANNING_DIR="${GOV_INFRA}/planning"
EVIDENCE_DIR="${GOV_INFRA}/evidence"
REPORT_PATH="${EVIDENCE_DIR}/gov-rubric-report.json"

# Always run checks from repo root so relative commands are stable.
cd "${REPO_ROOT}"

# Optional repo-local tools directory (to enforce pinned tool versions deterministically).
# Tools are installed here (never system-wide) and put first on PATH.
GOV_TOOLS_DIR="${GOV_INFRA}/.tools"
GOV_TOOLS_BIN="${GOV_TOOLS_DIR}/bin"
mkdir -p "${GOV_TOOLS_BIN}"
export PATH="${GOV_TOOLS_BIN}:${PATH}"

# Force the Go toolchain to match go.mod even when the host `go` is older.
# This avoids mixed-version builds (notably when running `go test -coverprofile`).
EXPECTED_GO_VERSION="$(awk '$1=="go"{print $2; exit}' "${REPO_ROOT}/go.mod" 2>/dev/null || true)"
if [[ -n "${EXPECTED_GO_VERSION}" ]]; then
  export GOTOOLCHAIN="go${EXPECTED_GO_VERSION}"
fi

# Tool pins (optional; populated by gov.init when possible).
# If these remain unset, checks that depend on them should be marked BLOCKED (never "use whatever is installed").
#
# NOTE: this repo currently does not pin these in CI; keep checks fail-closed until pins are set intentionally.
# M1 intent: pin golangci-lint to unblock deterministic lint/config verification.
PIN_GOLANGCI_LINT_VERSION="v2.10.1"
PIN_GOVULNCHECK_VERSION="v1.1.4"
PIN_SLITHER_VERSION="0.11.5"
PIN_SOLC_SELECT_VERSION="1.2.0"
PIN_SOLC_VERSION="0.8.24"

# Optional feature flags (opt-in pack features).
FEATURE_OSS_RELEASE="false"

# Ensure evidence directory exists
mkdir -p "${EVIDENCE_DIR}"

# Clean previous run outputs to prevent stale evidence from being misattributed.
# Only remove files this verifier owns (do not wipe arbitrary user evidence).
rm -f \
  "${REPORT_PATH}" \
  "${EVIDENCE_DIR}/"*-output.log \
  "${EVIDENCE_DIR}/DOC-5-parity.log"

# Initialize report structure
REPORT_SCHEMA_VERSION=1
REPORT_TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PASS_COUNT=0
FAIL_COUNT=0
BLOCKED_COUNT=0

# Results array (will be populated by run_check)
declare -a RESULTS=()

json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/\\r}"
  printf '%s' "$s"
}

record_result() {
  local id="$1"
  local category="$2"
  local status="$3"
  local message="$4"
  local evidence_path="$5"

  case "$status" in
    PASS) ((PASS_COUNT++)) || true ;;
    FAIL) ((FAIL_COUNT++)) || true ;;
    BLOCKED) ((BLOCKED_COUNT++)) || true ;;
    *) echo "Internal error: invalid status '${status}'" >&2; exit 2 ;;
  esac

  RESULTS+=(
    "{\"id\":\"$(json_escape "$id")\",\"category\":\"$(json_escape "$category")\",\"status\":\"$(json_escape "$status")\",\"message\":\"$(json_escape "$message")\",\"evidencePath\":\"$(json_escape "$evidence_path")\"}"
  )
}

is_unset_token() {
  # Treat as unset if empty, TODO placeholder, or a still-rendered template token.
  local v="$1"
  [[ -z "${v//[[:space:]]/}" ]] && return 0
  [[ "$v" == "TODO:"* ]] && return 0
  [[ "$v" == "{{"* ]] && return 0
  return 1
}

normalize_feature_flags() {
  if is_unset_token "$FEATURE_OSS_RELEASE"; then
    FEATURE_OSS_RELEASE="false"
  fi
  FEATURE_OSS_RELEASE="$(printf '%s' "$FEATURE_OSS_RELEASE" | tr '[:upper:]' '[:lower:]')"
  case "$FEATURE_OSS_RELEASE" in
    true|false) ;;
    *) FEATURE_OSS_RELEASE="false" ;;
  esac
}

ensure_golangci_lint_pinned() {
  # Ensure golangci-lint is available at a pinned version (prefer repo-local tools dir).
  local v="$PIN_GOLANGCI_LINT_VERSION"
  if is_unset_token "$v"; then
    echo "BLOCKED: golangci-lint version pin missing (set PIN_GOLANGCI_LINT_VERSION)" >&2
    return 2
  fi
  if [[ "$v" != v* ]]; then
    v="v${v}"
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "BLOCKED: go toolchain not available to install golangci-lint ${v}" >&2
    return 2
  fi

  local want="${v#v}"
  if command -v golangci-lint >/dev/null 2>&1; then
    if golangci-lint --version 2>/dev/null | grep -q "$want"; then
      return 0
    fi
  fi

  echo "Installing golangci-lint ${v} into ${GOV_TOOLS_BIN}..." >&2
  if ! GOBIN="${GOV_TOOLS_BIN}" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${v}"; then
    echo "BLOCKED: failed to install pinned golangci-lint ${v} (check network/toolchain)" >&2
    return 2
  fi

  if ! golangci-lint --version 2>/dev/null | grep -q "$want"; then
    echo "FAIL: installed golangci-lint does not report expected version ${v}" >&2
    golangci-lint --version 2>/dev/null || true
    return 1
  fi

  return 0
}

ensure_govulncheck_pinned() {
  local v="$PIN_GOVULNCHECK_VERSION"
  if is_unset_token "$v"; then
    echo "BLOCKED: govulncheck version pin missing (set PIN_GOVULNCHECK_VERSION)" >&2
    return 2
  fi
  if [[ "$v" != v* ]]; then
    v="v${v}"
  fi

  if ! command -v go >/dev/null 2>&1; then
    echo "BLOCKED: go toolchain not available to install govulncheck ${v}" >&2
    return 2
  fi

  if command -v govulncheck >/dev/null 2>&1; then
    if govulncheck -version 2>/dev/null | grep -q "govulncheck@${v}"; then
      return 0
    fi
  fi

  echo "Installing govulncheck ${v} into ${GOV_TOOLS_BIN}..." >&2
  if ! GOBIN="${GOV_TOOLS_BIN}" go install "golang.org/x/vuln/cmd/govulncheck@${v}"; then
    echo "BLOCKED: failed to install pinned govulncheck ${v} (check network/toolchain)" >&2
    return 2
  fi

  if ! govulncheck -version 2>/dev/null | grep -q "govulncheck@${v}"; then
    echo "FAIL: installed govulncheck does not report expected version ${v}" >&2
    govulncheck -version 2>/dev/null || true
    return 1
  fi

  return 0
}

allowlist_has_id() {
  local allowlist_path="$1"
  local id="$2"
  [[ -f "${allowlist_path}" ]] || return 1
  grep -Fqx -- "${id}" "${allowlist_path}"
}

sha256_12() {
  local s="$1"
  local hash=""
  if command -v sha256sum >/dev/null 2>&1; then
    hash="$(printf '%s' "${s}" | sha256sum | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    hash="$(printf '%s' "${s}" | shasum -a 256 | awk '{print $1}')"
  else
    echo "BLOCKED: sha256 tool missing (need sha256sum or shasum)" >&2
    return 2
  fi
  printf '%s' "${hash:0:12}"
  return 0
}

extract_go_mod_replaces() {
  local mod="${REPO_ROOT}/go.mod"
  [[ -f "${mod}" ]] || return 0

  awk '
    BEGIN { inblock=0 }
    $1 == "replace" && $2 == "(" { inblock=1; next }
    $1 == "replace" && $2 != "(" {
      $1=""; sub(/^[[:space:]]+/, ""); print; next
    }
    inblock && $1 == ")" { inblock=0; next }
    inblock { print; next }
  ' "${mod}"
}

scan_go_supply_chain() {
  # Scans Go module metadata for supply-chain risk signals.
  local allowlist_path="$1"

  if [[ ! -f "${REPO_ROOT}/go.mod" ]]; then
    echo "Go supply-chain scan: no go.mod detected; skipping."
    return 0
  fi

  local failures=0
  local allowlisted=0

  if [[ ! -f "${REPO_ROOT}/go.sum" ]]; then
    local id="GOV-SUPPLY:GO:MOD:rule=MISSING_GO_SUM"
    if allowlist_has_id "${allowlist_path}" "${id}"; then
      allowlisted=$((allowlisted + 1))
    else
      failures=$((failures + 1))
      echo "- ${id} file=go.sum"
    fi
  fi

  local known_malicious=(
    "github.com/boltdb-go/bolt"
    "github.com/gin-goinc"
    "github.com/go-chi/chi/v6"
  )

  local mod
  for mod in "${known_malicious[@]}"; do
    if grep -Fq -- "${mod}" "${REPO_ROOT}/go.mod" 2>/dev/null || ( [[ -f "${REPO_ROOT}/go.sum" ]] && grep -Fq -- "${mod}" "${REPO_ROOT}/go.sum" 2>/dev/null ); then
      local id="GOV-SUPPLY:GO:MOD:rule=KNOWN_MALICIOUS_MODULE:module=${mod}"
      if allowlist_has_id "${allowlist_path}" "${id}"; then
        allowlisted=$((allowlisted + 1))
      else
        failures=$((failures + 1))
        echo "- ${id}"
      fi
    fi
  done

  local line
  while IFS= read -r line; do
    [[ -z "${line//[[:space:]]/}" ]] && continue
    [[ "${line}" == "//"* ]] && continue
    [[ "${line}" == *"=>"* ]] || continue

    local left="${line%%=>*}"
    local right="${line#*=>}"
    left="$(printf '%s' "${left}" | xargs)"
    right="$(printf '%s' "${right}" | xargs)"
    [[ -z "${left}" || -z "${right}" ]] && continue

    local from_mod=""
    local from_ver=""
    local to_mod=""
    local to_ver=""
    from_mod="$(printf '%s' "${left}" | awk '{print $1}')"
    from_ver="$(printf '%s' "${left}" | awk '{print $2}')"
    to_mod="$(printf '%s' "${right}" | awk '{print $1}')"
    to_ver="$(printf '%s' "${right}" | awk '{print $2}')"
    [[ -z "${from_mod}" || -z "${to_mod}" ]] && continue

    # Local replace targets are common in multi-module repos; ignore.
    if [[ "${to_mod}" == ./* || "${to_mod}" == ../* || "${to_mod}" == /* ]]; then
      continue
    fi

    local from="${from_mod}@${from_ver:-_}"
    local to="${to_mod}@${to_ver:-_}"
    local id="GOV-SUPPLY:GO:REPLACE:rule=REMOTE_REPLACE:from=${from}:to=${to}"
    if allowlist_has_id "${allowlist_path}" "${id}"; then
      allowlisted=$((allowlisted + 1))
    else
      failures=$((failures + 1))
      echo "- ${id} detail=$(printf '%s' "${line}" | tr -d '\r')"
    fi
  done < <(extract_go_mod_replaces)

  echo "Supply-chain scan (Go): findings=${failures} allowlisted=${allowlisted}"

  if [[ "${failures}" -ne 0 ]]; then
    return 1
  fi
  return 0
}

scan_python_supply_chain() {
  # Scans Python dependency/config files for supply-chain risk signals.
  local allowlist_path="$1"

  local -a files=()
  while IFS= read -r f; do
    files+=("$f")
  done < <(
    find "${REPO_ROOT}" -maxdepth 6 -type f \( \
      -name 'requirements*.txt' -o \
      -name 'constraints*.txt' -o \
      -name 'Pipfile' -o \
      -name 'Pipfile.lock' -o \
      -name 'poetry.lock' -o \
      -name 'pdm.lock' -o \
      -name 'uv.lock' -o \
      -name 'pyproject.toml' \
    \) \
    -not -path '*/node_modules/*' \
    -not -path '*/.git/*' \
    -not -path '*/.venv/*' \
    -not -path '*/venv/*' \
    -not -path '*/__pycache__/*' \
    2>/dev/null | LC_ALL=C sort
  )

  if [[ "${#files[@]}" -eq 0 ]]; then
    echo "Python supply-chain scan: no Python dependency files detected; skipping."
    return 0
  fi

  local known_malicious=(
    "python3-dateutil"
    "jeilyfish"
    "python-binance"
    "request"
    "urllib"
    "djanga"
    "coloursama"
    "larpexodus"
    "graphalgo"
    "acloud-client"
    "tcloud-python-test"
  )

  local failures=0
  local allowlisted=0
  local file_count=0

  local f
  for f in "${files[@]}"; do
    file_count=$((file_count + 1))
    local rel="${f#${REPO_ROOT}/}"

    local line
    while IFS= read -r line || [[ -n "${line}" ]]; do
      local raw="${line}"
      raw="${raw//$'\r'/}"
      local trimmed
      trimmed="$(printf '%s' "${raw}" | sed -E 's/[[:space:]]+/ /g; s/^ +//; s/ +$//')"
      [[ -z "${trimmed}" ]] && continue

      local lower
      lower="$(printf '%s' "${trimmed}" | tr '[:upper:]' '[:lower:]')"

      local rule=""

      # Known malicious packages (typosquats / compromised).
      local pkg
      for pkg in "${known_malicious[@]}"; do
        if [[ "${lower}" == *"${pkg}"* ]]; then
          rule="KNOWN_MALICIOUS_PACKAGE"
          break
        fi
      done

      # Dependency sources that bypass standard indexes (VCS / direct URL).
      if [[ -z "${rule}" ]]; then
        if [[ "${lower}" == *"git+https://"* || "${lower}" == *"git+http://"* || "${lower}" == *"git+ssh://"* || "${lower}" == *"hg+http"* || "${lower}" == *"svn+http"* || "${lower}" == *"bzr+http"* ]]; then
          rule="VCS_OR_URL_DEP"
        elif [[ "${lower}" == *" @ https://"* || "${lower}" == *" @ http://"* || "${lower}" == *" @ file://"* || "${lower}" == *" @ ssh://"* ]]; then
          rule="VCS_OR_URL_DEP"
        elif [[ "${lower}" == *"git = \""* && ( "${lower}" == *"http://"* || "${lower}" == *"https://"* || "${lower}" == *"ssh://"* ) ]]; then
          rule="VCS_OR_URL_DEP"
        elif [[ "${lower}" == *"\"git\":"* && ( "${lower}" == *"http://"* || "${lower}" == *"https://"* || "${lower}" == *"ssh://"* ) ]]; then
          rule="VCS_OR_URL_DEP"
        fi
      fi

      # Custom indexes and trusted hosts (higher supply-chain risk).
      if [[ -z "${rule}" ]]; then
        if [[ "${lower}" == *"--index-url"* || "${lower}" == *"--extra-index-url"* || "${lower}" == *"--find-links"* ]] || [[ "${lower}" =~ (^|[[:space:]])-f([[:space:]]|$) ]]; then
          rule="CUSTOM_INDEX"
        elif [[ "${lower}" == *"--trusted-host"* ]]; then
          rule="TRUSTED_HOST"
        elif [[ "${lower}" == "-e "* || "${lower}" == "--editable "* ]]; then
          rule="EDITABLE_INSTALL"
        fi
      fi

      [[ -z "${rule}" ]] && continue

      local h=""
      h="$(sha256_12 "${rel}|${rule}|${trimmed}")" || return $?
      local id="GOV-SUPPLY:PYTHON:LINE:file=${rel}:rule=${rule}:sha256=${h}"

      if allowlist_has_id "${allowlist_path}" "${id}"; then
        allowlisted=$((allowlisted + 1))
      else
        failures=$((failures + 1))
        echo "- ${id} detail=${trimmed:0:200}"
      fi
    done < "${f}"
  done

  echo "Supply-chain scan (Python): files=${file_count} findings=${failures} allowlisted=${allowlisted}"

  if [[ "${failures}" -ne 0 ]]; then
    return 1
  fi
  return 0
}

check_supply_chain_actions_pinned() {
  # Enforces integrity pinning for GitHub Actions (reject floating tags like @v4).
  local wf_dir="${REPO_ROOT}/.github/workflows"
  if [[ ! -d "${wf_dir}" ]]; then
    echo "GitHub Actions pin check: no workflows detected; skipping."
    return 0
  fi

  local matches=""
  matches="$(grep -R --include='*.yml' --include='*.yaml' -nE '^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]].*@v[0-9]+' "${wf_dir}" 2>/dev/null || true)"
  if [[ -n "${matches}" ]]; then
    echo "FAIL: unpinned GitHub Action detected (uses @vN; pin by commit SHA)"
    echo "${matches}"
    return 1
  fi

  echo "GitHub Actions pin check: PASS (no uses @vN detected)"
  return 0
}

detect_node_package_manager_in_dir() {
  # Determines which package manager a Node project uses based on lockfiles in a directory.
  # Prints: npm|pnpm|yarn
  local dir="$1"

  local has_npm="false"
  local has_pnpm="false"
  local has_yarn="false"

  if [[ -f "${dir}/package-lock.json" ]] || [[ -f "${dir}/npm-shrinkwrap.json" ]]; then
    has_npm="true"
  fi
  if [[ -f "${dir}/pnpm-lock.yaml" ]]; then
    has_pnpm="true"
  fi
  if [[ -f "${dir}/yarn.lock" ]]; then
    has_yarn="true"
  fi

  local count=0
  [[ "$has_npm" == "true" ]] && count=$((count + 1))
  [[ "$has_pnpm" == "true" ]] && count=$((count + 1))
  [[ "$has_yarn" == "true" ]] && count=$((count + 1))

  if [[ "$count" -eq 0 ]]; then
    return 1
  fi
  if [[ "$count" -ne 1 ]]; then
    echo "Ambiguous Node lockfiles detected in ${dir} (expected exactly one)." >&2
    return 2
  fi

  if [[ "$has_pnpm" == "true" ]]; then
    printf '%s' "pnpm"
    return 0
  fi
  if [[ "$has_yarn" == "true" ]]; then
    printf '%s' "yarn"
    return 0
  fi
  printf '%s' "npm"
  return 0
}

install_node_deps_ignore_scripts_in_dir() {
  # Installs Node deps deterministically with lifecycle scripts disabled.
  local pm="$1"
  local dir="$2"

  if ! command -v node >/dev/null 2>&1; then
    echo "BLOCKED: node is required for supply-chain scanning (${dir}/package.json detected)" >&2
    return 2
  fi

  case "$pm" in
    npm)
      if ! command -v npm >/dev/null 2>&1; then
        echo "BLOCKED: npm is required for supply-chain scanning (${dir}/package-lock.json detected)" >&2
        return 2
      fi
      echo "Installing Node dependencies via npm (scripts disabled): ${dir}"
      (cd "${dir}" && npm ci --ignore-scripts --no-audit --no-fund)
      return 0
      ;;
    *)
      echo "BLOCKED: unsupported package manager '${pm}' for ${dir} (only npm currently implemented)" >&2
      return 2
      ;;
  esac
}

scan_node_modules_supply_chain_in_dir() {
  local dir="$1"
  local allowlist_path="$2"

  local nm="${dir}/node_modules"
  if [[ ! -d "${nm}" ]]; then
    echo "BLOCKED: ${dir}: node_modules/ not found (install step did not produce it)" >&2
    return 2
  fi
  if ! command -v node >/dev/null 2>&1; then
    echo "BLOCKED: node is required to scan node_modules package.json files" >&2
    return 2
  fi

  local pkg_json_list
  pkg_json_list="$(mktemp)"
  find "${nm}" -type f -name package.json 2>/dev/null | LC_ALL=C sort > "${pkg_json_list}"

  if [[ ! -s "${pkg_json_list}" ]]; then
    echo "${dir}: No dependency package.json files found under node_modules/; nothing to scan."
    rm -f "${pkg_json_list}"
    return 0
  fi

  set +e
  ALLOWLIST_PATH="${allowlist_path}" node - "${pkg_json_list}" <<'__GOV_NODE_SUPPLY_SCAN__'
const fs = require('fs');
const path = require('path');

const pkgListPath = process.argv[2];
const allowlistPath = process.env.ALLOWLIST_PATH || '';

function readAllowlist(p) {
  if (!p) return new Set();
  try {
    if (!fs.existsSync(p)) return new Set();
    const lines = fs.readFileSync(p, 'utf8').split(/\r?\n/);
    const ids = new Set();
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;
      ids.add(trimmed);
    }
    return ids;
  } catch (e) {
    console.error(`BLOCKED: failed to read allowlist at ${p}: ${e.message}`);
    process.exit(2);
  }
}

function sanitizeValue(v) {
  return String(v ?? '').split('\n').join(' ').split('\r').join(' ').trim();
}

function makeId(parts) {
  // ID format (plain text, exact match):
  // GOV-SUPPLY:NODE:<SCRIPT|FILE>:pkg=<name>:ver=<version>:hook=<hook>:(file=<relpath>:)(rule=<ruleId>|ioc=<ioc>)
  const segs = [
    'GOV-SUPPLY',
    'NODE',
    parts.kind,
    `pkg=${sanitizeValue(parts.pkg)}`,
    `ver=${sanitizeValue(parts.ver)}`,
    `hook=${sanitizeValue(parts.hook)}`
  ];
  if (parts.kind === 'FILE') {
    segs.push(`file=${sanitizeValue(parts.file)}`);
  }
  if (parts.ioc) {
    segs.push(`ioc=${sanitizeValue(parts.ioc)}`);
  } else {
    segs.push(`rule=${sanitizeValue(parts.rule)}`);
  }
  return segs.join(':');
}

const allowlist = readAllowlist(allowlistPath);
const hooks = ['preinstall', 'install', 'postinstall', 'prepack', 'prepare', 'prepublishOnly'];

const patterns = [
  { id: 'CURL_PIPE_SHELL', re: /curl\s+[^|]*\|\s*(sh|bash)\b/i },
  { id: 'WGET_PIPE_SHELL', re: /wget\s+[^|]*\|\s*(sh|bash)\b/i },
  { id: 'EVAL', re: /\beval\s*\(/i },
  { id: 'FUNCTION_CONSTRUCTOR', re: /\bFunction\s*\(/i },
  { id: 'BASE64_DECODE', re: /\b(base64\s+(-d|--decode)|base64\.b64decode|Buffer\.from\([^)]*base64|atob\s*\(|b64decode)\b/i },
  { id: 'CRED_FILE_ACCESS', re: /(\.npmrc|\.netrc|\.pypirc|pip\.conf)\b/i },
  { id: 'TOKEN_ENV_ACCESS', re: /\b(NPM_TOKEN|GITHUB_TOKEN|AWS_SECRET|AWS_ACCESS_KEY_ID|GOOGLE_APPLICATION|PYPI_TOKEN|TWINE_PASSWORD)\b/i },
  { id: 'WEBHOOK_EXFIL', re: /\b(webhook\.site|pipedream\.net|requestbin|pastebin\.com|transfer\.sh)\b/i }
];

const iocs = [
  'shai-hulud',
  'shai_hulud',
  'Shai-Hulud Repository',
  'Shai-Hulud Migration',
  'webhook.site',
  'bb8ca5f6-4175-45d2-b042-fc9ebb8170b7'
].map(s => s.toLowerCase());

function listPackageJsonPaths(listFile) {
  try {
    return fs.readFileSync(listFile, 'utf8').split(/\r?\n/).map(s => s.trim()).filter(Boolean);
  } catch (e) {
    console.error(`BLOCKED: failed to read package.json path list: ${e.message}`);
    process.exit(2);
  }
}

function safeReadJson(p) {
  try {
    const txt = fs.readFileSync(p, 'utf8');
    return JSON.parse(txt);
  } catch {
    return null;
  }
}

function normalizeScript(s) {
  return String(s ?? '').split('\n').join(' ').split('\r').join(' ').trim();
}

function scanTextForIocs(text, add) {
  const lower = text.toLowerCase();
  for (const ioc of iocs) {
    if (lower.includes(ioc)) {
      add(ioc);
    }
  }
}

const findings = [];
let allowlisted = 0;
let scannedPackages = 0;

const pkgJsonPaths = listPackageJsonPaths(pkgListPath);
for (const pkgJsonPath of pkgJsonPaths) {
  const pkg = safeReadJson(pkgJsonPath);
  if (!pkg) continue;
  const pkgName = pkg.name || path.basename(path.dirname(pkgJsonPath));
  const pkgVer = pkg.version || '';
  const scripts = pkg.scripts || {};

  scannedPackages++;

  for (const hook of hooks) {
    const rawScript = scripts[hook];
    if (typeof rawScript !== 'string' || rawScript.trim() === '') continue;
    const script = normalizeScript(rawScript);

    for (const { id, re } of patterns) {
      if (re.test(script)) {
        const fid = makeId({ kind: 'SCRIPT', pkg: pkgName, ver: pkgVer, hook, rule: id });
        if (allowlist.has(fid)) {
          allowlisted++;
        } else {
          findings.push({ id: fid, pkg: pkgName, ver: pkgVer, hook, where: pkgJsonPath, detail: script, rule: id });
        }
      }
    }

    const iocHits = new Set();
    scanTextForIocs(script, (ioc) => iocHits.add(ioc));
    for (const ioc of Array.from(iocHits).sort()) {
      const fid = makeId({ kind: 'SCRIPT', pkg: pkgName, ver: pkgVer, hook, ioc });
      if (allowlist.has(fid)) {
        allowlisted++;
      } else {
        findings.push({ id: fid, pkg: pkgName, ver: pkgVer, hook, where: pkgJsonPath, detail: script, ioc });
      }
    }

    // If a lifecycle hook runs a JS file via `node`, scan that file too.
    const pkgDir = path.dirname(pkgJsonPath);
    const nodeFileRe = /(^|[;&|]\s*)node\s+([^\s;&|]+\.js)\b/gi;
    const fileCandidates = new Set();
    let m;
    while ((m = nodeFileRe.exec(script)) !== null) {
      let p = m[2] || '';
      p = p.replace(/^['"]/, '').replace(/['"]$/, '');
      if (!p) continue;
      const resolved = path.isAbsolute(p) ? p : path.resolve(pkgDir, p);
      fileCandidates.add(resolved);
    }

    for (const absPath of Array.from(fileCandidates).sort()) {
      try {
        if (!fs.existsSync(absPath)) continue;
        const st = fs.statSync(absPath);
        const rel = path.relative(pkgDir, absPath);

        if (st.size > 1000000) {
          const fid = makeId({ kind: 'FILE', pkg: pkgName, ver: pkgVer, hook, file: rel, rule: 'LARGE_LIFECYCLE_SCRIPT' });
          if (allowlist.has(fid)) {
            allowlisted++;
          } else {
            findings.push({ id: fid, pkg: pkgName, ver: pkgVer, hook, where: absPath, detail: `${rel} (${st.size} bytes)`, rule: 'LARGE_LIFECYCLE_SCRIPT' });
          }
        }

        // Scan content (bounded) for IOCs and high-signal patterns.
        if (st.size <= 5000000) {
          const content = fs.readFileSync(absPath, 'utf8');
          const contentNorm = normalizeScript(content);

          const fileIocs = new Set();
          scanTextForIocs(contentNorm, (ioc) => fileIocs.add(ioc));
          for (const ioc of Array.from(fileIocs).sort()) {
            const fid = makeId({ kind: 'FILE', pkg: pkgName, ver: pkgVer, hook, file: rel, ioc });
            if (allowlist.has(fid)) {
              allowlisted++;
            } else {
              findings.push({ id: fid, pkg: pkgName, ver: pkgVer, hook, where: absPath, detail: rel, ioc });
            }
          }

          for (const { id, re } of patterns) {
            if (re.test(contentNorm)) {
              const fid = makeId({ kind: 'FILE', pkg: pkgName, ver: pkgVer, hook, file: rel, rule: id });
              if (allowlist.has(fid)) {
                allowlisted++;
              } else {
                findings.push({ id: fid, pkg: pkgName, ver: pkgVer, hook, where: absPath, detail: rel, rule: id });
              }
            }
          }
        }
      } catch {
        // Ignore unreadable files
      }
    }
  }
}

findings.sort((a, b) => a.id.localeCompare(b.id));

console.log(`Supply-chain scan (Node): scannedPackages=${scannedPackages} findings=${findings.length} allowlisted=${allowlisted}`);
if (allowlistPath) {
  console.log(`Allowlist: ${allowlistPath} (entries=${allowlist.size})`);
} else {
  console.log('Allowlist: (none)');
}

if (findings.length > 0) {
  console.log('');
  console.log('Findings (copy IDs into allowlist to suppress with justification):');
  for (const f of findings) {
    const loc = f.where ? ` where=${f.where}` : '';
    const extra = f.ioc ? ` ioc=${f.ioc}` : (f.rule ? ` rule=${f.rule}` : '');
    console.log(`- ${f.id}${extra}${loc}`);
    if (f.detail) {
      console.log(`  detail=${sanitizeValue(f.detail).slice(0, 200)}`);
    }
  }
  process.exit(1);
}

process.exit(0);
__GOV_NODE_SUPPLY_SCAN__
  local ec=$?
  set -e

  rm -f "${pkg_json_list}"

  if [[ $ec -eq 2 ]]; then
    return 2
  fi
  if [[ $ec -ne 0 ]]; then
    return 1
  fi
  return 0
}

scan_node_project_supply_chain() {
  local project_dir="$1"
  local allowlist="$2"

  if [[ ! -f "${project_dir}/package.json" ]]; then
    return 0
  fi

  local pm=""
  local pm_ec=0
  set +e
  pm="$(detect_node_package_manager_in_dir "${project_dir}")"
  pm_ec=$?
  set -e

  case "$pm_ec" in
    0) ;;
    1)
      echo "BLOCKED: ${project_dir}: package.json detected but no Node lockfile found" >&2
      return 2
      ;;
    2)
      echo "FAIL: ${project_dir}: multiple Node lockfiles present (ambiguous package manager)" >&2
      return 1
      ;;
    *)
      echo "BLOCKED: ${project_dir}: failed to detect Node package manager (exit code ${pm_ec})" >&2
      return 2
      ;;
  esac

  set +e
  install_node_deps_ignore_scripts_in_dir "${pm}" "${project_dir}"
  local ec_install=$?
  set -e
  if [[ $ec_install -eq 2 ]]; then
    return 2
  elif [[ $ec_install -ne 0 ]]; then
    return 1
  fi

  set +e
  scan_node_modules_supply_chain_in_dir "${project_dir}" "${allowlist}"
  local ec_scan=$?
  set -e
  if [[ $ec_scan -eq 2 ]]; then
    return 2
  elif [[ $ec_scan -ne 0 ]]; then
    return 1
  fi

  return 0
}

check_supply_chain() {
  # SEC-3: Supply-chain verification gate.
  # - Enforces GitHub Actions SHA pinning (no uses: ...@vN).
  # - Scans Node dependencies for subprojects (web/, cdk/, contracts/) with scripts disabled.
  # - Runs lightweight Go and Python metadata scans.

  local allowlist="${PLANNING_DIR}/lesser-host-supply-chain-allowlist.txt"
  if [[ -f "${allowlist}" ]]; then
    echo "Supply-chain allowlist: ${allowlist}"
  else
    echo "Supply-chain allowlist: missing (treated as empty): ${allowlist}"
  fi

  local fail=0
  local blocked=0

  set +e
  check_supply_chain_actions_pinned
  local ec_actions=$?
  set -e
  if [[ $ec_actions -ne 0 ]]; then
    fail=1
  fi

  local -a node_projects=("web" "cdk" "contracts")
  local p
  for p in "${node_projects[@]}"; do
    if [[ -f "${REPO_ROOT}/${p}/package.json" ]]; then
      echo ""
      echo "Node supply-chain scan: ${p}/"
      set +e
      scan_node_project_supply_chain "${REPO_ROOT}/${p}" "${allowlist}"
      local ec_node=$?
      set -e
      if [[ $ec_node -eq 2 ]]; then
        blocked=1
      elif [[ $ec_node -ne 0 ]]; then
        fail=1
      fi
    fi
  done

  echo ""
  set +e
  scan_go_supply_chain "${allowlist}"
  local ec_go=$?
  set -e
  if [[ $ec_go -eq 2 ]]; then
    blocked=1
  elif [[ $ec_go -ne 0 ]]; then
    fail=1
  fi

  echo ""
  set +e
  scan_python_supply_chain "${allowlist}"
  local ec_py=$?
  set -e
  if [[ $ec_py -eq 2 ]]; then
    blocked=1
  elif [[ $ec_py -ne 0 ]]; then
    fail=1
  fi

  if [[ "${fail}" -ne 0 ]]; then
    return 1
  fi
  if [[ "${blocked}" -ne 0 ]]; then
    return 2
  fi
  return 0
}

prepare_check_env() {
  # Optional preflight to enforce pinned tools for known Go gates.
  local id="$1"
  local cmd="$2"

  if [[ ! -f "${REPO_ROOT}/go.mod" ]]; then
    return 0
  fi

  # Node workspaces (web/cdk/contracts) can create node_modules/ during other rubric checks.
  # Some Node dependencies can trip `go test ./...` and `golangci-lint` when present.
  # Treat node_modules as non-source and remove it before Go-scoped checks.
  if [[ "$cmd" == *"go test"* || "$cmd" == *"golangci-lint"* || "$cmd" == *"govulncheck"* ]]; then
    while IFS= read -r -d '' d; do
      rm -rf "${d}"
    done < <(find "${REPO_ROOT}" -type d -name node_modules -prune -not -path "${REPO_ROOT}/.git/*" -print0 2>/dev/null)
  fi

  case "$id" in
    CON-2|COM-3|SEC-1)
      if [[ -f "${REPO_ROOT}/.golangci.yml" ]] || [[ "$cmd" == *"golangci-lint"* ]]; then
        ensure_golangci_lint_pinned
      fi
      ;;
    SEC-2)
      if [[ "$cmd" == *"govulncheck"* ]]; then
        ensure_govulncheck_pinned
      fi
      ;;
    *) return 0 ;;
  esac
}

# Helper: run a single check and record result
# Usage: run_check <rubric_id> <category> <command>
run_check() {
  local id="$1"
  local category="$2"
  local cmd="$3"

  local output_file="${EVIDENCE_DIR}/${id}-output.log"

  if [[ -z "${cmd//[[:space:]]/}" ]] || [[ "${cmd}" == "TODO:"* ]] || [[ "${cmd}" == "{{CMD_"* ]]; then
    printf '%s\n' "Verifier command not configured: ${cmd}" > "${output_file}"
    record_result "$id" "$category" "BLOCKED" "Verifier command not configured" "$output_file"
    return 0
  fi

  set +e
  (
    set -euo pipefail
    prepare_check_env "$id" "$cmd"
    eval "${cmd}"
  ) >"${output_file}" 2>&1
  local ec=$?
  set -e

  if [[ $ec -eq 0 ]]; then
    record_result "$id" "$category" "PASS" "Command succeeded" "$output_file"
  elif [[ $ec -eq 2 || $ec -eq 126 || $ec -eq 127 ]]; then
    record_result "$id" "$category" "BLOCKED" "Command reported BLOCKED (exit code ${ec})" "$output_file"
  else
    record_result "$id" "$category" "FAIL" "Command failed with exit code ${ec}" "$output_file"
  fi
}

check_file_exists() {
  local id="$1"
  local category="$2"
  local file_path="$3"

  if [[ -f "${file_path}" ]]; then
    record_result "$id" "$category" "PASS" "File exists" "$file_path"
  else
    record_result "$id" "$category" "FAIL" "Required file missing" "$file_path"
  fi
}

check_parity() {
  local threat_model="${PLANNING_DIR}/lesser-host-threat-model.md"
  local controls_matrix="${PLANNING_DIR}/lesser-host-controls-matrix.md"
  local evidence_path="${EVIDENCE_DIR}/DOC-5-parity.log"

  if [[ ! -f "${threat_model}" ]] || [[ ! -f "${controls_matrix}" ]]; then
    printf '%s\n' "Threat model or controls matrix missing" > "${evidence_path}"
    record_result "DOC-5" "Docs" "BLOCKED" "Threat model or controls matrix missing" "${evidence_path}"
  else
    local threat_ids
    threat_ids=$(grep -oE 'THR-[0-9]+' "${threat_model}" | sort -u || true)

    local missing=""
    for thr_id in ${threat_ids}; do
      if ! grep -q "${thr_id}" "${controls_matrix}"; then
        missing="${missing} ${thr_id}"
      fi
    done

    echo "Threat IDs found: ${threat_ids:-none}" > "${evidence_path}"
    echo "Missing from controls:${missing:-none}" >> "${evidence_path}"

    if [[ -z "${missing}" ]]; then
      record_result "DOC-5" "Docs" "PASS" "All threat IDs mapped in controls matrix" "${evidence_path}"
    else
      record_result "DOC-5" "Docs" "FAIL" "Unmapped threats:${missing}" "${evidence_path}"
    fi
  fi
}

check_mai_ci_rubric_enforced() {
  # MAI-4: verifies that CI runs the deterministic rubric verifier.
  local found_ci="false"
  local found_hook="false"

  if [[ -d "${REPO_ROOT}/.github/workflows" ]]; then
    found_ci="true"
    local wf
    for wf in "${REPO_ROOT}/.github/workflows/"*.yml "${REPO_ROOT}/.github/workflows/"*.yaml; do
      [[ -f "${wf}" ]] || continue
      if grep -q 'gov-verify-rubric\.sh' "${wf}"; then
        found_hook="true"
        echo "Found gov-verify-rubric.sh invocation in: ${wf#${REPO_ROOT}/}"
        break
      fi
    done
  fi

  if [[ "${found_ci}" != "true" ]]; then
    echo "MAI-4: FAIL (no CI configuration detected)"
    echo "Add CI that runs: bash gov-infra/verifiers/gov-verify-rubric.sh"
    return 1
  fi
  if [[ "${found_hook}" != "true" ]]; then
    echo "MAI-4: FAIL (CI configuration detected, but no job runs gov-verify-rubric.sh)"
    echo "Ensure CI runs: bash gov-infra/verifiers/gov-verify-rubric.sh"
    return 1
  fi

  echo "MAI-4: PASS"
  return 0
}

echo "=== GovTheory Rubric Verifier ==="
echo "Project: lesser-host"
echo "Timestamp: ${REPORT_TIMESTAMP}"
echo ""

normalize_feature_flags

# Commands are intentionally centralized here so the rubric docs and verifier stay aligned.
CMD_UNIT=$(cat <<'__GOV_CMD_UNIT__'
go test -count=1 ./...
__GOV_CMD_UNIT__
)

CMD_INTEGRATION=$(cat <<'__GOV_CMD_INTEGRATION__'
# Web unit tests
( cd web && npm ci && npm test )

# Solidity contract compile (Hardhat)
( cd contracts && npm ci && npm test )

# CDK runner contract tests + synth (no AWS creds required for synth)
( cd cdk && npm ci && npm test && npx cdk synth -c stage=lab )
__GOV_CMD_INTEGRATION__
)

CMD_COVERAGE=$(cat <<'__GOV_CMD_COVERAGE__'
rm -f gov-infra/evidence/coverage.out gov-infra/evidence/coverage-summary.txt

go test ./... -coverprofile=gov-infra/evidence/coverage.out

go tool cover -func=gov-infra/evidence/coverage.out | tee gov-infra/evidence/coverage-summary.txt

total="$(go tool cover -func=gov-infra/evidence/coverage.out | awk '/^total:/ {gsub(/%/,"",$3); print $3}')"

echo "total_coverage=${total}%"

awk -v total="${total}" -v thr="80" 'BEGIN{
  if (total == "") { print "FAIL: could not parse total coverage"; exit 1 }
  if ((total + 0) < (thr + 0)) {
    printf("FAIL: coverage %.1f%% < %.1f%%\n", total, thr);
    exit 1
  }
  printf("PASS: coverage %.1f%% >= %.1f%%\n", total, thr);
  exit 0
}'
__GOV_CMD_COVERAGE__
)

CMD_FMT=$(cat <<'__GOV_CMD_FMT__'
if ! command -v gofmt >/dev/null 2>&1; then
  echo "BLOCKED: gofmt is required" >&2
  exit 2
fi

# Format check should apply to repo-owned sources only (not build outputs like cdk.out/ or dependency trees like node_modules/).
if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if [[ -z "$(git ls-files '*.go')" ]]; then
    echo "FAIL: no tracked Go files found for formatting check"
    exit 1
  fi

  tmp_err="$(mktemp)"
  set +e
  unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l 2>"${tmp_err}")"
  ec=$?
  set -e

  if [[ $ec -ne 0 ]]; then
    echo "FAIL: gofmt errored"
    cat "${tmp_err}" || true
    rm -f "${tmp_err}"
    exit 1
  fi
  rm -f "${tmp_err}"
else
  echo "BLOCKED: git is required for deterministic formatting scope" >&2
  exit 2
fi

unformatted="$(printf '%s\n' "${unformatted}" | sed '/^$/d')"
if [[ -n "${unformatted}" ]]; then
  echo "Unformatted Go files:"
  echo "${unformatted}"
  exit 1
fi

echo "PASS: gofmt clean"
__GOV_CMD_FMT__
)

CMD_LINT=$(cat <<'__GOV_CMD_LINT__'
# Go lint (requires PIN_GOLANGCI_LINT_VERSION to be set; otherwise this check is BLOCKED).

golangci-lint run --timeout=10m

# Web lint + typecheck
( cd web && npm ci && npm run lint && npm run typecheck )

# CDK build (TypeScript)
( cd cdk && npm ci && npm run build )

# Solidity lint (contracts)
( cd contracts && npm ci && npm run lint )
__GOV_CMD_LINT__
)

CMD_CONTRACT=$(cat <<'__GOV_CMD_CONTRACT__'
# Contract parity: ensure Go boundary ABIs match Solidity artifacts.
#
# TipSplitter ABI is defined in Go for host registry ops; it must remain compatible
# with the Solidity contract compiled by Hardhat.

if ! command -v node >/dev/null 2>&1; then
  echo "BLOCKED: node is required" >&2
  exit 2
fi

artifact="contracts/artifacts/contracts/TipSplitter.sol/TipSplitter.json"
if [[ ! -f "${artifact}" ]]; then
  echo "BLOCKED: missing TipSplitter artifact at ${artifact} (run contracts build/tests to generate Hardhat artifacts)" >&2
  exit 2
fi

node <<'__GOV_NODE_CONTRACT__'
const fs = require('fs');

const goPath = 'internal/tips/tipsplitter_abi.go';
const artifactPath = 'contracts/artifacts/contracts/TipSplitter.sol/TipSplitter.json';

function readText(p) {
  return fs.readFileSync(p, 'utf8');
}

function extractGoRawStringConst(source, constName) {
  const re = new RegExp('const\\s+' + constName + '\\s*=\\s*`([\\s\\S]*?)`', 'm');
  const m = source.match(re);
  if (!m) {
    throw new Error(`could not find const ${constName} raw string in ${goPath}`);
  }
  return m[1];
}

function normalizeAbiEntry(entry) {
  const type = String(entry.type || '').trim();
  const name = String(entry.name || '').trim();
  const stateMutability = entry.stateMutability == null ? '' : String(entry.stateMutability).trim();

  const normalizeParams = (list) =>
    Array.isArray(list)
      ? list.map((p) => ({
          name: String(p.name || '').trim(),
          type: String(p.type || '').trim(),
        }))
      : [];

  return {
    type,
    name,
    stateMutability,
    inputs: normalizeParams(entry.inputs),
    outputs: normalizeParams(entry.outputs),
  };
}

function keyFor(entry) {
  const e = normalizeAbiEntry(entry);
  const inTypes = e.inputs.map((p) => p.type).join(',');
  const outTypes = e.outputs.map((p) => p.type).join(',');
  return `${e.type}:${e.name}:${e.stateMutability}:in(${inTypes}):out(${outTypes})`;
}

function signatureFor(entry) {
  const e = normalizeAbiEntry(entry);
  const inTypes = e.inputs.map((p) => p.type).join(',');
  return `${e.type} ${e.name}(${inTypes})`;
}

const goSource = readText(goPath);
const goAbiRaw = extractGoRawStringConst(goSource, 'TipSplitterABI');

let goAbi;
try {
  goAbi = JSON.parse(goAbiRaw);
} catch (e) {
  throw new Error(`failed to parse Go TipSplitterABI JSON: ${e.message}`);
}
if (!Array.isArray(goAbi)) {
  throw new Error(`Go TipSplitterABI JSON must be an array`);
}

const artifact = JSON.parse(readText(artifactPath));
if (!artifact || !Array.isArray(artifact.abi)) {
  throw new Error(`artifact missing .abi array at ${artifactPath}`);
}

const artifactKeys = new Set(artifact.abi.map(keyFor));

const missing = [];
for (const entry of goAbi) {
  const k = keyFor(entry);
  if (!artifactKeys.has(k)) {
    missing.push(signatureFor(entry));
  }
}

if (missing.length > 0) {
  console.error(`FAIL: Go TipSplitterABI entries not found in Solidity artifact ABI (${missing.length}):`);
  for (const m of missing) console.error(`- ${m}`);
  process.exit(1);
}

console.log('PASS: contract parity (TipSplitter ABI subset)');
__GOV_NODE_CONTRACT__

# REST contract parity: required lesser-host contract artifacts must stay complete and
# the checked-in generated adapter must match a fresh regeneration.
( cd web && npm ci && npm run verify:lesser-host-contracts )
__GOV_CMD_CONTRACT__
)

CMD_MODULES=$(cat <<'__GOV_CMD_MODULES__'
if ! command -v go >/dev/null 2>&1; then
  echo "BLOCKED: go is required" >&2
  exit 2
fi

mods="$(find . -name go.mod \
  -not -path './**/node_modules/**' \
  -not -path './**/dist/**' \
  -not -path './**/.build/**' \
  -not -path './**/cdk.out/**' \
  -not -path './**/vendor/**' \
  2>/dev/null | LC_ALL=C sort)"

if [[ -z "${mods}" ]]; then
  echo "FAIL: no go.mod files found"
  exit 1
fi

echo "Go modules found:"
echo "${mods}"

echo ""
	while IFS= read -r mod; do
	  [[ -z "${mod}" ]] && continue
	  dir="$(dirname "${mod}")"
	  echo "Compiling module: ${dir}"
	  # Node workspaces (CDK/web/contracts) may materialize node_modules/ during other rubric checks.
	  # Some Node dependencies include Go templates with invalid filenames, which can break `go test ./...`.
	  # COM-1 is defined against a clean checkout, so treat node_modules as non-source and remove it for module compilation.
	  if [[ -d "${dir}/node_modules" ]]; then
	    echo "Cleaning ${dir}/node_modules (not part of Go module)"
	    rm -rf "${dir}/node_modules"
	  fi
	  pkgs="$(cd "${dir}" && go list ./... 2>/dev/null)"
	  if [[ -z "${pkgs}" ]]; then
	    echo "No Go packages detected (skipping): ${dir}"
	  else
	    (cd "${dir}" && go test -run=^$ ./...)
	  fi
	  echo ""
	done <<< "${mods}"

echo "PASS: all modules compile"
__GOV_CMD_MODULES__
)

CMD_TOOLCHAIN=$(cat <<'__GOV_CMD_TOOLCHAIN__'
# Enforces that local toolchains match repo expectations.
# This is strict by design; if you want a looser policy, bump the rubric version and change this verifier.

# --- Go version pin ---
if ! command -v go >/dev/null 2>&1; then
  echo "BLOCKED: go is required" >&2
  exit 2
fi

expected_go="$(awk '$1=="go"{print $2; exit}' go.mod)"
if [[ -z "${expected_go}" ]]; then
  echo "FAIL: could not parse expected Go version from go.mod"
  exit 1
fi

actual_go="$(go env GOVERSION | sed 's/^go//')"
if [[ "${actual_go}" != "${expected_go}" ]]; then
  echo "FAIL: Go version mismatch: expected=${expected_go} actual=${actual_go}"
  exit 1
fi

echo "PASS: Go version matches go.mod (${actual_go})"

# --- Nested module pins (must match) ---
if [[ -f cdk/go.mod ]]; then
  expected_go_cdk="$(awk '$1=="go"{print $2; exit}' cdk/go.mod)"
  if [[ "${expected_go_cdk}" != "${expected_go}" ]]; then
    echo "FAIL: cdk/go.mod Go version mismatch: root=${expected_go} cdk=${expected_go_cdk}"
    exit 1
  fi
  echo "PASS: cdk/go.mod Go version matches root (${expected_go_cdk})"
fi

# --- Node version pin ---
if ! command -v node >/dev/null 2>&1; then
  echo "BLOCKED: node is required" >&2
  exit 2
fi

node_ver="$(node -p 'process.versions.node' 2>/dev/null || true)"
if [[ -z "${node_ver}" ]]; then
  echo "BLOCKED: failed to read node version" >&2
  exit 2
fi

node_major="${node_ver%%.*}"
if [[ "${node_major}" != "24" ]]; then
  echo "FAIL: Node major version mismatch: expected=24 actual=${node_ver}"
  exit 1
fi

echo "PASS: Node major version is 24 (${node_ver})"

# --- CI pin parity (best-effort; must not be "latest") ---
if [[ -f .github/workflows/ci.yml ]]; then
  grep -q 'go-version-file:[[:space:]]*go\.mod' .github/workflows/ci.yml || {
    echo "FAIL: CI must use go-version-file: go.mod";
    exit 1;
  }
  grep -Eq "node-version:[[:space:]]*'24'|node-version:[[:space:]]*\"24\"|node-version:[[:space:]]*24" .github/workflows/ci.yml || {
    echo "FAIL: CI must pin node-version to 24";
    exit 1;
  }

  # golangci-lint pin: require action config to specify an explicit golangci-lint version.
  if grep -q 'golangci/golangci-lint-action@' .github/workflows/ci.yml; then
    if ! grep -qE '^[[:space:]]*version:[[:space:]]*v[0-9]+\.[0-9]+\.[0-9]+' .github/workflows/ci.yml; then
      echo "BLOCKED: CI uses golangci-lint-action but does not pin golangci-lint 'version: vX.Y.Z'";
      exit 2;
    fi
  fi

  echo "PASS: CI pin checks (go-version-file + node major)"
else
  echo "BLOCKED: CI workflow not found (.github/workflows/ci.yml)" >&2
  exit 2
fi
__GOV_CMD_TOOLCHAIN__
)

CMD_LINT_CONFIG=$(cat <<'__GOV_CMD_LINT_CONFIG__'
if [[ ! -f .golangci.yml ]]; then
  echo "FAIL: missing .golangci.yml"
  exit 1
fi

# Prefer a dedicated config verification command if supported by the pinned golangci-lint.
# Fail closed if not supported (avoid silently trusting an unvalidated config).
if golangci-lint config --help 2>/dev/null | grep -q 'verify'; then
  golangci-lint config verify --config .golangci.yml
  echo "PASS: golangci-lint config verified"
  exit 0
fi

echo "BLOCKED: pinned golangci-lint does not appear to support 'config verify'; update pin/tooling" >&2
exit 2
__GOV_CMD_LINT_CONFIG__
)

CMD_COV_THRESHOLD=$(cat <<'__GOV_CMD_COV_THRESHOLD__'
# Anti-drift: the documented threshold must remain at 80% for rubric v0.1.
# If you change this, bump the rubric version and update the verifier accordingly.

rub="gov-infra/planning/lesser-host-10of10-rubric.md"
if [[ ! -f "${rub}" ]]; then
  echo "FAIL: missing rubric doc at ${rub}"
  exit 1
fi

grep -Fq 'Coverage ≥ 80%' "${rub}" || {
  echo "FAIL: rubric doc must contain the exact string 'Coverage ≥ 80%'";
  exit 1;
}

echo "PASS: coverage threshold floor is documented as 80%"
__GOV_CMD_COV_THRESHOLD__
)

CMD_SEC_CONFIG=$(cat <<'__GOV_CMD_SEC_CONFIG__'
# Anti-drift: keep gosec excludes narrow and explicit.

if [[ ! -f .golangci.yml ]]; then
  echo "FAIL: missing .golangci.yml"
  exit 1
fi

codes="$(awk '
  BEGIN { in_gosec=0; in_ex=0 }
  /^[[:space:]]*gosec:[[:space:]]*$/ { in_gosec=1; next }
  in_gosec && /^[[:space:]]*excludes:[[:space:]]*$/ { in_ex=1; next }
  in_ex && /^[[:space:]]*-[[:space:]]*/ {
    gsub(/^[[:space:]]*-[[:space:]]*/, "");
    # strip comments
    sub(/[[:space:]]+#.*/, "");
    if ($0 != "") print $0;
    next
  }
  in_ex && $0 !~ /^[[:space:]]*-[[:space:]]*/ && $0 !~ /^[[:space:]]*$/ { exit }
' .golangci.yml | LC_ALL=C sort -u)"

echo "gosec excludes:"; echo "${codes:-<none>}"; echo ""

extra="$(printf '%s\n' "${codes}" | grep -Ev '^(G101|G104)$' || true)"

if [[ -n "${extra}" ]]; then
  echo "FAIL: unexpected gosec excludes detected (policy allows only G101 and G104 for rubric v0.1):"
  echo "${extra}"
  exit 1
fi

echo "PASS: security config excludes are within policy"
__GOV_CMD_SEC_CONFIG__
)

CMD_LOGGING=$(cat <<'__GOV_CMD_LOGGING__'
set -euo pipefail

# COM-6: logging/operational standards.
#
# Policy (rubric v0.1):
# - Use structured JSON logs via slog to stdout.
# - All Lambda entrypoints wire apptheory observability hooks.
# - Avoid fmt.Print*/log.Print* in non-test Go sources.

fail=0

if [[ ! -f internal/observability/observability.go ]]; then
  echo "FAIL: missing internal/observability/observability.go"
  exit 1
fi

if ! grep -q 'slog.NewJSONHandler(os.Stdout' internal/observability/observability.go; then
  echo "FAIL: observability must use slog.NewJSONHandler(os.Stdout, ...)"
  fail=1
fi

missing=0
for f in cmd/*/main.go; do
  [[ -f "${f}" ]] || continue
  if ! grep -q 'apptheory.WithObservability(observability.New(' "${f}"; then
    echo "FAIL: missing apptheory.WithObservability(observability.New(...)) in ${f}"
    missing=1
  fi
done
if [[ "${missing}" -ne 0 ]]; then
  fail=1
fi

go_files="$(git ls-files '*.go' | grep -v '_test\\.go$' || true)"
if [[ -n "${go_files}" ]]; then
  if echo "${go_files}" | xargs grep -nE '\\b(fmt|log)\\.Print(ln|f)?\\b' >/dev/null 2>&1; then
    echo "FAIL: fmt.Print*/log.Print* found in non-test Go sources:"
    echo "${go_files}" | xargs grep -nE '\\b(fmt|log)\\.Print(ln|f)?\\b' || true
    fail=1
  fi
fi

if [[ "${fail}" -ne 0 ]]; then
  exit 1
fi

echo "PASS: logging standards"
__GOV_CMD_LOGGING__
)

CMD_SAST=$(cat <<'__GOV_CMD_SAST__'
# SAST is implemented via golangci-lint with gosec enabled + Slither for Solidity.
# Slither and solc-select are installed into a repo-local Python venv (never system-wide).

golangci-lint run --timeout=10m

if ! command -v python3 >/dev/null 2>&1; then
  echo "BLOCKED: python3 is required to run slither" >&2
  exit 2
fi

venv_dir="${GOV_TOOLS_DIR}/python/venv-sec-1-slither"
venv_python="${venv_dir}/bin/python"
venv_bin="${venv_dir}/bin"

if [[ ! -x "${venv_python}" ]]; then
  echo "Creating SEC-1 slither venv at ${venv_dir}..."
  if ! python3 -m venv "${venv_dir}"; then
    echo "BLOCKED: failed to create python venv (python3 -m venv)" >&2
    exit 2
  fi
fi

export PATH="${venv_bin}:${PATH}"

echo "Installing pinned slither/solc-select (SEC-1) into venv..."
PIP_DISABLE_PIP_VERSION_CHECK=1 "${venv_python}" -m pip install \
  "slither-analyzer==${PIN_SLITHER_VERSION}" \
  "solc-select==${PIN_SOLC_SELECT_VERSION}"

sec1_home="${GOV_TOOLS_DIR}/python/home-sec-1"
mkdir -p "${sec1_home}"

(
  export HOME="${sec1_home}"

  solc-select install "${PIN_SOLC_VERSION}"
  solc-select use "${PIN_SOLC_VERSION}"

  export PATH="${HOME}/.solc-select:${PATH}"

  cd contracts
  npm ci
  slither contracts/TipSplitter.sol --config-file slither.config.json
)
__GOV_CMD_SAST__
)

CMD_VULN=$(cat <<'__GOV_CMD_VULN__'
# Dependency vulnerability scan (Go).
# This is BLOCKED until govulncheck is pinned.

# NOTE: govulncheck's default (-scan=symbol) analysis uses SSA and currently panics on a known
# generic + variadic edge case involving named byte-slices (reproducible with jsontext.Value).
#
# To avoid weakening the vuln gate (while still being deterministic), we scan the shipped binaries.
# This exercises dependency usage as actually built, and avoids SSA construction.

tmp_bin_dir="${GOV_TOOLS_DIR}/tmp/sec-2-bin"
rm -rf "${tmp_bin_dir}"
mkdir -p "${tmp_bin_dir}"

bins=(
  "ai-worker=./cmd/ai-worker"
  "control-plane-api=./cmd/control-plane-api"
  "email-ingress=./cmd/email-ingress"
  "provision-worker=./cmd/provision-worker"
  "render-worker=./cmd/render-worker"
  "trust-api=./cmd/trust-api"
)

for entry in "${bins[@]}"; do
  name="${entry%%=*}"
  pkg="${entry#*=}"
  echo "Building ${pkg}..."
  go build -o "${tmp_bin_dir}/${name}" "${pkg}"
  echo "Scanning ${name}..."
  govulncheck -mode=binary "${tmp_bin_dir}/${name}"
  echo ""
done

echo "PASS: govulncheck (binary mode)"
__GOV_CMD_VULN__
)

CMD_SUPPLY=$(cat <<'__GOV_CMD_SUPPLY__'
check_supply_chain
__GOV_CMD_SUPPLY__
)

CMD_MAILBOX_CONTROLS=$(cat <<'__GOV_CMD_MAILBOX_CONTROLS__'
# CMP-4: bounded soul comm mailbox authority controls.
#
# This verifier intentionally checks the policy/control baseline before mailbox
# content storage ships. Later storage/API milestones should extend this gate to
# inspect concrete models, schemas, and endpoint implementations.

required_files=(
  "docs/adr/0005-bounded-soul-comm-mailbox-authority.md"
  "docs/roadmap-soul-comm-mailbox.md"
  "docs/soul-surface.md"
  "gov-infra/planning/lesser-host-threat-model.md"
  "gov-infra/planning/lesser-host-controls-matrix.md"
  "gov-infra/planning/lesser-host-evidence-plan.md"
)

fail=0

require_file() {
  local f="$1"
  if [[ ! -f "${f}" ]]; then
    echo "FAIL: missing required bounded-mailbox policy file: ${f}"
    fail=1
  else
    echo "file: ${f}"
  fi
}

require_pattern() {
  local f="$1"
  local pattern="$2"
  local label="$3"
  if [[ ! -f "${f}" ]]; then
    return
  fi
  if ! grep -Eiq -- "${pattern}" "${f}"; then
    echo "FAIL: ${label} not found in ${f}"
    echo "  pattern: ${pattern}"
    fail=1
  else
    echo "PASS: ${label}"
  fi
}

for f in "${required_files[@]}"; do
  require_file "${f}"
done

adr="docs/adr/0005-bounded-soul-comm-mailbox-authority.md"
soul="docs/soul-surface.md"
roadmap="docs/roadmap-soul-comm-mailbox.md"
threats="gov-infra/planning/lesser-host-threat-model.md"
controls="gov-infra/planning/lesser-host-controls-matrix.md"
evidence="gov-infra/planning/lesser-host-evidence-plan.md"

require_pattern "${adr}" 'bounded soul comm mailbox authority' 'ADR title declares bounded mailbox authority'
require_pattern "${adr}" 'explicit retention policy|retention policy' 'content retention policy requirement'
require_pattern "${adr}" 'encryption at rest|encryption' 'encryption requirement'
require_pattern "${adr}" 'access[- ]audit' 'access audit requirement'
require_pattern "${adr}" 'list/content split|List/content split' 'list/content split requirement'
require_pattern "${adr}" 'semantic memory' 'no semantic-memory role'
require_pattern "${adr}" 'sha256\(raw_key\)' 'hash-only instance auth requirement'
require_pattern "${adr}" 'write-once' 'write-once audit/event requirement'
require_pattern "${adr}" 'protected.*identity|identity.*protected' 'protected identity/provenance requirement'
require_pattern "${adr}" 'MCP facade' 'body remains MCP facade'
require_pattern "${adr}" 'notification projections' 'lesser projection-only boundary'
require_pattern "${adr}" 'cross-tenant search' 'cross-tenant search non-goal'

require_pattern "${soul}" 'Soul Comm Mailbox v1 authority' 'soul surface names mailbox authority'
require_pattern "${soul}" 'redacted previews/metadata' 'soul surface requires redacted list previews'
require_pattern "${soul}" 'sha256\(raw_key\)' 'soul surface documents hash-only auth'

require_pattern "${roadmap}" 'Framework incorporation decisions' 'roadmap records framework incorporation decisions'
require_pattern "${roadmap}" 'write-once audit/event rows|audit/event rows.*write-once' 'roadmap requires write-once audit/event rows'
require_pattern "${roadmap}" 'retention/encryption/list-redaction controls' 'roadmap requires retention/encryption/list-redaction controls'

require_pattern "${threats}" 'THR-11' 'threat model includes bounded mailbox threat'
require_pattern "${threats}" 'Bounded mailbox content/state drift' 'threat model names content/state drift'

require_pattern "${controls}" 'CMP-4' 'controls matrix includes CMP-4'
require_pattern "${controls}" 'THR-11' 'controls matrix maps THR-11'
require_pattern "${controls}" 'retention, encryption, access audit, list/content split' 'controls matrix enumerates bounded mailbox controls'

require_pattern "${evidence}" 'CMP-4' 'evidence plan includes CMP-4'
require_pattern "${evidence}" 'CMP-4-output\.log' 'evidence plan names CMP-4 evidence path'

if [[ "${fail}" -ne 0 ]]; then
  exit 1
fi

echo "PASS: bounded soul comm mailbox governance controls are explicit"
__GOV_CMD_MAILBOX_CONTROLS__
)

CMD_P0=$(cat <<'__GOV_CMD_P0__'
if ! command -v go >/dev/null 2>&1; then
  echo "BLOCKED: go is required" >&2
  exit 2
fi

# Ensure P0 tests actually exist (otherwise -run would be a false green).
p0_list="$(go test ./... -list '^TestP0_' 2>/dev/null | grep -E '^TestP0_' || true)"
p0_count="$(printf '%s\n' "${p0_list}" | sed '/^$/d' | wc -l | tr -d ' ')"
if [[ "${p0_count}" -lt 1 ]]; then
  echo "FAIL: no TestP0_* tests found"
  exit 1
fi

echo "P0 tests found (${p0_count}):"
printf '%s\n' "${p0_list}"

echo ""
go test -count=1 ./... -run '^TestP0_'

echo "PASS: P0 regression suite"
__GOV_CMD_P0__
)

CMD_FILE_BUDGET=$(cat <<'__GOV_CMD_FILE_BUDGET__'
fail=0

go_max_lines=2500
ts_max_lines=2000

echo "Go file budget: max_lines=${go_max_lines}"
go_files="$(git ls-files '*.go' | grep -v '^vendor/' || true)"
if [[ -n "${go_files}" ]]; then
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    lines="$(wc -l < "${f}" | tr -d ' ')"
    if [[ "${lines}" -gt "${go_max_lines}" ]]; then
      echo "FAIL: Go file too large (${lines} > ${go_max_lines}): ${f}"
      fail=1
    fi
  done <<< "${go_files}"

  echo ""
  echo "Top Go files by line count:"
  echo "${go_files}" | xargs wc -l | sort -n | tail -n 10
fi

echo ""
echo "TS/JS file budget: max_lines=${ts_max_lines} (excluding generated + .d.ts)"
ts_files="$(git ls-files '*.ts' '*.tsx' '*.js' '*.mjs' '*.cjs' \
  | grep -v '^web/src/lib/greater/adapters/graphql/generated/' \
  | grep -v '^web/src/lib/greater/adapters/rest/generated/' \
  | grep -v '\.d\.ts$' \
  || true)"
if [[ -n "${ts_files}" ]]; then
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    lines="$(wc -l < "${f}" | tr -d ' ')"
    if [[ "${lines}" -gt "${ts_max_lines}" ]]; then
      echo "FAIL: TS/JS file too large (${lines} > ${ts_max_lines}): ${f}"
      fail=1
    fi
  done <<< "${ts_files}"

  echo ""
  echo "Top TS/JS files by line count:"
  echo "${ts_files}" | xargs wc -l | sort -n | tail -n 10
fi

if [[ "${fail}" -ne 0 ]]; then
  exit 1
fi

echo "PASS: file budgets"
__GOV_CMD_FILE_BUDGET__
)

CMD_MAINTAINABILITY=$(cat <<'__GOV_CMD_MAINTAINABILITY__'
# Maintainability roadmap must exist and be token-free.

rm_file="gov-infra/planning/lesser-host-10of10-roadmap.md"
if [[ ! -f "${rm_file}" ]]; then
  echo "FAIL: missing roadmap at ${rm_file}"
  exit 1
fi

if grep -q '{{' "${rm_file}"; then
  echo "FAIL: roadmap contains unrendered template tokens"
  exit 1
fi

echo "PASS: maintainability roadmap present"
__GOV_CMD_MAINTAINABILITY__
)

CMD_SINGLETON=$(cat <<'__GOV_CMD_SINGLETON__'
if ! command -v git >/dev/null 2>&1; then
  echo "BLOCKED: git is required" >&2
  exit 2
fi

canon="internal/httpx/http_helpers.go"
if [[ ! -f "${canon}" ]]; then
  echo "FAIL: missing canonical helper file: ${canon}"
  exit 1
fi

# Enforce exactly one exported implementation for each helper.
declare -a exported_pats=(
  '^func ParseJSON[(]'
  '^func BearerToken[(]'
  '^func FirstHeaderValue[(]'
  '^func FirstQueryValue[(]'
)

for pat in "${exported_pats[@]}"; do
  matches="$(git grep -nE "${pat}" -- '*.go' | grep -v '_test\\.go:' || true)"
  count="$(printf '%s\n' "${matches}" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [[ "${count}" -ne 1 ]]; then
    echo "FAIL: expected exactly 1 definition matching ${pat}, found ${count}"
    printf '%s\n' "${matches}"
    exit 1
  fi
  if ! printf '%s\n' "${matches}" | grep -q "^${canon}:"; then
    echo "FAIL: canonical definition for ${pat} not in ${canon}"
    printf '%s\n' "${matches}"
    exit 1
  fi
done

# Ensure legacy helper names are not reintroduced elsewhere.
legacy_matches="$(git grep -nE '^func (parseJSON|bearerToken|firstHeaderValue|firstQueryValue)[(]' -- '*.go' | grep -v '_test\\.go:' || true)"
if [[ -n "${legacy_matches}" ]]; then
  echo "FAIL: legacy helper definitions still present (use internal/httpx instead):"
  printf '%s\n' "${legacy_matches}"
  exit 1
fi

echo "PASS: canonical helper singletons enforced"
__GOV_CMD_SINGLETON__
)

CMD_DOC_INTEGRITY=$(cat <<'__GOV_CMD_DOC_INTEGRITY__'
# Doc integrity: ensure no template tokens remain and required planning files exist.

token_paths=(
  "gov-infra/pack.json"
  "gov-infra/README.md"
  "gov-infra/AGENTS.md"
  "gov-infra/planning"
)
if grep -R --line-number '{{' "${token_paths[@]}" >/dev/null 2>&1; then
  echo "FAIL: unrendered template tokens found in governance artifacts"
  grep -R --line-number '{{' "${token_paths[@]}" || true
  exit 1
fi

required=(
  "gov-infra/README.md"
  "gov-infra/AGENTS.md"
  "gov-infra/pack.json"
  "gov-infra/planning/lesser-host-controls-matrix.md"
  "gov-infra/planning/lesser-host-threat-model.md"
  "gov-infra/planning/lesser-host-10of10-rubric.md"
  "gov-infra/planning/lesser-host-10of10-roadmap.md"
  "gov-infra/planning/lesser-host-evidence-plan.md"
  "gov-infra/planning/lesser-host-supply-chain-allowlist.txt"
  "gov-infra/planning/lesser-host-ai-drift-recovery.md"
  "gov-infra/planning/lesser-host-lint-green-roadmap.md"
  "gov-infra/planning/lesser-host-coverage-roadmap.md"
)

missing=0
for f in "${required[@]}"; do
  if [[ ! -f "${f}" ]]; then
    echo "FAIL: missing required governance artifact: ${f}"
    missing=1
  fi
done

if [[ "${missing}" -ne 0 ]]; then
  exit 1
fi

echo "PASS: doc integrity basic checks"
__GOV_CMD_DOC_INTEGRITY__
)

CMD_CI_ENFORCED="check_mai_ci_rubric_enforced"

# === Quality (QUA) ===
run_check "QUA-1" "Quality" "$CMD_UNIT"
run_check "QUA-2" "Quality" "$CMD_INTEGRATION"
run_check "QUA-3" "Quality" "$CMD_COVERAGE"

# === Consistency (CON) ===
run_check "CON-1" "Consistency" "$CMD_FMT"
run_check "CON-2" "Consistency" "$CMD_LINT"
run_check "CON-3" "Consistency" "$CMD_CONTRACT"

# === Completeness (COM) ===
run_check "COM-1" "Completeness" "$CMD_MODULES"
run_check "COM-2" "Completeness" "$CMD_TOOLCHAIN"
run_check "COM-3" "Completeness" "$CMD_LINT_CONFIG"
run_check "COM-4" "Completeness" "$CMD_COV_THRESHOLD"
run_check "COM-5" "Completeness" "$CMD_SEC_CONFIG"
run_check "COM-6" "Completeness" "$CMD_LOGGING"

# === Security (SEC) ===
run_check "SEC-1" "Security" "$CMD_SAST"
run_check "SEC-2" "Security" "$CMD_VULN"
run_check "SEC-3" "Security" "$CMD_SUPPLY"
run_check "SEC-4" "Security" "$CMD_P0"

# === Compliance Readiness (CMP) ===
check_file_exists "CMP-1" "Compliance" "${PLANNING_DIR}/lesser-host-controls-matrix.md"
check_file_exists "CMP-2" "Compliance" "${PLANNING_DIR}/lesser-host-evidence-plan.md"
check_file_exists "CMP-3" "Compliance" "${PLANNING_DIR}/lesser-host-threat-model.md"
run_check "CMP-4" "Compliance" "$CMD_MAILBOX_CONTROLS"

# === Maintainability (MAI) ===
run_check "MAI-1" "Maintainability" "$CMD_FILE_BUDGET"
run_check "MAI-2" "Maintainability" "$CMD_MAINTAINABILITY"
run_check "MAI-3" "Maintainability" "$CMD_SINGLETON"
run_check "MAI-4" "Maintainability" "$CMD_CI_ENFORCED"

# === Docs (DOC) ===
check_file_exists "DOC-1" "Docs" "${PLANNING_DIR}/lesser-host-threat-model.md"
check_file_exists "DOC-2" "Docs" "${PLANNING_DIR}/lesser-host-evidence-plan.md"
check_file_exists "DOC-3" "Docs" "${PLANNING_DIR}/lesser-host-10of10-rubric.md"
run_check "DOC-4" "Docs" "$CMD_DOC_INTEGRITY"
check_parity  # DOC-5

# === Generate Report ===
echo ""
echo "=== Generating Report ==="

RESULTS_JSON=$(printf "%s," "${RESULTS[@]}")
RESULTS_JSON="[${RESULTS_JSON%,}]"

OVERALL_STATUS="PASS"
if [[ ${FAIL_COUNT} -gt 0 ]]; then
  OVERALL_STATUS="FAIL"
elif [[ ${BLOCKED_COUNT} -gt 0 ]]; then
  OVERALL_STATUS="BLOCKED"
fi

cat > "${REPORT_PATH}" <<EOF
{
  "\$schema": "https://gov.pai.dev/schemas/gov-rubric-report.schema.json",
  "schemaVersion": ${REPORT_SCHEMA_VERSION},
  "timestamp": "${REPORT_TIMESTAMP}",
  "pack": {
    "version": "4221f0e715b1",
    "digest": "20fb3f879500ca3fd4c4ca35c95a4428e72c08d2dcf9dff40fdc0ef06f81276f"
  },
  "project": {
    "name": "lesser-host",
    "slug": "lesser-host"
  },
  "summary": {
    "status": "${OVERALL_STATUS}",
    "pass": ${PASS_COUNT},
    "fail": ${FAIL_COUNT},
    "blocked": ${BLOCKED_COUNT}
  },
  "results": ${RESULTS_JSON}
}
EOF

echo "Report written to: ${REPORT_PATH}"
echo ""
echo "=== Summary ==="
echo "Status: ${OVERALL_STATUS}"
echo "Pass: ${PASS_COUNT}"
echo "Fail: ${FAIL_COUNT}"
echo "Blocked: ${BLOCKED_COUNT}"

if [[ "${OVERALL_STATUS}" == "PASS" ]]; then
  exit 0
fi
exit 1
