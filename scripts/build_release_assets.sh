#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <version> [out-dir]" >&2
  exit 1
fi

VERSION="$1"
OUT_DIR="${2:-dist/release}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

if [[ -z "${VERSION}" ]]; then
  echo "version is required" >&2
  exit 1
fi
if [[ "${VERSION}" != v* ]]; then
  echo "version must start with 'v' (for example: v1.0.0)" >&2
  exit 1
fi

mkdir -p "${OUT_DIR}"

OPENAPI_SRC="docs/contracts/openapi.yaml"
SSE_CONTRACT_SRC="docs/contracts/soul-mint-conversation-sse.json"
SPEC_SRC_DIR="docs/spec/v3"

if [[ ! -f "${OPENAPI_SRC}" ]]; then
  echo "missing ${OPENAPI_SRC}" >&2
  exit 1
fi
if [[ ! -f "${SSE_CONTRACT_SRC}" ]]; then
  echo "missing ${SSE_CONTRACT_SRC}" >&2
  exit 1
fi
if [[ ! -d "${SPEC_SRC_DIR}/schemas" ]]; then
  echo "missing ${SPEC_SRC_DIR}/schemas" >&2
  exit 1
fi
if [[ ! -d "${SPEC_SRC_DIR}/fixtures" ]]; then
  echo "missing ${SPEC_SRC_DIR}/fixtures" >&2
  exit 1
fi

GIT_SHA="$(git rev-parse --verify HEAD)"

# Publish a release-local OpenAPI file whose refs resolve against the packaged `spec/v3/*` bundle.
sed 's#\.\./spec/v3/#spec/v3/#g' "${OPENAPI_SRC}" > "${OUT_DIR}/openapi.yaml"
cp "${SSE_CONTRACT_SRC}" "${OUT_DIR}/soul-mint-conversation-sse.json"

tar -czf "${OUT_DIR}/lesser-host-contracts-v3.tgz" \
  -C docs \
  spec/v3/README.md \
  spec/v3/schemas \
  spec/v3/fixtures

cat > "${OUT_DIR}/LESSER_HOST_REF.txt" <<TXT
tag: ${VERSION}
commit: ${GIT_SHA}
TXT

(
  cd "${OUT_DIR}"
  sha256sum openapi.yaml soul-mint-conversation-sse.json lesser-host-contracts-v3.tgz LESSER_HOST_REF.txt > checksums.txt
)

echo "Wrote contract release assets to ${OUT_DIR}"
