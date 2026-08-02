package shellstatus

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetWidth(80)
	s.SetThinkingForAgent("orch_1", false, true, false)
	s.SetCurrentAgent("orch_1")
	s.SetProjectInsights(editor.ProjectInsights{
		ServerName:  "gopls",
		ServerReady: true,
		Errors:      1,
		Warnings:    2,
	})

	host := ui.NewHost(th, s)
	host.Resize(80, 2)
	host.Layout()
	buf := surface.NewCellBuffer(80, 2)
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("shell status paint produced no non-empty cells")
	}
	plain := bufferPlain(buf)
	for _, label := range []string{"⚡1", "✕1", "⚠2", "gopls"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("missing status label %q:\n%s", label, plain)
		}
	}
	row0 := readRow(buf, 0, 80)
	if !strings.Contains(row0, "─") {
		t.Fatalf("expected top border in row 0, got %q", row0)
	}
	wantBorder := ui.NewTokenSet(th).Color(ui.ColorBorder)
	if !bufferHasFG(buf, wantBorder) {
		t.Fatal("shell status paint missing theme border foreground")
	}

	// Byte-identical regression. Deterministic here: no error/retry/info
	// messages, no LastBusyAt, fixed spinFrame=0 — the heartbeat dot is the
	// static "ready" glyph.
	uitest.CheckGolden(t, "shellstatus_dark_80x2", buf)
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

func bufferPlain(buf *surface.CellBuffer) string {
	var rows []string
	for y := 0; y < buf.Height(); y++ {
		rows = append(rows, readRow(buf, y, buf.Width()))
	}
	return strings.Join(rows, "\n")
}

func readRow(buf *surface.CellBuffer, y, w int) string {
	var b strings.Builder
	for x := 0; x < w && x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		}
	}
	return b.String()
}
