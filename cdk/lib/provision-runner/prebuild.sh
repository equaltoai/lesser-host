#!/usr/bin/env bash
# Pre-build phase: assume IAM roles into the target managed account.
set -euo pipefail

echo "Assuming managed-instance role in target account..."
ASSUME_ERR=$(mktemp)
CREDS=""
for attempt in $(seq 1 12); do
  if CREDS=$(aws sts assume-role --role-arn "arn:aws:iam::$TARGET_ACCOUNT_ID:role/$TARGET_ROLE_NAME" --role-session-name "lesser-host-$APP_SLUG" --duration-seconds 3600 --query "Credentials.[AccessKeyId,SecretAccessKey,SessionToken]" --output text 2>"$ASSUME_ERR"); then
    break
  fi
  status=$?
  if [ "$attempt" -ge 12 ]; then
    cat "$ASSUME_ERR" >&2
    rm -f "$ASSUME_ERR"
    exit "$status"
  fi
  echo "Managed-instance role not assumable yet (attempt $attempt/12); retrying..."
  sleep 5
done
rm -f "$ASSUME_ERR"
read MANAGED_AK MANAGED_SK MANAGED_TOKEN <<< "$CREDS"
mkdir -p ~/.aws
printf "[managed]\naws_access_key_id=%s\naws_secret_access_key=%s\naws_session_token=%s\n" "$MANAGED_AK" "$MANAGED_SK" "$MANAGED_TOKEN" > ~/.aws/credentials
printf "[profile managed]\nregion=%s\noutput=json\n" "$TARGET_REGION" > ~/.aws/config
MANAGED_CALLER_ACCOUNT=$(aws sts get-caller-identity --profile managed --query Account --output text)
test "$MANAGED_CALLER_ACCOUNT" = "$TARGET_ACCOUNT_ID" || {
  echo "ERROR: managed profile resolved account $MANAGED_CALLER_ACCOUNT, expected $TARGET_ACCOUNT_ID" >&2
  exit 1
}
aws sts get-caller-identity --profile managed
