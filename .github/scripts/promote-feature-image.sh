#!/usr/bin/env bash
set -Eeuo pipefail

: "${IMAGE_DIGEST:?IMAGE_DIGEST is required}"
: "${REGISTRY:?REGISTRY is required}"
: "${IMAGE_NAME:?IMAGE_NAME is required}"
bash "$(dirname "$0")/await-ci.sh"
current_head=$(gh api "repos/$GH_REPO/git/ref/heads/$BRANCH" --jq '.object.sha')
if [[ "$current_head" != "$COMMIT_SHA" ]]; then
  printf 'Branch advanced; immutable image retained without moving tags.\n'
  exit 0
fi
image=$(printf '%s/%s' "$REGISTRY" "$IMAGE_NAME" | tr '[:upper:]' '[:lower:]')
docker buildx imagetools create \
  --tag "$image:latest" \
  --tag "$image:dynamic-5h-pressure" \
  "$image@$IMAGE_DIGEST"
