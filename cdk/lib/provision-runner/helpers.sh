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

require_managed_release_tag() {
  name="$1"
  tag="$2"
  normalized=$(printf "%s" "$tag" | tr "[:upper:]" "[:lower:]")
  if [ -z "$tag" ]; then
    fail "$name must be set to a concrete release tag like v1.2.6"
  fi
  if [ "$normalized" = "latest" ]; then
    fail "$name must be resolved before the deploy runner starts; refusing dynamic latest resolution"
  fi
  case "$tag" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) fail "$name must be a concrete release tag like v1.2.6 (got: $tag)" ;;
  esac
  if ! printf "%s" "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    fail "$name must be a final semver tag like v1.2.6 (got: $tag)"
  fi
}

require_immutable_git_sha() {
  name="$1"
  value="$2"
  if ! printf "%s" "$value" | grep -Eq '^[0-9a-fA-F]{40}$'; then
    fail "$name must be a 40-character git commit SHA (got: $value)"
  fi
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

safe_release_asset_path() {
  value="$1"
  label="$2"
  if [ -z "$value" ]; then fail "$label is required"; fi
  case "$value" in
    /*|*\\*|*..*|*$'\n'*|*$'\r'*) fail "$label must be a safe relative release asset path (got: $value)" ;;
  esac
  if printf "%s" "$value" | grep -Eq '(^|/)(\.|)(/|$)'; then
    fail "$label must not contain empty/current path segments (got: $value)"
  fi
}

require_lesser_body_not_reserved_release_asset_path() {
  value="$1"
  label="$2"
  body_stage="$3"
  case "$value" in
    checksums.txt|lesser-body-release.json|lesser-body-deploy.json|deploy-lesser-body-from-release.sh|lesser-body.zip|"lesser-body-managed-$body_stage.template.json")
      fail "$label is reserved for a verified lesser-body release artifact (got: $value)"
      ;;
  esac
}

require_lesser_body_auxiliary_capability() {
  manifest_path="$1"
  if ! jq -e '.required_capabilities // [] | index("managed_auxiliary_assets_v1")' "$manifest_path" >/dev/null; then
    fail "lesser-body deploy manifest schema 2 requires managed_auxiliary_assets_v1 capability"
  fi
  unsupported=$(jq -r '.required_capabilities[]? | select(. != "managed_auxiliary_assets_v1")' "$manifest_path" | head -n 1)
  test -z "$unsupported" || fail "unsupported lesser-body required capability: $unsupported"
}

require_lesser_body_release_auxiliary_capability() {
  release_manifest_path="$1"
  if ! jq -e '.deploy.required_capabilities // [] | index("managed_auxiliary_assets_v1")' "$release_manifest_path" >/dev/null; then
    fail "lesser-body release manifest schema 2 requires managed_auxiliary_assets_v1 capability"
  fi
  unsupported=$(jq -r '.deploy.required_capabilities[]? | select(. != "managed_auxiliary_assets_v1")' "$release_manifest_path" | head -n 1)
  test -z "$unsupported" || fail "unsupported lesser-body release required capability: $unsupported"
}

require_lesser_body_auxiliary_manifest_agreement() {
  release_manifest_path="$1"
  deploy_manifest_path="$2"
  release_paths=$(jq -r '.artifacts.auxiliary_assets[]?.path' "$release_manifest_path" | sort)
  deploy_paths=$(jq -r '.auxiliary_assets[]?.path' "$deploy_manifest_path" | sort)
  test "$release_paths" = "$deploy_paths" || fail "lesser-body auxiliary asset paths must match between release and deploy manifests"
}

prepare_lesser_body_auxiliary_assets() {
  body_release_dir="$1"
  body_owner="$2"
  body_repo="$3"
  body_tag="$4"
  body_stage="$5"
  deploy_manifest_path="$body_release_dir/lesser-body-deploy.json"
  deploy_schema=$(jq -r '.schema // 1' "$deploy_manifest_path")
  if [ "$deploy_schema" = "1" ]; then
    return 0
  fi
  test "$deploy_schema" = "2" || fail "unsupported lesser-body deploy manifest schema: $deploy_schema"
  require_lesser_body_auxiliary_capability "$deploy_manifest_path"
  require_lesser_body_auxiliary_manifest_agreement "$body_release_dir/lesser-body-release.json" "$deploy_manifest_path"

  template_path="lesser-body-managed-$body_stage.template.json"
  jq -c '.auxiliary_assets[]?' "$deploy_manifest_path" | while IFS= read -r asset; do
    id=$(printf "%s" "$asset" | jq -r '.id // empty')
    path=$(printf "%s" "$asset" | jq -r '.path // empty')
    s3_key=$(printf "%s" "$asset" | jq -r '.s3_key // empty')
    template_parameter=$(printf "%s" "$asset" | jq -r '.template_parameter // empty')
    bytes=$(printf "%s" "$asset" | jq -r '.bytes // 0')
    required=$(printf "%s" "$asset" | jq -r 'if .required == false then "false" else "true" end')
    test -n "$id" || fail "lesser-body auxiliary asset id is required"
    safe_release_asset_path "$path" "lesser-body auxiliary asset $id path"
    safe_release_asset_path "$s3_key" "lesser-body auxiliary asset $id s3_key"
    require_lesser_body_not_reserved_release_asset_path "$path" "lesser-body auxiliary asset $id path" "$body_stage"
    require_lesser_body_not_reserved_release_asset_path "$s3_key" "lesser-body auxiliary asset $id s3_key" "$body_stage"
    printf "%s" "$template_parameter" | grep -Eq '^[A-Za-z][A-Za-z0-9]*$' || fail "lesser-body auxiliary asset $id template_parameter is invalid"
    test "$bytes" -gt 0 || fail "lesser-body auxiliary asset $id bytes must be positive"
    if [ "$required" = "true" ]; then
      refs=$(printf "%s" "$asset" | jq --arg stage "$body_stage" --arg template "$template_path" --arg param "$template_parameter" '[.template_references[]? | select((.stage == $stage) and (.template == $template) and (.key_parameter == $param))] | length')
      test "$refs" -gt 0 || fail "required lesser-body auxiliary asset $id has no matching template reference for $template_path"
    fi
    mkdir -p "$(dirname "$body_release_dir/$path")"
    download_github_release_asset "$body_owner" "$body_repo" "$body_tag" "$path" "$body_release_dir/$path"
    verify_downloaded_asset_checksum "$body_release_dir/checksums.txt" "$path" "$body_release_dir/$path"
    actual_bytes=$(stat -c%s "$body_release_dir/$path")
    test "$actual_bytes" = "$bytes" || fail "byte-size mismatch for lesser-body auxiliary asset $id"
  done
}

upload_lesser_body_auxiliary_assets() {
  body_release_dir="$1"
  body_asset_bucket="$2"
  body_asset_prefix="$3"
  deploy_manifest_path="$body_release_dir/lesser-body-deploy.json"
  deploy_schema=$(jq -r '.schema // 1' "$deploy_manifest_path")
  if [ "$deploy_schema" = "1" ]; then
    return 0
  fi
  test "$deploy_schema" = "2" || fail "unsupported lesser-body deploy manifest schema: $deploy_schema"
  require_lesser_body_auxiliary_capability "$deploy_manifest_path"

  jq -c '.auxiliary_assets[]?' "$deploy_manifest_path" | while IFS= read -r asset; do
    id=$(printf "%s" "$asset" | jq -r '.id // empty')
    path=$(printf "%s" "$asset" | jq -r '.path // empty')
    s3_key=$(printf "%s" "$asset" | jq -r '.s3_key // empty')
    content_type=$(printf "%s" "$asset" | jq -r '.content_type // empty')
    safe_release_asset_path "$path" "lesser-body auxiliary asset $id path"
    safe_release_asset_path "$s3_key" "lesser-body auxiliary asset $id s3_key"
    body_stage="${STAGE:-}"
    if [ -z "$body_stage" ]; then body_stage=$(jq -r '.templates | keys[0] // "dev"' "$deploy_manifest_path"); fi
    require_lesser_body_not_reserved_release_asset_path "$path" "lesser-body auxiliary asset $id path" "$body_stage"
    require_lesser_body_not_reserved_release_asset_path "$s3_key" "lesser-body auxiliary asset $id s3_key" "$body_stage"
    object_key="$body_asset_prefix/$s3_key"
    echo "Uploading lesser-body auxiliary asset $id to s3://$body_asset_bucket/$object_key"
    if [ -n "$content_type" ]; then
      AWS_PROFILE=managed aws s3 cp "$body_release_dir/$path" "s3://$body_asset_bucket/$object_key" --content-type "$content_type"
    else
      AWS_PROFILE=managed aws s3 cp "$body_release_dir/$path" "s3://$body_asset_bucket/$object_key"
    fi
  done
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
  require_immutable_git_sha "Lesser release manifest git_sha" "$LESSER_RELEASE_GIT_SHA"

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
  BODY_RELEASE_SCHEMA=$(jq -r '.schema // 1' "$body_release_dir/lesser-body-release.json")
  BODY_RELEASE_DEPLOY_SCHEMA=$(jq -r '.deploy.schema // 1' "$body_release_dir/lesser-body-release.json")
  BODY_RELEASE_DEPLOY_MANIFEST_SCHEMA=$(jq -r '.artifacts.deploy_manifest.schema // 1' "$body_release_dir/lesser-body-release.json")
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
  BODY_DEPLOY_SCHEMA=$(jq -r '.schema // 1' "$body_release_dir/lesser-body-deploy.json")
  if [ "$BODY_RELEASE_SCHEMA" = "2" ]; then
    test "$BODY_RELEASE_DEPLOY_SCHEMA" = "2" || fail "lesser-body release schema 2 requires deploy schema 2"
    test "$BODY_RELEASE_DEPLOY_MANIFEST_SCHEMA" = "2" || fail "lesser-body release schema 2 requires deploy manifest schema 2"
    test "$BODY_DEPLOY_SCHEMA" = "2" || fail "lesser-body release schema 2 requires lesser-body-deploy.json schema 2"
    require_lesser_body_release_auxiliary_capability "$body_release_dir/lesser-body-release.json"
  else
    test "$BODY_RELEASE_SCHEMA" = "1" || fail "unsupported lesser-body release manifest schema: $BODY_RELEASE_SCHEMA"
    test "$BODY_RELEASE_DEPLOY_SCHEMA" = "1" || fail "unsupported lesser-body deploy schema for release schema 1: $BODY_RELEASE_DEPLOY_SCHEMA"
    test "$BODY_RELEASE_DEPLOY_MANIFEST_SCHEMA" = "1" || fail "unsupported lesser-body deploy manifest schema for release schema 1: $BODY_RELEASE_DEPLOY_MANIFEST_SCHEMA"
  fi
  if [ "$BODY_DEPLOY_SCHEMA" = "2" ]; then
    prepare_lesser_body_auxiliary_assets "$body_release_dir" "$body_owner" "$body_repo" "$body_tag" "$body_stage"
  else
    test "$BODY_DEPLOY_SCHEMA" = "1" || fail "unsupported lesser-body deploy manifest schema: $BODY_DEPLOY_SCHEMA"
  fi
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

managed_instance_key_stage() {
  stage=$(printf "%s" "${STAGE:-lab}" | tr "[:upper:]" "[:lower:]")
  case "$stage" in prod|production) stage="live" ;; esac
  stage=$(printf "%s" "$stage" | tr -cd 'a-z0-9._-' | sed 's/^[._-]*//;s/[._-]*$//')
  if [ -z "$stage" ]; then stage="lab"; fi
  printf "%s" "$stage"
}

managed_instance_key_slug() {
  printf "%s" "${APP_SLUG:-}" | tr "[:upper:]" "[:lower:]" | xargs
}

managed_instance_key_secret_name() {
  key_stage=$(managed_instance_key_stage)
  key_slug=$(managed_instance_key_slug)
  if [ -z "$key_slug" ]; then fail "APP_SLUG is required for managed instance key secret"; fi
  printf "%s/%s/instance-key" "$key_stage" "$key_slug"
}

managed_instance_key_id_for_plaintext() {
  plaintext="$1"
  printf "%s" "$plaintext" | sha256sum | awk '{print $1}'
}

new_lesser_host_instance_key() {
  token=$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n\r')
  test -n "$token" || fail "failed to generate instance key token"
  printf "lhk_%s" "$token"
}

managed_instance_key_tag_value() {
  desc_path="$1"
  tag_key="$2"
  jq -r --arg key "$tag_key" '.Tags[]? | select(.Key == $key) | .Value // empty' "$desc_path" | head -n 1
}

validate_managed_instance_key_secret_tags() {
  desc_path="$1"
  expected_slug=$(managed_instance_key_slug)
  expected_stage=$(managed_instance_key_stage)
  managed_tag=$(managed_instance_key_tag_value "$desc_path" "lesser-host:managed" | tr "[:upper:]" "[:lower:]")
  slug_tag=$(managed_instance_key_tag_value "$desc_path" "lesser-host:instance-slug" | tr "[:upper:]" "[:lower:]")
  stage_tag=$(managed_instance_key_tag_value "$desc_path" "lesser-host:control-plane-stage")
  normalized_stage=$(printf "%s" "$stage_tag" | tr "[:upper:]" "[:lower:]" | tr -cd 'a-z0-9._-' | sed 's/^[._-]*//;s/[._-]*$//')
  case "$normalized_stage" in prod|production) normalized_stage="live" ;; esac
  test "$managed_tag" = "true" || fail "managed instance key secret is missing managed=true tag"
  test "$slug_tag" = "$expected_slug" || fail "managed instance key secret slug tag mismatch"
  test -n "$stage_tag" || fail "managed instance key secret stage tag is missing"
  test "$normalized_stage" = "$expected_stage" || fail "managed instance key secret stage tag mismatch"
}

