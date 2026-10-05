package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"crude/tui/theme"
)

func TestBlinkOnAlternates(t *testing.T) {
	if !BlinkOn(0) {
		t.Fatal("frame 0 should blink on")
	}
	if BlinkOn(ActivityBlinkPhaseMillis) {
		t.Fatal("frame at half period should blink off")
	}
	if !BlinkOn(ActivityBlinkCycleMillis) {
		t.Fatal("frame at full period should blink on again")
	}
}

func TestBlockCursorSolidWidth(t *testing.T) {
	th := theme.DefaultDark()
	solid := BlockCursorSolid(th.Colors.Accent)
	if strings.TrimSpace(stripAnsiSpinnerTest(solid)) != "█" {
		t.Fatalf("solid cursor = %q", stripAnsiSpinnerTest(solid))
	}
	off := BlockCursor(ActivityBlinkPhaseMillis, th.Colors.Accent)
	if off != " " {
		t.Fatalf("off-phase cursor = %q, want space", off)
	}
}

func TestActivityDotUsesCanonicalFullEmptyCycle(t *testing.T) {
	on := ActivityDot(0)
	off := ActivityDot(ActivityBlinkPhaseMillis)
	if on != "●" || off != "○" {
		t.Fatalf("phases = %q / %q, want ● / ○", on, off)
	}
	if again := ActivityDot(ActivityBlinkCycleMillis); again != "●" {
		t.Fatalf("next cycle = %q, want ●", again)
	}
}

func TestActivityDotKeepsStableMeasuredFootprint(t *testing.T) {
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"●": 1, "○": 2}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	filled := ActivityDot(0)
	hollow := ActivityDot(ActivityBlinkPhaseMillis)
	if ansi.GraphemeWidth.StringWidth(filled) != 2 || ansi.GraphemeWidth.StringWidth(hollow) != 2 {
		t.Fatalf("activity footprints differ: %q=%d %q=%d",
			filled, ansi.GraphemeWidth.StringWidth(filled),
			hollow, ansi.GraphemeWidth.StringWidth(hollow),
		)
	}
}

func TestBlinkOnTimeMatchesFrame(t *testing.T) {
	t0 := time.UnixMilli(0)
	if BlinkOnTime(t0) != BlinkOn(0) {
		t.Fatal("BlinkOnTime at 0ms should match BlinkOn(0)")
	}
	tHalf := time.UnixMilli(ActivityBlinkPhaseMillis)
	if BlinkOnTime(tHalf) != BlinkOn(ActivityBlinkPhaseMillis) {
		t.Fatal("BlinkOnTime at half period should match the activity-dot phase")
	}
}

func TestRecoverySquareUsesSharedClockAndStableMeasuredWidth(t *testing.T) {
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"■": 2}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	on, off := RecoverySquare(0), RecoverySquare(ActivityBlinkPhaseMillis)
	if on != "■" || strings.TrimSpace(off) != "" ||
		ansi.GraphemeWidth.StringWidth(on) != ansi.GraphemeWidth.StringWidth(off) {
		t.Fatalf("recovery square moved its label or ignored phase: %q / %q", on, off)
	}
}

func stripAnsiSpinnerTest(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
