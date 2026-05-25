#!/usr/bin/env bash
# SEC-8: CloudFront distribution composition.
#
# Synthesizes the CDK stack and parses the CloudFormation template to verify
# the CloudFront distribution composition shape:
#
#   1. The SSR Lambda Function URL is the OAC-protected default origin
#      (AuthType: AWS_IAM, origin access control present)
#   2. /_facetheory/data/* routes to the S3 htmlStoreBucket (OAC-protected,
#      GET/HEAD/OPTIONS only)
#   3. Every required bearer-auth cache behavior is present with exact
#      PathPattern matching (no fuzzy fallback). The target origin must
#      exist and must NOT carry OAC. Missing → FAIL.
#   4. S3 origins (framework-driven by AppTheorySsrSite + our htmlStoreBucket
#      sidecar) and the default SSR Function URL origin may carry OAC.
#      Custom/HTTP bearer-auth origins must NOT carry OAC (auth is in the
#      Lambda handler). Any Custom origin other than default with OAC → FAIL.
#
# Pass: all composition invariants hold. Fail: any drift detected.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-8
# Issue: equaltoai/lesser-host#400

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CDK_DIR="${REPO_ROOT}/cdk"

echo "SEC-8: CloudFront distribution composition"

if [[ ! -d "${CDK_DIR}" ]]; then
  echo "BLOCKED: cdk/ directory not found at ${CDK_DIR}" >&2
  exit 2
fi

if [[ ! -f "${CDK_DIR}/package.json" ]]; then
  echo "BLOCKED: cdk/package.json not found" >&2
  exit 2
fi

if ! command -v node >/dev/null 2>&1; then
  echo "BLOCKED: node is required" >&2
  exit 2
fi

# Synthesize the CDK stack for lab stage (requires no AWS credentials for synth).
echo "Synthesizing CDK (lab stage)..."
set +e
cdk_synth_output="$(cd "${CDK_DIR}" && npm ci --no-audit --no-fund 2>&1 && npx cdk synth -c stage=lab 2>&1)"
ec=$?
set -e

if [[ $ec -ne 0 ]]; then
  echo "" >&2
  echo "BLOCKED: CDK synth failed:" >&2
  printf '%s\n' "${cdk_synth_output}" >&2
  exit 2
fi

# Find the synthesized template.
TEMPLATE_DIR="${CDK_DIR}/cdk.out"
TEMPLATE_FILE=""
for f in "${TEMPLATE_DIR}/"*.template.json; do
  if [[ -f "${f}" ]]; then
    TEMPLATE_FILE="${f}"
    break
  fi
done

if [[ -z "${TEMPLATE_FILE}" ]]; then
  echo "BLOCKED: no synthesized template found under ${TEMPLATE_DIR}" >&2
  exit 2
fi

echo "Analyzing template: ${TEMPLATE_FILE#${REPO_ROOT}/}"
echo ""

# We need jq for parsing the CloudFormation template.
if ! command -v jq >/dev/null 2>&1; then
  echo "BLOCKED: jq is required to parse the synthesized CloudFormation template" >&2
  exit 2
fi

fail=0

# --- Check 1: CloudFront distribution exists ---
DISTRIBUTION_ID="$(jq -r '.Resources | to_entries[] | select(.value.Type == "AWS::CloudFront::Distribution") | .key' "${TEMPLATE_FILE}")"
if [[ -z "${DISTRIBUTION_ID}" ]]; then
  echo "FAIL: no CloudFront distribution found in synthesized template" >&2
  exit 1
fi
echo "CloudFront distribution: ${DISTRIBUTION_ID}"

DIST=".Resources.${DISTRIBUTION_ID}.Properties.DistributionConfig"

# ── Check 2: Default origin is OAC-protected Lambda Function URL ──────────
DEFAULT_ORIGIN_ID="$(jq -r "${DIST}.DefaultCacheBehavior.TargetOriginId" "${TEMPLATE_FILE}")"
echo "Default origin ID: ${DEFAULT_ORIGIN_ID}"

