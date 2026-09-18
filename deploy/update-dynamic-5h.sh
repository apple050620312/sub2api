#!/usr/bin/env bash
set -Eeuo pipefail

IMAGE="${SUB2API_IMAGE:-ghcr.io/apple050620312/sub2api:latest}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-180}"

log() {
  printf '[sub2api-update] %s\n' "$*"
}

die() {
  printf '[sub2api-update] ERROR: %s\n' "$*" >&2
  exit 1
}

command -v docker >/dev/null 2>&1 || die "docker is not installed"
docker compose version >/dev/null 2>&1 || die "docker compose is not available"
[[ -f "$COMPOSE_FILE" ]] || die "$COMPOSE_FILE was not found; run this script from the deployment directory"

compose=(docker compose -f "$COMPOSE_FILE")
"${compose[@]}" config --services | grep -qx sub2api || die "the Compose project has no sub2api service"
"${compose[@]}" config --services | grep -qx postgres || die "the Compose project has no postgres service"

old_image=$(docker inspect sub2api --format '{{.Config.Image}}' 2>/dev/null) || die "the sub2api container was not found"
timestamp=$(date '+%Y%m%d-%H%M%S')
backup_dir="backups/pre-update-${timestamp}"
mkdir -p "$backup_dir"

log "backing up the deployment to $backup_dir"
cp "$COMPOSE_FILE" "$backup_dir/$(basename "$COMPOSE_FILE")"
if [[ -f .env ]]; then
  cp .env "$backup_dir/env.backup"
  chmod 600 "$backup_dir/env.backup"
fi

"${compose[@]}" exec -T postgres sh -c \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "$backup_dir/postgres.dump"
[[ -s "$backup_dir/postgres.dump" ]] || die "PostgreSQL backup is empty"

"${compose[@]}" exec -T sub2api tar -C /app/data -czf - . \
  > "$backup_dir/app-data.tar.gz"
[[ -s "$backup_dir/app-data.tar.gz" ]] || die "application data backup is empty"

compose_tmp=$(mktemp "${COMPOSE_FILE}.XXXXXX")
cleanup() {
  rm -f "$compose_tmp"
}
trap cleanup EXIT

awk -v image="$IMAGE" '
  /^  sub2api:[[:space:]]*$/ { in_service = 1 }
  in_service && /^  [[:alnum:]_-]+:[[:space:]]*$/ && $0 !~ /^  sub2api:/ { in_service = 0 }
  in_service && /^    image:[[:space:]]*/ && !replaced {
    print "    image: " image
    replaced = 1
    next
  }
  { print }
  END { if (!replaced) exit 42 }
' "$COMPOSE_FILE" > "$compose_tmp" || die "could not replace the sub2api image in $COMPOSE_FILE"
mv "$compose_tmp" "$COMPOSE_FILE"
compose_tmp=''

if ! "${compose[@]}" config --quiet; then
  cp "$backup_dir/$(basename "$COMPOSE_FILE")" "$COMPOSE_FILE"
  die "updated Compose configuration is invalid; the original file was restored"
fi

rollback() {
  log "new container did not become healthy; restoring image $old_image"
  cp "$backup_dir/$(basename "$COMPOSE_FILE")" "$COMPOSE_FILE"
  "${compose[@]}" up -d --no-deps sub2api || true
  "${compose[@]}" logs --tail=100 sub2api || true
  die "update failed and the previous Compose configuration was restored"
}

log "pulling $IMAGE"
"${compose[@]}" pull sub2api
log "recreating only the sub2api service"
"${compose[@]}" up -d --no-deps sub2api

deadline=$((SECONDS + HEALTH_TIMEOUT))
while (( SECONDS < deadline )); do
  container_id=$("${compose[@]}" ps -q sub2api)
  [[ -n "$container_id" ]] || rollback
  state=$(docker inspect "$container_id" --format '{{.State.Status}}' 2>/dev/null || true)
  health=$(docker inspect "$container_id" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' 2>/dev/null || true)
  if [[ "$state" == running && ( "$health" == healthy || "$health" == none ) ]]; then
    log "update complete: $IMAGE"
    log "backup retained at $backup_dir"
    "${compose[@]}" ps
    exit 0
  fi
  if [[ "$state" == exited || "$state" == dead || "$health" == unhealthy ]]; then
    rollback
  fi
  sleep 2
done

rollback
