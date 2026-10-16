package viewmodel

import (
	"time"

	"charm.land/bubbletea/v2"

	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

// showDirtyClosePrompt opens the unsaved-changes modal. Default focus is
// "Cancel" (index 2) so an accidental Enter keeps the user's work safe.
func (m *App) showDirtyClosePrompt(path string) {
	if m.windowManager == nil {
		return
	}
	tab, ok := m.EditorState.Tabs[path]
	if !ok {
		return
	}
	// BaseTitle: the tab's ● dirty marker must not leak into dialog text;
	// the dialog itself communicates state.
	filename := tab.Editor.BaseTitle()
	if m.ws.Documents() != nil {
		m.ws.Documents().Cancel(tab.Editor.BufferID())
	}
	m.DirtyPromptChoice = 2
	frame := ui.BuildDirtyCloseFrame(m.Theme, filename, m.DirtyPromptChoice)
	if m.liveEditors[tab.Editor] != nil {
		frame = ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
			Title: "Shared unsaved changes", Question: "Save to disk or close only this view?",
			Detail:   "Closing keeps shared unsaved text for other views. Pending edits are published before closing; no shared work is discarded.",
			Severity: ui.ConfirmPromptWarning, Width: 68, FocusedIndex: m.DirtyPromptChoice,
			Buttons: []ui.ConfirmButtonSpec{{Label: "Save", ActionID: ui.ConfirmActionSave}, {Label: "Close view", ActionID: ui.ConfirmActionDiscard}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}},
		})
	}
	w, h := frame.Measure(80, 40)

	fw := wm.NewFloatingWindow(string(ui.OverlayDirtyClose), "", frame, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(true), // drag by frame title row
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayDirtyClose, fw, ui.ModalDialogPolicy(), func() {
		m.DirtyPromptPath = ""
	})
	m.DirtyPromptPath = path
}

// dirtyPromptSave is called when the user picks "Save" in the
// dirty-close prompt. Saves the file (if it has a path) and then
// closes the tab. If save fails, the popup stays open so the user
// can pick "Don't Save" or "Cancel".
func (m *App) dirtyPromptSave() tea.Cmd {
	path := m.DirtyPromptPath
	if path == "" {
		return nil
	}
	tab, ok := m.EditorState.Tabs[path]
	if !ok {
		// Tab was already closed somehow; just dismiss.
		m.overlayHost.Close(ui.OverlayDirtyClose)
		return nil
	}
	return m.requestEditorSave(tab.Editor, workspace.DocumentSaveManual, true)
}

// dirtyPromptDiscard is called when the user picks "Don't Save" in
// the dirty-close prompt. Closes the tab without saving.
func (m *App) dirtyPromptDiscard() {
	path := m.DirtyPromptPath
	m.overlayHost.Close(ui.OverlayDirtyClose)
	m.openNextAgentPrompt()
	if path != "" {
		m.closeEditorTabForce(path)
		// The user resolved this buffer's close decision, so a bulk close
		// waiting on it may proceed. A deferred close (writer still in flight)
		// resumes the walk from the save acknowledgement instead.
		if _, open := m.EditorState.Tabs[path]; !open {
			m.continuePendingTabClose()
		}
	}
}

// dirtyPromptCancel is called when the user picks "Cancel" in
// the dirty-close prompt. Dismisses the popup and keeps the tab open.
func (m *App) dirtyPromptCancel() {
	path := m.DirtyPromptPath
	if tab := m.EditorState.Tabs[path]; tab != nil && tab.Editor != nil {
		if state := m.liveEditors[tab.Editor]; state != nil {
			state.closeAfter, state.closeView = false, false
		}
		if state := m.editorSaves[tab.Editor.BufferID()]; state != nil {
			state.closeAfter = false
		}
	}
	m.overlayHost.Close(ui.OverlayDirtyClose)
	m.openNextAgentPrompt()
	// Cancelling a close preserves the document, so the deadline the prompt
	// suspended has to come back: otherwise a dirty buffer would sit with no
	// scheduled save until the next keystroke.
	m.rearmAfterDelayAutosave(path)
	m.abortPendingTabClose()
}

// rearmAfterDelayAutosave restores the After Delay deadline for one dirty
// buffer after a close decision stopped suppressing scheduling.
func (m *App) rearmAfterDelayAutosave(path string) {
	if m.editorSettings.AutoSave != types.AutoSaveAfterDelay || m.ws.Documents() == nil || path == "" {
		return
	}
	tab := m.EditorState.Tabs[path]
	if tab == nil || tab.Editor == nil || !tab.Editor.Modified() {
		return
	}
	if m.DirtyPromptPath == path {
		return
	}
	id := tab.Editor.BufferID()
	if state := m.editorSaves[id]; state != nil && (state.err != nil || state.discard || state.closeAfter) {
		return
	}
	m.ws.Documents().Schedule(id, path, tab.Editor.Revision(), time.Duration(m.editorSettings.AutoSaveDelay)*time.Millisecond)
}

// dirtyPromptCount is the number of buttons in the dirty-close dialog.
const dirtyPromptCount = 3

// handleDirtyPromptKey routes keypresses when the dirty-close dialog
// is open. Arrows/Tab move button focus in place (no tree rebuild);
// Enter activates the focused button; S/D/C are direct shortcuts;
// Esc cancels.
func (m *App) handleDirtyPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.DirtyPromptPath == "" {
		return nil
	}
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		return nil
	}
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		if n := ui.MoveConfirmFocus(cw, -1, dirtyPromptCount); n >= 0 {
			m.DirtyPromptChoice = n
		}
		return nil
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		if n := ui.MoveConfirmFocus(cw, delta, dirtyPromptCount); n >= 0 {
			m.DirtyPromptChoice = n
		}
		return nil
	case tea.KeyEnter:
		return m.activateDirtyPromptButton(m.DirtyPromptChoice)
	case tea.KeyEsc:
		m.dirtyPromptCancel()
		return nil
	}
	switch msg.String() {
	case "s", "S":
		m.DirtyPromptChoice = 0
		return m.dirtyPromptSave()
	case "d", "D":
		m.DirtyPromptChoice = 1
		m.dirtyPromptDiscard()
		return nil
	case "c", "C":
		m.DirtyPromptChoice = 2
		m.dirtyPromptCancel()
		return nil
	}
	return nil
}

// confirmFrameButtonAt maps content-local mouse coords to a confirm button index
// using the Host hit tree (WindowFrame product path).
func confirmFrameButtonAt(fw *wm.FloatingWindow, localX, localY int) int {
	if fw == nil {
		return -1
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil || cw.Host() == nil {
		return -1
	}
	// Ensure layout matches painted size.
	ww, hh := fw.Size()
	// Undecorated WindowFrame is painted into the full floating window size.
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	id := cw.Host().HitTest(localX, localY)
	return ui.ConfirmButtonIndexFromID(id)
}
