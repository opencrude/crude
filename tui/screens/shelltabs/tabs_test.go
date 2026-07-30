package shelltabs

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetProjection([]TabProjection{
		{ID: "1", Label: "Editor", Icon: "📄", Closable: true},
		{ID: "2", Label: "Chat", Icon: "💬", Closable: true},
	}, "1", -1, true)

	host := ui.NewHost(th, screen)
	host.Resize(40, 2)
	host.Layout()
	buf := surface.NewCellBuffer(40, 2)
	host.Paint(buf)

	if nonEmpty := countNonEmptyCells(buf); nonEmpty == 0 {
		t.Fatal("shell tabs paint produced no non-empty cells")
	}
	row0 := readRow(buf, 0, 40)
	for _, label := range []string{"Editor", "Chat"} {
		if !strings.Contains(row0, label) {
			t.Fatalf("missing tab label %q in row 0: %q", label, row0)
		}
	}
	for _, icon := range []string{"📄", "💬"} {
		if strings.Contains(row0, icon) {
			t.Fatalf("shell tabs must omit icons, found %q in %q", icon, row0)
		}
	}
	if !strings.Contains(row0, "❯") {
		t.Fatalf("expected active marker in row 0: %q", row0)
	}
	row1 := readRow(buf, 1, 40)
	if !strings.Contains(row1, "━") {
		t.Fatalf("expected focused separator in row 1, got %q", row1)
	}

	wantAccent := ui.NewTokenSet(th).Color(ui.ColorAccent)
	if !bufferHasFG(buf, wantAccent) {
		t.Fatal("shell tabs paint missing theme accent foreground")
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

func TestSetProjectionFromWindow(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	w := window.New(th)
	w.SetWidth(60)
	w.OpenTab("agent:main", "●", true)
	tab := w.Tabs()[0]
	w.SetTabDisplayTitleByIndex(0, "Main Agent")
	w.SetTabIconByIndex(0, "⚡")

	screen.SetProjectionFromWindow(w, 0, true)
	if got := screen.TabsControl().ActiveID(); got != TabIDString(tab.ID) {
		t.Fatalf("active id = %q want %q", got, TabIDString(tab.ID))
	}
}

func TestScreenPaintsTabsAndSeparator(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetProjection([]TabProjection{
		{ID: "1", Label: "Editor", Icon: "📄", Closable: true},
	}, "1", -1, true)

	host := ui.NewHost(th, screen)
	host.Resize(30, 2)
	host.Layout()
	buf := surface.NewCellBuffer(30, 2)
	host.Paint(buf)

	row0 := readRow(buf, 0, 30)
	if strings.Contains(row0, "📄") {
		t.Fatalf("shell tabs must omit icons, got %q", row0)
	}
	if !strings.Contains(row0, "Editor") || !strings.Contains(row0, "❯") {
		t.Fatalf("expected tab content in row 0, got %q", row0)
	}
	row1 := readRow(buf, 1, 30)
	if !strings.Contains(row1, "━") {
		t.Fatalf("expected focused separator in row 1, got %q", row1)
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
