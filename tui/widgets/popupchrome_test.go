package widgets

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

// stripForVisible returns the visible (ANSI-stripped) form of an
// ANSI-styled string. Used to assert on rendered output without
// caring about exact escape codes.
func stripForVisible(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if r == 0x1b {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestPopupChromeLines_Shape(t *testing.T) {
	th := theme.DefaultDark()
	cfg := PopupChromeConfig{
		Title:   "Test",
		Width:   30,
		Focused: false,
	}
	content := []string{
		"line one",
		"line two",
	}
	lines := PopupChromeLines(th, cfg, content)

	// Total height: top + 2 content + bottom = 4
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	// All lines should have the same visible width.
	w0 := rendering.VisibleWidth(lines[0])
	for i, l := range lines {
		if w := rendering.VisibleWidth(l); w != w0 {
			t.Errorf("line %d has visible width %d, want %d (line=%q)", i, w, w0, stripForVisible(l))
		}
	}
	if w0 != cfg.Width {
		t.Errorf("chrome width = %d, want %d", w0, cfg.Width)
	}

	// First line: ╭─ Test ─...─╮
	top := stripForVisible(lines[0])
	if !strings.HasPrefix(top, "╭─ Test ") {
		t.Errorf("top border doesn't start with '╭─ Test ': %q", top)
	}
	if !strings.HasSuffix(top, "╮") {
		t.Errorf("top border doesn't end with '╮': %q", top)
	}

	// Content lines: │  line one   │
	for i, l := range content {
		body := stripForVisible(lines[i+1])
		if !strings.HasPrefix(body, "│  ") {
			t.Errorf("content line %d doesn't start with '│  ': %q", i, body)
		}
		if !strings.HasSuffix(body, "│") {
			t.Errorf("content line %d doesn't end with '│': %q", i, body)
		}
		if !strings.Contains(body, l) {
			t.Errorf("content line %d doesn't contain %q: %q", i, l, body)
		}
	}

	// Last line: ╰─...─╯
	bottom := stripForVisible(lines[len(lines)-1])
	if !strings.HasPrefix(bottom, "╰") {
		t.Errorf("bottom doesn't start with '╰': %q", bottom)
	}
	if !strings.HasSuffix(bottom, "╯") {
		t.Errorf("bottom doesn't end with '╯': %q", bottom)
	}
}

func TestPopupChromeLines_FocusedBorder(t *testing.T) {
	th := theme.DefaultDark()

	unfocused := PopupChromeLines(th, PopupChromeConfig{Title: "X", Width: 20, Focused: false}, []string{""})[0]
	focused := PopupChromeLines(th, PopupChromeConfig{Title: "X", Width: 20, Focused: true}, []string{""})[0]

	// Both have the same shape but different colors. Verify the
	// title segment differs.
	if unfocused == focused {
		t.Fatal("focused and unfocused top borders are identical")
	}
}

func TestPopupChromeLines_EmptyTitle(t *testing.T) {
	th := theme.DefaultDark()
	lines := PopupChromeLines(th, PopupChromeConfig{Width: 20}, []string{""})
	top := stripForVisible(lines[0])
	// No title — just dashes between corners.
	if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
		t.Fatalf("empty title: bad shape: %q", top)
	}
	if strings.Contains(top, "─") == false {
		t.Fatalf("empty title: expected dashes in top: %q", top)
	}
}

func TestPopupChromeLines_TitleTruncation(t *testing.T) {
	th := theme.DefaultDark()
	// Very small width with long title — should not panic, should
	// truncate.
	lines := PopupChromeLines(th, PopupChromeConfig{Title: strings.Repeat("a", 100), Width: 20}, []string{""})
	w := rendering.VisibleWidth(lines[0])
	if w != 20 {
		t.Errorf("top border width = %d, want 20", w)
	}
}

func TestPopupChromeLines_TitleIcon(t *testing.T) {
	th := theme.DefaultDark()
	lines := PopupChromeLines(th, PopupChromeConfig{
		Title:     "Hint",
		TitleIcon: "i",
		Width:     30,
	}, []string{""})
	top := stripForVisible(lines[0])
	if !strings.Contains(top, "i Hint ") {
		t.Errorf("expected icon in top border: %q", top)
	}
}

func TestPopupChromeLines_ContentPadded(t *testing.T) {
	th := theme.DefaultDark()
	cfg := PopupChromeConfig{Title: "T", Width: 30, Focused: true}
	content := []string{"short"}
	lines := PopupChromeLines(th, cfg, content)
	body := stripForVisible(lines[1])
	// Should be exactly 30 visible chars.
	if w := rendering.VisibleWidth(lines[1]); w != 30 {
		t.Errorf("content width = %d, want 30 (line=%q)", w, body)
	}
	// And should have padding on both sides.
	if !strings.HasPrefix(body, "│  ") {
		t.Errorf("expected '│  ' prefix: %q", body)
	}
	if !strings.HasSuffix(body, "│") {
		t.Errorf("expected '│' suffix: %q", body)
	}
}

func TestPopupChromeLines_NarrowWidthClamped(t *testing.T) {
	th := theme.DefaultDark()
	// Width 4 is below the minimum 7. Should clamp to 7.
	lines := PopupChromeLines(th, PopupChromeConfig{Title: "T", Width: 4}, []string{""})
	for i, l := range lines {
		if w := rendering.VisibleWidth(l); w < 7 {
			t.Errorf("line %d width = %d, expected >= 7", i, w)
		}
	}
}

func TestPopupContentWidth_Height(t *testing.T) {
	if w := PopupContentWidth(20); w != 26 {
		t.Errorf("PopupContentWidth(20) = %d, want 26", w)
	}
	if h := PopupContentHeight(5); h != 7 {
		t.Errorf("PopupContentHeight(5) = %d, want 7", h)
	}
}

func TestPopupInputField_LabelValue(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupInputField(th, "Find", "hello", "", 2, true)
	visible := stripForVisible(line)
	if !strings.HasPrefix(visible, "Find:") {
		t.Errorf("expected 'Find:' prefix, got %q", visible)
	}
	// Cursor at position 2 splits "hello" into "he" + cursor + "llo".
	if !strings.Contains(visible, "he") {
		t.Errorf("expected 'he' in output, got %q", visible)
	}
	if !strings.Contains(visible, "llo") {
		t.Errorf("expected 'llo' in output, got %q", visible)
	}
}

func TestPopupInputField_NoLabel(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupInputField(th, "", "value", "", -1, false)
	visible := stripForVisible(line)
	if strings.HasPrefix(visible, ":") {
		t.Errorf("no-label input shouldn't have leading colon: %q", visible)
	}
	if !strings.Contains(visible, "value") {
		t.Errorf("expected 'value' in output, got %q", visible)
	}
}

func TestPopupInputField_CursorVisible(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupInputField(th, "Find", "ab", "", 1, true)
	// The cursor char is ▏. It should appear at position 1 of the
	// value, between 'a' and 'b'.
	visible := stripForVisible(line)
	if !strings.Contains(visible, "a▏b") {
		t.Errorf("expected cursor between a and b: %q", visible)
	}
}

func TestPopupInputField_CursorHiddenWhenUnfocused(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupInputField(th, "Find", "ab", "", 1, false)
	visible := stripForVisible(line)
	if strings.Contains(visible, "▏") {
		t.Errorf("cursor should not appear when unfocused: %q", visible)
	}
}

func TestPopupInputField_Placeholder(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupInputField(th, "Find", "", "search...", 0, false)
	visible := stripForVisible(line)
	if !strings.Contains(visible, "search...") {
		t.Errorf("expected placeholder in output: %q", visible)
	}
}

func TestPopupInputField_CursorClamped(t *testing.T) {
	th := theme.DefaultDark()
	// Cursor beyond end of value should clamp.
	line := PopupInputField(th, "Find", "hi", "", 99, true)
	visible := stripForVisible(line)
	if !strings.Contains(visible, "hi") {
		t.Errorf("expected 'hi' in output: %q", visible)
	}
}

func TestPopupToggle_ActiveVsInactive(t *testing.T) {
	th := theme.DefaultDark()
	on := PopupToggle(th, "Aa", true, false)
	off := PopupToggle(th, "Aa", false, false)
	if on == off {
		t.Fatal("active and inactive toggles produced identical output")
	}
	// Both should contain the glyph.
	if !strings.Contains(stripForVisible(on), "Aa") {
		t.Errorf("active toggle missing glyph: %q", on)
	}
	if !strings.Contains(stripForVisible(off), "Aa") {
		t.Errorf("inactive toggle missing glyph: %q", off)
	}
}

func TestPopupToggleRow(t *testing.T) {
	th := theme.DefaultDark()
	row := PopupToggleRow(th, "options:", []PopupToggleSpec{
		{Glyph: "Aa", Active: true},
		{Glyph: "\\b", Active: false},
		{Glyph: ".*", Active: false},
	})
	visible := stripForVisible(row)
	if !strings.Contains(visible, "options:") {
		t.Errorf("expected 'options:' prefix: %q", visible)
	}
	if !strings.Contains(visible, "Aa") {
		t.Errorf("expected Aa toggle: %q", visible)
	}
	if !strings.Contains(visible, "\\b") {
		t.Errorf("expected \\b toggle: %q", visible)
	}
	if !strings.Contains(visible, ".*") {
		t.Errorf("expected .* toggle: %q", visible)
	}
}

func TestPopupButton_FocusedUnfocused(t *testing.T) {
	th := theme.DefaultDark()
	focused := PopupButton(th, "Save", true, false)
	unfocused := PopupButton(th, "Save", false, false)
	if focused == unfocused {
		t.Fatal("focused and unfocused buttons are identical")
	}
	if !strings.Contains(stripForVisible(focused), "Save") {
		t.Errorf("focused button missing label: %q", focused)
	}
	if !strings.Contains(stripForVisible(unfocused), "Save") {
		t.Errorf("unfocused button missing label: %q", unfocused)
	}
}

func TestPopupButtonRow(t *testing.T) {
	th := theme.DefaultDark()
	row := PopupButtonRow(th, []PopupButtonSpec{
		{Label: "Yes", Focused: true},
		{Label: "No", Focused: false},
	})
	visible := stripForVisible(row)
	if !strings.Contains(visible, "Yes") {
		t.Errorf("expected 'Yes' in row: %q", visible)
	}
	if !strings.Contains(visible, "No") {
		t.Errorf("expected 'No' in row: %q", row)
	}
}

func TestPopupHintLine(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupHintLine(th, []HintSegment{
		{Key: "enter", Desc: "next"},
		{Key: "esc", Desc: "close"},
	})
	visible := stripForVisible(line)
	if !strings.Contains(visible, "enter") {
		t.Errorf("expected 'enter' in hint: %q", line)
	}
	if !strings.Contains(visible, "next") {
		t.Errorf("expected 'next' in hint: %q", line)
	}
	if !strings.Contains(visible, "esc") {
		t.Errorf("expected 'esc' in hint: %q", line)
	}
	if !strings.Contains(visible, "close") {
		t.Errorf("expected 'close' in hint: %q", line)
	}
	if !strings.Contains(visible, "·") {
		t.Errorf("expected '·' separator in hint: %q", line)
	}
}

func TestPopupSectionLabel(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupSectionLabel(th, "Code")
	visible := stripForVisible(line)
	if !strings.Contains(visible, "Code") {
		t.Errorf("expected 'Code' in section label: %q", line)
	}
}

func TestPopupDivider(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupDivider(th)
	visible := stripForVisible(line)
	if visible != "─" {
		t.Errorf("expected single dash, got %q", visible)
	}
}

func TestPopupEmptyLine(t *testing.T) {
	th := theme.DefaultDark()
	line := PopupEmptyLine(th)
	visible := stripForVisible(line)
	if visible != " " {
		t.Errorf("expected single space, got %q", visible)
	}
}

func TestPopupChromeLines_RealisticFindReplace(t *testing.T) {
	th := theme.DefaultDark()
	// Simulate a find/replace popup body.
	content := []string{
		PopupInputField(th, "Find", "hello", "type to search", 5, true),
		PopupInputField(th, "Replace", "world", "", 5, false),
		PopupEmptyLine(th),
		PopupHintLine(th, []HintSegment{
			{Key: "enter", Desc: "next"},
			{Key: "esc", Desc: "close"},
		}),
	}
	cfg := PopupChromeConfig{Title: "Find", TitleIcon: "🔍", Width: 50, Focused: true}
	lines := PopupChromeLines(th, cfg, content)

	if len(lines) != len(content)+2 {
		t.Fatalf("expected %d lines, got %d", len(content)+2, len(lines))
	}
	// All lines same width.
	w0 := rendering.VisibleWidth(lines[0])
	for i, l := range lines {
		if w := rendering.VisibleWidth(l); w != w0 {
			t.Errorf("line %d width = %d, want %d", i, w, w0)
		}
	}
	if w0 != 50 {
		t.Errorf("total width = %d, want 50", w0)
	}
}