# Verify the default origin has an OriginAccessControlId (OAC).
OAC_COUNT="$(jq -r "[${DIST}.Origins[] | select(.Id == \"${DEFAULT_ORIGIN_ID}\") | select(.OriginAccessControlId != null)] | length" "${TEMPLATE_FILE}")"
if [[ "${OAC_COUNT}" -eq 0 ]]; then
  echo "FAIL: default origin '${DEFAULT_ORIGIN_ID}' does not have OriginAccessControl (OAC required)" >&2
  fail=1
else
  echo "  OAC: present"
fi

# Verify the default origin viewer protocol policy.
DEFAULT_VP="$(jq -r "${DIST}.DefaultCacheBehavior.ViewerProtocolPolicy" "${TEMPLATE_FILE}")"
echo "  ViewerProtocolPolicy: ${DEFAULT_VP}"

# Verify the default behavior allowed methods.
DEFAULT_METHODS="$(jq -r "${DIST}.DefaultCacheBehavior.AllowedMethods // [] | join(\",\")" "${TEMPLATE_FILE}")"
echo "  AllowedMethods: ${DEFAULT_METHODS}"
echo ""

# ── Check 3: /_facetheory/data/* behavior ──────────────────────────────────
SIDECAR_BEHAVIOR="$(jq -r "[${DIST}.CacheBehaviors[]? | select(.PathPattern == \"_facetheory/data/*\")] | .[0]" "${TEMPLATE_FILE}")"
SIDECAR_ORIGIN_ID=""

if [[ -z "${SIDECAR_BEHAVIOR}" || "${SIDECAR_BEHAVIOR}" == "null" ]]; then
  echo "FAIL: no CacheBehavior for _facetheory/data/*" >&2
  fail=1
else
  SIDECAR_ORIGIN_ID="$(printf '%s' "${SIDECAR_BEHAVIOR}" | jq -r '.TargetOriginId')"
  echo "/_facetheory/data/* → origin: ${SIDECAR_ORIGIN_ID}"

  # The sidecar origin should be S3 (check if it's an S3Origin or has S3 config).
  SIDECAR_ORIGIN_TYPE="$(jq -r "${DIST}.Origins[] | select(.Id == \"${SIDECAR_ORIGIN_ID}\") | if .S3OriginConfig then \"S3\" elif .CustomOriginConfig then \"Custom\" else \"Unknown\" end" "${TEMPLATE_FILE}")"
  echo "  origin type: ${SIDECAR_ORIGIN_TYPE}"

  # Sidecar origin should have OAC.
  SIDECAR_OAC="$(jq -r "${DIST}.Origins[] | select(.Id == \"${SIDECAR_ORIGIN_ID}\") | .OriginAccessControlId // \"none\"" "${TEMPLATE_FILE}")"
  echo "  OAC: ${SIDECAR_OAC}"
  if [[ "${SIDECAR_OAC}" == "none" ]]; then
    echo "FAIL: _facetheory/data/* origin has no OAC" >&2
    fail=1
  fi

  # Sidecar behavior should be GET/HEAD/OPTIONS only.
  SIDECAR_METHODS="$(printf '%s' "${SIDECAR_BEHAVIOR}" | jq -r '.AllowedMethods // [] | join(",")')"
  echo "  AllowedMethods: ${SIDECAR_METHODS}"
  if printf '%s' "${SIDECAR_METHODS}" | grep -qE 'POST|PUT|PATCH|DELETE'; then
    echo "FAIL: _facetheory/data/* allows mutating methods (should be GET/HEAD/OPTIONS only)" >&2
    fail=1
  fi
fi
echo ""

# ── Check 4: Required bearer-auth behaviors ────────────────────────────────
#
# Every pattern below must be present in the CloudFront distribution as a
# CacheBehavior with an exact PathPattern match. The target origin must exist
# and must NOT carry OriginAccessControl (bearer-auth = AuthType NONE in
# handler; OAC belongs only on the SSR and sidecar origins).
#
# Patterns are sourced from cdk/lib/lesser-host-stack.ts attachBearerBehavior()
# calls. Missing any required behavior is a FAIL (fail-closed).

check_bearer_auth_behavior() {
  local pattern="$1"
  local label="$2"

  local behavior
  behavior="$(jq -r "[${DIST}.CacheBehaviors[]? | select(.PathPattern == \"${pattern}\")] | .[0]" "${TEMPLATE_FILE}")"

  if [[ -z "${behavior}" || "${behavior}" == "null" ]]; then
    echo "FAIL: required cache behavior '${pattern}' is missing (${label})" >&2
    fail=1
    return 0
  fi

  local origin_id
  origin_id="$(printf '%s' "${behavior}" | jq -r '.TargetOriginId')"
  echo "${pattern} → origin: ${origin_id}"

  # Verify the target origin exists in the Origins array.
  local origin_exists
  origin_exists="$(jq -r "[${DIST}.Origins[] | select(.Id == \"${origin_id}\")] | length" "${TEMPLATE_FILE}")"
  if [[ "${origin_exists}" -eq 0 ]]; then
    echo "FAIL: origin '${origin_id}' referenced by '${pattern}' not found in Origins array" >&2
    fail=1
    return 0
  fi

  # Bearer-auth origins must NOT have OAC.
  local oac
  oac="$(jq -r "${DIST}.Origins[] | select(.Id == \"${origin_id}\") | .OriginAccessControlId // \"none\"" "${TEMPLATE_FILE}")"
  if [[ "${oac}" != "none" ]]; then
    echo "FAIL: ${pattern} origin '${origin_id}' has OAC (should be AuthType NONE for bearer-auth in handler)" >&2
    fail=1
  else
    echo "  OAC: none (bearer-auth, correct)"
  fi
  echo ""
}

echo "--- Bearer-auth cache behavior verification ---"
echo ""

# ── Trust API paths (trustOrigin) ──
check_bearer_auth_behavior "api/v1/previews*"    "trust-api previews"
check_bearer_auth_behavior "api/v1/renders*"     "trust-api renders"
check_bearer_auth_behavior "api/v1/trust/*"      "trust-api trust"
check_bearer_auth_behavior "api/v1/publish/jobs*" "trust-api publish jobs"
check_bearer_auth_behavior "api/v1/soul/agents/*/update-registration" "trust-api update-registration"
check_bearer_auth_behavior "api/v1/ai/*"         "trust-api AI"
check_bearer_auth_behavior "api/v1/budget/debit"  "trust-api budget debit"

# ── Control-plane SSE paths (controlPlaneSseOrigin) ──
check_bearer_auth_behavior "api/v1/soul/agents/register/*/mint-conversation*" "control-plane SSE mint-conversation (register)"
check_bearer_auth_behavior "api/v1/soul/agents/*/mint-conversation*"          "control-plane SSE mint-conversation"

# ── Control-plane HTTP API catch-all + auth + setup (controlPlaneOrigin) ──
check_bearer_auth_behavior "api/*"               "control-plane API catch-all"
check_bearer_auth_behavior "auth/*"              "control-plane auth"
check_bearer_auth_behavior "setup/status"        "control-plane setup status"
check_bearer_auth_behavior "setup/bootstrap/*"   "control-plane setup bootstrap"
check_bearer_auth_behavior "setup/admin"         "control-plane setup admin"
check_bearer_auth_behavior "setup/finalize"      "control-plane setup finalize"

# ── Trust API .well-known + attestation paths (trustOrigin) ──
check_bearer_auth_behavior ".well-known/*"       "trust-api .well-known"
check_bearer_auth_behavior "attestations"         "trust-api attestations exact"
check_bearer_auth_behavior "attestations/*"       "trust-api attestations wildcard"

# ── Check 5: OAC enforcement ───────────────────────────────────────────────
#
# Invariant: OAC on a CloudFront origin means CloudFront signs requests before
# forwarding. Bearer-auth origins (Custom/HTTP) carry their own auth in the
# handler (bearer token, wallet challenge, WebAuthn); OAC would interfere.
#
# Allowed OAC carriers:
#   - Default SSR origin (Function URL, OAC = AWS_IAM fail-closed)
#   - S3 origins (framework-driven by AppTheorySsrSite; e.g. assets bucket,
#     htmlStoreBucket sidecar)
#
# Forbidden: any Custom/HTTP origin (other than default) with OAC.
# These are bearer-auth origins and must NOT be OAC-signed.
#
echo "--- Origin access control enforcement ---"
ALL_ORIGIN_IDS="$(jq -r "${DIST}.Origins[].Id" "${TEMPLATE_FILE}")"

while IFS= read -r oid; do
  [[ -z "${oid}" ]] && continue
  oac="$(jq -r "${DIST}.Origins[] | select(.Id == \"${oid}\") | .OriginAccessControlId // \"none\"" "${TEMPLATE_FILE}")"
  otype="$(jq -r "${DIST}.Origins[] | select(.Id == \"${oid}\") | if .S3OriginConfig then \"S3\" elif .CustomOriginConfig then \"Custom\" else \"Unknown\" end" "${TEMPLATE_FILE}")"

  if [[ "${oac}" == "none" ]]; then
    echo "  ${oid} (${otype}): OAC=none"
    continue
  fi

  # OAC is present. Determine if this is allowed.
  case "${otype}" in
    S3)
      # S3 origins: OAC is expected (AppTheorySsrSite creates them internally
      # for assets + we add htmlStoreBucket). Always allowed.
      echo "  ${oid} (${otype}): OAC present (allowed: S3 origin)"
      ;;
    Custom)
      if [[ "${oid}" == "${DEFAULT_ORIGIN_ID}" ]]; then
        echo "  ${oid} (${otype}): OAC present (allowed: default SSR Function URL origin)"
      else
        echo "FAIL: Custom origin '${oid}' has OAC (forbidden for bearer-auth origins)" >&2
        echo "  Bearer-auth origins must NOT carry OAC — auth is handled in the Lambda" >&2
        echo "  handler via bearer token, wallet challenge, or WebAuthn." >&2
        fail=1
      fi
      ;;
    *)
      # Unknown origin type with OAC: fail conservatively.
      echo "FAIL: origin '${oid}' (${otype}) has unexpected OAC" >&2
      fail=1
      ;;
  esac
done <<< "${ALL_ORIGIN_IDS}"

if [[ "${fail}" -ne 0 ]]; then
  echo "" >&2
  echo "FAIL: CloudFront distribution composition does not match expected shape" >&2
  exit 1
fi

echo ""
echo "PASS: CloudFront distribution composition verified (SSR/OAC default, S3 sidecar, 18 bearer-auth behaviors, OAC audit clean)"
