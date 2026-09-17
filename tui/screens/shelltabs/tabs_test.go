package shelltabs

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/uitest"
	"crude/tui/window"
)

func TestPaintGoldenCellBufferContract(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.SetProjection([]TabProjection{
		{ID: "1", Label: "Editor", Closable: true},
		{ID: "2", Label: "Chat", Closable: true},
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
	// Tab bars paint labels only: the projection carries no icon at all, so a
	// glyph can never leak into the strip (tofu risk).
	if strings.ContainsAny(row0, "❯") {
		t.Fatalf("shell tabs must not paint a prompt glyph: %q", row0)
	}
	row1 := readRow(buf, 1, 40)
	if !strings.Contains(row1, "━") {
		t.Fatalf("expected focused separator in row 1, got %q", row1)
	}

	wantAccent := ui.NewTokenSet(th).Color(ui.ColorAccent)
	if !bufferHasFG(buf, wantAccent) {
		t.Fatal("shell tabs paint missing theme accent foreground")
	}

	// Byte-identical regression: catches a shifted active marker or wrong
	// separator glyph that property checks cannot see.
	uitest.CheckGolden(t, "shelltabs_dark_40x2", buf)
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
		{ID: "1", Label: "Editor", Closable: true},
	}, "1", -1, true)

	host := ui.NewHost(th, screen)
	host.Resize(30, 2)
	host.Layout()
	buf := surface.NewCellBuffer(30, 2)
	host.Paint(buf)

	row0 := readRow(buf, 0, 30)
	if !strings.Contains(row0, "Editor") || strings.Contains(row0, "❯") {
		t.Fatalf("expected tab content without prompt glyph in row 0, got %q", row0)
	}
	row1 := readRow(buf, 1, 30)
	if !strings.Contains(row1, "━") {
		t.Fatalf("expected focused separator in row 1, got %q", row1)
	}
}

func TestMainBarInsetsLabelContentNotItsUnderline(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	screen := New(th)
	screen.SetProjection([]TabProjection{
		{ID: "1", Label: "Editor", Closable: true},
		{ID: "2", Label: "Chat", Closable: true},
	}, "1", -1, false)

	// The slot the shell hands over: the whole content column.
	slot := ui.Rect{X: 0, Y: 0, W: 40, H: ui.StripHeight}
	screen.Layout(ui.LayoutContext{}, slot)

	// The strip spans the whole row and the first tab's cell reaches its left
	// edge; only the label content is inset.
	if got := screen.tabs.Bounds(); got != slot {
		t.Fatalf("strip bounds=%+v want the full row %+v", got, slot)
	}
	if got := screen.tabs.LabelInset(); got != RailInset {
		t.Fatalf("label inset=%d want %d", got, RailInset)
	}

	host := ui.NewHost(th, screen)
	host.Resize(40, ui.StripHeight)
	host.Layout()
	buf := surface.NewCellBuffer(40, ui.StripHeight)
	host.Paint(buf)

	// Content: the inset column is empty and the label starts right after it.
	for x := 0; x < RailInset; x++ {
		if c := buf.Get(x, 0); c.Char != 0 && c.Char != ' ' {
			t.Fatalf("label row carries ink at x=%d: %q", x, c.Char)
		}
	}
	if got := buf.Get(RailInset, 0).Char; got != 'E' {
		t.Fatalf("first label char=%q want 'E' at x=%d", got, RailInset)
	}

	// Underline: it starts from the bar's start, not from the label inset, and
	// runs through the active tab's cell.
	accent := tokens.Color(ui.ColorAccent)
	if got := buf.Get(0, 1).Char; got != '─' && got != '━' {
		t.Fatalf("underline must start at the bar's left edge, got %q at x=0", got)
	}
	if got := buf.Get(0, 1).FG; !got.Equal(accent) {
		t.Fatal("underline must start at the bar's left edge in accent ink")
	}
	// Cell end of the active (first) tab: its close glyph plus " ×".
	closeX := screen.tabs.CloseLocalX(0)
	if closeX < 0 {
		t.Fatal("no close geometry for the active tab")
	}
	cellEnd := closeX + 2
	if got := buf.Get(cellEnd, 1).FG; got.Equal(accent) {
		t.Fatalf("underline bleeds past the active tab cell at x=%d", cellEnd)
	}
	if got := buf.Get(cellEnd, 1).Char; got != '─' && got != '━' {
		t.Fatalf("the rule must continue past the active tab, got %q at x=%d", got, cellEnd)
	}
	if underline := readRow(buf, 1, cellEnd); strings.Trim(underline, "─━") != "" {
		t.Fatalf("underline row must be plain indicator ink, got %q", underline)
	}
}

func TestRailInsetIsMainBarOnly(t *testing.T) {
	// The dock, deck, and mode strips span their own rows and must stay flush:
	// a bar-local inset would misalign them against their own chrome.
	th := theme.DefaultDark()
	tabs := ui.NewTabs("other/tabs", ui.NewTokenSet(th))
	tabs.SetItems([]ui.TabItem{{ID: "a", Label: "Alpha"}}, "a")
	tabs.Layout(ui.LayoutContext{}, ui.Rect{W: 12, H: ui.StripHeight})
	if got := tabs.Bounds(); got.X != 0 {
		t.Fatalf("shared strip must not carry the main bar's rail inset, got X=%d", got.X)
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
