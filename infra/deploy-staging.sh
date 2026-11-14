#!/usr/bin/env bash
set -Eeuo pipefail

: "${DEPLOY_DIR:?DEPLOY_DIR must point to the staging checkout}"

cd "$DEPLOY_DIR"
if [[ -f .env ]]; then
  set -a
  # The file is created by the deployment workflow with mode 0600.
  # shellcheck disable=SC1091
  source ./.env
  set +a
fi

: "${INFERLAB_IMAGE:?INFERLAB_IMAGE must be set in .env}"
: "${INFERLAB_SESSION_SECRET:?INFERLAB_SESSION_SECRET must be set in .env}"

if (( ${#INFERLAB_SESSION_SECRET} < 32 )); then
  echo "INFERLAB_SESSION_SECRET must contain at least 32 bytes" >&2
  exit 1
fi

compose=(docker compose -f "$DEPLOY_DIR/infra/docker-compose.yml")
previous_image=""
current_container="$(${compose[@]} ps -q inferlab 2>/dev/null || true)"
if [[ -n "$current_container" ]]; then
  previous_image="$(docker inspect --format '{{.Config.Image}}' "$current_container" 2>/dev/null || true)"
fi

rollback() {
  if [[ -z "$previous_image" ]]; then
    echo "Deployment failed and no previous image was found; inspect logs before retrying." >&2
    return
  fi
  echo "Health check failed; rolling back to $previous_image" >&2
  export INFERLAB_IMAGE="$previous_image"
  "${compose[@]}" up -d --no-build --remove-orphans inferlab
}

"${compose[@]}" config >/dev/null
docker pull "$INFERLAB_IMAGE"
"${compose[@]}" up -d --no-build --remove-orphans inferlab

for attempt in $(seq 1 30); do
  if curl --fail --silent --show-error --max-time 3 http://127.0.0.1:8080/healthz >/dev/null; then
    echo "InferLab staging health check passed with $INFERLAB_IMAGE"
    exit 0
  fi
  sleep 2
done

rollback
exit 1
