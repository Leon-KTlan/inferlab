#!/usr/bin/env bash
set -Eeuo pipefail

: "${DEPLOY_DIR:?DEPLOY_DIR must point to the staging checkout}"

cd "$DEPLOY_DIR"
if [[ ! -f .env ]]; then
  echo "DEPLOY_DIR/.env is required" >&2
  exit 1
fi

INFERLAB_IMAGE="$(awk -F= '$1 == "INFERLAB_IMAGE" { sub(/^[^=]*=/, ""); print; exit }' .env)"
: "${INFERLAB_IMAGE:?INFERLAB_IMAGE must be set in .env}"

compose=(docker compose --project-directory "$DEPLOY_DIR" --env-file "$DEPLOY_DIR/.env" -f "$DEPLOY_DIR/infra/docker-compose.yml")
previous_image=""
previous_rollback_image=""
current_container="$("${compose[@]}" ps -q inferlab 2>/dev/null || true)"
if [[ -n "$current_container" ]]; then
  previous_image="$(docker inspect --format '{{.Config.Image}}' "$current_container" 2>/dev/null || true)"
  previous_image_id="$(docker inspect --format '{{.Image}}' "$current_container" 2>/dev/null || true)"
  if [[ -n "$previous_image_id" ]]; then
    previous_rollback_image="inferlab:rollback-$(date -u +%Y%m%dT%H%M%SZ)"
    docker tag "$previous_image_id" "$previous_rollback_image"
  fi
fi

backup_dir="$DEPLOY_DIR/backups"
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"

snapshot_database() {
  local label="$1"
  local container="$2"
  local timestamp="$3"
  local backup_base="$backup_dir/${label}-${timestamp}.db"
  local image_id
  image_id="$(docker inspect --format '{{.Image}}' "$container" 2>/dev/null || true)"
  if [[ -z "$image_id" ]]; then
    echo "Cannot snapshot database: container image is unavailable" >&2
    return 1
  fi
  docker run --rm --user 0:0 --volumes-from "$container" -v "$backup_dir:/backup" --entrypoint /bin/sh "$image_id" -c '
    if [ ! -f /app/data/inferlab.db ]; then exit 2; fi
    cp -p /app/data/inferlab.db "/backup/$(basename "$1")"
    for suffix in -wal -shm; do
      if [ -f "/app/data/inferlab.db${suffix}" ]; then
        cp -p "/app/data/inferlab.db${suffix}" "/backup/$(basename "$1")${suffix}"
      fi
    done
  ' sh "$backup_base"
  echo "SQLite snapshot saved to $backup_base"
}

wait_for_health() {
  for attempt in $(seq 1 30); do
    if curl --fail --silent --show-error --max-time 3 http://127.0.0.1:8080/healthz >/dev/null; then
      return 0
    fi
    sleep 2
  done
  return 1
}

run_smoke() {
  local smoke_dir cookie_jar response experiment_id run_id status
  smoke_dir="$(mktemp -d)"
  cookie_jar="$smoke_dir/cookies"
  trap 'rm -rf "$smoke_dir"' RETURN

  response="$(curl --fail --silent --show-error --max-time 10 -c "$cookie_jar" -b "$cookie_jar" \
    -H 'Content-Type: application/json' \
    -d '{"name":"staging smoke","target":{"type":"mock","model":"mock-llm"},"workload":{"concurrency_steps":[1],"stage_seconds":1,"max_requests":5,"max_rps":5,"timeout_ms":1000},"slo":{"p95_e2e_ms":2000,"error_rate_max":1}}' \
    http://127.0.0.1:8080/api/v1/experiments)" || return 1
  experiment_id="$(printf '%s' "$response" | tr -d '\n' | sed -n 's/.*"experiment_id":"\([^"]*\)".*/\1/p')"
  [[ -n "$experiment_id" ]] || { echo "Smoke failed: experiment ID missing" >&2; return 1; }

  response="$(curl --fail --silent --show-error --max-time 10 -c "$cookie_jar" -b "$cookie_jar" \
    -X POST "http://127.0.0.1:8080/api/v1/experiments/$experiment_id/runs")" || return 1
  run_id="$(printf '%s' "$response" | tr -d '\n' | sed -n 's/.*"run_id":"\([^"]*\)".*/\1/p')"
  [[ -n "$run_id" ]] || { echo "Smoke failed: run ID missing" >&2; return 1; }

  for attempt in $(seq 1 45); do
    response="$(curl --fail --silent --show-error --max-time 10 -b "$cookie_jar" "http://127.0.0.1:8080/api/v1/runs/$run_id")" || return 1
    status="$(printf '%s' "$response" | tr -d '\n' | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')"
    case "$status" in
      succeeded) break ;;
      failed|canceled) echo "Smoke failed: run ended with status $status" >&2; return 1 ;;
      *) sleep 2 ;;
    esac
    if (( attempt == 45 )); then
      echo "Smoke failed: run did not finish" >&2
      return 1
    fi
  done

  response="$(curl --fail --silent --show-error --max-time 10 -b "$cookie_jar" "http://127.0.0.1:8080/api/v1/runs/$run_id/report")" || return 1
  printf '%s' "$response" | grep -q '"methodology"' || { echo "Smoke failed: report methodology missing" >&2; return 1; }
  echo "InferLab Mock smoke passed for run $run_id"
}

rollback() {
  local failed_container
  failed_container="$("${compose[@]}" ps -q inferlab 2>/dev/null || true)"
  if [[ -n "$failed_container" ]]; then
    "${compose[@]}" stop inferlab >/dev/null 2>&1 || true
    snapshot_database "failed" "$failed_container" "$(date -u +%Y%m%dT%H%M%SZ)" || true
  fi
  if [[ -z "$previous_rollback_image" ]]; then
    echo "Deployment failed and no previous image was found; inspect logs before retrying." >&2
    return 0
  fi
  echo "Deployment failed; preserving failed state and rolling back from $previous_image" >&2
  export INFERLAB_IMAGE="$previous_rollback_image"
  "${compose[@]}" up -d --no-build --remove-orphans inferlab
  if ! wait_for_health; then
    echo "Rollback health check failed; manual intervention is required" >&2
    return 1
  fi
}

"${compose[@]}" config >/dev/null
if [[ -n "$current_container" ]]; then
  "${compose[@]}" stop inferlab >/dev/null
  if ! snapshot_database "predeploy" "$current_container" "$(date -u +%Y%m%dT%H%M%SZ)"; then
    echo "Pre-deploy database backup failed; restarting the previous container" >&2
    "${compose[@]}" start inferlab >/dev/null || true
    exit 1
  fi
fi

if ! docker pull "$INFERLAB_IMAGE"; then
  rollback
  exit 1
fi
if ! "${compose[@]}" up -d --no-build --remove-orphans inferlab; then
  rollback
  exit 1
fi

if wait_for_health && run_smoke; then
  echo "InferLab health and Mock smoke checks passed with $INFERLAB_IMAGE"
  exit 0
fi

rollback
exit 1
