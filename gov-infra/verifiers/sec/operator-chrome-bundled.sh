#!/usr/bin/env bash
# Operator Console chrome bundling guard.
#
# Project 39 M2.1 (issue #427) introduced the dark warm-charcoal Operator
# chrome at `web/src/lib/tokens/operator-chrome.css`. The original wiring
# routed the import through `src/lib/tokens/index.ts`, which is M0.2
# scaffold that no runtime entrypoint actually loads — so the chrome was
# silently absent from the built bundle. Arch caught this in review
# 4363557132 on PR #512.
#
# This verifier locks in the fix: the operator-chrome stylesheet must
# always reach the built CSS bundle under web/dist/assets/. We assert
# both `.shell--operator` (the canonical scoping selector) and the
# warm-charcoal page-gradient anchor literal `#1c1410` appear in at
# least one built CSS file. Either absence is treated as a regression
# of the same shape arch caught.
#
# The verifier deliberately depends on `npm run build` having already
# produced `web/dist/`. Run it after the SEC-6 verifier (or any other
# verifier that triggers the build) so the artifact is fresh; running
# in isolation will fail with a clear "no dist build available" message.
#
# Pass: both literals present in at least one built CSS file. Fail:
# either literal missing. No partial credit.
#
# Source: PR #512 arch review 4363557132 (Blocker 1)
# Project 39: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.1

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WEB_DIR="${REPO_ROOT}/web"
DIST_CSS_DIR="${WEB_DIR}/dist/assets"

echo "Operator chrome bundling: verifying operator-chrome.css reaches the built bundle"

if [[ ! -d "${DIST_CSS_DIR}" ]]; then
  echo "FAIL: ${DIST_CSS_DIR} does not exist; run \`cd web && npm run build\` first" >&2
  exit 1
fi

CSS_FILES=()
while IFS= read -r -d '' f; do
  CSS_FILES+=("$f")
done < <(find "${DIST_CSS_DIR}" -maxdepth 1 -type f -name '*.css' -print0)

if [[ ${#CSS_FILES[@]} -eq 0 ]]; then
  echo "FAIL: no .css files found under ${DIST_CSS_DIR}" >&2
  exit 1
fi

# Both signals must be present together: the scoping selector AND the
# warm-charcoal anchor colour. Either alone would be ambiguous.
SCOPE_FOUND=0
COLOR_FOUND=0
for f in "${CSS_FILES[@]}"; do
  if grep -q '\.shell--operator' "$f"; then
    SCOPE_FOUND=1
  fi
  if grep -q '#1c1410' "$f"; then
    COLOR_FOUND=1
  fi
done

if [[ "${SCOPE_FOUND}" -ne 1 ]]; then
  echo "FAIL: '.shell--operator' selector missing from ${DIST_CSS_DIR}/*.css — operator-chrome.css is not in the import graph" >&2
  exit 1
fi
if [[ "${COLOR_FOUND}" -ne 1 ]]; then
  echo "FAIL: warm-charcoal anchor '#1c1410' missing from ${DIST_CSS_DIR}/*.css — operator-chrome.css is not in the import graph" >&2
  exit 1
fi

echo "PASS: operator-chrome.css is bundled (.shell--operator selector + #1c1410 anchor present)"
exit 0