read_managed_instance_key_plaintext() {
  secret_id="$1"
  raw=$(aws secretsmanager get-secret-value --profile managed --secret-id "$secret_id" --query SecretString --output text)
  test -n "$raw" && test "$raw" != "None" && test "$raw" != "null" || fail "managed instance key secret value is empty"
  if printf "%s" "$raw" | grep -q '^{'; then
    plaintext=$(printf "%s" "$raw" | jq -r '.secret // empty')
  else
    plaintext="$raw"
  fi
  test -n "$plaintext" || fail "managed instance key secret payload missing secret"
  printf "%s" "$plaintext"
}

write_managed_instance_key_secret_payload() {
  plaintext="$1"
  payload_path="$2"
  umask 077
  printf "%s" "$plaintext" | jq -Rs '{secret:.}' > "$payload_path"
}

tag_managed_instance_key_secret() {
  secret_arn="$1"
  key_id="$2"
  key_slug=$(managed_instance_key_slug)
  key_stage=$(managed_instance_key_stage)
  aws secretsmanager untag-resource --profile managed --secret-id "$secret_arn" --tag-keys "lesser-host:instance-key-id" "lesser-host:control-plane-stage" >/dev/null 2>&1 || true
  aws secretsmanager tag-resource --profile managed --secret-id "$secret_arn" --tags \
    Key=lesser-host:instance-slug,Value="$key_slug" \
    Key=lesser-host:instance-key-id,Value="$key_id" \
    Key=lesser-host:managed,Value=true \
    Key=lesser-host:control-plane-stage,Value="$key_stage" >/dev/null
}

