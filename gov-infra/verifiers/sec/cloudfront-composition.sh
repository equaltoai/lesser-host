#!/usr/bin/env bash
# SEC-8: CloudFront distribution composition.
#
# Synthesizes the CDK stack and parses the CloudFormation template to verify
# the CloudFront distribution composition shape:
#
#   1. The SSR Lambda Function URL is the OAC-protected default origin
#      (AuthType: AWS_IAM, origin access control present).
#   2. /_facetheory/data/* routes to the S3 htmlStoreBucket (OAC-protected,
#      GET/HEAD/OPTIONS only).
#   3. Every required bearer-auth cache behavior is present with exact
#      PathPattern matching (no fuzzy fallback), routes to its intended named
#      origin (trust API, control-plane HTTP API, or control-plane SSE for legacy UI streaming only), and
#      the target origin must NOT carry OAC. Missing or misrouted → FAIL.
#   4. S3 origins (framework-driven by AppTheorySsrSite + our htmlStoreBucket
#      sidecar) and the default SSR Function URL origin may carry OAC.
#      Custom/HTTP bearer-auth origins must NOT carry OAC (auth is in the
#      Lambda handler). Any Custom origin other than default with OAC → FAIL.
#   5. CloudFront WAF keeps the AWS managed no-User-Agent protection for every
#      route except exact /resolve, where ENS CCIP-Read clients are permitted
#      to omit User-Agent. This is implemented by counting the managed
#      NoUserAgent_HEADER rule, then blocking its managed label outside /resolve.
#
# Pass: all composition invariants hold. Fail: any drift detected.
# No partial credit.
#
# Source: docs/governance-rubric-web-ui-rework-2026-05-24.md SEC-8
# Issue: equaltoai/lesser-host#400; strengthened for LH-12 / #589
#
# Test hook: set SEC8_TEMPLATE_FILE to a synthesized or fixture template to
# skip CDK synth and analyze that template directly. Normal CI/rubric runs do
# not set this and therefore validate the current synthesized CDK output.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CDK_DIR="${REPO_ROOT}/cdk"

# Role markers are intentionally source-derived logical-ID substrings in the
# synthesized template. CDK-generated Origin IDs are opaque, so SEC-8 resolves
# the intended named origins from their resource references, then pins every
# required behavior to those resolved origin IDs.
TRUST_ORIGIN_MARKER="TrustHttpApi"
CONTROL_PLANE_ORIGIN_MARKER="ControlPlaneHttpApi"
CONTROL_PLANE_SSE_ORIGIN_MARKER="ControlPlaneSseRestApi"

# Patterns are sourced from cdk/lib/lesser-host-stack.ts attachBearerBehavior()
# calls. Missing any required behavior is a FAIL (fail-closed). The third field
# is the intended named origin role for route-pinning.
REQUIRED_BEARER_BEHAVIORS=$'resolve*|trust|trust-api resolver\nhealth*|trust|trust-api health\napi/v1/previews*|trust|trust-api previews\napi/v1/renders*|trust|trust-api renders\napi/v1/trust/*|trust|trust-api trust\napi/v1/publish/jobs*|trust|trust-api publish jobs\napi/v1/soul/agents/*/update-registration|trust|trust-api update-registration\napi/v1/ai/*|trust|trust-api AI\napi/v1/budget/debit|trust|trust-api budget debit\napi/v1/soul/agents/register/*/mint-conversation*|sse|control-plane SSE mint-conversation (register)\napi/v1/soul/agents/*/mint-conversation*|sse|control-plane SSE mint-conversation\napi/v1/soul/instance/agents/register/*/mint-conversation*|control-plane|control-plane HostedGenesisSession JSON mint-conversation (instance register)\napi/*|control-plane|control-plane API catch-all\nauth/*|control-plane|control-plane auth\nwebhooks/*|control-plane|control-plane webhooks\nsetup/status|control-plane|control-plane setup status\nsetup/bootstrap/*|control-plane|control-plane setup bootstrap\nsetup/admin|control-plane|control-plane setup admin\nsetup/finalize|control-plane|control-plane setup finalize\n.well-known/*|trust|trust-api .well-known\nattestations|trust|trust-api attestations exact\nattestations/*|trust|trust-api attestations wildcard'
REQUIRED_BEARER_BEHAVIOR_COUNT=22
NO_USER_AGENT_MANAGED_RULE="NoUserAgent_HEADER"
NO_USER_AGENT_MANAGED_LABEL="awswaf:managed:aws:core-rule-set:NoUserAgent_Header"
NO_USER_AGENT_EXCEPTION_RULE="BlockNoUserAgentExceptResolve"

