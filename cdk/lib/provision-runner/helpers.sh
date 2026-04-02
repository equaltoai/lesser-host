#!/usr/bin/env bash
# Shared helper functions for the provision runner buildspec.
# Sourced by build.sh — do not execute directly.

fail() { echo "ERROR: $1" >&2; exit 1; }

resolve_latest_release_tag() {
  o="$1"
  r="$2"
  url=$(curl -sSfL -o /dev/null -w "%{url_effective}" "https://github.com/$o/$r/releases/latest")
  echo "${url##*/}"
}

download_github_release_asset() {
  o="$1"
  r="$2"
  tag="$3"
  asset="$4"
  out="$5"
  url="https://github.com/$o/$r/releases/download/$tag/$asset"
  echo "Downloading $asset from $o/$r@$tag..."
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream" -sSfL -o "$out" "$url"
  else
    curl -sSfL -o "$out" "$url"
  fi
}

download_github_archive() {
  o="$1"
  r="$2"
  ref="$3"
  out="$4"
  url="https://api.github.com/repos/$o/$r/tarball/$ref"
  echo "Downloading source archive for $o/$r@$ref..."
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    curl -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/vnd.github+json" -sSfL -o "$out" "$url"
  else
    curl -H "Accept: application/vnd.github+json" -sSfL -o "$out" "$url"
  fi
}

checksum_for_asset() {
  checksums_path="$1"
  asset_name="$2"
  awk -v asset="$asset_name" '{ name=$2; sub(/^\*/, "", name); if (name == asset) { print $1; exit } }' "$checksums_path"
}

verify_downloaded_asset_checksum() {
  checksums_path="$1"
  asset_name="$2"
  file_path="$3"
  expected=$(checksum_for_asset "$checksums_path" "$asset_name")
  test -n "$expected" || fail "checksum entry missing for $asset_name"
  actual=$(sha256sum "$file_path" | awk '{print $1}')
  test "$expected" = "$actual" || fail "checksum mismatch for $asset_name"
}

prepare_lesser_release_dir() {
  release_dir="$1"
  rm -rf "$release_dir"
  mkdir -p "$release_dir"
  # Download all 7 required release files.
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "checksums.txt" "$release_dir/checksums.txt"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "lesser-release.json" "$release_dir/lesser-release.json"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "lesser-lambda-bundle.tar.gz" "$release_dir/lesser-lambda-bundle.tar.gz"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "lesser-lambda-bundle.json" "$release_dir/lesser-lambda-bundle.json"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "lesser-deploy-assembly.tar.gz" "$release_dir/lesser-deploy-assembly.tar.gz"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "lesser-deploy-assembly.json" "$release_dir/lesser-deploy-assembly.json"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "lesser-auth-ui.tar.gz" "$release_dir/lesser-auth-ui.tar.gz"
  # Download the CLI binary.
  ARCH=$(uname -m)
  BIN_NAME=""
  if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then BIN_NAME="lesser-linux-amd64"; fi
  if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then BIN_NAME="lesser-linux-arm64"; fi
  test -n "${BIN_NAME:-}" || fail "unsupported architecture: $ARCH"
  download_github_release_asset "$OWNER" "$REPO" "$TAG" "$BIN_NAME" "$release_dir/lesser"
  # Verify checksums for all downloaded files.
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "lesser-release.json" "$release_dir/lesser-release.json"
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "lesser-lambda-bundle.tar.gz" "$release_dir/lesser-lambda-bundle.tar.gz"
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "lesser-lambda-bundle.json" "$release_dir/lesser-lambda-bundle.json"
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "lesser-deploy-assembly.tar.gz" "$release_dir/lesser-deploy-assembly.tar.gz"
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "lesser-deploy-assembly.json" "$release_dir/lesser-deploy-assembly.json"
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "lesser-auth-ui.tar.gz" "$release_dir/lesser-auth-ui.tar.gz"
  verify_downloaded_asset_checksum "$release_dir/checksums.txt" "$BIN_NAME" "$release_dir/lesser"
  chmod +x "$release_dir/lesser"
  # Validate release manifest.
  LESSER_RELEASE_NAME=$(jq -r '.name // empty' "$release_dir/lesser-release.json")
  test "$LESSER_RELEASE_NAME" = "lesser" || fail "unexpected Lesser release manifest name: $LESSER_RELEASE_NAME"
  LESSER_RELEASE_VERSION=$(jq -r '.version // empty' "$release_dir/lesser-release.json")
  test "$LESSER_RELEASE_VERSION" = "$TAG" || fail "Lesser release manifest version mismatch: $LESSER_RELEASE_VERSION"
}

