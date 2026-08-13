package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"crude/tui/theme"
)

func opsEditor(t *testing.T, name, content string) *Editor {
	t.Helper()
	e := New(theme.DefaultDark())
	e.filePath = name
	e.title = name
	if content != "" {
		_, err := e.buf.Insert(Position{}, []byte(content))
		assert.NoError(t, err)
	}
	e.open = true
	e.viewW = 80
	e.viewH = 24
	e.initChroma()
	return e
}

func selectRange(e *Editor, sLine, sCol, eLine, eCol int) {
	e.selecting = true
	e.selAnchor = Position{Line: sLine, Col: sCol}
	e.selEnd = Position{Line: eLine, Col: eCol}
	e.cursor = Position{Line: eLine, Col: eCol}
}

func press(e *Editor, msg tea.KeyPressMsg) {
	_ = e.HandleEvent(msg)
}

func typeChar(e *Editor, r rune) {
	press(e, tea.KeyPressMsg{Code: r, Text: string(r)})
}

// ── auto-indent on Enter ─────────────────────────────────────────────

func TestAutoIndent_InheritsLeadingWhitespace(t *testing.T) {
	e := opsEditor(t, "test.go", "    foo")
	e.cursor = Position{Line: 0, Col: 7}
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "    foo\n    ", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 4}, e.cursor)
}

func TestAutoIndent_ExtraLevelAfterOpenBrace(t *testing.T) {
	e := opsEditor(t, "test.go", "func main() {")
	e.cursor = Position{Line: 0, Col: 13}
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "func main() {\n    ", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 4}, e.cursor)
}

func TestAutoIndent_ExtraLevelKeepsTabStyle(t *testing.T) {
	e := opsEditor(t, "test.go", "\tif x {")
	e.cursor = Position{Line: 0, Col: e.lineVisualWidth(0)}
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "\tif x {\n\t\t", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 8}, e.cursor)
}

func TestAutoIndent_ExtraLevelAfterBracketAndParen(t *testing.T) {
	for _, tt := range []struct {
		content string
	}{
		{"x := []int{"},
		{"foo("},
		{"m := map[string]int["},
	} {
		e := opsEditor(t, "test.go", tt.content)
		e.cursor = Position{Line: 0, Col: e.lineVisualWidth(0)}
		press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
		assert.Equal(t, tt.content+"\n    ", e.Value(), "content %q", tt.content)
	}
}

func TestAutoIndent_MidIndentEnterInheritsUpToCursor(t *testing.T) {
	e := opsEditor(t, "test.go", "        x")
	e.cursor = Position{Line: 0, Col: 4}
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "    \n        x", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 4}, e.cursor)
}

func TestAutoIndent_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "    foo {")
	e.cursor = Position{Line: 0, Col: 9}
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "    foo {\n        ", e.Value())
	e.Undo()
	assert.Equal(t, "    foo {", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 9}, e.cursor)
}

// ── auto-closing pairs ───────────────────────────────────────────────

func TestAutoClosePair_InsertsCloser(t *testing.T) {
	for opener, closer := range autoClosePairs {
		e := opsEditor(t, "test.go", "")
		typeChar(e, opener)
		assert.Equal(t, string(opener)+string(closer), e.Value(), "pair %c%c", opener, closer)
		assert.Equal(t, Position{Line: 0, Col: 1}, e.cursor, "cursor between %c%c", opener, closer)
	}
}

func TestAutoClosePair_TypeOverCloser(t *testing.T) {
	e := opsEditor(t, "test.go", "")
	typeChar(e, '(')
	typeChar(e, ')')
	assert.Equal(t, "()", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 2}, e.cursor)
}

func TestAutoClosePair_QuoteTypeOver(t *testing.T) {
	e := opsEditor(t, "test.go", "")
	typeChar(e, '"')
	typeChar(e, '"')
	assert.Equal(t, `""`, e.Value())
	assert.Equal(t, Position{Line: 0, Col: 2}, e.cursor)
}

func TestAutoClosePair_CloserInsertsPlainWhenNotNext(t *testing.T) {
	e := opsEditor(t, "test.go", "")
	typeChar(e, ')')
	assert.Equal(t, ")", e.Value())
}

