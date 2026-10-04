package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

// TestBracketedPasteContractTable is the shared paste contract for TextEdit,
// TextField (single-line), and TextArea (multi-line). Bracketed paste is the
// primary path; Ctrl+V fallback routes through the same InsertRunes/InsertString.
func TestBracketedPasteContractTable(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)

	tests := []struct {
		name      string
		initial   string
		paste     string
		wantEdit  string // TextEdit.InsertRunes (append at cursor)
		wantField string // TextField tea.PasteMsg
		wantArea  string // TextArea tea.PasteMsg (preserves newlines)
	}{
		{
			name:      "plain_ascii",
			initial:   "",
			paste:     "hello world",
			wantEdit:  "hello world",
			wantField: "hello world",
			wantArea:  "hello world",
		},
		{
			name:      "unicode_cjk",
			initial:   "prefix",
			paste:     "日本語test",
			wantEdit:  "prefix日本語test",
			wantField: "prefix日本語test",
			wantArea:  "prefix日本語test",
		},
		{
			name:      "newline_stripped_single_line",
			initial:   "",
			paste:     "a\nb\nc",
			wantEdit:  "abc",
			wantField: "abc",
			wantArea:  "a\nb\nc",
		},
		{
			name:      "tab_stripped_single_line",
			initial:   "",
			paste:     "a\tb",
			wantEdit:  "ab",
			wantField: "ab",
			wantArea:  "a\tb",
		},
		{
			name:      "crlf_stripped_single_line",
			initial:   "",
			paste:     "a\r\nb",
			wantEdit:  "ab",
			wantField: "ab",
			wantArea:  "a\nb",
		},
		{
			name:      "control_chars_filtered",
			initial:   "",
			paste:     "a\x00\x1bb",
			wantEdit:  "ab",
			wantField: "ab",
			wantArea:  "a\x00\x1bb",
		},
		{
			name:      "empty_paste_noop",
			initial:   "seed",
			paste:     "",
			wantEdit:  "seed",
			wantField: "seed",
			wantArea:  "seed",
		},
		{
			name:      "whitespace_only_filtered_single_line",
			initial:   "seed",
			paste:     "\n\t\r",
			wantEdit:  "seed",
			wantField: "seed",
			wantArea:  "seed\n\t",
		},
		{
			name:      "mixed_printable_and_whitespace",
			initial:   "",
			paste:     "x y\nz",
			wantEdit:  "x yz",
			wantField: "x yz",
			wantArea:  "x y\nz",
		},
	}

	for _, tc := range tests {
		t.Run("TextEdit/"+tc.name, func(t *testing.T) {
			e := NewTextEdit()
			e.SetValue(tc.initial)
			e.InsertRunes(tc.paste)
			if got := e.Value(); got != tc.wantEdit {
				t.Fatalf("value=%q want %q", got, tc.wantEdit)
			}
		})

		t.Run("TextField/"+tc.name, func(t *testing.T) {
			f := NewTextField("paste", ts)
			f.SetValue(tc.initial)
			f.Layout(LayoutContext{}, Rect{W: 40, H: 1})
			if tc.paste != "" {
				f.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: tc.paste}))
			}
			if got := f.Value(); got != tc.wantField {
				t.Fatalf("value=%q want %q", got, tc.wantField)
			}
		})

		t.Run("TextArea/"+tc.name, func(t *testing.T) {
			ta := NewTextArea("paste", ts, true)
			ta.SetValue(tc.initial)
			ta.Layout(LayoutContext{}, Rect{W: 40, H: 5})
			if tc.paste != "" {
				ta.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: tc.paste}))
			}
			if got := ta.Value(); got != tc.wantArea {
				t.Fatalf("value=%q want %q", got, tc.wantArea)
			}
		})
	}
}

func TestPasteChordAcceptsWindowsControlKeyShapes(t *testing.T) {
	for _, test := range []struct {
		name string
		key  tea.KeyPressMsg
		want bool
	}{
		{"ctrl", tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, true},
		{"ctrl shift uppercase", tea.KeyPressMsg{Code: 'V', Mod: tea.ModCtrl | tea.ModShift}, true},
		{"super", tea.KeyPressMsg{Code: 'v', Mod: tea.ModSuper}, true},
		{"ctrl alt reserved", tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModAlt}, false},
		{"unmodified", tea.KeyPressMsg{Code: 'v'}, false},
		{"other key", tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsPasteChord(test.key); got != test.want {
				t.Fatalf("IsPasteChord(%s) = %v, want %v", test.key.String(), got, test.want)
			}
		})
	}
}