write_managed_instance_key_receipt() {
  receipt_path="$1"
  secret_arn="$2"
  key_id="$3"
  rotated="$4"
  key_slug=$(managed_instance_key_slug)
  key_stage=$(managed_instance_key_stage)
  jq -n \
    --arg source "deploy-runner-managed-profile" \
    --arg secret_arn "$secret_arn" \
    --arg key_id "$key_id" \
    --arg instance_slug "$key_slug" \
    --arg stage "$key_stage" \
    --arg rotated "$rotated" \
    --arg verified_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    '{version:1,source:$source,secret_arn:$secret_arn,key_id:$key_id,instance_slug:$instance_slug,stage:$stage,rotated:($rotated=="true"),verified_at:$verified_at}' > "$receipt_path"
}

ensure_lesser_host_instance_key_secret() {
  : "${STATE_DIR:?STATE_DIR is required}"
  secret_ref="${LESSER_HOST_INSTANCE_KEY_SECRET_ID:-${LESSER_HOST_INSTANCE_KEY_ARN:-}}"
  if [ -z "$secret_ref" ]; then secret_ref=$(managed_instance_key_secret_name); fi

  desc_path="$STATE_DIR/managed-instance-key-describe.json"
  desc_err="$STATE_DIR/managed-instance-key-describe.err"
  payload_path="$STATE_DIR/managed-instance-key-payload.json"
  MANAGED_INSTANCE_KEY_RECEIPT_PATH="$STATE_DIR/managed-instance-key.json"
  rm -f "$desc_path" "$desc_err" "$payload_path" "$MANAGED_INSTANCE_KEY_RECEIPT_PATH"

  if aws secretsmanager describe-secret --profile managed --secret-id "$secret_ref" --output json > "$desc_path" 2>"$desc_err"; then
    validate_managed_instance_key_secret_tags "$desc_path"
    secret_arn=$(jq -r '.ARN // empty' "$desc_path")
    test -n "$secret_arn" && test "$secret_arn" != "null" || fail "managed instance key secret ARN is missing"
    plaintext=$(read_managed_instance_key_plaintext "$secret_arn")
    key_id=$(managed_instance_key_id_for_plaintext "$plaintext")
    tagged_key_id=$(managed_instance_key_tag_value "$desc_path" "lesser-host:instance-key-id")
    if [ -n "$tagged_key_id" ] && [ "$tagged_key_id" != "$key_id" ]; then
      fail "managed instance key secret key-id tag mismatch"
    fi
    rotated="false"
  else
    if ! grep -q "ResourceNotFoundException" "$desc_err"; then
      cat "$desc_err" >&2
      fail "failed to describe managed instance key secret"
    fi
    secret_name=$(managed_instance_key_secret_name)
    plaintext=$(new_lesser_host_instance_key)
    key_id=$(managed_instance_key_id_for_plaintext "$plaintext")
    write_managed_instance_key_secret_payload "$plaintext" "$payload_path"
    secret_arn=$(aws secretsmanager create-secret --profile managed \
      --name "$secret_name" \
      --description "managed instance API key" \
      --secret-string "file://$payload_path" \
      --tags \
        Key=lesser-host:instance-slug,Value="$(managed_instance_key_slug)" \
        Key=lesser-host:instance-key-id,Value="$key_id" \
        Key=lesser-host:managed,Value=true \
        Key=lesser-host:control-plane-stage,Value="$(managed_instance_key_stage)" \
      --query ARN --output text)
    test -n "$secret_arn" && test "$secret_arn" != "None" && test "$secret_arn" != "null" || fail "managed instance key secret create returned empty ARN"
    rotated="false"
  fi

  if bool_on "${LESSER_HOST_INSTANCE_KEY_ROTATE:-false}"; then
    plaintext=$(new_lesser_host_instance_key)
    key_id=$(managed_instance_key_id_for_plaintext "$plaintext")
    write_managed_instance_key_secret_payload "$plaintext" "$payload_path"
    aws secretsmanager update-secret --profile managed --secret-id "$secret_arn" --secret-string "file://$payload_path" >/dev/null
    rotated="true"
  fi

  tag_managed_instance_key_secret "$secret_arn" "$key_id"
  write_managed_instance_key_receipt "$MANAGED_INSTANCE_KEY_RECEIPT_PATH" "$secret_arn" "$key_id" "$rotated"
  export LESSER_HOST_INSTANCE_KEY_ARN="$secret_arn"
  export LESSER_HOST_INSTANCE_KEY_SECRET_ID="$secret_arn"
  export MANAGED_INSTANCE_KEY_RECEIPT_PATH
  rm -f "$payload_path" "$desc_err"
}

