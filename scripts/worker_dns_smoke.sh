#!/bin/sh
# Exercise the actual isolation script on a Compose-style network, then resolve as the Worker UID.
set -eu
worker_image=${1:-inferlab-dual-worker:local}
dns_network=$(docker network create "inferlab-worker-dns-$(date +%s)-$$")
trap 'docker network rm "$dns_network" >/dev/null' EXIT
docker run --rm --network "$dns_network" --read-only --tmpfs /tmp \
  --security-opt no-new-privileges --cap-drop ALL \
  --cap-add NET_ADMIN --cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
  -e CONTROL_IP=203.0.113.99 -e CONTROL_ORIGIN=https://control.example \
  --entrypoint sh "$worker_image" -ec '
    nslookup example.com >/dev/null
    /app/worker-isolation.sh --verify-only
    setpriv --bounding-set=-all --reuid=65532 --regid=65532 --clear-groups \
      timeout 12 nslookup example.com >/dev/null
    echo "Worker DNS after isolation and privilege drop: passed"
  '
