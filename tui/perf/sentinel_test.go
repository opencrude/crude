package perf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sentinelWithFile points the sentinel at a temp file, clears window
// bookkeeping, and restores after the test.
func sentinelWithFile(tb testing.TB) string {
	tb.Helper()
	f, err := os.Create(filepath.Join(tb.TempDir(), "perf.log"))
	if err != nil {
		tb.Fatal(err)
	}
	previous := sentinelFile
	sentinelFile = f
	EnableProbe()
	Reset()
	tickCount.Store(0)
	windowBase = Snapshot()
	ResetSentinelState()
	tb.Cleanup(func() {
		DisableProbe()
		ResetSentinelState()
		sentinelFile = previous
		_ = f.Close()
	})
	return targetPath(f)
}

func targetPath(f *os.File) string {
	name, err := filepath.Abs(f.Name())
	if err != nil {
		return f.Name()
	}
	return name
}

func readLog(tb testing.TB, path string) string {
	tb.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		tb.Fatal(err)
	}
	return string(b)
}

// TestSentinelWarnsOnIdleBudgetBust: an idle window whose render work exceeds
// the budget produces exactly one warning naming the top probe site, and the
// throttle stays silent for an immediate repeat of the same bust.
func TestSentinelWarnsOnIdleBudgetBust(t *testing.T) {
	path := sentinelWithFile(t)

	// A per-tick full re-render: 20 items / ~60 lines per frame plus a
	// whole-projection invalidation, the signature of every bug class from
	// the 2026-08-30 incident.
	for range sentinelWindowTicks {
		MarkCacheInvalidation()
		for range 20 {
			MarkItemRendered(3)
		}
		SentinelTick()
	}
	log := readLog(t, path)
	if !strings.Contains(log, "items=") || !strings.Contains(log, "cache-invalidate") && !strings.Contains(log, "item-rendered") {
		if log == "" {
			t.Fatal("sentinel wrote no warning for a per-tick re-render bust")
		}
		t.Fatalf("warning lacks attribution:\n%s", log)
	}

	// Throttle: an immediate second bust with the same top site stays silent.
	for range sentinelWindowTicks {
		MarkItemRendered(40)
		SentinelTick()
	}
	log2 := readLog(t, path)
	if strings.Count(log2, "crude-perf:") != 1 && sameTopSite(log, log2) {
		t.Fatalf("throttle did not suppress an identical repeated bust:\n%s\n---\n%s", log, log2)
	}
}

// TestSentinelQuietWhenWithinBudgetOrActive: windows inside the budget, and
// windows flagged as interactive, never log.
func TestSentinelQuietWhenWithinBudgetOrActive(t *testing.T) {
	path := sentinelWithFile(t)

	for range sentinelWindowTicks {
		MarkItemRendered(2)
		SentinelTick()
	}
	if log := readLog(t, path); log != "" {
		t.Fatalf("within-budget window logged:\n%s", log)
	}

	NoteActivity() // streaming / user input: work is expected
	for range sentinelWindowTicks {
		MarkItemRendered(60)
		SentinelTick()
	}
	if log := readLog(t, path); log != "" {
		t.Fatalf("active window logged:\n%s", log)
	}
}

// TestProbeAttribution: the probe ring records the caller of the marker.
func TestProbeAttribution(t *testing.T) {
	EnableProbe()
	t.Cleanup(DisableProbe)
	Reset()

	callerOfMark := func() { MarkItemRendered(3) }
	callerOfMark()
	callerOfMark()

	dump := ProbeDump()
	if strings.Count(dump, "x ") != 1 {
		t.Fatalf("expected exactly one probe site, dump:\n%s", dump)
	}
	if !strings.Contains(dump, "TestProbeAttribution") {
		t.Fatalf("probe dump lost caller context:\n%s", dump)
	}
}

func sameTopSite(a, b string) bool {
	return firstLine(a) == firstLine(b)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
