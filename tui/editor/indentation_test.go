package editor

import (
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestIndentCommandsContinueDocumentStyle(t *testing.T) {
	for _, tc := range []struct {
		name, text, unit string
		width            int
	}{
		{"spaces", "{\n  one\n  two\n}", "  ", 2},
		{"tabs", "{\n\tone\n\ttwo\n}", "\t", 4},
		{"four spaces", "{\n    one\n    two\n}", "    ", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, "sample.txt", tc.text)
			selectRange(e, 1, tc.width, 2, tc.width+3)
			before := e.captureViewState()
			press(e, tea.KeyPressMsg{Code: tea.KeyTab})
			require.Equal(t, tc.unit+tc.unit+"one", e.LineText(1))
			e.Undo()
			require.Equal(t, tc.text, e.Value())
			require.Equal(t, before, e.captureViewState())
			e.ClearSelection()
			e.SetCursorPosition(1, tc.width)
			press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
			require.Equal(t, "one", e.LineText(1))
			e.Undo()
			press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			require.Equal(t, "one", e.LineText(1))
			e.Undo()
			e.SetCursorPosition(0, 1)
			press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, tc.unit, e.LineText(1))
			e.Undo()
			require.Equal(t, tc.text, e.Value())
		})
	}
}

func TestTabUsesNextStopAndCachedStyleFollowsLinkedEdits(t *testing.T) {
	e := opsEditor(t, "sample.txt", "{\n  one\n}")
	e.SetCursorPosition(1, 1)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "   one", e.LineText(1)) // one space to the next two-space stop
	e.Undo()
	peer := e.NewLinkedView()
	require.Equal(t, 2, peer.indentStyle().size)
	peer.ReplaceDocument("{\n\tone\n}")
	require.True(t, e.indentStyle().tab)
	e.SetCursorPosition(1, 4)
	press(e, tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "\t\tone", peer.LineText(1))
}

func TestEnterBetweenPaddedPairKeepsClosingIndentAndOneUndo(t *testing.T) {
	for _, line := range []string{"  {  }", "\t{\t}"} {
		e := opsEditor(t, "sample.txt", line)
		before := leadingWhitespace(line)
		e.SetCursorPosition(0, LineVisualWidth(before, 4)+1)
		press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, before+"}", e.LineText(2))
		require.Equal(t, 3, e.LineCount())
		e.Undo()
		require.Equal(t, line, e.Value())
	}
}

func BenchmarkIndentStyleDense(b *testing.B) {
	e := New(nil)
	e.buf.ReplaceAll(strings.Repeat("{\n  one\n}\n", 10000))
	b.ReportAllocs()
	for b.Loop() {
		_ = e.indentStyle()
	}
}
