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
bootstrap_wallet_ssm_param="/lesser-host/$stage/setup/bootstrap-wallet-private-key"
tmp_files=()

cleanup_tmp_files() {
  if [ "${#tmp_files[@]}" -gt 0 ]; then
    rm -f "${tmp_files[@]}"
  fi
}
trap cleanup_tmp_files EXIT

make_secret_tmp_file() {
  local result_var="$1"
  local tmp_file
  tmp_file="$(mktemp "${TMPDIR:-/tmp}/lesser-host-bootstrap-wallet.XXXXXX")"
  chmod 600 "$tmp_file"
  tmp_files+=("$tmp_file")
  printf -v "$result_var" '%s' "$tmp_file"
}

bootstrap_wallet_helper() {
  (cd "$repo_root" && go run ./scripts/bootstrap-wallet "$@")
}

aws_ssm_get_bootstrap_wallet() {
  local output_file="$1"
  local error_file="$2"
  aws ssm get-parameter \
    --name "$bootstrap_wallet_ssm_param" \
    --with-decryption \
    --query 'Parameter.Value' \
    --output text >"$output_file" 2>"$error_file"
}

aws_ssm_put_bootstrap_wallet() {
  local payload_file="$1"
  local request_file
  make_secret_tmp_file request_file
  bootstrap_wallet_helper put-parameter-input \
    --name "$bootstrap_wallet_ssm_param" \
    --description "lesser-host $stage one-time setup bootstrap wallet private key" \
    <"$payload_file" >"$request_file"
  aws ssm put-parameter --cli-input-json "file://$request_file"
}

address_from_payload_file() {
  local payload_file="$1"
  bootstrap_wallet_helper address <"$payload_file"
}

resolve_bootstrap_wallet_address() {
  local configured_address="${BOOTSTRAP_WALLET_ADDRESS:-}"
  local normalized_address

  if [ -n "$configured_address" ]; then
    if ! normalized_address="$(bootstrap_wallet_helper normalize-address "$configured_address")"; then
      echo "invalid BOOTSTRAP_WALLET_ADDRESS: expected a real EVM 0x address; placeholders are not accepted" >&2
      exit 2
    fi
    echo "bootstrap wallet: using BOOTSTRAP_WALLET_ADDRESS override ($normalized_address)" >&2
    printf '%s\n' "$normalized_address"
    return 0
  fi

  if [ "${LESSER_HOST_CDK_DRY_RUN:-}" = "1" ] && [ "$action" = "down" ]; then
    echo "bootstrap wallet: dry-run destroy will not create credentials or read SSM" >&2
    printf '\n'
    return 0
  fi

  if [ "${LESSER_HOST_CDK_DRY_RUN:-}" = "1" ]; then
    local dry_payload_file
    make_secret_tmp_file dry_payload_file
    bootstrap_wallet_helper generate >"$dry_payload_file"
    normalized_address="$(address_from_payload_file "$dry_payload_file")"
    echo "bootstrap wallet: dry run generated non-persistent setup address ($normalized_address); no SSM read/write" >&2
    printf '%s\n' "$normalized_address"
    return 0
  fi

  local payload_file error_file
  make_secret_tmp_file payload_file
  make_secret_tmp_file error_file

  if aws_ssm_get_bootstrap_wallet "$payload_file" "$error_file"; then
    normalized_address="$(address_from_payload_file "$payload_file")"
    echo "bootstrap wallet: using SSM SecureString $bootstrap_wallet_ssm_param ($normalized_address)" >&2
    printf '%s\n' "$normalized_address"
    return 0
  fi

  if ! grep -q 'ParameterNotFound' "$error_file"; then
    echo "bootstrap wallet: failed reading SSM SecureString $bootstrap_wallet_ssm_param" >&2
    cat "$error_file" >&2
    exit 1
  fi

  if [ "$action" = "down" ]; then
    echo "bootstrap wallet: no BOOTSTRAP_WALLET_ADDRESS override and no SSM SecureString $bootstrap_wallet_ssm_param; destroy will not create credentials" >&2
    printf '\n'
    return 0
  fi

  bootstrap_wallet_helper generate >"$payload_file"
  normalized_address="$(address_from_payload_file "$payload_file")"
  if aws_ssm_put_bootstrap_wallet "$payload_file" > /dev/null 2>"$error_file"; then
    echo "bootstrap wallet: created SSM SecureString $bootstrap_wallet_ssm_param ($normalized_address)" >&2
    printf '%s\n' "$normalized_address"
    return 0
  fi

  if grep -q 'ParameterAlreadyExists' "$error_file"; then
    if aws_ssm_get_bootstrap_wallet "$payload_file" "$error_file"; then
      normalized_address="$(address_from_payload_file "$payload_file")"
      echo "bootstrap wallet: using concurrently-created SSM SecureString $bootstrap_wallet_ssm_param ($normalized_address)" >&2
      printf '%s\n' "$normalized_address"
      return 0
    fi
  fi

  echo "bootstrap wallet: failed creating SSM SecureString $bootstrap_wallet_ssm_param" >&2
  cat "$error_file" >&2
  exit 1
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

cd "$cdk_dir"

npm ci
npm run build

bootstrap_wallet_address="$(resolve_bootstrap_wallet_address)"
cdk_context_args=(-c "stage=$stage" -c "bootstrapWalletAddress=$bootstrap_wallet_address")

case "$action" in
  up)
    synth_out="$(mktemp -d "${TMPDIR:-/tmp}/lesser-host-cdk-synth.XXXXXX")"
    cleanup() {
      rm -rf "$synth_out"
      cleanup_tmp_files
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