func TestAutoClosePair_NoPairBeforeWordChar(t *testing.T) {
	e := opsEditor(t, "test.go", "foo")
	e.cursor = Position{Line: 0, Col: 0}
	typeChar(e, '(')
	assert.Equal(t, "(foo", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 1}, e.cursor)
}

func TestAutoClosePair_QuoteNoPairAfterWordChar(t *testing.T) {
	e := opsEditor(t, "test.go", "don")
	e.cursor = Position{Line: 0, Col: 3}
	typeChar(e, '\'')
	assert.Equal(t, "don'", e.Value())
}

func TestAutoClosePair_PairsBeforeCloser(t *testing.T) {
	e := opsEditor(t, "test.go", "()")
	e.cursor = Position{Line: 0, Col: 1}
	typeChar(e, '[')
	assert.Equal(t, "([])", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 2}, e.cursor)
}

func TestAutoClosePair_WrapSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "hello")
	selectRange(e, 0, 0, 0, 5)
	typeChar(e, '[')
	assert.Equal(t, "[hello]", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 1, 0, 6}, [4]int{sLine, sCol, eLine, eCol})
	assert.Equal(t, Position{Line: 0, Col: 6}, e.cursor)
}

func TestAutoClosePair_WrapMultiLineSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	selectRange(e, 0, 0, 1, 2)
	typeChar(e, '(')
	assert.Equal(t, "(aa\nbb)", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 1, 1, 2}, [4]int{sLine, sCol, eLine, eCol})
}

func TestAutoClosePair_WrapSingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "hello")
	selectRange(e, 0, 0, 0, 5)
	typeChar(e, '{')
	assert.Equal(t, "{hello}", e.Value())
	e.Undo()
	assert.Equal(t, "hello", e.Value())
	e.Redo()
	assert.Equal(t, "{hello}", e.Value())
}

func TestAutoClosePair_PairInsertSingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "")
	typeChar(e, '(')
	assert.Equal(t, "()", e.Value())
	e.Undo()
	assert.Equal(t, "", e.Value())
}

func TestAutoClosePair_BackspaceDeletesEmptyPair(t *testing.T) {
	e := opsEditor(t, "test.go", "")
	typeChar(e, '(')
	press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Equal(t, "", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 0}, e.cursor)
	e.Undo()
	assert.Equal(t, "()", e.Value())
}

func TestBackspace_PlainDeleteWhenNotBetweenPair(t *testing.T) {
	e := opsEditor(t, "test.go", "(x)")
	e.cursor = Position{Line: 0, Col: 2}
	press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Equal(t, "()", e.Value())
}

func TestBackspace_DeletesSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "hello")
	selectRange(e, 0, 1, 0, 4)
	press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Equal(t, "ho", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 1}, e.cursor)
	assert.False(t, e.HasSelection())
	e.Undo()
	assert.Equal(t, "hello", e.Value())
}

func TestDeleteKey_DeletesSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "hello")
	selectRange(e, 0, 1, 0, 4)
	press(e, tea.KeyPressMsg{Code: tea.KeyDelete})
	assert.Equal(t, "ho", e.Value())
}

// ── Tab / Shift+Tab indent and outdent ───────────────────────────────

func TestIndentLines_MultiLineSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	selectRange(e, 0, 1, 2, 1)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Equal(t, "    aa\n    bb\n    cc", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 5, 2, 5}, [4]int{sLine, sCol, eLine, eCol})
	assert.Equal(t, Position{Line: 2, Col: 5}, e.cursor)
	assert.True(t, e.HasSelection())
}

func TestIndentLines_LastLineExcludedAtColZero(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	selectRange(e, 0, 0, 2, 0)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Equal(t, "    aa\n    bb\ncc", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 0, 2, 0}, [4]int{sLine, sCol, eLine, eCol})
}

func TestIndentLines_SkipsEmptyLines(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\n\nbb")
	selectRange(e, 0, 0, 2, 2)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Equal(t, "    aa\n\n    bb", e.Value())
}

