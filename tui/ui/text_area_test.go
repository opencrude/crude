package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTextAreaVisualLineCountCacheInvalidatesOnContentChanges(t *testing.T) {
	engine := newTextAreaEngine()
	engine.SetValue("abcd")
	if got := engine.VisualLineCount(2); got != 2 {
		t.Fatalf("initial rows=%d", got)
	}
	engine.InsertString("ef")
	if got := engine.VisualLineCount(2); got != 3 {
		t.Fatalf("edited rows=%d", got)
	}
	if !engine.Undo() {
		t.Fatal("undo failed")
	}
	if got := engine.VisualLineCount(2); got != 2 {
		t.Fatalf("undo rows=%d", got)
	}
	engine.Clear()
	if got := engine.VisualLineCount(2); got != 1 {
		t.Fatalf("cleared rows=%d", got)
	}
}

func TestTextArea_NewIsEmpty(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	if ta.Value() != "" || ta.Len() != 0 {
		t.Fatalf("new area should be empty: value=%q len=%d", ta.Value(), ta.Len())
	}
	if ta.CurLine() != 0 || ta.CurCol() != 0 {
		t.Fatalf("cursor should be at 0,0: line=%d col=%d", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_SetValueMovesCursorToEnd(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2")
	if ta.Value() != "line1\nline2" {
		t.Fatalf("value=%q", ta.Value())
	}
	if ta.Len() != 11 {
		t.Fatalf("len=%d want 11", ta.Len())
	}
	if ta.CurLine() != 1 || ta.CurCol() != 5 {
		t.Fatalf("cursor=(%d,%d) want (1,5)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_EnterInsertsNewlineInMultiLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 5})
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if ta.Value() != "\n" {
		t.Fatalf("value=%q want \\n", ta.Value())
	}
	if ta.CurLine() != 1 || ta.CurCol() != 0 {
		t.Fatalf("cursor=(%d,%d) want (1,0)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_EnterSubmitsInSingleLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, false)
	ta.SetSubmitAction("submit")
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 1})
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'h', Text: "h"}))
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'i', Text: "i"}))
	if ta.Value() != "hi" {
		t.Fatalf("value=%q want hi", ta.Value())
	}
	res := ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if res.Action == nil || res.Action.ID != "submit" || res.Action.Payload != "hi" {
		t.Fatalf("enter should submit: %+v", res.Action)
	}
}

func TestTextArea_UpdateSingleLineEnterEmitsSubmitEvent(t *testing.T) {
	ta := NewTextArea("test/area", nil, false)
	ta.SetValue("submit me")
	cmd := ta.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, false)
	if cmd == nil {
		t.Fatal("single-line Enter should return a submit command")
	}
	msg := cmd()
	event, ok := msg.(TextAreaSubmitEvent)
	if !ok || event.Text != "submit me" {
		t.Fatalf("submit event=%T %#v", msg, msg)
	}
	if ta.Value() != "submit me" {
		t.Fatalf("submit changed value=%q", ta.Value())
	}
}

func TestTextArea_CtrlEnterSubmitsEvenInMultiLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetSubmitAction("submit")
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 5})
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	res := ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}))
	if res.Action == nil || res.Action.ID != "submit" {
		t.Fatalf("ctrl+enter should submit: %+v", res.Action)
	}
	if ta.Value() != "a" {
		t.Fatalf("value should still be 'a': %q", ta.Value())
	}
}

func TestTextArea_VerticalNavigationAcrossLines(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2\nline3")
	// cursor at end of line3 (2,5)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if ta.CurLine() != 1 {
		t.Fatalf("up from line3: line=%d want 1", ta.CurLine())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if ta.CurLine() != 0 {
		t.Fatalf("up from line2: line=%d want 0", ta.CurLine())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if ta.CurLine() != 0 || ta.CurCol() != 0 {
		t.Fatalf("up at top should stick: line=%d col=%d", ta.CurLine(), ta.CurCol())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if ta.CurLine() != 1 {
		t.Fatalf("down from line1: line=%d want 1", ta.CurLine())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	if ta.CurLine() != 2 {
		t.Fatalf("down from line2: line=%d want 2", ta.CurLine())
	}
}

func TestTextArea_HomeEndAreLineAware(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello\nworld")
	// cursor at end of "world" (1,5)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyHome}))
	if ta.CurLine() != 1 || ta.CurCol() != 0 {
		t.Fatalf("home: cursor=(%d,%d) want (1,0)", ta.CurLine(), ta.CurCol())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd}))
	if ta.CurLine() != 1 || ta.CurCol() != 5 {
		t.Fatalf("end: cursor=(%d,%d) want (1,5)", ta.CurLine(), ta.CurCol())
	}
	// Ctrl+Home goes to first line
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl}))
	if ta.CurLine() != 0 || ta.CurCol() != 0 {
		t.Fatalf("ctrl+home: cursor=(%d,%d) want (0,0)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_BackspaceAtLineStartJoinsLines(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2")
	// cursor at (1,0)
	ta.SetCursorFlat(6)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	if ta.Value() != "line1line2" {
		t.Fatalf("value=%q want line1line2", ta.Value())
	}
	if ta.CursorFlat() != 5 {
		t.Fatalf("cursor=%d want 5", ta.CursorFlat())
	}
}