prepare_lesser_checkout_dir() {
  release_dir="$1"
  checkout_dir="$2"
  rm -rf "$checkout_dir"
  mkdir -p "$checkout_dir"

  LESSER_RELEASE_GIT_SHA=$(jq -r '.git_sha // empty' "$release_dir/lesser-release.json")
  test -n "$LESSER_RELEASE_GIT_SHA" || fail "Lesser release manifest git_sha is missing"

  source_archive="$release_dir/lesser-source.tar.gz"
  download_github_archive "$OWNER" "$REPO" "$LESSER_RELEASE_GIT_SHA" "$source_archive"
  tar -xzf "$source_archive" -C "$checkout_dir" --strip-components=1
  rm -f "$source_archive"

  test -f "$checkout_dir/go.mod" || fail "release checkout missing go.mod at $checkout_dir/go.mod"
  test -f "$checkout_dir/infra/cdk/cdk.json" || fail "release checkout missing infra/cdk/cdk.json"
  test -f "$checkout_dir/infra/cdk/inventory/lambdas.go" || fail "release checkout missing infra/cdk/inventory/lambdas.go"

  echo "Prepared release-matched Lesser checkout at $checkout_dir"
}

current_go_version() {
  if ! command -v go >/dev/null 2>&1; then
    return 1
  fi

  if go env GOVERSION >/dev/null 2>&1; then
    go env GOVERSION
    return 0
  fi

  go version | awk '{print $3}'
}

go_version_series() {
  version="$1"
  printf "%s\n" "$version" | awk -F. '{print $1"."$2}'
}

ensure_lesser_go_toolchain() {
  release_dir="$1"
  required_go_version=$(jq -r '.go_version // empty' "$release_dir/lesser-release.json")
  test -n "$required_go_version" || fail "Lesser release manifest go_version is missing"
  required_go_series=$(go_version_series "$required_go_version")

  current_version=""
  if current_version=$(current_go_version 2>/dev/null); then
    if [ "$(go_version_series "$current_version")" = "$required_go_series" ]; then
      echo "Using existing Go toolchain $current_version for required series $required_go_version"
      return 0
    fi
  fi

  GO_ARCH=""
  ARCH=$(uname -m)
  if [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "amd64" ]; then GO_ARCH="amd64"; fi
  if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then GO_ARCH="arm64"; fi
  test -n "$GO_ARCH" || fail "unsupported architecture for Go toolchain: $ARCH"

  toolchain_cache_dir="$HOME/.cache/lesser-host/go"
  toolchain_archive_path="$toolchain_cache_dir/${required_go_version}.linux-${GO_ARCH}.tar.gz"
  toolchain_install_dir="$HOME/.local/lesser-host-go/$required_go_version"
  if [ ! -x "$toolchain_install_dir/bin/go" ]; then
    mkdir -p "$toolchain_cache_dir"
    mkdir -p "$(dirname "$toolchain_install_dir")"
    echo "Downloading Go toolchain $required_go_version for linux/$GO_ARCH..."
    curl -sSfL -o "$toolchain_archive_path" "https://go.dev/dl/${required_go_version}.linux-${GO_ARCH}.tar.gz"
    rm -rf "$toolchain_install_dir"
    mkdir -p "$toolchain_install_dir"
    tar -xzf "$toolchain_archive_path" -C "$toolchain_install_dir" --strip-components=1
  fi

  export PATH="$toolchain_install_dir/bin:$PATH"
  current_version=$(current_go_version 2>/dev/null || true)
  test "$(go_version_series "${current_version:-}")" = "$required_go_series" || fail "unable to activate Go toolchain series $required_go_version (got: ${current_version:-missing})"
  if [ "$current_version" != "$required_go_version" ]; then
    echo "Activated Go toolchain $current_version for required series $required_go_version; GOTOOLCHAIN=auto will select the release patch if needed."
  fi
}

