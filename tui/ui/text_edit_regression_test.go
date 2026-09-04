package ui

import "testing"

func TestTextEditBackspaceAfterZeroWidthSelectionKeepsNextInsertSafe(t *testing.T) {
	e := NewTextEdit()
	e.SetValue("abc")
	e.SetCursor(3, false)
	// TextField mouse clicks intentionally create a zero-width active
	// selection so Shift can extend from that caret.
	e.SetCursor(3, true)
	e.Backspace()
	e.InsertRunes("p")
	if got := e.Value(); got != "abp" {
		t.Fatalf("value=%q want %q", got, "abp")
	}
	if e.Cursor() != 3 || e.HasSelection() {
		t.Fatalf("cursor=%d selection=%v after insert", e.Cursor(), e.HasSelection())
	}
}