func TestIndentLines_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	selectRange(e, 0, 0, 2, 2)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Equal(t, "    aa\n    bb\n    cc", e.Value())
	e.Undo()
	assert.Equal(t, "aa\nbb\ncc", e.Value())
	e.Redo()
	assert.Equal(t, "    aa\n    bb\n    cc", e.Value())
}

func TestTab_NoSelectionInsertsSpaces(t *testing.T) {
	e := opsEditor(t, "test.go", "ab")
	e.cursor = Position{Line: 0, Col: 1}
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Equal(t, "a    b", e.Value())
}

func TestOutdentLines_MultiLineSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "    aa\n\tbb\ncc")
	selectRange(e, 0, 5, 2, 1)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assert.Equal(t, "aa\nbb\ncc", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 1, 2, 1}, [4]int{sLine, sCol, eLine, eCol})
}

func TestOutdent_CurrentLineNoSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "    aa")
	e.cursor = Position{Line: 0, Col: 6}
	press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assert.Equal(t, "aa", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 2}, e.cursor)
}

func TestOutdent_PartialIndent(t *testing.T) {
	e := opsEditor(t, "test.go", "  aa")
	e.cursor = Position{Line: 0, Col: 3}
	press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assert.Equal(t, "aa", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 1}, e.cursor)
}

func TestOutdent_NoIndentNoop(t *testing.T) {
	e := opsEditor(t, "test.go", "aa")
	e.cursor = Position{Line: 0, Col: 1}
	press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assert.Equal(t, "aa", e.Value())
	assert.Equal(t, 0, len(e.changes))
}

func TestOutdentLines_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "    aa\n    bb")
	selectRange(e, 0, 0, 1, 6)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	assert.Equal(t, "aa\nbb", e.Value())
	e.Undo()
	assert.Equal(t, "    aa\n    bb", e.Value())
}

// ── Smart Home ───────────────────────────────────────────────────────

func TestSmartHome_TogglesBetweenIndentAndColumnZero(t *testing.T) {
	e := opsEditor(t, "test.go", "    foo")
	e.cursor = Position{Line: 0, Col: 7}
	press(e, tea.KeyPressMsg{Code: tea.KeyHome})
	assert.Equal(t, 4, e.cursor.Col, "first Home goes to first non-whitespace")
	press(e, tea.KeyPressMsg{Code: tea.KeyHome})
	assert.Equal(t, 0, e.cursor.Col, "second Home goes to column 0")
	press(e, tea.KeyPressMsg{Code: tea.KeyHome})
	assert.Equal(t, 4, e.cursor.Col, "third Home toggles back to first non-whitespace")
}

func TestSmartHome_NoIndentStaysAtZero(t *testing.T) {
	e := opsEditor(t, "test.go", "foo")
	e.cursor = Position{Line: 0, Col: 2}
	press(e, tea.KeyPressMsg{Code: tea.KeyHome})
	assert.Equal(t, 0, e.cursor.Col)
	press(e, tea.KeyPressMsg{Code: tea.KeyHome})
	assert.Equal(t, 0, e.cursor.Col)
}

// ── move lines up/down ───────────────────────────────────────────────

func TestMoveLine_DownAndUp(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	e.cursor = Position{Line: 0, Col: 1}
	press(e, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	assert.Equal(t, "bb\naa\ncc", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 1}, e.cursor)
	press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	assert.Equal(t, "aa\nbb\ncc", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 1}, e.cursor)
}

func TestMoveLines_SelectionBlockDown(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc\ndd")
	selectRange(e, 1, 0, 2, 2)
	press(e, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	assert.Equal(t, "aa\ndd\nbb\ncc", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{2, 0, 3, 2}, [4]int{sLine, sCol, eLine, eCol})
}

func TestMoveLines_SelectionBlockUp(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc\ndd")
	selectRange(e, 1, 0, 2, 2)
	press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	assert.Equal(t, "bb\ncc\naa\ndd", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 0, 1, 2}, [4]int{sLine, sCol, eLine, eCol})
}

