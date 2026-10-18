#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

IMAGE="${SUB2API_IMAGE:-ghcr.io/apple050620312/sub2api:latest}"
IMAGE_DIGEST="${SUB2API_IMAGE_DIGEST:-}"
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-180}"
PRUNE_DANGLING_IMAGES="${PRUNE_DANGLING_IMAGES:-false}"
GHCR_USERNAME="${GHCR_USERNAME:-}"
GHCR_TOKEN="${GHCR_TOKEN:-}"

log() {
  printf '[sub2api-ghcr-update] %s\n' "$*"
}

die() {
  printf '[sub2api-ghcr-update] ERROR: %s\n' "$*" >&2
  exit 1
}

command -v docker >/dev/null 2>&1 || die "docker is not installed"
[[ "$HEALTH_TIMEOUT" =~ ^[1-9][0-9]*$ ]] || die "HEALTH_TIMEOUT must be a positive number of seconds"
[[ "$PRUNE_DANGLING_IMAGES" == true || "$PRUNE_DANGLING_IMAGES" == false ]] || die "PRUNE_DANGLING_IMAGES must be true or false"
docker compose version >/dev/null 2>&1 || die "docker compose is not available"
[[ -f "$COMPOSE_FILE" ]] || die "$COMPOSE_FILE was not found; run this script from the deployment directory"

if [[ -n "$IMAGE_DIGEST" ]]; then
  [[ "$IMAGE_DIGEST" =~ ^sha256:[[:xdigit:]]{64}$ ]] || die "SUB2API_IMAGE_DIGEST must be a sha256 digest"
  IMAGE_REF="${IMAGE%@*}@${IMAGE_DIGEST}"
else
  IMAGE_REF="$IMAGE"
fi
[[ "$IMAGE_REF" != *[[:space:]]* && -n "$IMAGE_REF" ]] || die "invalid image reference"

if [[ -n "$GHCR_TOKEN" ]]; then
  [[ -n "$GHCR_USERNAME" ]] || die "GHCR_USERNAME is required when GHCR_TOKEN is set"
  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io --username "$GHCR_USERNAME" --password-stdin >/dev/null
fi

compose=(docker compose -f "$COMPOSE_FILE")
services=$("${compose[@]}" config --services) || die "the Compose configuration could not be read"
grep -qx sub2api <<< "$services" || die "the Compose project has no sub2api service"
grep -qx postgres <<< "$services" || die "the Compose project has no postgres service"

old_container=$("${compose[@]}" ps -q sub2api)
[[ -n "$old_container" ]] || die "the sub2api container was not found"
old_image_id=$(docker inspect "$old_container" --format '{{.Image}}') || die "the running image could not be inspected"
timestamp=$(date '+%Y%m%d-%H%M%S')
rollback_image="sub2api-rollback:$timestamp"
docker image tag "$old_image_id" "$rollback_image" || die "the running image could not be retained"

backup_dir="backups/pre-update-${timestamp}"
mkdir -p "$backup_dir"
printf '%s\n' "$old_image_id" > "$backup_dir/image-id"
printf '%s\n' "$rollback_image" > "$backup_dir/image-reference"
printf '%s\n' "$IMAGE_REF" > "$backup_dir/new-image-reference"

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

compose_tmp=''
mutated=false
rolling_back=false
cleanup() {
  if [[ -n "$compose_tmp" ]]; then rm -f "$compose_tmp"; fi
}
trap cleanup EXIT

write_image() {
  local image="$1"
  compose_tmp=$(mktemp "${COMPOSE_FILE}.XXXXXX")
  awk -v image="$image" '
  /^  sub2api:[[:space:]]*$/ { in_service = 1 }
  in_service && /^  [[:alnum:]_-]+:[[:space:]]*$/ && $0 !~ /^  sub2api:/ { in_service = 0 }
  in_service && /^    image:[[:space:]]*/ && !replaced {
    print "    image: " image
    replaced = 1
    next
  }
  { print }
  END { if (!replaced) exit 42 }
' "$COMPOSE_FILE" > "$compose_tmp" || return 1
  mv "$compose_tmp" "$COMPOSE_FILE"
  compose_tmp=''
}

rollback() {
  trap - ERR
  if [[ "$rolling_back" == true ]]; then exit 1; fi
  rolling_back=true
  log "update failed; restoring image $rollback_image ($old_image_id)"
  cp "$backup_dir/$(basename "$COMPOSE_FILE")" "$COMPOSE_FILE" || die "could not restore Compose configuration"
  write_image "$rollback_image" || die "could not pin the previous image"
  if ! "${compose[@]}" up -d --no-deps --pull never sub2api; then
    die "rollback failed; retained image reference is in $backup_dir/image-reference"
  fi
  "${compose[@]}" logs --tail=100 sub2api || true
  die "update failed; previous image restored; database backup retained at $backup_dir"
}

on_error() {
  local result=$?
  if [[ "$mutated" == true ]]; then rollback; fi
  exit "$result"
}
trap on_error ERR

log "pulling $IMAGE_REF"
docker pull "$IMAGE_REF"
resolved_image=$(docker image inspect "$IMAGE_REF" --format '{{index .RepoDigests 0}}')
[[ "$resolved_image" =~ @sha256:[[:xdigit:]]{64}$ ]] || die "the pulled image has no immutable registry digest"
IMAGE_REF="$resolved_image"
printf '%s\n' "$IMAGE_REF" > "$backup_dir/new-image-reference"

mutated=true
write_image "$IMAGE_REF" || rollback
"${compose[@]}" config --quiet || rollback
log "recreating only the sub2api service"
"${compose[@]}" up -d --no-deps --pull never sub2api

deadline=$((SECONDS + HEALTH_TIMEOUT))
while (( SECONDS < deadline )); do
  container_id=$("${compose[@]}" ps -q sub2api)
  [[ -n "$container_id" ]] || rollback
  state=$(docker inspect "$container_id" --format '{{.State.Status}}' 2>/dev/null || true)
  health=$(docker inspect "$container_id" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' 2>/dev/null || true)
  if [[ "$state" == running && "$health" == healthy ]]; then
    log "update complete: $IMAGE_REF"
    log "backup retained at $backup_dir"
    if [[ "$PRUNE_DANGLING_IMAGES" == true ]]; then
      if docker image prune --force >/dev/null; then
        log "removed dangling Docker images"
      else
        log "image cleanup failed; the healthy deployment is retained"
      fi
    fi
    "${compose[@]}" ps || true
    exit 0
  fi
  if [[ "$state" == exited || "$state" == dead || "$health" == unhealthy ]]; then
    rollback
  fi
  sleep 2
done

rollback
