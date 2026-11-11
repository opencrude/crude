package ui

import (
	tea "charm.land/bubbletea/v2"
	"testing"
)

func TestPastedLineBreaksUsesOneForm(t *testing.T) {
	for in, want := range map[string]string{
		"":              "",
		"plain":         "plain",
		"a\nb":          "a\nb",
		"a\rb\rc":       "a\nb\nc",
		"a\r\nb\r\n":    "a\nb\n",
		"a\r\nb\rc\nd":  "a\nb\nc\nd",
		"a\r\r\nb":      "a\n\nb",
		"trailing\r":    "trailing\n",
		"\r\n\r\n":      "\n\n",
		"tab\tkept\r日本": "tab\tkept\n日本",
	} {
		if got := PastedLineBreaks(in); got != want {
			t.Fatalf("PastedLineBreaks(%q) = %q, want %q", in, got, want)
		}
	}
}

// Clipboard completion can target the original control directly instead of
// re-entering App.Update. Both TextArea entry points must decode bare CR too.
func TestTextAreaDirectPasteKeepsTerminalLineBreaks(t *testing.T) {
	for _, direct := range []bool{false, true} {
		area := NewTextArea("paste", NewTokenSet(nil), true)
		paste := tea.PasteMsg{Content: "one\rtwo\r\nthree"}
		if direct {
			area.Update(paste, false)
		} else {
			area.Handle(EventContext{Focused: true}, CustomEvent(paste))
		}
		if got := area.Value(); got != "one\ntwo\nthree" {
			t.Fatalf("direct=%t: %q", direct, got)
		}
	}
}
