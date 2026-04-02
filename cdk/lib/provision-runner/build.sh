#!/usr/bin/env bash
# Build phase dispatcher for the provision runner.
# At synth time, helpers.sh is prepended automatically.
# At runtime in CodeBuild, this is a single inlined script.
set -euo pipefail

RUN_MODE="${RUN_MODE:-lesser}"
OWNER="${GITHUB_OWNER:-equaltoai}"
REPO="${GITHUB_REPO:-lesser}"
TAG="${LESSER_VERSION:-}"

if [ "$RUN_MODE" = "lesser-body" ]; then
  ### INLINE: build-lesser-body.sh ###
  exit 0
fi

TAG_NORMALIZED=$(printf "%s" "$TAG" | tr "[:upper:]" "[:lower:]")
if [ -z "$TAG" ] || [ "$TAG_NORMALIZED" = "latest" ]; then
  echo "Resolving latest Lesser release..."
  TAG=$(resolve_latest_release_tag "$OWNER" "$REPO")
fi
test -n "$TAG"
test "$TAG" != "null"
echo "Using Lesser release: $TAG"
LESSER_RELEASE_DIR="$(pwd)/lesser-release"
prepare_lesser_release_dir "$LESSER_RELEASE_DIR"
LESSER_CHECKOUT_DIR="$(pwd)/lesser-src"
prepare_lesser_checkout_dir "$LESSER_RELEASE_DIR" "$LESSER_CHECKOUT_DIR"
ensure_lesser_go_toolchain "$LESSER_RELEASE_DIR"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

# Shared setup for lesser and lesser-mcp paths.
STATE_DIR="$HOME/.lesser/$APP_SLUG/$BASE_DOMAIN"
mkdir -p "$STATE_DIR"
STAGE_DOMAIN="$BASE_DOMAIN"
if [ "$STAGE" != "live" ]; then STAGE_DOMAIN="$STAGE.$BASE_DOMAIN"; fi

if [ "$RUN_MODE" = "lesser" ]; then
  ### INLINE: build-lesser.sh ###
elif [ "$RUN_MODE" = "lesser-mcp" ]; then
  ### INLINE: build-lesser-mcp.sh ###
else
  fail "unknown RUN_MODE: $RUN_MODE"
fi