TEMPLATE_FILE="${SEC8_TEMPLATE_FILE:-}"

printf 'SEC-8: CloudFront distribution composition\n'

if [[ -n "${TEMPLATE_FILE}" ]]; then
  if [[ ! -f "${TEMPLATE_FILE}" ]]; then
    echo "BLOCKED: SEC8_TEMPLATE_FILE does not exist: ${TEMPLATE_FILE}" >&2
    exit 2
  fi
  echo "Using provided CloudFormation template: ${TEMPLATE_FILE#"${REPO_ROOT}"/}"
else
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

  if [[ ${ec} -ne 0 ]]; then
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
fi

# We need jq for parsing the CloudFormation template.
if ! command -v jq >/dev/null 2>&1; then
  echo "BLOCKED: jq is required to parse the synthesized CloudFormation template" >&2
  exit 2
fi

echo "Analyzing template: ${TEMPLATE_FILE#"${REPO_ROOT}"/}"
echo ""

fail=0

jq_dist() {
  local filter="$1"
  jq -r --arg dist "${DISTRIBUTION_ID}" "${filter}" "${TEMPLATE_FILE}"
}

origin_field() {
  local origin_id="$1"
  local filter="$2"
  jq -r \
    --arg dist "${DISTRIBUTION_ID}" \
    --arg oid "${origin_id}" \
    ".Resources[\$dist].Properties.DistributionConfig.Origins[]? | select(.Id == \$oid) | ${filter}" \
    "${TEMPLATE_FILE}"
}

