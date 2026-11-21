#!/bin/sh
# Full isolated acceptance; never reads a supplier key or an existing database.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
evidence="$root/docs/tasks/dual-execution/evidence"
acceptance_tmp=$(mktemp -d "${TMPDIR:-/tmp}/inferlab-dual-acceptance.XXXXXX")
trap 'rm -rf "$acceptance_tmp"' EXIT INT TERM
mkdir -p "$evidence"
run_check() { label=$1; shift; echo "Checking $label"; if "$@" > "$acceptance_tmp/check.log" 2>&1; then echo "PASS $label"; else cat "$acceptance_tmp/check.log"; exit 1; fi; }
cd "$root/services/api"
run_check 'Go vet' go vet ./...
if ! go test -json ./... > "$acceptance_tmp/go-test.jsonl"; then cat "$acceptance_tmp/go-test.jsonl"; exit 1; fi
echo 'PASS Go tests'
run_check 'Go race' go test -race ./...
cd "$root/apps/web"
run_check 'Frontend lint (tsc)' npm run lint
run_check 'Frontend typecheck' npm run typecheck
run_check 'Frontend build' npm run build
run_check 'Frontend tests' npm run test
cd "$root"
run_check 'Pinned source hash and build' "${BROWSER_PYTHON:-python3}" scripts/verify_runner_release.py
cd "$root/services/api"
run_check 'TLS browser two-mode round trip' env BROWSER_PYTHON="${BROWSER_PYTHON:-python3}" go test -tags browser -run TestDualExecutionBrowser -count=1 -timeout=90s -v
cd "$root"
run_check 'Worker image build' docker build -f infra/worker.Dockerfile -t inferlab-dual-worker:local .
if ! docker run --rm --read-only --tmpfs /tmp --security-opt no-new-privileges \
  --cap-drop ALL --cap-add NET_ADMIN --cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
  -e CONTROL_IP=203.0.113.99 -e CONTROL_ORIGIN=https://control.example \
  inferlab-dual-worker:local --verify-only > "$evidence/worker-isolation.txt" 2>&1; then cat "$evidence/worker-isolation.txt"; exit 1; fi
echo 'PASS Worker egress and privilege probes'
run_check 'Worker DNS on Compose-style network' sh scripts/worker_dns_smoke.sh
"${BROWSER_PYTHON:-python3}" - "$acceptance_tmp/go-test.jsonl" "$evidence/acceptance.json" <<'PY'
import datetime, json, sys
from pathlib import Path
records = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
passed = [r['Test'] for r in records if r.get('Action') == 'pass' and 'Test' in r]
Path(sys.argv[2]).write_text(json.dumps({'result': 'passed', 'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'go_tests_including_subtests': len(passed), 'dual_execution_tests': [t for t in passed if t.startswith('TestDualExecution')], 'checks': ['go vet', 'go test', 'go test -race', 'npm lint (tsc)', 'npm typecheck', 'npm build', 'npm test', 'real TLS two-mode browser round trip', 'Docker network and zero-capability probes', 'Worker DNS after isolation on Compose-style network'], 'provider_calls': 0, 'production_changes': False}, indent=2) + '\n')
PY
git diff --check
echo "Dual execution acceptance passed. Evidence: $evidence"