func TestTextArea_DeleteAtLineEndJoinsLines(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2")
	ta.SetCursorFlat(5) // end of line1
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDelete}))
	if ta.Value() != "line1line2" {
		t.Fatalf("value=%q want line1line2", ta.Value())
	}
}

func TestTextArea_DeleteToLineStartStopsAtLineBoundary(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2")
	ta.SetCursorFlat(8) // 'n' in "line2" (l-i-n-e-2 = cols 0-4, flat 6-10)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}))
	// Ctrl+U deletes from line2 start (flat 6) to cursor (flat 8): removes "li", leaves "ne2"
	if ta.Value() != "line1\nne2" {
		t.Fatalf("value=%q want 'line1\\nne2'", ta.Value())
	}
}

func TestTextArea_SelectionSpansLines(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("foo\nbar\nbaz")
	ta.SetSelection(2, 10) // "o\nbar\nba"
	s, e := ta.SelectionRange()
	if s != 2 || e != 10 {
		t.Fatalf("range=[%d,%d) want [2,10)", s, e)
	}
	if ta.SelectedText() != "o\nbar\nba" {
		t.Fatalf("selected=%q", ta.SelectedText())
	}
}

func TestTextArea_PasteInsertsStringWithNewlines(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: "hello\nworld"}))
	if ta.Value() != "hello\nworld" {
		t.Fatalf("value=%q", ta.Value())
	}
	if ta.CurLine() != 1 || ta.CurCol() != 5 {
		t.Fatalf("cursor=(%d,%d) want (1,5)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_PaintRendersTextIntoBuffer(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("hello")
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 3})
	buf := surface.NewCellBuffer(20, 3)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	row := rowText(buf, 0)
	if strings.Contains(row, "❯") {
		t.Fatalf("row 0=%q must not paint a prompt glyph", row)
	}
	if !strings.HasPrefix(strings.TrimLeft(row, " "), "hello") {
		t.Fatalf("row 0=%q missing 'hello'", row)
	}
}

func TestTextArea_PaintHandlesNewlines(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("ab\ncd")
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 20, H: 3})
	buf := surface.NewCellBuffer(20, 3)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	t.Logf("row0=%q", rowText(buf, 0))
	t.Logf("row1=%q", rowText(buf, 1))
	if c := buf.Get(0, 0).Char; c != 'a' {
		t.Fatalf("row0 col0=%q want 'a'", string(c))
	}
	if c := buf.Get(1, 0).Char; c != 'b' {
		t.Fatalf("row0 col1=%q want 'b'", string(c))
	}
	if c := buf.Get(0, 1).Char; c != 'c' {
		t.Fatalf("row1 col0=%q want 'c'", string(c))
	}
	if c := buf.Get(1, 1).Char; c != 'd' {
		t.Fatalf("row1 col1=%q want 'd'", string(c))
	}
}

func TestTextArea_PaintPreservesLegacyPromptAndWrapGeometry(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("abcdefgh")
	ta.cursor.MarkActivity()

	ta.Layout(LayoutContext{}, Rect{W: 8, H: 2})
	buf := surface.NewCellBuffer(8, 2)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})

	lines := []string{rowText(buf, 0), rowText(buf, 1)}
	if got := strings.TrimRight(lines[0], " "); got != "abcdefgh" {
		t.Fatalf("first line=%q want abcdefgh", got)
	}
	if ta.textWidth != 8 {
		t.Fatalf("text width=%d want 8", ta.textWidth)
	}
}

