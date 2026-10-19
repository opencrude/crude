package diffview

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestFindRevealsExactPaintedSubstringAndClearsStaleQuery(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{After: strings.Repeat("x", 90) + " needle needle tail"}))
	h := host(s, 70, 12)
	s.SetQuery("needle")
	settleSearch(t, s)
	buf := surface.NewCellBuffer(70, 12)
	h.Paint(buf)
	if !strings.Contains(text(buf), "needle") || s.after.horizontal.Offset() == 0 {
		t.Fatal("find did not reveal horizontal target")
	}
	x := s.after.Bounds().X + s.after.gutter() + 91 - s.after.horizontal.Offset()
	for i := 0; i < 6; i++ {
		c := buf.Get(x+i, 1)
		if c.BG != s.tokens.Color(ui.ColorAccent) || cell.ContrastRatio(c.FG, c.BG) < 4.5 {
			t.Fatalf("active match cell=%+v", c)
		}
	}
	if c := buf.Get(x-1, 1); c.BG == s.tokens.Color(ui.ColorAccent) {
		t.Fatal("highlight includes adjacent source")
	}
	// Reveal enough context to inspect both occurrences independently from the
	// find target's minimal visibility requirement.
	s.after.horizontal.ScrollTo(90)
	h.Paint(buf)
	x = s.after.Bounds().X + s.after.gutter() + 91 - s.after.horizontal.Offset()
	if c := buf.Get(x+7, 1); c.BG != s.tokens.Color(ui.ColorWarning) {
		t.Fatal("inactive occurrence not distinguished")
	}
	s.SetQuery("absent")
	settleSearch(t, s)
	if s.NextMatch(false) || s.matchIndex != -1 || s.SearchStatus() != "No matches" {
		t.Fatal("stale target survived changed query")
	}
	h.Paint(buf)
	if c := buf.Get(x, 1); c.BG == s.tokens.Color(ui.ColorAccent) {
		t.Fatal("stale highlight survived")
	}
	s.SetQuery("tail")
	settleSearch(t, s)
	if s.search.Active != 0 || s.matchIndex != 0 {
		t.Fatal("query did not choose first result")
	}
}

func TestSyntaxImmutablePreparationAndRuntimeTheme(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{Path: "example.go", After: "package main\n/* multiline\ncomment */\nvar greeting = \"hello\"\n"}))
	h := host(s, 100, 12)
	buf := surface.NewCellBuffer(100, 12)
	h.Paint(buf)
	x := s.after.Bounds().X + s.after.gutter()
	dark := buf.Get(x, 1).FG
	if dark == s.tokens.Color(ui.ColorDiffAddition) {
		t.Fatal("keyword lost syntax palette")
	}
	prepared := &s.prepared.afterSyntax[0][0]
	h.SetTheme(theme.DefaultLight())
	h.Paint(buf)
	light := buf.Get(x, 1).FG
	if light == dark || light == s.tokens.Color(ui.ColorDiffAddition) || prepared != &s.prepared.afterSyntax[0][0] {
		t.Fatal("theme re-lexed or retained stale syntax")
	}
	for i := 0; i < 30; i++ {
		h.Paint(buf)
	}
	if prepared != &s.prepared.afterSyntax[0][0] {
		t.Fatal("frame mutated prepared syntax")
	}
}

func TestSourceGeometryControlSafetyAndKeyboardSelection(t *testing.T) {
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{After: "\t界é👩‍💻\x1bX\r\nlast"}))
	h := host(s, 100, 12)
	buf := surface.NewCellBuffer(100, 12)
	h.Paint(buf)
	x := s.after.Bounds().X + s.after.gutter()
	if c := buf.Get(x+4, 1); c.Content != "界" {
		t.Fatalf("tab/CJK paint=%+v", c)
	}
	for y := 0; y < 12; y++ {
		for x := 0; x < 100; x++ {
			c := buf.Get(x, y)
			if c.Char == '\r' || c.Char == '\x1b' {
				t.Fatal("raw control painted")
			}
		}
	}
	h.Focus(s.after.ID())
	for i := 0; i < 3; i++ {
		h.Handle(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	}
	if got := s.after.SelectedText(); got != "\t界é" {
		t.Fatalf("keyboard split source grapheme: %q", got)
	}
	s.after.start = point{}
	s.after.end = point{row: 1, column: 4}
	s.after.selected = true
	if got := s.after.SelectedText(); got != "\t界é👩‍💻\x1bX\r\nlast" {
		t.Fatalf("range copy bytes=%q", got)
	}
	s.after.dragging = true
	s.after.CancelPointerCapture()
	if s.after.selected || s.after.dragging {
		t.Fatal("cancel retained pointer-owned selection")
	}
}

func BenchmarkDenseSyntaxDiffPaint(b *testing.B) {
	s := New(nil)
	s.SetPrepared(Prepare(Comparison{Path: "dense.go", After: strings.Repeat("var item = \"hello\" // comment\n", 10000)}))
	h := host(s, 160, 40)
	buf := surface.NewCellBuffer(160, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Paint(buf)
	}
}

func TestEmptyComparisonSelectionAndWidthProfile(t *testing.T) {
	s := New(nil)
	h := host(s, 100, 12)
	h.Focus(s.DocumentID())
	h.Handle(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	s.SetPrepared(Prepare(Comparison{After: strings.Repeat("☰", 100)}))
	h.MarkLayout()
	h.Layout()
	before := s.after.horizontal.Content()
	ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{Exact: map[string]int{"☰": 1}})
	t.Cleanup(func() { ansi.SetTerminalWidthProfile(ansi.TerminalWidthProfile{}) })
	s.InvalidateWidthProfile()
	h.MarkLayout()
	h.Layout()
	if s.after.horizontal.Content() != 100 || s.after.horizontal.Content() >= before {
		t.Fatal("comparison retained horizontal geometry from the previous terminal profile")
	}
}