prepare_lesser_body_release_dir() {
  body_release_dir="$1"
  body_owner="$2"
  body_repo="$3"
  body_tag="$4"
  body_stage="$5"
  rm -rf "$body_release_dir"
  mkdir -p "$body_release_dir"
  download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "checksums.txt" "$body_release_dir/checksums.txt"
  download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "lesser-body-release.json" "$body_release_dir/lesser-body-release.json"
  download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "lesser-body-deploy.json" "$body_release_dir/lesser-body-deploy.json"
  download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "deploy-lesser-body-from-release.sh" "$body_release_dir/deploy-lesser-body-from-release.sh"
  download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "lesser-body.zip" "$body_release_dir/lesser-body.zip"
  download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "lesser-body-managed-$body_stage.template.json" "$body_release_dir/lesser-body-managed-$body_stage.template.json"
  chmod +x "$body_release_dir/deploy-lesser-body-from-release.sh"
  verify_downloaded_asset_checksum "$body_release_dir/checksums.txt" "lesser-body-release.json" "$body_release_dir/lesser-body-release.json"
  verify_downloaded_asset_checksum "$body_release_dir/checksums.txt" "lesser-body-deploy.json" "$body_release_dir/lesser-body-deploy.json"
  verify_downloaded_asset_checksum "$body_release_dir/checksums.txt" "deploy-lesser-body-from-release.sh" "$body_release_dir/deploy-lesser-body-from-release.sh"
  verify_downloaded_asset_checksum "$body_release_dir/checksums.txt" "lesser-body.zip" "$body_release_dir/lesser-body.zip"
  verify_downloaded_asset_checksum "$body_release_dir/checksums.txt" "lesser-body-managed-$body_stage.template.json" "$body_release_dir/lesser-body-managed-$body_stage.template.json"
  BODY_RELEASE_NAME=$(jq -r '.name // empty' "$body_release_dir/lesser-body-release.json")
  test "$BODY_RELEASE_NAME" = "lesser-body" || fail "unexpected lesser-body release manifest name: $BODY_RELEASE_NAME"
  BODY_RELEASE_VERSION=$(jq -r '.version // empty' "$body_release_dir/lesser-body-release.json")
  test "$BODY_RELEASE_VERSION" = "$body_tag" || fail "lesser-body release manifest version mismatch: $BODY_RELEASE_VERSION"
  BODY_DEPLOY_MANIFEST=$(jq -r '.deploy.manifest_path // empty' "$body_release_dir/lesser-body-release.json")
  test "$BODY_DEPLOY_MANIFEST" = "lesser-body-deploy.json" || fail "unexpected lesser-body deploy manifest path: $BODY_DEPLOY_MANIFEST"
  BODY_SOURCE_CHECKOUT_REQUIRED=$(jq -r 'if .deploy.source_checkout_required == false then "false" elif .deploy.source_checkout_required == true then "true" else empty end' "$body_release_dir/lesser-body-release.json")
  test "$BODY_SOURCE_CHECKOUT_REQUIRED" = "false" || fail "lesser-body release unexpectedly requires a source checkout"
  BODY_NPM_INSTALL_REQUIRED=$(jq -r 'if .deploy.npm_install_required == false then "false" elif .deploy.npm_install_required == true then "true" else empty end' "$body_release_dir/lesser-body-release.json")
  test "$BODY_NPM_INSTALL_REQUIRED" = "false" || fail "lesser-body release unexpectedly requires npm install"
  BODY_TEMPLATE_PATH=$(jq -r --arg stage "$body_stage" '.artifacts.deploy_templates[$stage].path // empty' "$body_release_dir/lesser-body-release.json")
  test "$BODY_TEMPLATE_PATH" = "lesser-body-managed-$body_stage.template.json" || fail "unexpected lesser-body template path for stage $body_stage: $BODY_TEMPLATE_PATH"
  BODY_SCRIPT_PATH=$(jq -r '.artifacts.deploy_script.path // empty' "$body_release_dir/lesser-body-release.json")
  test "$BODY_SCRIPT_PATH" = "deploy-lesser-body-from-release.sh" || fail "unexpected lesser-body deploy script path: $BODY_SCRIPT_PATH"
  BODY_LAMBDA_PATH=$(jq -r '.artifacts.lambda_zip.path // empty' "$body_release_dir/lesser-body-release.json")
  test "$BODY_LAMBDA_PATH" = "lesser-body.zip" || fail "unexpected lesser-body lambda zip path: $BODY_LAMBDA_PATH"
}

