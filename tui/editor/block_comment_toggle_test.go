package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// Ctrl+/ did nothing in a language that has block comments only, such as
// markup and style sheets. Each selected line gets its own pair of delimiters.
func TestToggleCommentUsesBlockDelimitersWithoutALinePrefix(t *testing.T) {
	toggle := tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}
	for _, tc := range []struct {
		name, file, before, after string
		setup                     func(*Editor)
	}{
		{"markup line", "page.html", "<p>hello</p>", "<!-- <p>hello</p> -->", func(e *Editor) { e.SetCursorPosition(0, 3) }},
		{"style line", "site.css", "  color: red;  ", "  /* color: red; */  ", func(e *Editor) { e.SetCursorPosition(0, 4) }},
		{"selected lines skip blanks", "page.html", "<ul>\n\n\t<li>a</li>\n</ul>", "<!-- <ul> -->\n\n\t<!-- <li>a</li> -->\n<!-- </ul> -->",
			func(e *Editor) { selectRange(e, 0, 0, 3, 5) }},
		{"one uncommented line comments all", "page.html", "<!-- <a> -->\n<b>", "<!-- <a> -->\n<!-- <b> -->",
			func(e *Editor) { selectRange(e, 0, 0, 1, 3) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, tc.file, tc.before)
			tc.setup(e)
			press(e, toggle)
			require.Equal(t, tc.after, e.Value())
			e.Undo()
			require.Equal(t, tc.before, e.Value(), "one undo step")
		})
	}
}

func TestToggleBlockCommentRestoresTextCaretAndSelection(t *testing.T) {
	toggle := tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}
	e := opsEditor(t, "page.html", "\t<p>hello</p>")
	e.SetCursorPosition(0, 7) // after "<p>" (the tab is four columns wide)
	press(e, toggle)
	require.Equal(t, "\t<!-- <p>hello</p> -->", e.Value())
	require.Equal(t, 12, e.CursorCol(), "the caret stays on the same source character")
	press(e, toggle)
	require.Equal(t, "\t<p>hello</p>", e.Value())
	require.Equal(t, 7, e.CursorCol())

	// A caret at the end of the content stays inside the comment, and a file
	// without any comment syntax is left unchanged.
	e = opsEditor(t, "site.css", "a{}")
	e.SetCursorPosition(0, 3)
	press(e, toggle)
	require.Equal(t, "/* a{} */", e.Value())
	require.Equal(t, 6, e.CursorCol())
	e = opsEditor(t, "data.json", "{\"a\": 1}")
	press(e, toggle)
	require.Equal(t, "{\"a\": 1}", e.Value())
}

func TestBlockCommentMixedSelectionNeverNestsAndUndoKeepsSource(t *testing.T) {
	e := opsEditor(t, "page.html", "<!-- <a> -->\n<b>\n  ")
	selectRange(e, 0, 0, 2, 2)
	before := e.captureViewState()
	press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
	require.Equal(t, "<!-- <a> -->\n<!-- <b> -->\n  ", e.Value())
	e.Undo()
	require.Equal(t, "<!-- <a> -->\n<b>\n  ", e.Value())
	require.Equal(t, before, e.captureViewState())
	for _, text := range []string{"<!-- comment\nend -->", "color: red; /* note */"} {
		e := opsEditor(t, "site.css", text)
		if text[0] == '<' {
			e.filePath = "page.html"
		}
		selectRange(e, 0, 0, e.LineCount()-1, e.lineVisualWidth(e.LineCount()-1))
		press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
		require.Equal(t, text, e.Value(), "unsafe partial delimiters must not be nested")
	}
}
