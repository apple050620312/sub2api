#!/usr/bin/env bash
set -Eeuo pipefail

: "${IMAGE_DIGEST:?IMAGE_DIGEST is required}"
: "${REGISTRY:?REGISTRY is required}"
: "${IMAGE_NAME:?IMAGE_NAME is required}"
if ! result=$(gh api "repos/$GH_REPO/actions/workflows/backend-ci.yml/runs?head_sha=$COMMIT_SHA&branch=$BRANCH&event=push&per_page=100" \
  --jq '.workflow_runs | sort_by(.run_number) | last | [.head_sha // "missing", .status // "missing", .conclusion // "pending"] | @tsv'); then
  printf 'CI status unavailable; retaining immutable image only.\n'
  exit 0
fi
IFS=$'\t' read -r observed_sha status conclusion <<< "$result"
if [[ "$observed_sha" != "$COMMIT_SHA" || "$status" != completed || "$conclusion" != success ]]; then
  printf 'CI is not successful yet; retaining immutable image only.\n'
  exit 0
fi
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
