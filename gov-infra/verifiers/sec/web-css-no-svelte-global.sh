#!/usr/bin/env bash
# Plain-CSS no-:global() guard.
#
# Project 39: arch follow-up after the M2.1 chrome bundle fix on PR #512.
#
# `:global(...)` is a Svelte-scoped-styles construct: it tells the Svelte
# compiler to bypass the per-component class-scoping rewrite for the
# wrapped selector. It is meaningful only inside a Svelte component's
# `<style>` block. In a standalone `.css` file (loaded directly through
# Vite / main.ts), `:global()` is an unrecognised pseudo-class — Lightning
# CSS warns with "'global' is not recognized as a valid pseudo-class".
#
# This verifier walks every plain `.css` file under `web/src/` and fails
# if any contains a `:global(` CSS selector. `.svelte` files are NOT
# scanned because `:global()` inside `<style>` blocks there is legal.
# Hits inside `/* ... */` comments are tolerated so the file can
# document the rule without tripping it.
#
# Same precedent as `web/src/lib/tokens/ds.css`, which documents:
#   "no `:global()` to keep these usable as plain CSS (closes the
#   2026-05-16 LightningCSS feedback locally)."
#
# Pass: zero plain-CSS files contain a non-comment `:global(` selector.
# Fail: any plain-CSS file contains a non-comment `:global(` selector.
# No partial credit.
#
# Source: PR #512 arch follow-up on operator-chrome.css :global() leak
# Project 39: docs/enumerated-changes-web-ui-rework-2026-05-24.md M2.1

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
SRC_DIR="${REPO_ROOT}/web/src"

echo "Plain-CSS :global() guard: scanning ${SRC_DIR}/**/*.css"

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "FAIL: ${SRC_DIR} does not exist" >&2
  exit 1
fi

HITS=()
while IFS= read -r -d '' f; do
  # Strip /* ... */ block comments before matching so commentary about
  # the rule (e.g. the documentation header in operator-chrome.css) does
  # not trip the verifier. The sed pipeline removes both single-line
  # `/* ... */` and multi-line block comments in a streaming fashion.
  STRIPPED=$(awk 'BEGIN{c=0} {
    line=$0
    while (1) {
      if (c==0) {
        i=index(line, "/*")
        if (i==0) { print line; break }
        j=index(substr(line, i+2), "*/")
        if (j>0) {
          line=substr(line,1,i-1) substr(line, i+j+3)
          continue
        } else {
          print substr(line,1,i-1)
          c=1
          break
        }
      } else {
        j=index(line, "*/")
        if (j>0) { line=substr(line, j+2); c=0; continue } else { break }
      }
    }
  }' "$f")
  if printf '%s' "$STRIPPED" | grep -qE ':global\('; then
    HITS+=("$f")
  fi
done < <(find "${SRC_DIR}" -type f -name '*.css' -print0)

if [[ ${#HITS[@]} -gt 0 ]]; then
  echo "FAIL: plain-CSS file(s) contain a non-comment ':global(' selector:" >&2
  for h in "${HITS[@]}"; do
    rel="${h#${REPO_ROOT}/}"
    echo "  $rel" >&2
    grep -nE ':global\(' "$h" | head -5 >&2 || true
  done
  echo "" >&2
  echo "':global()' is a Svelte-scoped-styles construct, valid only inside" >&2
  echo "<style> blocks in .svelte files. In a plain .css file it is an" >&2
  echo "unrecognised pseudo-class and Lightning CSS will warn on every" >&2
  echo "build. Use bare selectors instead (the file is already global)." >&2
  exit 1
fi

echo "PASS: no plain-CSS files contain non-comment ':global(' selectors"
exit 0
