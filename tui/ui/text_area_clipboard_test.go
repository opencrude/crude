package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// newClipboardTextArea builds a text area with a recording clipboard writer so
// chords are asserted without touching the system clipboard.
func newClipboardTextArea(t *testing.T, value string) (*TextArea, *[]string) {
	t.Helper()
	area := NewTextArea("clip", NewTokenSet(theme.DefaultDark()), true)
	area.SetValue(value)
	area.Layout(LayoutContext{}, Rect{W: 40, H: 4})
	copied := &[]string{}
	area.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			*copied = append(*copied, text)
			return nil
		}
	})
	return area, copied
}

func runClipboardCmd(t *testing.T, res EventResult) {
	t.Helper()
	if res.Cmd == nil {
		t.Fatal("chord returned no clipboard command")
	}
	_ = res.Cmd()
}

// Ctrl+C and Ctrl+Shift+C copy the selection and never mutate the text; the
// explicit chord exists because terminals often swallow Ctrl+Shift+C for their
// own copy and send either encoding to the app.
func TestTextAreaCopyChordsCopySelection(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "ctrl+c", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
		{name: "ctrl+shift+c", key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}},
		{name: "ctrl+shift+C uppercase", key: tea.KeyPressMsg{Code: 'C', Mod: tea.ModCtrl | tea.ModShift}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			area, copied := newClipboardTextArea(t, "hello world")
			area.SetSelection(0, 5)

			res := area.Handle(EventContext{Focused: true}, KeyEvent(tc.key))
			if !res.Handled {
				t.Fatalf("%s not handled", tc.name)
			}
			if area.Value() != "hello world" {
				t.Fatalf("copy mutated the value: %q", area.Value())
			}
			if !area.HasActiveSelection() {
				t.Fatal("copy dropped the selection")
			}
			if len(*copied) != 0 {
				t.Fatalf("clipboard write ran synchronously: %q", *copied)
			}
			runClipboardCmd(t, res)
			if len(*copied) != 1 || (*copied)[0] != "hello" {
				t.Fatalf("copied=%q want [hello]", *copied)
			}
		})
	}
}

// Ctrl+X cuts the selection, and with nothing selected it cuts the whole value
// using the same command the context menu's Cut performs.
func TestTextAreaCutChordCutsSelectionThenWholeValue(t *testing.T) {
	t.Run("selection", func(t *testing.T) {
		area, copied := newClipboardTextArea(t, "hello world")
		area.SetSelection(0, 6)

		res := area.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
		if !res.Handled {
			t.Fatal("ctrl+x not handled")
		}
		if got := area.Value(); got != "world" {
			t.Fatalf("value=%q want 'world'", got)
		}
		if area.HasActiveSelection() {
			t.Fatal("cut kept the selection")
		}
		runClipboardCmd(t, res)
		if len(*copied) != 1 || (*copied)[0] != "hello " {
			t.Fatalf("copied=%q want ['hello ']", *copied)
		}
	})

	t.Run("no selection cuts whole value", func(t *testing.T) {
		area, copied := newClipboardTextArea(t, "draft text")
		area.ClearSelection()

		res := area.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
		if !res.Handled {
			t.Fatal("ctrl+x not handled")
		}
		if got := area.Value(); got != "" {
			t.Fatalf("value=%q want empty", got)
		}
		runClipboardCmd(t, res)
		if len(*copied) != 1 || (*copied)[0] != "draft text" {
			t.Fatalf("copied=%q want ['draft text']", *copied)
		}
	})
}

// Esc drops the selection and is otherwise left to the host (popups close, the
// chat composer clears its draft). Ctrl+C with nothing selected is likewise
// unhandled so the composer can implement "clear the draft".
func TestTextAreaEscAndUnhandledCopyFallThrough(t *testing.T) {
	t.Run("esc clears selection", func(t *testing.T) {
		area, _ := newClipboardTextArea(t, "hello world")
		area.SetSelection(2, 7)

		res := area.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
		if !res.Handled {
			t.Fatal("esc with a selection must be handled")
		}
		if area.HasActiveSelection() {
			t.Fatal("esc kept the selection")
		}
		if got := area.Value(); got != "hello world" {
			t.Fatalf("esc mutated the value: %q", got)
		}
	})

	t.Run("esc without selection falls through", func(t *testing.T) {
		area, _ := newClipboardTextArea(t, "hello world")
		area.ClearSelection()
		if res := area.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc})); res.Handled {
			t.Fatal("esc without a selection must fall through to the host")
		}
	})

	t.Run("ctrl+c without selection falls through", func(t *testing.T) {
		area, copied := newClipboardTextArea(t, "hello world")
		area.ClearSelection()
		res := area.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
		if res.Handled {
			t.Fatal("ctrl+c without a selection must fall through to the host")
		}
		if res.Cmd != nil {
			t.Fatal("ctrl+c without a selection produced a clipboard command")
		}
		if len(*copied) != 0 {
			t.Fatalf("copied=%q want none", *copied)
		}
	})
}

// A read-only area still copies but never cuts.
func TestTextAreaReadOnlyCopiesButNeverCuts(t *testing.T) {
	area, copied := newClipboardTextArea(t, "preview text")
	area.SetReadOnly(true)
	area.SetSelection(0, 7)

	copyRes := area.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
	runClipboardCmd(t, copyRes)
	if len(*copied) != 1 || (*copied)[0] != "preview" {
		t.Fatalf("copied=%q want ['preview']", *copied)
	}
	area.SetSelection(0, 7)
	if res := area.Handle(EventContext{}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})); !res.Handled {
		t.Fatal("ctrl+x on a read-only area must be consumed, not fall through")
	}
	if got := area.Value(); got != "preview text" {
		t.Fatalf("read-only cut mutated the value: %q", got)
	}
	if len(*copied) != 1 {
		t.Fatalf("read-only cut leaked a clipboard write: %q", *copied)
	}
}

// Dragging writes the selection to the clipboard on release and drops it, the
// same contract the chat message area and docked terminals already follow.
func TestTextAreaDragCopiesOnReleaseAndDropsSelection(t *testing.T) {
	area, copied := newClipboardTextArea(t, "hello world")
	area.Layout(LayoutContext{}, Rect{W: 40, H: 4})

	area.Handle(EventContext{LocalX: 0, LocalY: 0}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	area.Handle(EventContext{LocalX: 5, LocalY: 0}, MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
	if got := area.SelectedText(); got != "hello" {
		t.Fatalf("drag selected %q want 'hello'", got)
	}
	res := area.Handle(EventContext{LocalX: 5, LocalY: 0}, MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft}))
	if area.HasActiveSelection() || area.IsSelecting() {
		t.Fatalf("release kept selection=%q selecting=%v", area.SelectedText(), area.IsSelecting())
	}
	runClipboardCmd(t, res)
	if len(*copied) != 1 || (*copied)[0] != "hello" {
		t.Fatalf("copied=%q want ['hello']", *copied)
	}
	// Nothing is copied when the press never dragged.
	area.Handle(EventContext{LocalX: 0, LocalY: 0}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if res := area.Handle(EventContext{LocalX: 0, LocalY: 0}, MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft})); res.Cmd != nil {
		t.Fatal("click without drag returned a clipboard command")
	}
}
