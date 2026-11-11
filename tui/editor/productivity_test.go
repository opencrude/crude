package editor

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProductivityNativeCommandsAndAtomicHistory(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		line, col           int
		key                 tea.KeyPressMsg
	}{
		{"duplicate", "α👩‍💻\r\nnext", "α👩‍💻\r\nα👩‍💻\r\nnext", 0, 1, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}},
		{"join", "one  \r\n\ttwo\r\nlast", "one two\r\nlast", 0, 1, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl | tea.ModShift}},
		{"line below", "  first\nlast", "  first\n  \nlast", 0, 4, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}},
		{"line above", "  first\nlast", "  \n  first\nlast", 0, 4, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl | tea.ModAlt}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, "sample.txt", tc.before)
			e.SetCursorPosition(tc.line, tc.col)
			before := e.captureViewState()
			press(e, tc.key)
			require.Equal(t, tc.after, e.Value())
			after := e.captureViewState()
			e.Undo()
			require.Equal(t, tc.before, e.Value())
			require.Equal(t, before, e.captureViewState())
			e.Redo()
			require.Equal(t, tc.after, e.Value())
			require.Equal(t, after, e.captureViewState())
		})
	}
}

func TestDuplicatePrimaryShortcutCopiesSelection(t *testing.T) {
	e := opsEditor(t, "sample.txt", "before 👩‍💻 after")
	e.SetSelection(0, 7, 0, 9)
	press(e, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "before 👩‍💻👩‍💻 after", e.Value())
	require.Equal(t, "👩‍💻", e.SelectedText())
	e.Undo()
	require.Equal(t, "before 👩‍💻 after", e.Value())
}

func TestProductivityActionsPreserveSourceAndUndo(t *testing.T) {
	for _, tc := range []struct {
		id            ui.ActionID
		before, after string
		line, col     int
		selection     []int
	}{
		{"edit.upperCase", "école 👩‍💻", "ÉCOLE 👩‍💻", 0, 2, nil},
		{"edit.lowerCase", "ÉCOLE 👩‍💻", "école 👩‍💻", 0, 2, nil},
		{"edit.toggleCase", "lower", "LOWER", 0, 2, nil},
		{"edit.toggleCase", "UPPER", "upper", 0, 2, nil},
		{"edit.sortLines", "z\né\na\n", "a\nz\né\n", 1, 1, nil},
		{"edit.reverseLines", "z\né\na\n", "a\né\nz\n", 0, 0, nil},
		{"edit.sortLines", "keep\nz\na\nlast", "keep\na\nz\nlast", 1, 0, []int{1, 0, 3, 0}},
		{"edit.transpose", "a👩‍💻z", "👩‍💻az", 0, 1, nil},
		{"edit.transpose", "aé", "éa", 0, 2, nil},
		{"edit.joinLines", "  a\n  b\n  c\nlast", "  a b c\nlast", 0, 2, []int{0, 0, 3, 0}},
	} {
		t.Run(string(tc.id)+tc.before, func(t *testing.T) {
			e := opsEditor(t, "ops.txt", tc.before)
			e.SetCursorPosition(tc.line, tc.col)
			if tc.selection != nil {
				r := tc.selection
				e.SetSelection(r[0], r[1], r[2], r[3])
			}
			before := e.captureViewState()
			e.HandleEvent(ActionMsg{ID: tc.id})
			require.Equal(t, tc.after, e.Value())
			after := e.captureViewState()
			e.Undo()
			require.Equal(t, tc.before, e.Value())
			require.Equal(t, before, e.captureViewState())
			e.Redo()
			require.Equal(t, tc.after, e.Value())
			require.Equal(t, after, e.captureViewState())
		})
	}
}

