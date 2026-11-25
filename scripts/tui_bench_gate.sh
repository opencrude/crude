#!/usr/bin/env bash
# Phase 15 bench regression gate.
#
# Runs the canonical performance benchmarks with -benchmem and fails when a
# benchmark EXCEEDS its allocation ceiling from scripts/perf_ceilings.txt
# (the machine-readable twin of docs/reference/performance-baselines.md).
# Allocations are hardware-independent but can differ by GOOS/toolchain runtime,
# so the ceiling file supports narrow GOOS overrides while retaining one generic
# CI baseline. Timing is intentionally NOT gated (CPU frequency variation across
# runners makes wall-clock thresholds noise).
# Dense off-timer restoration cases use fixed samples; other workloads keep
# duration calibration, including the amortization of pooled scratch storage.
#
# Usage: scripts/tui_bench_gate.sh
set -euo pipefail

cd "$(dirname "$0")/.."

CEILINGS_FILE="scripts/perf_ceilings.txt"
BENCH_GOOS="$(go env GOOS)"

# Optional, caller-owned durable evidence in Go benchmark format. The gate uses
# the same samples and ceilings whether or not evidence is requested.
if [[ -n "${BENCH_EVIDENCE:-}" ]]; then
  revision="$(git rev-parse HEAD)"
  cohort="$(sed -n 's/^const ProtocolBuild = "\([0-9a-f]*\)"$/\1/p' internal/buildinfo/cohort_gen.go)"
  [[ ${#cohort} -eq 64 ]]
  tree_state=clean
  if [[ -n "$(git status --porcelain)" ]]; then tree_state=modified; fi
  {
    printf 'revision: %s\nsource_cohort: %s\nworktree: %s\n' "$revision" "$cohort" "$tree_state"
    printf 'go_version: %s\ngomaxprocs: 2\nos_release: %s\n' "$(go env GOVERSION)" "$(uname -rs)"
    printf 'suite: crude-bench-gate\n'
  } > "$BENCH_EVIDENCE"
fi

# name -> "bytes allocs" ceilings. A four-column "<name> <goos> <bytes>
# <allocs>" row overrides the generic three-column row for the active GOOS.
ceiling() {
  awk -v name="$1" -v goos="$BENCH_GOOS" '
    $1 != name { next }
    NF == 4 && $2 == goos {
      print $3, $4
      exact = 1
      exit
    }
    NF == 3 && fallback == "" {
      fallback = $2 " " $3
    }
    END {
      if (!exact && fallback != "") print fallback
    }
  ' "$CEILINGS_FILE"
}

fail=0

run_bench() {
  # $1 = bench regex, $2.. = packages (each a separate go test arg)
  local out
  out="$(GOMAXPROCS=2 go test -run '^$' -bench "$1" -benchtime="${BENCHTIME:-1s}" -benchmem -count=1 "${@:2}" 2>&1)" || {
    echo "FAIL: benchmark run errored: $1 ($*)"
    echo "$out" | tail -20
    fail=1
    return
  }
  if [[ -n "${BENCH_EVIDENCE:-}" ]]; then
    printf '\nbenchmark_expression: %s\nbenchmark_duration: %s\n%s\n' "$1" "${BENCHTIME:-1s}" "$out" >> "$BENCH_EVIDENCE"
  fi
  # Sub-benchmark names can contain '='. Custom metrics can precede B/op;
  # identify the two allocation values by unit rather than field number.
  local rows
  rows="$(awk '/^Benchmark[^[:space:]]+-[0-9]+[[:space:]]+[0-9]+/ {
    b = a = "-"
    for (i=3; i<=NF; i++) {
      if ($i == "B/op") b = $(i-1)
      if ($i == "allocs/op") a = $(i-1)
    }
    print $1, b, a
  }' <<< "$out")"
  if [[ -z "$rows" ]]; then
    echo "FAIL: no benchmark results for $1"
    fail=1
    return
  fi
  while read -r name b alloc; do
    if [[ ! "$b" =~ ^[0-9]+$ || ! "$alloc" =~ ^[0-9]+$ ]]; then
      echo "FAIL: missing allocation metrics for $name"
      fail=1
      continue
    fi
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
  done <<< "$rows"
}

if (( $# )); then
  if [[ "$1" != "--only" || $# -lt 3 ]]; then
    echo "Usage: $0 [--only BENCHMARK_REGEX PACKAGE ...]" >&2
    exit 2
  fi
  shift
  run_bench "$@"
  exit "$fail"
fi

echo "== TUI bench regression gate ($BENCH_GOOS) =="
run_bench 'Benchmark(DemoGallery|Blit|WriteString|Flatten|FullRender|GenerateANSI)' ./tui/ui ./tui/surface ./tui/compositor
run_bench '^BenchmarkDirectAnimationFrame$' ./tui/compositor
run_bench '^BenchmarkFillRectDenseSpans$' ./tui/surface
run_bench 'Benchmark(EditorPaint|EditorScrollRepaint)' ./tui/screens/shelleditor
run_bench '^BenchmarkBuffer(DirtyCheckLargeFile|SavepointManualRoundTripLargeFile)$' ./tui/editor
run_bench '^BenchmarkSyntax(LongPythonViewport|DocumentPreparation|MapInlineDecorations)$' ./tui/editor
run_bench '^BenchmarkSnippetTypingDense$/^lines20000$/^mirrors(8|64)$/^paint(false|true)$' ./tui/editor
run_bench '^Benchmark(CompletionLeadingStopsDense|OccurrenceProjectionLongLine)$' ./tui/editor
run_bench '^Benchmark(CompletionNormalization|HighlightNormalizationLongLine)$' ./workspace
run_bench '^BenchmarkInlayReaderStableLayout$' ./tui/screens/inlay
run_bench '^BenchmarkSemantic(DecodeDense|ParseDense)$' ./lsp/lspsemantic
# These restore a dense source outside each measured stage. Duration calibration
# otherwise selects excessive fixture work without collecting a better sample.
BENCHTIME=100x run_bench '^BenchmarkSemanticEditingDense$' ./tui/editor
run_bench '^BenchmarkEditorMixedDirectionPan$' ./tui/editor
run_bench '^BenchmarkWrapped(EditorWorkload|RowPreparation)$' ./tui/editor
BENCHTIME=100x run_bench '^BenchmarkMultipleCaret(Workload|Stages)$' ./tui/editor
run_bench '^Benchmark(EditorOrientation|MinimapRebuild)$' ./tui/editor
run_bench '^BenchmarkNativeSyntax(Preparation|Incremental|ColdSession)$' ./insight/syntax
run_bench 'BenchmarkChatStreamPaint' ./tui/screens/shellchat
run_bench '^BenchmarkTextTidePaint$' ./tui/chatprojection
run_bench '^BenchmarkMixedMarkdown' ./tui/chatprojection
run_bench '^BenchmarkChatFind' ./tui/screens/shellchat
run_bench '^BenchmarkDenseFileLinkHit$' ./tui/screens/shellchat
run_bench '^BenchmarkParseSourceReference$' ./internal/filelink
run_bench '^BenchmarkFooterOverflowPaint$' ./tui/screens/shellfooter
run_bench '^BenchmarkSearchProjectDense$' ./workspace/fileio
run_bench '^BenchmarkProjectSearchSelection$' ./tui/screens/findinfiles
run_bench '^BenchmarkLargeDiffPreview$' ./tui/chatprojection
run_bench '^BenchmarkDenseCommand(SettlementPresentedFrame|Reveal)$' ./tui/chatprojection
run_bench '^BenchmarkDense(Syntax)?DiffPaint$' ./tui/screens/diffview
run_bench '^BenchmarkGitDiffLongLine(Paint|FarRightPaint)$' ./tui/screens/gitdiff
run_bench '^BenchmarkOpenIndexedHistory$' ./workspace/localhistory
run_bench '^BenchmarkPublishContext(ReplaceUnrelatedArchiveHistory|RecoveryImage)$' ./agent/sessiondb
run_bench '^BenchmarkDenseRecoverySnapshot$' ./agent/session
run_bench '^Benchmark(DenseNoticeFooterReads|TickPreferenceReads)$' ./core/shellport/remote
run_bench '^Benchmark(CheckpointAppend_560Msgs|RecoveryCheckpoint_(560|2000)Msgs)$' ./agent/sessiondb
run_bench '^BenchmarkSessionDatabaseStartup$' ./agent/sessiondb
run_bench '^BenchmarkEstimateMultipartImage$' ./agent/provider
run_bench '^Benchmark(ProjectInsightsCached10kDiagnostics|UsageSummaryCached1000Requests)$' ./workspace
# Scenario benches: steady-state work budgets (see scripts/perf_ceilings.txt).
run_bench 'Benchmark(IdleTick|IdlePresentedFrame|AnimatedPresentedFrame|TickHiddenNotices|TabRederive|PointerMotionCoalesced|AgentSwitchWarm|SidebarResizePresentedFrame_500Msgs|SidebarResizeMutation_500Msgs|DenseChatSingleRowChange_500Msgs|DenseChatWidthCommit_500HeavyMsgs)' ./tui/viewmodel
run_bench '^BenchmarkMainPane$' ./tui/viewmodel
run_bench '^BenchmarkDenseChildPane(Sync|Paint)$' ./tui/viewmodel
# Reset the live call outside each timed settlement, at a fixed history size.
BENCHTIME=10x run_bench '^BenchmarkDenseChildPaneUpdate$' ./tui/viewmodel

if [[ $fail -ne 0 ]]; then
  echo "== BENCH GATE FAILED =="
  exit 1
fi
echo "== BENCH GATE PASSED =="
