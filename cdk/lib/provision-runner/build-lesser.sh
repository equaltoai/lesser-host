#!/usr/bin/env bash
# Build phase for RUN_MODE=lesser (full Lesser deployment).
# The CLI binary and all release assets are in LESSER_RELEASE_DIR, prepared by build.sh.

CONSENT_MESSAGE=""
if [ -n "${CONSENT_MESSAGE_B64:-}" ]; then CONSENT_MESSAGE=$(printf "%s" "$CONSENT_MESSAGE_B64" | base64 --decode); fi
PROVISION_INPUT="$STATE_DIR/provision.json"
: "${LESSER_HOST_URL:?LESSER_HOST_URL is required}"
LESSER_HOST_ATTESTATIONS_URL="${LESSER_HOST_ATTESTATIONS_URL:-$LESSER_HOST_URL}"
: "${LESSER_HOST_INSTANCE_KEY_ARN:?LESSER_HOST_INSTANCE_KEY_ARN is required}"
validate_https_custom_domain "LESSER_HOST_URL" "$LESSER_HOST_URL"
validate_https_custom_domain "LESSER_HOST_ATTESTATIONS_URL" "$LESSER_HOST_ATTESTATIONS_URL"
case "$LESSER_HOST_INSTANCE_KEY_ARN" in arn:*) ;; *) fail "LESSER_HOST_INSTANCE_KEY_ARN must start with arn:";; esac
: "${SOUL_BINDING_INTEGRATION_KEY_ARN:?SOUL_BINDING_INTEGRATION_KEY_ARN is required}"
case "$SOUL_BINDING_INTEGRATION_KEY_ARN" in arn:*) ;; *) fail "SOUL_BINDING_INTEGRATION_KEY_ARN must start with arn:";; esac
if bool_on "${TIP_ENABLED:-}"; then
  if [ -z "${TIP_CHAIN_ID:-}" ]; then fail "TIP_CHAIN_ID is required when TIP_ENABLED=true"; fi
  case "$TIP_CHAIN_ID" in *[!0-9]*|"") fail "TIP_CHAIN_ID must be a positive integer when TIP_ENABLED=true";; 0) fail "TIP_CHAIN_ID must be > 0 when TIP_ENABLED=true";; esac
  if [ -z "${TIP_CONTRACT_ADDRESS:-}" ]; then fail "TIP_CONTRACT_ADDRESS is required when TIP_ENABLED=true"; fi
  printf "%s" "$TIP_CONTRACT_ADDRESS" | grep -Eq '^0x[0-9a-fA-F]{40}$' || fail "TIP_CONTRACT_ADDRESS must be a 20-byte EVM address when TIP_ENABLED=true"
fi
jq -n --arg slug "$APP_SLUG" --arg stage "$STAGE" --arg admin_wallet_address "$ADMIN_WALLET_ADDRESS" --arg admin_username "$ADMIN_USERNAME" --arg admin_wallet_chain_id "${ADMIN_WALLET_CHAIN_ID:-}" --arg consent_message "$CONSENT_MESSAGE" --arg consent_signature "${CONSENT_SIGNATURE:-}" --arg lesser_host_url "${LESSER_HOST_URL:-}" --arg lesser_host_attestations_url "${LESSER_HOST_ATTESTATIONS_URL:-}" --arg lesser_host_instance_key_arn "${LESSER_HOST_INSTANCE_KEY_ARN:-}" --arg soul_binding_integration_key_arn "${SOUL_BINDING_INTEGRATION_KEY_ARN:-}" --arg translation_enabled "${TRANSLATION_ENABLED:-}" --arg tip_enabled "${TIP_ENABLED:-}" --arg tip_chain_id "${TIP_CHAIN_ID:-}" --arg tip_contract_address "${TIP_CONTRACT_ADDRESS:-}" --arg ai_enabled "${AI_ENABLED:-}" --arg ai_moderation_enabled "${AI_MODERATION_ENABLED:-}" --arg ai_nsfw_detection_enabled "${AI_NSFW_DETECTION_ENABLED:-}" --arg ai_spam_detection_enabled "${AI_SPAM_DETECTION_ENABLED:-}" --arg ai_pii_detection_enabled "${AI_PII_DETECTION_ENABLED:-}" --arg ai_content_detection_enabled "${AI_CONTENT_DETECTION_ENABLED:-}" 'def bool($v): ($v|ascii_downcase) as $x | ($x=="true" or $x=="1" or $x=="yes" or $x=="on"); {"schema":1,"slug":$slug,"stage":$stage,"admin_wallet_address":$admin_wallet_address,"admin_username":$admin_username} | if $admin_wallet_chain_id != "" then .admin_wallet_chain_id = ($admin_wallet_chain_id|tonumber) else . end | if $consent_message != "" then .consent_message = $consent_message else . end | if $consent_signature != "" then .consent_signature = $consent_signature else . end | if $lesser_host_url != "" then .lesser_host_url = $lesser_host_url else . end | if $lesser_host_attestations_url != "" then .lesser_host_attestations_url = $lesser_host_attestations_url elif $lesser_host_url != "" then .lesser_host_attestations_url = $lesser_host_url else . end | if $lesser_host_instance_key_arn != "" then .lesser_host_instance_key_arn = $lesser_host_instance_key_arn else . end | if $soul_binding_integration_key_arn != "" then .soul_binding_integration_key_arn = $soul_binding_integration_key_arn else . end | if $translation_enabled != "" then .translation_enabled = bool($translation_enabled) else . end | if $tip_enabled != "" then .tip_enabled = bool($tip_enabled) else . end | if $tip_chain_id != "" then .tip_chain_id = ($tip_chain_id|tonumber) else . end | if $tip_contract_address != "" then .tip_contract_address = $tip_contract_address else . end | if $ai_enabled != "" then .ai_enabled = bool($ai_enabled) else . end | if $ai_moderation_enabled != "" then .ai_moderation_enabled = bool($ai_moderation_enabled) else . end | if $ai_nsfw_detection_enabled != "" then .ai_nsfw_detection_enabled = bool($ai_nsfw_detection_enabled) else . end | if $ai_spam_detection_enabled != "" then .ai_spam_detection_enabled = bool($ai_spam_detection_enabled) else . end | if $ai_pii_detection_enabled != "" then .ai_pii_detection_enabled = bool($ai_pii_detection_enabled) else . end | if $ai_content_detection_enabled != "" then .ai_content_detection_enabled = bool($ai_content_detection_enabled) else . end' > "$PROVISION_INPUT"

