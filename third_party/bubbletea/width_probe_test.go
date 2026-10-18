package tea

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
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

func TestTerminalWidthProbePreservesMeasurementsAfterClassFallback(t *testing.T) {
	// These independent CPR values come from Alacritty 0.13.2 directly and
	// through tmux 3.4. Some match the startup guess but not the class fallback
	// enabled by the same probe: those measurements must remain authoritative.
	for _, tc := range []struct {
		name     string
		clusters []string
		widths   []int
	}{
		{"legacy", []string{"x", "·", "🧑‍🌾", "क्‍ष", "🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f"}, []int{1, 1, 4, 2, 2}},
		{"tmux", []string{"x", "·", "🧑‍🌾", "▶️", "™️", "♥️"}, []int{1, 1, 2, 2, 2, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{})
			t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
			probe := &terminalWidthProbe{row: 20, clusters: tc.clusters, measurements: make(map[string]int)}
			for _, width := range tc.widths {
				probe.accept(CursorPositionMsg{X: width, Y: 19})
			}
			profile, _ := probe.profile()
			ansi.SetTerminalWidthProfile(profile)
			for i, cluster := range tc.clusters {
				if got := ansi.GraphemeWidth.StringWidth(cluster); got != tc.widths[i] {
					t.Errorf("calibrated width of %q = %d, CPR measured %d", cluster, got, tc.widths[i])
				}
				// Exercise the rendered row as well as the width helper: padding
				// must leave the sentinel at the measured terminal column.
				buf := uv.NewScreenBuffer(20, 1)
				buf.Method = ansi.GraphemeWidth
				uv.NewStyledString(cluster+" | sentinel").Draw(buf, uv.Rect(0, 0, 20, 1))
				if got := buf.CellAt(tc.widths[i]+1, 0); got == nil || got.Content != "|" {
					t.Errorf("%q shifted rendered sentinel from CPR column %d: %#v", cluster, tc.widths[i]+1, got)
				}
			}
		})
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
