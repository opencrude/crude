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

// TestDockStripOwnsLabelsAndFullWidthBaseline pins the standardized tab strip
// inside the dock: the strip reserves its own baseline row, that baseline runs
// the full dock width (rule bleed), and the terminal body starts below the
// chrome instead of losing rows to the terminal.
func TestDockStripOwnsLabelsAndFullWidthBaseline(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	screen := shellterm.New(th, "strip", "strip")
	t.Cleanup(screen.Close)
	dock.Mount("strip", screen)
	dock.SetActive("strip")

	host := ui.NewHost(th, dock)
	host.Resize(40, 12)
	host.Layout()
	buf := surface.NewCellBuffer(40, 12)
	host.Paint(buf)

	if got := dock.tabs.Bounds(); got.H != ui.StripHeight || got.Y != dividerH {
		t.Fatalf("dock strip bounds=%+v want y=%d h=%d", got, dividerH, ui.StripHeight)
	}
	if got := chromeH; got != dividerH+ui.StripHeight {
		t.Fatalf("dock chrome rows=%d want %d", got, dividerH+ui.StripHeight)
	}
	if got := screen.Bounds().Y; got != chromeH {
		t.Fatalf("terminal body starts at y=%d want %d", got, chromeH)
	}
	if MinRenderableHeight != chromeH+workspace.MinTerminalRows {
		t.Fatalf("MinRenderableHeight=%d does not include the strip", MinRenderableHeight)
	}

	labelRow := strings.TrimRight(readCells(buf, dividerH, 40), " ")
	if !strings.Contains(labelRow, "strip") {
		t.Fatalf("dock tab label missing: %q", labelRow)
	}
	baseline := readCells(buf, dividerH+1, 40)
	if strings.Trim(baseline, "─") != "" {
		t.Fatalf("dock baseline must span the whole dock row: %q", baseline)
	}
}

func readCells(buf *surface.CellBuffer, y, w int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := buf.Get(x, y)
		if c.Char != 0 && c.Width != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