upload_optional_artifact() {
  artifact_path="$1"
  artifact_key="$2"
  if [ -n "$artifact_key" ] && [ -f "$artifact_path" ]; then
    aws s3 cp "$artifact_path" "s3://$ARTIFACT_BUCKET/$artifact_key" >/dev/null 2>&1 || true
  fi
}

write_lesser_body_artifact() {
  artifact_path="$1"
  status="$2"
  release_version="$3"
  template_path="$4"
  stack_name="$5"
  verification_mode="$6"
  detail_path="$7"
  detail=""
  if [ -f "$detail_path" ]; then
    detail=$(tail -n 40 "$detail_path")
  fi
  jq -n --arg status "$status" --arg release_version "$release_version" --arg template_path "$template_path" --arg stack_name "$stack_name" --arg verification_mode "$verification_mode" --arg detail "$detail" --arg verified_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" '{version:1,status:$status,lesser_body_version:$release_version,template_path:$template_path,stack_name:$stack_name,verification_mode:$verification_mode,detail:$detail,verified_at:$verified_at}' > "$artifact_path"
}

run_lesser_body_helper_with_capture() {
  log_path="$1"
  shift
  : > "$log_path"
  if "$@" > >(tee -a "$log_path") 2> >(tee -a "$log_path" >&2); then
    return 0
  fi
  return 1
}

patch_cfn_template_logical_id() {
  template_path="$1"
  from_id="$2"
  to_id="$3"

  if [ -z "$from_id" ] || [ -z "$to_id" ] || [ "$from_id" = "$to_id" ]; then
    return 0
  fi
  if [ ! -f "$template_path" ]; then
    echo "WARN: missing template for patch: $template_path"
    return 0
  fi

  tmp_path="${template_path}.patched"
  jq --arg from "$from_id" --arg to "$to_id" '
    def walk(f):
      . as $in
      | if type == "object" then
          reduce keys_unsorted[] as $key ({}; .[$key] = ($in[$key] | walk(f)))
          | f
        elif type == "array" then map(walk(f)) | f
        else f
        end;

    (.Resources // {}) as $resources
    | .Resources = ($resources | with_entries(if .key == $from then .key = $to else . end))
    | walk(if type == "string" and . == $from then $to else . end)
  ' "$template_path" > "$tmp_path"
  mv "$tmp_path" "$template_path"
}

patch_lesser_body_template_for_existing_stack() {
  stack_name="$1"
  app_slug="$2"
  stage="$3"
  release_dir="$4"
  template_path="$5"

  if [ ! -f "$template_path" ]; then
    echo "WARN: missing lesser-body template at $template_path; skipping template patch."
    return 0
  fi

  resources_path="$release_dir/stack-resources.json"
  if ! AWS_PROFILE=managed aws cloudformation list-stack-resources --stack-name "$stack_name" --output json > "$resources_path" 2>/dev/null; then
    echo "No existing stack named $stack_name; skipping template logical-id compatibility patch."
    return 0
  fi

  patch_param() {
    from_id="$1"
    param_name="$2"
    existing_id=$(jq -r --arg name "$param_name" '.StackResourceSummaries[] | select(.ResourceType=="AWS::SSM::Parameter" and .PhysicalResourceId==$name) | .LogicalResourceId' "$resources_path" | head -n 1)
    if [ -n "$existing_id" ] && [ "$existing_id" != "null" ] && [ "$existing_id" != "$from_id" ]; then
      echo "Patching template logical id: $from_id -> $existing_id (existing SSM param $param_name)"
      patch_cfn_template_logical_id "$template_path" "$from_id" "$existing_id"
    fi
  }

  export_prefix="/$app_slug/$stage/lesser-body/exports/v1"
  patch_param "McpLambdaArnParam" "$export_prefix/mcp_lambda_arn"
  patch_param "McpEndpointParam" "$export_prefix/mcp_endpoint_url"
  patch_param "McpSessionTableParam" "$export_prefix/mcp_session_table_name"
  patch_param "McpStreamTableParam" "$export_prefix/mcp_stream_table_name"
}

bool_on() {
  v=$(printf "%s" "$1" | tr "[:upper:]" "[:lower:]")
  case "$v" in true|1|yes|on) return 0 ;; *) return 1 ;; esac
}

