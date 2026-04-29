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

npm ci
npm run build

case "$action" in
  up)
    ./node_modules/.bin/cdk deploy --all -c "stage=$stage" --require-approval never
    ;;
  down)
    ./node_modules/.bin/cdk destroy --all -c "stage=$stage" --force
    ;;
esac