soul_binding_integration_secret_name() {
  key_stage=$(managed_instance_key_stage)
  key_slug=$(managed_instance_key_slug)
  if [ -z "$key_slug" ]; then fail "APP_SLUG is required for soul binding integration secret"; fi
  printf "%s/%s/soul-binding-integration" "$key_stage" "$key_slug"
}

new_soul_binding_integration_bearer() {
  token=$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n\r')
  test -n "$token" || fail "failed to generate soul binding integration bearer"
  printf "lsbi_%s" "$token"
}

validate_soul_binding_integration_secret_tags() {
  desc_path="$1"
  expected_slug=$(managed_instance_key_slug)
  expected_stage=$(managed_instance_key_stage)
  managed_tag=$(managed_instance_key_tag_value "$desc_path" "lesser-host:managed" | tr "[:upper:]" "[:lower:]")
  slug_tag=$(managed_instance_key_tag_value "$desc_path" "lesser-host:instance-slug" | tr "[:upper:]" "[:lower:]")
  stage_tag=$(managed_instance_key_tag_value "$desc_path" "lesser-host:control-plane-stage")
  normalized_stage=$(printf "%s" "$stage_tag" | tr "[:upper:]" "[:lower:]" | tr -cd 'a-z0-9._-' | sed 's/^[._-]*//;s/[._-]*$//')
  case "$normalized_stage" in prod|production) normalized_stage="live" ;; esac
  test "$managed_tag" = "true" || fail "soul binding integration secret is missing managed=true tag"
  test "$slug_tag" = "$expected_slug" || fail "soul binding integration secret slug tag mismatch"
  test -n "$stage_tag" || fail "soul binding integration secret stage tag is missing"
  test "$normalized_stage" = "$expected_stage" || fail "soul binding integration secret stage tag mismatch"
}

