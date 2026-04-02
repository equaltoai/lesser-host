#!/usr/bin/env bash
# Build phase for RUN_MODE=lesser-mcp (wire MCP onto existing Lesser API).
# The CLI binary and all release assets are in LESSER_RELEASE_DIR, prepared by build.sh.

echo "Wiring POST /mcp/{actor} on existing Lesser API domain..."

CONSENT_MESSAGE=""
if [ -n "${CONSENT_MESSAGE_B64:-}" ]; then CONSENT_MESSAGE=$(printf "%s" "$CONSENT_MESSAGE_B64" | base64 --decode); fi
PROVISION_INPUT="$STATE_DIR/provision.json"
: "${LESSER_HOST_URL:?LESSER_HOST_URL is required}"
LESSER_HOST_ATTESTATIONS_URL="${LESSER_HOST_ATTESTATIONS_URL:-$LESSER_HOST_URL}"
: "${LESSER_HOST_INSTANCE_KEY_ARN:?LESSER_HOST_INSTANCE_KEY_ARN is required}"
validate_https_custom_domain "LESSER_HOST_URL" "$LESSER_HOST_URL"
validate_https_custom_domain "LESSER_HOST_ATTESTATIONS_URL" "$LESSER_HOST_ATTESTATIONS_URL"
case "$LESSER_HOST_INSTANCE_KEY_ARN" in arn:*) ;; *) fail "LESSER_HOST_INSTANCE_KEY_ARN must start with arn:";; esac
if bool_on "${TIP_ENABLED:-}"; then
  if [ -z "${TIP_CHAIN_ID:-}" ]; then fail "TIP_CHAIN_ID is required when TIP_ENABLED=true"; fi
  case "$TIP_CHAIN_ID" in *[!0-9]*|"") fail "TIP_CHAIN_ID must be a positive integer when TIP_ENABLED=true";; 0) fail "TIP_CHAIN_ID must be > 0 when TIP_ENABLED=true";; esac
  if [ -z "${TIP_CONTRACT_ADDRESS:-}" ]; then fail "TIP_CONTRACT_ADDRESS is required when TIP_ENABLED=true"; fi
fi
jq -n --arg slug "$APP_SLUG" --arg stage "$STAGE" --arg admin_wallet_address "$ADMIN_WALLET_ADDRESS" --arg admin_username "$ADMIN_USERNAME" --arg admin_wallet_chain_id "${ADMIN_WALLET_CHAIN_ID:-}" --arg consent_message "$CONSENT_MESSAGE" --arg consent_signature "${CONSENT_SIGNATURE:-}" --arg lesser_host_url "${LESSER_HOST_URL:-}" --arg lesser_host_attestations_url "${LESSER_HOST_ATTESTATIONS_URL:-}" --arg lesser_host_instance_key_arn "${LESSER_HOST_INSTANCE_KEY_ARN:-}" --arg translation_enabled "${TRANSLATION_ENABLED:-}" --arg tip_enabled "${TIP_ENABLED:-}" --arg tip_chain_id "${TIP_CHAIN_ID:-}" --arg tip_contract_address "${TIP_CONTRACT_ADDRESS:-}" --arg ai_enabled "${AI_ENABLED:-}" --arg ai_moderation_enabled "${AI_MODERATION_ENABLED:-}" --arg ai_nsfw_detection_enabled "${AI_NSFW_DETECTION_ENABLED:-}" --arg ai_spam_detection_enabled "${AI_SPAM_DETECTION_ENABLED:-}" --arg ai_pii_detection_enabled "${AI_PII_DETECTION_ENABLED:-}" --arg ai_content_detection_enabled "${AI_CONTENT_DETECTION_ENABLED:-}" 'def bool($v): ($v|ascii_downcase) as $x | ($x=="true" or $x=="1" or $x=="yes" or $x=="on"); {"schema":1,"slug":$slug,"stage":$stage,"admin_wallet_address":$admin_wallet_address,"admin_username":$admin_username} | if $admin_wallet_chain_id != "" then .admin_wallet_chain_id = ($admin_wallet_chain_id|tonumber) else . end | if $consent_message != "" then .consent_message = $consent_message else . end | if $consent_signature != "" then .consent_signature = $consent_signature else . end | if $lesser_host_url != "" then .lesser_host_url = $lesser_host_url else . end | if $lesser_host_attestations_url != "" then .lesser_host_attestations_url = $lesser_host_attestations_url elif $lesser_host_url != "" then .lesser_host_attestations_url = $lesser_host_url else . end | if $lesser_host_instance_key_arn != "" then .lesser_host_instance_key_arn = $lesser_host_instance_key_arn else . end | if $translation_enabled != "" then .translation_enabled = bool($translation_enabled) else . end | if $tip_enabled != "" then .tip_enabled = bool($tip_enabled) else . end | if $tip_chain_id != "" then .tip_chain_id = ($tip_chain_id|tonumber) else . end | if $tip_contract_address != "" then .tip_contract_address = $tip_contract_address else . end | if $ai_enabled != "" then .ai_enabled = bool($ai_enabled) else . end | if $ai_moderation_enabled != "" then .ai_moderation_enabled = bool($ai_moderation_enabled) else . end | if $ai_nsfw_detection_enabled != "" then .ai_nsfw_detection_enabled = bool($ai_nsfw_detection_enabled) else . end | if $ai_spam_detection_enabled != "" then .ai_spam_detection_enabled = bool($ai_spam_detection_enabled) else . end | if $ai_pii_detection_enabled != "" then .ai_pii_detection_enabled = bool($ai_pii_detection_enabled) else . end | if $ai_content_detection_enabled != "" then .ai_content_detection_enabled = bool($ai_content_detection_enabled) else . end' > "$PROVISION_INPUT"

