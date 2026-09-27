#!/usr/bin/env bash
# Phase 15 bench regression gate.
#
# Runs the canonical performance benchmarks with -benchmem and fails when a
# benchmark EXCEEDS its allocation ceiling from scripts/perf_ceilings.txt
# (the machine-readable twin of docs/reference/performance-baselines.md).
# Allocations are hardware-independent, so ceilings are enforceable in CI;
# timing is intentionally NOT gated (CPU frequency variation across runners
# makes wall-clock thresholds noise).
#
# Usage: scripts/tui_bench_gate.sh
set -euo pipefail

cd "$(dirname "$0")/.."

CEILINGS_FILE="scripts/perf_ceilings.txt"

# name -> "bytes allocs" ceilings
ceiling() {
  awk -v name="$1" '$1 == name {print $2, $3; found=1; exit} END {if (!found) print ""}' "$CEILINGS_FILE"
}

fail=0

run_bench() {
  # $1 = bench regex, $2.. = packages (each a separate go test arg)
  local out
  out="$(GOMAXPROCS=2 go test -run '^$' -bench "$1" -benchmem -count=1 "${@:2}" 2>&1)" || {
    echo "FAIL: benchmark run errored: $1 ($*)"
    echo "$out" | tail -20
    fail=1
    return
  }
  # Parse "BenchmarkName-N  count  ns/op  B/op  allocs/op"
  # fields: 1=name 2=count 3=ns 4=ns/op 5=B 6=B/op 7=allocs 8=allocs/op
  while read -r name _ _ _ b _ alloc _; do
    # strip -N suffix for ceiling lookup
    base="${name%-*}"
    ceils="$(ceiling "$base")"
    if [[ -z "$ceils" ]]; then
      echo "WARN: no ceiling recorded for $base — add one to $CEILINGS_FILE"
      continue
    fi
    read -r maxB maxAlloc <<< "$ceils"
    if [[ "$b" -gt "$maxB" || "$alloc" -gt "$maxAlloc" ]]; then
      echo "FAIL: $base allocations ${b}B/${alloc} allocs exceed ceiling ${maxB}B/${maxAlloc} allocs"
      fail=1
    else
      echo "OK: $base ${b}B ${alloc} allocs (ceiling ${maxB}B/${maxAlloc})"
    fi
  done < <(echo "$out" | grep -E '^Benchmark[A-Za-z0-9_]+-[0-9]+[[:space:]]+[0-9]+')
}

echo "== TUI bench regression gate =="
run_bench 'Benchmark(DemoGallery|Blit|WriteString|Flatten|FullRender|GenerateANSI)' ./tui/ui ./tui/surface ./tui/compositor
run_bench 'Benchmark(EditorPaint|EditorScrollRepaint)' ./tui/screens/shelleditor
run_bench '^BenchmarkBuffer(DirtyCheckLargeFile|SavepointManualRoundTripLargeFile)$' ./tui/editor
run_bench 'BenchmarkChatStreamPaint' ./tui/screens/shellchat
run_bench '^BenchmarkLargeDiffPreview$' ./tui/chatprojection
run_bench '^BenchmarkOpenIndexedHistory$' ./localhistory
run_bench '^Benchmark(ProjectInsightsCached10kDiagnostics|UsageSummaryCached1000Requests)$' ./workspace
# Scenario benches: steady-state work budgets (see scripts/perf_ceilings.txt).
run_bench 'Benchmark(IdleTick|TabRederive|PointerMotionCoalesced|AgentSwitchWarm|SidebarResizePresentedFrame_500Msgs|SidebarResizeMutation_500Msgs|DenseChatSingleRowChange_500Msgs|DenseChatWidthCommit_500HeavyMsgs)' ./tui/viewmodel

if [[ $fail -ne 0 ]]; then
  echo "== BENCH GATE FAILED =="
  exit 1
fi
echo "== BENCH GATE PASSED =="
