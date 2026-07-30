package ui

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestHintPlainTextFromActions(t *testing.T) {
	reg := DefaultRegistry()
	h := NewHint("h", reg, NewTokenSet(theme.DefaultDark()))
	h.SetSegments([]HintSegment{
		ActionSeg("file.save"),
		ActionSeg("editor.close"),
	})
	got := h.PlainText()
	if !strings.Contains(got, "Ctrl+S save") || !strings.Contains(got, "Esc close") {
		t.Fatalf("PlainText=%q", got)
	}
}

func TestHintPaintBoldKey(t *testing.T) {
	th := theme.DefaultDark()
	tokens := NewTokenSet(th)
	h := NewHint("h", DefaultRegistry(), tokens)
	h.SetSegments([]HintSegment{ActionSeg("file.save")})
	h.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 1})
	buf := surface.NewCellBuffer(40, 1)
	h.Paint(buf, 0, 0, PaintContext{})
	c := buf.Get(0, 0)
	if c.Char != 'C' {
		t.Fatalf("expected Ctrl key start, got %q", string(c.Char))
	}
	if c.Attr&cell.AttrBold == 0 {
		t.Fatal("expected bold key")
	}
}

func TestKeyChordPlainText(t *testing.T) {
	k := NewKeyChord("k", "Ctrl+S", "save", nil)
	if got := k.PlainText(); got != "Ctrl+S save" {
		t.Fatalf("PlainText=%q", got)
	}
}
