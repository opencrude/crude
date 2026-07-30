package ui

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
)

func TestBlinkOnAlternates(t *testing.T) {
	if !BlinkOn(0) {
		t.Fatal("frame 0 should blink on")
	}
	if BlinkOn(BlinkHalf) {
		t.Fatal("frame at half period should blink off")
	}
	if !BlinkOn(BlinkPeriod) {
		t.Fatal("frame at full period should blink on again")
	}
}

func TestThinkingDotIndexCycles(t *testing.T) {
	seen := make(map[int]bool)
	for f := 0; f < DotCycleStep*4; f += DotCycleStep {
		seen[ThinkingDotIndex(f)] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 dot indices, got %d", len(seen))
	}
}

func TestBlockCursorSolidWidth(t *testing.T) {
	th := theme.DefaultDark()
	solid := BlockCursorSolid(th.Colors.Accent)
	if strings.TrimSpace(stripAnsiSpinnerTest(solid)) != "█" {
		t.Fatalf("solid cursor = %q", stripAnsiSpinnerTest(solid))
	}
	off := BlockCursor(BlinkHalf, th.Colors.Accent)
	if off != " " {
		t.Fatalf("off-phase cursor = %q, want space", off)
	}
}

func TestSpinnerPhaseGlyphs(t *testing.T) {
	on := SpinnerPhase(0)
	off := SpinnerPhase(BlinkHalf)
	if on != "●" || off != "○" {
		t.Fatalf("phases = %q / %q, want ● / ○", on, off)
	}
}

func TestHeartbeatDotPlainStates(t *testing.T) {
	g, b, f := HeartbeatDotPlain(0, "ready")
	if g != "●" || b || f {
		t.Fatalf("ready = %q bold=%v faint=%v", g, b, f)
	}
	g, _, _ = HeartbeatDotPlain(0, "failed")
	if g != "✕" {
		t.Fatalf("failed glyph = %q", g)
	}
}

func TestBlinkOnTimeMatchesFrame(t *testing.T) {
	t0 := time.UnixMilli(0)
	if BlinkOnTime(t0) != BlinkOn(0) {
		t.Fatal("BlinkOnTime at 0ms should match BlinkOn(0)")
	}
	tHalf := time.UnixMilli(BlinkHalf)
	if BlinkOnTime(tHalf) != BlinkOn(BlinkHalf) {
		t.Fatal("BlinkOnTime at half period should match BlinkOn(BlinkHalf)")
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
