package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestComponentID(t *testing.T) {
	id := NewID(RootID, "dialog")
	if id != "dialog" {
		t.Fatal(id)
	}
	id2 := NewID(id, "ok")
	if id2 != "dialog/ok" {
		t.Fatal(id2)
	}
	if id2.Parent() != id {
		t.Fatal(id2.Parent())
	}
	if id2.Segment() != "ok" {
		t.Fatal(id2.Segment())
	}
	if ComponentID("top").Parent() != RootID {
		t.Fatal("top parent")
	}
}

func TestConstraintsClamp(t *testing.T) {
	cs := Constraints{MinW: 2, MaxW: 10, MinH: 1, MaxH: 5}
	s := cs.ClampSize(Size{W: 1, H: 9})
	if s.W != 2 || s.H != 5 {
		t.Fatal(s)
	}
	s = Tight(3, 4).ClampSize(Size{W: 99, H: 99})
	if s.W != 3 || s.H != 4 {
		t.Fatal(s)
	}
}

func TestEventResultMerge(t *testing.T) {
	a := EventResult{Handled: true, Action: &Action{ID: "a"}, Invalidate: InvalidatePaint}
	b := EventResult{Handled: false, Action: &Action{ID: "b"}, Invalidate: InvalidateLayout}
	m := a.Merge(b)
	if !m.Handled || m.Action.ID != "b" {
		t.Fatal(m)
	}
	if m.Invalidate&(InvalidatePaint|InvalidateLayout) != InvalidatePaint|InvalidateLayout {
		t.Fatal(m.Invalidate)
	}
}

func TestLabel_MeasurePaint(t *testing.T) {
	th := theme.DefaultDark()
	l := NewLabel("lbl", "hello", th)
	l.SetStyle(cell.Red, cell.Blue, 0)
	sz := l.Measure(LayoutContext{Theme: th}, Loose(80, 10))
	if sz.W != 5 || sz.H != 1 {
		t.Fatal(sz)
	}
	l.Layout(LayoutContext{Theme: th}, Rect{X: 0, Y: 0, W: 5, H: 1})
	buf := surface.NewCellBuffer(10, 2)
	l.Paint(buf, 0, 0, PaintContext{Theme: th})
	if buf.Get(0, 0).Char != 'h' || buf.Get(4, 0).Char != 'o' {
		t.Fatalf("paint: %c %c", buf.Get(0, 0).Char, buf.Get(4, 0).Char)
	}
	// Truncation
	l.SetText("hello world")
	l.Layout(LayoutContext{}, Rect{W: 5, H: 1})
	buf.Clear()
	l.Paint(buf, 0, 0, PaintContext{})
	// last should be ellipsis
	if buf.Get(4, 0).Char != '…' {
		t.Fatalf("ellipsis got %U", buf.Get(4, 0).Char)
	}
	sem := l.Semantics()
	if sem.Role != RoleLabel || sem.Value != "hello world" {
		t.Fatal(sem)
	}
}