func TestMoveLines_BoundaryNoop(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	e.cursor = Position{Line: 0, Col: 0}
	press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	assert.Equal(t, "aa\nbb", e.Value())

	e.cursor = Position{Line: 1, Col: 0}
	press(e, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	assert.Equal(t, "aa\nbb", e.Value())
	assert.Equal(t, 0, len(e.changes))
}

func TestMoveLine_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	e.cursor = Position{Line: 1, Col: 2}
	press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	assert.Equal(t, "bb\naa\ncc", e.Value())
	e.Undo()
	assert.Equal(t, "aa\nbb\ncc", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 2}, e.cursor)
	e.Redo()
	assert.Equal(t, "bb\naa\ncc", e.Value())
}

func TestMoveLines_PreservesWideRunesAndTabs(t *testing.T) {
	e := opsEditor(t, "test.go", "\tf(文)\nplain")
	e.cursor = Position{Line: 0, Col: 0}
	press(e, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	assert.Equal(t, "plain\n\tf(文)", e.Value())
}

// ── duplicate line/selection ─────────────────────────────────────────

func TestDuplicateLine_NoSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	e.cursor = Position{Line: 0, Col: 1}
	press(e, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa\naa\nbb", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 1}, e.cursor)
}

func TestDuplicateSelection(t *testing.T) {
	e := opsEditor(t, "test.go", "hello")
	selectRange(e, 0, 1, 0, 3)
	press(e, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "helello", e.Value())
	sLine, sCol, eLine, eCol := e.SelectionRange()
	assert.Equal(t, [4]int{0, 3, 0, 5}, [4]int{sLine, sCol, eLine, eCol})
	assert.Equal(t, Position{Line: 0, Col: 5}, e.cursor)
}

func TestDuplicateSelection_MultiLine(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	selectRange(e, 0, 0, 1, 2)
	press(e, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa\nbbaa\nbb", e.Value())
}

func TestDuplicate_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	e.cursor = Position{Line: 0, Col: 2}
	press(e, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa\naa\nbb", e.Value())
	e.Undo()
	assert.Equal(t, "aa\nbb", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 2}, e.cursor)
}

// ── delete current line ──────────────────────────────────────────────

func TestDeleteLine_Middle(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	e.cursor = Position{Line: 1, Col: 1}
	press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa\ncc", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 1}, e.cursor)
}

func TestDeleteLine_LastLine(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	e.cursor = Position{Line: 1, Col: 1}
	press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 1}, e.cursor)
}

func TestDeleteLine_OnlyLine(t *testing.T) {
	e := opsEditor(t, "test.go", "abc")
	e.cursor = Position{Line: 0, Col: 2}
	press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 0}, e.cursor)
}

func TestDeleteLines_Selection(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc\ndd")
	selectRange(e, 0, 1, 2, 1)
	press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "dd", e.Value())
	assert.False(t, e.HasSelection())
}

func TestDeleteLine_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb\ncc")
	e.cursor = Position{Line: 1, Col: 2}
	press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa\ncc", e.Value())
	e.Undo()
	assert.Equal(t, "aa\nbb\ncc", e.Value())
	assert.Equal(t, Position{Line: 1, Col: 2}, e.cursor)
}

func TestDeleteLine_RedoClampsCursorToBuffer(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	e.cursor = Position{Line: 1, Col: 1}
	press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift})
	assert.Equal(t, "aa", e.Value())
	e.Undo()
	assert.Equal(t, "aa\nbb", e.Value())
	e.Redo()
	assert.Equal(t, "aa", e.Value())
	assert.LessOrEqual(t, e.cursor.Line, e.LineCount()-1)
	assert.LessOrEqual(t, e.cursor.Col, e.lineVisualWidth(e.cursor.Line))
}

// ── toggle line comment ──────────────────────────────────────────────

func TestToggleComment_GoCommentAndUncomment(t *testing.T) {
	e := opsEditor(t, "test.go", "foo()\nbar()")
	selectRange(e, 0, 0, 1, 5)
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "// foo()\n// bar()", e.Value())
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "foo()\nbar()", e.Value())
}

