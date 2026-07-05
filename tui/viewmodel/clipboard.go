package viewmodel

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"crude/clipboard"
)

func (m *App) copyEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
}

func (m *App) cutEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
	m.EditorState.Editor.DeleteSelection()
	m.contentDirty = true
}

func (m *App) pasteIntoEditor() {
	if m.EditorState.Editor == nil {
		return
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.StatusBar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	m.EditorState.Editor.HandleEvent(tea.PasteMsg{Content: content})
	m.StatusBar.SetInfo(fmt.Sprintf("Pasted %d chars", len(content)))
	m.contentDirty = true
}

func (m *App) pasteIntoInput() {
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.StatusBar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	canType := !m.OverlayState.IsBlocking()
	updated, _, _ := m.Input.Update(tea.PasteMsg{Content: content},
		canType, m.OverlayState.IsBlocking(), m.ChatAreaHeight, m.Input.ShowHint())
	m.Input = updated
	m.StatusBar.SetInfo(fmt.Sprintf("Pasted %d chars into input", len(content)))
}

func (m *App) copyToClipboard(s string) {
	if s == "" {
		return
	}
	if err := clipboard.WriteText(s); err != nil {
		m.StatusBar.SetError("clipboard error: " + err.Error())
		return
	}
	m.StatusBar.SetInfo(fmt.Sprintf("Copied %d chars", len(s)))
}
