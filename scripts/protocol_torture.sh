#!/usr/bin/env bash
# Isolated opt-in real-process protocol gates. No sudo, no host tc changes,
# no capture windows, no model credentials, no connection to the user's core.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/libghostty_env.sh
export LC_ALL=C
export GOMAXPROCS="${GOMAXPROCS:-2}"
export CRUDE_PROTOCOL_TORTURE=1
export CRUDE_TORTURE_HTOP="${CRUDE_TORTURE_HTOP:-$(command -v htop || true)}"
export CRUDE_TORTURE_FFMPEG="${CRUDE_TORTURE_FFMPEG:-/home/lenovo/tools/ffmpeg/ffmpeg}"
export CRUDE_TORTURE_MPV="${CRUDE_TORTURE_MPV:-/home/lenovo/tools/mpv/mpv}"
for binary in "$CRUDE_TORTURE_HTOP" "$CRUDE_TORTURE_FFMPEG" "$CRUDE_TORTURE_MPV"; do
  [[ -n "$binary" && -x "$binary" ]] || { echo "FAIL: required executable unavailable: $binary" >&2; exit 1; }
done
[[ "$(uname -s)" == Linux ]] || { echo 'FAIL: native Linux PTY/pidfd required' >&2; exit 1; }
: "${CRUDE_TEMP_DIR:?Set CRUDE_TEMP_DIR to an evidence directory}"
out="$(mktemp -d "$CRUDE_TEMP_DIR/protocol-torture-XXXXXX")"
export CRUDE_HOME="$out/home"
mkdir -p "$CRUDE_HOME"
echo "Evidence: $out"
{ go version; uname -srmo; grep 'model name' /proc/cpuinfo | head -1; echo "GOMAXPROCS=$GOMAXPROCS"; grep CapEff /proc/self/status; } > "$out/environment.txt"
failed=0
go test -o "$out/core.test" ./core -run '^TestProtocolTorture' -count=1 -timeout=180s -v > "$out/journeys.log" 2>&1 || failed=1
cat "$out/journeys.log"
go test -c -fuzz '^FuzzTerminalInputAdmissionOrdering$' -o "$out/coreclient.test" ./coreclient
# Run from the evidence directory: native fuzz failure corpora belong there,
# never as an unreviewed write into the source checkout.
(cd "$out" && ./coreclient.test -test.run '^FuzzTerminalInputAdmissionOrdering$' -test.fuzz '^FuzzTerminalInputAdmissionOrdering$' -test.fuzzcachedir "$out/input-fuzz-cache" -test.fuzztime=35s -test.parallel=2 -test.timeout=90s) > "$out/fuzz.log" 2>&1 || failed=1
cat "$out/fuzz.log"
go test -c -fuzz '^FuzzTerminalGridAuthorityStream$' -o "$out/core-fuzz.test" ./core
(cd "$out" && ./core-fuzz.test -test.run '^FuzzTerminalGridAuthorityStream$' -test.fuzz '^FuzzTerminalGridAuthorityStream$' -test.fuzzcachedir "$out/grid-fuzz-cache" -test.fuzztime=35s -test.parallel=2 -test.timeout=90s) > "$out/grid-fuzz.log" 2>&1 || failed=1
cat "$out/grid-fuzz.log"
# Separate profiled run: instrumentation is not mixed into the unprofiled gate.
go test -o "$out/core-profile.test" ./core -run '^TestProtocolTortureMPVPTYResponse$' -count=1 -timeout=120s -v -cpuprofile "$out/flood.cpu" -memprofile "$out/flood.heap" > "$out/profile.log" 2>&1 || failed=1
if [[ -f "$out/flood.cpu" ]]; then
  go tool pprof -top -nodecount=25 "$out/core-profile.test" "$out/flood.cpu" > "$out/cpu-top.txt"
  go tool pprof -top -alloc_space -nodecount=25 "$out/core-profile.test" "$out/flood.heap" > "$out/alloc-top.txt"
fi
echo 'UNVERIFIED: actual outer presented FPS; shell-only GC CPU; key-to-PTY-write p99; distinct-host SSH/QUIC (netem: scripts/protocol_netem.sh). No host interfaces modified.'
echo "Evidence: $out"
if ((failed)); then echo 'FAIL: see measured gates above'; exit 1; fi
echo 'PASS: implemented gates only; UNVERIFIED rows remain open.'
