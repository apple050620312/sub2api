#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin"
export PATH="$test_dir/bin:$PATH" TEST_STATE="$test_dir"
export HEALTH_TIMEOUT=1 PRUNE_DANGLING_IMAGES=true
export TEST_DIGEST="sha256:$(printf '%064d' 1)"

cat > "$test_dir/bin/docker" <<'SH'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$TEST_STATE/docker-calls"
case "$1" in
  pull) [[ "$TEST_MODE" != pull-failure ]] ;;
  image)
    case "$2" in
      inspect) printf 'ghcr.io/example/sub2api@%s\n' "$TEST_DIGEST" ;;
      prune) [[ "$TEST_MODE" != cleanup-failure ]] ;;
      tag) : ;;
    esac
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
      config)
        if [[ "${2:-}" == --services ]]; then
          printf 'sub2api\n'
          if [[ "$TEST_MODE" == streamed-services ]]; then
            for service in {1..4096}; do printf 'sidecar-%s\n' "$service"; done
          fi
          printf 'postgres\n'
        fi
        ;;
      ps) printf 'app-container\n' ;;
      exec) [[ "$TEST_MODE" != backup-failure ]] && printf 'backup-content\n' ;;
      up)
        count=$(cat "$TEST_STATE/up-count" 2>/dev/null || printf 0)
        printf '%s' "$((count+1))" > "$TEST_STATE/up-count"
        if [[ "$count" == 0 && "$TEST_MODE" == up-failure ]]; then exit 1; fi
        ;;
      logs) : ;;
    esac
    ;;
esac
SH
chmod +x "$test_dir/bin/docker"

for mode in success streamed-services pull-failure backup-failure up-failure unhealthy no-health cleanup-failure; do
  export TEST_MODE="$mode"
  deployment="$test_dir/$mode"
  mkdir -p "$deployment"
  cat > "$deployment/docker-compose.yml" <<'YAML'
services:
  sub2api:
    image: ghcr.io/example/sub2api:old
  postgres:
    image: postgres:18
YAML
  rm -f "$test_dir/up-count" "$test_dir/docker-calls"
  result=0
  (cd "$deployment" && bash "$root/deploy/update-ghcr.sh") > "$test_dir/output" 2>&1 || result=$?
  case "$mode" in
    success|streamed-services|cleanup-failure)
      [[ "$result" == 0 && "$(cat "$test_dir/up-count")" == 1 ]] || { cat "$test_dir/output"; exit 1; }
      grep -q "image: ghcr.io/example/sub2api@$TEST_DIGEST" "$deployment/docker-compose.yml"
      ;;
    pull-failure|backup-failure)
      [[ "$result" != 0 && ! -f "$test_dir/up-count" ]] || { cat "$test_dir/output"; exit 1; }
      grep -q 'image: ghcr.io/example/sub2api:old' "$deployment/docker-compose.yml"
      ;;
    *)
      [[ "$result" != 0 && "$(cat "$test_dir/up-count")" == 2 ]] || { cat "$test_dir/output"; exit 1; }
      grep -q 'image: sub2api-rollback:' "$deployment/docker-compose.yml"
      ;;
  esac
  grep -q 'image tag sha256:old-id sub2api-rollback:' "$test_dir/docker-calls"
  if [[ "$mode" != backup-failure ]]; then
    for backup in "$deployment"/backups/*/postgres.dump; do
      [[ -s "$backup" ]] || exit 1
    done
  fi
done
printf 'GHCR update, immutable digest, backups and rollback tests passed\n'
