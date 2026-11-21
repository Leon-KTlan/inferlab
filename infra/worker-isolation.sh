#!/bin/sh
# Container network namespace only. Never run with host networking or privileged.
set -eu
: "${CONTROL_IP:?Set the dedicated TLS API control IPv4 address}"
: "${CONTROL_ORIGIN:?Set https://control-host origin, certificate must verify}"
case "$CONTROL_IP" in *[!0-9.]*|'') echo 'Invalid CONTROL_IP' >&2; exit 2;; esac
case "$CONTROL_ORIGIN" in https://*) ;; *) echo 'TLS control origin required' >&2; exit 2;; esac
# Default deny including IPv6; only the TLS control service may be private.
ip6tables -P OUTPUT DROP
iptables -P OUTPUT DROP
iptables -F OUTPUT
iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -d "$CONTROL_IP/32" -p tcp --dport 443 -j ACCEPT
# Docker embedded DNS is the sole loopback exception, for name resolution only.
ip r show default | awk '{print $3}' > /tmp/worker-gateway
iptables -A OUTPUT -d 127.0.0.11/32 -p udp --dport 53 -j ACCEPT
iptables -A OUTPUT -d 127.0.0.11/32 -p tcp --dport 53 -j ACCEPT
for cidr in 0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.168.0.0/16 198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4; do
  iptables -A OUTPUT -d "$cidr" -j DROP
done
iptables -A OUTPUT -p tcp --dport 443 -j ACCEPT
iptables -A OUTPUT -j DROP
# Verify real packets hit DROP rules, not merely closed TCP ports.
for address in 127.0.0.1 169.254.169.254 100.100.100.200 "$(cat /tmp/worker-gateway)"; do
  if [ "$address" = "$CONTROL_IP" ]; then echo 'Control IP overlaps an isolation probe' >&2; exit 2; fi
  nc -z -w 1 "$address" 80 >/dev/null 2>&1 && { echo 'Isolation probe unexpectedly connected' >&2; exit 1; }
done
iptables -nvL OUTPUT | awk '$3=="DROP" && $1>0 {hits++} END {if(hits<3) exit 1}'
echo 'Worker network probes passed: loopback, host bridge and metadata blocked.'
if [ "${1:-}" = '--verify-only' ]; then
  exec setpriv --bounding-set=-all --reuid=65532 --regid=65532 --clear-groups awk '/^(Uid|Gid|CapEff|CapBnd):/ {print} /^CapEff:/ && $2!="0000000000000000" {bad=1} /^CapBnd:/ && $2!="0000000000000000" {bad=1} END {exit bad}' /proc/self/status
fi
# Drop all capabilities before parsing target tasks; no host mounts, no Docker socket.
exec setpriv --bounding-set=-all --reuid=65532 --regid=65532 --clear-groups /app/inferlab worker --api-url "$CONTROL_ORIGIN"
