#!/usr/bin/env bash
# Build phase for RUN_MODE=lesser-body (AgentCore MCP deployment).
# Expects helpers.sh to be sourced before this script runs.

echo "Deploying lesser-body (AgentCore MCP)..."
: "${APP_SLUG:?APP_SLUG is required}"
: "${BASE_DOMAIN:?BASE_DOMAIN is required}"
: "${STAGE:?STAGE is required}"
: "${TARGET_ACCOUNT_ID:?TARGET_ACCOUNT_ID is required}"
: "${TARGET_REGION:?TARGET_REGION is required}"
: "${ARTIFACT_BUCKET:?ARTIFACT_BUCKET is required}"
: "${RECEIPT_S3_KEY:?RECEIPT_S3_KEY is required}"

STATE_DIR="$HOME/.lesser/$APP_SLUG/$BASE_DOMAIN"
mkdir -p "$STATE_DIR"
STAGE_DOMAIN="$BASE_DOMAIN"
if [ "$STAGE" != "live" ]; then STAGE_DOMAIN="$STAGE.$BASE_DOMAIN"; fi

BODY_OWNER="${LESSER_BODY_GITHUB_OWNER:-equaltoai}"
BODY_REPO="${LESSER_BODY_GITHUB_REPO:-lesser-body}"
BODY_TAG="${LESSER_BODY_VERSION:-}"
BODY_TAG_NORMALIZED=$(printf "%s" "$BODY_TAG" | tr "[:upper:]" "[:lower:]")
if [ -z "$BODY_TAG" ] || [ "$BODY_TAG_NORMALIZED" = "latest" ]; then
  echo "Resolving latest lesser-body release..."
  BODY_TAG=$(resolve_latest_release_tag "$BODY_OWNER" "$BODY_REPO")
fi
test -n "$BODY_TAG" && test "$BODY_TAG" != "null"
echo "Using lesser-body release: $BODY_TAG"
BODY_RELEASE_DIR="$(pwd)/lesser-body-release"
prepare_lesser_body_release_dir "$BODY_RELEASE_DIR" "$BODY_OWNER" "$BODY_REPO" "$BODY_TAG" "$STAGE"
BODY_STACK_NAME="$APP_SLUG-$STAGE-lesser-body"
BODY_TEMPLATE_PATH="lesser-body-managed-$STAGE.template.json"
BODY_ASSET_BUCKET="cdk-hnb659fds-assets-$TARGET_ACCOUNT_ID-$TARGET_REGION"
BODY_ASSET_PREFIX="managed/lesser-body/$APP_SLUG/$STAGE/$BODY_TAG"
BODY_TEMPLATE_CERT_PATH="$STATE_DIR/body-template-certification.json"
BODY_FAILURE_PATH="$STATE_DIR/body-failure.json"
BODY_TEMPLATE_CERT_LOG="$STATE_DIR/body-template-certification.log"
BODY_DEPLOY_LOG="$STATE_DIR/body-deploy.log"
rm -f "$BODY_TEMPLATE_CERT_PATH" "$BODY_FAILURE_PATH" "$BODY_TEMPLATE_CERT_LOG" "$BODY_DEPLOY_LOG"

BODY_TEMPLATE_FILE="$BODY_RELEASE_DIR/$BODY_TEMPLATE_PATH"
patch_lesser_body_template_for_existing_stack "$BODY_STACK_NAME" "$APP_SLUG" "$STAGE" "$BODY_RELEASE_DIR" "$BODY_TEMPLATE_FILE"

