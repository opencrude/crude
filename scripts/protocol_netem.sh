#!/usr/bin/env bash
# Opt-in network-impaired QUIC gate: core and client run in two private network
# namespaces joined by one veth pair, netem on both ends (each profile's delay
# and loss apply per direction). Requires sudo for the namespaces only; both
# roles run as the invoking user. No host interface, route or qdisc is changed.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/libghostty_env.sh
[[ "$(uname -s)" == Linux ]] || { echo 'FAIL: Linux network namespaces required' >&2; exit 1; }
: "${CRUDE_TEMP_DIR:?Set CRUDE_TEMP_DIR to an evidence directory}"
# Profiles are one-way delay/loss per veth end. 25ms is the plan's 50 ms RTT
# gate; 80ms (160 ms RTT) is above mosh's prediction trigger.
PROFILES="${CRUDE_NETEM_PROFILES:-25ms/1% 80ms/1%}"
out="$(mktemp -d "$CRUDE_TEMP_DIR/protocol-netem-XXXXXX")"
echo "Evidence: $out"
go test -c -o "$out/remote.test" ./shellport/remote
ns_core="crude-netem-core-$$"
ns_client="crude-netem-client-$$"
cleanup() {
  sudo ip netns del "$ns_core" 2>/dev/null || true
  sudo ip netns del "$ns_client" 2>/dev/null || true
}
trap cleanup EXIT
as_user() {
  local ns="$1" rv="$2"; shift 2
  sudo ip netns exec "$ns" setpriv --reuid="$(id -u)" --regid="$(id -g)" --init-groups \
    env -i HOME="$HOME" PATH="$PATH" LC_ALL=C GOMAXPROCS="${GOMAXPROCS:-2}" TMPDIR="$out" CRUDE_NETEM_DIR="$rv" "$@"
}
failed=0
{ go version; uname -srmo; grep 'model name' /proc/cpuinfo | head -1; } > "$out/environment.txt"
for profile in $PROFILES; do
  delay="${profile%/*}" loss="${profile#*/}"
  run="$out/delay-$delay-loss-${loss%\%}"
  mkdir -p "$run"
  rv="$run/rendezvous"
  mkdir -m 0700 "$rv"
  cleanup
  sudo ip netns add "$ns_core"
  sudo ip netns add "$ns_client"
  # The veth pair is created inside the core namespace, so no end ever exists
  # in the host namespace.
  sudo ip -n "$ns_core" link add vcore type veth peer name vclient netns "$ns_client"
  for spec in "$ns_core vcore 10.203.0.1" "$ns_client vclient 10.203.0.2"; do
    read -r ns dev addr <<<"$spec"
    sudo ip -n "$ns" link set lo up
    sudo ip -n "$ns" addr add "$addr/30" dev "$dev"
    sudo ip -n "$ns" link set "$dev" up
    sudo ip netns exec "$ns" tc qdisc add dev "$dev" root netem delay "$delay" loss "$loss"
  done
  {
    echo "netem: delay $delay loss $loss on each veth end"
    sudo ip netns exec "$ns_core" tc qdisc show dev vcore
    sudo ip netns exec "$ns_client" tc qdisc show dev vclient
    sudo ip netns exec "$ns_client" ping -c 20 -i 0.2 -q 10.203.0.1 | tail -2
  } > "$run/network.txt" 2>&1
  cat "$run/network.txt"
  as_user "$ns_core" "$rv" CRUDE_NETEM_ROLE=core CRUDE_NETEM_LISTEN=10.203.0.1:7340 \
    "$out/remote.test" -test.run '^TestNetemCoreRole$' -test.count=1 -test.timeout=300s -test.v > "$run/core.log" 2>&1 &
  core_pid=$!
  as_user "$ns_client" "$rv" CRUDE_NETEM_ROLE=client \
    "$out/remote.test" -test.run '^TestNetemClientRole$' -test.count=1 -test.timeout=240s -test.v > "$run/client.log" 2>&1 || failed=1
  wait "$core_pid" || failed=1
  cat "$run/client.log"
  grep -E '^(--- |FAIL|ok|PASS)' "$run/core.log" || true
done
echo "Evidence: $out"
if ((failed)); then echo 'FAIL: see the netem gate output above'; exit 1; fi
echo 'PASS: netem QUIC gate (two private namespaces; not physically separate hosts)'
