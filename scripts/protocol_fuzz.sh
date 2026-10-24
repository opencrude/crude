#!/usr/bin/env bash
# Bounded native campaigns. Failure corpora/cache/logs stay in CRUDE_TEMP_DIR;
# promoting a minimized corpus into source is a separate, explicit review.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
. scripts/libghostty_env.sh
export GOMAXPROCS="${GOMAXPROCS:-2}"
export LC_ALL=C
: "${CRUDE_TEMP_DIR:?Set CRUDE_TEMP_DIR to an evidence directory}"
seconds="${FUZZ_SECONDS:-2}"
[[ "$seconds" =~ ^[1-9][0-9]*$ ]] || { echo 'FAIL: FUZZ_SECONDS must be positive integer seconds' >&2; exit 1; }
(( seconds <= 3600 )) || { echo 'FAIL: FUZZ_SECONDS exceeds the 3600-second bound' >&2; exit 1; }
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
  './shellport/remote:FuzzRemoteStateTopicFences'
  './workspace:FuzzConversationStreamReaders FuzzConversationMalformedTextOffset FuzzTerminalLeaseStateMachine'
  './tui/editor:FuzzDocumentUpdateAtomicBoundary'
  './terminalvt:FuzzPackedRow FuzzPackedDictionaryRow'
  './core:FuzzTerminalGridAuthorityStream FuzzCoreFeatureDocuments FuzzCoreFeatureFilesHistory FuzzCoreFeatureSettings FuzzCoreFeatureDraftCAS FuzzCoreSessionOwnership FuzzCoreSessionTerminalLifecycle FuzzCoreInputAdmissionReceipts FuzzCoreSessionSettingsReset FuzzCoreGitReviewedMutations FuzzCoreLSPRequests FuzzCoreCapabilities FuzzCoreStartupWorkspaces FuzzCoreRemoteAuthentication FuzzCoreBulkLifecycle FuzzCoreMetricsRequests'
  './tui/viewmodel:FuzzAppFrameState'
  './tui/compositor:FuzzDirectSurfaceResize'
  './third_party/bubbletea:FuzzDirectRendererResizeAndConflation FuzzDirectSurfaceTerminalOutput'
  './third_party/ultraviolet:FuzzParseSequence'
  './remoteconn:FuzzCurrentFormatDocuments'
)
selected=" ${FUZZ_TARGETS:-} "
found=' '
for entry in "${inventory[@]}"; do
  package="${entry%%:*}"
  names="${entry#*:}"
  targets=()
  for name in $names; do
    if [[ -z "${FUZZ_TARGETS:-}" || "$selected" == *" $name "* ]]; then
      targets+=("$name")
      found+="$name "
    fi
  done
  ((${#targets[@]})) || continue
  label="${package#./}"
  label="${label//\//-}"
  work="$out/$label"
  mkdir -p "$work"
  regex="$(IFS='|'; echo "${targets[*]}")"
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
  for name in "${targets[@]}"; do
    grep -Fxq "$name" "$work/targets.txt" || { echo "FAIL: missing target $package/$name" >&2; exit 1; }
    echo "Fuzz $package/$name (${seconds}s)"
    # No output truncation and no swallowed failures. Native fuzz owns its exact
    # workers; normal signal/exit unwinding leaves all evidence here for review.
    (cd "$work" && "$binary" -test.run '^$' -test.fuzz "^$name$" \
      -test.fuzztime "${seconds}s" -test.parallel "$GOMAXPROCS" \
      -test.fuzzcachedir "$work/cache" -test.timeout "$((seconds + 120))s") \
      >"$work/$name.log" 2>&1 || {
        cat "$work/$name.log" >&2
        echo "FAIL: $package/$name (corpus and complete log: $work)" >&2
        exit 1
      }
    cat "$work/$name.log"
  done
done
for name in ${FUZZ_TARGETS:-}; do
  [[ "$found" == *" $name "* ]] || { echo "FAIL: unknown requested target: $name" >&2; exit 1; }
done
echo "PASS: bounded native fuzz campaigns; evidence: $out"
