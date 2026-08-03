package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestSelectionModel_ClampWrap(t *testing.T) {
	s := NewSelectionModel(false)
	s.SetCount(3)
	s.SetIndex(0)
	s.MoveUp()
	if s.Index() != 0 {
		t.Fatal("clamp up")
	}
	s.MoveDown()
	s.MoveDown()
	s.MoveDown()
	if s.Index() != 2 {
		t.Fatal("clamp down")
	}
	w := NewSelectionModel(true)
	w.SetCount(3)
	w.SetIndex(0)
	w.MoveUp()
	if w.Index() != 2 {
		t.Fatal("wrap up")
	}
	w.MoveDown()
	if w.Index() != 0 {
		t.Fatal("wrap down")
	}
}

func TestScrollModel_EnsureVisible(t *testing.T) {
	s := NewScrollModel()
	s.SetContent(20)
	s.SetViewport(5)
	s.EnsureVisible(10)
	if s.Offset() != 6 { // 10 - 5 + 1
		t.Fatalf("off=%d", s.Offset())
	}
	s.EnsureVisible(0)
	if s.Offset() != 0 {
		t.Fatal(s.Offset())
	}
	top, h := s.ThumbBounds(10)
	if h < 1 {
		t.Fatal(top, h)
	}
}

func TestList_KeyboardActivate(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)
	l := NewList("list", ts)
	l.SetItems([]ListItem{
		{ID: "1", Label: "Alpha"},
		{ID: "2", Label: "Beta"},
		{ID: "3", Label: "Gamma"},
	})
	l.Layout(LayoutContext{Theme: th}, Rect{W: 30, H: 5})
	l.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if l.SelectedIndex() != 1 {
		t.Fatal(l.SelectedIndex())
	}
	res := l.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	item, ok := res.Action.Payload.(ListItem)
	if !ok || item.Label != "Beta" {
		t.Fatalf("%+v", res.Action)
	}
}

func TestList_SkipsHeaderAndDisabled(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	l := NewList("menu", ts)
	l.SetWrap(true)
	l.SetItems([]ListItem{
		{Header: true, Label: "Section"},
		{Label: "A"},
		{Disabled: true, Label: "Nope"},
		{Separator: true},
		{Label: "B"},
	})
	// First selectable is A (index 1)
	if l.SelectedIndex() != 1 {
		t.Fatalf("want first selectable 1, got %d", l.SelectedIndex())
	}
	l.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if l.SelectedIndex() != 4 {
		t.Fatalf("down should skip disabled/sep to B (4), got %d", l.SelectedIndex())
	}
	l.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	// wrap to A
	if l.SelectedIndex() != 1 {
		t.Fatalf("wrap to A, got %d", l.SelectedIndex())
	}
	// click disabled does not activate
	l.SelectIndex(2)
	res := l.Handle(EventContext{LocalY: 2, Focused: true}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if res.Action != nil {
		t.Fatal("disabled click must not activate")
	}
}

func TestList_MouseAndWheel(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	l := NewList("list", ts)
	items := make([]ListItem, 20)
	for i := range items {
		items[i] = ListItem{ID: string(rune('a' + i%26)), Label: "item"}
	}
	l.SetItems(items)
	l.Layout(LayoutContext{}, Rect{W: 20, H: 5})
	l.Handle(EventContext{LocalY: 2}, MouseClickEvent(tea.MouseClickMsg{X: 0, Y: 2, Button: tea.MouseLeft}))
	if l.SelectedIndex() != 2 {
		t.Fatal(l.SelectedIndex())
	}
	off := l.Scroll().Offset()
	l.Handle(EventContext{}, MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelDown}))
	if l.Scroll().Offset() < off {
		t.Fatal("wheel should increase offset")
	}
}

func TestList_Paint(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	l := NewList("list", ts)
	l.SetItems([]ListItem{{Label: "Hello"}})
	l.Layout(LayoutContext{}, Rect{W: 20, H: 3})
	buf := surface.NewCellBuffer(20, 3)
	l.Paint(buf, 0, 0, PaintContext{Focused: true})
	// marker ❯ is multi-byte; look for 'H'
	found := false
	for x := 0; x < 20; x++ {
		if buf.Get(x, 0).Char == 'H' {
			found = true
		}
	}
	if !found {
		t.Fatal("expected Hello painted")
	}
}

func TestTokenSet_ThemeSwitchBumpsVersion(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)
	v1 := ts.Version
	ts.Rebuild(th)
	if ts.Version <= v1 {
		t.Fatal("version should bump")
	}
	if ts.Color(ColorAccent).IsDefault() {
		// accent should resolve from dark theme
		// might still be truecolor non-default
	}
	fg, bg := ts.ButtonColors(StateFocused)
	if fg.IsDefault() && bg.IsDefault() {
		t.Fatal("button colors")
	}
}

func TestResolveControlState_Precedence(t *testing.T) {
	if ResolveControlState(true, true, true, true, true) != StateDisabled {
		t.Fatal("disabled wins")
	}
	if ResolveControlState(false, true, true, true, true) != StateFocused {
		t.Fatal("focused over selected")
	}
	if ResolveControlState(false, false, true, true, true) != StateSelected {
		t.Fatal("selected over pressed")
	}
}

func TestListRowColors_FocusDistinctFromSelected(t *testing.T) {
	ts := NewTokenSet(theme.DefaultDark())
	_, focusBG := ts.ListRowColors(StateFocused)
	_, selBG := ts.ListRowColors(StateSelected)
	if focusBG.Equal(selBG) {
		t.Fatal("focused and selected list rows must use different backgrounds")
	}
	if !focusBG.Equal(ts.Color(ColorAccent)) {
		t.Fatal("focused list row should use Accent background")
	}
	if !selBG.Equal(ts.Color(ColorSelection)) {
		t.Fatal("selected (unfocused) list row should use Selection background")
	}
}
