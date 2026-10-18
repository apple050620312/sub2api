#!/usr/bin/env bash
set -Eeuo pipefail

: "${GH_REPO:?GH_REPO is required}"
: "${COMMIT_SHA:?COMMIT_SHA is required}"
: "${BRANCH:?BRANCH is required}"
deadline=$((SECONDS + ${CI_WAIT_TIMEOUT:-3600}))

while (( SECONDS < deadline )); do
  if result=$(gh api "repos/$GH_REPO/actions/workflows/backend-ci.yml/runs?head_sha=$COMMIT_SHA&branch=$BRANCH&event=push&per_page=100" \
    --jq '.workflow_runs | sort_by(.run_number) | last | [.head_sha // "missing", .status // "missing", .conclusion // "pending"] | @tsv'); then
    IFS=$'\t' read -r observed_sha status conclusion <<< "$result"
    if [[ "$observed_sha" == "$COMMIT_SHA" && "$status" == completed ]]; then
      if [[ "$conclusion" == success ]]; then
        printf 'CI succeeded for %s\n' "$COMMIT_SHA"
        exit 0
      fi
      printf 'CI rejected publication for %s: %s\n' "$COMMIT_SHA" "$conclusion" >&2
      exit 1
    fi
  fi
  sleep "${CI_POLL_INTERVAL:-15}"
done

printf 'CI success for %s was not verified before timeout\n' "$COMMIT_SHA" >&2
exit 1