tag_soul_binding_integration_secret() {
  secret_arn="$1"
  key_id="$2"
  key_slug=$(managed_instance_key_slug)
  key_stage=$(managed_instance_key_stage)
  aws secretsmanager untag-resource --profile managed --secret-id "$secret_arn" --tag-keys "lesser-host:soul-binding-key-id" "lesser-host:control-plane-stage" >/dev/null 2>&1 || true
  aws secretsmanager tag-resource --profile managed --secret-id "$secret_arn" --tags \
    Key=lesser-host:instance-slug,Value="$key_slug" \
    Key=lesser-host:soul-binding-key-id,Value="$key_id" \
    Key=lesser-host:managed,Value=true \
    Key=lesser-host:control-plane-stage,Value="$key_stage" >/dev/null
}

write_soul_binding_integration_receipt() {
  receipt_path="$1"
  secret_arn="$2"
  key_id="$3"
  key_slug=$(managed_instance_key_slug)
  key_stage=$(managed_instance_key_stage)
  jq -n \
    --arg source "deploy-runner-managed-profile" \
    --arg secret_arn "$secret_arn" \
    --arg key_id "$key_id" \
    --arg instance_slug "$key_slug" \
    --arg stage "$key_stage" \
    --arg verified_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    '{version:1,source:$source,secret_arn:$secret_arn,key_id:$key_id,instance_slug:$instance_slug,stage:$stage,verified_at:$verified_at}' > "$receipt_path"
}

