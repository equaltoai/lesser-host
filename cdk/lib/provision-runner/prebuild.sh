#!/usr/bin/env bash
# Pre-build phase: assume IAM roles into the target managed account.
set -euo pipefail

echo "Assuming managed-instance role in target account..."
CREDS=$(aws sts assume-role --role-arn "arn:aws:iam::$TARGET_ACCOUNT_ID:role/$TARGET_ROLE_NAME" --role-session-name "lesser-host-$APP_SLUG" --duration-seconds 3600 --query "Credentials.[AccessKeyId,SecretAccessKey,SessionToken]" --output text)
read MANAGED_AK MANAGED_SK MANAGED_TOKEN <<< "$CREDS"
mkdir -p ~/.aws
printf "[managed]\naws_access_key_id=%s\naws_secret_access_key=%s\naws_session_token=%s\n" "$MANAGED_AK" "$MANAGED_SK" "$MANAGED_TOKEN" > ~/.aws/credentials
printf "[profile managed]\nregion=%s\noutput=json\n" "$TARGET_REGION" > ~/.aws/config
aws sts get-caller-identity --profile managed
