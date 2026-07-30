package shellheader

import (
	"strings"
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestPlainLinePlacesUsageBeforeClock(t *testing.T) {
	th := theme.DefaultDark()
	plain := PlainLine(Projection{
		ModelName: "openrouter/model",
		StartTime: time.Now().Add(-time.Hour),
		Usage:     "ready",
		Width:     90,
	})
	usageAt := strings.Index(plain, "ready")
	clockAt := strings.LastIndex(plain, "1h")
	if usageAt < 0 || clockAt < 0 || usageAt >= clockAt {
		t.Fatalf("expected usage before elapsed clock, got %q", plain)
	}

	narrow := PlainLine(Projection{
		ModelName: strings.Repeat("model", 20),
		YOLO:      true,
		StartTime: time.Now().Add(-time.Hour),
		Usage:     strings.Repeat("usage ", 20),
		ViewLabel: strings.Repeat("agent", 20),
		Width:     50,
	})
	if got, maxWidth := rendering.VisibleWidth(narrow), 50-th.Spacing.PadLR*2; got > maxWidth {
		t.Fatalf("header overflow: width=%d max=%d line=%q", got, maxWidth, narrow)
	}
}

func TestPlainLineOmitsEmptyViewLabelParenthetical(t *testing.T) {
	withLabel := PlainLine(Projection{
		ModelName: "model",
		StartTime: time.Now(),
		ViewLabel: "Main Agent",
		Width:     80,
	})
	if !strings.Contains(withLabel, "⟨Main Agent⟩") {
		t.Fatalf("expected parenthetical label, got %q", withLabel)
	}
	without := PlainLine(Projection{
		ModelName: "model",
		StartTime: time.Now(),
		Width:     80,
	})
	if strings.Contains(without, "⟨") || strings.Contains(without, "⟩") {
		t.Fatalf("empty ViewLabel must omit parenthetical, got %q", without)
	}
}

func TestPaintGoldenCellBufferContract(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetProjection(Projection{
		ModelName: "openrouter/model",
		StartTime: time.Now(),
		Usage:     "ready",
		Width:     60,
	})
	host := ui.NewHost(th, s)
	host.Resize(60, 2)
	host.Layout()
	buf := surface.NewCellBuffer(60, 2)
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("shell header paint produced no non-empty cells")
	}
	line0 := readRow(buf, 0, 60)
	for _, label := range []string{"crude", "ready", "openrouter"} {
		if !strings.Contains(line0, label) {
			t.Fatalf("missing header label %q in row 0: %q", label, line0)
		}
	}
	line1 := readRow(buf, 1, 60)
	if !strings.Contains(line1, "─") {
		t.Fatalf("expected separator in row 1, got %q", line1)
	}
	wantAccent := ui.NewTokenSet(th).Color(ui.ColorAccent)
	if !bufferHasFG(buf, wantAccent) {
		t.Fatal("shell header paint missing theme accent foreground")
	}
}

func countNonEmptyCells(buf *surface.CellBuffer) int {
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Char != 0 && c.Width != 0 {
				n++
			}
		}
	}
	return n
}

func bufferHasFG(buf *surface.CellBuffer, want cell.Color) bool {
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if fg := buf.Get(x, y).FG; !fg.IsDefault() && fg.Equal(want) {
				return true
			}
		}
	}
	return false
}

func TestScreenPaintsBrandAndSep(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetProjection(Projection{
		ModelName: "m",
		StartTime: time.Now(),
		Usage:     "ready",
		Width:     40,
	})
	host := ui.NewHost(th, s)
	host.Resize(40, 2)
	host.Layout()
	buf := surface.NewCellBuffer(40, 2)
	host.Paint(buf)

	line0 := readRow(buf, 0, 40)
	if !strings.Contains(line0, "crude") {
		t.Fatalf("expected brand in row 0, got %q", line0)
	}
	if !strings.Contains(line0, "ready") {
		t.Fatalf("expected usage in row 0, got %q", line0)
	}
	line1 := readRow(buf, 1, 40)
	if !strings.Contains(line1, "─") {
		t.Fatalf("expected separator in row 1, got %q", line1)
	}
}

func readRow(buf *surface.CellBuffer, y, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		}
	}
	return b.String()
}