BODY_TEMPLATE_CERTIFY="${BODY_TEMPLATE_CERTIFY:-}"
BODY_TEMPLATE_CERTIFY_NORMALIZED=$(printf "%s" "$BODY_TEMPLATE_CERTIFY" | tr "[:upper:]" "[:lower:]")
if [ "$BODY_TEMPLATE_CERTIFY_NORMALIZED" = "true" ] || [ "$BODY_TEMPLATE_CERTIFY_NORMALIZED" = "1" ] || [ "$BODY_TEMPLATE_CERTIFY_NORMALIZED" = "yes" ] || [ "$BODY_TEMPLATE_CERTIFY_NORMALIZED" = "on" ]; then
  echo "Certifying lesser-body CloudFormation changeset..."
  if run_lesser_body_helper_with_capture "$BODY_TEMPLATE_CERT_LOG" env AWS_PROFILE=managed bash "$BODY_RELEASE_DIR/deploy-lesser-body-from-release.sh" --stack-name "$BODY_STACK_NAME" --asset-bucket "$BODY_ASSET_BUCKET" --asset-prefix "$BODY_ASSET_PREFIX" --app "$APP_SLUG" --stage "$STAGE" --base-domain "$BASE_DOMAIN" --no-execute-changeset; then
    write_lesser_body_artifact "$BODY_TEMPLATE_CERT_PATH" "passed" "$BODY_TAG" "$BODY_TEMPLATE_PATH" "$BODY_STACK_NAME" "cloudformation_deploy_no_execute_changeset" "$BODY_TEMPLATE_CERT_LOG"
    upload_optional_artifact "$BODY_TEMPLATE_CERT_PATH" "${BODY_TEMPLATE_CERT_S3_KEY:-}"
  else
    write_lesser_body_artifact "$BODY_TEMPLATE_CERT_PATH" "failed" "$BODY_TAG" "$BODY_TEMPLATE_PATH" "$BODY_STACK_NAME" "cloudformation_deploy_no_execute_changeset" "$BODY_TEMPLATE_CERT_LOG"
    upload_optional_artifact "$BODY_TEMPLATE_CERT_PATH" "${BODY_TEMPLATE_CERT_S3_KEY:-}"
    write_lesser_body_artifact "$BODY_FAILURE_PATH" "failed" "$BODY_TAG" "$BODY_TEMPLATE_PATH" "$BODY_STACK_NAME" "cloudformation_deploy_no_execute_changeset" "$BODY_TEMPLATE_CERT_LOG"
    upload_optional_artifact "$BODY_FAILURE_PATH" "${BODY_FAILURE_S3_KEY:-}"
    fail "lesser-body template certification failed for $BODY_TEMPLATE_PATH"
  fi
else
  echo "Skipping lesser-body changeset certification (BODY_TEMPLATE_CERTIFY not enabled)."
  echo "Skipped: BODY_TEMPLATE_CERTIFY is not enabled." > "$BODY_TEMPLATE_CERT_LOG"
  write_lesser_body_artifact "$BODY_TEMPLATE_CERT_PATH" "skipped" "$BODY_TAG" "$BODY_TEMPLATE_PATH" "$BODY_STACK_NAME" "cloudformation_deploy_no_execute_changeset" "$BODY_TEMPLATE_CERT_LOG"
  upload_optional_artifact "$BODY_TEMPLATE_CERT_PATH" "${BODY_TEMPLATE_CERT_S3_KEY:-}"
fi
if ! run_lesser_body_helper_with_capture "$BODY_DEPLOY_LOG" env AWS_PROFILE=managed bash "$BODY_RELEASE_DIR/deploy-lesser-body-from-release.sh" --stack-name "$BODY_STACK_NAME" --asset-bucket "$BODY_ASSET_BUCKET" --asset-prefix "$BODY_ASSET_PREFIX" --app "$APP_SLUG" --stage "$STAGE" --base-domain "$BASE_DOMAIN"; then
  write_lesser_body_artifact "$BODY_FAILURE_PATH" "failed" "$BODY_TAG" "$BODY_TEMPLATE_PATH" "$BODY_STACK_NAME" "cloudformation_deploy" "$BODY_DEPLOY_LOG"
  upload_optional_artifact "$BODY_FAILURE_PATH" "${BODY_FAILURE_S3_KEY:-}"
  fail "lesser-body deploy helper failed for $BODY_TEMPLATE_PATH"
fi
BODY_PARAM="/$APP_SLUG/$STAGE/lesser-body/exports/v1/mcp_lambda_arn"
BODY_LAMBDA_ARN=$(aws ssm get-parameter --profile managed --name "$BODY_PARAM" --query "Parameter.Value" --output text)
test -n "$BODY_LAMBDA_ARN" && test "$BODY_LAMBDA_ARN" != "null"
BODY_RECEIPT_PATH="$STATE_DIR/body-state.json"
BODY_RELEASE_GIT_SHA=$(jq -r '.git_sha // empty' "$BODY_RELEASE_DIR/lesser-body-release.json")
jq -n --arg stage "$STAGE" --arg base_domain "$BASE_DOMAIN" --arg mcp_url "https://api.$STAGE_DOMAIN/mcp/{actor}" --arg version "$BODY_TAG" --arg mcp_lambda_arn "$BODY_LAMBDA_ARN" --arg release_git_sha "$BODY_RELEASE_GIT_SHA" --arg template_path "lesser-body-managed-$STAGE.template.json" '{version:1,stage:$stage,base_domain:$base_domain,mcp_url:$mcp_url,lesser_body_version:$version,mcp_lambda_arn:$mcp_lambda_arn,managed_deploy_artifacts:{mode:"release",checksums_path:"checksums.txt",release_manifest_path:"lesser-body-release.json",release:{name:"lesser-body",version:$version,git_sha:$release_git_sha,source_checkout_required:false,npm_install_required:false},deploy_artifact:{kind:"lesser_body_managed_deploy",path:"lesser-body.zip",manifest_path:"lesser-body-deploy.json",script_path:"deploy-lesser-body-from-release.sh",template_path:$template_path}}}' > "$BODY_RECEIPT_PATH"
aws s3 cp "$BODY_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"