(
  cd "$LESSER_CHECKOUT_DIR"
  "$LESSER_RELEASE_DIR/lesser" up --app "$APP_SLUG" --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT" --release-dir "$LESSER_RELEASE_DIR"
)
enable_agents

MCP_BODY_PARAM="/$APP_SLUG/$STAGE/lesser-body/exports/v1/mcp_lambda_arn"
MCP_LAMBDA_ARN=$(aws ssm get-parameter --profile managed --name "$MCP_BODY_PARAM" --query "Parameter.Value" --output text)
test -n "$MCP_LAMBDA_ARN" && test "$MCP_LAMBDA_ARN" != "null"

MCP_RECEIPT_PATH="$STATE_DIR/mcp-state.json"
LAMBDA_METADATA_PATH="$STATE_DIR/deploy/lambda-assets/metadata.json"
test -f "$LAMBDA_METADATA_PATH"
jq -n --arg stage "$STAGE" --arg base_domain "$BASE_DOMAIN" --arg mcp_url "https://api.$STAGE_DOMAIN/mcp/{actor}" --arg mcp_lambda_arn "$MCP_LAMBDA_ARN" --arg lesser_body_version "${LESSER_BODY_VERSION:-}" --slurpfile release "$LESSER_RELEASE_DIR/lesser-release.json" --slurpfile bundle "$LESSER_RELEASE_DIR/lesser-lambda-bundle.json" --slurpfile metadata "$LAMBDA_METADATA_PATH" '({version:1,stage:$stage,base_domain:$base_domain,mcp_url:$mcp_url,mcp_lambda_arn:$mcp_lambda_arn} + (if $lesser_body_version != "" then {lesser_body_version:$lesser_body_version} else {} end) + {managed_deploy_artifacts:{mode:($metadata[0].mode // "release"),checksums_path:"checksums.txt",release_manifest_path:"lesser-release.json",release:{name:($release[0].name // ""),version:($release[0].version // ""),git_sha:($release[0].git_sha // "")},deploy_artifact:{kind:"lambda_bundle",path:($bundle[0].bundle.path // ""),manifest_path:"lesser-lambda-bundle.json",files:($metadata[0].files // ($bundle[0].files | map(.path))),prepared_at:($metadata[0].prepared_at // "")}}})' > "$MCP_RECEIPT_PATH"
aws s3 cp "$MCP_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"
