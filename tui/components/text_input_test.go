package components

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// TestTextInput_OnSubmit verifies the new OnSubmit callback fires
// on Enter in single-line mode (default). The callback is the
// primary way popups like find/replace and text prompt are wired
// to the Enter key without going through the SubmitEvent message
// round-trip.
func TestTextInput_OnSubmit(t *testing.T) {
	th := theme.DefaultDark()
	in := NewTextInput(th)
	called := 0
	in.onSubmit = func() { called++ }

	// Type "hi".
	in, _ = in.Update(tea.KeyPressMsg{Code: 'h', Text: "h"}, false)
	in, _ = in.Update(tea.KeyPressMsg{Code: 'i', Text: "i"}, false)
	if got := in.Value(); got != "hi" {
		t.Fatalf("expected value 'hi', got %q", got)
	}

	// Press Enter — onSubmit should fire.
	in, _ = in.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false)
	if called != 1 {
		t.Errorf("expected onSubmit called once, got %d", called)
	}
}

// TestTextInput_OnSubmit_MultiLine verifies that in multi-line
// mode, Enter inserts a newline (does NOT fire onSubmit). The
// caller can still use Alt+Enter to invoke onSubmit in multi-line
// mode (matches VSCode's text input behavior).
func TestTextInput_OnSubmit_MultiLine(t *testing.T) {
	th := theme.DefaultDark()
	in := NewTextInput(th)
	in.multiLine = true
	called := 0
	in.onSubmit = func() { called++ }

	in, _ = in.Update(tea.KeyPressMsg{Code: 'h', Text: "h"}, false)
	in, _ = in.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false)
	in, _ = in.Update(tea.KeyPressMsg{Code: 'i', Text: "i"}, false)

	if got := in.Value(); got != "h\ni" {
		t.Errorf("expected multi-line value 'h\\ni', got %q", got)
	}
	if called != 0 {
		t.Errorf("onSubmit should NOT fire in multi-line mode on Enter")
	}
}

// TestTextInput_RuneSafeCursor verifies the cursor handles multi-
// byte input correctly. Regression test for the byte-offset bug
// in the find/replace popup.
func TestTextInput_RuneSafeCursor(t *testing.T) {
	th := theme.DefaultDark()
	in := NewTextInput(th)
	for _, r := range "日本語" {
		in, _ = in.Update(tea.KeyPressMsg{Code: r, Text: string(r)}, false)
	}
	if got := in.Value(); got != "日本語" {
		t.Fatalf("value corrupted by multi-byte input: got %q", got)
	}
	if got := in.CursorFlat(); got != 3 {
		t.Errorf("expected cursor at rune 3, got %d (byte-cursor bug!)", got)
	}

	// Backspace should remove the last rune (1 step), not the
	// last byte (3 steps). The byte-cursor bug would put the
	// cursor at byte 6, splitting the middle rune.
	in, _ = in.Update(tea.KeyPressMsg{Code: tea.KeyBackspace}, false)
	if got := in.Value(); got != "日本" {
		t.Errorf("expected '日本' after backspace, got %q", got)
	}
	if got := in.CursorFlat(); got != 2 {
		t.Errorf("expected cursor at rune 2 after backspace, got %d", got)
	}
}

// TestTextInput_Placeholder is a placeholder for the placeholder
// feature — currently the popupchrome.PopupInputField handles
// placeholders externally. A real TextInput placeholder would
// render italic-muted text when the buffer is empty.
func TestTextInput_Placeholder(t *testing.T) {
	th := theme.DefaultDark()
	in := NewTextInput(th)
	in.placeholder = "type something…"
	// Placeholder is set; the field itself is empty.
	if in.Value() != "" {
		t.Error("expected empty value")
	}
	if in.placeholder == "" {
		t.Error("placeholder should be set")
	}
	// Note: render integration is in widgets/popupchrome.go; the
	// TextInput itself just stores the placeholder for callers.
}
