#!/bin/sh
# Local preview only; fresh temporary database, platform deliberately unconfigured.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
preview_dir=$(mktemp -d "${TMPDIR:-/tmp}/inferlab-dual-preview.XXXXXX")
api_pid=''
cleanup() { if [ -n "$api_pid" ]; then kill "$api_pid" 2>/dev/null || true; wait "$api_pid" 2>/dev/null || true; api_pid=''; fi; rm -rf "$preview_dir"; }
trap cleanup EXIT INT TERM
(cd "$root/apps/web" && npm run build)
(cd "$root/services/api" && go build -trimpath -o "$preview_dir/inferlab" .)
echo 'InferLab preview: http://127.0.0.1:4186 (Ctrl+C stops and removes the temporary database).'
APP_ENV=local PLATFORM_EXECUTION_ENABLED=false DB_PATH="$preview_dir/preview.db" WEB_DIR="$root/apps/web/dist" LISTEN_ADDR=127.0.0.1:4186 "$preview_dir/inferlab" &
api_pid=$!
wait "$api_pid"
