package activitybar

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestHitIndexGlyphRows(t *testing.T) {
	// Every row of an entry's band selects that entry; a one-row target is
	// too small for a mouse.
	for i, want := range views {
		for dy := 0; dy < RowStride; dy++ {
			localY := i*RowStride + dy
			if got := hitIndex(localY); got != i {
				t.Fatalf("localY=%d hit=%d want %d (%s)", localY, got, i, want.Label())
			}
		}
	}
	if hitIndex(len(views)*RowStride) != -1 {
		t.Fatal("rows below the activity targets are not hits")
	}
	if hitIndex(-1) != -1 {
		t.Fatal("negative rows are not hits")
	}
}

func TestClickEmitsSelect(t *testing.T) {
	s := New(theme.DefaultDark())
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: 20})
	res := s.Handle(ui.EventContext{LocalY: 2 * RowStride}, ui.Event{
		Kind:       ui.EventMouseClick,
		MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft},
	})
	if !res.Handled || res.Action == nil || res.Action.ID != ActionSelect {
		t.Fatalf("result=%+v", res)
	}
	view, ok := res.Action.Payload.(View)
	if !ok || view != ViewTerminals {
		t.Fatalf("payload=%v", res.Action.Payload)
	}
}

// Every entry must occupy an identical, balanced block: the glyph on the
// band's middle row, and hover/active highlights covering the whole band.
// A one-row marker beside a two-row hover box reads as misalignment.
func TestEntriesArePaintedAsUniformBands(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetActive(ViewFiles)
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: len(views)*RowStride + 2})
	if RowStride%2 == 0 {
		t.Fatalf("RowStride=%d must be odd so the glyph has an exact middle row", RowStride)
	}

	buf := surface.NewCellBuffer(Width, len(views)*RowStride+2)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	for i, v := range views {
		y := i*RowStride + iconRow
		want := []rune(v.Icon(s.nerdFont))[0]
		if got := buf.Get(iconCol, y).Char; got != want {
			t.Fatalf("%s glyph at row %d = %q want %q", v.Label(), y, got, want)
		}
	}

	accent := ui.NewTokenSet(th).Color(ui.ColorAccent)
	for dy := 0; dy < RowStride; dy++ {
		if got := buf.Get(markerCol, dy); got.Char != '▌' || !got.FG.Equal(accent) {
			t.Fatalf("active marker must span the whole band; row %d = %q", dy, got.Char)
		}
	}
	if buf.Get(markerCol, RowStride).Char == '▌' {
		t.Fatal("active marker must stop at its own band")
	}
}

func TestHoverBandCoversWholeEntry(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetActive(ViewFiles)
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: len(views)*RowStride + 2})
	// Hover the Terminals entry on its last row.
	s.Handle(ui.EventContext{LocalY: 2*RowStride + RowStride - 1}, ui.Event{Kind: ui.EventMouseMotion})

	buf := surface.NewCellBuffer(Width, len(views)*RowStride+2)
	s.Paint(buf, 0, 0, ui.PaintContext{Hovered: true})
	hover := ui.NewTokenSet(th).Color(ui.ColorHover)
	for dy := 0; dy < RowStride; dy++ {
		y := 2*RowStride + dy
		for x := 0; x < ruleCol; x++ {
			if !buf.Get(x, y).BG.Equal(hover) {
				t.Fatalf("hover band gap at (%d,%d)", x, y)
			}
		}
		if buf.Get(ruleCol, y).BG.Equal(hover) {
			t.Fatalf("hover must stop before the rule at row %d", y)
		}
	}
	if buf.Get(iconCol, 2*RowStride-1).BG.Equal(hover) {
		t.Fatal("hover must not bleed into the entry above")
	}
}

// A collapsed panel has to look collapsed: keeping the accent marker lit
// claims a panel is open when the rail is the only thing on screen.
func TestActiveMarkerTracksPanelVisibility(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetActive(ViewFiles)
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: len(views) * RowStride})

	s.SetPanelOpen(false)
	buf := surface.NewCellBuffer(Width, len(views)*RowStride)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(markerCol, iconRow).Char == '▌' {
		t.Fatal("collapsed sidebar must not paint an active marker")
	}
	if buf.Get(iconCol, iconRow).Char == 0 {
		t.Fatal("the current view's glyph must stay visible while collapsed")
	}

	s.SetPanelOpen(true)
	buf = surface.NewCellBuffer(Width, len(views)*RowStride)
	s.Paint(buf, 0, 0, ui.PaintContext{})
	if buf.Get(markerCol, iconRow).Char != '▌' {
		t.Fatal("an open panel must paint its active marker")
	}
}

func TestHoverPaintsOnlyWhileHostHoversRail(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: 20})
	res := s.Handle(ui.EventContext{LocalY: 3}, ui.Event{Kind: ui.EventMouseMotion})
	if !res.Handled {
		t.Fatal("motion over a glyph row must be handled")
	}
	hover := ui.NewTokenSet(th).Color(ui.ColorHover)

	buf := surface.NewCellBuffer(Width, 20)
	s.Paint(buf, 0, 0, ui.PaintContext{Hovered: true})
	if !buf.Get(1, 3).BG.Equal(hover) {
		t.Fatal("hovered rail row should paint hover bg while host hovers the rail")
	}

	buf = surface.NewCellBuffer(Width, 20)
	s.Paint(buf, 0, 0, ui.PaintContext{Hovered: false})
	if buf.Get(1, 3).BG.Equal(hover) {
		t.Fatal("stale hover must not paint after the pointer left the rail")
	}
}
