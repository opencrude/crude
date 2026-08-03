package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func newTestMenu(items []MenuItem) *Menu {
	m := NewMenu("menu", NewTokenSet(theme.DefaultDark()))
	m.SetItems(items)
	return m
}

func TestMenu_WrapNavigation(t *testing.T) {
	m := newTestMenu([]MenuItem{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	})
	m.Layout(LayoutContext{}, Rect{W: 20, H: 5})
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if m.SelectedIndex() != 2 {
		t.Fatalf("wrap up selected=%d want 2", m.SelectedIndex())
	}
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if m.SelectedIndex() != 0 {
		t.Fatalf("wrap down selected=%d want 0", m.SelectedIndex())
	}
}

func TestMenu_SkipsHeaderSeparatorDisabled(t *testing.T) {
	m := newTestMenu([]MenuItem{
		{Header: true, Label: "Section"},
		{ID: "a", Label: "A"},
		{Disabled: true, Label: "Nope"},
		{Separator: true},
		{ID: "b", Label: "B"},
	})
	if m.SelectedIndex() != 1 {
		t.Fatalf("first selectable=%d want 1", m.SelectedIndex())
	}
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if m.SelectedIndex() != 4 {
		t.Fatalf("down should skip disabled/sep to B (4), got %d", m.SelectedIndex())
	}
	// Click on separator must not activate.
	res := m.Handle(EventContext{LocalY: 3}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if res.Action != nil {
		t.Fatal("separator click must not activate")
	}
}

func TestMenu_PageHomeEnd(t *testing.T) {
	items := make([]MenuItem, 10)
	for i := range items {
		items[i] = MenuItem{ID: string(rune('a' + i%26)), Label: "item"}
	}
	m := newTestMenu(items)
	m.Layout(LayoutContext{}, Rect{W: 20, H: 4})
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyPgDown}))
	if m.SelectedIndex() != 4 {
		t.Fatalf("pgdown selected=%d want 4", m.SelectedIndex())
	}
	if off := m.Scroll().Offset(); off != 1 {
		t.Fatalf("pgdown ensure-visible off=%d want 1", off)
	}
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd}))
	if m.SelectedIndex() != 9 {
		t.Fatalf("end selected=%d want 9", m.SelectedIndex())
	}
	if off := m.Scroll().Offset(); off != 6 {
		t.Fatalf("end ensure-visible off=%d want 6", off)
	}
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyHome}))
	if m.SelectedIndex() != 0 || m.Scroll().Offset() != 0 {
		t.Fatalf("home selected=%d off=%d want 0/0", m.SelectedIndex(), m.Scroll().Offset())
	}
}

func TestMenu_EscDismiss(t *testing.T) {
	m := newTestMenu([]MenuItem{{ID: "a", Label: "A"}})
	res := m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if res.Action == nil || res.Action.ID != "menu.dismiss" {
		t.Fatalf("esc dismiss=%+v", res.Action)
	}
}

func TestMenu_Activate(t *testing.T) {
	m := newTestMenu([]MenuItem{
		{ID: "open", Label: "Open"},
		{ID: "save", Label: "Save", Accel: "Ctrl+S"},
		{ID: "more", Label: "More", Submenu: true},
	})
	m.Layout(LayoutContext{}, Rect{W: 24, H: 5})
	m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	res := m.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	item, ok := res.Action.Payload.(MenuItem)
	if !ok || item.ID != "save" {
		t.Fatalf("activate payload=%+v", res.Action)
	}
	if res.Action.ID != "menu.activate" {
		t.Fatalf("action id=%s", res.Action.ID)
	}
}

func TestMenu_StateDistinction(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	m := newTestMenu([]MenuItem{
		{ID: "a", Label: "Alpha"},
		{ID: "b", Label: "Beta"},
		{ID: "c", Label: "Gamma"},
	})
	m.Layout(LayoutContext{}, Rect{W: 20, H: 5})

	fbuf := surface.NewCellBuffer(20, 5)
	m.Paint(fbuf, 0, 0, PaintContext{Focused: true})
	focusBG := fbuf.Get(2, 0).BG

	ubuf := surface.NewCellBuffer(20, 5)
	m.Paint(ubuf, 0, 0, PaintContext{Focused: false})
	selBG := ubuf.Get(2, 0).BG

	res := m.Handle(EventContext{LocalY: 1}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if res.Action != nil {
		t.Fatal("hover must not activate")
	}
	if m.SelectedIndex() != 0 {
		t.Fatal("hover must not move selection")
	}
	m.Paint(ubuf, 0, 0, PaintContext{Focused: false})
	hoverBG := ubuf.Get(2, 1).BG

	if focusBG.Equal(selBG) || selBG.Equal(hoverBG) || focusBG.Equal(hoverBG) {
		t.Fatalf("states not distinct: focus=%v sel=%v hover=%v", focusBG, selBG, hoverBG)
	}
	if !focusBG.Equal(ts.Color(ColorAccent)) {
		t.Fatal("focused item should use Accent bg")
	}
	if !selBG.Equal(ts.Color(ColorSelection)) {
		t.Fatal("selected (unfocused) item should use Selection bg")
	}
	if !hoverBG.Equal(ts.Color(ColorHover)) {
		t.Fatal("hovered item should use Hover bg")
	}

	act := m.Handle(EventContext{LocalY: 2}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if act.Action == nil || act.Action.ID != "menu.activate" {
		t.Fatal("click must activate")
	}
}

func TestMenu_PaintAccelSubmenu(t *testing.T) {
	m := newTestMenu([]MenuItem{
		{ID: "open", Label: "Open"},
		{ID: "save", Label: "Save", Accel: "Ctrl+S"},
		{ID: "more", Label: "More", Submenu: true},
	})
	m.Layout(LayoutContext{}, Rect{W: 24, H: 5})
	buf := surface.NewCellBuffer(24, 5)
	m.Paint(buf, 0, 0, PaintContext{Focused: true})
	if got := menuRowText(buf, 1, 0, 24); !strings.Contains(got, "Ctrl+S") {
		t.Fatalf("accel row=%q", got)
	}
	// Submenu indicator paints at the right edge in accent.
	sub := buf.Get(23, 2).Char
	if sub != '▸' {
		t.Fatalf("submenu indicator=%q want ▸", sub)
	}
}

func menuRowText(buf *surface.CellBuffer, y, x, w int) string {
	out := make([]rune, 0, w)
	for i := 0; i < w; i++ {
		c := buf.Get(x+i, y)
		if c.Char == 0 {
			break
		}
		out = append(out, c.Char)
	}
	return string(out)
}