(
  cd "$LESSER_CHECKOUT_DIR"
  "$LESSER_RELEASE_DIR/lesser" up --app "$APP_SLUG" --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT" --release-dir "$LESSER_RELEASE_DIR"
)
if [ -n "${CONSENT_MESSAGE_B64:-}" ] && [ -n "${CONSENT_SIGNATURE:-}" ]; then
  (
    cd "$LESSER_CHECKOUT_DIR"
    "$LESSER_RELEASE_DIR/lesser" init-admin --base-domain "$BASE_DOMAIN" --aws-profile managed --provisioning-input "$PROVISION_INPUT"
  )
else
  echo "Skipping init-admin (missing consent message/signature)."
fi
if bool_on "${MANAGED_ENABLE_AGENT_REGISTRATION:-}"; then
  enable_agents
else
  echo "Skipping managed agent-registration enablement (MANAGED_ENABLE_AGENT_REGISTRATION is not enabled)."
fi

RECEIPT_PATH="$STATE_DIR/state.json"
test -f "$RECEIPT_PATH"
LAMBDA_METADATA_PATH="$STATE_DIR/deploy/lambda-assets/metadata.json"
if [ ! -f "$LAMBDA_METADATA_PATH" ]; then
  mkdir -p "$(dirname "$LAMBDA_METADATA_PATH")"
  jq -n --slurpfile bundle "$LESSER_RELEASE_DIR/lesser-lambda-bundle.json" '{mode:"release",files:($bundle[0].files | map(.path)),prepared_at:""}' > "$LAMBDA_METADATA_PATH"
fi
MANAGED_RECEIPT_PATH="$STATE_DIR/state.managed.json"
jq --slurpfile release "$LESSER_RELEASE_DIR/lesser-release.json" --slurpfile bundle "$LESSER_RELEASE_DIR/lesser-lambda-bundle.json" --slurpfile metadata "$LAMBDA_METADATA_PATH" --slurpfile instance_key "$MANAGED_INSTANCE_KEY_RECEIPT_PATH" --slurpfile soul_binding "$SOUL_BINDING_INTEGRATION_RECEIPT_PATH" '. + {managed_instance_key:$instance_key[0],soul_binding_integration:$soul_binding[0],managed_deploy_artifacts:{mode:($metadata[0].mode // "release"),checksums_path:"checksums.txt",release_manifest_path:"lesser-release.json",release:{name:($release[0].name // ""),version:($release[0].version // ""),git_sha:($release[0].git_sha // "")},deploy_artifact:{kind:"lambda_bundle",path:($bundle[0].bundle.path // ""),manifest_path:"lesser-lambda-bundle.json",files:(if (($metadata[0].files // []) | length) > 0 then $metadata[0].files else ($bundle[0].files | map(.path)) end),prepared_at:($metadata[0].prepared_at // "")}}}' "$RECEIPT_PATH" > "$MANAGED_RECEIPT_PATH"
aws s3 cp "$MANAGED_RECEIPT_PATH" "s3://$ARTIFACT_BUCKET/$RECEIPT_S3_KEY"
if [ -f /tmp/bootstrap.json ]; then aws s3 cp /tmp/bootstrap.json "s3://$ARTIFACT_BUCKET/$BOOTSTRAP_S3_KEY"; fi