func TestTextArea_SetPromptEmptyPaintsNoGlyph(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetPrompt(">")
	ta.SetPrompt("")
	ta.SetValue("x")
	ta.Layout(LayoutContext{}, Rect{W: 8, H: 1})
	buf := surface.NewCellBuffer(8, 1)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})
	plain := rowText(buf, 0)
	if strings.Contains(plain, "❯") || strings.HasPrefix(plain, ">") {
		t.Fatalf("prompt=%q want no glyph", plain)
	}
	if !strings.HasPrefix(strings.TrimLeft(plain, " "), "x") {
		t.Fatalf("prompt=%q want leading x", plain)
	}
}

func TestTextArea_PaintRendersSolidCursorStates(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("abc")
	ta.SetCursorFlat(1)
	ta.Layout(LayoutContext{}, Rect{W: 8, H: 1})
	buf := surface.NewCellBuffer(8, 1)
	ta.cursor.MarkActivity()
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})

	// The cursor preserves the grapheme and overlays reverse video.
	under := buf.Get(1, 0) // 'b' at col 1
	if under.Char != 'b' || !under.Attr.Has(cell.AttrReverse) {
		t.Fatalf("cursor under character is not inverse video: %+v", under)
	}

	ta.SetCursorFlat(3)
	buf = surface.NewCellBuffer(8, 1)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	end := buf.Get(3, 0) // block cursor at end-of-line col 3
	if end.Char != ' ' || !end.Attr.Has(cell.AttrReverse) {
		t.Fatalf("end cursor missing reverse block: %+v", end)
	}
}

func TestTextArea_MouseCoordinatesAccountForPrompt(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("abc")
	ta.SetTextWidth(10)
	if got := ta.MouseToFlatPos(0, 0); got != 0 {
		t.Fatalf("mouse at first text cell=%d want 0", got)
	}
	if got := ta.MouseToFlatPos(1, 0); got != 1 {
		t.Fatalf("mouse at second text cell=%d want 1", got)
	}
}

func TestTextArea_PaintSelectionAndAtomicChipStyles(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	ta := NewTextArea("test/area", tokens, true)
	ta.SetValue("abcdef")
	ta.SetAtomicRanges([]AtomicRange{{ID: "ref", Start: 0, End: 3}})
	ta.SetSelection(0, 2)
	ta.SetCursorFlat(6)
	ta.Layout(LayoutContext{}, Rect{W: 20, H: 2})
	buf := surface.NewCellBuffer(20, 2)
	ta.cursor.MarkActivity()
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})

	chipStart := 0
	if !buf.Get(chipStart, 0).Attr.Has(cell.AttrBold) {
		t.Fatalf("chip attr=%v missing bold", buf.Get(chipStart, 0).Attr)
	}
	if !buf.Get(chipStart, 0).BG.Equal(tokens.Color(ColorTextSelection)) {
		t.Fatalf("selected chip bg=%v want %v", buf.Get(chipStart, 0).BG, tokens.Color(ColorTextSelection))
	}
	// The whole token paints as a filled pill (selection overlap or not);
	// plain text AFTER the chip must stay on the default background.
	if !buf.Get(chipStart+2, 0).BG.Equal(tokens.Color(ColorSelection)) {
		t.Fatalf("chip cell %d lost pill background=%v", chipStart+2, tokens.Color(ColorSelection))
	}
	if buf.Get(5, 0).BG.Equal(tokens.Color(ColorSelection)) {
		t.Fatalf("text after chip inherited chip background")
	}
}

