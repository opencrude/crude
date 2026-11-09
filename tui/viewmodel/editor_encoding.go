package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"time"

	"crude/textencoding"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/workspace"
)

// The standard menu owns keyboard/pointer navigation and terminal bounds. A
// selection requests the ordinary serialized Save pipeline, never a UI writer.
func (m *App) openEncodingMenu(ed *editor.Editor) tea.Cmd {
	if ed == nil || !ed.IsOpen() || ed.ReadOnly() {
		return nil
	}
	if ed.FilePath() == "" {
		m.openUntitledSave(ed, false)
		return nil
	}
	state := m.liveEditors[ed.Primary()]
	if state == nil {
		return nil
	}
	generation, key := m.workspaceSwitchGen.Load(), editorDocumentKey(ed)
	menu := &ui.ContextMenuState{Title: "Encoding: " + ed.DocumentState().Encoding.Label()}
	section := ui.ContextMenuSection{SectionLabel: "Save text with encoding"}
	for _, encoding := range textencoding.Choices() {
		section.Items = append(section.Items, ui.ContextMenuItem{
			Label: "Save as " + encoding.Label(),
			Action: func() {
				if generation != m.workspaceSwitchGen.Load() || !ed.IsOpen() || editorDocumentKey(ed) != key || m.liveEditors[ed.Primary()] != state {
					return
				}
				if state.busy || state.save || state.reload || state.uncertain != nil || state.err != nil {
					m.Feedback.NotifyInfo("Wait for the current file operation to finish.")
					return
				}
				state.convertEncoding = &encoding
				state.save, state.reason, state.format = true, workspace.DocumentSaveManual, false
			},
		})
	}
	menu.Sections = []ui.ContextMenuSection{section}
	menu.Sections = append(menu.Sections, ui.ContextMenuSection{SectionLabel: "Read saved bytes", Items: []ui.ContextMenuItem{{
		Label: "Reload from disk", Action: func() {
			if generation == m.workspaceSwitchGen.Load() && ed.IsOpen() && editorDocumentKey(ed) == key && m.liveEditors[ed.Primary()] == state {
				m.confirmEditorReload(ed)
			}
		},
	}}})
	return m.openHeaderPopup(menu)
}

const editorReloadOverlay ui.OverlayID = "editor-reload"

type editorReloadPrompt struct {
	editor   *editor.Editor
	activate func(int) tea.Cmd
}

func (m *App) confirmEditorReload(ed *editor.Editor) {
	state := m.liveEditors[ed.Primary()]
	ds := ed.DocumentState()
	if state == nil || state.busy || state.save || state.reload || state.uncertain != nil || ds.Conflict != nil {
		m.Feedback.NotifyInfo("Resolve the current file operation before reloading.")
		return
	}
	revision, generation := ed.Revision(), m.workspaceSwitchGen.Load()
	p := &editorReloadPrompt{editor: ed}
	p.activate = func(index int) tea.Cmd {
		if index < 0 {
			return nil
		}
		if m.editorReload != p {
			return nil
		}
		m.overlayHost.Close(editorReloadOverlay)
		if index == 0 && ed.IsOpen() && ed.Revision() == revision && generation == m.workspaceSwitchGen.Load() && m.liveEditors[ed.Primary()] == state {
			state.reload, state.err, state.due = true, nil, time.Time{}
			state.closeAfter = false
		}
		return nil
	}
	m.editorReload = p
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Reload File", Question: "Read the saved file again?",
		Detail:       "The file's byte order mark selects its encoding.\nUnsaved text is replaced. Undo can restore it.",
		Buttons:      []ui.ConfirmButtonSpec{{Label: "Reload", ActionID: ui.ConfirmActionDiscard}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}},
		FocusedIndex: 1, Width: 60, Severity: ui.ConfirmPromptWarning,
	})
	m.openConfirmModal(editorReloadOverlay, frame, p.activate, func() {
		if m.editorReload == p {
			m.editorReload = nil
		}
	})
}
