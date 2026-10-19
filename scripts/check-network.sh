#!/usr/bin/env bash
# Checks the network check in internal/image/system.sh without Incus: it runs
# the script's check_network function on this machine, against a name that
# doesn't resolve and a port nobody listens on, and expects the message that
# names the failing hop.
#
#   scripts/check-network.sh
set -uo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
fn=$(sed -n '/^check_network() {/,/^}/p' "$root/internal/image/system.sh")
[ -n "$fn" ] || { echo "check_network not found in system.sh" >&2; exit 1; }
fail=0
expect() { # name, host, wanted text
  out=$(AGENTBOX_NETCHECK_HOST=$2 bash -c "$fn; check_network" 2>&1); rc=$?
  if [ "$rc" -ne 0 ] && [[ $out == *"$3"* ]] && [[ $out == *"Docker's firewall"* && $out == *mirrored* ]]; then echo "ok: $1"; else echo "FAIL: $1 (rc=$rc): $out"; fail=1; fi
}
if awk 'NR>1 && $2=="00000000" {f=1} END {exit !f}' /proc/net/route; then
  expect "no DNS" nonexistent.invalid "can't resolve"
  # localhost resolves, and nothing listens on port 80 here.
  if ! timeout 2 bash -c 'exec 3<>/dev/tcp/localhost/80' 2>/dev/null; then
    expect "no connection" localhost "connection to it times out"
  fi
else
  expect "no route" deb.debian.org "no network address or route"
fi
exit $fail
