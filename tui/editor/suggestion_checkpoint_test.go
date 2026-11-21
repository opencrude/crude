package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMultilineSuggestionKeepsTheRemainderAfterWordAccept(t *testing.T) {
	e := newInlayTestEditor(t, "call()")
	e.SetCursorPosition(0, 5)
	text := "alpha + beta\nsecond()"
	if err := e.SetSuggestion("errand", e.Revision(), Suggestion{Start: 5, End: 5, Text: text}); err != nil {
		t.Fatal(err)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	_, rest, ok := e.GhostText()
	if !ok || rest != " + beta\nsecond()" || e.Value() != "call(alpha)" {
		t.Fatalf("word accept: source=%q rest=%q shown=%v", e.Value(), rest, ok)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "call("+text+")" {
		t.Fatalf("Tab lost multiline text: %q", e.Value())
	}
}

func TestMultilineSuggestionRowsNeverBecomeSourceUntilTab(t *testing.T) {
	e := newInlayTestEditor(t, "call()\nnext()")
	e.SetCursorPosition(0, 5)
	if err := e.SetSuggestion("errand", e.Revision(), Suggestion{Start: 5, End: 5, Text: "first\nsecond\nthird"}); err != nil {
		t.Fatal(err)
	}
	renderTestInlays(e, 80, 24, false)
	if !e.hasGhostRows() || e.ghostRows(0) != 2 || e.visualRowCount() != 4 {
		t.Fatalf("ghost rows not projected: %d", e.visualRowCount())
	}
	if e.Value() != "call()\nnext()" || e.Modified() {
		t.Fatal("display-only rows modified source")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyEsc})
	if e.hasGhostRows() || e.visualRowCount() != 2 {
		t.Fatal("Escape kept display-only rows")
	}
	if strings.Contains(e.Value(), "second") {
		t.Fatal("Escape committed a suggestion")
	}
}