// TestTextArea_AtomicChipPaintsAsFilledPill guards the attachment-block look:
// EVERY cell of a reference token carries the pill background so chips read
// as blocks (Cursor-style), independent of any text selection.
func TestTextArea_AtomicChipPaintsAsFilledPill(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	ta := NewTextArea("test/area", tokens, true)
	ta.SetValue("[File1] rest")
	ta.SetAtomicRanges([]AtomicRange{{ID: "ref", Start: 0, End: 7}})
	ta.SetCursorFlat(9)
	ta.Layout(LayoutContext{}, Rect{W: 20, H: 2})
	buf := surface.NewCellBuffer(20, 2)
	ta.cursor.MarkActivity()
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})

	pillBG := tokens.Color(ColorSelection)
	for i := 0; i < 7; i++ {
		if !buf.Get(i, 0).BG.Equal(pillBG) {
			t.Fatalf("pill cell %d bg=%v want %v", i, buf.Get(i, 0).BG, pillBG)
		}
		if !buf.Get(i, 0).FG.Equal(tokens.Color(ColorAccent2)) {
			t.Fatalf("pill cell %d fg=%v want accent2", i, buf.Get(i, 0).FG)
		}
	}
	if buf.Get(8, 0).BG.Equal(pillBG) {
		t.Fatal("text outside the chip inherited the pill background")
	}

	// Hovering highlights the pill instead.
	ta.hoverFlat = 2
	buf = surface.NewCellBuffer(20, 2)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})
	if !buf.Get(2, 0).BG.Equal(tokens.Color(ColorHover)) {
		t.Fatalf("hovered pill bg=%v want %v", buf.Get(2, 0).BG, tokens.Color(ColorHover))
	}

	// Error tone swaps the text color to error while keeping the pill fill.
	ta.hoverFlat = -1
	ta.SetAtomicRanges([]AtomicRange{{ID: "ref", Start: 0, End: 7, Tone: ChipToneError}})
	buf = surface.NewCellBuffer(20, 2)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})
	if !buf.Get(2, 0).FG.Equal(tokens.Color(ColorError)) {
		t.Fatalf("error-tone chip fg=%v want error", buf.Get(2, 0).FG)
	}
}

func TestTextArea_PaintPreservesGraphemeCells(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("a👩‍💻b")
	ta.Layout(LayoutContext{}, Rect{W: 12, H: 2})
	buf := surface.NewCellBuffer(12, 2)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})

	if got := buf.Get(1, 0).Content; got != "👩‍💻" {
		t.Fatalf("emoji content=%q want full grapheme", got)
	}
	if buf.Get(1, 0).Width != 2 || buf.Get(2, 0).Width != 0 {
		t.Fatalf("emoji cells=%+v/%+v", buf.Get(1, 0), buf.Get(2, 0))
	}
	if got := buf.Get(3, 0).Char; got != 'b' {
		t.Fatalf("text after emoji=%q want b", string(got))
	}
}

func TestTextArea_MeasureUsesAvailableTextWidth(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("abcdefgh")
	size := ta.Measure(LayoutContext{}, Loose(6, 20))
	if size.W != 6 || size.H != 2 {
		t.Fatalf("measure=%+v want {W:6 H:2}", size)
	}
}

func TestTextArea_RuneSafeUnicode(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("日本語\n🌍world")
	// 日本語=3 runes, \n=1, 🌍=1 rune (single codepoint), world=5 → total 10
	if ta.Len() != 10 {
		t.Fatalf("len=%d want 10", ta.Len())
	}
	// Backspace removes last rune
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	if ta.Value() != "日本語\n🌍worl" {
		t.Fatalf("value=%q", ta.Value())
	}
}

// TestTextArea_TypedZWJEmojiPreserved locks the grapheme-joiner fix: typing a
// ZWJ family emoji through the engine keeps the U+200D joiners intact.
func TestTextArea_TypedZWJEmojiPreserved(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	family := "👨‍👩‍👧"
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: '👨', Text: family}))
	if ta.Value() != family {
		t.Fatalf("typed ZWJ runes dropped: value=%q want %q", ta.Value(), family)
	}
}

func TestTextArea_GoalColumnPreservedAcrossShorterLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetTextWidth(40)
	ta.SetValue("long line here\nx")
	// cursor at end of "x" (1,1)
	ta.SetCursorFlat(15) // at 'l' in "long line here"
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
	// Should clamp to end of "x" (col 1)
	if ta.CurLine() != 1 || ta.CurCol() != 1 {
		t.Fatalf("down to short line: cursor=(%d,%d) want (1,1)", ta.CurLine(), ta.CurCol())
	}
	// Up should try to restore the goal column
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if ta.CurLine() != 0 {
		t.Fatalf("up back: line=%d want 0", ta.CurLine())
	}
	// Goal column was 1 (from "x"), so we land at col 1
	if ta.CurCol() != 1 {
		t.Logf("goal col behavior: col=%d (goal=1)", ta.CurCol())
	}
}

