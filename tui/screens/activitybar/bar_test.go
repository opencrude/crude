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
	if !ok || view != ViewGit {
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
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: (len(views)+1)*RowStride + 2})
	if RowStride%2 == 0 {
		t.Fatalf("RowStride=%d must be odd so the glyph has an exact middle row", RowStride)
	}

	buf := surface.NewCellBuffer(Width, (len(views)+1)*RowStride+2)
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
		if got := buf.Get(outerGutterCol, dy); got.Char != ' ' {
			t.Fatalf("outer gutter row %d is not blank: %q", dy, got.Char)
		}
		if got := buf.Get(markerCol, dy); got.Char != '▌' || !got.FG.Equal(accent) {
			t.Fatalf("active marker must span the whole band; row %d = %q", dy, got.Char)
		}
	}
	if buf.Get(markerCol, RowStride).Char == '▌' {
		t.Fatal("active marker must stop at its own band")
	}
	for dy := 0; dy < RowStride; dy++ {
		if got := buf.Get(panelPadCol, dy); got.Char != ' ' {
			t.Fatalf("panel padding row %d is not blank: %q", dy, got.Char)
		}
		if got := buf.Get(afterIconCol, dy); got.Char != ' ' {
			t.Fatalf("extra hover pad row %d is not blank: %q", dy, got.Char)
		}
		if got := buf.Get(ruleCol, dy); got.Char == ' ' || got.Char == 0 {
			t.Fatalf("panel left border row %d is blank", dy)
		}
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
		if buf.Get(outerGutterCol, y).BG.Equal(hover) {
			t.Fatalf("outer gutter must not join the hover band at row %d", y)
		}
		for x := markerCol; x < ruleCol; x++ {
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
	// Nerd-font glyphs often paint into the next cell, so the extra column
	// after the badge is part of the hover band: visible pads match.
	if !buf.Get(afterIconCol, 2*RowStride+iconRow).BG.Equal(hover) {
		t.Fatal("hover must include the extra column after the icon")
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

func TestAgentsIconIsRobotNotCogs(t *testing.T) {
	if got := ViewAgents.Icon(true); got != "\uf17b" {
		t.Fatalf("nerd-font agents icon=%q want nf-fa-android", got)
	}
	if got := SettingsIcon(true); got != "\uf085" {
		t.Fatalf("nerd-font settings icon=%q want nf-fa-cogs", got)
	}
}

func TestSettingsGearPaintsAtBottom(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	h := len(views)*RowStride + RowStride + 2
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: h})
	buf := surface.NewCellBuffer(Width, h)
	s.Paint(buf, 0, 0, ui.PaintContext{})

	y := settingsBandTop(h) + iconRow
	want := []rune(SettingsIcon(s.nerdFont))[0]
	if got := buf.Get(iconCol, y).Char; got != want {
		t.Fatalf("settings glyph at row %d = %q want %q", y, got, want)
	}
	if buf.Get(markerCol, y).Char == '▌' {
		t.Fatal("settings gear must not paint an active-view marker")
	}
	// Destination icons stay at the top of the rail.
	if got := buf.Get(iconCol, iconRow).Char; got == want {
		t.Fatal("settings gear must not replace the Files icon")
	}
}

func TestClickSettingsEmitsAction(t *testing.T) {
	s := New(theme.DefaultDark())
	h := len(views)*RowStride + RowStride + 4
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: h})
	res := s.Handle(ui.EventContext{LocalY: settingsBandTop(h) + iconRow}, ui.Event{
		Kind:       ui.EventMouseClick,
		MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft},
	})
	if !res.Handled || res.Action == nil || res.Action.ID != ActionSettings {
		t.Fatalf("result=%+v", res)
	}
}

func TestSettingsHoverBandCoversWholeEntry(t *testing.T) {
	th := theme.DefaultDark()
	s := New(th)
	s.SetActive(ViewFiles)
	h := len(views)*RowStride + RowStride + 2
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: h})
	top := settingsBandTop(h)
	s.Handle(ui.EventContext{LocalY: top + RowStride - 1}, ui.Event{Kind: ui.EventMouseMotion})

	buf := surface.NewCellBuffer(Width, h)
	s.Paint(buf, 0, 0, ui.PaintContext{Hovered: true})
	hover := ui.NewTokenSet(th).Color(ui.ColorHover)
	for dy := 0; dy < RowStride; dy++ {
		y := top + dy
		if buf.Get(outerGutterCol, y).BG.Equal(hover) {
			t.Fatalf("outer gutter must not join the settings hover band at row %d", y)
		}
		for x := markerCol; x < ruleCol; x++ {
			if !buf.Get(x, y).BG.Equal(hover) {
				t.Fatalf("settings hover band gap at (%d,%d)", x, y)
			}
		}
		if buf.Get(ruleCol, y).BG.Equal(hover) {
			t.Fatalf("settings hover must stop before the rule at row %d", y)
		}
	}
}

func TestShortRailKeepsSettingsAndOverflow(t *testing.T) {
	s := New(theme.DefaultDark())
	h := len(views) * RowStride
	s.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: Width, H: h})
	if !SettingsAt(h-1, h) {
		t.Fatal("short rail must keep Settings reachable")
	}
	res := s.Handle(ui.EventContext{LocalY: h - 1}, ui.Event{
		Kind:       ui.EventMouseClick,
		MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft},
	})
	if res.Action == nil || res.Action.ID != ActionSettings {
		t.Fatal("bottom band must activate Settings")
	}
	res = s.Handle(ui.EventContext{LocalY: visibleViewCount(h)*RowStride + iconRow}, ui.Event{Kind: ui.EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}})
	if res.Action == nil || res.Action.ID != ActionOverflow {
		t.Fatal("hidden destinations must remain reachable through More")
	}
	for _, view := range Views() {
		if view.Label() == "Terminals" {
			t.Fatal("terminals must not appear in the rail")
		}
	}
}
