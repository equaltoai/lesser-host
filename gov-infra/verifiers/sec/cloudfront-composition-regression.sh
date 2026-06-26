#!/usr/bin/env bash
# Regression harness for SEC-8 CloudFront composition verifier.
#
# These synthetic templates prove LH-12 fails closed for the residual unsafe
# paths called out in equaltoai/lesser-host#589:
#   (a) default-origin Lambda Function URL AuthType != AWS_IAM
#   (b) bearer behavior routed to the wrong existing non-OAC origin
#   (c) resolve*/health*/webhooks/* removed from required behavior coverage

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
VERIFIER="${SCRIPT_DIR}/cloudfront-composition.sh"
FIXTURE_DIR="${SCRIPT_DIR}/fixtures/cloudfront-composition"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

run_expect_pass() {
  local name="$1"
  local fixture="$2"
  local log="${TMP_DIR}/${name}.log"

  if SEC8_TEMPLATE_FILE="${fixture}" bash "${VERIFIER}" >"${log}" 2>&1; then
    echo "PASS: ${name} accepted"
  else
    echo "FAIL: ${name} should have passed" >&2
    cat "${log}" >&2
    exit 1
  fi
}

run_expect_fail() {
  local name="$1"
  local fixture="$2"
  local expected="$3"
  local log="${TMP_DIR}/${name}.log"

  if SEC8_TEMPLATE_FILE="${fixture}" bash "${VERIFIER}" >"${log}" 2>&1; then
    echo "FAIL: ${name} should have failed closed" >&2
    cat "${log}" >&2
    exit 1
  fi

  if ! grep -Fq "${expected}" "${log}"; then
    echo "FAIL: ${name} failed, but did not emit expected evidence: ${expected}" >&2
    cat "${log}" >&2
    exit 1
  fi

  echo "PASS: ${name} failed closed (${expected})"
}

cd "${REPO_ROOT}"

run_expect_pass \
  "fixture-pass-current-shape" \
  "${FIXTURE_DIR}/pass-current-shape.json"

run_expect_fail \
  "fixture-fail-default-lambda-url-auth-none" \
  "${FIXTURE_DIR}/fail-default-lambda-url-auth-none.json" \
  "AuthType 'NONE' is not AWS_IAM"

run_expect_fail \
  "fixture-fail-bearer-behavior-wrong-origin" \
  "${FIXTURE_DIR}/fail-bearer-behavior-wrong-origin.json" \
  "api/v1/trust/* routes to origin 'control-plane-origin', expected trust origin 'trust-origin'"

run_expect_fail \
  "fixture-fail-expanded-required-patterns-missing" \
  "${FIXTURE_DIR}/fail-expanded-required-patterns-missing.json" \
  "required cache behavior 'resolve*' is missing"

run_expect_fail \
  "fixture-fail-instance-json-authority-behavior-missing" \
  "${FIXTURE_DIR}/fail-instance-json-authority-behavior-missing.json" \
  "required cache behavior 'api/v1/soul/instance/agents/register/*/mint-conversation*' is missing"

run_expect_fail \
  "fixture-fail-instance-json-authority-wrong-origin" \
  "${FIXTURE_DIR}/fail-instance-json-authority-wrong-origin.json" \
  "api/v1/soul/instance/agents/register/*/mint-conversation* routes to origin 'sse-origin', expected control-plane origin 'control-plane-origin'"
