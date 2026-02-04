#!/usr/bin/env bash
set -euo pipefail

STAGE="${STAGE:-lab}"
STACK_NAME="lesser-host-${STAGE}"

if ! command -v aws >/dev/null 2>&1; then
  echo "error: aws CLI is required" >&2
  exit 1
fi

REGION="${AWS_REGION:-$(aws configure get region || true)}"
if [[ -z "${REGION}" ]]; then
  REGION="us-east-1"
fi

echo "Stack: ${STACK_NAME}"
echo "Region: ${REGION}"

get_output() {
  local key="$1"
  aws cloudformation describe-stacks \
    --stack-name "${STACK_NAME}" \
    --query "Stacks[0].Outputs[?OutputKey=='${key}'].OutputValue | [0]" \
    --output text
}

STATE_TABLE_NAME="$(get_output StateTableName)"
ARTIFACTS_BUCKET_NAME="$(get_output ArtifactsBucketName)"
RENDER_WORKER_FUNCTION_NAME="$(get_output RenderWorkerFunctionName)"

if [[ -z "${STATE_TABLE_NAME}" || "${STATE_TABLE_NAME}" == "None" ]]; then
  echo "error: missing StateTableName output" >&2
  exit 1
fi
if [[ -z "${ARTIFACTS_BUCKET_NAME}" || "${ARTIFACTS_BUCKET_NAME}" == "None" ]]; then
  echo "error: missing ArtifactsBucketName output" >&2
  exit 1
fi
if [[ -z "${RENDER_WORKER_FUNCTION_NAME}" || "${RENDER_WORKER_FUNCTION_NAME}" == "None" ]]; then
  echo "error: missing RenderWorkerFunctionName output" >&2
  exit 1
fi

NONCE="$(node -e "console.log(require('crypto').randomBytes(8).toString('hex'))")"
NORMALIZED_URL="https://example.com/?lh_sweep_test=${NONCE}"
RENDER_ID="$(printf "v1:%s" "${NORMALIZED_URL}" | sha256sum | awk '{print $1}')"

THUMB_KEY="renders/${RENDER_ID}/thumbnail.jpg"
SNAP_KEY="renders/${RENDER_ID}/snapshot.txt"

NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
EXPIRES_AT="$(date -u -d '1 day ago' +%Y-%m-%dT%H:%M:%SZ)"
TTL="$(date -u -d "${EXPIRES_AT} + 7 days" +%s)"
GSI1SK="${EXPIRES_AT}#${RENDER_ID}"

echo "RenderID: ${RENDER_ID}"
echo "ExpiresAt: ${EXPIRES_AT} (TTL ${TTL})"

thumb_file="$(mktemp)"
snap_file="$(mktemp)"
trap 'rm -f "${thumb_file}" "${snap_file}" >/dev/null 2>&1 || true' EXIT

printf "test" > "${thumb_file}"
printf "snapshot for %s\n" "${NORMALIZED_URL}" > "${snap_file}"

echo "Uploading S3 objects..."
aws s3api put-object --bucket "${ARTIFACTS_BUCKET_NAME}" --key "${THUMB_KEY}" --body "${thumb_file}" --content-type "image/jpeg" >/dev/null
aws s3api put-object --bucket "${ARTIFACTS_BUCKET_NAME}" --key "${SNAP_KEY}" --body "${snap_file}" --content-type "text/plain; charset=utf-8" >/dev/null

echo "Creating DynamoDB render artifact record..."
aws dynamodb put-item \
  --table-name "${STATE_TABLE_NAME}" \
  --item "{
    \"PK\": {\"S\": \"RENDER#${RENDER_ID}\"},
    \"SK\": {\"S\": \"ARTIFACT\"},
    \"ttl\": {\"N\": \"${TTL}\"},
    \"gsi1PK\": {\"S\": \"RENDER_EXPIRES\"},
    \"gsi1SK\": {\"S\": \"${GSI1SK}\"},
    \"id\": {\"S\": \"${RENDER_ID}\"},
    \"policyVersion\": {\"S\": \"v1\"},
    \"normalizedUrl\": {\"S\": \"${NORMALIZED_URL}\"},
    \"thumbnailObjectKey\": {\"S\": \"${THUMB_KEY}\"},
    \"thumbnailContentType\": {\"S\": \"image/jpeg\"},
    \"snapshotObjectKey\": {\"S\": \"${SNAP_KEY}\"},
    \"snapshotContentType\": {\"S\": \"text/plain; charset=utf-8\"},
    \"textPreview\": {\"S\": \"retention sweep test\"},
    \"retentionClass\": {\"S\": \"benign\"},
    \"createdAt\": {\"S\": \"${NOW}\"},
    \"renderedAt\": {\"S\": \"${NOW}\"},
    \"expiresAt\": {\"S\": \"${EXPIRES_AT}\"}
  }" >/dev/null

echo "Invoking retention sweep..."
aws lambda invoke \
  --function-name "${RENDER_WORKER_FUNCTION_NAME}" \
  --payload "{\"version\":\"0\",\"id\":\"lh-sweep\",\"detail-type\":\"Scheduled Event\",\"source\":\"aws.events\",\"account\":\"000000000000\",\"time\":\"${NOW}\",\"region\":\"${REGION}\",\"resources\":[],\"detail\":{}}" \
  --cli-binary-format raw-in-base64-out \
  /tmp/lh-retention-sweep.json >/dev/null

echo "Waiting for deletion (eventual consistency)..."
for i in $(seq 1 15); do
  item="$(aws dynamodb get-item --table-name "${STATE_TABLE_NAME}" --key "{\"PK\":{\"S\":\"RENDER#${RENDER_ID}\"},\"SK\":{\"S\":\"ARTIFACT\"}}" --query "Item.PK.S" --output text 2>/dev/null || true)"
  if [[ "${item}" == "None" || -z "${item}" ]]; then
    break
  fi
  sleep 2
done

echo "Verifying DynamoDB record deleted..."
item="$(aws dynamodb get-item --table-name "${STATE_TABLE_NAME}" --key "{\"PK\":{\"S\":\"RENDER#${RENDER_ID}\"},\"SK\":{\"S\":\"ARTIFACT\"}}" --query "Item.PK.S" --output text 2>/dev/null || true)"
if [[ "${item}" != "None" && -n "${item}" ]]; then
  echo "error: DynamoDB record still exists" >&2
  exit 1
fi

echo "Verifying S3 objects deleted..."
if aws s3api head-object --bucket "${ARTIFACTS_BUCKET_NAME}" --key "${THUMB_KEY}" >/dev/null 2>&1; then
  echo "error: thumbnail object still exists" >&2
  exit 1
fi
if aws s3api head-object --bucket "${ARTIFACTS_BUCKET_NAME}" --key "${SNAP_KEY}" >/dev/null 2>&1; then
  echo "error: snapshot object still exists" >&2
  exit 1
fi

echo "ok: retention sweep deleted DynamoDB + S3 artifacts"

