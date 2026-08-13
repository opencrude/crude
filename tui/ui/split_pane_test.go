package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

func TestSplitModel_ResolveFractionAndAbsolute(t *testing.T) {
	m := NewSplitModel(Horizontal)
	first, div, second := m.Resolve(10)
	if first != 5 || div != 1 || second != 4 {
		t.Fatalf("fraction 0.5: first=%d div=%d second=%d", first, div, second)
	}
	m.SetFirstSize(7)
	first, div, second = m.Resolve(10)
	if first != 7 || div != 1 || second != 2 {
		t.Fatalf("abs 7: first=%d div=%d second=%d", first, div, second)
	}
	m.SetFirstSize(100)
	first, _, _ = m.Resolve(10)
	if first != 9 {
		t.Fatalf("clamped first=%d", first)
	}
}

func TestSplitModel_MinSizes(t *testing.T) {
	m := NewSplitModel(Horizontal)
	m.SetMinSizes(3, 4)
	m.SetFirstSize(1)
	first, _, second := m.Resolve(10)
	if first != 3 || second != 6 {
		t.Fatalf("min first: first=%d second=%d", first, second)
	}
	m.SetFirstSize(9)
	first, _, second = m.Resolve(10)
	if first != 5 || second != 4 {
		t.Fatalf("min second: first=%d second=%d", first, second)
	}
}

func TestSplitModel_DragDelta(t *testing.T) {
	m := NewSplitModel(Horizontal)
	m.SetMinSizes(2, 0)
	m.SetMaxFirst(20)
	m.BeginDrag(10, 8)
	m.DragTo(14)
	if m.FirstSize() != 12 {
		t.Fatalf("drag +4: first=%d", m.FirstSize())
	}
	m.DragTo(0)
	if m.FirstSize() != 2 {
		t.Fatalf("drag clamped to min: first=%d", m.FirstSize())
	}
	m.EndDrag()
	if m.Dragging() {
		t.Fatal("EndDrag should clear dragging")
	}
}

func TestSplitPane_DragResizesFirstPane(t *testing.T) {
	th := theme.DefaultDark()
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	s := NewSplitPane("s", Horizontal, a, b)
	s.SetMinSizes(2, 2)
	host := NewHost(th, s)
	host.Resize(20, 5)
	host.Layout()
	if a.Bounds().W != 10 {
		t.Fatalf("precondition first W=%d", a.Bounds().W)
	}
	div := s.Divider()
	if div == nil {
		t.Fatal("missing divider")
	}
	dr := div.Bounds()
	clickX := dr.X
	res := host.Handle(tea.MouseClickMsg{X: clickX, Y: 1, Button: tea.MouseLeft})
	if !res.Handled || !host.HasPointerCapture() {
		t.Fatalf("click divider: handled=%v capture=%v", res.Handled, host.HasPointerCapture())
	}
	res = host.Handle(tea.MouseMotionMsg{X: clickX + 4, Y: 1, Button: tea.MouseLeft})
	if !res.Handled {
		t.Fatal("motion should be handled while captured")
	}
	host.Layout()
	if a.Bounds().W != 14 {
		t.Fatalf("after +4 drag first W=%d want 14", a.Bounds().W)
	}
	if b.Bounds().X != 15 {
		t.Fatalf("second X=%d want 15", b.Bounds().X)
	}
	host.Handle(tea.MouseReleaseMsg{X: clickX + 4, Y: 1, Button: tea.MouseLeft})
	if host.HasPointerCapture() {
		t.Fatal("release should clear capture")
	}
}

func TestSplitPane_VerticalDrag(t *testing.T) {
	th := theme.DefaultDark()
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	s := NewSplitPane("s", Vertical, a, b)
	s.SetMinSizes(2, 2)
	host := NewHost(th, s)
	host.Resize(8, 20)
	host.Layout()
	div := s.Divider()
	clickY := div.Bounds().Y
	host.Handle(tea.MouseClickMsg{X: 1, Y: clickY, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: 1, Y: clickY - 3, Button: tea.MouseLeft})
	host.Layout()
	if a.Bounds().H != 7 {
		t.Fatalf("after -3 drag first H=%d want 7", a.Bounds().H)
	}
}

func TestSplitDivider_PaintsCenteredGlyph(t *testing.T) {
	th := theme.DefaultDark()
	th.Glyphs.BorderV = "!"
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	s := NewSplitPane("s", Horizontal, a, b)
	s.SetTokens(NewTokenSet(th))
	host := NewHost(th, s)
	host.Resize(10, 3)
	buf := surface.NewCellBuffer(10, 3)
	host.Paint(buf)
	for y := 0; y < 3; y++ {
		if c := buf.Get(5, y).Char; c != '!' {
			t.Fatalf("divider col 5 row %d char=%q want '!'", y, string(c))
		}
	}
}

func TestSplitPane_KeyboardNudge(t *testing.T) {
	th := theme.DefaultDark()
	a := newTestBox("a", 1, 1, 'a')
	b := newTestBox("b", 1, 1, 'b')
	s := NewSplitPane("s", Horizontal, a, b)
	s.SetMinSizes(2, 2)
	host := NewHost(th, s)
	host.Resize(20, 4)
	host.Layout()
	host.Focus(s.Divider().ID())
	res := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !res.Handled {
		t.Fatal("Right should nudge the focused divider")
	}
	host.Layout()
	if a.Bounds().W != 11 {
		t.Fatalf("nudge Right first W=%d want 11", a.Bounds().W)
	}
}
