package ui

import (
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// TestFilterFieldUnicodeEmojiZWJWideEditing proves the shared TextEdit engine
// keeps the filter value intact across paste, insert, and delete for
// multi-byte, combining, ZWJ-cluster, and wide runes (Phase 6 exit criterion
// 2). Cursor and length are rune offsets, never bytes.
func TestFilterFieldUnicodeEmojiZWJWideEditing(t *testing.T) {
	f := NewFilterField("filter", nil)
	f.Layout(LayoutContext{}, Rect{W: 60, H: 1})

	family := "👨‍👩‍👧"          // 5 runes (3 emoji + 2 ZWJ)
	combining := "e\u0301"     // 2 runes (base + combining acute)
	wide := "日"               // 1 rune, 2 cells wide
	want := "a" + family + combining + wide + "🚀"

	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: '👨', Text: family}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'e', Text: combining}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: '日', Text: wide}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: '🚀', Text: "🚀"}))

	if f.Value() != want {
		t.Fatalf("value=%q want %q", f.Value(), want)
	}
	if !utf8.ValidString(f.Value()) {
		t.Fatalf("value is not valid UTF-8: %q", f.Value())
	}
	wantLen := 1 + len([]rune(family)) + len([]rune(combining)) + 1 + 1
	if f.Edit().Len() != wantLen {
		t.Fatalf("Len=%d want %d runes", f.Edit().Len(), wantLen)
	}
	if f.Edit().Cursor() != wantLen {
		t.Fatalf("Cursor=%d want %d (rune index)", f.Edit().Cursor(), wantLen)
	}

	// Backspace removes exactly one rune at a time, never a byte.
	for _, step := range []string{
		"a" + family + combining + wide, // drop 🚀
		"a" + family + combining,        // drop 日
		"a" + family + "e",              // drop combining acute, base stays
		"a" + family,                    // drop e
	} {
		f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
		if f.Value() != step {
			t.Fatalf("after backspace value=%q want %q", f.Value(), step)
		}
		if !utf8.ValidString(f.Value()) {
			t.Fatalf("value corrupted after backspace: %q", f.Value())
		}
	}
}

// TestFilterFieldCursorIsRuneOffset proves cursor navigation and insertion use
// rune offsets, so cursor position can never be confused with a byte offset
// (Phase 6 exit criterion 3).
func TestFilterFieldCursorIsRuneOffset(t *testing.T) {
	f := NewFilterField("filter", nil)
	f.SetValue("héllo🚀")

	f.Edit().MoveHome(false)
	f.Edit().SetCursor(1, false)
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'X', Text: "X"}))
	want := "hXéllo🚀"
	if f.Value() != want {
		t.Fatalf("value=%q want %q", f.Value(), want)
	}
	// The cursor sits on the rune boundary after the X (2 runes).
	if f.Edit().Cursor() != 2 {
		t.Fatalf("cursor=%d want 2 runes", f.Edit().Cursor())
	}
}

// TestFilterFieldNotifyOnEditAndEscClear proves the collection link: OnChanged
// fires on every edit and on Esc-to-clear, and not at all for programmatic
// SetValue (used by screens restoring model state).
func TestFilterFieldNotifyOnEditAndEscClear(t *testing.T) {
	var seen []string
	f := NewFilterField("filter", nil)
	f.OnChanged(func(v string) { seen = append(seen, v) })
	f.Layout(LayoutContext{}, Rect{W: 30, H: 1})

	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'b', Text: "b"}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	if got := joinStrings(seen); got != "a,ab,a" {
		t.Fatalf("notifications=%q want a,ab,a", got)
	}

	f.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: "xyz"}))
	if got := joinStrings(seen); got != "a,ab,a,axyz" {
		t.Fatalf("paste notification=%q want a,ab,a,axyz", got)
	}

	// Esc clears the non-empty value and notifies once.
	res := f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if !res.Handled {
		t.Fatal("Esc on non-empty value should be handled (clear)")
	}
	if got := joinStrings(seen); got != "a,ab,a,axyz," {
		t.Fatalf("esc-clear notification=%q want trailing empty", got)
	}
	if f.Value() != "" {
		t.Fatalf("value after Esc=%q want empty", f.Value())
	}

	// Esc on an empty value passes through (owner closes/cancels).
	res = f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if res.Handled {
		t.Fatal("Esc on empty value should pass through")
	}

	// SetValue never notifies.
	f.SetValue("programmatic")
	if got := joinStrings(seen); got != "a,ab,a,axyz," {
		t.Fatalf("SetValue must not notify: %q", got)
	}
}

// TestFilterFieldEditNotifiesExactlyOncePerKey guards against double-sync when
// a surface also re-applies the value after Handle (the old ad-hoc filter
// pattern).
func TestFilterFieldEditNotifiesExactlyOncePerKey(t *testing.T) {
	count := 0
	f := NewFilterField("filter", nil)
	f.OnChanged(func(string) { count++ })
	f.Layout(LayoutContext{}, Rect{W: 30, H: 1})

	for _, r := range []rune("abc") {
		f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: r, Text: string(r)}))
	}
	if count != 3 {
		t.Fatalf("OnChanged fired %d times, want 3", count)
	}
}

func joinStrings(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
