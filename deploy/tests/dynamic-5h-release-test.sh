#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin"
export PATH="$test_dir/bin:$PATH"
export GH_REPO=example/sub2api COMMIT_SHA=abc123 BRANCH=codex/dynamic-5h-pressure
export CI_WAIT_TIMEOUT=5 CI_POLL_INTERVAL=0.05
export TEST_STATE="$test_dir"

cat > "$test_dir/bin/gh" <<'SH'
#!/usr/bin/env bash
set -eu
count=$(cat "$TEST_STATE/gh-count" 2>/dev/null || printf 0)
printf '%s' "$((count + 1))" > "$TEST_STATE/gh-count"
if [[ "$*" == *git/ref/heads/* ]]; then printf '%s\n' "${TEST_HEAD:-$COMMIT_SHA}"; exit 0; fi
[[ "$*" == *"head_sha=$COMMIT_SHA"* && "$*" == *"event=push"* ]] || exit 9
case "$TEST_MODE" in
  success) printf '%s\tcompleted\tsuccess\n' "$COMMIT_SHA" ;;
  failure|cancelled|skipped) printf '%s\tcompleted\t%s\n' "$COMMIT_SHA" "$TEST_MODE" ;;
  wrong-sha) printf 'other\tcompleted\tsuccess\n' ;;
  pending) printf '%s\tin_progress\tpending\n' "$COMMIT_SHA" ;;
  transient) if [[ "$count" == 0 ]]; then exit 1; else printf '%s\tcompleted\tsuccess\n' "$COMMIT_SHA"; fi ;;
esac
SH

cat > "$test_dir/bin/docker" <<'SH'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$TEST_STATE/docker-calls"
case "$1" in
  buildx) : ;;
  image)
    if [[ "$2" == inspect && "$TEST_MODE" != no-digest ]]; then printf 'ghcr.io/example/sub2api@sha256:old\n'; fi
    ;;
  inspect)
    if [[ "$*" == *'{{.Image}}'* ]]; then printf 'sha256:old-id\n'
    elif [[ "$*" == *State.Status* ]]; then printf 'running\n'
    elif [[ "$TEST_MODE" == unhealthy ]]; then printf 'unhealthy\n'
    elif [[ "$TEST_MODE" == no-health ]]; then printf 'none\n'
    else printf 'healthy\n'; fi
    ;;
  compose)
    shift
    if [[ "${1:-}" == version ]]; then exit 0; fi
    shift 2
    case "$1" in
      config) if [[ "${2:-}" == --services ]]; then printf 'sub2api\npostgres\n'; fi ;;
      ps) printf 'app-container\n' ;;
      exec) printf 'backup-content\n' ;;
      pull) [[ "$TEST_MODE" != pull-failure ]] ;;
      up)
        if [[ "$*" == *'--pull never'* ]]; then printf 'rollback\n' > "$TEST_STATE/rollback";
        elif [[ "$TEST_MODE" == up-failure ]]; then exit 1; fi
        ;;
      logs) : ;;
    esac
    ;;
esac
SH
chmod +x "$test_dir/bin/gh" "$test_dir/bin/docker"

for mode in success transient failure cancelled skipped wrong-sha pending; do
  export TEST_MODE="$mode"
  rm -f "$test_dir/gh-count"
  result=0
  bash "$root/.github/scripts/await-ci.sh" > "$test_dir/ci-output" 2>&1 || result=$?
  if [[ "$mode" == success || "$mode" == transient ]]; then
    [[ "$result" == 0 ]] || { cat "$test_dir/ci-output"; exit 1; }
  else
    [[ "$result" != 0 ]] || { printf 'CI gate incorrectly accepted %s\n' "$mode"; exit 1; }
  fi
done

export TEST_MODE=success REGISTRY=ghcr.io IMAGE_NAME=Example/Sub2api IMAGE_DIGEST=sha256:verified
for head in "$COMMIT_SHA" newer-commit; do
  export TEST_HEAD="$head"
  rm -f "$test_dir/docker-calls"
  bash "$root/.github/scripts/promote-feature-image.sh" > "$test_dir/promote-output" 2>&1
  if [[ "$head" == "$COMMIT_SHA" ]]; then
    grep -q 'ghcr.io/example/sub2api@sha256:verified' "$test_dir/docker-calls"
    grep -q -- '--tag ghcr.io/example/sub2api:latest' "$test_dir/docker-calls"
  else
    [[ ! -f "$test_dir/docker-calls" ]] || exit 1
  fi
done

for mode in success no-digest pull-failure up-failure unhealthy no-health; do
  export TEST_MODE="$mode" HEALTH_TIMEOUT=1
  deployment="$test_dir/$mode"
  mkdir -p "$deployment"
  cat > "$deployment/docker-compose.yml" <<'YAML'
services:
  sub2api:
    image: ghcr.io/example/sub2api:latest
  postgres:
    image: postgres:15
YAML
  rm -f "$test_dir/rollback"
  result=0
  (cd "$deployment" && bash "$root/deploy/update-dynamic-5h.sh") > "$test_dir/update-output" 2>&1 || result=$?
  if [[ "$mode" == success || "$mode" == no-digest ]]; then
    [[ "$result" == 0 && ! -f "$test_dir/rollback" ]] || { cat "$test_dir/update-output"; exit 1; }
  else
    [[ "$result" != 0 && -f "$test_dir/rollback" ]] || { cat "$test_dir/update-output"; exit 1; }
    grep -q 'image: ghcr.io/example/sub2api@sha256:old' "$deployment/docker-compose.yml"
    grep -q 'sha256:old-id' "$deployment"/backups/*/image-id
  fi
done
printf 'Dynamic 5h CI gate and immutable rollback tests passed\n'