ensure_soul_binding_integration_secret() {
  : "${STATE_DIR:?STATE_DIR is required}"
  secret_ref="${SOUL_BINDING_INTEGRATION_KEY_ARN:-}"
  if [ -z "$secret_ref" ]; then secret_ref=$(soul_binding_integration_secret_name); fi

  desc_path="$STATE_DIR/soul-binding-integration-describe.json"
  desc_err="$STATE_DIR/soul-binding-integration-describe.err"
  payload_path="$STATE_DIR/soul-binding-integration-payload.json"
  SOUL_BINDING_INTEGRATION_RECEIPT_PATH="$STATE_DIR/soul-binding-integration.json"
  rm -f "$desc_path" "$desc_err" "$payload_path" "$SOUL_BINDING_INTEGRATION_RECEIPT_PATH"

  if aws secretsmanager describe-secret --profile managed --secret-id "$secret_ref" --output json > "$desc_path" 2>"$desc_err"; then
    validate_soul_binding_integration_secret_tags "$desc_path"
    secret_arn=$(jq -r '.ARN // empty' "$desc_path")
    test -n "$secret_arn" && test "$secret_arn" != "null" || fail "soul binding integration secret ARN is missing"
    plaintext=$(read_managed_instance_key_plaintext "$secret_arn")
    key_id=$(managed_instance_key_id_for_plaintext "$plaintext")
    tagged_key_id=$(managed_instance_key_tag_value "$desc_path" "lesser-host:soul-binding-key-id")
    if [ -n "$tagged_key_id" ] && [ "$tagged_key_id" != "$key_id" ]; then
      fail "soul binding integration secret key-id tag mismatch"
    fi
  else
    if ! grep -q "ResourceNotFoundException" "$desc_err"; then
      cat "$desc_err" >&2
      fail "failed to describe soul binding integration secret"
    fi
    secret_name=$(soul_binding_integration_secret_name)
    plaintext=$(new_soul_binding_integration_bearer)
    key_id=$(managed_instance_key_id_for_plaintext "$plaintext")
    write_managed_instance_key_secret_payload "$plaintext" "$payload_path"
    secret_arn=$(aws secretsmanager create-secret --profile managed \
      --name "$secret_name" \
      --description "managed soul binding integration bearer" \
      --secret-string "file://$payload_path" \
      --tags \
        Key=lesser-host:instance-slug,Value="$(managed_instance_key_slug)" \
        Key=lesser-host:soul-binding-key-id,Value="$key_id" \
        Key=lesser-host:managed,Value=true \
        Key=lesser-host:control-plane-stage,Value="$(managed_instance_key_stage)" \
      --query ARN --output text)
    test -n "$secret_arn" && test "$secret_arn" != "None" && test "$secret_arn" != "null" || fail "soul binding integration secret create returned empty ARN"
  fi

  tag_soul_binding_integration_secret "$secret_arn" "$key_id"
  write_soul_binding_integration_receipt "$SOUL_BINDING_INTEGRATION_RECEIPT_PATH" "$secret_arn" "$key_id"
  export SOUL_BINDING_INTEGRATION_KEY_ARN="$secret_arn"
  export LESSER_SOUL_BINDING_INTEGRATION_BEARER_ARN="$secret_arn"
  export SOUL_BINDING_INTEGRATION_RECEIPT_PATH
  rm -f "$payload_path" "$desc_err"
}

