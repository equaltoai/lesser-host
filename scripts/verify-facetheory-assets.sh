#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: verify-facetheory-assets.sh <base-url>" >&2
}

if [ "$#" -ne 1 ]; then
  usage
  exit 2
fi

base_url="${1%/}"
case "$base_url" in
  http://*|https://*) ;;
  *)
    echo "base-url must start with http:// or https://" >&2
    exit 2
    ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

root_headers="$tmp_dir/root.headers"
root_body="$tmp_dir/root.html"
curl -fsS -D "$root_headers" -o "$root_body" "$base_url/"

mapfile -t assets < <(python3 - "$root_body" <<'PY'
import re
import sys
from pathlib import Path

html = Path(sys.argv[1]).read_text(encoding="utf-8")
seen = set()
for match in re.finditer(r'(?:href|src)="([^"]+)"', html):
    url = match.group(1)
    if not url.startswith("/assets/") or url in seen:
        continue
    seen.add(url)
    print(url)
PY
)

if [ "${#assets[@]}" -eq 0 ]; then
  echo "no /assets/* references found in SSR shell" >&2
  exit 1
fi

for asset_path in "${assets[@]}"; do
  request_id="facetheory-asset-smoke-${RANDOM}-${RANDOM}"
  headers="$tmp_dir/asset.headers"
  body="$tmp_dir/asset.body"
  curl -sS -H "x-request-id: ${request_id}" \
    -D "$headers" \
    -o "$body" \
    "${base_url}${asset_path}?facetheory_asset_smoke=${RANDOM}${RANDOM}"

  status="$(awk 'tolower($0) ~ /^http\// { code=$2 } END { print code }' "$headers")"
  content_type="$(awk 'tolower($0) ~ /^content-type:/ { sub(/^[^:]+:[[:space:]]*/, ""); gsub(/\r/, ""); print; exit }' "$headers")"
  echoed_request_id="$(awk 'tolower($0) ~ /^x-request-id:/ { sub(/^[^:]+:[[:space:]]*/, ""); gsub(/\r/, ""); print; exit }' "$headers")"

  case "$status" in
    2*) ;;
    *)
      echo "${asset_path}: expected 2xx, got ${status:-missing}" >&2
      sed -n '1,20p' "$headers" >&2
      head -c 300 "$body" >&2 || true
      echo >&2
      exit 1
      ;;
  esac

  case "$asset_path" in
    *.css)
      case "$content_type" in
        text/css*) ;;
        *)
          echo "${asset_path}: expected text/css, got ${content_type:-missing}" >&2
          exit 1
          ;;
      esac
      ;;
    *.js|*.mjs)
      case "$content_type" in
        text/javascript*|application/javascript*) ;;
        *)
          echo "${asset_path}: expected JavaScript MIME, got ${content_type:-missing}" >&2
          exit 1
          ;;
      esac
      ;;
  esac

  if [ "$echoed_request_id" != "$request_id" ]; then
    echo "${asset_path}: expected AppTheory asset behavior to echo x-request-id" >&2
    echo "expected: $request_id" >&2
    echo "observed: ${echoed_request_id:-missing}" >&2
    exit 1
  fi

  if head -c 40 "$body" | grep -q '<?xml'; then
    echo "${asset_path}: asset response is an XML error document" >&2
    exit 1
  fi

  echo "${asset_path}: ok (${status}, ${content_type})"
done
