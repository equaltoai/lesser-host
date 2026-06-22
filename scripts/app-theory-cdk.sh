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

case "$action" in
  up)
    synth_out="$(mktemp -d "${TMPDIR:-/tmp}/lesser-host-cdk-synth.XXXXXX")"
    cleanup() {
      rm -rf "$synth_out"
    }
    trap cleanup EXIT

    ./node_modules/.bin/cdk synth --all -c "stage=$stage" --output "$synth_out"
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

    ./node_modules/.bin/cdk destroy --all -c "stage=$stage" --force
    ;;
esac