# VAPID web-push credentials, provisioned per managed stage in the instance
# account. Mirrors lesser's scripts/ensure_vapid_credentials.sh contract exactly
# (verified against lesser v1.6.23): the secret lives at `lesser/vapid-key-<stage>`
# in the instance account, holds a P-256 keypair JSON {public_key, private_key,
# subject, created_at, updated_at}, and is consumed by `lesser up` via the
# VAPID_SECRET_ARN / VAPID_PUBLIC_KEY / VAPID_SUBJECT environment variables
# (cmd/lesser/up.go + cmd/lesser/cdk.go). Never rotates a healthy secret; a
# pre-existing secret (created by the manual script or a prior run) is reused.

vapid_secret_name() {
  key_stage=$(managed_instance_key_stage)
  printf "lesser/vapid-key-%s" "$key_stage"
}

vapid_resolve_subject() {
  printf "%s" "${VAPID_SUBJECT_OVERRIDE:-mailto:push@${BASE_DOMAIN}}"
}

generate_vapid_pair() {
  priv_path="$1"
  pub_der_path="$2"
  openssl ecparam -name prime256v1 -genkey -noout -out "$priv_path" >/dev/null 2>&1
  openssl ec -in "$priv_path" -pubout -outform DER -out "$pub_der_path" >/dev/null 2>&1
  # The last 65 bytes of the DER SubjectPublicKeyInfo are the uncompressed SEC1
  # point (0x04 || X || Y); urlsafe base64 without padding, byte-identical to
  # ensure_vapid_credentials.sh's python3 encoding.
  public_key=$(tail -c 65 "$pub_der_path" | base64 -w0 | tr '+/' '-_' | tr -d '=\n\r')
  test -n "$public_key" || fail "failed to generate VAPID public key"
  printf "%s" "$public_key"
}

write_vapid_key_receipt() {
  receipt_path="$1"
  secret_arn="$2"
  public_key="$3"
  subject="$4"
  reused="$5"
  key_slug=$(managed_instance_key_slug)
  key_stage=$(managed_instance_key_stage)
  jq -n \
    --arg source "deploy-runner-managed-profile" \
    --arg secret_arn "$secret_arn" \
    --arg public_key "$public_key" \
    --arg subject "$subject" \
    --arg instance_slug "$key_slug" \
    --arg stage "$key_stage" \
    --arg reused "$reused" \
    --arg verified_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    '{version:1,source:$source,secret_arn:$secret_arn,public_key:$public_key,subject:$subject,instance_slug:$instance_slug,stage:$stage,reused:($reused=="true"),verified_at:$verified_at}' > "$receipt_path"
}