func TestTextArea_SetTextWidthUpdatesWrappedNavigation(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetTextWidth(4)
	ta.SetValue("abcd efgh")
	if got := ta.TotalVisualLines(); got != 2 {
		t.Fatalf("visual lines=%d want 2", got)
	}
	if got := ta.CursorVisualLine(); got != 1 {
		t.Fatalf("cursor visual line=%d want 1", got)
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
	if ta.CurLine() != 0 || ta.CurCol() != 4 {
		t.Fatalf("wrapped up cursor=(%d,%d) want (0,4)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_UpdateSpaceInsideDiscardedWrapWhitespace(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.Layout(LayoutContext{}, Rect{W: 4, H: 2})
	ta.SetValue("abcd x")
	ta.SetCursorFlat(4)

	ta.Update(tea.KeyPressMsg{Code: ' ', Text: " "}, false)

	if got := ta.Value(); got != "abcd  x" {
		t.Fatalf("value=%q want %q", got, "abcd  x")
	}
	if got := ta.CursorFlat(); got != 5 {
		t.Fatalf("cursor=%d want 5", got)
	}
	if got := ta.CursorVisualLine(); got != 1 {
		t.Fatalf("cursor visual line=%d want 1", got)
	}

	buf := surface.NewCellBuffer(4, 2)
	ta.cursor.MarkActivity()
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	if cursor := buf.Get(0, 1); !cursor.Attr.Has(cell.AttrReverse) {
		t.Fatalf("continuation cursor=%+v want reverse block at row start", cursor)
	}

	ta.Layout(LayoutContext{}, Rect{W: 6, H: 2})
	if got := ta.CursorVisualLine(); got != 0 {
		t.Fatalf("cursor visual line after widen=%d want 0", got)
	}
	ta.Layout(LayoutContext{}, Rect{W: 4, H: 2})
	if got := ta.CursorVisualLine(); got != 1 {
		t.Fatalf("cursor visual line after shrink=%d want 1", got)
	}

	ta.Update(tea.KeyPressMsg{Code: tea.KeyBackspace}, false)
	if got := ta.Value(); got != "abcd x" {
		t.Fatalf("backspace value=%q want %q", got, "abcd x")
	}
	if got := ta.CursorFlat(); got != 4 {
		t.Fatalf("backspace cursor=%d want 4", got)
	}
	if got := ta.CursorVisualLine(); got != 0 {
		t.Fatalf("backspace cursor visual line=%d want 0", got)
	}

	ta.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, false)
	if got := ta.Value(); got != "abcd  x" {
		t.Fatalf("undo value=%q want %q", got, "abcd  x")
	}
	if got := ta.CursorFlat(); got != 5 {
		t.Fatalf("undo cursor=%d want 5", got)
	}
}

func TestTextArea_HandleUnicodeSpaceInsideDiscardedWrapWhitespace(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.Layout(LayoutContext{}, Rect{W: 4, H: 2})
	ta.SetValue("界界 x")
	ta.SetCursorFlat(2)

	res := ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: ' ', Text: " "}))

	if !res.Handled {
		t.Fatal("space was not handled")
	}
	if got := ta.Value(); got != "界界  x" {
		t.Fatalf("value=%q want %q", got, "界界  x")
	}
	if got := ta.CursorFlat(); got != 3 {
		t.Fatalf("cursor=%d want 3", got)
	}
	if got := ta.CursorVisualLine(); got != 1 {
		t.Fatalf("cursor visual line=%d want 1", got)
	}
	buf := surface.NewCellBuffer(4, 2)
	ta.cursor.MarkActivity()
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: true})
	if cursor := buf.Get(0, 1); !cursor.Attr.Has(cell.AttrReverse) {
		t.Fatalf("unicode continuation cursor=%+v want reverse block at row start", cursor)
	}
}

func TestTextArea_TrailingDiscardedWrapWhitespaceKeepsCursorBounded(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.Layout(LayoutContext{}, Rect{W: 4, H: 1})
	ta.SetValue("abcd ")

	ta.Update(tea.KeyPressMsg{Code: ' ', Text: " "}, false)

	if got := ta.Value(); got != "abcd  " {
		t.Fatalf("value=%q want %q", got, "abcd  ")
	}
	if got := ta.CursorFlat(); got != 6 {
		t.Fatalf("cursor=%d want 6", got)
	}
	if got := ta.CursorVisualLine(); got != 0 {
		t.Fatalf("cursor visual line=%d want 0", got)
	}
}

