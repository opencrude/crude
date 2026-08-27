package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTerminalWidthProbeBuildsMeasuredProfile(t *testing.T) {
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	probe := &terminalWidthProbe{
		row:          20,
		clusters:     []string{"x", "·", "○", "☰", "🧑‍🌾"},
		measurements: make(map[string]int),
	}
	for index, width := range []int{1, 2, 2, 1, 4} {
		consumed, complete := probe.accept(CursorPositionMsg{X: width, Y: 19})
		if !consumed || complete != (index == 4) {
			t.Fatalf("response %d: consumed=%v complete=%v", index, consumed, complete)
		}
	}
	profile, changed := probe.profile()
	if !changed || profile.AmbiguousWidth != 2 || !profile.LegacyGraphemeClusters {
		t.Fatalf("profile = %#v changed=%v", profile, changed)
	}
	if profile.Exact["☰"] != 1 || profile.Exact["🧑‍🌾"] != 4 {
		t.Fatalf("exact corrections = %#v", profile.Exact)
	}
}

func TestTerminalWidthProbeSequenceIsInvisibleAndComplete(t *testing.T) {
	probe := newTerminalWidthProbe(1, 24, []string{"◆", "◆"})
	sequence := probe.sequence()
	if got := strings.Count(sequence, ansi.RequestCursorPositionReport); got != len(probe.clusters) {
		t.Fatalf("cursor reports = %d, want %d", got, len(probe.clusters))
	}
	for _, required := range []string{
		ansi.SetModeSynchronizedOutput,
		ansi.SaveCursor,
		ansi.SetModeUnicodeCore,
		ansi.EraseEntireLine,
		ansi.RestoreCursor,
		ansi.ResetModeSynchronizedOutput,
	} {
		if !strings.Contains(sequence, required) {
			t.Fatalf("probe sequence missing %q", required)
		}
	}
}

func TestTerminalWidthProbeRejectsUnrelatedCursorReport(t *testing.T) {
	probe := newTerminalWidthProbe(1, 24, nil)
	consumed, complete := probe.accept(CursorPositionMsg{X: 1, Y: 3})
	if consumed || complete || probe.next != 0 {
		t.Fatalf("unrelated report consumed=%v complete=%v next=%d", consumed, complete, probe.next)
	}
}
