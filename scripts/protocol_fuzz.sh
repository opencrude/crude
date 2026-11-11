#!/usr/bin/env bash
# Bounded native campaigns. Failure corpora/cache/logs stay in CRUDE_TEMP_DIR;
# promoting a minimized corpus into source is a separate, explicit review.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
. scripts/libghostty_env.sh
export GOMAXPROCS="${GOMAXPROCS:-2}"
export LC_ALL=C
: "${CRUDE_TEMP_DIR:?Set CRUDE_TEMP_DIR to an evidence directory}"
cases_override="${FUZZ_CASES:-}"
if [[ -n "$cases_override" ]]; then
  if ! [[ "$cases_override" =~ ^[1-9][0-9]*$ ]] || ((${#cases_override} > 7)) ||
    (( cases_override > 1000000 )); then
    echo 'FAIL: FUZZ_CASES must be 1–1000000 generated executions' >&2
    exit 1
  fi
fi
[[ "$(go env CGO_ENABLED)" == 1 ]] || { echo 'FAIL: native cgo terminal dependency required' >&2; exit 1; }
out="$(mktemp -d "$CRUDE_TEMP_DIR/protocol-fuzz-XXXXXX")"
out="$(cd "$out" && pwd)"
# A real short directory, not a symlink: Unix socket paths have a hard limit.
home="$(mktemp -d /tmp/crude-fuzz-XXXXXX)"
trap 'rm -rf -- "$home"' EXIT
export CRUDE_HOME="$home"
echo "Evidence: $out"

# One instrumented executable per package, containing every named target in it.
# FUZZ_TARGETS is an optional space-separated exact-name subset for local work.
inventory=(
  './coreprotocol:FuzzEveryWireDecoder FuzzWireTimestampMessage'
  './corewire:FuzzMessageReader FuzzMessageFragmentation FuzzReaderLimitTransitions FuzzCompressedStream FuzzMuxFrames FuzzMuxStateMachine FuzzQUICStreamClass'
  './coreclient:FuzzBulkReceiver FuzzTerminalInputAdmissionOrdering FuzzWorkspaceTopicReplicaState'
  './shellport/remote:FuzzRemoteStateTopicFences=64'
  './workspace:FuzzConversationStreamReaders FuzzConversationMalformedTextOffset FuzzTerminalLeaseStateMachine=64'
  './tui/editor:FuzzDocumentUpdateAtomicBoundary'
  './terminalvt:FuzzPackedRow FuzzPackedDictionaryRow'
  './core:FuzzTerminalGridAuthorityStream=4096 FuzzCoreFeatureDocuments=64 FuzzCoreFeatureFilesHistory=64 FuzzCoreFeatureSettings=64 FuzzCoreFeatureDraftCAS=64 FuzzCoreSessionOwnership=64 FuzzCoreSessionTerminalLifecycle=64 FuzzCoreInputAdmissionReceipts=64 FuzzCoreSessionSettingsReset=64 FuzzCoreGitReviewedMutations=64 FuzzCoreLSPRequests=64 FuzzCoreCapabilities=64 FuzzCoreStartupWorkspaces=64 FuzzCoreRemoteAuthentication=64 FuzzCoreBulkLifecycle=64 FuzzCoreMetricsRequests=64'
  './tui/viewmodel:FuzzAppFrameState=64'
  './tui/compositor:FuzzDirectSurfaceResize'
  './third_party/bubbletea:FuzzDirectRendererResizeAndConflation FuzzDirectSurfaceTerminalOutput'
  './third_party/ultraviolet:FuzzParseSequence'
  './remoteconn:FuzzCurrentFormatDocuments'
)
selected=" ${FUZZ_TARGETS:-} "
found=' '
for package_entry in "${inventory[@]}"; do
  package="${package_entry%%:*}"
  names="${package_entry#*:}"
  targets=()
  for entry in $names; do
    name="${entry%%=*}"
    if [[ -z "${FUZZ_TARGETS:-}" || "$selected" == *" $name "* ]]; then
      targets+=("$entry")
      found+="$name "
    fi
  done
  ((${#targets[@]})) || continue
  label="${package#./}"
  label="${label//\//-}"
  work="$out/$label"
  mkdir -p "$work"
  target_names=()
  for entry in "${targets[@]}"; do target_names+=("${entry%%=*}"); done
  regex="$(IFS='|'; echo "${target_names[*]}")"
  binary="$work/fuzz.test"
  echo "Compile $package: $regex"
  (cd "$package" && go test -c -fuzz "^($regex)$" -o "$binary" .) >"$work/compile.log" 2>&1 || {
    cat "$work/compile.log" >&2
    echo "FAIL: native fuzz compilation/dependency: $package" >&2
    exit 1
  }
  "$binary" -test.list '^Fuzz' >"$work/targets.txt"
  # Preserve checked-in seeds without ever writing a crash back to the checkout.
  if [[ -d "$package/testdata/fuzz" ]]; then
    mkdir -p "$work/testdata"
    cp -R "$package/testdata/fuzz" "$work/testdata/fuzz"
  fi
  for entry in "${targets[@]}"; do
    name="${entry%%=*}"
    cases=65536
    [[ "$entry" == *=* ]] && cases="${entry#*=}"
    cases="${cases_override:-$cases}"
    grep -Fxq "$name" "$work/targets.txt" || { echo "FAIL: missing target $package/$name" >&2; exit 1; }
    # Discover and validate the complete baseline with the real target. Go's
    # execution limit includes warmup, so a fixed total alone can stop before
    # a large seed inventory is even complete.
    (cd "$work" && "$binary" -test.run "^$name$" -test.v -test.timeout 120s) \
      >"$work/$name.seeds.log" 2>&1 || {
        cat "$work/$name.seeds.log" >&2
        echo "FAIL: seed validation $package/$name" >&2
        exit 1
      }
    seeds="$(grep -Ec "^=== RUN[[:space:]]+$name/[^/]+$" "$work/$name.seeds.log")" || {
      echo "FAIL: no seed executions observed for $package/$name" >&2
      exit 1
    }
    echo "Fuzz $package/$name ($seeds seeds + $cases generated executions)"
    # No output truncation and no swallowed failures. Native fuzz owns its exact
    # workers; normal signal/exit unwinding leaves all evidence here for review.
    (cd "$work" && "$binary" -test.run '^$' -test.fuzz "^$name$" \
        -test.fuzztime "$((seeds + cases))x" -test.parallel "$GOMAXPROCS" \
        -test.fuzzcachedir "$work/cache" -test.timeout 120s) \
      >"$work/$name.log" 2>&1 || {
        cat "$work/$name.log" >&2
        echo "FAIL: $package/$name (corpus and complete log: $work)" >&2
        exit 1
      }
    cat "$work/$name.log"
    grep -q 'now fuzzing with' "$work/$name.log" || {
      echo "FAIL: $package/$name stopped before generated-input fuzzing" >&2
      exit 1
    }
  done
done
for name in ${FUZZ_TARGETS:-}; do
  [[ "$found" == *" $name "* ]] || { echo "FAIL: unknown requested target: $name" >&2; exit 1; }
done
echo "PASS: bounded native fuzz campaigns; evidence: $out"