validate_https_custom_domain() {
  NAME="$1"
  VALUE="$2"
  if [ -z "$VALUE" ]; then fail "$NAME is empty"; fi
  case "$VALUE" in https://*) ;; *) fail "$NAME must start with https:// (got: $VALUE)";; esac
  case "$VALUE" in *.lambda-url.*|*amazonaws.com*|*.on.aws*|*cloudfront.net*) fail "$NAME must be a custom domain URL, not an AWS-generated hostname (got: $VALUE)";; esac
}

enable_agents() {
  echo "Ensuring agents are enabled..."
  STACK_NAME="$APP_SLUG-$STAGE"
  API_FN="$APP_SLUG-$STAGE-api"
  GRAPHQL_FN="$APP_SLUG-$STAGE-graphql"
  GRAPHQL_WS_FN="$APP_SLUG-$STAGE-graphql-ws"

  update_lambda_env() {
    FN="$1"
    echo "Setting agent env vars on Lambda: $FN"
    CUR=$(aws lambda get-function-configuration --profile managed --function-name "$FN" --output json | jq -c '.Environment.Variables // {}')
    NEXT=$(printf "%s" "$CUR" | jq -c '. + {"ALLOW_AGENTS":"true","ALLOW_AGENT_REGISTRATION":"true"}')
    ENV=$(jq -nc --argjson vars "$NEXT" '{Variables:$vars}')
    aws lambda update-function-configuration --profile managed --function-name "$FN" --environment "$ENV" >/dev/null
  }

  wait_lambda_update() {
    FN="$1"
    for i in $(seq 1 60); do
      STATUS=$(aws lambda get-function-configuration --profile managed --function-name "$FN" --query "LastUpdateStatus" --output text)
      case "$STATUS" in
        Successful) return 0 ;;
        Failed) fail "Lambda update failed: $FN" ;;
      esac
      sleep 2
    done
    fail "Lambda update timed out: $FN"
  }

  update_lambda_env "$API_FN"
  update_lambda_env "$GRAPHQL_FN"
  update_lambda_env "$GRAPHQL_WS_FN"

  wait_lambda_update "$API_FN"
  wait_lambda_update "$GRAPHQL_FN"
  wait_lambda_update "$GRAPHQL_WS_FN"

  TABLE_NAME=$(aws cloudformation describe-stacks --profile managed --stack-name "$STACK_NAME" --output json | jq -r '.Stacks[0].Outputs[] | select(.OutputKey=="TableName") | .OutputValue' | head -n 1)
  test -n "$TABLE_NAME" && test "$TABLE_NAME" != "null"

  NOW=$(date -u +"%Y-%m-%dT%H:%M:%S.%NZ")
  aws dynamodb update-item --profile managed --region "$TARGET_REGION" --table-name "$TABLE_NAME" \
    --key '{"PK":{"S":"INSTANCE#CONFIG"},"SK":{"S":"AGENT_CONFIG"}}' \
    --update-expression 'SET allowAgents=:t, allowAgentRegistration=:t, defaultQuarantineDays=:dq, maxAgentsPerOwner=:mao, allowRemoteAgents=:ar, remoteQuarantineDays=:rdq, blockedAgentDomains=:empty, trustedAgentDomains=:empty, agentMaxPostsPerHour=:ampph, verifiedAgentMaxPostsPerHour=:vampph, agentMaxFollowsPerHour=:amfph, verifiedAgentMaxFollowsPerHour=:vamfph, hybridRetrievalEnabled=:hre, hybridRetrievalMaxCandidates=:hrmc, updatedAt=:now' \
    --expression-attribute-values '{":t":{"BOOL":true},":dq":{"N":"7"},":mao":{"N":"3"},":ar":{"BOOL":true},":rdq":{"N":"7"},":empty":{"L":[]},":ampph":{"N":"50"},":vampph":{"N":"200"},":amfph":{"N":"20"},":vamfph":{"N":"100"},":hre":{"BOOL":false},":hrmc":{"N":"200"},":now":{"S":"'"$NOW"'"}}' \
    --return-values NONE >/dev/null
  echo "Agents enabled."
}
