package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
)

func (m *App) runEditorAction(ed *editor.Editor, token string, id ui.ActionID) tea.Cmd {
	if m.EditorState == nil || m.EditorState.Editor != ed || ed == nil || !ed.IsOpen() || ed.ActionToken() != token {
		m.Feedback.NotifyInfo("The editor changed. Select the command again.")
		return nil
	}
	m.closeCompletionPopup()
	m.closeSignatureHelp()
	return ed.HandleEvent(editor.ActionMsg{ID: id})
}

func (m *App) openEditorActions() tea.Cmd {
	if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return nil
	}
	ed := m.EditorState.Editor
	token := ed.ActionToken()
	model := newCommandPaletteModel(m.Theme, func(id string) tea.Cmd {
		m.overlayHost.Close(ui.OverlayCommandPalette)
		return m.runEditorAction(ed, token, ui.ActionID(id))
	}, m.actionRegistry)
	model.items = nil
	for _, action := range ui.EditorActions() {
		if ed.ReadOnly() && action.MutatesText {
			continue
		}
		model.items = append(model.items, cmdEntry{ID: string(action.ID), Label: action.Label, Shortcut: ui.FormatShortcut(m.actionRegistry.HintFor(action.ID)), Category: "Edit", Icon: " "})
	}
	model.applyFilter()
	_, cmd := m.openCommandPicker(model, "Editor actions")
	return cmd
}
