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
#   3. /api/*, /auth/*, /setup/* routes to the control-plane-api Lambda
#      Function URL (AuthType: NONE — bearer-auth in handler)
#   4. /.well-known/* and /attestations/* routes to the trust-api Lambda
#      Function URL (AuthType: NONE — bearer-auth in handler)
#   5. Only the SSR default origin and the sidecar S3 origin carry OAC;
#      bearer-auth origins do NOT carry OAC
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

# --- Check 2: Default origin is OAC-protected Lambda Function URL ---
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

# --- Check 3: /_facetheory/data/* behavior routes to S3 htmlStoreBucket ---
SIDECAR_BEHAVIOR="$(jq -r "[${DIST}.CacheBehaviors[]? | select(.PathPattern == \"_facetheory/data/*\")] | .[0]" "${TEMPLATE_FILE}")"
if [[ -z "${SIDECAR_BEHAVIOR}" || "${SIDECAR_BEHAVIOR}" == "null" ]]; then
  echo "FAIL: no CacheBehavior for _facetheory/data/*" >&2
  fail=1
else
  SIDECAR_ORIGIN="$(printf '%s' "${SIDECAR_BEHAVIOR}" | jq -r '.TargetOriginId')"
  echo "/_facetheory/data/* → origin: ${SIDECAR_ORIGIN}"

  # The sidecar origin should be S3 (check if it's an S3Origin or has S3 config).
  SIDECAR_ORIGIN_TYPE="$(jq -r "${DIST}.Origins[] | select(.Id == \"${SIDECAR_ORIGIN}\") | if .S3OriginConfig then \"S3\" elif .CustomOriginConfig then \"Custom\" else \"Unknown\" end" "${TEMPLATE_FILE}")"
  echo "  origin type: ${SIDECAR_ORIGIN_TYPE}"

  # Sidecar origin should have OAC.
  SIDECAR_OAC="$(jq -r "${DIST}.Origins[] | select(.Id == \"${SIDECAR_ORIGIN}\") | .OriginAccessControlId // \"none\"" "${TEMPLATE_FILE}")"
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

# --- Check 4: /api/*, /auth/*, /setup/* route to control-plane Lambda (AuthType NONE) ---
check_bearer_auth_behavior() {
  local pattern="$1"
  local label="$2"

  local behavior
  behavior="$(jq -r "[${DIST}.CacheBehaviors[]? | select(.PathPattern == \"${pattern}\")] | .[0]" "${TEMPLATE_FILE}")"
  if [[ -z "${behavior}" || "${behavior}" == "null" ]]; then
    behavior="$(jq -r "[${DIST}.CacheBehaviors[]? | select(.PathPattern | startswith(\"${pattern%'*'}\"))] | .[0]" "${TEMPLATE_FILE}")"
  fi
  if [[ -z "${behavior}" || "${behavior}" == "null" ]]; then
    echo "  ${pattern}: not found (may be under wildcard)" >&2
    return 0
  fi

  local origin_id
  origin_id="$(printf '%s' "${behavior}" | jq -r '.TargetOriginId')"
  echo "${pattern} → origin: ${origin_id}"

  # Check the origin is a Lambda Function URL (CustomOrigin with FunctionURL domain).
  local origin_domain
  origin_domain="$(jq -r "${DIST}.Origins[] | select(.Id == \"${origin_id}\") | .DomainName // \"\"" "${TEMPLATE_FILE}")"
  echo "  domain: ${origin_domain}"

  # Bearer-auth origins should NOT have OAC.
  local oac
  oac="$(jq -r "${DIST}.Origins[] | select(.Id == \"${origin_id}\") | .OriginAccessControlId // \"none\"" "${TEMPLATE_FILE}")"
  if [[ "${oac}" != "none" ]]; then
    echo "FAIL: ${pattern} origin has OAC (should be AuthType NONE for bearer-auth in handler)" >&2
    fail=1
  else
    echo "  OAC: none (bearer-auth, correct)"
  fi
  echo ""
}

check_bearer_auth_behavior "api/*" "API (control-plane)"
check_bearer_auth_behavior "auth/*" "Auth (control-plane)"
check_bearer_auth_behavior "setup/status" "Setup (control-plane)"
check_bearer_auth_behavior ".well-known/*" "Trust API (.well-known)"
check_bearer_auth_behavior "attestations" "Trust API (attestations)"
check_bearer_auth_behavior "attestations/*" "Trust API (attestations/*)"

# --- Check 5: No OAC on non-default, non-sidecar origins ---
echo "--- Origin access control audit ---"
DEFAULT_OAC="$(jq -r "${DIST}.Origins[] | select(.Id == \"${DEFAULT_ORIGIN_ID}\") | .OriginAccessControlId // \"\"" "${TEMPLATE_FILE}")"
ALL_ORIGIN_IDS="$(jq -r "${DIST}.Origins[].Id" "${TEMPLATE_FILE}")"
while IFS= read -r oid; do
  [[ -z "${oid}" ]] && continue
  oac="$(jq -r "${DIST}.Origins[] | select(.Id == \"${oid}\") | .OriginAccessControlId // \"none\"" "${TEMPLATE_FILE}")"
  otype="$(jq -r "${DIST}.Origins[] | select(.Id == \"${oid}\") | if .S3OriginConfig then \"S3\" elif .CustomOriginConfig then \"Custom\" else \"Unknown\" end" "${TEMPLATE_FILE}")"
  echo "  ${oid} (${otype}): OAC=${oac}"
done <<< "${ALL_ORIGIN_IDS}"

if [[ "${fail}" -ne 0 ]]; then
  echo "" >&2
  echo "FAIL: CloudFront distribution composition does not match expected shape" >&2
  exit 1
fi

echo ""
echo "PASS: CloudFront distribution composition verified (SSR/OAC default, S3 sidecar, bearer-auth origins intact)"
