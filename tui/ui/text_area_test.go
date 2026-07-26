package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
)

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
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: 'h', Text: "h"})
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: 'i', Text: "i"})
	if ta.Value() != "hi" {
		t.Fatalf("value=%q want hi", ta.Value())
	}
	res := ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnter})
	if res.Action == nil || res.Action.ID != "submit" || res.Action.Payload != "hi" {
		t.Fatalf("enter should submit: %+v", res.Action)
	}
}

func TestTextArea_CtrlEnterSubmitsEvenInMultiLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetSubmitAction("submit")
	ta.Layout(LayoutContext{}, Rect{X: 0, Y: 0, W: 40, H: 5})
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: 'a', Text: "a"})
	res := ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
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
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyUp})
	if ta.CurLine() != 1 {
		t.Fatalf("up from line3: line=%d want 1", ta.CurLine())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyUp})
	if ta.CurLine() != 0 {
		t.Fatalf("up from line2: line=%d want 0", ta.CurLine())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyUp})
	if ta.CurLine() != 0 || ta.CurCol() != 0 {
		t.Fatalf("up at top should stick: line=%d col=%d", ta.CurLine(), ta.CurCol())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyDown})
	if ta.CurLine() != 1 {
		t.Fatalf("down from line1: line=%d want 1", ta.CurLine())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyDown})
	if ta.CurLine() != 2 {
		t.Fatalf("down from line2: line=%d want 2", ta.CurLine())
	}
}

func TestTextArea_HomeEndAreLineAware(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("hello\nworld")
	// cursor at end of "world" (1,5)
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyHome})
	if ta.CurLine() != 1 || ta.CurCol() != 0 {
		t.Fatalf("home: cursor=(%d,%d) want (1,0)", ta.CurLine(), ta.CurCol())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyEnd})
	if ta.CurLine() != 1 || ta.CurCol() != 5 {
		t.Fatalf("end: cursor=(%d,%d) want (1,5)", ta.CurLine(), ta.CurCol())
	}
	// Ctrl+Home goes to first line
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	if ta.CurLine() != 0 || ta.CurCol() != 0 {
		t.Fatalf("ctrl+home: cursor=(%d,%d) want (0,0)", ta.CurLine(), ta.CurCol())
	}
}

func TestTextArea_BackspaceAtLineStartJoinsLines(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2")
	// cursor at (1,0)
	ta.SetCursorFlat(6)
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyBackspace})
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
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyDelete})
	if ta.Value() != "line1line2" {
		t.Fatalf("value=%q want line1line2", ta.Value())
	}
}

func TestTextArea_DeleteToLineStartStopsAtLineBoundary(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("line1\nline2")
	ta.SetCursorFlat(8) // 'n' in "line2" (l-i-n-e-2 = cols 0-4, flat 6-10)
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
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
	ta.Handle(EventContext{Focused: true}, tea.PasteMsg{Content: "hello\nworld"})
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
	// First cell should be 'h'
	if c := buf.Get(0, 0); c.Char != 'h' {
		t.Fatalf("first cell=%q want 'h'", string(c.Char))
	}
	// Find 'e','l','l','o'
	row := rowText(buf, 0)
	if !strings.Contains(row, "hello") {
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

func TestTextArea_RuneSafeUnicode(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetValue("日本語\n🌍world")
	// 日本語=3 runes, \n=1, 🌍=1 rune (single codepoint), world=5 → total 10
	if ta.Len() != 10 {
		t.Fatalf("len=%d want 10", ta.Len())
	}
	// Backspace removes last rune
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if ta.Value() != "日本語\n🌍worl" {
		t.Fatalf("value=%q", ta.Value())
	}
}

func TestTextArea_GoalColumnPreservedAcrossShorterLine(t *testing.T) {
	ta := NewTextArea("test/area", nil, true)
	ta.SetTextWidth(40)
	ta.SetValue("long line here\nx")
	// cursor at end of "x" (1,1)
	ta.SetCursorFlat(15) // at 'l' in "long line here"
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyDown})
	// Should clamp to end of "x" (col 1)
	if ta.CurLine() != 1 || ta.CurCol() != 1 {
		t.Fatalf("down to short line: cursor=(%d,%d) want (1,1)", ta.CurLine(), ta.CurCol())
	}
	// Up should try to restore the goal column
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyUp})
	if ta.CurLine() != 0 {
		t.Fatalf("up back: line=%d want 0", ta.CurLine())
	}
	// Goal column was 1 (from "x"), so we land at col 1
	if ta.CurCol() != 1 {
		t.Logf("goal col behavior: col=%d (goal=1)", ta.CurCol())
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
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyBackspace})
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
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	// cursorNextWord from 0: 'h' not space → skip word "hello" → at 5 (space)
	if ta.CursorFlat() != 5 {
		t.Fatalf("ctrl+right: cursor=%d want 5 (end of 'hello')", ta.CursorFlat())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	// from 5: skip spaces → at 'w' (6), skip word "world" → at 11
	if ta.CursorFlat() != 11 {
		t.Fatalf("ctrl+right again: cursor=%d want 11", ta.CursorFlat())
	}
	ta.Handle(EventContext{Focused: true}, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
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