ensure_vapid_key_secret() {
  : "${STATE_DIR:?STATE_DIR is required}"
  : "${BASE_DOMAIN:?BASE_DOMAIN is required for the VAPID subject}"
  secret_ref="${VAPID_SECRET_ARN:-}"
  if [ -z "$secret_ref" ]; then secret_ref=$(vapid_secret_name); fi

  desc_path="$STATE_DIR/vapid-key-describe.json"
  desc_err="$STATE_DIR/vapid-key-describe.err"
  payload_path="$STATE_DIR/vapid-key-payload.json"
  priv_path="$STATE_DIR/vapid-key-private.pem"
  pub_der_path="$STATE_DIR/vapid-key-public.der"
  VAPID_RECEIPT_PATH="$STATE_DIR/vapid-key.json"
  rm -f "$desc_path" "$desc_err" "$payload_path" "$priv_path" "$pub_der_path" "$VAPID_RECEIPT_PATH"

  public_key=""
  subject=""
  reused="false"

  if aws secretsmanager describe-secret --profile managed --secret-id "$secret_ref" --output json > "$desc_path" 2>"$desc_err"; then
    secret_arn=$(jq -r '.ARN // empty' "$desc_path")
    test -n "$secret_arn" && test "$secret_arn" != "null" || fail "VAPID secret ARN is missing"
    existing_secret=$(aws secretsmanager get-secret-value --profile managed --secret-id "$secret_arn" --query SecretString --output text 2>/dev/null || echo "")
    if [ -n "$existing_secret" ] && [ "$existing_secret" != "None" ] && [ "$existing_secret" != "null" ]; then
      public_key=$(printf '%s' "$existing_secret" | jq -r '.public_key // empty')
      private_key=$(printf '%s' "$existing_secret" | jq -r '.private_key // empty')
      subject=$(printf '%s' "$existing_secret" | jq -r '.subject // empty')
      created_at=$(printf '%s' "$existing_secret" | jq -r '.created_at // empty')
    fi
    if [ -n "$subject" ] && [ "$subject" != "null" ]; then
      resolved_subject="$subject"
    else
      resolved_subject="$(vapid_resolve_subject)"
    fi
    if [ -z "${public_key:-}" ] || [ -z "${private_key:-}" ]; then
      # Secret exists but lacks key material; regenerate in place, preserving
      # the subject. This is not a rotation of a healthy secret.
      public_key="$(generate_vapid_pair "$priv_path" "$pub_der_path")"
      jq -n \
        --rawfile priv "$priv_path" \
        --arg pub "$public_key" \
        --arg sub "$resolved_subject" \
        --arg created "${created_at:-$(date -u +"%Y-%m-%dT%H:%M:%SZ")}" \
        --arg now "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
        '{public_key:$pub, private_key:$priv, subject:$sub, created_at:$created, updated_at:$now}' \
        > "$payload_path"
      aws secretsmanager put-secret-value --profile managed --secret-id "$secret_arn" --secret-string "file://$payload_path" >/dev/null
    fi
    reused="true"
  else
    if ! grep -q "ResourceNotFoundException" "$desc_err"; then
      cat "$desc_err" >&2
      fail "failed to describe VAPID secret"
    fi
    resolved_subject="$(vapid_resolve_subject)"
    public_key="$(generate_vapid_pair "$priv_path" "$pub_der_path")"
    jq -n \
      --rawfile priv "$priv_path" \
      --arg pub "$public_key" \
      --arg sub "$resolved_subject" \
      --arg now "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
      '{public_key:$pub, private_key:$priv, subject:$sub, created_at:$now, updated_at:$now}' \
      > "$payload_path"
    secret_arn=$(aws secretsmanager create-secret --profile managed \
      --name "$(vapid_secret_name)" \
      --description "VAPID key pair for ${STAGE} (managed by lesser-host provision runner)" \
      --secret-string "file://$payload_path" \
      --tags \
        Key=lesser-host:managed,Value=true \
        Key=lesser-host:control-plane-stage,Value="$(managed_instance_key_stage)" \
      --query ARN --output text)
    test -n "$secret_arn" && test "$secret_arn" != "None" && test "$secret_arn" != "null" || fail "VAPID secret create returned empty ARN"
    reused="false"
  fi

  rm -f "$payload_path" "$desc_err" "$priv_path" "$pub_der_path"

  test -n "$secret_arn" && test "$secret_arn" != "None" && test "$secret_arn" != "null" || fail "failed to resolve VAPID secret ARN"
  case "$secret_arn" in arn:*) ;; *) fail "VAPID_SECRET_ARN must start with arn:";; esac
  if [ -z "${public_key:-}" ] || [ "$public_key" = "null" ]; then
    public_key=$(aws secretsmanager get-secret-value --profile managed --secret-id "$secret_arn" --query SecretString --output text | jq -r '.public_key')
  fi
  if [ -z "${resolved_subject:-}" ]; then resolved_subject="$(vapid_resolve_subject)"; fi

  export VAPID_SECRET_ARN="$secret_arn"
  export VAPID_PUBLIC_KEY="$public_key"
  export VAPID_SUBJECT="$resolved_subject"
  export VAPID_RECEIPT_PATH
  write_vapid_key_receipt "$VAPID_RECEIPT_PATH" "$secret_arn" "$public_key" "$resolved_subject" "$reused"
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
