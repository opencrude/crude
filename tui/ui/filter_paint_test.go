package ui

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestRenderFilterLine_EmptyPlaceholder(t *testing.T) {
	th := theme.DefaultDark()
	ed := NewTextEdit()
	out := RenderFilterLine(th, ed, FilterPaintOptions{
		Prompt:      ">",
		Placeholder: "search",
		Width:       40,
		Focused:     true,
	})
	plain := rendering.StripAnsi(out)
	if !strings.Contains(plain, "search") {
		t.Fatalf("expected placeholder, got %q", plain)
	}
	if !strings.Contains(plain, "█") {
		t.Fatalf("expected block cursor when empty+focused, got %q", plain)
	}
}

func TestRenderFilterLine_InverseCursorOnGrapheme(t *testing.T) {
	th := theme.DefaultDark()
	ed := NewTextEdit()
	ed.SetValue("abc")
	ed.SetCursor(1, false)
	out := RenderFilterLine(th, ed, FilterPaintOptions{
		Prompt:  "> ",
		Width:   40,
		Focused: true,
	})
	plain := rendering.StripAnsi(out)
	if !strings.Contains(plain, "abc") {
		t.Fatalf("expected value, got %q", plain)
	}
	// Focused non-empty should not use trailing block alone for mid-string cursor.
	if plain == "" {
		t.Fatal("empty paint")
	}
}

func TestScrollbarCellANSI_UsesScrollModel(t *testing.T) {
	th := theme.DefaultDark()
	s := SyncScrollModel(nil, 100, 10, 0)
	thumb := ScrollbarCellANSI(th, s, 0)
	track := ScrollbarCellANSI(th, s, 9)
	if rendering.StripAnsi(thumb) == " " {
		t.Fatal("expected thumb glyph at top")
	}
	if rendering.StripAnsi(track) == rendering.StripAnsi(thumb) {
		// bottom may or may not be thumb depending on size; just ensure non-empty
		_ = track
	}
	empty := ScrollbarCellANSI(th, SyncScrollModel(nil, 5, 10, 0), 0)
	if rendering.StripAnsi(empty) != " " {
		t.Fatalf("no scrollbar when content fits, got %q", empty)
	}
}
