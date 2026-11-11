package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestStructuralBracketMotionAndPaintIgnoreQuotedAndCommentDelimiters(t *testing.T) {
	for _, path := range []string{"match.go", "match.c"} {
		t.Run(path, func(t *testing.T) {
			source := "call(\"[)]\", values[2]) // )"
			e := loadEditorText(t, path, source)
			e.readOnly = false
			e.SetCursorPosition(0, 4)
			painted := paintEditor(e, 80, 10)
			close := strings.Index(source, ") //")
			require.True(t, painted.Get(e.gutterWidth()+close, 0).Attr.Has(cell.AttrBold), "matching delimiter not painted")
			press(e, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt})
			require.Equal(t, close, e.CursorCol())
			require.Equal(t, source, e.Value())
			require.False(t, e.Modified())
			press(e, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt})
			require.Equal(t, 4, e.CursorCol())
			e.SetCursorPosition(0, strings.Index(source, "[)]"))
			before := e.cursor
			press(e, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt})
			require.Equal(t, before, e.cursor)
			e.SetCursorPosition(0, len(source)-1)
			before = e.cursor
			press(e, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt})
			require.Equal(t, before, e.cursor)
		})
	}
}

func TestStructuralSelectionUsesCurrentSyntaxAndShrinksExactly(t *testing.T) {
	source := "package p\nfunc f() {\n    result := wrap(arg + 1)\n}\n"
	e := loadEditorText(t, "ranges.go", source)
	e.readOnly = false
	paintEditor(e, 80, 10)
	e.SetCursorPosition(2, 20)
	initial := e.captureViewState()
	e.HandleEvent(ActionMsg{ID: "select.expand"})
	require.Equal(t, "arg", e.SelectedText())
	e.HandleEvent(ActionMsg{ID: "select.expand"})
	require.Equal(t, "arg + 1", e.SelectedText())
	e.HandleEvent(ActionMsg{ID: "select.expand"})
	require.Equal(t, "(arg + 1)", e.SelectedText())
	for range 3 {
		e.HandleEvent(ActionMsg{ID: "select.shrink"})
	}
	require.Equal(t, initial, e.captureViewState())
	require.Equal(t, source, e.Value())
	// A changed source must not retain stale structural locations.
	e.SetCursorPosition(0, 0)
	e.InsertRune('x')
	e.StopSyntax()
	e.SetCursorPosition(2, 18)
	before := e.cursor
	e.HandleEvent(ActionMsg{ID: "move.matchingBracket"})
	require.Equal(t, before, e.cursor)
}
