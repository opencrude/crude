package termdock

import (
	"strings"
	"testing"

	"crude/tui/screens/shellterm"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestDockUsesSingleDividerChromeAndVerticalTerminalRail(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	first := shellterm.New(th, "one", "one")
	second := shellterm.New(th, "two", "two")
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	dock.Mount("one", first)
	dock.Mount("two", second)

	host := ui.NewHost(th, dock)
	host.Resize(80, 12)
	host.Layout()
	buf := surface.NewCellBuffer(80, 12)
	host.Paint(buf)

	if got := second.Bounds().Y; got != dividerH+1 {
		t.Fatalf("terminal body starts at y=%d want %d", got, dividerH+1)
	}
	if MinRenderableHeight != dividerH+1+workspace.MinTerminalRows {
		t.Fatalf("MinRenderableHeight=%d want divider+actions+PTY rows", MinRenderableHeight)
	}
	if dock.rail.Bounds().W == 0 {
		t.Fatal("multiple terminals did not create a vertical rail")
	}
	painted := readCellsRange(buf, dock.rail.Bounds().Y, dock.rail.Bounds().X, dock.rail.Bounds().W)
	if !strings.Contains(painted, "one") {
		t.Fatalf("terminal rail label missing: %q", painted)
	}
}

func readCellsRange(buf *surface.CellBuffer, y, start, w int) string {
	var b strings.Builder
	for x := start; x < start+w; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