func TestButton_ActivateKeyAndClick(t *testing.T) {
	btn := NewButton("btn", "Save", "form.save")
	btn.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	res := btn.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !res.Handled || res.Action == nil || res.Action.ID != "form.save" {
		t.Fatalf("%+v", res)
	}
	res = btn.Handle(EventContext{}, tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if !res.Handled || res.Action.ID != "form.save" {
		t.Fatal(res)
	}
	btn.SetDisabled(true)
	res = btn.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Handled {
		t.Fatal("disabled should not activate")
	}
}

func TestButton_PaintStates(t *testing.T) {
	btn := NewButton("btn", "OK", "ok")
	tokens := NewTokenSet(theme.DefaultDark())
	btn.SetTokens(tokens)
	btn.Layout(LayoutContext{}, Rect{W: 8, H: 1})
	buf := surface.NewCellBuffer(8, 1)
	btn.Paint(buf, 0, 0, PaintContext{Focused: true})
	if buf.Get(0, 0).Char != '[' {
		t.Fatalf("got %c", buf.Get(0, 0).Char)
	}
	_, wantBG := tokens.ButtonColors(StateFocused)
	if !buf.Get(0, 0).BG.Equal(wantBG) {
		t.Fatalf("focus bg %+v want %+v", buf.Get(0, 0).BG, wantBG)
	}
}

func TestStack_VerticalLayout(t *testing.T) {
	th := theme.DefaultDark()
	a := NewLabel("a", "one", th)
	b := NewLabel("b", "two", th)
	s := NewStack("stack", Vertical, 1, a, b)
	s.Layout(LayoutContext{Theme: th}, Rect{W: 10, H: 5})
	if a.Bounds().Y != 0 || a.Bounds().H != 1 {
		t.Fatal(a.Bounds())
	}
	if b.Bounds().Y != 2 { // 1 height + 1 gap
		t.Fatalf("b.Y=%d", b.Bounds().Y)
	}
	if len(s.Children()) != 2 {
		t.Fatal(len(s.Children()))
	}
}

func TestStack_HorizontalLayout(t *testing.T) {
	th := theme.DefaultDark()
	a := NewLabel("a", "AB", th)
	b := NewLabel("b", "CD", th)
	s := NewStack("stack", Horizontal, 1, a, b)
	s.Layout(LayoutContext{Theme: th}, Rect{W: 20, H: 1})
	if a.Bounds().X != 0 {
		t.Fatal(a.Bounds())
	}
	// a width 2 + gap 1 = b at 3
	if b.Bounds().X != 3 {
		t.Fatalf("b.X=%d", b.Bounds().X)
	}
}

func TestHost_LayoutHitFocusPaint(t *testing.T) {
	th := theme.DefaultDark()
	save := NewButton("save", "Save", "save")
	cancel := NewButton("cancel", "Cancel", "cancel")
	root := NewStack("root", Horizontal, 2, save, cancel)
	h := NewHost(th, root)
	h.Register(save)
	h.Register(cancel)
	h.Resize(40, 5)
	h.Layout()

	// Hit test first button area
	id := h.HitTest(1, 0)
	if id != "save" {
		t.Fatalf("hit=%q", id)
	}

	// Click save
	res := h.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "save" {
		t.Fatalf("action %+v", res.Action)
	}
	if h.Focused() != "save" {
		t.Fatalf("focus %q", h.Focused())
	}

	// Keyboard activate focused
	res = h.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Action == nil || res.Action.ID != "save" {
		t.Fatalf("key action %+v", res.Action)
	}

	// Paint
	buf := surface.NewCellBuffer(40, 5)
	h.Paint(buf)
	// Should contain 'S' from Save
	found := false
	for y := 0; y < 5; y++ {
		for x := 0; x < 40; x++ {
			if buf.Get(x, y).Char == 'S' {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected Save painted")
	}
}

func TestHost_HoverMotion(t *testing.T) {
	th := theme.DefaultDark()
	btn := NewButton("b", "X", "x")
	root := NewStack("root", Vertical, 0, btn)
	h := NewHost(th, root)
	h.Register(btn)
	h.Resize(20, 3)
	h.Handle(tea.MouseMotionMsg{X: 1, Y: 0})
	if h.Hovered() != "b" {
		t.Fatalf("hover %q", h.Hovered())
	}
}

func TestHost_FocusUnknown(t *testing.T) {
	h := NewHost(theme.DefaultDark(), NewLabel("l", "x", nil))
	if h.Focus("missing") {
		t.Fatal("should reject unknown")
	}
}

type actionCatchingStack struct {
	*Stack
	caught string
}

func (s *actionCatchingStack) HandleAction(_ EventContext, action Action) EventResult {
	s.caught = action.ID
	return EventResult{Handled: true}
}

func TestHostBubblesActionsToMountedParents(t *testing.T) {
	button := NewButton("root/save", "Save", "settings.save")
	root := &actionCatchingStack{Stack: NewStack("root", Vertical, 0, button)}
	host := NewHost(theme.DefaultDark(), root)
	host.Resize(30, 3)
	host.Layout()
	host.Focus(button.ID())

	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !result.Handled || result.Action != nil || root.caught != "settings.save" {
		t.Fatalf("bubbled result=%#v caught=%q", result, root.caught)
	}
}

func TestRectContainsEmpty(t *testing.T) {
	r := Rect{X: 1, Y: 1, W: 2, H: 2}
	if !r.Contains(1, 1) || r.Contains(3, 1) {
		t.Fatal("contains")
	}
	if !(Rect{}).Empty() {
		t.Fatal("empty")
	}
}
