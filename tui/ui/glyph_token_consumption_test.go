package ui

import (
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
)

// themeWithGlyph returns a dark theme with one glyph token overridden.
func themeWithGlyph(apply func(*theme.Glyphs)) *theme.Theme {
	th := theme.DefaultDark()
	apply(&th.Glyphs)
	return th
}

func TestPanelDeckDividerConsumesBorderHGlyph(t *testing.T) {
	th := themeWithGlyph(func(g *theme.Glyphs) { g.BorderH = "=" })
	deck := NewPanelDeck("deck", NewTokenSet(th))
	deck.Open(PanelEntry{ID: "p", Title: "P", Content: NewLabel("deck/p", "P", th)})

	host := NewHost(th, deck)
	host.Resize(20, 4)
	host.Layout()
	buf := surface.NewCellBuffer(20, 4)
	host.Paint(buf)
	if got := buf.Get(0, 0).Char; got != '=' {
		t.Fatalf("deck divider char=%q want '=' (BorderH token)", string(got))
	}
}

func TestTextAreaHasNoDefaultPromptGlyph(t *testing.T) {
	th := themeWithGlyph(func(g *theme.Glyphs) { g.Prompt = ">" })
	ta := NewTextArea("test/area", NewTokenSet(th), true)
	ta.SetValue("hello")
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 3})
	buf := surface.NewCellBuffer(20, 3)
	ta.Paint(buf, 0, 0, PaintContext{Theme: th, Focused: true})
	row := rowText(buf, 0)
	if strings.HasPrefix(strings.TrimLeft(row, " "), ">") || strings.Contains(row, "❯") {
		t.Fatalf("row 0=%q must not consume Glyphs.Prompt", row)
	}
	if !strings.Contains(row, "hello") {
		t.Fatalf("row 0=%q missing hello", row)
	}
}

func TestTreeChevronConsumesTreeCollapsedExpandedGlyphs(t *testing.T) {
	th := themeWithGlyph(func(g *theme.Glyphs) {
		g.TreeCollapsed = "x"
		g.TreeExpanded = "X"
	})
	tree := NewTree("tree", NewTokenSet(th))
	tree.SetRows([]TreeRow{
		{ID: "/d", Label: "dir", Depth: 0, IsDir: true, Expanded: false},
		{ID: "/D", Label: "open", Depth: 0, IsDir: true, Expanded: true},
	})
	tree.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 5})
	buf := surface.NewCellBuffer(40, 5)
	tree.Paint(buf, 0, 0, PaintContext{Focused: true})
	row0 := rowText(buf, 0)
	row1 := rowText(buf, 1)
	if !strings.Contains(row0, "x") {
		t.Fatalf("collapsed row=%q missing 'x' (TreeCollapsed token)", row0)
	}
	if !strings.Contains(row1, "X") {
		t.Fatalf("expanded row=%q missing 'X' (TreeExpanded token)", row1)
	}
}

func TestScrollbarControlMeasureConsumesScrollbarWidthMetric(t *testing.T) {
	th := theme.DefaultDark()
	th.Metrics.ScrollbarWidth = 2
	tokens := NewTokenSet(th)
	sb := NewScrollbarControl("sb", NewScrollModel(), tokens)
	sz := sb.Measure(LayoutContext{}, Loose(10, 10))
	if sz.W != 2 {
		t.Fatalf("scrollbar width=%d want 2 (Metrics.ScrollbarWidth token)", sz.W)
	}
}

func TestMenuConsumesBorderPromptTreeGlyphs(t *testing.T) {
	th := themeWithGlyph(func(g *theme.Glyphs) {
		g.BorderH = "-"
		g.TreeCollapsed = "»"
	})
	m := NewMenu("menu", NewTokenSet(th))
	m.SetItems([]MenuItem{
		{ID: "sep", Label: "—", Separator: true},
		{ID: "one", Label: "One"},
		{ID: "sub", Label: "Sub", Submenu: true},
	})
	m.SelectIndex(1)
	m.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 6})
	buf := surface.NewCellBuffer(20, 6)
	m.Paint(buf, 0, 0, PaintContext{Focused: true})
	if got := buf.Get(0, 0).Char; got != '-' {
		t.Fatalf("menu separator row char=%q want '-' (BorderH token)", string(got))
	}
	row := rowText(buf, 1)
	if strings.Contains(row, ">") || strings.Contains(row, "❯") {
		t.Fatalf("selected menu row=%q must not paint a prompt glyph", row)
	}
	if !strings.Contains(row, "One") {
		t.Fatalf("selected menu row=%q missing label", row)
	}
	subRow := rowText(buf, 2)
	if !strings.Contains(subRow, "»") {
		t.Fatalf("submenu row=%q missing '»' (TreeCollapsed token)", subRow)
	}
}