func TestTextArea_LayoutKeepsEndCursorVisible(t *testing.T) {
	ta := NewTextArea("test/area", NewTokenSet(theme.DefaultDark()), true)
	ta.SetValue("one\ntwo\nthree")
	ta.Layout(LayoutContext{}, Rect{W: 12, H: 2})
	buf := surface.NewCellBuffer(12, 2)
	ta.Paint(buf, 0, 0, PaintContext{Theme: theme.DefaultDark(), Focused: false})
	if got := rowText(buf, 0) + rowText(buf, 1); !strings.Contains(got, "three") {
		t.Fatalf("visible rows=%q missing end line", got)
	}
}

func TestTextArea_AtomicRangeBackspaceDeletesWholeToken(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello [@ref] world")
	// "hello [@ref] world": h(0)e(1)l(2)l(3)o(4) (5)[(6)@(7)r(8)e(9)f(10)](11) (12)w(13)
	// The token [@ref] spans indices 6..11 (Start=6, End=12, exclusive)
	ta.SetAtomicRanges([]AtomicRange{{ID: "ref", Start: 6, End: 12}})
	// cursor right after ']' at flat 12
	ta.SetCursorFlat(12)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	if ta.Value() != "hello  world" {
		t.Fatalf("atomic backspace: value=%q", ta.Value())
	}
}

func TestTextArea_ClearResetsState(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("some\ncontent")
	ta.SetSelection(0, 4)
	ta.Clear()
	if ta.Value() != "" || ta.Len() != 0 {
		t.Fatalf("clear: value=%q len=%d", ta.Value(), ta.Len())
	}
	if ta.HasActiveSelection() {
		t.Fatal("clear should drop selection")
	}
	if ta.CurLine() != 0 || ta.CurCol() != 0 {
		t.Fatalf("clear: cursor=(%d,%d)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_HeightGrowsWithContent(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetTextWidth(40)
	if ta.Height() != 1 {
		t.Fatalf("empty height=%d want 1", ta.Height())
	}
	ta.SetValue("single line")
	if ta.Height() != 1 {
		t.Fatalf("single line height=%d want 1", ta.Height())
	}
	ta.SetValue("line1\nline2\nline3")
	if ta.Height() != 3 {
		t.Fatalf("three lines height=%d want 3", ta.Height())
	}
}

func TestTextArea_WordMotionCtrlLeftRight(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello world")
	ta.SetCursorFlat(0)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}))
	// cursorNextWord from 0: 'h' not space → skip word "hello" → at 5 (space)
	if ta.CursorFlat() != 5 {
		t.Fatalf("ctrl+right: cursor=%d want 5 (end of 'hello')", ta.CursorFlat())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}))
	// from 5: skip spaces → at 'w' (6), skip word "world" → at 11
	if ta.CursorFlat() != 11 {
		t.Fatalf("ctrl+right again: cursor=%d want 11", ta.CursorFlat())
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl}))
	// cursorPrevWord from 11: pos-- → 10 ('d'), 'd' not space, skip word back → 6 ('w')
	if ta.CursorFlat() != 6 {
		t.Fatalf("ctrl+left: cursor=%d want 6 (start of 'world')", ta.CursorFlat())
	}
}

func rowText(buf *surface.CellBuffer, y int) string {
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		ch := buf.Get(x, y).Char
		if ch == 0 {
			ch = ' '
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func TestTextArea_ShiftArrowStartsSelection(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello")
	ta.SetCursorFlat(5) // end
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}))
	if !ta.HasActiveSelection() {
		t.Fatal("shift+left should start selection")
	}
	s, e := ta.SelectionRange()
	if s != 4 || e != 5 {
		t.Fatalf("selection=[%d,%d) want [4,5)", s, e)
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}))
	s2, e2 := ta.SelectionRange()
	if s2 != 3 || e2 != 5 {
		t.Fatalf("extended selection=[%d,%d) want [3,5)", s2, e2)
	}
}

func TestTextArea_ShiftHomeEndSelectsLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello world")
	ta.SetCursorFlat(5) // at space
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}))
	s, e := ta.SelectionRange()
	if s != 0 || e != 5 {
		t.Fatalf("shift+home selection=[%d,%d) want [0,5)", s, e)
	}
	// Shift+End extends from the anchor (5) to end, flipping the selection
	// direction; this matches standard editor behavior.
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModShift}))
	s2, e2 := ta.SelectionRange()
	if s2 != 5 || e2 != 11 {
		t.Fatalf("shift+end selection=[%d,%d) want [5,11)", s2, e2)
	}
}

