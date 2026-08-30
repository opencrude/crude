#!/usr/bin/env bash
# Phase 15 bench regression gate.
#
# Runs the canonical performance benchmarks with -benchmem and fails when a
# benchmark EXCEEDS its documented allocation ceiling from
# docs/reference/performance-baselines.md. Allocations are hardware-independent, so
# ceilings are enforceable in CI; timing is intentionally NOT gated (CPU
# frequency variation across runners makes wall-clock thresholds noise).
#
# Usage: scripts/tui_bench_gate.sh
set -euo pipefail

cd "$(dirname "$0")/.."

# name -> "bytes allocs" ceilings (from docs/reference/performance-baselines.md)
# Lines: "BenchmarkName  B/op  allocs/op"
ceiling() {
  case "$1" in
    BenchmarkDemoGalleryPaint_80x24)            echo "1300 60" ;;
    BenchmarkDemoGalleryPaint_120x40)           echo "2000 60" ;;
    BenchmarkDemoGalleryHandleKeyEditCycle)     echo "48 3" ;;
    BenchmarkBlit_80x24)                        echo "0 0" ;;
    BenchmarkWriteString_Line)                  echo "0 0" ;;
    BenchmarkFlattenZOrder_Nested)              echo "2300 8" ;;
    BenchmarkFullRender_80x24)                  echo "12000 6" ;;
    BenchmarkGenerateANSI_80x24)                echo "9000 2" ;;
    BenchmarkEditorPaint_80x24)                 echo "9000 1200" ;;
    BenchmarkEditorPaint_120x40)                echo "20000 2200" ;;
    BenchmarkEditorPaint_LargeFile_120x40)      echo "20000 2200" ;;
    BenchmarkEditorScrollRepaint_120x40)        echo "20000 2200" ;;
    BenchmarkChatStreamPaint_80x24)             echo "9000 300" ;;
    BenchmarkChatStreamPaint_120x40)            echo "20000 400" ;;
    *) echo "" ;;
  esac
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
      echo "WARN: no ceiling recorded for $base — update docs/reference/performance-baselines.md or the gate"
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
run_bench 'BenchmarkChatStreamPaint' ./tui/screens/shellchat

if [[ $fail -ne 0 ]]; then
  echo "== BENCH GATE FAILED =="
  exit 1
fi
echo "== BENCH GATE PASSED =="
