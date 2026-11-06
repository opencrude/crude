package viewmodel

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"crude/tui/editor"
	"crude/tui/ui"
)

// openGoToLine asks for a line in the active file. It uses the shared text
// dialog, pre-filled with the cursor's line and selected, so typing a number
// replaces it. Enter jumps; Esc leaves the cursor where it was.
func (m *App) openGoToLine() {
	ed := m.EditorState.Editor
	if ed == nil || !ed.IsOpen() {
		return
	}
	st := m.openTextPrompt(ui.TextPromptGoToLine, "Go to line",
		fmt.Sprintf("Line 1-%d, or line:column", max(1, ed.LineCount())),
		strconv.Itoa(ed.CursorLine()+1), m.jumpToLine)
	if st != nil {
		st.Edit.SelectAll()
	}
}

// jumpToLine moves the cursor to a 1-based line, or to a line and column, and
// marks that line as a landing cue: clearly visible, but not a selection, so
// typing does not replace the line. A target that is already on screen does
// not move the view; a target further away lands with context around it.
func (m *App) jumpToLine(value string) error {
	ed := m.EditorState.Editor
	if ed == nil || !ed.IsOpen() {
		return errors.New("open a file to go to a line")
	}
	lines := max(1, ed.LineCount())
	lineText, columnText, hasColumn := strings.Cut(strings.TrimSpace(value), ":")
	line, err := strconv.Atoi(strings.TrimSpace(lineText))
	if err != nil {
		return errors.New("enter a line number, for example 42")
	}
	if line < 1 || line > lines {
		return fmt.Errorf("this file has %d lines. Enter 1-%d", lines, lines)
	}
	column := 1
	if hasColumn {
		column, err = strconv.Atoi(strings.TrimSpace(columnText))
		if err != nil || column < 1 {
			return errors.New("enter the column after the colon, for example 42:5")
		}
	}
	index := line - 1
	// SetCursorPosition clamps the column to the line, so a column past the
	// end of a short line lands at its end.
	ed.SetCursorPosition(index, column-1)
	ed.SetLandingRange(index, 0, editor.LineVisualWidth(ed.LineText(index), editor.DefaultTabWidth))
	ed.ScrollToCursor()
	return nil
}
