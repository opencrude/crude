package diffview

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"fmt"
	"strings"
	"testing"
)

func text(buf *surface.CellBuffer) string {
	var b strings.Builder
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Content != "" {
				b.WriteString(c.Content)
			} else if c.Width > 0 {
				b.WriteRune(c.Char)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
func host(s *Screen, w, h int) *ui.Host {
	h3 := ui.NewHost(s.tokens.Theme(), s)
	s.SetFocusHandler(h3.Focus)
	h3.Resize(w, h)
	h3.Layout()
	return h3
}
func TestAlignedSourceMappingAndExactCopy(t *testing.T) {
	c := Comparison{BeforeLabel: "Saved v1", AfterLabel: "Buffer v2", Before: "a\n\told\r\nz\n", After: "a\nnew\nextra\nz\n"}
	p := Prepare(c)
	if len(p.rows) != 4 || p.rows[2].BeforeLine != 0 || p.rows[3].BeforeLine != 3 || p.rows[3].AfterLine != 4 {
		t.Fatalf("mapping=%+v", p.rows)
	}
	s := New(nil)
	s.SetPrepared(p)
	h := host(s, 100, 14)
	buf := surface.NewCellBuffer(100, 14)
	h.Paint(buf)
	for _, want := range []string{"Saved v1", "Buffer v2", "extra", "old"} {
		if !strings.Contains(text(buf), want) {
			t.Fatal(want)
		}
	}
	if buf.Get(s.before.Bounds().X+s.before.Bounds().W-1, 2).BG != s.tokens.Color(ui.ColorDiffDeletionBg) || buf.Get(s.after.Bounds().X+s.after.Bounds().W-1, 2).BG != s.tokens.Color(ui.ColorDiffAdditionBg) {
		t.Fatal("changed source rows lack full-width themed diff bands")
	}
	h.Focus(s.before.ID())
	h.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	if got := s.before.SelectedText(); got != c.Before {
		t.Fatalf("exact old copy=%q", got)
	}
	s.before.all = false
	s.before.start = point{row: 1}
	s.before.end = point{row: 3, column: 1}
	if got := s.before.SelectedText(); got != "\told\r\nz" {
		t.Fatalf("alignment leaked into copy=%q", got)
	}
	if !s.NavigateHunk(1) || s.hunkIndex != 0 {
		t.Fatal("hunk nav")
	}
	s.SetQuery("extra")
	settleSearch(t, s)
	if !s.NextMatch(false) || s.matchIndex != 2 {
		t.Fatal("search aligned insertion")
	}
	h.Focus(s.after.ID())
	h.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if s.Comparison() != c {
		t.Fatal("reader mutated source")
	}
}
func dense(n int) Comparison {
	var a, b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&a, "old line %04d\n", i)
		fmt.Fprintf(&b, "new line %04d\n", i)
	}
	return Comparison{BeforeLabel: "Saved", AfterLabel: "Buffer", Before: a.String(), After: b.String()}
}
func TestHostScrollbarAndSplitDragPreservePresentation(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(dense(200)))
	h := host(s, 120, 20)
	buf := surface.NewCellBuffer(120, 20)
	h.Paint(buf)
	initial := text(buf)
	bar, _ := h.ScreenRect(s.scrollbar.ID())
	h.Handle(tea.MouseClickMsg{X: bar.X, Y: bar.Y, Button: tea.MouseLeft})
	h.Handle(tea.MouseMotionMsg{X: bar.X - 15, Y: bar.Y + 8, Button: tea.MouseLeft})
	h.Handle(tea.MouseReleaseMsg{X: bar.X - 15, Y: bar.Y + 8, Button: tea.MouseLeft})
	h.Paint(buf)
	if s.ScrollOffset() == 0 || text(buf) == initial || h.HasPointerCapture() {
		t.Fatal("captured scrollbar did not move painted rows / release")
	}
	div, _ := h.ScreenRect(s.divider.ID())
	h.Handle(tea.MouseClickMsg{X: div.X, Y: div.Y + 2, Button: tea.MouseLeft})
	h.Handle(tea.MouseMotionMsg{X: div.X + 12, Y: div.Y + 2, Button: tea.MouseLeft})
	h.Handle(tea.MouseReleaseMsg{X: div.X + 12, Y: div.Y + 2, Button: tea.MouseLeft})
	h.Layout()
	width := s.before.Bounds().W
	if width <= 60 {
		t.Fatalf("split drag width=%d", width)
	}
	s.SetPresentation(Unified)
	h.MarkLayout()
	h.Layout()
	s.SetPresentation(SideBySide)
	h.MarkLayout()
	h.Layout()
	if s.before.Bounds().W != width {
		t.Fatal("toggle lost ratio")
	}
	h.Resize(30, 15)
	h.Layout()
	if s.Presentation() != Unified || s.PreferredPresentation() != SideBySide {
		t.Fatal("fallback lost preference")
	}
	h.Resize(120, 20)
	h.Layout()
	if s.before.Bounds().W != width {
		t.Fatal("narrow fallback lost ratio")
	}
}
func TestThemeSwitchRecolorsSelectionAndDiff(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(dense(3)))
	h := host(s, 100, 10)
	h.Focus(s.after.ID())
	h.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	h.SetTheme(theme.DefaultLight())
	buf := surface.NewCellBuffer(100, 10)
	h.Paint(buf)
	r := s.after.Bounds()
	c := buf.Get(r.X+s.after.gutter(), 1)
	if c.BG != ui.NewTokenSet(theme.DefaultLight()).Color(ui.ColorTextSelection) {
		t.Fatal("old-theme selection")
	}
	h.Handle(tea.KeyPressMsg{Code: tea.KeyEscape})
	h.Paint(buf)
	c = buf.Get(r.X+s.after.gutter(), 1)
	if c.FG != s.tokens.Color(ui.ColorDiffAddition) {
		t.Fatal("diff palette stale")
	}
}
func BenchmarkDenseDiffPrepare(b *testing.B) {
	c := dense(10000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Prepare(c)
	}
}
func BenchmarkDenseDiffPaint(b *testing.B) {
	s := New(nil)
	s.SetPrepared(Prepare(dense(10000)))
	h := host(s, 160, 40)
	buf := surface.NewCellBuffer(160, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Paint(buf)
	}
}