func TestToggleComment_PythonHash(t *testing.T) {
	e := opsEditor(t, "test.py", "x = 1")
	e.cursor = Position{Line: 0, Col: 0}
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "# x = 1", e.Value())
}

func TestToggleComment_ShellHash(t *testing.T) {
	e := opsEditor(t, "run.sh", "echo hi")
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "# echo hi", e.Value())
}

func TestToggleComment_CtrlUnderscoreLegacyChord(t *testing.T) {
	e := opsEditor(t, "test.go", "foo()")
	press(e, tea.KeyPressMsg{Code: '_', Mod: tea.ModCtrl})
	assert.Equal(t, "// foo()", e.Value())
}

func TestToggleComment_PreservesIndentation(t *testing.T) {
	e := opsEditor(t, "test.go", "\tfoo()\n    bar()")
	selectRange(e, 0, 0, 1, 8)
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "\t// foo()\n    // bar()", e.Value())
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "\tfoo()\n    bar()", e.Value())
}

func TestToggleComment_MixedCommentsAll(t *testing.T) {
	e := opsEditor(t, "test.go", "// aa\nbb")
	selectRange(e, 0, 0, 1, 2)
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "// // aa\n// bb", e.Value())
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "// aa\nbb", e.Value())
}

func TestToggleComment_SkipsBlankLines(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\n\nbb")
	selectRange(e, 0, 0, 2, 2)
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "// aa\n\n// bb", e.Value())
}

func TestToggleComment_UncommentWithoutSpace(t *testing.T) {
	e := opsEditor(t, "test.go", "//foo()")
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "foo()", e.Value())
}

func TestToggleComment_UnknownExtensionNoop(t *testing.T) {
	e := opsEditor(t, "test.xyz", "foo")
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "foo", e.Value())
	assert.Equal(t, 0, len(e.changes))
}

func TestToggleComment_CursorShifts(t *testing.T) {
	e := opsEditor(t, "test.go", "foo()")
	e.cursor = Position{Line: 0, Col: 3}
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "// foo()", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 6}, e.cursor)
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "foo()", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 3}, e.cursor)
}

func TestToggleComment_SingleUndoStep(t *testing.T) {
	e := opsEditor(t, "test.go", "aa\nbb")
	selectRange(e, 0, 0, 1, 2)
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	assert.Equal(t, "// aa\n// bb", e.Value())
	e.Undo()
	assert.Equal(t, "aa\nbb", e.Value())
	e.Redo()
	assert.Equal(t, "// aa\n// bb", e.Value())
}

// ── undo grouping and DeleteSelection undo ───────────────────────────

func TestDeleteSelection_IsUndoable(t *testing.T) {
	e := opsEditor(t, "test.go", "hello world")
	selectRange(e, 0, 5, 0, 11)
	assert.True(t, e.DeleteSelection())
	assert.Equal(t, "hello", e.Value())
	assert.Equal(t, Position{Line: 0, Col: 5}, e.cursor)
	e.Undo()
	assert.Equal(t, "hello world", e.Value())
}

func TestUndoGroup_UngroupedChangesStayIndividual(t *testing.T) {
	e := opsEditor(t, "test.go", "")
	typeChar(e, 'a')
	typeChar(e, 'b')
	assert.Equal(t, "ab", e.Value())
	e.Undo()
	assert.Equal(t, "a", e.Value())
	e.Undo()
	assert.Equal(t, "", e.Value())
}

func TestUndoGroup_GroupUndoneAndRedoneAtomically(t *testing.T) {
	e := opsEditor(t, "test.go", "x")
	cursorBefore := e.cursor
	e.beginUndoGroup()
	e.insertRecorded(Position{Line: 0, Col: 1}, "y", cursorBefore)
	e.insertRecorded(Position{Line: 0, Col: 2}, "z", cursorBefore)
	e.endUndoGroup()
	e.finishEdit()
	assert.Equal(t, "xyz", e.Value())

	e.Undo()
	assert.Equal(t, "x", e.Value())
	e.Redo()
	assert.Equal(t, "xyz", e.Value())
	e.Undo()
	assert.Equal(t, "x", e.Value())
}