func TestProductivityMotionsAndSelectionHistory(t *testing.T) {
	e := opsEditor(t, "motion.txt", "HTTPServer2_value 👩‍💻é\nnext\n\n\nlast")
	for _, col := range []int{4, 10, 11, 12, 17, 18, 20, 21} {
		e.HandleEvent(ActionMsg{ID: "move.subwordNext"})
		require.Equal(t, col, e.CursorCol())
	}
	for _, col := range []int{20, 18, 17, 12, 11, 10, 4, 0} {
		e.HandleEvent(ActionMsg{ID: "move.subwordPrevious"})
		require.Equal(t, col, e.CursorCol())
	}
	for _, line := range []int{2, 4} {
		press(e, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl})
		require.Equal(t, line, e.CursorLine())
	}
	press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	require.Equal(t, 3, e.CursorLine())
	press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl})
	require.Equal(t, 1, e.CursorLine())
	e.SetCursorPosition(0, 2)
	original := e.captureViewState()
	press(e, tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
	require.Equal(t, "HTTPServer2_value", e.SelectedText())
	word := e.captureViewState()
	press(e, tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
	require.Equal(t, e.LineText(0), e.SelectedText())
	press(e, tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt})
	require.Equal(t, word, e.captureViewState())
	press(e, tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt})
	require.Equal(t, original, e.captureViewState())
	e.HandleEvent(ActionMsg{ID: "select.expand"})
	e.SetSelection(0, 0, 0, 16) // an independent selection, even with the same bounds
	kept := e.captureViewState()
	e.HandleEvent(ActionMsg{ID: "select.shrink"})
	require.Equal(t, kept, e.captureViewState())
	e.SetCursorPosition(0, 0)
	press(e, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	require.Equal(t, e.LineText(0)+"\n", e.SelectedText())
	press(e, tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	require.Equal(t, e.LineText(0)+"\nnext\n", e.SelectedText())
}

func TestProductivityReadOnlyAndNoopActions(t *testing.T) {
	e := opsEditor(t, "readonly.txt", "word\nnext")
	e.readOnly = true
	for _, a := range ui.EditorActions() {
		if !a.MutatesText {
			continue
		}
		before := e.captureViewState()
		e.HandleEvent(ActionMsg{ID: a.ID})
		require.Equal(t, "word\nnext", e.Value())
		require.Equal(t, before, e.captureViewState())
		require.False(t, e.Modified())
	}
	press(e, tea.KeyPressMsg{Code: 'f', Mod: tea.ModAlt})
	require.Equal(t, 4, e.CursorCol())
	press(e, tea.KeyPressMsg{Code: 'w', Mod: tea.ModAlt})
	require.Equal(t, "word", e.SelectedText())
	e.readOnly = false
	e.SetCursorPosition(1, 4)
	token := e.ActionToken()
	e.HandleEvent(ActionMsg{ID: "not.an.action"})
	require.Equal(t, token, e.ActionToken())
	e.HandleEvent(ActionMsg{ID: "edit.joinLines"})
	require.Equal(t, "word\nnext", e.Value())
}

func TestProductivityQueuedActionSurvivesRecovery(t *testing.T) {
	e, _ := projectionEditor(t, "base")
	require.NoError(t, e.BindDocumentOwner(projectionOwner))
	e.SetOnDocumentUndo(func(DocumentUndoIntent) {})
	e.Undo()
	e.HandleEvent(ActionMsg{ID: "edit.duplicateLine"})
	r, ok := e.ExportDocumentRecovery()
	require.True(t, ok)
	require.Len(t, r.HistoryQueue, 1)
	require.Equal(t, ui.ActionID("edit.duplicateLine"), r.HistoryQueue[0].Action)
	encoded, err := json.Marshal(r)
	require.NoError(t, err)
	var decoded DocumentRecovery
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	next, _ := projectionEditor(t, "base")
	require.NoError(t, next.RestoreDocumentRecovery(decoded))
	next.DrainDocumentUndo()
	require.Equal(t, "base", next.Value())
	intent, ok := next.PendingDocumentUndoIntent(false)
	require.True(t, ok)
	require.NoError(t, next.CompleteDocumentUndo(intent, errors.New("empty history")))
	next.CompleteDocumentHistorySync()
	require.Equal(t, "base\nbase", next.Value())
	change := acknowledgeHistoryEdit(t, next, "native-action")
	require.NotEmpty(t, change.GroupID)
	decoded.HistoryQueue[0].Action = "unknown"
	require.Error(t, decoded.Validate())
}
