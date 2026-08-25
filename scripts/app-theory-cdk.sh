#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: app-theory-cdk.sh <up|down> <lab|live>" >&2
}

if [ "$#" -ne 2 ]; then
  usage
  exit 2
fi

action="$1"
stage="$2"

case "$action" in
  up|down) ;;
  *)
    usage
    exit 2
    ;;
esac

case "$stage" in
  lab|live) ;;
  *)
    echo "invalid stage: $stage" >&2
    exit 2
    ;;
esac

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd "$script_dir/.." && pwd -P)"
cdk_dir="$repo_root/cdk"

bootstrap_wallet_helper() {
  (cd "$repo_root" && go run ./scripts/bootstrap-wallet "$@")
}

resolve_bootstrap_wallet_override() {
  local configured_address="${BOOTSTRAP_WALLET_ADDRESS:-}"
  local normalized_address

  if [ -z "$configured_address" ]; then
    echo "bootstrap wallet: CDK custom resource will generate/store the one-time setup wallet during stack creation" >&2
    printf '\n'
    return 0
  fi

  if ! normalized_address="$(bootstrap_wallet_helper normalize-address "$configured_address")"; then
    echo "invalid BOOTSTRAP_WALLET_ADDRESS override: expected a real EVM 0x address; placeholders are not accepted" >&2
    exit 2
  fi
  echo "bootstrap wallet: using BOOTSTRAP_WALLET_ADDRESS emergency override ($normalized_address); CDK will not manage an SSM private key for this deploy" >&2
  printf '%s\n' "$normalized_address"
}

"$repo_root/scripts/validate-deploy-provenance.sh" "$repo_root"

aws_profile="${AWS_PROFILE:-}"
case "$aws_profile" in
  ""|*[!A-Za-z0-9_+=,.@-]*)
    echo "invalid AWS profile" >&2
    exit 2
    ;;
esac

if [ -z "${AWS_REGION:-}" ]; then
  AWS_REGION="$(aws configure get region 2>/dev/null || true)"
fi

if [ -n "${AWS_REGION:-}" ]; then
  case "$AWS_REGION" in
    *[!A-Za-z0-9-]*)
      echo "invalid AWS region" >&2
      exit 2
      ;;
  esac
  export AWS_REGION
fi

if [ -z "${AWS_DEFAULT_REGION:-}" ] && [ -n "${AWS_REGION:-}" ]; then
  export AWS_DEFAULT_REGION="$AWS_REGION"
fi

# CDK's Node credential provider can fail to load an SSO profile even when the
# AWS CLI can still vend cached role credentials for it. Export short-lived
# credentials from the already-validated profile inside the deploy wrapper so
# the AppTheory deploy contract remains the only deploy path. Values are sourced
# into the process environment and never echoed.
if command -v aws >/dev/null 2>&1; then
  cdk_credential_env="$(mktemp "${TMPDIR:-/tmp}/lesser-host-cdk-creds.XXXXXX")"
  if aws configure export-credentials --profile "$aws_profile" --format env-no-export >"$cdk_credential_env" 2>/dev/null; then
    set -a
    # shellcheck disable=SC1090
    . "$cdk_credential_env"
    set +a
  fi
  rm -f "$cdk_credential_env"
fi

cd "$cdk_dir"

npm ci
npm run build

cdk_context_args=(-c "stage=$stage")
bootstrap_wallet_address="$(resolve_bootstrap_wallet_override)"
if [ -n "$bootstrap_wallet_address" ]; then
  cdk_context_args+=(-c "bootstrapWalletAddress=$bootstrap_wallet_address")
fi

case "$action" in
  up)
    synth_out="$(mktemp -d "${TMPDIR:-/tmp}/lesser-host-cdk-synth.XXXXXX")"
    cleanup() {
      rm -rf "$synth_out"
    }
    trap cleanup EXIT

    ./node_modules/.bin/cdk synth "${cdk_context_args[@]}" --output "$synth_out" --quiet
    template_path="$synth_out/lesser-host-$stage.template.json"
    if [ ! -f "$template_path" ]; then
      template_count="$(find "$synth_out" -maxdepth 1 -type f -name '*.template.json' | wc -l | tr -d ' ')"
      if [ "$template_count" = "1" ]; then
        template_path="$(find "$synth_out" -maxdepth 1 -type f -name '*.template.json' -print -quit)"
      else
        echo "expected one synthesized template for stage $stage in $synth_out" >&2
        exit 1
      fi
    fi
    "$repo_root/scripts/validate-hosted-genesis-template.mjs" "$template_path"
    "$repo_root/scripts/validate-deploy-template-placeholders.mjs" "$template_path"
    "$repo_root/scripts/validate-live-domain-template.mjs" "$stage" "$template_path"
    "$repo_root/scripts/validate-cfn-dependency-cycles.mjs" "$template_path"

    if [ "${LESSER_HOST_CDK_DRY_RUN:-}" = "1" ]; then
      echo "dry run: would deploy validated cloud assembly $synth_out for stage $stage" >&2
      exit 0
    fi

    # Issue #1052: prune hosted-genesis microvm image versions BEFORE the
    # deploy publishes a new one. AWS::Lambda::MicrovmImage hard-caps an image
    # at 50 versions; the tool keeps the newest N (default 5) of this stage's
    # image and deletes the rest serially, failing closed if the image is
    # still at/over the cap (the deploy would otherwise 402). This runs on
    # every real deploy through the AppTheory contract; dry-run skips it.
    (cd "$repo_root" && go run ./scripts/hosted-genesis-microvm-prune prune "$stage")

    ./node_modules/.bin/cdk deploy --app "$synth_out" --all --require-approval never
    ;;
  down)
    if [ "${LESSER_HOST_CDK_DRY_RUN:-}" = "1" ]; then
      echo "dry run: would destroy stage $stage from $cdk_dir" >&2
      exit 0
    fi

    ./node_modules/.bin/cdk destroy --all "${cdk_context_args[@]}" --force
    ;;
esac