type nested struct {
	ui.Base
	child *Screen
}

func (n *nested) Children() []ui.Component { return []ui.Component{n.child} }
func (n *nested) Layout(ctx ui.LayoutContext, r ui.Rect) {
	n.SetBounds(r)
	n.child.Layout(ctx, ui.Rect{X: 7, Y: 4, W: max(0, r.W-12), H: max(0, r.H-7)})
}
func TestNestedNonzeroOriginPointerAndPaint(t *testing.T) {
	s := NewWithID(nil, "nested/diff")
	s.SetPrepared(Prepare(dense(150)))
	root := &nested{Base: ui.NewBase("root"), child: s}
	h := ui.NewHost(theme.DefaultDark(), root)
	h.Resize(132, 27)
	h.Layout()
	buf := surface.NewCellBuffer(132, 27)
	h.Paint(buf)
	if c := buf.Get(7, 4); c.Char != 'S' {
		t.Fatalf("label origin=%+v", c)
	}
	div, _ := h.ScreenRect(s.divider.ID())
	start := s.before.Bounds().W
	h.Handle(tea.MouseClickMsg{X: div.X, Y: div.Y + 3, Button: tea.MouseLeft})
	h.Handle(tea.MouseMotionMsg{X: div.X + 9, Y: div.Y + 3, Button: tea.MouseLeft})
	h.Handle(tea.MouseReleaseMsg{X: div.X + 9, Y: div.Y + 3, Button: tea.MouseLeft})
	h.Layout()
	if s.before.Bounds().W != start+9 {
		t.Fatal("nested capture geometry")
	}
	h.Paint(buf)
	for y := 0; y < 4; y++ {
		for x := 0; x < 132; x++ {
			if c := buf.Get(x, y); c.Char != 0 && c.Char != ' ' {
				t.Fatalf("paint escaped above slot at %d,%d", x, y)
			}
		}
	}
	pane, _ := h.ScreenRect(s.after.ID())
	h.Handle(tea.MouseClickMsg{X: pane.X + s.after.gutter(), Y: pane.Y + 1, Button: tea.MouseLeft})
	h.Handle(tea.MouseMotionMsg{X: pane.X + s.after.gutter() + 3, Y: pane.Y + 1, Button: tea.MouseLeft})
	r := h.Handle(tea.MouseReleaseMsg{X: pane.X + s.after.gutter() + 3, Y: pane.Y + 1, Button: tea.MouseLeft})
	if r.Cmd == nil || h.HasPointerCapture() || s.after.selected {
		t.Fatal("nested selection did not copy and clear")
	}
}
func TestAddedDeletedAndMissingNewlineMapping(t *testing.T) {
	for _, c := range []Comparison{{Before: "", After: "first\nlast"}, {Before: "first\nlast", After: ""}, {Before: "a\nb\nc\n", After: "a\nc\n"}, {Before: "a\nc\n", After: "a\nb\nc\n"}} {
		s := New(nil)
		s.SetPrepared(Prepare(c))
		host(s, 100, 12)
		s.before.selected = true
		s.before.all = true
		s.after.selected = true
		s.after.all = true
		if s.before.SelectedText() != c.Before || s.after.SelectedText() != c.After {
			t.Fatalf("source bytes changed: %+v", c)
		}
		for _, r := range s.prepared.rows {
			if r.BeforeLine == 0 && r.Before != "" || r.AfterLine == 0 && r.After != "" {
				t.Fatalf("invalid absence mapping: %+v", r)
			}
		}
	}
}
