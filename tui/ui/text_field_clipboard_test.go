package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// newClipboardTextField builds a single-line field with a recording clipboard
// writer so chords are asserted without touching the system clipboard.
func newClipboardTextField(t *testing.T, value string) (*TextField, *[]string) {
	t.Helper()
	field := NewTextField("clip", NewTokenSet(theme.DefaultDark()))
	field.SetValue(value)
	field.Layout(LayoutContext{}, Rect{W: 20, H: 1})
	copied := &[]string{}
	field.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			*copied = append(*copied, text)
			return nil
		}
	})
	return field, copied
}

// Inline inputs (filters, form fields, search boxes) accept the same copy
// chords as the multi-line editor.
func TestTextFieldCopyChordsCopySelection(t *testing.T) {
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
			field, copied := newClipboardTextField(t, "hello")
			field.Edit().SelectAll()

			res := field.Handle(EventContext{Focused: true}, KeyEvent(tc.key))
			if !res.Handled {
				t.Fatalf("%s not handled", tc.name)
			}
			if field.Value() != "hello" {
				t.Fatalf("copy mutated the value: %q", field.Value())
			}
			runClipboardCmd(t, res)
			if len(*copied) != 1 || (*copied)[0] != "hello" {
				t.Fatalf("copied=%q want [hello]", *copied)
			}
		})
	}
}

func TestTextFieldCutChordCutsSelectionThenWholeValue(t *testing.T) {
	t.Run("selection", func(t *testing.T) {
		field, copied := newClipboardTextField(t, "hello world")
		field.Edit().SelectAll()

		res := field.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
		if got := field.Value(); got != "" {
			t.Fatalf("value=%q want empty", got)
		}
		runClipboardCmd(t, res)
		if len(*copied) != 1 || (*copied)[0] != "hello world" {
			t.Fatalf("copied=%q want ['hello world']", *copied)
		}
	})

	t.Run("no selection cuts whole value", func(t *testing.T) {
		field, copied := newClipboardTextField(t, "filter-text")
		field.Edit().ClearSelection()

		res := field.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
		if !res.Handled {
			t.Fatal("ctrl+x not handled")
		}
		if got := field.Value(); got != "" {
			t.Fatalf("value=%q want empty", got)
		}
		runClipboardCmd(t, res)
		if len(*copied) != 1 || (*copied)[0] != "filter-text" {
			t.Fatalf("copied=%q want ['filter-text']", *copied)
		}
	})
}

// A masked field refuses every clipboard command: the rewritten value must not
// travel through the clipboard, and Cut must not empty it either.
func TestTextFieldPasswordRefusesClipboard(t *testing.T) {
	field, copied := newClipboardTextField(t, "sk-secret")
	field.SetPassword(true)
	if !field.TextSecret() {
		t.Fatal("password field must report TextSecret")
	}
	field.Edit().SelectAll()

	copyRes := field.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
	if !copyRes.Handled || copyRes.Cmd != nil {
		t.Fatalf("password copy res=%#v", copyRes)
	}
	cutRes := field.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}))
	if !cutRes.Handled || cutRes.Cmd != nil {
		t.Fatalf("password cut res=%#v", cutRes)
	}
	if got := field.Value(); got != "sk-secret" {
		t.Fatalf("password field value=%q want unchanged", got)
	}
	if len(*copied) != 0 {
		t.Fatalf("password field wrote the clipboard: %q", *copied)
	}
}

// Esc clears the selection and otherwise stays unhandled so the popup keeps
// its own Esc ladder.
func TestTextFieldEscClearsSelectionThenFallsThrough(t *testing.T) {
	field, _ := newClipboardTextField(t, "hello")
	field.Edit().SelectAll()
	res := field.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if !res.Handled || field.HasSelection() {
		t.Fatalf("esc res=%#v selection=%v", res, field.HasSelection())
	}
	if res := field.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc})); res.Handled {
		t.Fatal("esc without a selection must fall through to the host")
	}
}