resolve_origin_by_marker() {
  local role="$1"
  local marker="$2"
  local -a ids=()

  mapfile -t ids < <(
    jq -r \
      --arg dist "${DISTRIBUTION_ID}" \
      --arg marker "${marker}" \
      '.Resources[$dist].Properties.DistributionConfig.Origins[]?
       | select(.CustomOriginConfig != null)
       | select((.DomainName | tojson) | contains($marker))
       | .Id' \
      "${TEMPLATE_FILE}" | sort -u
  )

  if [[ ${#ids[@]} -eq 0 ]]; then
    echo "FAIL: could not resolve ${role} origin from marker '${marker}'" >&2
    return 1
  fi

  if [[ ${#ids[@]} -gt 1 ]]; then
    echo "FAIL: marker '${marker}' resolved multiple ${role} origins: ${ids[*]}" >&2
    return 1
  fi

  printf '%s' "${ids[0]}"
}

expected_origin_for_role() {
  local role="$1"
  case "${role}" in
    trust)
      printf '%s' "${TRUST_ORIGIN_ID:-}"
      ;;
    control-plane)
      printf '%s' "${CONTROL_PLANE_ORIGIN_ID:-}"
      ;;
    sse)
      printf '%s' "${CONTROL_PLANE_SSE_ORIGIN_ID:-}"
      ;;
    *)
      echo "FAIL: internal verifier error: unknown origin role '${role}'" >&2
      return 1
      ;;
  esac
}

# --- Check 1: CloudFront distribution exists ---
mapfile -t DISTRIBUTION_IDS < <(
  jq -r '.Resources | to_entries[] | select(.value.Type == "AWS::CloudFront::Distribution") | .key' "${TEMPLATE_FILE}" | sort -u
)

if [[ ${#DISTRIBUTION_IDS[@]} -eq 0 ]]; then
  echo "FAIL: no CloudFront distribution found in synthesized template" >&2
  exit 1
fi

if [[ ${#DISTRIBUTION_IDS[@]} -gt 1 ]]; then
  echo "FAIL: expected exactly one CloudFront distribution, found ${#DISTRIBUTION_IDS[@]}: ${DISTRIBUTION_IDS[*]}" >&2
  exit 1
fi

DISTRIBUTION_ID="${DISTRIBUTION_IDS[0]}"
echo "CloudFront distribution: ${DISTRIBUTION_ID}"

# ── Check 1b: WAF no-User-Agent route exception is exact ───────────────────
#
# AWSManagedRulesCommonRuleSet blocks requests that are missing User-Agent.
# ENS CCIP-Read clients are not browsers, and the default Node ethers CCIP
# fetch path may omit User-Agent. The exception must be route-scoped: count the
# managed no-UA rule so it labels the request, then block that label everywhere
# except exact /resolve.
mapfile -t WEB_ACL_IDS < <(
  jq -r '.Resources | to_entries[] | select(.value.Type == "AWS::WAFv2::WebACL") | .key' "${TEMPLATE_FILE}" | sort -u
)

if [[ ${#WEB_ACL_IDS[@]} -eq 0 ]]; then
  echo "FAIL: no WAFv2 WebACL found in synthesized template" >&2
  fail=1
elif [[ ${#WEB_ACL_IDS[@]} -gt 1 ]]; then
  echo "FAIL: expected exactly one WAFv2 WebACL, found ${#WEB_ACL_IDS[@]}: ${WEB_ACL_IDS[*]}" >&2
  fail=1
else
  WEB_ACL_ID="${WEB_ACL_IDS[0]}"
  echo "WAF WebACL: ${WEB_ACL_ID}"

  COMMON_RULE_COUNT="$(
    jq -r \
      --arg webacl "${WEB_ACL_ID}" \
      '[.Resources[$webacl].Properties.Rules[]? | select(.Name == "AWSManagedRulesCommonRuleSet")] | length' \
      "${TEMPLATE_FILE}"
  )"
  if [[ "${COMMON_RULE_COUNT}" -ne 1 ]]; then
    echo "FAIL: expected exactly one AWSManagedRulesCommonRuleSet rule, found ${COMMON_RULE_COUNT}" >&2
    fail=1
  fi

  NO_UA_OVERRIDE_COUNT="$(
    jq -r \
      --arg webacl "${WEB_ACL_ID}" \
      --arg rule "${NO_USER_AGENT_MANAGED_RULE}" \
      '[.Resources[$webacl].Properties.Rules[]?
        | select(.Name == "AWSManagedRulesCommonRuleSet")
        | .Statement.ManagedRuleGroupStatement.RuleActionOverrides[]?
        | select(.Name == $rule and (.ActionToUse.Count != null))] | length' \
      "${TEMPLATE_FILE}"
  )"
  if [[ "${NO_UA_OVERRIDE_COUNT}" -ne 1 ]]; then
    echo "FAIL: ${NO_USER_AGENT_MANAGED_RULE} must be overridden to Count exactly once" >&2
    fail=1
  else
    echo "  ${NO_USER_AGENT_MANAGED_RULE}: Count override present"
  fi

  NO_UA_BLOCK_RULE_COUNT="$(
    jq -r \
      --arg webacl "${WEB_ACL_ID}" \
      --arg rule "${NO_USER_AGENT_EXCEPTION_RULE}" \
      '[.Resources[$webacl].Properties.Rules[]? | select(.Name == $rule)] | length' \
      "${TEMPLATE_FILE}"
  )"
  if [[ "${NO_UA_BLOCK_RULE_COUNT}" -ne 1 ]]; then
    echo "FAIL: expected exactly one ${NO_USER_AGENT_EXCEPTION_RULE} rule, found ${NO_UA_BLOCK_RULE_COUNT}" >&2
    fail=1
  else
    NO_UA_BLOCK_RULE_OK="$(
      jq -r \
        --arg webacl "${WEB_ACL_ID}" \
        --arg rule "${NO_USER_AGENT_EXCEPTION_RULE}" \
        '.Resources[$webacl].Properties.Rules[]?
         | select(.Name == $rule)
         | if (.Priority == 1 and (.Action.Block != null)) then "yes" else "no" end' \
        "${TEMPLATE_FILE}"
    )"
    if [[ "${NO_UA_BLOCK_RULE_OK}" != "yes" ]]; then
      echo "FAIL: ${NO_USER_AGENT_EXCEPTION_RULE} must have Priority=1 and Action=Block" >&2
      fail=1
    fi

    NO_UA_AND_STATEMENT_COUNT="$(
      jq -r \
        --arg webacl "${WEB_ACL_ID}" \
        --arg rule "${NO_USER_AGENT_EXCEPTION_RULE}" \
        '.Resources[$webacl].Properties.Rules[]?
         | select(.Name == $rule)
         | (.Statement.AndStatement.Statements // []) | length' \
        "${TEMPLATE_FILE}"
    )"
    if [[ "${NO_UA_AND_STATEMENT_COUNT}" -ne 2 ]]; then
      echo "FAIL: ${NO_USER_AGENT_EXCEPTION_RULE} must have exactly two AND predicates (managed label + path exception), found ${NO_UA_AND_STATEMENT_COUNT}" >&2
      fail=1
    fi

    NO_UA_LABEL_PREDICATE_COUNT="$(
      jq -r \
        --arg webacl "${WEB_ACL_ID}" \
        --arg rule "${NO_USER_AGENT_EXCEPTION_RULE}" \
        --arg label "${NO_USER_AGENT_MANAGED_LABEL}" \
        '[.Resources[$webacl].Properties.Rules[]?
          | select(.Name == $rule)
          | .Statement.AndStatement.Statements[]?
          | select(.LabelMatchStatement.Scope == "LABEL" and .LabelMatchStatement.Key == $label)] | length' \
        "${TEMPLATE_FILE}"
    )"
    if [[ "${NO_UA_LABEL_PREDICATE_COUNT}" -ne 1 ]]; then
      echo "FAIL: ${NO_USER_AGENT_EXCEPTION_RULE} must match managed label ${NO_USER_AGENT_MANAGED_LABEL}" >&2
      fail=1
    fi

    NO_UA_PATH_EXCEPTION_COUNT="$(
      jq -r \
        --arg webacl "${WEB_ACL_ID}" \
        --arg rule "${NO_USER_AGENT_EXCEPTION_RULE}" \
        '[.Resources[$webacl].Properties.Rules[]?
          | select(.Name == $rule)
          | .Statement.AndStatement.Statements[]?
          | .NotStatement.Statement.ByteMatchStatement?
          | select(
              (.FieldToMatch.UriPath != null)
              and .PositionalConstraint == "EXACTLY"
              and .SearchString == "/resolve"
              and (.TextTransformations == [{"Priority":0,"Type":"NONE"}])
            )] | length' \
        "${TEMPLATE_FILE}"
    )"
    if [[ "${NO_UA_PATH_EXCEPTION_COUNT}" -ne 1 ]]; then
      echo "FAIL: ${NO_USER_AGENT_EXCEPTION_RULE} must exempt only exact /resolve" >&2
      fail=1
    fi

    if [[ "${NO_UA_BLOCK_RULE_OK}" == "yes" && "${NO_UA_AND_STATEMENT_COUNT}" -eq 2 && "${NO_UA_LABEL_PREDICATE_COUNT}" -eq 1 && "${NO_UA_PATH_EXCEPTION_COUNT}" -eq 1 ]]; then
      echo "  ${NO_USER_AGENT_EXCEPTION_RULE}: blocks ${NO_USER_AGENT_MANAGED_LABEL} except exact /resolve"
    fi
  fi
fi
echo ""

# ── Check 2: Default origin is OAC-protected Lambda Function URL ──────────
# shellcheck disable=SC2016 # jq reads $dist from --arg in jq_dist().
DEFAULT_ORIGIN_ID="$(jq_dist '.Resources[$dist].Properties.DistributionConfig.DefaultCacheBehavior.TargetOriginId // empty')"
if [[ -z "${DEFAULT_ORIGIN_ID}" ]]; then
  echo "FAIL: default cache behavior has no TargetOriginId" >&2
  fail=1
else
  echo "Default origin ID: ${DEFAULT_ORIGIN_ID}"
fi

DEFAULT_ORIGIN_COUNT=0
if [[ -n "${DEFAULT_ORIGIN_ID}" ]]; then
  DEFAULT_ORIGIN_COUNT="$(jq -r --arg dist "${DISTRIBUTION_ID}" --arg oid "${DEFAULT_ORIGIN_ID}" '[.Resources[$dist].Properties.DistributionConfig.Origins[]? | select(.Id == $oid)] | length' "${TEMPLATE_FILE}")"
fi
if [[ "${DEFAULT_ORIGIN_COUNT}" -eq 0 ]]; then
  echo "FAIL: default origin '${DEFAULT_ORIGIN_ID}' not found in Origins array" >&2
  fail=1
fi

# Verify the default origin is a Custom origin (Lambda Function URL origin), not S3/unknown.
if [[ "${DEFAULT_ORIGIN_COUNT}" -ne 0 ]]; then
  DEFAULT_ORIGIN_TYPE="$(origin_field "${DEFAULT_ORIGIN_ID}" 'if .S3OriginConfig then "S3" elif .CustomOriginConfig then "Custom" else "Unknown" end')"
  echo "  Origin type: ${DEFAULT_ORIGIN_TYPE}"
  if [[ "${DEFAULT_ORIGIN_TYPE}" != "Custom" ]]; then
    echo "FAIL: default origin '${DEFAULT_ORIGIN_ID}' is ${DEFAULT_ORIGIN_TYPE}, expected Custom Lambda Function URL origin" >&2
    fail=1
  fi
fi

# Verify the default origin has an OriginAccessControlId (OAC).
OAC_COUNT=0
if [[ -n "${DEFAULT_ORIGIN_ID}" ]]; then
  OAC_COUNT="$(jq -r --arg dist "${DISTRIBUTION_ID}" --arg oid "${DEFAULT_ORIGIN_ID}" '[.Resources[$dist].Properties.DistributionConfig.Origins[]? | select(.Id == $oid) | select(.OriginAccessControlId != null)] | length' "${TEMPLATE_FILE}")"
fi
if [[ "${OAC_COUNT}" -eq 0 ]]; then
  echo "FAIL: default origin '${DEFAULT_ORIGIN_ID}' does not have OriginAccessControl (OAC required)" >&2
  fail=1
else
  echo "  OAC: present"
fi

# Verify the default origin's DomainName is backed by an AWS::Lambda::Url whose
# AuthType is AWS_IAM. OAC presence alone does not make a Function URL private;
# a Function URL with AuthType NONE remains public even if CloudFront signs its
# own origin requests.
if [[ -n "${DEFAULT_ORIGIN_ID}" ]]; then
  mapfile -t DEFAULT_LAMBDA_URL_IDS < <(
    jq -r \
      --arg dist "${DISTRIBUTION_ID}" \
      --arg oid "${DEFAULT_ORIGIN_ID}" \
      'def getatt_refs:
         .. | objects | select(has("Fn::GetAtt")) | ."Fn::GetAtt" as $g |
         if (($g | type) == "array" and ($g | length) >= 2) then
           { logicalId: ($g[0] | tostring), attr: ($g[1] | tostring) }
         elif (($g | type) == "string") then
           ($g | split(".") | select(length >= 2) | { logicalId: (.[0] | tostring), attr: (.[1] | tostring) })
         else
           empty
         end;
       .Resources[$dist].Properties.DistributionConfig.Origins[]?
       | select(.Id == $oid)
       | .DomainName
       | getatt_refs
       | select(.attr == "FunctionUrl")
       | .logicalId' \
      "${TEMPLATE_FILE}" | sort -u
  )

  if [[ ${#DEFAULT_LAMBDA_URL_IDS[@]} -eq 0 ]]; then
    echo "FAIL: default origin '${DEFAULT_ORIGIN_ID}' DomainName is not backed by AWS::Lambda::Url.FunctionUrl" >&2
    fail=1
  elif [[ ${#DEFAULT_LAMBDA_URL_IDS[@]} -gt 1 ]]; then
    echo "FAIL: default origin '${DEFAULT_ORIGIN_ID}' references multiple Lambda Function URLs: ${DEFAULT_LAMBDA_URL_IDS[*]}" >&2
    fail=1
  else
    DEFAULT_LAMBDA_URL_ID="${DEFAULT_LAMBDA_URL_IDS[0]}"
    DEFAULT_LAMBDA_URL_TYPE="$(jq -r --arg id "${DEFAULT_LAMBDA_URL_ID}" '.Resources[$id].Type // empty' "${TEMPLATE_FILE}")"
    DEFAULT_LAMBDA_URL_AUTH_TYPE="$(jq -r --arg id "${DEFAULT_LAMBDA_URL_ID}" '.Resources[$id].Properties.AuthType // empty' "${TEMPLATE_FILE}")"
    DEFAULT_LAMBDA_URL_INVOKE_MODE="$(jq -r --arg id "${DEFAULT_LAMBDA_URL_ID}" '.Resources[$id].Properties.InvokeMode // "none"' "${TEMPLATE_FILE}")"

    echo "  Lambda URL: ${DEFAULT_LAMBDA_URL_ID} (AuthType=${DEFAULT_LAMBDA_URL_AUTH_TYPE}, InvokeMode=${DEFAULT_LAMBDA_URL_INVOKE_MODE})"

    if [[ "${DEFAULT_LAMBDA_URL_TYPE}" != "AWS::Lambda::Url" ]]; then
      echo "FAIL: default origin FunctionUrl reference '${DEFAULT_LAMBDA_URL_ID}' is ${DEFAULT_LAMBDA_URL_TYPE}, expected AWS::Lambda::Url" >&2
      fail=1
    fi

    if [[ "${DEFAULT_LAMBDA_URL_AUTH_TYPE}" != "AWS_IAM" ]]; then
      echo "FAIL: default origin Lambda Function URL '${DEFAULT_LAMBDA_URL_ID}' AuthType '${DEFAULT_LAMBDA_URL_AUTH_TYPE}' is not AWS_IAM" >&2
      fail=1
    fi
  fi
fi

# Verify the default origin viewer protocol policy.
# shellcheck disable=SC2016 # jq reads $dist from --arg in jq_dist().
DEFAULT_VP="$(jq_dist '.Resources[$dist].Properties.DistributionConfig.DefaultCacheBehavior.ViewerProtocolPolicy // empty')"
echo "  ViewerProtocolPolicy: ${DEFAULT_VP}"

# Verify the default behavior allowed methods.
# shellcheck disable=SC2016 # jq reads $dist from --arg in jq_dist().
DEFAULT_METHODS="$(jq_dist '.Resources[$dist].Properties.DistributionConfig.DefaultCacheBehavior.AllowedMethods // [] | join(",")')"
echo "  AllowedMethods: ${DEFAULT_METHODS}"
echo ""

# ── Check 3: /_facetheory/data/* behavior ──────────────────────────────────
SIDECAR_BEHAVIOR="$(jq -c --arg dist "${DISTRIBUTION_ID}" '[.Resources[$dist].Properties.DistributionConfig.CacheBehaviors[]? | select(.PathPattern == "_facetheory/data/*")] | .[0] // empty' "${TEMPLATE_FILE}")"
SIDECAR_ORIGIN_ID=""

if [[ -z "${SIDECAR_BEHAVIOR}" || "${SIDECAR_BEHAVIOR}" == "null" ]]; then
  echo "FAIL: no CacheBehavior for _facetheory/data/*" >&2
  fail=1
else
  SIDECAR_ORIGIN_ID="$(printf '%s' "${SIDECAR_BEHAVIOR}" | jq -r '.TargetOriginId // empty')"
  echo "/_facetheory/data/* → origin: ${SIDECAR_ORIGIN_ID}"

  # The sidecar origin should be S3 (check if it's an S3Origin or has S3 config).
  SIDECAR_ORIGIN_TYPE="$(origin_field "${SIDECAR_ORIGIN_ID}" 'if .S3OriginConfig then "S3" elif .CustomOriginConfig then "Custom" else "Unknown" end')"
  echo "  origin type: ${SIDECAR_ORIGIN_TYPE}"
  if [[ "${SIDECAR_ORIGIN_TYPE}" != "S3" ]]; then
    echo "FAIL: _facetheory/data/* origin '${SIDECAR_ORIGIN_ID}' is ${SIDECAR_ORIGIN_TYPE}, expected S3" >&2
    fail=1
  fi

  # Sidecar origin should have OAC.
  SIDECAR_OAC="$(origin_field "${SIDECAR_ORIGIN_ID}" '.OriginAccessControlId // "none"')"
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
# CacheBehavior with an exact PathPattern match. The target origin must exist,
# must be the intended named origin, and must NOT carry OriginAccessControl
# (bearer-auth = AuthType NONE in handler; OAC belongs only on the SSR and
# sidecar origins).

TRUST_ORIGIN_ID=""
CONTROL_PLANE_ORIGIN_ID=""
CONTROL_PLANE_SSE_ORIGIN_ID=""

if TRUST_ORIGIN_ID="$(resolve_origin_by_marker "trust-api" "${TRUST_ORIGIN_MARKER}")"; then
  echo "Expected trust-api origin: ${TRUST_ORIGIN_ID}"
else
  fail=1
fi

if CONTROL_PLANE_ORIGIN_ID="$(resolve_origin_by_marker "control-plane HTTP API" "${CONTROL_PLANE_ORIGIN_MARKER}")"; then
  echo "Expected control-plane origin: ${CONTROL_PLANE_ORIGIN_ID}"
else
  fail=1
fi

if CONTROL_PLANE_SSE_ORIGIN_ID="$(resolve_origin_by_marker "control-plane SSE" "${CONTROL_PLANE_SSE_ORIGIN_MARKER}")"; then
  echo "Expected control-plane SSE origin: ${CONTROL_PLANE_SSE_ORIGIN_ID}"
else
  fail=1
fi

echo ""

check_bearer_auth_behavior() {
  local pattern="$1"
  local expected_role="$2"
  local label="$3"
  local expected_origin_id

  expected_origin_id="$(expected_origin_for_role "${expected_role}")"

  local behavior
  behavior="$(jq -c --arg dist "${DISTRIBUTION_ID}" --arg pattern "${pattern}" '[.Resources[$dist].Properties.DistributionConfig.CacheBehaviors[]? | select(.PathPattern == $pattern)] | .[0] // empty' "${TEMPLATE_FILE}")"

  if [[ -z "${behavior}" || "${behavior}" == "null" ]]; then
    echo "FAIL: required cache behavior '${pattern}' is missing (${label})" >&2
    fail=1
    return 0
  fi

  local origin_id
  origin_id="$(printf '%s' "${behavior}" | jq -r '.TargetOriginId // empty')"
  echo "${pattern} → origin: ${origin_id} (expected ${expected_role}: ${expected_origin_id:-unresolved})"

  # Verify the target origin exists in the Origins array.
  local origin_exists
  origin_exists="$(jq -r --arg dist "${DISTRIBUTION_ID}" --arg oid "${origin_id}" '[.Resources[$dist].Properties.DistributionConfig.Origins[]? | select(.Id == $oid)] | length' "${TEMPLATE_FILE}")"
  if [[ "${origin_exists}" -eq 0 ]]; then
    echo "FAIL: origin '${origin_id}' referenced by '${pattern}' not found in Origins array" >&2
    fail=1
    return 0
  fi

  # Pin each required behavior to its intended named origin. This catches
  # redirects to a different existing non-OAC bearer origin.
  if [[ -z "${expected_origin_id}" ]]; then
    echo "FAIL: cannot validate intended origin for '${pattern}' because role '${expected_role}' was not resolved" >&2
    fail=1
  elif [[ "${origin_id}" != "${expected_origin_id}" ]]; then
    echo "FAIL: ${pattern} routes to origin '${origin_id}', expected ${expected_role} origin '${expected_origin_id}' (${label})" >&2
    fail=1
  else
    echo "  intended origin: ${expected_role} (correct)"
  fi

  # Bearer-auth origins must NOT have OAC.
  local oac
  oac="$(origin_field "${origin_id}" '.OriginAccessControlId // "none"')"
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

while IFS='|' read -r pattern expected_role label; do
  [[ -z "${pattern}" ]] && continue
  check_bearer_auth_behavior "${pattern}" "${expected_role}" "${label}"
done <<< "${REQUIRED_BEARER_BEHAVIORS}"

# ── Check 5: OAC enforcement ───────────────────────────────────────────────
#
# Invariant: OAC on a CloudFront origin means CloudFront signs requests before
# forwarding. Bearer-auth origins (Custom/HTTP) carry their own auth in the
# handler (bearer token, wallet challenge, WebAuthn); OAC would interfere.
#
# Allowed OAC carriers:
#   - Default SSR origin (Function URL, AuthType AWS_IAM fail-closed)
#   - S3 origins (framework-driven by AppTheorySsrSite; e.g. assets bucket,
#     htmlStoreBucket sidecar)
#
# Forbidden: any Custom/HTTP origin (other than default) with OAC.
# These are bearer-auth origins and must NOT be OAC-signed.
#
echo "--- Origin access control enforcement ---"
mapfile -t ALL_ORIGIN_IDS < <(jq -r --arg dist "${DISTRIBUTION_ID}" '.Resources[$dist].Properties.DistributionConfig.Origins[].Id' "${TEMPLATE_FILE}")

for oid in "${ALL_ORIGIN_IDS[@]}"; do
  [[ -z "${oid}" ]] && continue
  oac="$(origin_field "${oid}" '.OriginAccessControlId // "none"')"
  otype="$(origin_field "${oid}" 'if .S3OriginConfig then "S3" elif .CustomOriginConfig then "Custom" else "Unknown" end')"

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
done

if [[ "${fail}" -ne 0 ]]; then
  echo "" >&2
  echo "FAIL: CloudFront distribution composition does not match expected shape" >&2
  exit 1
fi

echo ""
echo "PASS: CloudFront distribution composition verified (SSR Lambda URL AWS_IAM/OAC default, S3 sidecar, ${REQUIRED_BEARER_BEHAVIOR_COUNT} bearer-auth behaviors pinned to intended origins, instance hosted-genesis JSON route pinned to control-plane authority, WAF no-User-Agent exception scoped to exact /resolve, OAC audit clean)"