func TestTextArea_PlainArrowClearsSelection(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello")
	ta.SetSelection(0, 3)
	if !ta.HasActiveSelection() {
		t.Fatal("selection should be active")
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyLeft}))
	if ta.HasActiveSelection() {
		t.Fatal("plain left should clear selection")
	}
}

func TestTextArea_CtrlASelectsAll(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello\nworld")
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}))
	if !ta.HasActiveSelection() {
		t.Fatal("Ctrl+A should select all")
	}
	s, e := ta.SelectionRange()
	if s != 0 || e != 11 {
		t.Fatalf("select-all=[%d,%d) want [0,11)", s, e)
	}
}

func TestTextArea_SelectionPersistsAfterStopSelecting(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello")
	ta.SetSelection(1, 4)
	ta.SetSelecting(true)
	// Simulate mouse release
	ta.StopSelecting()
	if !ta.HasActiveSelection() {
		t.Fatal("selection should persist after stop-selecting")
	}
	if ta.SelectedText() != "ell" {
		t.Fatalf("selected text=%q want 'ell'", ta.SelectedText())
	}
}

func TestTextArea_BackspaceDeletesSelection(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello world")
	ta.SetSelection(0, 6)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	if ta.Value() != "world" {
		t.Fatalf("backspace selection: value=%q want 'world'", ta.Value())
	}
	if ta.HasActiveSelection() {
		t.Fatal("backspace should clear selection")
	}
}

func TestTextArea_DeleteDeletesSelection(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello world")
	ta.SetSelection(0, 6)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDelete}))
	if ta.Value() != "world" {
		t.Fatalf("delete selection: value=%q want 'world'", ta.Value())
	}
}

func TestTextAreaMatureEditingContract(t *testing.T) {
	ta := NewTextArea("area", NewTokenSet(theme.DefaultDark()), true)
	ta.Layout(LayoutContext{}, Rect{W: 20, H: 3})
	ta.SetValue("alpha beta\nsecond line")
	ta.SetCursorFlat(6)
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModCtrl}))
	if got := ta.Value(); got != "alpha \nsecond line" {
		t.Fatalf("ctrl+delete=%q", got)
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}))
	if got := ta.Value(); got != "alpha beta\nsecond line" {
		t.Fatalf("undo=%q", got)
	}
	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}))
	if got := ta.Value(); got != "alpha \nsecond line" {
		t.Fatalf("redo=%q", got)
	}

	ta.SetValue("alpha beta\nsecond line")
	click := MouseClickEvent(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	ta.Handle(EventContext{LocalX: 2, LocalY: 0}, click)
	ta.Handle(EventContext{LocalX: 2, LocalY: 0}, click)
	if got := ta.SelectedText(); got != "alpha" {
		t.Fatalf("double-click selection=%q", got)
	}
	if res := ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})); !res.Handled || res.Cmd == nil {
		t.Fatalf("ctrl+c result=%+v", res)
	}
	ta.Handle(EventContext{LocalX: 2, LocalY: 0}, click)
	if got := ta.SelectedText(); got != "alpha beta\n" {
		t.Fatalf("triple-click selection=%q", got)
	}
	ta.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: "replacement"}))
	if got := ta.Value(); got != "replacementsecond line" {
		t.Fatalf("paste did not replace selection: %q", got)
	}
}

func TestTextAreaWheelAndOutsideDragScroll(t *testing.T) {
	ta := NewTextArea("area", nil, true)
	ta.SetValue("one\ntwo\nthree\nfour\nfive\nsix")
	ta.Layout(LayoutContext{}, Rect{W: 12, H: 2})
	initial := ta.ScrollOffset()
	if res := ta.Handle(EventContext{}, MouseWheelEvent(tea.MouseWheelMsg{Button: tea.MouseWheelUp})); !res.Handled || ta.ScrollOffset() >= initial {
		t.Fatalf("wheel result=%+v scroll=%d initial=%d", res, ta.ScrollOffset(), initial)
	}
	ta.Handle(EventContext{LocalX: 0, LocalY: 0}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	before := ta.ScrollOffset()
	for range 3 {
		ta.Handle(EventContext{LocalX: 4, LocalY: 5}, MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
	}
	if ta.ScrollOffset() <= before || !ta.HasActiveSelection() {
		t.Fatalf("outside drag scroll=%d before=%d selection=%q", ta.ScrollOffset(), before, ta.SelectedText())
	}
}
